package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func cacheHexDigest(value string) ([sha256.Size]byte, bool) {
	var out [sha256.Size]byte
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != len(out) || value != strings.ToLower(value) {
		return out, false
	}
	copy(out[:], raw)
	return out, true
}

func (s cacheStore) tryLock(root *os.Root, name string) (*os.File, error) {
	file, err := openCacheFile(root, name, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := tryLockCacheFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// touch happens only after full receipt validation. It never delays serving a
// valid hit behind a publisher: unavailable/busy lifecycle locks skip the hint.
func (s cacheStore) touch(key [sha256.Size]byte) {
	s.touchAt(key, time.Now())
}

func (s cacheStore) touchAt(key [sha256.Size]byte, now time.Time) {
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return
	}
	defer func() { _ = root.Close() }()
	slot, err := s.tryLock(root, s.lockName(key))
	if err != nil {
		return
	}
	defer func() { _ = slot.Close() }()
	mutation, err := s.tryLock(root, "mutation.lock")
	if err != nil {
		return
	}
	defer func() { _ = mutation.Close() }()
	_ = markCacheUse(root, s.path(key), now, false)
	// A locator is shared by compiler namespaces. Do not refresh another
	// compiler's pointer after last-writer-wins republication.
	if namespace, err := readCacheIndex(root, key); err == nil && namespace == s.namespace {
		_ = markCacheUse(root, cacheIndexPath(key), now, false)
	}
}

const cacheUseInterval = time.Hour

// Age is a retention hint, never semantic evidence. Callers hold SLOT and
// MUTATION; marking inspects only the known path, and errors never fail a build.
// Future mtimes remain conservative after wall-clock corrections.
func markCacheUse(root *os.Root, path string, now time.Time, create bool) error {
	if err := validateStageDirectory(root, filepath.Dir(path)); err != nil {
		return err
	}
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && create {
		file, err := openCacheFile(root, path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		return root.Chtimes(path, now, now)
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errInvalidCacheArtifact
	}
	if now.Sub(info.ModTime()) < cacheUseInterval {
		return nil
	}
	return root.Chtimes(path, now, now)
}
