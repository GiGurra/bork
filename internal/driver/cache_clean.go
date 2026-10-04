package driver

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type CacheCleanReport struct {
	Results, Stages, Temporaries, Dependencies int
	Bytes                                      int64
}

// Clean removes compiler-owned artifacts, preserving project outputs, Go's
// cache and permanent coordination files. Waiting for active clients is cancellable.
func Clean(ctx context.Context, all bool) (CacheCleanReport, error) {
	directory, err := cacheRootDir()
	if err != nil {
		return CacheCleanReport{}, err
	}
	if _, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
		return CacheCleanReport{}, nil
	} else if err != nil {
		return CacheCleanReport{}, err
	}
	var namespace [sha256.Size]byte
	if !all && (runtime.GOOS == "linux" || runtime.GOOS == "darwin") {
		namespace, err = compilerArtifactNamespace(strconv.Itoa(cacheArtifactSchema), cacheArtifactLayout)
		if err != nil {
			return CacheCleanReport{}, fmt.Errorf("identify current compiler cache: %w; use clean --all for every namespace", err)
		}
	}
	return cleanCache(ctx, directory, namespace, all)
}

type cacheCleanEntry struct {
	path, slot, layer string
	namespace, key    [sha256.Size]byte
}

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
	// Drain maintenance before selecting detached trash. Ordering matches the
	// worker admission -> trim -> SLOT -> MUTATION protocol.
	for _, name := range []string{"trim-admission.lock", "trim.lock"} {
		file, err := store.lockWithContext(ctx, root, name)
		if err != nil {
			return report, err
		}
		admissions = append(admissions, file)
	}
	mutation, err := store.lockWithContext(ctx, root, "mutation.lock")
	if err != nil {
		return report, err
	}
	selected, err := selectCleanEntries(ctx, root, namespace, all)
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
		case "index":
		case "script-deps":
			report.Dependencies++
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

// Explicit cleanup may enumerate; ordinary lookup/publication never does.
const cleanDirectoryBatchSize = 256

func cleanDirectoryEntries(ctx context.Context, root *os.Root, path string) ([]os.DirEntry, error) {
	return cleanDirectoryEntriesWithBatch(ctx, root, path, cleanDirectoryBatchSize)
}

