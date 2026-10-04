package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
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
	path := s.path(key)
	info, err := root.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	now := time.Now()
	_ = root.Chtimes(path, now, now)
}
