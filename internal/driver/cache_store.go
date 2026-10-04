package driver

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// cacheStore currently has no production callers. Its explicit root is used by
// tests only; automatic reads/writes wait for eviction and clean support.
// The root is the shared bork cache directory, so staging can coordinate on
// mutation.lock while keeping its own fixed slot pool.
type cacheStore struct {
	root      string
	namespace [sha256.Size]byte
}

func (s cacheStore) directory() string {
	return filepath.Join("results", "v1", hex.EncodeToString(s.namespace[:]))
}
func cacheArtifactFilename(key [sha256.Size]byte) string { return hex.EncodeToString(key[:]) + ".json" }

// read only decodes complete artifacts. It does not certify input receipts or
// refresh last-use timestamps before the caller has validated those receipts.
// Every filesystem/decoding problem is a miss.
func (s cacheStore) read(request cacheArtifactRequest) *cacheArtifactBody {
	key, err := request.key()
	if err != nil {
		return nil
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	file, err := openCacheFile(root, filepath.Join(s.directory(), cacheArtifactFilename(key)), os.O_RDONLY, 0)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > cacheArtifactMaxBytes {
		return nil
	}
	body, err := decodeCacheArtifact(file, s.namespace, key)
	if err != nil {
		return nil
	}
	return body
}

// write holds SLOT then MUTATION, matching the lifecycle lock order. Encoding
// happens before opening the cache; oversize artifacts need no disk allocation.
// Callers must keep successful compilation results even if this returns an error.
func (s cacheStore) write(body *cacheArtifactBody) error {
	if body == nil || body.Namespace != s.namespace {
		return errInvalidCacheArtifact
	}
	encoded, err := encodeCacheArtifact(body)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(filepath.Join("locks", "results-v1"), 0700); err != nil {
		return err
	}
	slot, err := s.lock(root, s.lockName(body.Key))
	if err != nil {
		return err
	}
	defer func() { _ = slot.Close() }()
	mutation, err := s.lock(root, "mutation.lock")
	if err != nil {
		return err
	}
	defer func() { _ = mutation.Close() }()
	directory := s.directory()
	if err := root.MkdirAll(directory, 0700); err != nil {
		return err
	}
	temporary := filepath.Join(directory, ".tmp-"+rand.Text())
	file, err := openCacheFile(root, temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temporary) }()
	_, writeErr := file.Write(encoded)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename(temporary, filepath.Join(directory, cacheArtifactFilename(body.Key)))
}

func (s cacheStore) lockName(key [sha256.Size]byte) string {
	// Never unlink these 256 files: waiters may hold open descriptors. Mapping
	// is fixed for this storage schema so mixed binaries coordinate correctly.
	var input [2 * sha256.Size]byte
	copy(input[:sha256.Size], s.namespace[:])
	copy(input[sha256.Size:], key[:])
	slot := sha256.Sum256(input[:])
	return filepath.Join("locks", "results-v1", fmt.Sprintf("%02x.lock", slot[0]))
}
func (s cacheStore) lock(root *os.Root, name string) (*os.File, error) {
	file, err := openCacheFile(root, name, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errInvalidCacheArtifact
	}
	if err == nil {
		err = lockCacheFile(file)
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// findCacheArtifact locates an untrusted candidate without hashing the running
// compiler first. The caller must certify its actual namespace before serving.
// Namespace enumeration is bounded; excessive or malformed cache trees miss.
func findCacheArtifact(directory string, request cacheArtifactRequest) *cacheArtifactBody {
	if _, err := request.key(); err != nil {
		return nil
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	namespaces, err := root.Open(filepath.Join("results", "v1"))
	if err != nil {
		return nil
	}
	entries, readErr := namespaces.ReadDir(1025)
	_ = namespaces.Close()
	if readErr != nil && readErr != io.EOF || len(entries) > 1024 {
		return nil
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		bytes, err := hex.DecodeString(entry.Name())
		if err != nil || len(bytes) != sha256.Size {
			continue
		}
		var namespace [sha256.Size]byte
		copy(namespace[:], bytes)
		store := cacheStore{root: directory, namespace: namespace}
		if body := store.read(request); body != nil {
			inputs, err := body.Source.snapshot()
			if err == nil && inputs.current() {
				return body
			}
		}
	}
	return nil
}
