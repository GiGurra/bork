package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GiGurra/bork/internal/gen"
)

func TestExecutionObservationInvalidEndpointReleasesStage(t *testing.T) {
	observations := executionObservations{Invocations: 1, Captured: 1}
	cleaned := false
	// An invalid seal must fail before spawning Go or trusting any path/input.
	inventory := &goExecutionInventory{Version: goExecutionInventoryVersion}
	inventory.seal = inventory.identity()
	inventory.Invocation.Mode = "changed after capture"
	observations.finish(inventory, func() { cleaned = true })
	if !cleaned || observations.Validated != 0 || observations.Declined != 1 || observations.LastDecline == "" {
		t.Fatalf("invalid endpoint lost decline or cleanup: %+v, cleanup=%t", observations, cleaned)
	}
	observations.decline(executionDecline(strings.Repeat("x", 513)))
	if len(observations.LastDecline) > 512 {
		t.Fatal("accounting retained unbounded diagnostics")
	}
}

func TestExecutionObservedComptime(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("initial Go inventory supports Linux amd64")
	}
	savedHook := goModuleHook
	goModuleHook = nil
	t.Cleanup(func() { goModuleHook = savedHook })
	for _, setting := range []string{"GOPACKAGESDRIVER=off", "GOTOOLCHAIN=local", "GOENV=off", "GOWORK=off", "GOFLAGS=", "GOEXPERIMENT=", "GOAMD64=v1", "GOCACHEPROG=", "GO_EXTLINK_ENABLED=0", "GOFIPS140=off", "GO111MODULE=on", "CGO_ENABLED=0"} {
		name, value, _ := strings.Cut(setting, "=")
		t.Setenv(name, value)
	}
	ctx := captureGoContext()
	if ctx.err != nil || !supportedGoVersion(ctx.values["GOVERSION"]) {
		t.Skip("requires supported native Go launcher")
	}
	stageRoot := t.TempDir()
	output := filepath.Join(t.TempDir(), "eval")
	stage := goExecutionStage{Root: stageRoot, Mode: "comptime", Output: output, Program: []byte("package main"), Module: &goModuleInputs{mod: []byte("module example.com/observed")}}
	invocation, err := goExecutionEnvelope(ctx, stage)
	if err != nil || !slices.Equal(invocation.BuildArgs, []string{"build", "-mod=readonly", "-buildvcs=false", "-ldflags=-linkmode=internal", "-o", output, "."}) {
		t.Fatalf("actual output argv not bound: %+v, %v", invocation, err)
	}
	for _, invalid := range []string{"relative-output", filepath.Join(stageRoot, "eval"), string([]byte{'/', 0xff})} {
		stage.Output = invalid
		if _, err := goExecutionEnvelope(ctx, stage); err == nil {
			t.Fatalf("invalid output qualified: %q", invalid)
		}
	}
	for _, tc := range []struct {
		name, source string
		captured     bool
	}{
		{"pure", `fn main(){println(comptime{21*2})}`, true},
		{"foreign unsafe", "fn value():Int unsafe go{return 42}\nfn main(){println(comptime{value()})}", false},
		{"CGO enabled", `fn main(){println(comptime{42})}`, false},
		{"timeout releases stage", "fn spin():Int{spin()}\nfn main(){println(comptime{spin()})}", true},
		{"empty", `fn main(){println(42)}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := ctx
			if tc.name == "CGO enabled" {
				t.Setenv("CGO_ENABLED", "1")
				ctx = captureGoContext()
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ModFile), []byte("module example.com/observed\nunsafe \"example.com/observed\"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			compile := func(usage *goUsage) []byte {
				loaded, module, err := loadCompilationInputs(dir, nil)
				if err != nil {
					t.Fatal(err)
				}
				program, err := checkLoadedProgramTracked(loaded, module, ctx, captureEmbedsSnapshot, usage, nil)
				if err != nil {
					t.Fatal(err)
				}
				source, err := gen.Package(program.files, program.info)
				if err != nil {
					t.Fatal(err)
				}
				return source
			}
			usage := &goUsage{}
			if tc.name == "timeout releases stage" {
				limited := *ctx
				limited.evalLimit = 150 * time.Millisecond
				loaded, module, err := loadCompilationInputs(dir, nil)
				if err != nil {
					t.Fatal(err)
				}
				_, err = checkLoadedProgramTracked(loaded, module, &limited, captureEmbedsSnapshot, usage, nil)
				if err == nil || !strings.Contains(err.Error(), "evaluation exceeded 150ms") {
					t.Fatalf("tracked timeout changed diagnostic: %v", err)
				}
				if usage.executions.Captured != 1 || usage.executions.Validated != 1 {
					t.Fatalf("timeout did not complete endpoint validation: %+v", usage.executions)
				}
				// The next execution uses the same stage slot. Completion proves
				// that cancellation released the previous stage lock.
				if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(`fn main(){println(comptime{42})}`), 0600); err != nil {
					t.Fatal(err)
				}
				compile(nil)
				if _, eligible := usage.execution.receipts(); eligible {
					t.Fatal("timeout qualified execution reuse")
				}
				return
			}
			observed := compile(usage)
			if !bytes.Equal(observed, compile(nil)) {
				t.Fatal("tracked execution changed generated output")
			}
			counts := usage.executions
			if tc.name == "empty" {
				if usage.evaluator || counts != (executionObservations{}) || usage.execution != nil {
					t.Fatal("empty phase collected execution inputs")
				}
				return
			}
			if !usage.evaluator || counts.Invocations != 1 || sessionBypassReason(ctx, usage) != "compile-time evaluator" {
				t.Fatalf("execution lost evaluator bypass: %+v", counts)
			}
			if usage.execution == nil {
				t.Fatal("actual invocation was not accounted for")
			}
			if _, eligible := usage.execution.receipts(); eligible {
				t.Fatal("Go-only observation certified an execution receipt")
			}
			if tc.captured {
				if counts.Captured != 1 || counts.Validated != 1 || counts.Declined != 0 || counts.LastGoIdentity == ([32]byte{}) {
					t.Fatalf("real execution inventory incomplete: %+v", counts)
				}
			} else if counts.Captured != 0 || counts.Validated != 0 || counts.Declined != 1 || counts.LastDecline == "" {
				t.Fatalf("unsafe selection collected execution closure: %+v", counts)
			}
		})
	}
}
