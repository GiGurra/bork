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
	metadata := stageMetadataForTest(t, nil)
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
	if err != nil || report.Results != 1 || report.Stages != 1 {
		t.Fatalf("all clean: %+v %v", report, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy retained with --all")
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
			metadata := stageMetadataForTest(t, nil)
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
func TestCacheResultDeletedTargetCleanup(t *testing.T) {
	original, _ := cacheArtifactFixture(t)
	base := t.TempDir()
	store := cacheStore{root: base, namespace: original.Namespace}
	first := lifecycleBody(t, original, 1)
	if err := store.write(first); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(first.Request.Path); err != nil {
		t.Fatal(err)
	}
	second := lifecycleBody(t, original, 2)
	for index := 3; store.lockName(first.Key) == store.lockName(second.Key); index++ {
		second = lifecycleBody(t, original, index)
	}
	if err := store.write(second); err != nil {
		t.Fatal(err)
	}
	if store.read(first.Request) != nil || store.read(second.Request) == nil {
		t.Fatal("deleted target did not retire before LRU pressure")
	}
}

func TestCacheCleanRejectsAliasedLayersAndLocks(t *testing.T) {
	requireStageLock(t)
	for _, alias := range []string{"stage/v2", "results/v1", "jobs/v1", "locks/publish-v1", "locks/stage-v2", "stage/v1/locks"} {
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
			if alias == "locks/stage-v2" {
				stage := filepath.Join(base, "stage", "v2", stageLifecycleKey(2), "tree")
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
