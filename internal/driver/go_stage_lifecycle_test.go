package driver

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func stageLifecycleKey(index int) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("stage-%d", index))))
}
func stageMetadataForTest(t *testing.T) goStageMetadata {
	t.Helper()
	return goStageMetadata{Schema: goStageSchema, Program: t.TempDir(), Mode: "build"}
}
func TestGoStagePublicationDoesNotScanOrEvict(t *testing.T) {
	t.Parallel()
	requireStageLock(t)
	base := t.TempDir()
	metadata := stageMetadataForTest(t)
	module := &goModuleInputs{mod: []byte("module stage\n")}
	first, _, release, err := stageGoStable(base, stageLifecycleKey(1), []byte("first"), module, nil, metadata)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	secondKey := stageLifecycleKey(2)
	for index := 3; goStageLockPath(base, secondKey) == goStageLockPath(base, stageLifecycleKey(1)); index++ {
		secondKey = stageLifecycleKey(index)
	}
	dir := filepath.Join(base, "stage", "v3", secondKey[:2])
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for index := range 4097 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("unknown-%d", index)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	second, _, done, err := stageGoStable(base, secondKey, []byte("second"), module, nil, metadata)
	if err != nil {
		t.Fatal(err)
	}
	done()
	if data, err := os.ReadFile(filepath.Join(first, "main.go")); err != nil || string(data) != "first" {
		t.Fatal("busy unrelated stage changed")
	}
	if data, err := os.ReadFile(filepath.Join(second, "main.go")); err != nil || string(data) != "second" {
		t.Fatal("new stage unavailable")
	}
}
func TestGoStageFixedPendingAndMetadata(t *testing.T) {
	t.Parallel()
	requireStageLock(t)
	base := t.TempDir()
	metadata := stageMetadataForTest(t)
	module := &goModuleInputs{mod: []byte("module stage\n")}
	key := stageLifecycleKey(1)
	first, _, release, err := stageGoStable(base, key, []byte("first"), module, nil, metadata)
	if err != nil {
		t.Fatal(err)
	}
	release()
	entry := filepath.Dir(first)
	for _, name := range []string{"next", "previous"} {
		dir := filepath.Join(entry, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "abandoned"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, _, release, err = stageGoStable(base, key, []byte("second"), module, nil, metadata)
	if err != nil {
		t.Fatal(err)
	}
	release()
	for _, name := range []string{"next", "previous"} {
		if _, err := os.Stat(filepath.Join(entry, name)); !os.IsNotExist(err) {
			t.Fatal("abandoned generation retained", err)
		}
	}
	data, err := os.ReadFile(filepath.Join(entry, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted goStageMetadata
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Schema != 3 || persisted.Program != metadata.Program || persisted.Mode != "build" {
		t.Fatalf("metadata: %+v", persisted)
	}
	// Deletion is maintenance work; a new build must not inspect/retire other roots.
	if err := os.Remove(metadata.Program); err != nil {
		t.Fatal(err)
	}
	other := stageMetadataForTest(t)
	_, _, release, err = stageGoStable(base, stageLifecycleKey(2), []byte("other"), module, nil, other)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(first); err != nil {
		t.Fatal("build evicted deleted-root stage", err)
	}
}
func TestGoStageDeclinesContainedEntryAlias(t *testing.T) {
	t.Parallel()
	requireStageLock(t)
	base := t.TempDir()
	metadata := stageMetadataForTest(t)
	module := &goModuleInputs{mod: []byte("module stage\n")}
	dir, _, release, err := stageGoStable(base, stageLifecycleKey(1), []byte("first"), module, nil, metadata)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	alias := filepath.Join(base, goStageEntryPath(stageLifecycleKey(2)))
	if err := os.MkdirAll(filepath.Dir(alias), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(dir), alias); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := stageGoStable(base, stageLifecycleKey(2), []byte("second"), module, nil, metadata); err == nil {
		t.Fatal("entry alias bypassed original lock")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "main.go")); err != nil || string(data) != "first" {
		t.Fatal("aliased tree was overwritten")
	}
}

func TestGoStageHourlyUseSurvivesReplacement(t *testing.T) {
	t.Parallel()
	requireStageLock(t)
	base := t.TempDir()
	key := stageLifecycleKey(1)
	metadata := stageMetadataForTest(t)
	module := &goModuleInputs{mod: []byte("module stage\n")}
	start := time.Unix(1700000000, 0)
	publish := func(source string, now time.Time) {
		t.Helper()
		_, _, release, err := stageGoStableAt(base, key, []byte(source), module, nil, metadata, now)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	marker := filepath.Join(base, goStageEntryPath(key), "used")
	assertTime := func(want time.Time) {
		t.Helper()
		info, err := os.Stat(marker)
		if err != nil || !info.ModTime().Equal(want) {
			t.Fatalf("marker %v, err %v; want %v", info, err, want)
		}
	}
	publish("first", start)
	assertTime(start)
	publish("edited", start.Add(time.Hour-time.Second))
	assertTime(start)
	data, err := os.ReadFile(filepath.Join(base, goStageEntryPath(key), "tree", "main.go"))
	if err != nil || string(data) != "edited" {
		t.Fatal("fresh replacement failed", err)
	}
	publish("again", start.Add(time.Hour))
	assertTime(start.Add(time.Hour))
	publish("clock moved back", start.Add(-time.Hour))
	assertTime(start.Add(time.Hour))
	// Retention hints cannot turn a successful compilation into failure.
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(marker, 0700); err != nil {
		t.Fatal(err)
	}
	publish("bad marker", start.Add(2*time.Hour))
	data, err = os.ReadFile(filepath.Join(base, goStageEntryPath(key), "tree", "main.go"))
	if err != nil || string(data) != "bad marker" {
		t.Fatal("hint failure lost stage", err)
	}
}
