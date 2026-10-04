package driver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const cacheTrimInterval = 24 * time.Hour
const cacheTrimAge = 5*24*time.Hour + cacheUseInterval
const cacheTrimStateLimit = 4 << 10
const cacheTrimWorkerTime = 5 * time.Second

type cacheTrimState struct {
	Version, Layer, Shard int
	Cutoff, Completed     time.Time
}
type cacheTrimReport struct {
	Steps, Removed int
	Complete       bool
}

var cacheTrimLayers = [...]string{"trash/v1", "results/v2", "stage/v3", "indexes/v1"}

// Whole shards are the portable resume unit. An interrupted pass rereads only
// its current shard, streaming bounded batches, never a materialized inventory.
// The detached worker supplies the hard runtime bound for blocking filesystem
// operations. Progress and timestamps are retention hints, not semantic evidence.
func runCacheTrim(ctx context.Context, base string, now time.Time, shards int) (cacheTrimReport, error) {
	var report cacheTrimReport
	if shards <= 0 || shards > 256 {
		return report, errInvalidCacheArtifact
	}
	ctx, cancel := context.WithTimeout(ctx, cacheTrimWorkerTime)
	defer cancel()
	root, err := os.OpenRoot(base)
	if err != nil {
		return report, err
	}
	defer func() { _ = root.Close() }()
	store := cacheStore{root: base}
	worker, err := store.tryLock(root, "trim.lock")
	if err != nil {
		return report, nil
	}
	defer func() { _ = worker.Close() }()
	state := readCacheTrimState(root)
	if !validCacheTrimState(state) || state.Completed.After(now.Add(cacheTrimInterval)) || state.Cutoff.After(now.Add(-cacheTrimAge)) {
		state = cacheTrimState{Version: 2}
	}
	if !state.Completed.IsZero() && now.Sub(state.Completed) < cacheTrimInterval {
		report.Complete = true
		return report, nil
	}
	if !state.Completed.IsZero() {
		state = cacheTrimState{Version: 2}
	}
	if state.Cutoff.IsZero() {
		state.Cutoff = now.Add(-cacheTrimAge)
	}
	for report.Steps < shards && state.Layer < len(cacheTrimLayers) && ctx.Err() == nil {
		report.Steps++
		err = trimCacheShard(ctx, root, store, state, &report)
		if err != nil {
			break
		}
		state.Shard++
		if state.Shard == 256 {
			state.Layer++
			state.Shard = 0
		}
	}
	if state.Layer == len(cacheTrimLayers) {
		state.Completed = now
		state.Cutoff = time.Time{}
		report.Complete = true
	}
	if saveErr := writeCacheTrimState(root, store, state); saveErr != nil {
		return report, saveErr
	}
	if err != nil {
		return report, err
	}
	return report, ctx.Err()
}
func validCacheTrimState(state cacheTrimState) bool {
	return state.Version == 2 && state.Layer >= 0 && state.Layer <= len(cacheTrimLayers) && state.Shard >= 0 && state.Shard < 256 && (state.Layer < len(cacheTrimLayers) || state.Shard == 0)
}
func readCacheTrimState(root *os.Root) cacheTrimState {
	var state cacheTrimState
	file, err := openCacheFile(root, "trim.json", os.O_RDONLY, 0)
	if err != nil {
		return state
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, cacheTrimStateLimit+1))
	if err != nil || len(data) > cacheTrimStateLimit || json.Unmarshal(data, &state) != nil {
		return cacheTrimState{}
	}
	return state
}
func writeCacheTrimState(root *os.Root, store cacheStore, state cacheTrimState) error {
	data, err := json.Marshal(state)
	if err != nil || len(data) > cacheTrimStateLimit {
		return errInvalidCacheArtifact
	}
	mutation, err := store.tryLock(root, "mutation.lock")
	if err != nil {
		return nil
	}
	defer func() { _ = mutation.Close() }()
	if err := root.Remove("trim.json.next"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := openCacheFile(root, "trim.json.next", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove("trim.json.next") }()
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename("trim.json.next", "trim.json")
}
func streamCacheTrim(ctx context.Context, root *os.Root, path string, visit func(string) error) error {
	if err := validateStageDirectory(root, path); err != nil {
		return nil
	}
	directory, err := root.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return nil
	}
	defer func() { _ = directory.Close() }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, readErr := directory.ReadDir(128)
		for _, entry := range batch {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(entry.Name()); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return nil
		}
	}
}
func trimCacheShard(ctx context.Context, root *os.Root, store cacheStore, state cacheTrimState, report *cacheTrimReport) error {
	shard := fmt.Sprintf("%02x", state.Shard)
	layer := cacheTrimLayers[state.Layer]
	if layer == "results/v2" {
		// Result resume shards are request-key prefixes across compiler namespaces.
		// Compiler prefixes cannot be used as units: one namespace can hold millions
		// of request keys. Namespace discovery is streaming and maintenance-only.
		return streamCacheTrim(ctx, root, layer, func(prefix string) error {
			if !cacheShard(prefix) {
				return nil
			}
			return streamCacheTrim(ctx, root, filepath.Join(layer, prefix), func(namespace string) error {
				digest, ok := cacheHexDigest(namespace)
				if !ok || namespace[:2] != prefix {
					return nil
				}
				return trimCacheLeaf(ctx, root, store, filepath.Join(layer, prefix, namespace, shard), shard, digest, state.Cutoff, report)
			})
		})
	}
	return trimCacheLeaf(ctx, root, store, filepath.Join(layer, shard), shard, [sha256.Size]byte{}, state.Cutoff, report)
}
func trimCacheLeaf(ctx context.Context, root *os.Root, store cacheStore, path, shard string, namespace [sha256.Size]byte, cutoff time.Time, report *cacheTrimReport) error {
	return streamCacheTrim(ctx, root, path, func(name string) error {
		stage := filepath.Dir(path) == "stage/v3"
		trash := filepath.Dir(path) == "trash/v1"
		keyName := name
		if !stage && !trash {
			if filepath.Ext(name) != ".json" {
				return nil
			}
			keyName = name[:len(name)-len(".json")]
		}
		key, ok := cacheHexDigest(keyName)
		if !ok || keyName[:2] != shard {
			return nil
		}
		entry := filepath.Join(path, name)
		if trash {
			info, err := root.Lstat(entry)
			if err != nil || !info.Mode().IsRegular() && !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			// Detached garbage cannot be used by Go or artifact readers. No publication
			// lock is held while removing a potentially large tree.
			if root.RemoveAll(entry) == nil {
				report.Removed++
			}
			return ctx.Err()
		}
		slot := ""
		if stage {
			slot = goStageLockPath("", keyName)
		} else if namespace != ([sha256.Size]byte{}) {
			slot = (cacheStore{namespace: namespace}).lockName(key)
		}
		detached, err := detachCacheTrimEntry(root, store, entry, slot, stage, cutoff)
		if err != nil || detached == "" {
			return nil
		}
		report.Removed++
		if ctx.Err() == nil {
			_ = root.RemoveAll(detached)
		}
		return ctx.Err()
	})
}
func detachCacheTrimEntry(root *os.Root, store cacheStore, path, slotName string, stage bool, cutoff time.Time) (string, error) {
	if err := validateStageDirectory(root, filepath.Dir(path)); err != nil {
		return "", err
	}
	if slotName != "" {
		if err := validateStageDirectory(root, filepath.Dir(slotName)); err != nil {
			return "", err
		}
		slot, err := store.tryLock(root, slotName)
		if err != nil {
			return "", err
		}
		defer func() { _ = slot.Close() }()
	}
	mutation, err := store.tryLock(root, "mutation.lock")
	if err != nil {
		return "", err
	}
	defer func() { _ = mutation.Close() }()
	info, err := root.Lstat(path)
	if err != nil {
		return "", err
	}
	if stage {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errInvalidCacheArtifact
		}
		info, err = root.Lstat(filepath.Join(path, "used"))
		if errors.Is(err, os.ErrNotExist) {
			stamp, ok := cacheTrimStagePublication(root, path)
			if !ok || !stamp.Before(cutoff) || markCacheUse(root, filepath.Join(path, "used"), stamp, true) != nil {
				return "", nil
			}
			info, err = root.Lstat(filepath.Join(path, "used"))
		}
	}
	if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
		return "", nil
	}
	name := fmt.Sprintf("%x", sha256.Sum256([]byte(path+"\x00"+rand.Text())))
	trash := filepath.Join("trash", "v1", name[:2], name)
	if err := ensureStageDirectory(root, filepath.Dir(trash), 0700); err != nil {
		return "", err
	}
	if err := root.Rename(path, trash); err != nil {
		return "", err
	}
	return trash, nil
}
func cacheTrimStagePublication(root *os.Root, path string) (time.Time, bool) {
	var metadata goStageMetadata
	file, err := openCacheFile(root, filepath.Join(path, "metadata.json"), os.O_RDONLY, 0)
	if err != nil {
		return time.Time{}, false
	}
	data, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	_ = file.Close()
	if err != nil || len(data) > 16<<10 || json.Unmarshal(data, &metadata) != nil || metadata.Schema != goStageSchema || !filepath.IsAbs(metadata.Program) || metadata.Mode == "" {
		return time.Time{}, false
	}
	var newest time.Time
	for _, name := range []string{path, filepath.Join(path, "metadata.json"), filepath.Join(path, "tree")} {
		info, err := root.Lstat(name)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return time.Time{}, false
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, true
}
