package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDiskCacheCLIParity(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("running compiler image receipt currently Linux-only")
	}
	version, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(version), "go1.26") && !strings.HasPrefix(string(version), "go1.27") {
		t.Skip("unsupported Go inventory version")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	root := t.TempDir()
	exe := filepath.Join(root, "bork-test-cache")
	build := exec.Command("go", "build", "-ldflags=-X github.com/GiGurra/bork/internal/driver.cacheTestGate=enabled", "-o", exe, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build test CLI: %v\n%s", err, out)
	}
	program := filepath.Join(root, "program")
	if err := os.Mkdir(program, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(program, "main.bork")
	if err := os.WriteFile(source, []byte("fn main() { _ = dbg(1); todo() }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cache, probe := filepath.Join(root, "cache"), filepath.Join(root, "probe")
	t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", "")
	t.Setenv("BORK_TEST_DISK_CACHE_PROBE", probe)
	run := func(args ...string) ([]byte, error) { t.Helper(); return exec.Command(exe, args...).CombinedOutput() }
	checkStatus := func(expected string) {
		t.Helper()
		data, err := os.ReadFile(probe)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != expected+"\n" {
			t.Fatalf("cache status: %q, want %s", data, expected)
		}
		if err := os.Truncate(probe, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"check", "--json", program}, {"check", program}, {"emit", program}} {
		t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", "")
		clean, err := run(args...)
		if err != nil {
			t.Fatalf("clean: %v\n%s", err, clean)
		}
		t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", cache)
		first, err := run(args...)
		if err != nil {
			t.Fatalf("fresh: %v\n%s", err, first)
		}
		// Human and JSON check share the same semantic request.
		status := "miss"
		if args[0] == "check" && len(args) == 2 {
			status = "hit"
		}
		checkStatus(status)
		second, err := run(args...)
		if err != nil {
			t.Fatalf("cached: %v\n%s", err, second)
		}
		checkStatus("hit")
		if !bytes.Equal(clean, first) || !bytes.Equal(clean, second) {
			t.Fatalf("clean/cached parity: %s\n%s\n%s", clean, first, second)
		}
	}
	// A same-mtime content change invalidates a fresh-process hit.
	before, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("fn main() { _ = dbg(2); todo() }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(source, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	changed, err := run("emit", program)
	if err != nil {
		t.Fatalf("edited: %v\n%s", err, changed)
	}
	checkStatus("miss")
	t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", "")
	clean, err := run("emit", program)
	if err != nil || !bytes.Equal(clean, changed) {
		t.Fatalf("edit parity: %v", err)
	}
	t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", cache)
	// Corruption is an ordinary miss and gets replaced by a fresh result.
	if err := filepath.WalkDir(cache, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".json") {
			return os.WriteFile(path, []byte("corrupt"), 0600)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	repaired, err := run("emit", program)
	if err != nil || !bytes.Equal(repaired, clean) {
		t.Fatalf("corrupt fallback: %v", err)
	}
	checkStatus("miss")

	// Membership and newly observed process configuration invalidate the entry.
	if err := os.WriteFile(filepath.Join(program, "helper.bork"), []byte("fn helper() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := run("emit", program); err != nil {
		t.Fatalf("membership: %v\n%s", err, out)
	}
	checkStatus("miss")
	t.Setenv("BORK_TEST_ADDED_ENV", "yes")
	if out, err := run("emit", program); err != nil {
		t.Fatalf("environment: %v\n%s", err, out)
	}
	checkStatus("miss")
	if out, err := run("emit", program); err != nil {
		t.Fatalf("environment hit: %v\n%s", err, out)
	}
	checkStatus("hit")
	if err := os.Remove(filepath.Join(program, "helper.bork")); err != nil {
		t.Fatal(err)
	}
	// Failed results are never published or reused.
	if err := os.WriteFile(source, []byte("fn helper() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		output, err := run("emit", program)
		if err == nil || !bytes.Contains(output, []byte("package has no main function")) {
			t.Fatalf("missing main: %v\n%s", err, output)
		}
		checkStatus("error")
	}

	// Evaluators must observe foreign file edits, even with no source changes.
	foreign := filepath.Join(root, "foreign")
	unsafeSource := fmt.Sprintf(`fn readForeign(path:String):String unsafe go{
import "text/template"
t,err:=template.ParseFiles(path);if err!=nil{panic(err)};return t.Tree.Root.String()
}
fn main(){println(comptime{readForeign(%q)})}`, foreign)
	if err := os.WriteFile(filepath.Join(program, "bork.mod"), []byte("module example.com/cacheparity\nunsafe \"example.com/cacheparity\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(unsafeSource), 0600); err != nil {
		t.Fatal(err)
	}
	var prior []byte
	for _, contents := range []string{"first", "second"} {
		if err := os.WriteFile(foreign, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		generated, err := run("emit", program)
		if err != nil {
			t.Fatalf("evaluator: %v\n%s", err, generated)
		}
		checkStatus("bypass")
		if prior != nil && bytes.Equal(prior, generated) {
			t.Fatal("foreign file edit reused evaluator output")
		}
		prior = generated
	}

}

func TestDiskCacheCLIRequiresBuiltGate(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "bork")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	command := exec.Command("go", "build", "-o", exe, ".")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	source := filepath.Join(root, "main.bork")
	if err := os.WriteFile(source, []byte("fn main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cache, probe := filepath.Join(root, "cache"), filepath.Join(root, "probe")
	t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", cache)
	t.Setenv("BORK_TEST_DISK_CACHE_PROBE", probe)
	if out, err := exec.Command(exe, "check", source).CombinedOutput(); err != nil {
		t.Fatalf("check: %v\n%s", err, out)
	}
	for _, path := range []string{cache, probe} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("ordinary CLI touched test cache: %s (%v)", path, err)
		}
	}
}
