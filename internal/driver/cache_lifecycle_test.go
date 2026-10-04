package driver

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheResultPublicationDoesNotScanOrEvict(t *testing.T) {
	body, _ := cacheArtifactFixture(t)
	store := cacheStore{root: t.TempDir(), namespace: body.Namespace}
	if err := store.write(body); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store.root, filepath.Dir(store.path(body.Key)))
	for index := range 4097 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("unknown-%d", index)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	broken := sha256.Sum256([]byte("unrelated corrupt result"))
	brokenPath := filepath.Join(store.root, store.path(broken))
	if err := os.MkdirAll(filepath.Dir(brokenPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brokenPath, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	// A full-inventory publisher would reject the unrelated large/corrupt tree.
	if err := store.write(body); err != nil {
		t.Fatal(err)
	}
	if store.read(body.Request) == nil || findCacheArtifact(store.root, body.Request) == nil {
		t.Fatal("direct lookup failed")
	}
	if data, err := os.ReadFile(brokenPath); err != nil || string(data) != "broken" {
		t.Fatal("unrelated entry changed")
	}
}
func TestCacheResultLocatorAlternatingCompilers(t *testing.T) {
	body, _ := cacheArtifactFixture(t)
	other := *body
	other.Namespace = sha256.Sum256([]byte("other compiler"))
	other.GoSource = append(bytes.Clone(body.GoSource), []byte("\n// other compiler\n")...)
	base := t.TempDir()
	for _, current := range []*cacheArtifactBody{body, &other, body, &other} {
		store := cacheStore{root: base, namespace: current.Namespace}
		if err := store.write(current); err != nil {
			t.Fatal(err)
		}
		candidate := findCacheArtifact(base, current.Request)
		if candidate == nil || candidate.Namespace != current.Namespace || !bytes.Equal(candidate.GoSource, current.GoSource) {
			t.Fatal("locator selected wrong compiler payload")
		}
		// The CLI rejects this namespace for the other running compiler and compiles
		// freshly; a locator never authorizes a cross-namespace hit.
		previous := body.Namespace
		if current.Namespace == body.Namespace {
			previous = other.Namespace
		}
		if candidate.Namespace == previous {
			t.Fatal("other compiler could accept locator")
		}
	}
}
func TestCacheResultLocatorFailurePreservesArtifact(t *testing.T) {
	body, _ := cacheArtifactFixture(t)
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "indexes"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	store := cacheStore{root: base, namespace: body.Namespace}
	if err := store.write(body); err != nil {
		t.Fatal("locator failure failed publication:", err)
	}
	if store.read(body.Request) == nil {
		t.Fatal("artifact lost")
	}
	if findCacheArtifact(base, body.Request) != nil {
		t.Fatal("unavailable locator selected an artifact")
	}
	if err := os.Remove(filepath.Join(base, "indexes")); err != nil {
		t.Fatal(err)
	}
	if err := store.write(body); err != nil {
		t.Fatal(err)
	}
	if findCacheArtifact(base, body.Request) == nil {
		t.Fatal("locator did not recover")
	}
	path := filepath.Join(base, cacheIndexPath(body.Key))
	for _, contents := range []string{"", "../outside", fmt.Sprintf("%x", body.Namespace) + "extra"} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if findCacheArtifact(base, body.Request) != nil {
			t.Fatal("malformed locator accepted")
		}
	}
}
func TestCacheResultTouch(t *testing.T) {
	body, _ := cacheArtifactFixture(t)
	store := cacheStore{root: t.TempDir(), namespace: body.Namespace}
	if err := store.write(body); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.root, store.path(body.Key))
	old := time.Unix(100, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	store.touch(body.Key)
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().After(old) {
		t.Fatalf("touch: %v", err)
	}
	root, err := os.OpenRoot(store.root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	mutation, err := store.lock(root, "mutation.lock")
	if err != nil {
		t.Fatal(err)
	}
	store.touch(body.Key)
	_ = mutation.Close()
}
