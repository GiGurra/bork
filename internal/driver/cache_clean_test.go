package driver

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCacheCleanNamespacesVersionsAndPreservation(t *testing.T) {
	requireStageLock(t)
	body, _ := cacheArtifactFixture(t)
	base := t.TempDir()
	store := cacheStore{root: base, namespace: body.Namespace}
	if err := store.write(body); err != nil {
		t.Fatal(err)
	}
	other := *body
	other.Namespace = sha256.Sum256([]byte("other compiler"))
	otherStore := store
	otherStore.namespace = other.Namespace
	if err := otherStore.write(&other); err != nil {
		t.Fatal(err)
	}
	metadata := stageMetadataForTest(t)
	module := &goModuleInputs{mod: []byte("module stage\n")}
	stage, _, release, err := stageGoStable(base, stageLifecycleKey(1), []byte("source"), module, nil, metadata)
	if err != nil {
		t.Fatal(err)
	}
	release()
	legacy := filepath.Join(base, "stage", "v1", stageLifecycleKey(2), "tree")
	if err := os.MkdirAll(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "main.go"), []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	legacyV2 := filepath.Join(base, "stage", "v2", stageLifecycleKey(3), "tree")
	if err := os.MkdirAll(legacyV2, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyV2, "main.go"), []byte("legacy v2"), 0600); err != nil {
		t.Fatal(err)
	}
	legacyResult := filepath.Join(base, "results", "v1", fmt.Sprintf("%x", body.Namespace), cacheArtifactFilename(body.Key))
	if err := os.MkdirAll(filepath.Dir(legacyResult), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyResult, []byte("legacy result"), 0600); err != nil {
		t.Fatal(err)
	}
	job := filepath.Join(base, "jobs", "v1", ".job-abandoned")
	if err := os.MkdirAll(filepath.Dir(job), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(job, nil, 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "project-output")
	if err := os.WriteFile(output, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := cleanCache(context.Background(), base, body.Namespace, false)
	if err != nil || report.Results != 1 || report.Stages != 1 || report.Temporaries != 1 || report.Bytes <= 0 {
		t.Fatalf("current clean: %+v %v", report, err)
	}
	if store.read(body.Request) != nil || otherStore.read(body.Request) == nil {
		t.Fatal("namespace selection wrong")
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("current stage retained")
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("legacy deleted without --all")
	}
	for _, path := range []string{filepath.Join(base, store.lockName(body.Key)), goStageLockPath(base, stageLifecycleKey(1)), filepath.Join(base, "mutation.lock"), filepath.Join(base, "locks", "publish-v1", "00.lock")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("coordination file removed: %s %v", path, err)
		}
	}
	report, err = cleanCache(context.Background(), base, body.Namespace, true)
	if err != nil || report.Results != 2 || report.Stages != 2 {
		t.Fatalf("all clean: %+v %v", report, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy retained with --all")
	}
	for _, path := range []string{legacyV2, legacyResult} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("legacy layout retained", path, err)
		}
	}
	if data, err := os.ReadFile(output); err != nil || string(data) != "preserve" {
		t.Fatal("project output changed")
	}
	report, err = cleanCache(context.Background(), base, body.Namespace, true)
	if err != nil || report != (CacheCleanReport{}) {
		t.Fatalf("empty clean: %+v %v", report, err)
	}
}

type cleanWaitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *cleanWaitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}
func TestCacheCleanWaitsForStageAndCancellation(t *testing.T) {
	requireStageLock(t)
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "release", true: "cancel"}[cancelled], func(t *testing.T) {
			base := t.TempDir()
			metadata := stageMetadataForTest(t)
			module := &goModuleInputs{mod: []byte("module stage\n")}
			stage, _, release, err := stageGoStable(base, stageLifecycleKey(1), []byte("source"), module, nil, metadata)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observed := &cleanWaitingContext{Context: ctx, waiting: make(chan struct{})}
			result := make(chan error, 1)
			go func() { _, err := cleanCache(observed, base, [32]byte{}, true); result <- err }()
			<-observed.waiting // Done is consulted only when a selected slot is busy.
			if _, err := os.Stat(stage); err != nil {
				t.Fatal("active tree removed")
			}
			if cancelled {
				cancel()
			} else {
				release()
			}
			err = <-result
			if cancelled {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel: %v", err)
				}
				if _, err := os.Stat(stage); err != nil {
					t.Fatal("cancelled clean removed active stage")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(stage); !os.IsNotExist(err) {
					t.Fatal("released stage retained")
				}
			}
		})
	}
}
func TestCacheCleanDrainsPublisherAdmission(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "locks", "publish-v1"), 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	slot, err := (cacheStore{}).lock(root, filepath.Join("locks", "publish-v1", "00.lock"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &cleanWaitingContext{Context: ctx, waiting: make(chan struct{})}
	result := make(chan error, 1)
	go func() { _, err := cleanCache(observed, base, [32]byte{}, true); result <- err }()
	<-observed.waiting
	_ = slot.Close()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
func TestCacheCleanMissingUnavailableAndSymlink(t *testing.T) {
	requireStageLock(t)
	report, err := cleanCache(context.Background(), filepath.Join(t.TempDir(), "missing"), [32]byte{}, true)
	if err != nil || report != (CacheCleanReport{}) {
		t.Fatalf("missing: %+v %v", report, err)
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := cleanCache(context.Background(), blocked, [32]byte{}, true); err == nil {
		t.Fatal("inaccessible cache reported success")
	}
	base := t.TempDir()
	outside := filepath.Join(t.TempDir(), "output")
	if err := os.WriteFile(outside, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "stage", "v2")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, stageLifecycleKey(1))); err != nil {
		t.Fatal(err)
	}
	report, err = cleanCache(context.Background(), base, [32]byte{}, true)
	if err != nil || report.Stages != 1 {
		t.Fatalf("corrupt symlink clean: %+v %v", report, err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "preserve" {
		t.Fatal("followed entry symlink")
	}
}

func TestCacheCleanLocatorTemporaries(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	namespace := sha256.Sum256([]byte("compiler"))
	other := sha256.Sum256([]byte("other compiler"))
	key := sha256.Sum256([]byte("request"))
	index := filepath.Join(base, cacheIndexPath(key))
	if err := os.MkdirAll(filepath.Dir(index), 0700); err != nil {
		t.Fatal(err)
	}
	current, foreign, partial := index+".tmp-current", index+".tmp-foreign", index+".tmp-partial"
	for path, data := range map[string]string{current: fmt.Sprintf("%x", namespace), foreign: fmt.Sprintf("%x", other), partial: "partial"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := cleanCache(context.Background(), base, namespace, false)
	if err != nil || report.Temporaries != 1 {
		t.Fatalf("current locator cleanup: %+v %v", report, err)
	}
	if _, err := os.Stat(current); !os.IsNotExist(err) {
		t.Fatal("current locator temporary survived", err)
	}
	for _, path := range []string{foreign, partial} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("foreign/unknown namespace temporary removed", err)
		}
	}
	report, err = cleanCache(context.Background(), base, namespace, true)
	if err != nil || report.Temporaries != 2 {
		t.Fatalf("all locator cleanup: %+v %v", report, err)
	}
}

type batchCancelContext struct {
	context.Context
	calls int
}

func (c *batchCancelContext) Err() error {
	c.calls++
	if c.calls > 2 {
		return context.Canceled
	}
	return nil
}

func TestCacheCleanDirectoryBatchesCancel(t *testing.T) {
	base := t.TempDir()
	for index := range 700 {
		if err := os.WriteFile(filepath.Join(base, fmt.Sprint(index)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	ctx := &batchCancelContext{Context: context.Background()}
	if _, err := cleanDirectoryEntries(ctx, root, "."); !errors.Is(err, context.Canceled) {
		t.Fatalf("batch cancellation: %v", err)
	}
}

func TestCacheRemovalSupportsLargeTree(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "entry"), 0700); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(base, "payload")
	if err := os.WriteFile(payload, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	// Valid staged trees can exceed the old node cap through files plus parent
	// directories. Cleanup also needs to remove interrupted/malformed large trees.
	for index := range goStageInventoryLimit + 1 {
		if index%16384 == 0 {
			payload = filepath.Join(base, fmt.Sprintf("payload-%d", index))
			if err := os.WriteFile(payload, []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Link(payload, filepath.Join(base, "entry", fmt.Sprint(index))); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	size, err := cacheRemovalBytes(context.Background(), root, "entry")
	if err != nil || size != int64(goStageInventoryLimit+1) {
		t.Fatalf("large tree accounting: %d %v", size, err)
	}
}

func TestCacheCleanRejectsAliasedLayersAndLocks(t *testing.T) {
	requireStageLock(t)
	for _, alias := range []string{"stage/v3", "results/v2", "jobs/v1", "locks/publish-v1", "locks/stage-v3", "stage/v1/locks"} {
		t.Run(alias, func(t *testing.T) {
			base := t.TempDir()
			legacyKey := stageLifecycleKey(1)
			legacy := filepath.Join(base, "stage", "v1", legacyKey, "tree")
			if err := os.MkdirAll(legacy, 0700); err != nil {
				t.Fatal(err)
			}
			payload := filepath.Join(legacy, "main.go")
			if err := os.WriteFile(payload, []byte("active legacy"), 0600); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(base)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			oldSlot := sha256.Sum256([]byte(legacyKey))
			legacyLocks := filepath.Join(base, "stage", "v1", "locks")
			if err := os.MkdirAll(legacyLocks, 0700); err != nil {
				t.Fatal(err)
			}
			slot, err := (cacheStore{}).lock(root, filepath.Join("stage", "v1", "locks", fmt.Sprintf("%02x.lock", oldSlot[0])))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = slot.Close() }()
			link := filepath.Join(base, filepath.FromSlash(alias))
			target := filepath.Join(base, "stage", "v1")
			if alias == "stage/v1/locks" {
				// Keep the actual legacy lock descriptor alive while redirecting its path.
				target = filepath.Join(base, "alternate-locks")
				if err := os.Rename(legacyLocks, target); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if alias == "locks/stage-v3" {
				stage := filepath.Join(base, goStageEntryPath(stageLifecycleKey(2)), "tree")
				if err := os.MkdirAll(stage, 0700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err = cleanCache(ctx, base, [32]byte{}, alias == "stage/v1/locks")
			if err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("aliased %s was not rejected: %v", alias, err)
			}
			if data, err := os.ReadFile(payload); err != nil || string(data) != "active legacy" {
				t.Fatalf("wrong-slot removal: %v", err)
			}
		})
	}
}
