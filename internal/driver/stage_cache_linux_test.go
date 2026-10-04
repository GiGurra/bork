package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

// Tests retain generated staging in a private per-user temporary cache so
// ordinary runs can reuse Go's build cache even with a read-only HOME cache.
// Remove this directory to clear it; bork clean uses the normal user cache.
func configureTestStageCache() {
	base := filepath.Join(os.TempDir(), fmt.Sprintf("bork-driver-tests-%d", os.Getuid()))
	goStageCacheDir = testStageCacheDir(base, os.Getenv("HOME"))
}

func testStageCacheDir(base, home string) func() (string, error) {
	return func() (string, error) {
		// Empty XDG selects harness staging; explicit XDG or a changed HOME keeps
		// environment-mutating cache and fallback fixtures on the platform path.
		if os.Getenv("XDG_CACHE_HOME") != "" || os.Getenv("HOME") != home {
			return os.UserCacheDir()
		}
		if err := os.Mkdir(base, 0o700); err != nil && !os.IsExist(err) {
			return "", err
		}
		info, err := os.Lstat(base)
		if err != nil {
			return "", err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.IsDir() || info.Mode().Perm()&0o077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
			return "", fmt.Errorf("test staging root is not a private owned directory: %s", base)
		}
		return base, nil
	}
}

func TestDriverStageCacheRoot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	base := filepath.Join(t.TempDir(), "cache")
	resolve := testStageCacheDir(base, os.Getenv("HOME"))
	before := captureGoContext()
	first, err := resolve()
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolve()
	if err != nil || first != base || second != first {
		t.Fatalf("unstable cache root: %q, %q, %v", first, second, err)
	}
	after := captureGoContext()
	if before.namespace != after.namespace || !reflect.DeepEqual(before.processEnv, after.processEnv) {
		t.Fatal("staging selection changed execution context")
	}
	probe := filepath.Join(first, "write")
	if err := os.WriteFile(probe, []byte("writable"), 0o600); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", blocked)
	if got, err := resolve(); err != nil || got != blocked {
		t.Fatalf("explicit fallback root: %q, %v", got, err)
	}
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", t.TempDir())
	want, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := resolve(); err != nil || got != want {
		t.Fatalf("HOME override: %q, %v", got, err)
	}
}

func TestDriverStageCacheRejectsUnsafeRoot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := testStageCacheDir(alias, os.Getenv("HOME"))(); err == nil {
		t.Fatal("accepted aliased staging root")
	}
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := testStageCacheDir(target, os.Getenv("HOME"))(); err == nil {
		t.Fatal("accepted public staging root")
	}
}
