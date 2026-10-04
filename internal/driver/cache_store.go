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
	namespace := hex.EncodeToString(s.namespace[:])
	return filepath.Join("results", "v2", namespace[:2], namespace)
}
func cacheArtifactFilename(key [sha256.Size]byte) string { return hex.EncodeToString(key[:]) + ".json" }
func (s cacheStore) path(key [sha256.Size]byte) string {
	name := cacheArtifactFilename(key)
	return filepath.Join(s.directory(), name[:2], name)
}
func cacheIndexPath(key [sha256.Size]byte) string {
	name := cacheArtifactFilename(key)
	return filepath.Join("indexes", "v1", name[:2], name)
}

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
	file, err := openCacheFile(root, s.path(key), os.O_RDONLY, 0)
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
	if err := ensureStageDirectory(root, filepath.Join("locks", "results-v2"), 0700); err != nil {
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
	directory := filepath.Dir(s.path(body.Key))
	if err := ensureStageDirectory(root, directory, 0700); err != nil {
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
	if err := root.Rename(temporary, s.path(body.Key)); err != nil {
		return err
	}
	// A locator failure leaves a valid artifact; compilation/publication succeed.
	_ = s.writeIndex(root, body.Key)
	return nil
}

func (s cacheStore) lockName(key [sha256.Size]byte) string {
	// Never unlink these 256 files: waiters may hold open descriptors. Mapping
	// is fixed for this storage schema so mixed binaries coordinate correctly.
	var input [2 * sha256.Size]byte
	copy(input[:sha256.Size], s.namespace[:])
	copy(input[sha256.Size:], key[:])
	slot := sha256.Sum256(input[:])
	return filepath.Join("locks", "results-v2", fmt.Sprintf("%02x.lock", slot[0]))
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

// findCacheArtifact uses a direct untrusted namespace locator before compiler
// hashing. Namespace and complete receipt certification still authorize every hit.
func findCacheArtifact(directory string, request cacheArtifactRequest) *cacheArtifactBody {
	key, err := request.key()
	if err != nil {
		return nil
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	namespace, err := readCacheIndex(root, key)
	if err != nil {
		return nil
	}
	body := (cacheStore{root: directory, namespace: namespace}).read(request)
	if body == nil {
		return nil
	}
	inputs, err := body.Source.snapshot()
	if err != nil || !inputs.current() {
		return nil
	}
	return body
}

func readCacheIndex(root *os.Root, key [sha256.Size]byte) ([sha256.Size]byte, error) {
	return readCacheNamespace(root, cacheIndexPath(key))
}
func readCacheNamespace(root *os.Root, path string) ([sha256.Size]byte, error) {
	var namespace [sha256.Size]byte
	file, err := openCacheFile(root, path, os.O_RDONLY, 0)
	if err != nil {
		return namespace, err
	}
	defer func() { _ = file.Close() }()
	// Exactly one lowercase SHA-256 namespace, not a filesystem path.
	data, err := io.ReadAll(io.LimitReader(file, 65))
	if err != nil || len(data) != 64 {
		return namespace, errInvalidCacheArtifact
	}

	namespace, ok := cacheHexDigest(string(data))
	if !ok {
		return namespace, errInvalidCacheArtifact
	}
	return namespace, nil
}

func (s cacheStore) writeIndex(root *os.Root, key [sha256.Size]byte) error {
	path := cacheIndexPath(key)
	if err := ensureStageDirectory(root, filepath.Dir(path), 0700); err != nil {
		return err
	}
	pending := path + ".tmp-" + rand.Text()
	file, err := openCacheFile(root, pending, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(pending) }()
	_, writeErr := file.Write([]byte(hex.EncodeToString(s.namespace[:])))
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename(pending, path)
}
