package driver

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/syntax"
)

func requireStageLock(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("platform uses temporary staging")
	}
}

func TestStableGoStageReplacesCompleteInputs(t *testing.T) {
	requireStageLock(t)
	base := t.TempDir()
	module := &goModuleInputs{mod: []byte("module example.com/old\ngo 1.26\n"), sum: []byte("old checksum\n")}
	embeds := []*check.Embedded{{Files: []check.EmbeddedFile{{StagePath: "assets/old", Data: []byte("old asset")}}}}
	first, _, release, err := stageGoStable(base, "entry", []byte("old source"), module, embeds)
	if err != nil {
		t.Fatal(err)
	}
	release()
	module = &goModuleInputs{mod: []byte("module example.com/new\ngo 1.26\n")}
	second, _, release, err := stageGoStable(base, "entry", []byte("new source"), module, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if second != first {
		t.Fatalf("source edit moved package directory: %s -> %s", first, second)
	}
	expectedMod := append([]byte(nil), module.mod...)
	if goModuleHook != nil {
		expectedMod = goModuleHook(expectedMod)
	}
	for name, want := range map[string]string{"main.go": "new source", "go.mod": string(expectedMod)} {
		got, err := os.ReadFile(filepath.Join(second, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q (%v)", name, got, err)
		}
	}
	for _, name := range []string{"go.sum", "assets/old"} {
		if _, err := os.Stat(filepath.Join(second, name)); !os.IsNotExist(err) {
			t.Fatalf("stale input %s remains: %v", name, err)
		}
	}
}

func TestGoStageFallbackBuildsFresh(t *testing.T) {
	program, err := checkProgramObserved("../../examples/hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	switch runtime.GOOS {
	case "windows":
		t.Setenv("LocalAppData", blocked)
	case "darwin":
		t.Setenv("HOME", blocked)
	default:
		t.Setenv("XDG_CACHE_HOME", blocked)
	}
	for _, value := range []string{"one", "two"} {
		source := []byte(fmt.Sprintf("package main\nimport \"fmt\"\nfunc main(){fmt.Println(%q)}", value))
		exe := filepath.Join(t.TempDir(), "program")
		if err := buildGoWithContext(program.files, source, exe, program.module, program.context); err != nil {
			t.Fatal(err)
		}
		got, err := exec.Command(exe).CombinedOutput()
		if err != nil || string(got) != value+"\n" {
			t.Fatalf("fallback = %q (%v)", got, err)
		}
	}
}

func TestStableGoStageConcurrentProcesses(t *testing.T) {
	requireStageLock(t)
	ctx := captureGoContext()
	if ctx.err != nil {
		t.Fatal(ctx.err)
	}
	t.Setenv("GOCACHE", ctx.values["GOCACHE"])
	if runtime.GOOS == "darwin" {
		t.Setenv("HOME", t.TempDir())
	} else {
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
	}
	cacheBase, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BORK_GO_STAGE_HELPER", "1")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.bork"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var processes []*exec.Cmd
	var outputs []*strings.Builder
	for _, value := range []string{"one", "two", "three", "four"} {
		exe := filepath.Join(t.TempDir(), "program")
		command := exec.Command(os.Args[0], "-test.run=^TestGoStageProcessHelper$", "--", root, exe, value)
		output := new(strings.Builder)
		command.Stdout, command.Stderr = output, output
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		processes = append(processes, command)
		outputs = append(outputs, output)
	}
	for index, command := range processes {
		if err := command.Wait(); err != nil {
			t.Fatalf("child %d: %v\n%s", index, err, outputs[index])
		}
	}
	entries, err := os.ReadDir(filepath.Join(cacheBase, "bork", "stage", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "locks" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("children did not contend on one staging entry: %d", count)
	}
}

func TestGoStageProcessHelper(t *testing.T) {
	if os.Getenv("BORK_GO_STAGE_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	args := os.Args[len(os.Args)-3:]
	files := []*syntax.File{{Path: filepath.Join(args[0], "main.bork"), Package: "main"}}
	module, err := captureGoModule(files, diskSources{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := captureGoContext()
	source := []byte(fmt.Sprintf("package main\nimport \"fmt\"\nfunc main(){fmt.Println(%q)}", args[2]))
	if err := buildGoWithContext(files, source, args[1], module, ctx); err != nil {
		t.Fatal(err)
	}
	got, err := exec.Command(args[1]).CombinedOutput()
	if err != nil || string(got) != args[2]+"\n" {
		t.Fatalf("another request replaced staged inputs: %q (%v)", got, err)
	}
}

func TestGoStageLockPoolBounded(t *testing.T) {
	// Cache eviction must not leave an unbounded permanent lock per program.
	base := t.TempDir()
	slots := map[string]bool{}
	for index := range 4096 {
		path := goStageLockPath(base, fmt.Sprint(index))
		if filepath.Dir(path) != filepath.Join(base, "locks") {
			t.Fatal("lock lives inside an evicted tree")
		}
		slots[path] = true
	}
	if len(slots) != 256 {
		t.Fatalf("lock pool has %d slots, want 256", len(slots))
	}
}
