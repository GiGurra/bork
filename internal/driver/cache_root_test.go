package driver

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/GiGurra/bork/internal/toolenv"
)

func TestConfiguredCacheRoot(t *testing.T) {
	switch runtime.GOOS {
	case "darwin":
		t.Setenv("HOME", t.TempDir())
	case "windows":
		t.Setenv("APPDATA", t.TempDir())
	default:
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	}
	t.Setenv("BORKCACHE", "")
	t.Setenv("BORK_CACHE", "")
	saved := filepath.Join(t.TempDir(), "saved")
	explicit := filepath.Join(t.TempDir(), "environment")
	if err := toolenv.Update([]string{"BORKCACHE=" + saved, "BORK_CACHE=off"}, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := cacheRootDir(); err != nil || got != saved {
		t.Fatalf("saved root: %q %v", got, err)
	}
	if !cacheDisabled() {
		t.Fatal("saved cache toggle ignored")
	}
	t.Setenv("BORKCACHE", explicit)
	t.Setenv("BORK_CACHE", "on")
	if got, err := cacheRootDir(); err != nil || got != explicit {
		t.Fatalf("environment root: %q %v", got, err)
	}
	if cacheDisabled() {
		t.Fatal("environment cache toggle ignored")
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		// Clean must select the same root without creating a second bork subdirectory.
		if err := os.MkdirAll(explicit, 0700); err != nil {
			t.Fatal(err)
		}
		note := filepath.Join(explicit, "keep.txt")
		if err := os.WriteFile(note, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Clean(context.Background(), true); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(note); err != nil {
			t.Fatalf("clean removed unowned file: %v", err)
		}
		if _, err := os.Stat(filepath.Join(explicit, "bork")); !os.IsNotExist(err) {
			t.Fatalf("nested cache: %v", err)
		}
	}
	t.Setenv("BORKCACHE", "relative")
	if _, err := Clean(context.Background(), true); err == nil {
		t.Fatal("clean accepted invalid cache root")
	}
}
