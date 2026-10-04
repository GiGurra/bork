package driver

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/GiGurra/bork/internal/check"
)

func TestComptimePackageValues(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"chain", `Squares=comptime{range(1,6).map(n=>n*n)}
Total=comptime{Squares.fold(0,(s,n)=>s+n)}
fn main(){println(Total);println(comptime{Total+1})}`, "55\n56\n"},
		{"forward helper", `Total=comptime{read()+1}
fn read():Int{Base}
Base=21*2
Unused:Int=panic("unused package value")
fn main(){println(Total)}`, "43\n"},
		{"package annotation", `pred positive(n:Int){n>0}
Value:Int where positive=comptime{1}
fn require(n:Int where positive):Int{n}
fn main(){println(comptime{require(Value)})}`, "1\n"},
		{"nested helper computation", `Base=20
fn read():Int{comptime{Base+1}}
Total=comptime{read()*2}
fn main(){println(Total)}`, "42\n"},
		{"computed field", `Base=21
type C={n:Int,lazy value:Int=n+Base}
fn main(){println(comptime{C{n:21}.value})}`, "42\n"},
		{"computed field dependency", `Base=21
type C={n:Int,lazy value:Int=n+comptime{Base*2}}
fn main(){println(comptime{C{n:0}.value})}`, "42\n"},
		{"success value", `Base=1
fn main(){comptime{Ok};println(comptime{Base})}`, "1\n"},
		{"local return", `Base={if(true){return 42};0}
fn main(){println(comptime{Base})}`, "42\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := fixtureDir(t)
			if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(dir, "program")
			if err := Build(dir, exe); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(exe).CombinedOutput()
			if err != nil {
				t.Fatal(err)
			}
			if string(output) != tc.want {
				t.Fatalf("want %q, got %q", tc.want, output)
			}
		})
	}
}

func TestComptimePackageErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"direct cycle", `A:Int=comptime{B};B:Int=comptime{A};fn main(){}`, "dependency cycle"},
		{"helper cycle", `A:Int=comptime{read()};fn read():Int{A};fn main(){}`, "dependency cycle"},
		{"package proof before execution", `pred positive(n:Int){n>0}
Value:Int where positive=comptime{-1}
fn require(n:Int where positive):Int{panic("must not run")}
fn main(){println(comptime{require(Value)})}`, "positive(-1) is false"},
		{"initializer proof before execution", `pred positive(n:Int){n>0}
fn require(n:Int where positive):Int{panic("must not run")}
Value=require(-1)
fn main(){println(comptime{Value})}`, "positive(-1) is false"},
		{"runtime local lazy", `Base=1
fn main(){lazy local=2;println(comptime{Base+local})}`, "comptime cannot capture runtime value local"},
		{"unsupported package data", `Value:(Int)=>Int=n=>n+1
fn main(){println(comptime{Value(1)})}`, "cannot bake package value"},
		{"package effect", `Value={println(1);2};fn main(){println(comptime{Value})}`, "requires a pure initializer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := fixtureDir(t)
			if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q; got %v", tc.want, err)
			}
			if strings.Contains(err.Error(), "comptime failed: panic: must not run") {
				t.Fatal("executed before checking proof")
			}
		})
	}
}

func TestComptimePackageBatchBuildCount(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell launcher")
	}
	for _, tc := range []struct{ name, source string }{
		{"ordinary chain", `fn main(){a=comptime{range(1,5).map(n=>n*n)};b=comptime{a.fold(0,(s,n)=>s+n)};c=comptime{b+1};d=comptime{c+1};println(d)}`},
		{"single", `fn main(){println(comptime{range(1,6).map(n=>n*n).fold(0,(s,n)=>s+n)})}`},
		{"chain", `Squares=comptime{range(1,6).map(n=>n*n)}
Total=comptime{Squares.fold(0,(s,n)=>s+n)}
fn main(){println(Total)}`},
		{"long chain", `V0=1
V1=comptime{V0+1};V2=comptime{V1+1};V3=comptime{V2+1};V4=comptime{V3+1}
V5=comptime{V4+1};V6=comptime{V5+1};V7=comptime{V6+1};V8=comptime{V7+1}
fn main(){println(V8)}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := fixtureDir(t)
			if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			loaded, module, err := loadCompilationInputs(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := captureGoContext()
			counter := filepath.Join(t.TempDir(), "builds")
			launcher := filepath.Join(t.TempDir(), "go")
			script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = build ]; then echo build >> '%s'; fi\nexec '%s' \"$@\"\n", counter, ctx.tool)
			if err := os.WriteFile(launcher, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			ctx.tool = launcher
			start := time.Now()
			_, err = checkLoadedProgramObserved(loaded, module, ctx, captureEmbedsSnapshot, nil)
			if err != nil {
				t.Fatal(err)
			}
			builds, err := os.ReadFile(counter)
			if err != nil || string(builds) != "build\n" {
				t.Fatalf("want one evaluator build, got %q: %v", builds, err)
			}
			t.Logf("one evaluator build; elapsed %s", time.Since(start))
		})
	}
}

func TestComptimePackageLimits(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"timeout", `Base=1
fn spin():Int unsafe go{for{}}
fn main(){println(comptime{spin()+Base})}`, "evaluation exceeded 150ms"},
		{"size", `Base=1
fn huge():String unsafe go{import "strings"
return strings.Repeat("x",17<<20)}
fn main(){_=comptime{_=Base;huge()}}`, "result exceeds 16 MiB"},
		{"unordered", `Base=1
fn main(){println(comptime{_=Base;{"a":1}.unordered().keys()})}`, "comptime cannot iterate an unordered map"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := validatorFixture(t, tc.source)
			loaded, module, err := loadCompilationInputs(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := captureGoContext()
			ctx.evalLimit = 150 * time.Millisecond
			_, err = checkLoadedProgramObserved(loaded, module, ctx, captureEmbedsSnapshot, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q; got %v", tc.want, err)
			}
		})
	}
}

// Malformed protocol output must cancel the process before waiting for it.
// The outer deadline contains the test if that cancellation regresses.
func TestComptimeBatchInvalidResponseCleanup(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell subprocess")
	}
	watchdog, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	ctx, cancel := context.WithCancel(watchdog)
	defer cancel()
	node := &check.Comptime{}
	cmd := exec.CommandContext(ctx, "sh", "-c", "printf '\\002'; sleep 30")
	cmd.WaitDelay = time.Second
	configureEvaluationProcess(cmd)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &boundedOutput{limit: 64 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	batch := &comptimeBatch{nodes: []*check.Comptime{node}, cmd: cmd, input: input, output: output, stderr: stderr, cancel: cancel}
	defer batch.close()
	_, err = batch.evaluate(node, nil, nil, nil, &goContext{evalLimit: 150 * time.Millisecond}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid comptime response") {
		t.Fatalf("want invalid response, got %v", err)
	}
	if watchdog.Err() != nil {
		t.Fatal("response error waited for the watchdog instead of canceling evaluator")
	}
	if !batch.waited || cmd.ProcessState == nil {
		t.Fatal("evaluator was not reaped")
	}
}
