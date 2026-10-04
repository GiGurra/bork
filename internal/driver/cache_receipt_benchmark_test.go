package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// BenchmarkCacheReceiptPrerequisites measures uncached identity work each time.
// It is not a disk-hit benchmark: persisted results are not enabled yet. An
// optional real CLI image avoids substituting the larger Go test executable.
func BenchmarkCacheReceiptPrerequisites(b *testing.B) {
	launcher, err := exec.LookPath("go")
	if err != nil {
		b.Fatal(err)
	}
	image := os.Getenv("BORK_BENCH_COMPILER_IMAGE")
	hashImage := func() error {
		if image != "" {
			_, _, err := captureGoToolEvidence(image)
			return err
		}
		_, err := hashCompilerImage()
		return err
	}
	root := b.TempDir()
	file := filepath.Join(root, "main.bork")
	if err := os.WriteFile(file, []byte("fn main() {}\n"), 0600); err != nil {
		b.Fatal(err)
	}
	inputs := newSourceSnapshot()
	if _, err := inputs.readFile(file); err != nil {
		b.Fatal(err)
	}
	if _, err := inputs.directory(root); err != nil {
		b.Fatal(err)
	}
	if _, err := inputs.readFile(filepath.Join(root, ModFile)); !os.IsNotExist(err) {
		b.Fatal(err)
	}
	receipt, err := inputs.receipt()
	if err != nil {
		b.Fatal(err)
	}
	replay, err := receipt.snapshot()
	if err != nil {
		b.Fatal(err)
	}
	cases := []struct {
		name string
		run  func() error
	}{
		{"compiler-sha", hashImage},
		{"launcher-sha", func() error { _, _, err := captureGoToolEvidence(launcher); return err }},
		{"source-validation", func() error {
			if !replay.current() {
				b.Fatal("receipt not current")
			}
			return nil
		}},
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
	b.Run("serial", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, benchmark := range cases {
				if err := benchmark.run(); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	b.Run("overlapped", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var compilerErr, launcherErr error
			var join sync.WaitGroup
			join.Add(2)
			go func() { defer join.Done(); compilerErr = hashImage() }()
			go func() { defer join.Done(); _, _, launcherErr = captureGoToolEvidence(launcher) }()
			if !replay.current() {
				b.Fatal("receipt not current")
			}
			join.Wait()
			if compilerErr != nil {
				b.Fatal(compilerErr)
			}
			if launcherErr != nil {
				b.Fatal(launcherErr)
			}
		}
	})
}
