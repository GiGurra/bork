package driver

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// fixtureDir gives a leaf test fresh files at a stable source path. Call once
// per leaf, without child tests acquiring this pool: hashed slot collisions
// could deadlock an ancestor. The lock stays held through execution and cleanup. No files or
// executables survive cleanup, only the bounded lock pool does.
func fixtureDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(".")
	if err != nil {
		return t.TempDir()
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return t.TempDir()
	}
	cache, err := goStageCacheDir()
	if err != nil {
		return t.TempDir()
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(root+"\x00"+t.Name())))
	if dir, ok := freshFixtureDir(t, filepath.Join(cache, "fixtures-v1"), key); ok {
		return dir
	}
	return t.TempDir()
}

func freshFixtureDir(t *testing.T, base, key string) (string, bool) {
	t.Helper()
	if _, err := privateTestCacheDir(base); err != nil {
		return "", false
	}
	if err := os.MkdirAll(filepath.Join(base, "locks"), 0o700); err != nil {
		return "", false
	}
	lock, err := lockGoStage(goStageLockPath(base, key))
	if err != nil {
		return "", false
	}
	dir := filepath.Join(base, key)
	if err := os.RemoveAll(dir); err != nil {
		_ = lock.Close()
		return "", false
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		_ = lock.Close()
		return "", false
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("removing fixture: %v", err)
		}
		_ = lock.Close()
	})
	return dir, true
}

func TestFixtureDirectoryLifecycle(t *testing.T) {
	base := filepath.Join(t.TempDir(), "private")
	var first string
	t.Run("first", func(t *testing.T) {
		dir, ok := freshFixtureDir(t, base, "same-key")
		if !ok {
			t.Fatal("fixture unavailable")
		}
		first = dir
		if err := os.WriteFile(filepath.Join(dir, "stale"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("fixture survived cleanup: %v", err)
	}
	t.Run("second", func(t *testing.T) {
		dir, ok := freshFixtureDir(t, base, "same-key")
		if !ok || dir != first {
			t.Fatalf("unstable fixture: %q, %v", dir, ok)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("fixture not fresh: %v, %v", entries, err)
		}
	})
	blocked := filepath.Join(base, "blocked")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := freshFixtureDir(t, blocked, "key"); ok {
		t.Fatal("accepted unavailable cache")
	}
	t.Setenv("XDG_CACHE_HOME", blocked)
	fallback := fixtureDir(t)
	if _, err := os.Stat(fallback); err != nil {
		t.Fatalf("temporary fallback failed: %v", err)
	}
}
