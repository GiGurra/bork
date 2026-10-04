package driver

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Each hit uses fresh receipt restoration, as a fresh CLI process does. Compiler
// and launcher startup hashing are measured separately; the CLI overlaps them.
func BenchmarkCacheHitBreakdown(b *testing.B) {
	b.Setenv("GOPACKAGESDRIVER", "off")
	b.Setenv("GOTOOLCHAIN", "local")
	root := b.TempDir()
	path := filepath.Join(root, "main.bork")
	input := []byte("fn main(){}\n")
	if err := os.WriteFile(path, input, 0600); err != nil {
		b.Fatal(err)
	}
	session := NewSession()
	emitted, err := session.Emit(path)
	if err != nil {
		b.Fatal(err)
	}
	if session.last == nil {
		b.Skip("ineligible fixture")
	}
	body, err := cacheArtifactFrom(session.last, session.last.sourcePaths, sha256.Sum256([]byte("hit benchmark")))
	if err != nil {
		b.Fatal(err)
	}
	encoded, err := encodeCacheArtifact(body)
	if err != nil {
		b.Fatal(err)
	}
	cases := []struct {
		name string
		run  func() error
	}{
		{"decode", func() error {
			_, err := decodeCacheArtifact(bytes.NewReader(encoded), body.Namespace, body.Key)
			return err
		}},
		{"source-receipt-hashing", func() error {
			inputs, err := body.Source.snapshot()
			if err != nil {
				return err
			}
			if !inputs.current() {
				b.Fatal("changed inputs")
			}
			return nil
		}},
		{"go-receipt-restore-including-launcher", func() error { _, err := body.Go.restore(resolveGoContext()); return err }},
		{"launcher-content-hash", func() error { _, _, err := captureGoToolEvidence(body.Go.Tool); return err }},
		{"sdk-name-receipt-hashing", func() error {
			for _, receipt := range body.Names {
				name, err := receipt.restore(&goContextValidation{root: body.Go.Root})
				if err != nil {
					return err
				}
				if !name.inputs.currentMetadata() {
					b.Fatal("changed SDK")
				}
			}
			return nil
		}},
		{"installed-sdk-hit-including-decode", func() error {
			hit, err := decodeCacheArtifact(bytes.NewReader(encoded), body.Namespace, body.Key)
			if err != nil {
				return err
			}
			_, err = hit.validateWithContext(body.Request, body.Namespace, func(receipt *goContextReceipt) (*goContext, error) {
				return receipt.restoreInstalledSDK(resolveGoContext())
			})
			return err
		}},
		{"cli-style-hit-overlapped-launcher", func() error {
			var digest [sha256.Size]byte
			var evidence *goToolEvidence
			var toolErr error
			resolved := resolveGoContext()
			var join sync.WaitGroup
			join.Add(1)
			go func() { defer join.Done(); digest, evidence, toolErr = captureGoToolEvidence(resolved.tool) }()
			hit, err := decodeCacheArtifact(bytes.NewReader(encoded), body.Namespace, body.Key)
			join.Wait()
			if err != nil {
				return err
			}
			if toolErr != nil {
				return toolErr
			}
			_, err = hit.validateWithContext(body.Request, body.Namespace, func(receipt *goContextReceipt) (*goContext, error) {
				return receipt.restoreWithToolEvidence(resolved, digest, evidence)
			})
			return err
		}},
		{"validated-hit-without-decoding", func() error { _, err := body.validate(body.Request, body.Namespace); return err }},
		{"uncached-check", func() error { _, _, err := Check(path); return err }},
		{"uncached-emit", func() error { _, err := Emit(path); return err }},
		{"uncached-build-stable-output", func() error { return Build(path, filepath.Join(root, "program")) }},
	}
	for _, item := range cases {
		b.Run(item.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := item.run(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(len(input)), "bork-bytes")
			b.ReportMetric(float64(len(emitted)), "go-bytes")
			b.ReportMetric(float64(len(encoded)), "receipt-bytes")
		})
	}
}
