package driver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GiGurra/bork/internal/check"
)

const derivePlanMemoryLimit = 32 << 20
const derivePlanContentLimit = 64 << 20
const derivePlanEntryLimit = 16 << 20

type derivePlanCache struct {
	mu        sync.Mutex
	entries   map[string][]byte
	order     []string
	bytes     int
	root      string
	namespace [sha256.Size]byte
}

func newDerivePlanCache() *derivePlanCache {
	cache := &derivePlanCache{entries: map[string][]byte{}}
	root, err := cacheRootDir()
	if err != nil {
		return cache
	}
	namespace, err := compilerArtifactNamespace("shape-plan-v1", "normalized-syntax-v1")
	if err == nil {
		cache.root, cache.namespace = root, namespace
	}
	return cache
}

func validDerivePlanKey(key string) bool {
	if len(key) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(key)
	return err == nil && len(decoded) == sha256.Size
}

func (cache *derivePlanCache) path(key string) string {
	return filepath.Join("plans", "v1", hex.EncodeToString(cache.namespace[:]), key+".json")
}

func (cache *derivePlanCache) retain(key string, data []byte) {
	if old := cache.entries[key]; old != nil {
		cache.bytes -= len(old)
		for i, present := range cache.order {
			if present == key {
				cache.order = append(cache.order[:i], cache.order[i+1:]...)
				break
			}
		}
	}
	cache.entries[key] = bytes.Clone(data)
	cache.bytes += len(data)
	cache.order = append(cache.order, key)
	for cache.bytes > derivePlanMemoryLimit {
		oldest := cache.order[0]
		cache.order = cache.order[1:]
		cache.bytes -= len(cache.entries[oldest])
		delete(cache.entries, oldest)
	}
}

func (cache *derivePlanCache) DerivePlanGet(key string) []byte {
	if !validDerivePlanKey(key) || cacheDisabled() {
		return nil
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if data := cache.entries[key]; data != nil {
		owned := bytes.Clone(data)
		cache.retain(key, data)
		return owned
	}
	if cache.root == "" {
		return nil
	}
	root, err := os.OpenRoot(cache.root)
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(cache.path(key))
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > derivePlanEntryLimit {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(file, derivePlanEntryLimit+1))
	if err != nil || len(data) > derivePlanEntryLimit {
		return nil
	}
	cache.retain(key, data)
	return bytes.Clone(data)
}

func (cache *derivePlanCache) DerivePlanPut(key string, data []byte) {
	if !validDerivePlanKey(key) || len(data) == 0 || len(data) > derivePlanEntryLimit || cacheDisabled() {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.retain(key, data)
	if cache.root == "" || os.MkdirAll(cache.root, 0700) != nil {
		return
	}
	root, err := os.OpenRoot(cache.root)
	if err != nil {
		return
	}
	defer func() { _ = root.Close() }()
	if ensureStageDirectory(root, "locks/shape-plans", 0700) != nil {
		return
	}
	store := cacheStore{root: cache.root, namespace: cache.namespace}
	slot, err := store.lock(root, filepath.Join("locks", "shape-plans", key[:2]+".lock"))
	if err != nil {
		return
	}
	defer func() { _ = slot.Close() }()
	mutation, err := store.lock(root, "mutation.lock")
	if err != nil {
		return
	}
	defer func() { _ = mutation.Close() }()
	path := cache.path(key)
	if ensureStageDirectory(root, filepath.Dir(path), 0700) != nil {
		return
	}
	temporary := path + ".tmp"
	_ = root.Remove(temporary)
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer func() { _ = root.Remove(temporary) }()
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil || root.Rename(temporary, path) != nil {
		return
	}
	trimDerivePlans(root)
}

func trimDerivePlans(root *os.Root) {
	type entry struct {
		path string
		size int64
		age  time.Time
	}
	var entries []entry
	var total int64
	_ = fs.WalkDir(root.FS(), "plans/v1", func(path string, item fs.DirEntry, err error) error {
		if err != nil || !item.Type().IsRegular() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		info, err := item.Info()
		if err == nil {
			entries = append(entries, entry{path: path, size: info.Size(), age: info.ModTime()})
			total += info.Size()
		}
		return nil
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].age.Before(entries[j].age) })
	for _, entry := range entries {
		if total <= derivePlanContentLimit {
			break
		}
		if root.Remove(entry.path) == nil {
			total -= entry.size
		}
	}
}

var sharedDerivePlanCache = sync.OnceValue(newDerivePlanCache)

func (gp goPackages) DerivePlanStore() check.DerivePlanStore {
	if cacheDisabled() || gp.context != nil && (gp.context.driver != "off" || gp.context.driverErr != nil || gp.context.err != nil) {
		return nil
	}
	if gp.usage != nil && gp.usage.plans != nil {
		return gp.usage.plans
	}
	return sharedDerivePlanCache()
}
