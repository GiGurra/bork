package driver

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime/pprof"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GiGurra/bork/internal/gen"
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
		{"config", "../../examples/config"},
		{"http_server", "../../examples/http_server"},
		{"synthetic100", syntheticProgram(b, 100)},
		{"synthetic1000", syntheticProgram(b, 1000)},
	}
	for _, item := range paths {
		b.Run(item.name, func(b *testing.B) {
			for _, target := range []string{"parse", "module", "configuration", "check", "lower", "contracts", "embeds", "effects", "lifetimes", "comptime", "facts", "generate", "go_build"} {
				b.Run(target, func(b *testing.B) {
					out := filepath.Join(b.TempDir(), "program")
					// Prime process-local metadata and the Go cache independently of
					// which phase filters or benchmark order the caller selects.
					files, info, src, err := emitObserved(item.path, nil)
					if err != nil {
						b.Fatal(err)
					}
					if target == "comptime" && len(info.Comptimes) == 0 {
						b.Skip("program has no comptime expressions")
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
	t.Parallel()
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
	expected := []string{"parse", "module", "configuration", "check", "lower", "contracts", "embeds", "effects", "lifetimes", "facts", "generate"}
	if !slices.Equal(phases, expected) {
		t.Fatalf("phases = %v, want %v", phases, expected)
	}
}

// BenchmarkSession includes content validation of configuration, tools and
// package names on hits; it must not be compared with fresh CLI startup.
func BenchmarkSession(b *testing.B) {
	for _, corpus := range []struct{ name, path string }{
		{"hello", "../../examples/hello"},
		{"synthetic1000", syntheticProgram(b, 1000)},
	} {
		b.Run(corpus.name, func(b *testing.B) {
			path := corpus.path
			for _, emit := range []bool{false, true} {
				name := "check"
				if emit {
					name = "emit"
				}
				b.Run(name, func(b *testing.B) {
					request := func(session *Session) error {
						if emit {
							_, err := session.Emit(path)
							return err
						}
						_, err := session.Check(path)
						return err
					}
					b.Run("first", func(b *testing.B) {
						b.ReportAllocs()
						for b.Loop() {
							if err := request(NewSession()); err != nil {
								b.Fatal(err)
							}
						}
					})
					for _, repeat := range []string{"unchanged", "unchanged_stable"} {
						b.Run(repeat, func(b *testing.B) {
							session := NewSession()
							if err := request(session); err != nil {
								b.Fatal(err)
							}
							if repeat == "unchanged_stable" {
								// Establish a real monotonic observation outside the timed loop.
								time.Sleep(goToolTimestampMargin + time.Millisecond)
							}
							b.ReportAllocs()
							for b.Loop() {
								if err := request(session); err != nil {
									b.Fatal(err)
								}
							}
							b.ReportMetric(float64(session.Stats().Hits)/float64(b.N), "hit/op")
						})
					}
				})
			}
		})
	}
}

func BenchmarkSessionValidation(b *testing.B) {
	session := NewSession()
	if _, err := session.Emit("../../examples/hello"); err != nil {
		b.Fatal(err)
	}
	artifact := session.last
	if artifact == nil {
		b.Fatal(session.Stats())
	}
	// Measure established validation after the portable launcher warm-up.
	time.Sleep(goToolTimestampMargin + time.Millisecond)
	b.Run("configuration", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if ctx := captureSessionGoContext(artifact.context); ctx.err != nil {
				b.Fatal(ctx.err)
			}
		}
	})
	b.Run("source_assets", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if !artifact.inputs.current() || !artifact.assets.current() {
				b.Fatal("inputs changed")
			}
		}
	})
	b.Run("names", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, input := range artifact.names {
				if input.inputs != nil {
					if !input.inputs.current() {
						b.Fatal("name inputs changed")
					}
				} else {
					usage := &goUsage{}
					got := (goPackages{module: artifact.module, context: artifact.context, usage: usage}).Names(input.paths)
					if !maps.Equal(got, input.names) {
						b.Fatal("names changed")
					}
				}
			}
		}
	})
}

