package driver

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type CacheCleanReport struct {
	Results, Stages, Temporaries int
	Bytes                        int64
}

// Clean removes compiler-owned artifacts, preserving project outputs, Go's
// cache and permanent coordination files. Waiting for active clients is cancellable.
func Clean(ctx context.Context, all bool) (CacheCleanReport, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return CacheCleanReport{}, err
	}
	directory := filepath.Join(base, "bork")
	if _, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
		return CacheCleanReport{}, nil
	} else if err != nil {
		return CacheCleanReport{}, err
	}
	var namespace [sha256.Size]byte
	if !all && runtime.GOOS == "linux" {
		namespace, err = compilerArtifactNamespace(strconv.Itoa(cacheArtifactSchema), cacheArtifactLayout)
		if err != nil {
			return CacheCleanReport{}, fmt.Errorf("identify current compiler cache: %w; use clean --all for every namespace", err)
		}
	}
	return cleanCache(ctx, directory, namespace, all)
}

type cacheCleanEntry struct{ path, slot, layer string }

func cleanCache(ctx context.Context, directory string, namespace [sha256.Size]byte, all bool) (CacheCleanReport, error) {
	var report CacheCleanReport
	root, err := os.OpenRoot(directory)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	defer func() { _ = root.Close() }()
	store := cacheStore{root: directory, namespace: namespace}
	// Drain bounded detached publishers before selecting identities. Holding all
	// admission locks prevents an already queued job from repopulating the selection.
	if err := ensureStageDirectory(root, filepath.Join("locks", "publish-v1"), 0700); err != nil {
		return report, err
	}
	var admissions []*os.File
	defer func() {
		for _, file := range admissions {
			_ = file.Close()
		}
	}()
	for index := 0; index < cachePublisherSlots; index++ {
		file, err := store.lockWithContext(ctx, root, filepath.Join("locks", "publish-v1", fmt.Sprintf("%02x.lock", index)))
		if err != nil {
			return report, err
		}
		admissions = append(admissions, file)
	}
	mutation, err := store.lockWithContext(ctx, root, "mutation.lock")
	if err != nil {
		return report, err
	}
	selected, err := selectCleanEntries(root, namespace, all)
	_ = mutation.Close()
	if err != nil {
		return report, err
	}
	for _, entry := range selected {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		var slot *os.File
		if entry.slot != "" {
			if err := ensureStageDirectory(root, filepath.Dir(entry.slot), 0700); err != nil {
				return report, err
			}
			slot, err = store.lockWithContext(ctx, root, entry.slot)
			if err != nil {
				return report, err
			}
		}
		mutation, err = store.lockWithContext(ctx, root, "mutation.lock")
		if err != nil {
			if slot != nil {
				_ = slot.Close()
			}
			return report, err
		}
		bytes, removeErr := removeCleanEntry(ctx, root, entry)
		_ = mutation.Close()
		if slot != nil {
			_ = slot.Close()
		}
		if errors.Is(removeErr, os.ErrNotExist) {
			continue
		}
		if removeErr != nil {
			return report, removeErr
		}
		report.Bytes += bytes
		switch entry.layer {
		case "results":
			report.Results++
		case "stage":
			report.Stages++
		default:
			report.Temporaries++
		}
	}
	return report, nil
}
func (s cacheStore) lockWithContext(ctx context.Context, root *os.Root, name string) (*os.File, error) {
	if err := validateStageDirectory(root, filepath.Dir(name)); err != nil {
		return nil, err
	}
	file, err := openCacheFile(root, name, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		err := tryLockCacheFile(file)
		if err == nil {
			return file, nil
		}
		if !cacheLockBusy(err) {
			_ = file.Close()
			return nil, err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
func selectCleanEntries(root *os.Root, namespace [sha256.Size]byte, all bool) ([]cacheCleanEntry, error) {
	if err := validateStageDirectory(root, filepath.Join("results", "v1")); err != nil {
		return nil, err
	}
	namespaces, err := cacheDirectoryEntries(root, filepath.Join("results", "v1"), cacheInventoryLimit)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var entries []cacheCleanEntry
	remaining := cacheInventoryLimit - len(namespaces)
	for _, candidate := range namespaces {
		digest, ok := cacheHexDigest(candidate.Name())
		if !ok || !candidate.IsDir() || !all && (digest != namespace || namespace == ([sha256.Size]byte{})) {
			continue
		}
		dir := filepath.Join("results", "v1", candidate.Name())
		if err := validateStageDirectory(root, dir); err != nil {
			return nil, err
		}
		files, err := cacheDirectoryEntries(root, dir, remaining)
		if err != nil {
			return nil, err
		}
		remaining -= len(files)
		for _, file := range files {
			path := filepath.Join(dir, file.Name())
			if strings.HasPrefix(file.Name(), ".tmp-") {
				entries = append(entries, cacheCleanEntry{path: path, layer: "temporary"})
				continue
			}
			key, ok := cacheHexDigest(strings.TrimSuffix(file.Name(), ".json"))
			if !ok || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			entries = append(entries, cacheCleanEntry{path: path, slot: (cacheStore{namespace: digest}).lockName(key), layer: "results"})
		}
	}
	versions := []string{"v2"}
	if all {
		versions = append(versions, "v1")
	}
	for _, version := range versions {
		dir := filepath.Join("stage", version)
		if err := validateStageDirectory(root, dir); err != nil {
			return nil, err
		}
		stages, err := cacheDirectoryEntries(root, dir, cacheInventoryLimit)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, stage := range stages {
			if _, ok := cacheHexDigest(stage.Name()); !ok {
				continue
			}
			slot := strings.TrimPrefix(goStageLockPath(root.Name(), stage.Name()), root.Name()+string(filepath.Separator))
			if version == "v1" {
				hash := sha256.Sum256([]byte(stage.Name()))
				slot = filepath.Join("stage", "v1", "locks", fmt.Sprintf("%02x.lock", hash[0]))
			}
			entries = append(entries, cacheCleanEntry{path: filepath.Join(dir, stage.Name()), slot: slot, layer: "stage"})
		}
	}
	if err := validateStageDirectory(root, filepath.Join("jobs", "v1")); err != nil {
		return nil, err
	}
	jobs, err := cacheDirectoryEntries(root, filepath.Join("jobs", "v1"), cacheInventoryLimit)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, job := range jobs {
		if strings.HasPrefix(job.Name(), ".job-") {
			entries = append(entries, cacheCleanEntry{path: filepath.Join("jobs", "v1", job.Name()), layer: "temporary"})
		}
	}
	return entries, nil
}
func removeCleanEntry(ctx context.Context, root *os.Root, entry cacheCleanEntry) (int64, error) {
	if err := validateStageDirectory(root, filepath.Dir(entry.path)); err != nil {
		return 0, err
	}
	if _, err := root.Lstat(entry.path); err != nil {
		return 0, err
	}
	bytes, err := cacheRemovalBytes(ctx, root, entry.path)
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := root.RemoveAll(entry.path); err != nil {
		return 0, err
	}
	if entry.layer != "stage" {
		_ = root.Remove(filepath.Dir(entry.path))
	}
	return bytes, nil
}
func cacheRemovalBytes(ctx context.Context, root *os.Root, path string) (int64, error) {
	remaining := goStageInventoryLimit
	pending := []string{path}
	var bytes int64
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		path := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		remaining--
		if remaining < 0 {
			return 0, errCacheArtifactBudget
		}
		info, err := root.Lstat(path)
		if err != nil {
			return 0, err
		}
		if !info.IsDir() {
			bytes += info.Size()
			continue
		}
		names, err := cacheDirectoryEntries(root, path, remaining-len(pending))
		if err != nil {
			return 0, err
		}
		for _, name := range names {
			pending = append(pending, filepath.Join(path, name.Name()))
		}
	}
	return bytes, nil
}
