//go:build unix

package main

import (
	"bytes"
	"debug/buildinfo"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// rewriteExecutable corrupts path in place, keeping its inode. Linux can
// briefly report ETXTBSY when opening a recently executed program for
// writing, even after it has exited, so retry until the kernel releases it.
func rewriteExecutable(path string, data []byte) error {
	for attempt := 0; ; attempt++ {
		err := os.WriteFile(path, data, 0700)
		if !errors.Is(err, syscall.ETXTBSY) || attempt == 100 {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

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
	if err := os.Chmod(output, 0600); err != nil {
		t.Fatal(err)
	}
	build()
	if out, err := exec.Command(output).CombinedOutput(); err != nil || string(out) != "42\n" {
		t.Fatalf("repaired permissions: %s: %v", out, err)
	}
	if err := rewriteExecutable(output, []byte("corrupted executable")); err != nil {
		t.Fatal(err)
	}
	build()
	if out, err := exec.Command(output).CombinedOutput(); err != nil || string(out) != "42\n" {
		t.Fatalf("repaired executable: %s: %v", out, err)
	}
}

func TestBuildReceiptRepairsCorruptionWithIntactBuildID(t *testing.T) {
	exe, _, _ := buildReceiptCLI(t)
	root := t.TempDir()
	source, output := filepath.Join(root, "main.bork"), filepath.Join(root, "hello")
	if err := os.WriteFile(source, []byte("fn main() uses io { println(\"UNIQUE_ORIGINAL_MESSAGE\") }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	build := func() {
		t.Helper()
		if out, err := exec.Command(exe, "build", source, "-o", output).CombinedOutput(); err != nil {
			t.Fatalf("build: %s: %v", out, err)
		}
	}
	build()
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.ReplaceAll(data, []byte("UNIQUE_ORIGINAL_MESSAGE"), []byte("UNIQUE_CORRUPTEDMESSAGE"))
	if bytes.Equal(data, changed) || len(data) != len(changed) {
		t.Fatal("did not alter an executable string without changing its size")
	}
	if err := rewriteExecutable(output, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := buildinfo.ReadFile(output); err != nil {
		t.Fatalf("corruption damaged Go build metadata: %v", err)
	}
	build()
	if out, err := exec.Command(output).CombinedOutput(); err != nil || string(out) != "UNIQUE_ORIGINAL_MESSAGE\n" {
		t.Fatalf("corrupt executable remained cached: %s: %v", out, err)
	}
}

func TestBuildReceiptsConcurrentSharedOutput(t *testing.T) {
	exe, _, _ := buildReceiptCLI(t)
	root := t.TempDir()
	output := filepath.Join(root, "shared")
	var sources []string
	for _, name := range []string{"a", "b"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, filepath.Join(dir, "main.bork"))
	}
	for range 3 {
		var builders sync.WaitGroup
		failures := make(chan string, 2)
		for i, source := range sources {
			text := "fn main() uses io { println(\"" + []string{"A", "B"}[i] + "\") }\n"
			if err := os.WriteFile(source, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			builders.Go(func() {
				if out, err := exec.Command(exe, "build", source, "-o", output).CombinedOutput(); err != nil {
					failures <- string(out) + ": " + err.Error()
				}
			})
		}
		builders.Wait()
		close(failures)
		for failure := range failures {
			t.Fatal(failure)
		}
		for i, source := range sources {
			if out, err := exec.Command(exe, "build", source, "-o", output).CombinedOutput(); err != nil {
				t.Fatalf("build: %s: %v", out, err)
			}
			if out, err := exec.Command(output).CombinedOutput(); err != nil || string(out) != []string{"A\n", "B\n"}[i] {
				t.Fatalf("shared output receipt selected the wrong program: %s: %v", out, err)
			}
		}
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
