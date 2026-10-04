package driver

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// BenchmarkCachePublicationStages separates first-session work from receipt,
// encoding and publication costs. It uses warm filesystem/Go caches; the actual
// CLI compiler/launcher startup hashes are measured separately.
func BenchmarkCachePublicationStages(b *testing.B) {
	if runtime.GOOS != "linux" {
		b.Skip("running image receipts currently Linux-only")
	}
	b.Setenv("GOPACKAGESDRIVER", "off")
	b.Setenv("GOTOOLCHAIN", "local")
	root := b.TempDir()
	path := filepath.Join(root, "main.bork")
	if err := os.WriteFile(path, []byte("fn main() {}\n"), 0600); err != nil {
		b.Fatal(err)
	}
	session := NewSession()
	if _, err := session.Emit(path); err != nil {
		b.Fatal(err)
	}
	artifact := session.last
	if artifact == nil {
		b.Skipf("unsupported inventory: %+v", session.Stats())
	}
	namespace := sha256.Sum256([]byte("publication benchmark"))
	body, err := cacheArtifactFrom(artifact, artifact.sourcePaths, namespace)
	if err != nil {
		b.Fatal(err)
	}
	store := cacheStore{root: filepath.Join(root, "cache"), namespace: namespace}
	cases := []struct {
		name string
		run  func() error
	}{
		{"session-compile", func() error { _, err := NewSession().Emit(path); return err }},
		{"source-receipt", func() error { _, err := artifact.inputs.receipt(); return err }},
		{"go-receipt", func() error { _, err := artifact.context.receipt(); return err }},
		{"metadata-receipts", func() error {
			for _, name := range artifact.names {
				if _, err := name.receipt(); err != nil {
					return err
				}
			}
			return nil
		}},
		{"artifact-receipt", func() error { _, err := cacheArtifactFrom(artifact, artifact.sourcePaths, namespace); return err }},
		{"encode", func() error { _, err := encodeCacheArtifact(body); return err }},
		{"atomic-store-including-encode", func() error { return store.write(body) }},
	}
	for _, benchmark := range cases {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := benchmark.run(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
