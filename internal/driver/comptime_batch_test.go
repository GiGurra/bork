package driver

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/gen"
)

// Reload each arm: checking mutates the graph when it bakes a computed value.
func ordinaryComptimeOutput(t *testing.T, dir string, standalone bool, limit time.Duration) ([]byte, error) {
	t.Helper()
	loaded, module, err := loadCompilationInputs(dir, nil)
	if err != nil {
		return nil, err
	}
	ctx := captureGoContext()
	ctx.comptimeStandalone, ctx.evalLimit = standalone, limit
	program, err := checkLoadedProgramObserved(loaded, module, ctx, captureEmbedsSnapshot, nil)
	if err != nil {
		return nil, err
	}
	return gen.Package(program.files, program.info)
}

func TestComptimeOrdinaryBatchParity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, source, want string
		limit              time.Duration
	}{
		{name: "capture chain", source: `fn main(){a=comptime{range(1,5).map(n=>n*n)};b=comptime{a.fold(0,(s,n)=>s+n)};println(b)}`},
		{name: "nested and helper dependency", source: `fn helper():Int{comptime{21}}
fn main(){println(comptime{n=comptime{helper()};n*2})}`},
		{name: "computed field dependency", source: `type R={n:Int,lazy more:Int=n+comptime{2}}
fn main(){println(comptime{R{n:40}.more})}`},
		{name: "contextual facts", source: `pred positive(n:Int){n>0}
fn identity(n:Int where positive):Int where positive{n}
fn main(){a:Int where positive=comptime{identity(2)};b:Int where positive=comptime{a+1};println(b)}`},
		{name: "false preflight", source: `pred positive(n:Int){n>0}
fn must(n:Int where positive):Int{panic("must not execute")}
fn main(){_=comptime{1};println(comptime{must(-1)})}`, want: "positive(-1) is false"},
		{name: "failed value before dependent", source: `pred positive(r:R){r.n>0}
type R={n:Int} where positive
fn invalid():R unsafe go{return R{n:-1}}
fn consume(r:R):Int{panic("must not execute")}
fn main(){r=comptime{invalid()};println(comptime{consume(r)})}`, want: "positive(R { n: -1 }) is false"},
		{name: "cycle", source: `fn a():Int{comptime{b()}}
fn b():Int{a()}
fn main(){println(a())}`, want: "cyclic comptime"},
		{name: "per recipe timeout", source: `fn spin():Int unsafe go{for{}}
fn main(){_=comptime{1};println(comptime{spin()})}`, want: "evaluation exceeded 150ms", limit: 150 * time.Millisecond},
		{name: "result limit", source: `fn huge():String unsafe go{import "strings"
return strings.Repeat("x",17<<20)}
fn main(){_=comptime{1};println(comptime{huge()})}`, want: "result exceeds 16 MiB"},
		// The combined encoded results exceed one request's budget.
		{name: "result budget resets", source: `fn large():String unsafe go{import "strings"
return strings.Repeat("x",7<<20)}
fn main(){a=comptime{large()};b=comptime{large()};println(a.byteLength()+b.byteLength())}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := validatorFixture(t, tc.source+"\nfn batchAnchor():Int{comptime{0}}\n")
			before, beforeErr := ordinaryComptimeOutput(t, dir, true, tc.limit)
			after, afterErr := ordinaryComptimeOutput(t, dir, false, tc.limit)
			if tc.want != "" {
				for _, err := range []error{beforeErr, afterErr} {
					if err == nil || !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("want %q, got %v", tc.want, err)
					}
					if strings.Contains(err.Error(), "panic: must not execute") {
						t.Fatal("executed before proof admission")
					}
				}
				if strings.SplitN(beforeErr.Error(), "\n", 2)[0] != strings.SplitN(afterErr.Error(), "\n", 2)[0] {
					t.Fatalf("diagnostics differ:\nstandalone: %v\nbatch: %v", beforeErr, afterErr)
				}
			} else {
				if beforeErr != nil || afterErr != nil {
					t.Fatalf("standalone: %v; batch: %v", beforeErr, afterErr)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("emitted Go differs")
				}
			}
		})
	}
}

func TestComptimeOrdinaryBatchBuildReadParity(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	source := `import "bork/build"
fn main(){a=comptime{build.ReadString("config.txt")};b=comptime{build.ReadBytes("config.txt")};println(a);println(b)}`
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	var prior []byte
	for _, text := range []string{"hello", "world"} {
		if err := os.WriteFile(filepath.Join(dir, "config.txt"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		before, err := ordinaryComptimeOutput(t, dir, true, 0)
		if err != nil {
			t.Fatal(err)
		}
		after, err := ordinaryComptimeOutput(t, dir, false, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("build-read output differs")
		}
		if bytes.Equal(prior, after) {
			t.Fatal("fresh build input was not observed")
		}
		prior = after
	}
}

// Use a protocol peer rather than a clock-effect helper: real comptime recipes
// cannot sleep. Each request fits the deadline while their sum exceeds it.
func TestComptimeBatchDeadlineResets(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell protocol peer")
	}
	node := &check.Comptime{}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "sh", "-c", "while read index; do sleep 1.1; printf '\\001'; done")
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
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	stderr := &boundedOutput{limit: 64 << 10}
	cmd.Stderr = stderr
	batch := &comptimeBatch{nodes: []*check.Comptime{node}, cmd: cmd, input: input, output: output, stderr: stderr, cancel: cancel, dir: dir}
	defer batch.close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := batch.evaluate(node, nil, nil, nil, &goContext{evalLimit: 2 * time.Second}, nil); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
}
