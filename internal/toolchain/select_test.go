package toolchain

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GiGurra/bork/internal/testutil"
)

func TestSelect(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bork.mod"), []byte("module example.com/app\nbork 0.4\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		current, setting, query string
		fails                   bool
	}{
		{"v0.3.9", "auto", "v0.4", false},
		{"v0.4.1", "auto", "", false},
		{"dev", "auto", "", false},
		{"v0.3.9", "local", "", true},
		{"v0.4.1", "local", "", false},
		{"dev", "v0.4.2", "v0.4.2", false},
		{"v0.4.2", "0.4.2", "", false},
		{"v0.5.0", "v0.4.2", "v0.4.2", false},
		{"v0.5.0", "v0.3.9", "", true},
		{"dev", "v0.4", "", true},
	} {
		selection, err := Select(tc.current, tc.setting, filepath.Join(dir, "nested", "main.bork"))
		if (err != nil) != tc.fails || err == nil && selection.Query != tc.query {
			t.Errorf("%+v: %+v (%v)", tc, selection, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "bork.mod"), []byte("module example.com/nested\n"), 0644); err != nil {
		t.Fatal(err)
	}
	selection, err := Select("v0.1.0", "local", filepath.Join(dir, "nested"))
	if err != nil || selection.Query != "" || selection.Reason != "local compiler" {
		t.Fatalf("nearest manifest: %+v (%v)", selection, err)
	}
}

func TestEnsureCompiler(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Go uses a shell script")
	}
	fixture := testutil.Compiler(t, "v0.4.2")
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	goScript := "#!/bin/sh\n/usr/bin/printf '%s\\n' \"$@\" >> \"$INSTALL_LOG\"\n/bin/cp \"$COMPILER_FIXTURE\" \"$GOBIN/bork\"\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(goScript), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("COMPILER_FIXTURE", fixture)
	t.Setenv("INSTALL_LOG", log)
	root := filepath.Join(dir, "cache")
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			path, version, err := Ensure(context.Background(), root, "v0.4", io.Discard)
			if err != nil || version != "v0.4.2" || path != executable(filepath.Join(cacheDir(root), "v0.4.2")) {
				t.Errorf("ensure: %s, %s (%v)", path, version, err)
			}
		})
	}
	wg.Wait()
	data, err := os.ReadFile(log)
	if err != nil || strings.Count(string(data), "install\n") != 1 || !strings.Contains(string(data), "@v0.4\n") {
		t.Fatalf("should install once via minor query: %s (%v)", data, err)
	}
	if _, _, err := Ensure(context.Background(), root, "v0.5.0", io.Discard); err == nil || !strings.Contains(err.Error(), "Go installed v0.4.2") {
		t.Fatalf("incorrect downloaded version: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir(root), "v0.5.0")); !os.IsNotExist(err) {
		t.Fatalf("incorrect compiler was published: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	for _, query := range []string{"v0.4", "v0.4.2"} {
		if _, version, err := Ensure(context.Background(), root, query, io.Discard); err != nil || version != "v0.4.2" {
			t.Fatalf("offline cache: %s (%v)", version, err)
		}
	}
	if _, _, err := Ensure(context.Background(), root, "v0.5.0", io.Discard); err == nil || !strings.Contains(err.Error(), "requires Go") {
		t.Fatalf("missing Go: %v", err)
	}
	if err := Clean(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "toolchains")); !os.IsNotExist(err) {
		t.Fatalf("clean kept compilers: %v", err)
	}
}

func TestCompilerLockCancellation(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("unsupported lock platform")
	}
	path := filepath.Join(t.TempDir(), "lock")
	release, err := acquire(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := acquire(ctx, path); err != context.DeadlineExceeded {
		t.Fatalf("lock cancellation: %v", err)
	}
}
