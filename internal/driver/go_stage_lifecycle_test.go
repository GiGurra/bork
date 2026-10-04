package driver

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func stageLifecycleKey(index int) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("stage-%d", index))))
}
func stageMetadataForTest(t *testing.T, policy *cacheLimits) goStageMetadata {
	t.Helper()
	return goStageMetadata{Schema: goStageSchema, Program: t.TempDir(), Mode: "build", policy: policy}
}
func stageInventoryForTest(t *testing.T, base string) []goStageEntry {
	t.Helper()
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	entries, err := stageInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
func TestGoStageBudgetSkipsBusyEntries(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	policy := cacheLimits{stageBytes: 1 << 20, entries: 1}
	metadata := stageMetadataForTest(t, &policy)
	module := &goModuleInputs{mod: []byte("module stage\n")}
	first, _, release, err := stageGoStable(base, stageLifecycleKey(1), []byte("first"), module, nil, metadata)
	if err != nil {
		t.Fatal(err)
	}
	secondKey := stageLifecycleKey(2)
	for index := 3; goStageLockPath(base, secondKey) == goStageLockPath(base, stageLifecycleKey(1)); index++ {
		secondKey = stageLifecycleKey(index)
	}
	if _, _, _, err := stageGoStable(base, secondKey, []byte("second"), module, nil, metadata); err == nil {
		t.Fatal("active tree evicted or budget exceeded")
	}
	if data, err := os.ReadFile(filepath.Join(first, "main.go")); err != nil || string(data) != "first" {
		t.Fatal("busy tree changed")
	}
	release()
	_, _, release, err = stageGoStable(base, secondKey, []byte("second"), module, nil, metadata)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("released old tree retained: %v", err)
	}
	if _, err := os.Stat(goStageLockPath(base, stageLifecycleKey(1))); err != nil {
		t.Fatal("coordination lock removed")
	}
}
func TestGoStageByteBudgetAndAbandonedGenerations(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	policy := cacheLimits{stageBytes: 4096, entries: 100}
	metadata := stageMetadataForTest(t, &policy)
	module := &goModuleInputs{mod: []byte("module stage\n")}
	source := make([]byte, 1024)
	var first string
	for index := 0; index < 5; index++ {
		dir, _, release, err := stageGoStable(base, stageLifecycleKey(index), source, module, nil, metadata)
		if err != nil {
			t.Fatal(err)
		}
		release()
		first = dir
	}
	var bytes int64
	for _, entry := range stageInventoryForTest(t, base) {
		bytes += entry.bytes
	}
	if bytes > policy.stageBytes {
		t.Fatalf("stage bytes %d > %d", bytes, policy.stageBytes)
	}
	pending := filepath.Join(filepath.Dir(first), "new-abandoned")
	if err := os.Mkdir(pending, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending, "payload"), make([]byte, 8192), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, release, err := stageGoStable(base, stageLifecycleKey(4), source, module, nil, metadata)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Fatalf("abandoned generation remains: %v", err)
	}
	if _, _, _, err := stageGoStable(base, stageLifecycleKey(6), make([]byte, 8192), module, nil, metadata); err == nil {
		t.Fatal("oversized stage persisted")
	}
}
func TestGoStageDeletedProgramAndMetadata(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	metadata := stageMetadataForTest(t, nil)
	module := &goModuleInputs{mod: []byte("module stage\n")}
	first, _, release, err := stageGoStable(base, stageLifecycleKey(1), []byte("first"), module, nil, metadata)
	if err != nil {
		t.Fatal(err)
	}
	release()
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(first), "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted goStageMetadata
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Program != metadata.Program || persisted.Mode != "build" || persisted.Schema != 2 {
		t.Fatalf("metadata: %+v", persisted)
	}
	if err := os.Remove(metadata.Program); err != nil {
		t.Fatal(err)
	}
	second := stageMetadataForTest(t, nil)
	_, _, release, err = stageGoStable(base, stageLifecycleKey(2), []byte("second"), module, nil, second)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("deleted-program stage retained: %v", err)
	}
}
func TestGoStageInventoryRejectsSymlinks(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	metadata := stageMetadataForTest(t, nil)
	module := &goModuleInputs{mod: []byte("module stage\n")}
	dir, _, release, err := stageGoStable(base, stageLifecycleKey(1), []byte("first"), module, nil, metadata)
	if err != nil {
		t.Fatal(err)
	}
	release()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if _, err := stageInventory(root); err == nil {
		t.Fatal("entry symlink accepted")
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "preserve" {
		t.Fatal("outside target changed")
	}
}

func TestGoStageDeclinesContainedEntryAlias(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	metadata := stageMetadataForTest(t, nil)
	module := &goModuleInputs{mod: []byte("module stage\n")}
	dir, _, release, err := stageGoStable(base, stageLifecycleKey(1), []byte("first"), module, nil, metadata)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	alias := filepath.Join(base, "stage", "v2", stageLifecycleKey(2))
	if err := os.Symlink(filepath.Base(filepath.Dir(dir)), alias); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := stageGoStable(base, stageLifecycleKey(2), []byte("second"), module, nil, metadata); err == nil {
		t.Fatal("entry alias bypassed original lock")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "main.go")); err != nil || string(data) != "first" {
		t.Fatal("aliased tree was overwritten")
	}
}

func TestGoStageReservesInventoryNodes(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	policy := cacheLimits{stageBytes: 1 << 20, stageNodes: 14, entries: 100}
	metadata := stageMetadataForTest(t, &policy)
	module := &goModuleInputs{mod: []byte("module stage\n")}
	for index := 0; index < 12; index++ {
		_, _, release, err := stageGoStable(base, stageLifecycleKey(index), []byte("source"), module, nil, metadata)
		if err != nil {
			t.Fatal(err)
		}
		release()
		entries := stageInventoryForTest(t, base)
		nodes := 0
		for _, entry := range entries {
			nodes += entry.nodes
		}
		if nodes > policy.stageNodes {
			t.Fatalf("scanner nodes %d > %d", nodes, policy.stageNodes)
		}
	}
	files := map[string][]byte{}
	for index := 0; index < goStageInventoryLimit/2; index++ {
		files[fmt.Sprintf("asset-%d/file", index)] = nil
	}
	if _, err := goStageNodes(files); err == nil {
		t.Fatal("module/parent/metadata nodes were not counted")
	}
}
