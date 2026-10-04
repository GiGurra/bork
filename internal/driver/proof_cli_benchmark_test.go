package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Profile the one-shot CLI compiler path after a host-string edit. Clear the
// process-local Go name cache to model a fresh CLI request; the SDK object cache
// and stable stage paths remain warm. Process startup/hashing are excluded.
func BenchmarkProofCLIEditPhases(b *testing.B) {
	b.Setenv("CGO_ENABLED", "0")
	b.Setenv("GOMAXPROCS", "2")
	root := b.TempDir()
	for _, name := range []string{"bork.mod", "main.bork", "settings/settings.bork"} {
		data, err := os.ReadFile(filepath.Join("../../examples/config", name))
		if err != nil {
			b.Fatal(err)
		}
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			b.Fatal(err)
		}
	}
	original, err := os.ReadFile(filepath.Join(root, "main.bork"))
	if err != nil {
		b.Fatal(err)
	}
	exe := filepath.Join(b.TempDir(), "program")
	if err := Build(root, exe); err != nil {
		b.Fatal(err)
	}
	times := map[string]time.Duration{}
	previous := ""
	since := time.Now()
	observe := func(next string) {
		now := time.Now()
		if previous != "" {
			times[previous] += now.Sub(since)
		}
		previous, since = next, now
	}
	b.ResetTimer()
	for i := range b.N {
		b.StopTimer()
		text := bytes.ReplaceAll(original, []byte("localhost"), []byte("localhost"+strconv.Itoa(i+1)))
		if err := os.WriteFile(filepath.Join(root, "main.bork"), text, 0600); err != nil {
			b.Fatal(err)
		}
		standardGoNames.Clear()
		b.StartTimer()
		program, source, err := emitProgramObserved(root, observe)
		if err != nil {
			b.Fatal(err)
		}
		observe("go_build_stage")
		if err := buildGoWithContext(program.files, source, exe, program.module, program.context, program.info.Embeds...); err != nil {
			b.Fatal(err)
		}
		observe("")
	}
	b.StopTimer()
	for phase, duration := range times {
		b.ReportMetric(float64(duration.Nanoseconds())/float64(b.N), phase+"-ns/op")
	}
}
