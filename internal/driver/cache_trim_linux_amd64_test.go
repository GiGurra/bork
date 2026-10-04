//go:build linux && amd64

package driver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func completeTrim(t *testing.T, base string, now time.Time, budget int) int {
	t.Helper()
	removed := 0
	for range 1000 {
		report, err := runCacheTrim(context.Background(), base, now, budget)
		if err != nil && err != context.DeadlineExceeded {
			t.Fatal(err)
		}
		if report.Steps > budget {
			t.Fatal("worker exceeded budget", report)
		}
		removed += report.Removed
		if report.Complete {
			return removed
		}
	}
	t.Fatal("trim made no bounded progress")
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
func TestCacheTrimResumesBoundedDeletion(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	now := time.Unix(1700000000, 0)
	old := trimStage(t, base, 1, now.Add(-cacheTrimAge-time.Hour), 200)
	fresh := trimStage(t, base, 2, now, 2)
	future := trimStage(t, base, 3, now.Add(24*time.Hour), 2)
	unknown := filepath.Join(base, "stage", "v3", "unknown")
	if err := os.Mkdir(unknown, 0700); err != nil {
		t.Fatal(err)
	}
	report, err := runCacheTrim(context.Background(), base, now, 12)
	if err != nil {
		t.Fatal(err)
	}
	if report.Complete || report.Steps != 12 {
		t.Fatal("expected partial bounded cycle", report)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	state := readCacheTrimState(root)
	_ = root.Close()
	if !validCacheTrimState(state) || state.Cutoff.IsZero() {
		t.Fatal("invalid persisted progress", state)
	}
	if removed := completeTrim(t, base, now, 12); removed != 1 {
		t.Fatalf("removed=%d", removed)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old stage retained", err)
	}
	for _, path := range []string{fresh, future, unknown} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("fresh/future/unknown removed", path, err)
		}
	}
	report, err = runCacheTrim(context.Background(), base, now.Add(time.Hour), 12)
	if err != nil || report.Steps != 0 || !report.Complete {
		t.Fatal("daily gate", report, err)
	}
	if removed := completeTrim(t, base, now.Add(cacheTrimAge+time.Hour), 12); removed != 1 {
		t.Fatal("next daily cycle did not restart", removed)
	}
}
func TestCacheTrimResultsLocatorsAndBusyStage(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	now := time.Unix(1700000000, 0)
	old := now.Add(-cacheTrimAge - time.Hour)
	body, _ := cacheArtifactFixture(t)
	store := cacheStore{root: base, namespace: body.Namespace}
	if err := store.write(body); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{store.path(body.Key), cacheIndexPath(body.Key)} {
		if err := os.Chtimes(filepath.Join(base, path), old, old); err != nil {
			t.Fatal(err)
		}
	}
	stage := trimStage(t, base, 1, old, 1)
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	slot, err := store.lock(root, goStageLockPath("", stageLifecycleKey(1)))
	if err != nil {
		t.Fatal(err)
	}
	if removed := completeTrim(t, base, now, 16); removed != 2 {
		t.Fatal("result/locator removal", removed)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatal("busy stage removed", err)
	}
	_ = slot.Close()
	if removed := completeTrim(t, base, now.Add(cacheTrimInterval), 16); removed != 1 {
		t.Fatal("busy stage not reconsidered", removed)
	}
}
func TestCacheTrimFreshStageInterruptsRemoval(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	now := time.Unix(1700000000, 0)
	path := trimStage(t, base, 1, now.Add(-cacheTrimAge-time.Hour), 100)
	found := false
	for range 100 {
		if _, err := runCacheTrim(context.Background(), base, now, 1); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(base)
		if err != nil {
			t.Fatal(err)
		}
		state := readCacheTrimState(root)
		_ = root.Close()
		if len(state.Removal) > 1 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("did not enter partial tree removal")
	}
	trimStage(t, base, 1, now, 3)
	completeTrim(t, base, now, 8)
	if data, err := os.ReadFile(filepath.Join(path, "tree", "main.go")); err != nil || string(data) != "package main" {
		t.Fatal("fresh stage deleted during resume", err)
	}
}
func TestCacheTrimInvalidStateAndAlias(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	now := time.Unix(1700000000, 0)
	stage := trimStage(t, base, 1, now, 1)
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	store := cacheStore{root: base}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte("untrusted")))
	states := []cacheTrimState{
		{Version: 1, Delete: "../outside"},
		{Version: 1, Layer: 99},
		{Version: 1, Scan: []cacheTrimFrame{{Path: "locks", Offset: 1}}},
		{Version: 1, Cutoff: now.Add(time.Hour), Delete: goStageEntryPath(stageLifecycleKey(1))},
		{Version: 1, Delete: goStageEntryPath(stageLifecycleKey(1)), Removal: []cacheTrimFrame{{Path: "stage/v3/" + key[:2] + "/" + key}}},
	}
	for _, state := range states {
		if err := writeCacheTrimState(root, store, state); err != nil {
			t.Fatal(err)
		}
		completeTrim(t, base, now, 8)
		if _, err := os.Stat(stage); err != nil {
			t.Fatal("invalid progress removed fresh stage", err)
		}
	}
	// A recognizable alias never allows maintenance to traverse its target.
	aliasKey := stageLifecycleKey(9)
	alias := filepath.Join(base, goStageEntryPath(aliasKey))
	if err := os.MkdirAll(filepath.Dir(alias), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(stage, alias); err != nil {
		t.Fatal(err)
	}
	if err := writeCacheTrimState(root, store, cacheTrimState{Version: 1}); err != nil {
		t.Fatal(err)
	}
	completeTrim(t, base, now, 8)
	if _, err := os.Lstat(alias); err != nil {
		t.Fatal("alias was handled as owned stage", err)
	}
}

