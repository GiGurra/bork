package driver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestCacheStoreRoundtripAndMisses(t *testing.T) {
	t.Parallel()
	body, _ := cacheArtifactFixture(t)
	store := cacheStore{root: filepath.Join(t.TempDir(), "results"), namespace: body.Namespace}
	if store.read(body.Request) != nil {
		t.Fatal("missing cache hit")
	}
	if err := store.write(body); err != nil {
		t.Fatal(err)
	}
	restored := store.read(body.Request)
	if restored == nil || !bytes.Equal(restored.GoSource, body.GoSource) {
		t.Fatal("stored result differs")
	}
	path := filepath.Join(store.root, store.path(body.Key))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("artifact mode %v", info.Mode())
	}
	if err := os.WriteFile(path, []byte("truncated"), 0600); err != nil {
		t.Fatal(err)
	}
	if store.read(body.Request) != nil {
		t.Fatal("corrupt cache hit")
	}
	if err := store.write(body); err != nil {
		t.Fatal(err)
	}
	other := body.Request
	other.Path += "/../main.bork"
	if store.read(other) != nil {
		t.Fatal("raw alias shared an artifact")
	}
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	unavailable := cacheStore{root: blocked, namespace: body.Namespace}
	if unavailable.read(body.Request) != nil {
		t.Fatal("unavailable cache hit")
	}
	if err := unavailable.write(body); err == nil {
		t.Fatal("blocked cache unexpectedly writable")
	}
}

func TestCacheStoreConcurrentReplacement(t *testing.T) {
	t.Parallel()
	body, _ := cacheArtifactFixture(t)
	store := cacheStore{root: t.TempDir(), namespace: body.Namespace}
	if err := store.write(body); err != nil {
		t.Fatal(err)
	}
	other := *body
	other.GoSource = append(bytes.Clone(body.GoSource), []byte("\n// second complete generation\n")...)
	var workers sync.WaitGroup
	var errors sync.Mutex
	var failures []error
	record := func(err error) {
		if err != nil {
			errors.Lock()
			failures = append(failures, err)
			errors.Unlock()
		}
	}
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			for n := 0; n < 20; n++ {
				generation := body
				if (index+n)%2 != 0 {
					generation = &other
				}
				record(store.write(generation))
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for n := 0; n < 100; n++ {
				read := store.read(body.Request)
				if read == nil || !bytes.Equal(read.GoSource, body.GoSource) && !bytes.Equal(read.GoSource, other.GoSource) {
					t.Error("reader observed partial/missing replacement")
					return
				}
			}
		}()
	}
	workers.Wait()
	if len(failures) != 0 {
		t.Fatal(failures)
	}
	entries, err := os.ReadDir(filepath.Join(store.root, store.directory()))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary files retained: %v", entries)
	}
}

func TestCacheStoreDeclinesSymlinkEscape(t *testing.T) {
	t.Parallel()
	body, _ := cacheArtifactFixture(t)
	outside := t.TempDir()
	root := t.TempDir()
	store := cacheStore{root: root, namespace: body.Namespace}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, store.directory())), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, store.directory())); err != nil {
		t.Fatal(err)
	}
	if err := store.write(body); err == nil {
		t.Fatal("namespace escaped cache root")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("wrote outside cache root")
	}
	if err := os.Remove(filepath.Join(root, store.directory())); err != nil {
		t.Fatal(err)
	}
	if err := store.write(body); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(root, store.path(body.Key))
	if err := os.Rename(artifact, artifact+".target"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(artifact)+".target", artifact); err != nil {
		t.Fatal(err)
	}
	if store.read(body.Request) != nil {
		t.Fatal("followed final artifact symlink")
	}
}

func TestCacheStoreLockPoolBounded(t *testing.T) {
	t.Parallel()
	store := cacheStore{namespace: sha256.Sum256([]byte("namespace"))}
	slots := map[string]bool{}
	for i := 0; i < 4096; i++ {
		key := sha256.Sum256([]byte(hex.EncodeToString([]byte{byte(i), byte(i >> 8)})))
		slots[store.lockName(key)] = true
	}
	if len(slots) > 256 {
		t.Fatal("unbounded lock pool")
	}
}

func TestCacheStoreCrossProcess(t *testing.T) {
	t.Parallel()
	if input := os.Getenv("BORK_CACHE_STORE_HELPER_BODY"); input != "" {
		data, err := os.ReadFile(input)
		if err != nil {
			t.Fatal(err)
		}
		var body cacheArtifactBody
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		store := cacheStore{root: os.Getenv("BORK_CACHE_STORE_HELPER_ROOT"), namespace: body.Namespace}
		body.GoSource = append(body.GoSource, []byte("\n// "+os.Getenv("BORK_CACHE_STORE_HELPER_TAG")+"\n")...)
		for i := 0; i < 10; i++ {
			if err := store.write(&body); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	body, _ := cacheArtifactFixture(t)
	store := cacheStore{root: t.TempDir(), namespace: body.Namespace}
	if err := store.write(body); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(t.TempDir(), "body.json")
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture, data, 0600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var commands []*exec.Cmd
	var outputs []*bytes.Buffer
	for i := 0; i < 4; i++ {
		command := exec.Command(self, "-test.run=^TestCacheStoreCrossProcess$", "-test.timeout=30s")
		command.Env = append(os.Environ(), "BORK_CACHE_STORE_HELPER_BODY="+fixture, "BORK_CACHE_STORE_HELPER_ROOT="+store.root, "BORK_CACHE_STORE_HELPER_TAG="+strconv.Itoa(i))
		output := new(bytes.Buffer)
		command.Stdout = output
		command.Stderr = output
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = command.Process.Kill() })
		commands = append(commands, command)
		outputs = append(outputs, output)
	}
	for i, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("child%d: %v\n%s", i, err, outputs[i].String())
		}
	}
	restored := store.read(body.Request)
	if restored == nil {
		t.Fatal("no complete child generation")
	}
	for i := 0; i < 4; i++ {
		if bytes.Equal(restored.GoSource, append(bytes.Clone(body.GoSource), []byte("\n// "+strconv.Itoa(i)+"\n")...)) {
			return
		}
	}
	t.Fatal("unexpected child output")
}

func TestCacheFindSkipsStaleNamespaces(t *testing.T) {
	t.Parallel()
	body, _ := cacheArtifactFixture(t)
	root := t.TempDir()
	old := *body
	old.Namespace = [32]byte{}
	if err := (cacheStore{root: root, namespace: old.Namespace}).write(&old); err != nil {
		t.Fatal(err)
	}
	// The old compiler's entry retains an earlier source observation.
	data, err := os.ReadFile(body.Request.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(body.Request.Path, append(data, []byte("// edit\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if candidate := findCacheArtifact(root, body.Request); candidate != nil {
		t.Fatal("stale old namespace selected")
	}
	session := NewSession()
	if _, err := session.Emit(body.Request.Path); err != nil {
		t.Fatal(err)
	}
	current, err := cacheArtifactFrom(session.last, body.SourcePaths, body.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if err := (cacheStore{root: root, namespace: current.Namespace}).write(current); err != nil {
		t.Fatal(err)
	}
	candidate := findCacheArtifact(root, current.Request)
	if candidate == nil || candidate.Namespace != current.Namespace {
		t.Fatal("old namespace hides valid current entry")
	}
}
