//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRunAndScriptReuseExecutable(t *testing.T) {
	exe := cliExecutable(t, false)
	cache := t.TempDir()
	t.Setenv("BORKCACHE", cache)
	t.Setenv("BORK_CACHE", "on")
	t.Setenv("BORKTOOLCHAIN", "local")
	root := t.TempDir()
	program := filepath.Join(root, "main.bork")
	script := filepath.Join(root, "script.bork")
	for path, source := range map[string]string{
		program: "fn main() uses io { println(42) }\n",
		script:  "#!/usr/bin/env -S bork script\nprintln(42)\n",
	} {
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"run", program}, {"script", script}} {
		var before os.FileInfo
		var executable string
		for range 2 {
			out, err := exec.Command(exe, args...).CombinedOutput()
			if err != nil || string(out) != "42\n" {
				t.Fatalf("%v: %s: %v", args, out, err)
			}
			if before == nil {
				if err := filepath.WalkDir(cache, func(path string, entry os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if entry.Name() == "program" {
						info, err := entry.Info()
						if err != nil {
							return err
						}
						if executable == "" || info.ModTime().After(before.ModTime()) {
							executable, before = path, info
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if before == nil {
					t.Fatal("run did not publish a cached executable")
				}
			} else {
				after, err := os.Stat(executable)
				if err != nil || !os.SameFile(before, after) {
					t.Fatalf("unchanged program executable was replaced: %v", err)
				}
			}
		}
	}
}

func TestBuildPreservesUnchangedExecutable(t *testing.T) {
	exe := cliExecutable(t, false)
	t.Setenv("BORKCACHE", t.TempDir())
	t.Setenv("BORKTOOLCHAIN", "local")
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
	build()
	after, err := os.Stat(output)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("unchanged build executable was replaced: %v", err)
	}
	if err := os.WriteFile(source, []byte("fn main() uses io { println(99) }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	build()
	out, err := exec.Command(output).CombinedOutput()
	if err != nil || string(out) != "99\n" {
		t.Fatalf("changed build: %s: %v", out, err)
	}
}
