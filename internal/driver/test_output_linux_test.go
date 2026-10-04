package driver

import (
	"crypto/sha256"
	"debug/buildinfo"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Each positive golden/example leaf owns one fixed output slot. Targets survive
// successful tests; source fixtures never do. Toolchain/environment/source
// changes are checked by a fresh plain Go build, rather than by this helper.
// Call only once per leaf, with no descendants acquiring output slots. Hold the
// lock through build and execution. Delete test-outputs-v1 to wipe retained
// outputs when tests are idle; production clean does not own this test layer.
func fixtureOutputPath(t *testing.T) string {
	t.Helper()
	cache, err := goStageCacheDir()
	if err == nil {
		if out, ok := persistentFixtureOutput(t, filepath.Join(cache, "bork", "test-outputs-v1"), t.Name()); ok {
			return out
		}
	}
	return filepath.Join(t.TempDir(), "program")
}

func persistentFixtureOutput(t *testing.T, base, identity string) (string, bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(base), 0700); err != nil {
		return "", false
	}
	if _, err := privateTestCacheDir(base); err != nil {
		return "", false
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
	slot := filepath.Join(base, key)
	if _, err := privateTestCacheDir(slot); err != nil {
		return "", false
	}
	root, err := os.OpenRoot(slot)
	if err != nil {
		return "", false
	}
	defer func() { _ = root.Close() }()
	lock, err := (cacheStore{root: slot}).lock(root, "LOCK")
	if err != nil {
		return "", false
	}
	out := filepath.Join(slot, "program")
	if info, err := os.Lstat(out); err == nil {
		stat, owned := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || !owned || stat.Uid != uint32(os.Getuid()) {
			_ = lock.Close()
			return "", false
		}
		// Missing/partial targets simply take Go's ordinary fresh build path.
		_, invalid := buildinfo.ReadFile(out)
		if invalid != nil || info.Size() == 0 || info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0022 != 0 {
			if err := os.Remove(out); err != nil {
				_ = lock.Close()
				return "", false
			}
		}
	} else if !os.IsNotExist(err) {
		_ = lock.Close()
		return "", false
	}
	t.Cleanup(func() {
		if t.Failed() {
			_ = os.Remove(out)
		}
		_ = lock.Close()
	})
	return out, true
}