// BenchmarkBuildStages separates Bork emission from compilation of its emitted
// Go. Setup primes the Go build cache; go_build includes staging and linking,
// but excludes Bork compilation and program execution.
func BenchmarkBuildStages(b *testing.B) {
	for _, item := range []struct{ name, path string }{
		{"config", "../../examples/config"},
		{"http_server", "../../examples/http_server"},
	} {
		b.Run(item.name, func(b *testing.B) {
			program, err := checkProgramObserved(item.path, nil)
			if err != nil {
				b.Fatal(err)
			}
			src, err := gen.Package(program.files, program.info)
			if err != nil {
				b.Fatal(err)
			}
			out := filepath.Join(b.TempDir(), "program")
			if err := buildGoWithContext(program.files, src, out, program.module, program.context, program.info.Embeds...); err != nil {
				b.Fatal(err)
			}
			b.Run("bork_compile", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := Emit(item.path); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(len(src)), "go-bytes")
			})
			b.Run("go_build", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if err := buildGoWithContext(program.files, src, out, program.module, program.context, program.info.Embeds...); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(len(src)), "go-bytes")
			})
		})
	}
}

// BenchmarkSessionFirstPhases reports first-request costs including configuration
// and inventory seeding. Every iteration owns a fresh Session; no result hits.
func BenchmarkSessionFirstPhases(b *testing.B) {
	for _, item := range []struct{ name, path string }{
		{"hello", "../../examples/hello"}, {"config", "../../examples/config"},
		{"http_server", "../../examples/http_server"}, {"synthetic1000", syntheticProgram(b, 1000)},
	} {
		b.Run(item.name, func(b *testing.B) {
			for _, target := range []string{"configuration", "validate", "parse", "module", "check", "lower", "contracts", "embeds", "effects", "lifetimes", "comptime", "facts", "warnings", "generate", "retain"} {
				b.Run(target, func(b *testing.B) {
					// Calibrate against full requests, including very short or absent phases.
					// Otherwise a sub-microsecond phase schedules millions of full compiles.
					var elapsed time.Duration
					var started time.Time
					observed := false
					observe := func(name string) {
						if !started.IsZero() {
							elapsed += time.Since(started)
							started = time.Time{}
						}
						pprof.SetGoroutineLabels(pprof.WithLabels(context.Background(), pprof.Labels("phase", name, "program", item.name)))
						if name == target {
							observed = true
							started = time.Now()
						}
					}
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						session := NewSession()
						session.observe = observe
						if _, err := session.Emit(item.path); err != nil {
							b.Fatal(err)
						}
					}
					pprof.SetGoroutineLabels(context.Background())
					if !observed {
						b.Skip("program does not execute this phase")
					}
					b.ReportMetric(float64(elapsed.Nanoseconds())/float64(b.N), "phase-ns/op")
				})
			}
		})
	}
}

// BenchmarkGoStaging compares identical captured emitted inputs. Each mode
// primes its own cache variant before timing; stable keeps a directory for the
// benchmark's lifetime. It measures staging and Go compilation/linking only.
func BenchmarkGoStaging(b *testing.B) {
	for _, item := range []struct{ name, path string }{
		{"config", "../../examples/config"}, {"http_server", "../../examples/http_server"},
	} {
		b.Run(item.name, func(b *testing.B) {
			program, err := checkProgramObserved(item.path, nil)
			if err != nil {
				b.Fatal(err)
			}
			src, err := gen.Package(program.files, program.info)
			if err != nil {
				b.Fatal(err)
			}
			for _, mode := range []string{"random", "trimpath", "stable"} {
				b.Run(mode, func(b *testing.B) {
					stable := filepath.Join(b.TempDir(), "stage")
					out := filepath.Join(b.TempDir(), "program")
					build := func() {
						dir := stable
						if mode != "stable" {
							var err error
							dir, err = os.MkdirTemp("", "bork-stage-benchmark-*")
							if err != nil {
								b.Fatal(err)
							}
							defer func() { _ = os.RemoveAll(dir) }()
						}
						if err := os.MkdirAll(dir, 0o755); err != nil {
							b.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(dir, "main.go"), src, 0o644); err != nil {
							b.Fatal(err)
						}
						if err := stageEmbeds(dir, program.info.Embeds); err != nil {
							b.Fatal(err)
						}
						if _, err := program.module.write(dir, program.context.moduleHook); err != nil {
							b.Fatal(err)
						}
						args := []string{"build", "-mod=readonly", "-buildvcs=false", "-o", out, "."}
						if mode == "trimpath" {
							args = append([]string{"build", "-trimpath"}, args[1:]...)
						}
						cmd := program.context.command(args...)
						cmd.Dir = dir
						cmd.Env = append(cmd.Env, "GOWORK=off", "GOFLAGS=")
						if output, err := cmd.CombinedOutput(); err != nil {
							b.Fatalf("%v:\n%s", err, output)
						}
					}
					build()
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						build()
					}
					b.ReportMetric(float64(len(src)), "go-bytes")
				})
			}
		})
	}
}
