package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCacheCleanCLI(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("persistent locks supported on Linux/Darwin")
	}
	directory := t.TempDir()
	exe := cliExecutable(t, false)
	switch runtime.GOOS {
	case "darwin":
		t.Setenv("HOME", directory)
	default:
		t.Setenv("XDG_CACHE_HOME", directory)
	}
	root, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, "bork")
	key := strings.Repeat("a", 64)
	namespace := strings.Repeat("b", 64)
	current := filepath.Join(root, "stage", "v3", key[:2], key, "tree")
	legacy := filepath.Join(root, "stage", "v1", key, "tree")
	artifact := filepath.Join(root, "results", "v1", namespace, key+".json")
	unknown := filepath.Join(root, "user-note.txt")
	for _, path := range []string{filepath.Join(current, "main.go"), filepath.Join(legacy, "main.go"), artifact, unknown} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("contents"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	output, err := exec.Command(exe, "clean").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Removed 0 results, 1 staged trees") {
		t.Fatalf("current clean: %v\n%s", err, output)
	}
	for _, path := range []string{legacy, artifact, unknown} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("default clean removed %s", path)
		}
	}
	output, err = exec.Command(exe, "clean", "--all").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Removed 1 results, 1 staged trees") {
		t.Fatalf("all clean: %v\n%s", err, output)
	}
	for _, path := range []string{legacy, artifact} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("all clean retained %s", path)
		}
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatal("removed unrecognized user file")
	}
	output, err = exec.Command(exe, "clean", "--all").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Removed 0 results, 0 staged trees") {
		t.Fatalf("empty clean: %v\n%s", err, output)
	}
}
