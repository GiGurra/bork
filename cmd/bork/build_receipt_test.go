//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildReceiptSkipsGoAndRepairsOutput(t *testing.T) {
	exe, _, probe := buildReceiptCLI(t)
	root := t.TempDir()
	source, output := filepath.Join(root, "main.bork"), filepath.Join(root, "hello")
	if err := os.WriteFile(source, []byte("fn main() uses io { println(42) }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	build := func() {
		t.Helper()
		if out, err := exec.Command(exe, "build", source, "-o", output).CombinedOutput(); err != nil {
			t.Fatalf("build: %s: %v", out, err)
		}
	}
	build()
	before, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(probe); err != nil || !strings.Contains(string(data), "build ") {
		t.Fatalf("cold build didn't record Go: %s: %v", data, err)
	}
	if err := os.Truncate(probe, 0); err != nil {
		t.Fatal(err)
	}
	build()
	if data, err := os.ReadFile(probe); err != nil || len(data) != 0 {
		t.Fatalf("warm build invoked Go: %s: %v", data, err)
	}
	after, err := os.Stat(output)
	if err != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() {
		t.Fatalf("warm build changed binary: %v", err)
	}
	if err := os.WriteFile(output, []byte("corrupted executable"), 0700); err != nil {
		t.Fatal(err)
	}
	build()
	if out, err := exec.Command(output).CombinedOutput(); err != nil || string(out) != "42\n" {
		t.Fatalf("repaired executable: %s: %v", out, err)
	}
}

func buildReceiptCLI(t *testing.T) (exe, cache, probe string) {
	t.Helper()
	exe = cliExecutable(t, true)
	cache = t.TempDir()
	t.Setenv("BORKCACHE", cache)
	t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", cache)
	t.Setenv("BORK_TEST_DISK_CACHE_BACKGROUND", "")
	t.Setenv("BORK_TEST_CACHE_PRODUCTION", "")
	t.Setenv("BORKTOOLCHAIN", "local")
	t.Setenv("BORK_CACHE", "on")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOPACKAGESDRIVER", "off")
	probe = filepath.Join(t.TempDir(), "go-invocations")
	t.Setenv("BORK_TEST_GO_COMMAND_PROBE", probe)
	return
}

func TestRunAndScriptSkipGoAndPreserveMtime(t *testing.T) {
	for _, operation := range []string{"run", "script"} {
		t.Run(operation, func(t *testing.T) {
			exe, cache, probe := buildReceiptCLI(t)
			source := filepath.Join(t.TempDir(), "main.bork")
			text := "fn main() uses io { println(42) }\n"
			if operation == "script" {
				text = "#!/usr/bin/env -S bork script\nprintln(42)\n"
			}
			if err := os.WriteFile(source, []byte(text), 0700); err != nil {
				t.Fatal(err)
			}
			run := func() {
				t.Helper()
				if out, err := exec.Command(exe, operation, source).CombinedOutput(); err != nil || string(out) != "42\n" {
					t.Fatalf("run: %s: %v", out, err)
				}
			}
			run()
			paths, err := filepath.Glob(filepath.Join(cache, "stage", "v3", "*", "*", "program"))
			if err != nil || len(paths) != 1 {
				t.Fatalf("cached executable: %v: %v", paths, err)
			}
			before, err := os.Stat(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Truncate(probe, 0); err != nil {
				t.Fatal(err)
			}
			run()
			if data, err := os.ReadFile(probe); err != nil || len(data) != 0 {
				t.Fatalf("warm run invoked Go: %s: %v", data, err)
			}
			after, err := os.Stat(paths[0])
			if err != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() {
				t.Fatalf("warm run changed binary: %v", err)
			}
		})
	}
}
