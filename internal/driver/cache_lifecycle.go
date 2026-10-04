package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const cacheResultByteLimit int64 = 256 << 20
const cacheEntryLimit = 1024
const cacheInventoryLimit = 4096

// Lifecycle limits count all compiler namespaces. The smaller test policy is
// private; no user-controlled policy can make an artifact bypass decoder bounds.
type cacheLimits struct {
	stageNodes  int
	stageBytes  int64
	resultBytes int64
	entries     int
}

func (s cacheStore) limits() cacheLimits {
	if s.policy != nil {
		out := *s.policy
		if out.stageNodes <= 0 {
			out.stageNodes = goStageInventoryLimit
		}
		return out
	}
	return cacheLimits{stageNodes: goStageInventoryLimit, stageBytes: cacheResultByteLimit, resultBytes: cacheResultByteLimit, entries: cacheEntryLimit}
}

type cacheResultEntry struct {
	path           string
	namespace, key [sha256.Size]byte
	bytes          int64
	used           time.Time
}

// resultInventory runs under MUTATION. It counts recognizable artifacts even
// when corrupt, and removes abandoned temporaries: no live writer can own them
// while this lock is held. Unrecognized paths are never deleted automatically.
func resultInventory(root *os.Root) ([]cacheResultEntry, error) {
	namespaces, err := cacheDirectoryEntries(root, filepath.Join("results", "v1"), cacheInventoryLimit)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	remaining := cacheInventoryLimit - len(namespaces)
	var entries []cacheResultEntry
	for _, namespace := range namespaces {
		digest, ok := cacheHexDigest(namespace.Name())
		if !ok || !namespace.IsDir() {
			continue
		}
		dir := filepath.Join("results", "v1", namespace.Name())
		names, err := cacheDirectoryEntries(root, dir, remaining)
		if err != nil {
			return nil, err
		}
		remaining -= len(names)
		for _, name := range names {
			path := filepath.Join(dir, name.Name())
			if strings.HasPrefix(name.Name(), ".tmp-") {
				info, err := root.Lstat(path)
				if err != nil {
					return nil, err
				}
				if info.Mode().IsRegular() {
					if err := root.Remove(path); err != nil {
						return nil, err
					}
				}
				continue
			}
			key, ok := cacheHexDigest(strings.TrimSuffix(name.Name(), ".json"))
			if !ok || !strings.HasSuffix(name.Name(), ".json") {
				continue
			}
			info, err := root.Lstat(path)
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() {
				return nil, errInvalidCacheArtifact
			}
			entries = append(entries, cacheResultEntry{path: path, namespace: digest, key: key, bytes: info.Size(), used: info.ModTime()})
		}
	}
	slices.SortFunc(entries, func(a, b cacheResultEntry) int {
		if a.used.Before(b.used) {
			return -1
		}
		if a.used.After(b.used) {
			return 1
		}
		return strings.Compare(a.path, b.path)
	})
	return entries, nil
}
func cacheDirectoryEntries(root *os.Root, path string, limit int) ([]os.DirEntry, error) {
	if limit < 0 {
		return nil, errCacheArtifactBudget
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	entries, err := file.ReadDir(limit + 1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > limit {
		return nil, errCacheArtifactBudget
	}
	return entries, nil
}
func cacheHexDigest(value string) ([sha256.Size]byte, bool) {
	var out [sha256.Size]byte
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != len(out) || value != strings.ToLower(value) {
		return out, false
	}
	copy(out[:], raw)
	return out, true
}

// reserveResult runs while the publisher holds its result SLOT then MUTATION.
// Eviction acquires other slots only nonblocking: a collision/busy reader means
// skip that entry. Account the full new temporary alongside the old generation.
func (s cacheStore) reserveResult(root *os.Root, key [sha256.Size]byte, bytes int64) error {
	limits := s.limits()
	if bytes < 0 || bytes > limits.resultBytes || limits.entries < 1 {
		return errCacheArtifactBudget
	}
	entries, err := resultInventory(root)
	if err != nil {
		return err
	}
	var used int64
	count := len(entries)
	added := 1
	for _, entry := range entries {
		used += entry.bytes
		if entry.namespace == s.namespace && entry.key == key {
			added = 0
		}
	}
	for _, entry := range entries {
		if used+bytes <= limits.resultBytes && count+added <= limits.entries {
			return nil
		}
		if entry.namespace == s.namespace && entry.key == key {
			continue
		}
		other := cacheStore{namespace: entry.namespace}
		if other.lockName(entry.key) == s.lockName(key) {
			continue
		}
		slot, err := s.tryLock(root, other.lockName(entry.key))
		if err != nil {
			continue
		}
		err = root.Remove(entry.path)
		_ = slot.Close()
		if err != nil {
			return err
		}
		used -= entry.bytes
		count--
		// Empty namespace directories carry no identity and are recreated by writers.
		_ = root.Remove(filepath.Dir(entry.path))
	}
	if used+bytes > limits.resultBytes || count+added > limits.entries {
		return errCacheArtifactBudget
	}
	return nil
}
func (s cacheStore) tryLock(root *os.Root, name string) (*os.File, error) {
	file, err := openCacheFile(root, name, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := tryLockCacheFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// touch happens only after full receipt validation. It never delays serving a
// valid hit behind a publisher: unavailable/busy lifecycle locks skip the hint.
func (s cacheStore) touch(key [sha256.Size]byte) {
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return
	}
	defer func() { _ = root.Close() }()
	slot, err := s.tryLock(root, s.lockName(key))
	if err != nil {
		return
	}
	defer func() { _ = slot.Close() }()
	mutation, err := s.tryLock(root, "mutation.lock")
	if err != nil {
		return
	}
	defer func() { _ = mutation.Close() }()
	path := filepath.Join(s.directory(), cacheArtifactFilename(key))
	info, err := root.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	now := time.Now()
	_ = root.Chtimes(path, now, now)
}
