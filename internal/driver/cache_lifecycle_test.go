package driver

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func lifecycleBody(t *testing.T, original *cacheArtifactBody, index int) *cacheArtifactBody {
	t.Helper()
	body := *original
	body.Request.Path = fmt.Sprintf("%s-%04d", original.Request.Path, index)
	var err error
	body.Key, err = body.Request.key()
	if err != nil {
		t.Fatal(err)
	}
	return &body
}
func distinctLifecycleBodies(t *testing.T, store cacheStore, original *cacheArtifactBody, count int) []*cacheArtifactBody {
	t.Helper()
	slots := map[string]bool{}
	var bodies []*cacheArtifactBody
	for index := 1; len(bodies) < count; index++ {
		body := lifecycleBody(t, original, index)
		slot := store.lockName(body.Key)
		if slots[slot] {
			continue
		}
		slots[slot] = true
		bodies = append(bodies, body)
	}
	return bodies
}
func inventoryForTest(t *testing.T, store cacheStore) []cacheResultEntry {
	t.Helper()
	root, err := os.OpenRoot(store.root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	entries, err := resultInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
func TestCacheResultBudgetAcrossNamespaces(t *testing.T) {
	original, _ := cacheArtifactFixture(t)
	policy := cacheLimits{resultBytes: 1 << 20, entries: 2}
	store := cacheStore{root: t.TempDir(), namespace: original.Namespace, policy: &policy}
	bodies := distinctLifecycleBodies(t, store, original, 3)
	first := bodies[0]
	if err := store.write(first); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(100, 0)
	path := filepath.Join(store.root, store.directory(), cacheArtifactFilename(first.Key))
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	second := bodies[1]
	second.Namespace = sha256.Sum256([]byte("other compiler"))
	other := store
	other.namespace = second.Namespace
	if err := other.write(second); err != nil {
		t.Fatal(err)
	}
	third := bodies[2]
	if err := store.write(third); err != nil {
		t.Fatal(err)
	}
	if store.read(first.Request) != nil {
		t.Fatal("oldest cross-namespace entry retained")
	}
	if other.read(second.Request) == nil || store.read(third.Request) == nil {
		t.Fatal("newer entries lost")
	}
	if len(inventoryForTest(t, store)) != 2 {
		t.Fatal("entry count exceeded")
	}
	// Eviction never unlinks coordination files.
	if _, err := os.Stat(filepath.Join(store.root, store.lockName(first.Key))); err != nil {
		t.Fatal(err)
	}
}
func TestCacheResultByteBudgetAndCrashTemporary(t *testing.T) {
	original, _ := cacheArtifactFixture(t)
	body := lifecycleBody(t, original, 1)
	encoded, err := encodeCacheArtifact(body)
	if err != nil {
		t.Fatal(err)
	}
	policy := cacheLimits{resultBytes: int64(len(encoded)) * 2, entries: 10}
	store := cacheStore{root: t.TempDir(), namespace: body.Namespace, policy: &policy}
	bodies := distinctLifecycleBodies(t, store, original, 6)
	for _, body := range bodies[:4] {
		if err := store.write(body); err != nil {
			t.Fatal(err)
		}
	}
	var size int64
	for _, entry := range inventoryForTest(t, store) {
		size += entry.bytes
	}
	if size > policy.resultBytes {
		t.Fatalf("bytes %d > %d", size, policy.resultBytes)
	}
	temporary := filepath.Join(store.root, store.directory(), ".tmp-abandoned")
	if err := os.WriteFile(temporary, make([]byte, 100), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.write(bodies[4]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(temporary); !os.IsNotExist(err) {
		t.Fatalf("abandoned payload survived: %v", err)
	}
	policy.resultBytes = 1
	if err := store.write(bodies[5]); err == nil {
		t.Fatal("oversized publication accepted")
	}
}
func TestCacheResultBusyEntrySkipsEviction(t *testing.T) {
	original, _ := cacheArtifactFixture(t)
	policy := cacheLimits{resultBytes: 1 << 20, entries: 1}
	store := cacheStore{root: t.TempDir(), namespace: original.Namespace, policy: &policy}
	first := lifecycleBody(t, original, 1)
	second := lifecycleBody(t, original, 2)
	for index := 3; store.lockName(first.Key) == store.lockName(second.Key); index++ {
		second = lifecycleBody(t, original, index)
	}
	if err := store.write(first); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(store.root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	slot, err := store.lock(root, store.lockName(first.Key))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.write(second); err == nil {
		t.Fatal("publication exceeded count with busy entry")
	}
	if store.read(first.Request) == nil {
		t.Fatal("busy artifact removed")
	}
	_ = slot.Close()
	if err := store.write(second); err != nil {
		t.Fatal(err)
	}
	if store.read(first.Request) != nil || store.read(second.Request) == nil {
		t.Fatal("released entry not evicted")
	}
}
func TestCacheResultTouchAndBoundedInventory(t *testing.T) {
	body, _ := cacheArtifactFixture(t)
	store := cacheStore{root: t.TempDir(), namespace: body.Namespace}
	if err := store.write(body); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.root, store.directory(), cacheArtifactFilename(body.Key))
	old := time.Unix(100, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	store.touch(body.Key)
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().After(old) {
		t.Fatalf("touch: %v", err)
	}
	root, err := os.OpenRoot(store.root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	mutation, err := store.lock(root, "mutation.lock")
	if err != nil {
		t.Fatal(err)
	}
	store.touch(body.Key) // Nonblocking: a live publisher must not delay a hit.
	_ = mutation.Close()
	dir := filepath.Join(store.root, "results", "v1")
	for i := 0; i < cacheInventoryLimit; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("unknown-%04d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := resultInventory(root); err == nil {
		t.Fatal("unbounded inventory accepted")
	}
	if err := store.write(body); err == nil {
		t.Fatal("publication in unbounded tree accepted")
	}
}

func TestCacheResultCrossProcessBudget(t *testing.T) {
	if fixture := os.Getenv("BORK_RESULT_BUDGET_BODY"); fixture != "" {
		data, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		var body cacheArtifactBody
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		index, err := strconv.Atoi(os.Getenv("BORK_RESULT_BUDGET_INDEX"))
		if err != nil {
			t.Fatal(err)
		}
		body.Namespace = sha256.Sum256([]byte(fmt.Sprintf("compiler-%d", index)))
		policy := cacheLimits{resultBytes: 1 << 20, entries: 2}
		store := cacheStore{root: os.Getenv("BORK_RESULT_BUDGET_ROOT"), namespace: body.Namespace, policy: &policy}
		for n := 0; n < 10; n++ {
			if err := store.write(lifecycleBody(t, &body, index*10+n)); err != nil && !errors.Is(err, errCacheArtifactBudget) {
				t.Fatal(err)
			}
		}
		return
	}
	body, _ := cacheArtifactFixture(t)
	cache := t.TempDir()
	fixture := filepath.Join(t.TempDir(), "body.json")
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture, data, 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for index := 0; index < 4; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			cmd := exec.Command(exe, "-test.run=^TestCacheResultCrossProcessBudget$", "-test.count=1")
			cmd.Env = append(os.Environ(), "BORK_RESULT_BUDGET_BODY="+fixture, "BORK_RESULT_BUDGET_ROOT="+cache, "BORK_RESULT_BUDGET_INDEX="+strconv.Itoa(index))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("worker: %v\n%s", err, out)
			}
		}()
	}
	workers.Wait()
	entries := inventoryForTest(t, cacheStore{root: cache})
	var bytes int64
	for _, entry := range entries {
		bytes += entry.bytes
	}
	if len(entries) > 2 || bytes > 1<<20 {
		t.Fatalf("cross-process overshoot: %d entries %d bytes", len(entries), bytes)
	}
	if len(entries) == 0 {
		t.Fatal("no publisher succeeded")
	}
}
