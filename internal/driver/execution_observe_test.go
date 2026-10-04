package driver

import (
	"bytes"
	"github.com/GiGurra/bork/internal/check"
	"go/constant"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GiGurra/bork/internal/gen"
)

func TestExecutionObservationCommandAndDecline(t *testing.T) {
	t.Parallel()
	usage := &goUsage{}
	observation := beginExecutionObservation(usage, nil, "predicate", time.Second)
	cmd := exec.Command("/go", "build", "-o", "/first", ".")
	cmd.Dir, cmd.Env = "/stage", []string{"A=one", "A=two"}
	observation.command(cmd, true)
	first := usage.executions.LastBuild
	cmd.Args[3] = "/second"
	observation.command(cmd, true)
	if first == usage.executions.LastBuild {
		t.Fatal("output argv missing")
	}
	first = usage.executions.LastBuild
	slices.Reverse(cmd.Env)
	observation.command(cmd, true)
	if first == usage.executions.LastBuild {
		t.Fatal("ordered effective environment missing")
	}
	observation.command(cmd, false)
	if usage.executions.LastProcess != usage.executions.LastBuild {
		t.Fatal("process descriptor missing")
	}
	observation.finish()
	if _, eligible := usage.execution.receipts(); eligible {
		t.Fatal("observations certified reuse")
	}
	usage.executions.decline(executionDecline(strings.Repeat("x", 513)))
	if len(usage.executions.LastDecline) > 512 {
		t.Fatal("unbounded diagnostic")
	}
	cmd.Env = []string{strings.Repeat("x", executionIdentityMaxBytes+1)}
	observation.command(cmd, true)
	if usage.executions.LastDecline != "command observation exceeds budget" {
		t.Fatal("unbounded command retained")
	}
	deferred := &goUsage{deferInputs: true}
	if beginExecutionObservation(deferred, nil, "predicate", 0) != nil || !deferred.evaluator || deferred.execution != nil || deferred.executions != (executionObservations{}) {
		t.Fatal("deferred bypass collected observation")
	}
}

func TestExecutionObservedPredicateMemo(t *testing.T) {
	t.Parallel()
	program := predicateMemoProgram(t, "pred p(n:Int){n>0}")
	query := check.Query{Pred: program.info.Funcs["p"], Args: []constant.Value{constant.MakeInt64(1)}}
	query.Params = query.Pred.Params
	usage := &goUsage{}
	eval := evaluatorWithTimeoutObserved(program.files, program.info, program.module, program.context, time.Minute, newPredicateMemo(), usage)
	for range 2 {
		results, err := eval([]check.Query{query})
		if err != nil || !slices.Equal(results, []bool{true}) {
			t.Fatalf("predicate: %v, %v", results, err)
		}
	}
	if !usage.evaluator || usage.executions.Invocations != 2 || usage.executions.MemoHits != 1 || len(usage.execution.invocations) != 2 {
		t.Fatalf("memo lost logical accounting: %+v", usage.executions)
	}
	if usage.executions.LastBuild != ([32]byte{}) || usage.executions.LastProcess != ([32]byte{}) {
		t.Fatal("memo hit inherited command evidence")
	}
	if _, eligible := usage.execution.receipts(); eligible {
		t.Fatal("memo qualified enclosing reuse")
	}
}

func TestExecutionObservedComptime(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("installed SDK identity requires supported Linux launcher")
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
	for _, tc := range []struct {
		name, source string
		captured     bool
	}{
		{"pure", `fn main(){println(comptime{21*2})}`, true},
		{"foreign unsafe", "fn value():Int unsafe go{return 42}\nfn main(){println(comptime{value()})}", true},
		{"CGO enabled", `fn main(){println(comptime{42})}`, true},
		{"timeout releases stage", "fn spin():Int{spin()}\nfn main(){println(comptime{spin()})}", true},
		{"empty", `fn main(){println(42)}`, false},
		{"deferred", `fn main(){println(comptime{42})}`, false},
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
			usage := &goUsage{deferInputs: tc.name == "deferred"}
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
			if tc.name == "deferred" {
				if !usage.evaluator || counts != (executionObservations{}) || usage.execution != nil {
					t.Fatal("deferred comptime captured inventory")
				}
				return
			}
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
				t.Fatal("SDK observation certified an execution receipt")
			}
			if tc.captured {
				if counts.Captured != 1 || counts.Validated != 1 || counts.Declined != 0 || counts.LastSDK.Version != 1 || counts.LastBuild == ([32]byte{}) || counts.LastProcess == ([32]byte{}) {
					t.Fatalf("installed SDK/command observation incomplete: %+v", counts)
				}
			} else if counts.Captured != 0 || counts.Validated != 0 || counts.Declined != 1 || counts.LastDecline == "" {
				t.Fatalf("SDK identity unavailable: %+v", counts)
			}
		})
	}
}
