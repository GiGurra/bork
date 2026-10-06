package driver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func completeTrim(t *testing.T, base string, now time.Time, budget int) int {
	t.Helper()
	removed := 0
	for range 2000 {
		report, err := runCacheTrim(context.Background(), base, now, budget)
		if err != nil && err != context.DeadlineExceeded {
			t.Fatal(err)
		}
		if report.Steps > budget {
			t.Fatal("worker exceeded shard budget", report)
		}
		removed += report.Removed
		if report.Complete {
			return removed
		}
	}
	t.Fatal("trim did not complete its shard cycle")
	return 0
}
func trimStage(t *testing.T, base string, index int, when time.Time, files int) string {
	t.Helper()
	key := stageLifecycleKey(index)
	meta := stageMetadataForTest(t)
	_, _, release, err := stageGoStableAt(base, key, []byte("package main"), &goModuleInputs{mod: []byte("module stage\n")}, nil, meta, when)
	if err != nil {
		t.Fatal(err)
	}
	release()
	entry := filepath.Join(base, goStageEntryPath(key))
	tree := filepath.Join(entry, "tree", "nested")
	if err := os.MkdirAll(tree, 0700); err != nil {
		t.Fatal(err)
	}
	for i := range files {
		if err := os.WriteFile(filepath.Join(tree, fmt.Sprint(i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return entry
}

// Sequential: exact progress and daily-gate assertions require immediate
// nonblocking lock reacquisition. Parallel subprocess launches can inherit held
// lock descriptors until exec, briefly retaining locks after the parent closes.
func TestCacheTrimShardProgressAndDailyGate(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	now := time.Unix(1700000000, 0)
	stale := trimStage(t, base, 1, now.Add(-cacheTrimAge-time.Hour), 300)
	fresh := trimStage(t, base, 2, now, 1)
	future := trimStage(t, base, 3, now.Add(24*time.Hour), 1)
	first, err := runCacheTrim(context.Background(), base, now, 3)
	if err != nil || first.Steps != 3 || first.Complete {
		t.Fatal(first, err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	state := readCacheTrimState(root)
	if !validCacheTrimState(state) || state.Shard != 3 || state.Layer != 0 {
		t.Fatal("bounded shard progress", state)
	}
	if removed := completeTrim(t, base, now, 32); removed != 1 {
		t.Fatal("stale stage deletion", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale retained", err)
	}
	for _, path := range []string{fresh, future} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("fresh/future removed", err)
		}
	}
	report, err := runCacheTrim(context.Background(), base, now.Add(time.Hour), 32)
	if err != nil || report.Steps != 0 || !report.Complete {
		t.Fatal("daily gate", report, err)
	}
	if removed := completeTrim(t, base, now.Add(cacheTrimAge+time.Hour), 32); removed != 1 {
		t.Fatal("cycle did not restart", removed)
	}
}
func TestCacheTrimBusyEntryAndLockAlias(t *testing.T) {
	t.Parallel()
	requireStageLock(t)
	base := t.TempDir()
	now := time.Unix(1700000000, 0)
	old := now.Add(-cacheTrimAge - time.Hour)
	path := trimStage(t, base, 1, old, 1)
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	store := cacheStore{root: base}
	slot, err := store.lock(root, goStageLockPath("", stageLifecycleKey(1)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slot.Close() }()
	completeTrim(t, base, now, 32)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("busy entry removed", err)
	}
	if err := root.Rename("locks/stage-v3", "locks/held-stage-v3"); err != nil {
		t.Fatal(err)
	}
	if err := root.Mkdir("locks/other", 0700); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("other", "locks/stage-v3"); err != nil {
		t.Skip(err)
	}
	completeTrim(t, base, now.Add(cacheTrimInterval), 32)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("alias bypassed busy entry", err)
	}
}

// Sequential: direct detach assertions require immediate nonblocking lock
// reacquisition after publication, independent of concurrent process launches.
func TestCacheTrimDetachedEntryRepublicationAndTrashRecovery(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	now := time.Unix(1700000000, 0)
	old := now.Add(-cacheTrimAge - time.Hour)
	path := trimStage(t, base, 1, old, 1)
	key := stageLifecycleKey(1)
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	store := cacheStore{root: base}
	detached, err := detachCacheTrimEntry(root, store, goStageEntryPath(key), goStageLockPath("", key), true, now.Add(-cacheTrimAge))
	if err != nil || detached == "" {
		t.Fatal("detach", detached, err)
	}
	// Simulate worker interruption after the atomic detach, before removing trash.
	trimStage(t, base, 1, now, 2)
	completeTrim(t, base, now, 32)
	if _, err := root.Stat(detached); !os.IsNotExist(err) {
		t.Fatal("abandoned trash retained", err)
	}
	data, err := os.ReadFile(filepath.Join(path, "tree", "main.go"))
	if err != nil || string(data) != "package main" {
		t.Fatal("fresh publication damaged", err)
	}
	// A stale decision does not authorize detaching a fresh replacement.
	if next, err := detachCacheTrimEntry(root, store, goStageEntryPath(key), goStageLockPath("", key), true, now.Add(-cacheTrimAge)); err != nil || next != "" {
		t.Fatal("fresh entry detached", next, err)
	}
}

// Sequential: the exact removal count requires each entry's slot and mutation
// locks to be free when trim reaches it. Parallel subprocess launches can
// inherit those descriptors (the fixture's or trim's own) until exec, and trim
// skips busy entries.
func TestCacheTrimResultNamespacesAndLocators(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	now := time.Unix(1700000000, 0)
	old := now.Add(-cacheTrimAge - time.Hour)
	body, _ := cacheArtifactFixture(t)
	for _, namespace := range [][sha256.Size]byte{body.Namespace, sha256.Sum256([]byte("other compiler"))} {
		body.Namespace = namespace
		store := cacheStore{root: base, namespace: namespace}
		if err := store.write(body); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(base, store.path(body.Key)), old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(filepath.Join(base, cacheIndexPath(body.Key)), old, old); err != nil {
		t.Fatal(err)
	}
	if removed := completeTrim(t, base, now, 32); removed != 3 {
		t.Fatal("result/locator shards", removed)
	}
}

// Sequential: removing the stale entry requires each entry's slot and mutation
// locks to be free when trim reaches it. Parallel subprocess launches can
// inherit those descriptors (the fixture's or trim's own) until exec, and trim
// skips busy entries.
func TestCacheTrimMissingMarkerAndInvalidProgress(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	now := time.Unix(1700000000, 0)
	old := now.Add(-cacheTrimAge - time.Hour)
	stale := trimStage(t, base, 1, old, 1)
	fresh := trimStage(t, base, 2, now, 1)
	for _, path := range []string{stale, fresh} {
		if err := os.Remove(filepath.Join(path, "used")); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"metadata.json", "tree", "."} {
			if err := os.Chtimes(filepath.Join(path, name), old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.Chtimes(filepath.Join(fresh, "metadata.json"), now, now); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for _, state := range []cacheTrimState{{Version: 2, Layer: 99}, {Version: 2, Shard: -1}, {Version: 2, Cutoff: now.Add(time.Hour)}} {
		if err := writeCacheTrimState(root, cacheStore{root: base}, state); err != nil {
			t.Fatal(err)
		}
		completeTrim(t, base, now, 32)
		if _, err := os.Stat(fresh); err != nil {
			t.Fatal("invalid progress removed fresh entry", err)
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("unmarked stale entry retained", err)
	}
}

// Sequential: this fixture expects immediate nonblocking lock reacquisition
// after close, which can overlap descriptor inheritance during parallel launches.
func TestCacheTrimCancellationAndCleanTrash(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	now := time.Unix(1700000000, 0)
	old := now.Add(-cacheTrimAge - time.Hour)
	trimStage(t, base, 1, old, 1)
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	store := cacheStore{root: base}
	worker, err := store.lock(root, "trim.lock")
	if err != nil {
		t.Fatal(err)
	}
	report, err := runCacheTrim(context.Background(), base, now, 32)
	if err != nil || report.Steps != 0 {
		t.Fatal("busy worker", report, err)
	}
	_ = worker.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err = runCacheTrim(ctx, base, now, 32)
	if err != context.Canceled || report.Steps != 0 {
		t.Fatal("canceled worker", report, err)
	}
	detached, err := detachCacheTrimEntry(root, store, goStageEntryPath(stageLifecycleKey(1)), goStageLockPath("", stageLifecycleKey(1)), true, now.Add(-cacheTrimAge))
	if err != nil || detached == "" {
		t.Fatal(detached, err)
	}
	if _, err := cleanCache(context.Background(), base, [sha256.Size]byte{}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Stat(detached); !os.IsNotExist(err) {
		t.Fatal("clean left detached trash", err)
	}
}
