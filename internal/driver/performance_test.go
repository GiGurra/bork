package driver

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"slices"
	"strings"
	"testing"
)

// BenchmarkCompiler covers every example without executing their effects.
// Run and test latency are measured by scripts/benchmark.py in fresh CLI processes.
func BenchmarkCompiler(b *testing.B) {
	entries, err := os.ReadDir("../../examples")
	if err != nil {
		b.Fatal(err)
	}
	paths := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() {
			paths[entry.Name()] = filepath.Join("../../examples", entry.Name())
		}
	}
	for _, size := range []int{100, 1000} {
		paths[fmt.Sprintf("synthetic%d", size)] = syntheticProgram(b, size)
	}
	// Stable order makes benchmark output and workload order reproducible.
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		path := paths[name]
		b.Run(name, func(b *testing.B) {
			b.Run("check", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, _, err := Check(path); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("emit", func(b *testing.B) {
				b.ReportAllocs()
				var src []byte
				for b.Loop() {
					src, err = Emit(path)
					if err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(len(src)), "go-bytes")
			})
		})
	}
}

// BenchmarkCompilerPhases excludes preceding/following phases from each timed
// interval and allocation count. Profiles still contain setup; CPU labels let
// pprof isolate the phase. Each iteration reloads syntax because checking mutates it.
func BenchmarkCompilerPhases(b *testing.B) {
	paths := []struct{ name, path string }{
		{"hello", "../../examples/hello"},
		{"calculator", "../../examples/calculator"},
		{"signup_api", "../../examples/signup_api"},
		{"synthetic100", syntheticProgram(b, 100)},
		{"synthetic1000", syntheticProgram(b, 1000)},
	}
	for _, item := range paths {
		b.Run(item.name, func(b *testing.B) {
			for _, target := range []string{"parse", "module", "check", "lower", "contracts", "embeds", "effects", "lifetimes", "facts", "generate", "go_build"} {
				b.Run(target, func(b *testing.B) {
					out := filepath.Join(b.TempDir(), "program")
					// Prime process-local metadata and the Go cache independently of
					// which phase filters or benchmark order the caller selects.
					files, info, src, err := emitObserved(item.path, nil)
					if err != nil {
						b.Fatal(err)
					}
					if target == "go_build" {
						if err := buildGo(files, src, out, info.Embeds...); err != nil {
							b.Fatal(err)
						}
					}
					b.ReportAllocs()
					b.ResetTimer()
					b.StopTimer()
					active := false
					observe := func(name string) {
						if active {
							b.StopTimer()
							active = false
						}
						ctx := pprof.WithLabels(context.Background(), pprof.Labels("phase", name, "program", item.name))
						pprof.SetGoroutineLabels(ctx)
						if name == target {
							b.StartTimer()
							active = true
						}
					}
					for range b.N {
						files, info, src, err := emitObserved(item.path, observe)
						if err != nil {
							b.Fatal(err)
						}
						if target == "go_build" {
							observe("go_build")
							if err := buildGo(files, src, out, info.Embeds...); err != nil {
								b.Fatal(err)
							}
						}
						observe("")
					}
					pprof.SetGoroutineLabels(context.Background())
				})
			}
		})
	}
}

func syntheticProgram(b *testing.B, count int) string {
	b.Helper()
	dir := b.TempDir()
	var src strings.Builder
	for i := range count {
		fmt.Fprintf(&src, "fn value%d(x: Int): Int {\n  x + %d\n}\n\n", i, i)
	}
	src.WriteString("fn main() {\n")
	for i := range count {
		fmt.Fprintf(&src, "  assert(value%d(1) == %d)\n", i, i+1)
	}
	src.WriteString("}\n\ntest \"generated functions\" {\n  assert(value0(1) == 1)\n}\n")
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(src.String()), 0o644); err != nil {
		b.Fatal(err)
	}
	return dir
}

func BenchmarkCompilerTest(b *testing.B) {
	path := syntheticProgram(b, 1000)
	for b.Loop() {
		code, err := Test(path, io.Discard, TestOptions{})
		if err != nil || code != 0 {
			b.Fatalf("test: code %d, error %v", code, err)
		}
	}
}

// Observation must preserve emitted bytes and the order of compiler passes.
func TestObservedEmission(t *testing.T) {
	path := "../../examples/hello"
	want, err := Emit(path)
	if err != nil {
		t.Fatal(err)
	}
	var phases []string
	_, _, got, err := emitObserved(path, func(name string) { phases = append(phases, name) })
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("observation changed generated Go")
	}
	expected := []string{"parse", "module", "check", "lower", "contracts", "embeds", "effects", "lifetimes", "facts", "generate"}
	if !slices.Equal(phases, expected) {
		t.Fatalf("phases = %v, want %v", phases, expected)
	}
}
