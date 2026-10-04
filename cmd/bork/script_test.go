package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScriptCLIAndCache(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("result cache platform")
	}
	exe := cliExecutable(t, true)
	t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", t.TempDir())
	t.Setenv("BORK_TEST_DISK_CACHE_BACKGROUND", "")
	t.Setenv("BORK_TEST_CACHE_PRODUCTION", "")
	t.Setenv("BORK_CACHE", "on")
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	probe := filepath.Join(t.TempDir(), "probe")
	t.Setenv("BORK_TEST_DISK_CACHE_PROBE", probe)
	root := t.TempDir()
	path := filepath.Join(root, "hello.bork")
	// This file has no shebang: the command alone must select script mode.
	source := "import \"bork/process\"\nx=42\nprintln(x)\nprintln(process.Args())\n"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{"first", "second"} {
		out, err := exec.Command(exe, "script", path, "--", arg).CombinedOutput()
		want := "42\n[\"" + arg + "\"]\n"
		if err != nil || string(out) != want {
			t.Fatalf("script: %s: %v", out, err)
		}
	}
	data, err := os.ReadFile(probe)
	if err != nil || !strings.Contains(string(data), "hit\n") {
		t.Fatalf("no warm script result-cache hit: %s: %v", data, err)
	}
	out, err := exec.Command(exe, "check", path).CombinedOutput()
	if err == nil {
		t.Fatalf("ordinary mode reused script result: %s", out)
	}
	if err := os.WriteFile(path, []byte("#!/usr/bin/env -S bork script\nprintln(99)\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out, err = exec.Command(exe, "run", path).CombinedOutput()
	if err != nil || string(out) != "99\n" {
		t.Fatalf("shebang run: %s: %v", out, err)
	}
	out, err = exec.Command(exe, "script", root).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "single .bork file") {
		t.Fatalf("directory script: %s: %v", out, err)
	}
}
