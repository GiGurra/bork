package driver

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkCacheResultAccounting measures result admission over recognizable
// small artifact envelopes with valid request headers. Receipts/payloads are
// omitted: they are not read by accounting. It excludes encoding/publication.
func BenchmarkCacheResultAccounting(b *testing.B) {
	for _, count := range []int{1, 128, 512, 1024} {
		b.Run(fmt.Sprintf("entries%d", count), func(b *testing.B) {
			base := b.TempDir()
			store := cacheStore{root: filepath.Join(base, "cache"), namespace: sha256.Sum256([]byte("accounting-benchmark"))}
			if err := os.MkdirAll(filepath.Join(store.root, store.directory()), 0700); err != nil {
				b.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(store.root, "locks", "results-v1"), 0700); err != nil {
				b.Fatal(err)
			}
			var measured [sha256.Size]byte
			for index := range count {
				path := filepath.Join(base, fmt.Sprintf("main-%d.bork", index))
				if err := os.WriteFile(path, []byte("fn main() {}\n"), 0600); err != nil {
					b.Fatal(err)
				}
				request := cacheArtifactRequest{Path: path, Cwd: base, Emit: true}
				key, err := request.key()
				if err != nil {
					b.Fatal(err)
				}
				if index == 0 {
					measured = key
				}
				body, err := json.Marshal(cacheArtifactBody{Schema: cacheArtifactSchema, Namespace: store.namespace, Key: key, Request: request})
				if err != nil {
					b.Fatal(err)
				}
				data, err := json.Marshal(cacheArtifactEnvelope{Schema: cacheArtifactSchema, Body: body})
				if err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(store.root, store.directory(), cacheArtifactFilename(key)), data, 0600); err != nil {
					b.Fatal(err)
				}
			}
			root, err := os.OpenRoot(store.root)
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			slot, err := store.lock(root, store.lockName(measured))
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = slot.Close() }()
			mutation, err := store.lock(root, "mutation.lock")
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = mutation.Close() }()
			b.ReportAllocs()
			for b.Loop() {
				if err := store.reserveResult(root, measured, 1024); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