func TestCacheTrimPoisonedRemovalCannotCrossBusySlot(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	now := time.Unix(1700000000, 0)
	old := now.Add(-cacheTrimAge - time.Hour)
	firstKey := stageLifecycleKey(1)
	secondKey := stageLifecycleKey(2)
	for index := 3; goStageLockPath("", firstKey) == goStageLockPath("", secondKey); index++ {
		secondKey = stageLifecycleKey(index)
	}
	// Put both entries in the same shard so an internal '..' reaches its sibling.
	secondKey = firstKey[:2] + secondKey[2:]
	for goStageLockPath("", firstKey) == goStageLockPath("", secondKey) {
		secondKey = firstKey[:2] + fmt.Sprintf("%x", sha256.Sum256([]byte(secondKey)))[2:]
	}
	trimStage(t, base, 1, old, 2)
	metadata := stageMetadataForTest(t)
	_, _, release, err := stageGoStableAt(base, secondKey, []byte("busy program"), &goModuleInputs{mod: []byte("module stage\n")}, nil, metadata, old)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	state := cacheTrimState{Version: 1, Cutoff: now.Add(-cacheTrimAge), Delete: goStageEntryPath(firstKey), Removal: []cacheTrimFrame{{Path: goStageEntryPath(firstKey)}, {Path: goStageEntryPath(firstKey) + "/../" + secondKey + "/tree"}}}
	if validCacheTrimState(state) {
		t.Fatal("uncanonical removal frame accepted")
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := writeCacheTrimState(root, cacheStore{root: base}, state); err != nil {
		t.Fatal(err)
	}
	completeTrim(t, base, now, 32)
	data, err := os.ReadFile(filepath.Join(base, goStageEntryPath(secondKey), "tree", "main.go"))
	if err != nil || string(data) != "busy program" {
		t.Fatal("poisoned progress crossed busy SLOT", err)
	}
}

func TestCacheTrimBusyWorkerAndCancellation(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	now := time.Unix(1700000000, 0)
	trimStage(t, base, 1, now.Add(-cacheTrimAge-time.Hour), 10)
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	lock, err := (cacheStore{root: base}).lock(root, "trim.lock")
	if err != nil {
		t.Fatal(err)
	}
	report, err := runCacheTrim(context.Background(), base, now, 100)
	if err != nil || report.Steps != 0 {
		t.Fatal("busy maintenance did work", report, err)
	}
	_ = lock.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err = runCacheTrim(ctx, base, now, 100)
	if err != context.Canceled || report.Steps != 0 {
		t.Fatal("canceled worker did work", report, err)
	}
	completeTrim(t, base, now, 16)
}

func TestCacheTrimMissingMarkerUsesPublicationEvidence(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	now := time.Unix(1700000000, 0)
	old := now.Add(-cacheTrimAge - time.Hour)
	stale := trimStage(t, base, 1, old, 1)
	fresh := trimStage(t, base, 2, now, 1)
	for _, entry := range []string{stale, fresh} {
		if err := os.Remove(filepath.Join(entry, "used")); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"metadata.json", "tree", "."} {
			if err := os.Chtimes(filepath.Join(entry, name), old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.Chtimes(filepath.Join(fresh, "metadata.json"), now, now); err != nil {
		t.Fatal(err)
	}
	if removed := completeTrim(t, base, now, 16); removed != 1 {
		t.Fatal("missing-marker migration", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("old unmarked stage retained", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh publication removed", err)
	}
}
func TestCacheTrimDeepProgressFitsBoundedState(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	now := time.Unix(1700000000, 0)
	stage := trimStage(t, base, 1, now.Add(-cacheTrimAge-time.Hour), 0)
	path := filepath.Join(stage, "tree")
	// Full-path-per-frame serialization would exceed32KiB at this depth.
	for range 90 {
		path = filepath.Join(path, "long-component-abcdefghijk")
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(path, "leaf"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if removed := completeTrim(t, base, now, 12); removed != 1 {
		t.Fatal("deep tree retained", removed)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("deep tree incomplete", err)
	}
}

func TestCacheTrimUnsupportedLongTreeDoesNotRestartCycle(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	now := time.Unix(1700000000, 0)
	old := now.Add(-cacheTrimAge - time.Hour)
	entry := trimStage(t, base, 1, old, 0)
	ordinary := trimStage(t, base, 2, old, 1)
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	path := filepath.Join(goStageEntryPath(stageLifecycleKey(1)), "tree")
	for range 20 {
		path = filepath.Join(path, strings.Repeat("x", 230))
		if err := root.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	file, err := root.OpenFile(filepath.Join(path, "leaf"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	completeTrim(t, base, now, 12)
	if _, err := os.Stat(ordinary); !os.IsNotExist(err) {
		t.Fatal("unsupported tree stalled other entries", err)
	}
	if _, err := os.Stat(entry); err != nil {
		t.Fatal("unsupported tree should remain for explicit clean", err)
	}
	state := readCacheTrimState(root)
	if !state.Completed.Equal(now) || !validCacheTrimState(state) {
		t.Fatal("cycle restarted instead of skipping unsupported candidate", state)
	}
}

func TestCacheTrimDeclinesAliasedLockPool(t *testing.T) {
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
	lock, err := (cacheStore{root: base}).lock(root, goStageLockPath("", stageLifecycleKey(1)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := root.Rename("locks/stage-v3", "locks/held-stage-v3"); err != nil {
		t.Fatal(err)
	}
	if err := root.Mkdir("locks/other", 0700); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("other", "locks/stage-v3"); err != nil {
		t.Fatal(err)
	}
	completeTrim(t, base, now, 16)
	data, err := os.ReadFile(filepath.Join(path, "tree", "main.go"))
	if err != nil || string(data) != "package main" {
		t.Fatal("alias bypassed original busy lock", err)
	}
}