func cleanDirectoryEntriesWithBatch(ctx context.Context, root *os.Root, path string, batchSize int) ([]os.DirEntry, error) {
	if err := validateStageDirectory(root, path); err != nil {
		return nil, err
	}
	file, err := root.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var result []os.DirEntry
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := file.ReadDir(batchSize)
		result = append(result, batch...)
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
	}
}
func cacheShard(name string) bool {
	return len(name) == 2 && strings.IndexFunc(name, func(r rune) bool { return !strings.ContainsRune("0123456789abcdef", r) }) < 0
}
func selectCleanEntries(ctx context.Context, root *os.Root, namespace [sha256.Size]byte, all bool) ([]cacheCleanEntry, error) {
	var entries []cacheCleanEntry
	addResults := func(dir string, digest [sha256.Size]byte, legacy bool) error {
		dirs := []string{dir}
		if !legacy {
			dirs = nil
			shards, err := cleanDirectoryEntries(ctx, root, dir)
			if err != nil {
				return err
			}
			for _, shard := range shards {
				if cacheShard(shard.Name()) && shard.IsDir() {
					dirs = append(dirs, filepath.Join(dir, shard.Name()))
				}
			}
		}
		for _, directory := range dirs {
			files, err := cleanDirectoryEntries(ctx, root, directory)
			if err != nil {
				return err
			}
			for _, file := range files {
				path := filepath.Join(directory, file.Name())
				if strings.HasPrefix(file.Name(), ".tmp-") {
					entries = append(entries, cacheCleanEntry{path: path, layer: "temporary"})
					continue
				}
				key, ok := cacheHexDigest(strings.TrimSuffix(file.Name(), ".json"))
				if !ok || !strings.HasSuffix(file.Name(), ".json") || !legacy && file.Name()[:2] != filepath.Base(directory) {
					continue
				}
				slot := (cacheStore{namespace: digest}).lockName(key)
				if legacy {
					slot = legacyResultLockName(digest, key)
				}
				entries = append(entries, cacheCleanEntry{path: path, slot: slot, layer: "results"})
			}
		}
		return nil
	}
	// New result layout fans out both compiler namespaces and request keys.
	prefixes, err := cleanDirectoryEntries(ctx, root, filepath.Join("results", "v2"))
	if err != nil {
		return nil, err
	}
	for _, prefix := range prefixes {
		if !cacheShard(prefix.Name()) || !prefix.IsDir() {
			continue
		}
		dir := filepath.Join("results", "v2", prefix.Name())
		candidates, err := cleanDirectoryEntries(ctx, root, dir)
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			digest, ok := cacheHexDigest(candidate.Name())
			if !ok || !candidate.IsDir() || candidate.Name()[:2] != prefix.Name() || !all && (namespace == ([sha256.Size]byte{}) || digest != namespace) {
				continue
			}
			if err := addResults(filepath.Join(dir, candidate.Name()), digest, false); err != nil {
				return nil, err
			}
		}
	}
	if all {
		candidates, err := cleanDirectoryEntries(ctx, root, filepath.Join("results", "v1"))
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			digest, ok := cacheHexDigest(candidate.Name())
			if ok && candidate.IsDir() {
				if err := addResults(filepath.Join("results", "v1", candidate.Name()), digest, true); err != nil {
					return nil, err
				}
			}
		}
	}
	versions := []string{"v3"}
	if all {
		versions = append(versions, "v2", "v1")
	}
	for _, version := range versions {
		base := filepath.Join("stage", version)
		candidates, err := cleanDirectoryEntries(ctx, root, base)
		if err != nil {
			return nil, err
		}
		dirs := []string{base}
		if version == "v3" {
			dirs = nil
			for _, candidate := range candidates {
				if cacheShard(candidate.Name()) && candidate.IsDir() {
					dirs = append(dirs, filepath.Join(base, candidate.Name()))
				}
			}
		}
		for _, dir := range dirs {
			stages := candidates
			if version == "v3" {
				stages, err = cleanDirectoryEntries(ctx, root, dir)
				if err != nil {
					return nil, err
				}
			}
			for _, stage := range stages {
				if _, ok := cacheHexDigest(stage.Name()); !ok || version == "v3" && stage.Name()[:2] != filepath.Base(dir) {
					continue
				}
				hash := sha256.Sum256([]byte(stage.Name()))
				slot := filepath.Join("locks", "stage-v3", fmt.Sprintf("%02x.lock", hash[0]))
				if version == "v2" {
					slot = filepath.Join("locks", "stage-v2", fmt.Sprintf("%02x.lock", hash[0]))
				}
				if version == "v1" {
					slot = filepath.Join("stage", "v1", "locks", fmt.Sprintf("%02x.lock", hash[0]))
				}
				entries = append(entries, cacheCleanEntry{path: filepath.Join(dir, stage.Name()), slot: slot, layer: "stage"})
			}
		}
	}
	jobs, err := cleanDirectoryEntries(ctx, root, filepath.Join("jobs", "v1"))
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		if strings.HasPrefix(job.Name(), ".job-") {
			entries = append(entries, cacheCleanEntry{path: filepath.Join("jobs", "v1", job.Name()), layer: "temporary"})
		}
	}
	shards, err := cleanDirectoryEntries(ctx, root, filepath.Join("indexes", "v1"))
	if err != nil {
		return nil, err
	}
	for _, shard := range shards {
		if !cacheShard(shard.Name()) || !shard.IsDir() {
			continue
		}
		dir := filepath.Join("indexes", "v1", shard.Name())
		indexes, err := cleanDirectoryEntries(ctx, root, dir)
		if err != nil {
			return nil, err
		}
		for _, index := range indexes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if len(index.Name()) > 74 && index.Name()[:2] == shard.Name() && strings.HasPrefix(index.Name()[64:], ".json.tmp-") {
				if _, ok := cacheHexDigest(index.Name()[:64]); ok {
					path := filepath.Join(dir, index.Name())
					if !all {
						digest, err := readCacheNamespace(root, path)
						if err != nil || namespace == ([sha256.Size]byte{}) || digest != namespace {
							continue
						}
					}
					entries = append(entries, cacheCleanEntry{path: path, layer: "temporary"})
					continue
				}
			}

			name := strings.TrimSuffix(index.Name(), ".json")
			key, ok := cacheHexDigest(name)
			if !ok || !strings.HasSuffix(index.Name(), ".json") || name[:2] != shard.Name() {
				continue
			}
			selected := cacheCleanEntry{path: filepath.Join(dir, index.Name()), layer: "index", key: key}
			if !all {
				digest, err := readCacheIndex(root, key)
				if err != nil || namespace == ([sha256.Size]byte{}) || digest != namespace {
					continue
				}
				selected.namespace = namespace
			}
			entries = append(entries, selected)
		}
	}

	trashShards, err := cleanDirectoryEntries(ctx, root, "trash/v1")
	if err != nil {
		return nil, err
	}
	for _, shard := range trashShards {
		if !cacheShard(shard.Name()) || !shard.IsDir() {
			continue
		}
		dir := filepath.Join("trash", "v1", shard.Name())
		garbage, err := cleanDirectoryEntries(ctx, root, dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range garbage {
			if _, ok := cacheHexDigest(entry.Name()); ok && entry.Name()[:2] == shard.Name() {
				entries = append(entries, cacheCleanEntry{path: filepath.Join(dir, entry.Name()), layer: "temporary"})
			}
		}
	}

	if all {
		deps, err := cleanDirectoryEntries(ctx, root, filepath.Join("scripts", "deps"))
		if err != nil {
			return nil, err
		}
		for _, entry := range deps {
			if _, ok := cacheHexDigest(entry.Name()); ok && entry.IsDir() {
				entries = append(entries, cacheCleanEntry{path: filepath.Join("scripts", "deps", entry.Name()), slot: filepath.Join("locks", "script-deps", entry.Name()+".lock"), layer: "script-deps"})
			}
		}
	}
	return entries, nil
}
func legacyResultLockName(namespace, key [sha256.Size]byte) string {
	digest := sha256.New()
	_, _ = digest.Write(namespace[:])
	_, _ = digest.Write(key[:])
	return filepath.Join("locks", "results-v1", fmt.Sprintf("%02x.lock", digest.Sum(nil)[0]))
}
func removeCleanEntry(ctx context.Context, root *os.Root, entry cacheCleanEntry) (int64, error) {
	if err := validateStageDirectory(root, filepath.Dir(entry.path)); err != nil {
		return 0, err
	}
	if _, err := root.Lstat(entry.path); err != nil {
		return 0, err
	}
	if entry.layer == "index" && entry.namespace != ([sha256.Size]byte{}) {
		actual, err := readCacheIndex(root, entry.key)
		if err != nil || actual != entry.namespace {
			return 0, os.ErrNotExist
		}
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
	if entry.layer != "stage" && entry.layer != "script-deps" {
		_ = root.Remove(filepath.Dir(entry.path))
	}
	return bytes, nil
}
func cacheRemovalBytes(ctx context.Context, root *os.Root, path string) (int64, error) {
	return cacheRemovalBytesWithBatch(ctx, root, path, cleanDirectoryBatchSize)
}

func cacheRemovalBytesWithBatch(ctx context.Context, root *os.Root, path string, batchSize int) (int64, error) {
	pending := []string{path}
	var bytes int64
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		path := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		info, err := root.Lstat(path)
		if err != nil {
			return 0, err
		}
		if !info.IsDir() {
			bytes += info.Size()
			continue
		}
		names, err := cleanDirectoryEntriesWithBatch(ctx, root, path, batchSize)
		if err != nil {
			return 0, err
		}
		for _, name := range names {
			pending = append(pending, filepath.Join(path, name.Name()))
		}
	}
	return bytes, nil
}
