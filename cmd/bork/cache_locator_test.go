package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDiskCacheAlternatingCompilerLocator(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("running compiler image receipt is Linux-only")
	}
	version, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(version), "go1.26") && !strings.HasPrefix(string(version), "go1.27") {
		t.Skip("unsupported Go receipt version")
	}
	root := t.TempDir()
	executables := []string{filepath.Join(root, "compiler-a"), filepath.Join(root, "compiler-b")}
	for index, exe := range executables {
		flags := "-X github.com/GiGurra/bork/internal/driver.cacheTestGate=enabled -buildid=locator-" + string(rune('a'+index))
		if output, err := exec.Command("go", "build", "-ldflags="+flags, "-o", exe, ".").CombinedOutput(); err != nil {
			t.Fatalf("build compiler: %v\n%s", err, output)
		}
	}
	source := filepath.Join(root, "main.bork")
	if err := os.WriteFile(source, []byte("fn main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(root, "probe")
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", filepath.Join(root, "cache"))
	t.Setenv("BORK_TEST_DISK_CACHE_PROBE", probe)
	t.Setenv("BORK_TEST_DISK_CACHE_BACKGROUND", "")
	t.Setenv("BORK_CACHE", "on")
	var expected []byte
	for _, index := range []int{0, 0, 1, 1, 0, 0, 1, 1} {
		output, err := exec.Command(executables[index], "emit", source).CombinedOutput()
		if err != nil {
			t.Fatalf("compile: %v\n%s", err, output)
		}
		if expected == nil {
			expected = bytes.Clone(output)
		} else if !bytes.Equal(output, expected) {
			t.Fatal("alternating compiler output changed")
		}
	}
	statuses, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	if string(statuses) != "miss\nhit\nmiss\nhit\nmiss\nhit\nmiss\nhit\n" {
		t.Fatalf("namespace locator transitions: %q", statuses)
	}
}
