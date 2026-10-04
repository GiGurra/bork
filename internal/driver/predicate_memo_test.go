package driver

import (
	"crypto/sha256"
	"fmt"
	"go/constant"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GiGurra/bork/internal/check"
)

func TestPredicateMemoSupport(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, source string
		want         bool
	}{
		{"pure", `package main; import "fmt"; func main(){fmt.Println(true)}`, true},
		{"foreign init", `package main; import _ "example.com/init"; func main(){}`, false},
		{"unknown standard support", `package main; import _ "net/http"; func main(){}`, false},
		{"startup call", `package main; var x = user(); func user()int{return 1}; func main(){}`, false},
		{"startup init", `package main; func init(){user()}; func user(){};func main(){}`, false},
		{"compiler init", `package main; var _equalMapHook, _equalMaps func();func init(){_equalMapHook=_equalMaps};func main(){}`, true},
		{"altered compiler init", `package main; var _equalMapHook, _equalMaps func();func init(){_equalMapHook=_equalMaps;user()};func user(){};func main(){}`, false},
		{"compiler seed", `package main; import "hash/maphash"; var _mapSeed=maphash.MakeSeed();func main(){}`, true},
		{"unknown seed", `package main; import "hash/maphash"; var userSeed=maphash.MakeSeed();func main(){}`, false},
		{"linkname", "package main\n//go:linkname x foreign\nvar x int\nfunc main(){}", false},
		{"malformed", "not Go", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := predicateMemoSupport([]byte(tc.source)); got != tc.want {
				t.Fatalf("support eligibility=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestPredicateMemoOwnershipAndBudgets(t *testing.T) {
	t.Parallel()
	memo := newPredicateMemo()
	key := sha256.Sum256([]byte("one"))
	result := []bool{true, false}
	memo.put(key, result)
	result[0] = false
	got, ok := memo.get(key)
	if !ok || !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("stored result aliases caller: %v, %v", got, ok)
	}
	got[0] = false
	got, _ = memo.get(key)
	if !got[0] {
		t.Fatal("returned result aliases retained state")
	}
	if _, ok := newPredicateMemo().get(key); ok {
		t.Fatal("result escaped its invocation")
	}
	for i := range predicateMemoEntries + 1 {
		memo.put(sha256.Sum256([]byte(fmt.Sprint(i))), []bool{false})
	}
	if len(memo.results) != predicateMemoEntries {
		t.Fatal("entry bound exceeded")
	}
	memo = newPredicateMemo()
	memo.put(key, make([]bool, predicateMemoResultBytes))
	memo.put(sha256.Sum256([]byte("overflow")), []bool{true})
	if memo.bytes != predicateMemoResultBytes || len(memo.results) != 1 {
		t.Fatal("result payload bound exceeded")
	}
}

func TestPredicateMemoActualStageIdentity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for name, contents := range map[string]string{"main.go": "package main", "go.mod": "module example.com/memo", "go.sum": "captured sum", "asset.txt": "captured asset"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := &goContext{tool: "/native/go", env: []string{"GOOS=linux"}, processEnv: []string{"VALUE=one"}}
	embeds := []*check.Embedded{{Files: []check.EmbeddedFile{{StagePath: "asset.txt", Data: []byte("different unstaged data")}}}}
	key, ok := predicateMemoKey(root, ctx, time.Second, embeds)
	if !ok {
		t.Fatal("valid staged inputs declined")
	}
	for _, name := range []string{"main.go", "go.mod", "go.sum", "asset.txt"} {
		original, _ := os.ReadFile(filepath.Join(root, name))
		if err := os.WriteFile(filepath.Join(root, name), append(slices.Clone(original), 'x'), 0600); err != nil {
			t.Fatal(err)
		}
		changed, ok := predicateMemoKey(root, ctx, time.Second, embeds)
		if !ok || changed == key {
			t.Fatalf("actual %s bytes missing from key", name)
		}
		if err := os.WriteFile(filepath.Join(root, name), original, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, mutate := range []func(*goContext){
		func(c *goContext) { c.tool = "/different/go" },
		func(c *goContext) { c.toolDigest[0] = 1 },
		func(c *goContext) { c.env = []string{"GOOS=darwin"} },
		func(c *goContext) { c.processEnv = []string{"VALUE=two"} },
	} {
		changed := *ctx
		mutate(&changed)
		if got, ok := predicateMemoKey(root, &changed, time.Second, embeds); !ok || got == key {
			t.Fatal("build/execution context change missing from key")
		}
	}
	if got, ok := predicateMemoKey(root, ctx, 2*time.Second, embeds); !ok || got == key {
		t.Fatal("timeout missing from key")
	}
	if err := os.Remove(filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if _, ok := predicateMemoKey(root, ctx, time.Second, embeds); ok {
		t.Fatal("missing required manifest qualified")
	}
}

func predicateMemoProgram(t *testing.T, source string) *compiledProgram {
	t.Helper()
	root := t.TempDir()
	for name, text := range map[string]string{ModFile: "module example.com/predicate-memo\nunsafe \"example.com/predicate-memo\"\n", "main.bork": source + "\nfn main(){}"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	program, err := checkProgramObserved(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

func predicateMemoGoCounter(t *testing.T, ctx *goContext, body string) func() int {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("counter fixture requires a shell")
	}
	// All callers are leaves. Keep the launcher identity stable for Go's own
	// object cache, while fixtureDir deletes the counter/tool bytes on cleanup.
	root := fixtureDir(t)
	count := filepath.Join(root, "count")
	tool := filepath.Join(root, "go")
	text := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = build ]; then printf 'build\\n' >> '%s'; fi\n", strings.ReplaceAll(count, "'", "'\\''")) + body + "\n"
	if err := os.WriteFile(tool, []byte(text), 0700); err != nil {
		t.Fatal(err)
	}
	ctx.tool = tool
	ctx.toolDigest = sha256.Sum256([]byte(text))
	ctx.pinSettings()
	return func() int {
		data, _ := os.ReadFile(count)
		return strings.Count(string(data), "build\n")
	}
}

func TestPredicateMemoNativeEvaluation(t *testing.T) {
	t.Parallel()
	program := predicateMemoProgram(t, "pred p(n:Int){n>0}")
	actual := program.context.tool
	count := predicateMemoGoCounter(t, program.context, "exec '"+strings.ReplaceAll(actual, "'", "'\\''")+"' \"$@\"")
	query := check.Query{Pred: program.info.Funcs["p"], Args: []constant.Value{constant.MakeInt64(1)}}
	query.Params = query.Pred.Params
	memo := newPredicateMemo()
	eval := evaluatorWithTimeoutMemo(program.files, program.info, program.module, program.context, time.Minute, memo)
	for range 3 {
		result, err := eval([]check.Query{query})
		if err != nil || !slices.Equal(result, []bool{true}) {
			t.Fatalf("native predicate: %v, %v", result, err)
		}
		result[0] = false
	}
	if count() != 1 {
		t.Fatalf("identical audited queries built %d times, want 1", count())
	}
	query.Args[0] = constant.MakeInt64(-1)
	for range 2 {
		result, err := eval([]check.Query{query})
		if err != nil || !slices.Equal(result, []bool{false}) {
			t.Fatalf("false predicate result: %v, %v", result, err)
		}
	}
	if count() != 2 {
		t.Fatal("changed program was not built, or false result was not reused")
	}
	for _, timeout := range []time.Duration{time.Minute, 0} {
		fresh := evaluatorWithTimeoutMemo(program.files, program.info, program.module, program.context, timeout, newPredicateMemo())
		if _, err := fresh([]check.Query{query}); err != nil {
			t.Fatal(err)
		}
	}
	if count() != 4 {
		t.Fatal("result leaked to a new invocation or unbounded evaluator")
	}
}

func TestPredicateMemoDeclinesAndFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, source, output, wantError string
		timeout                         time.Duration
	}{
		{"unsafe query", "pred p(n:Int) unsafe go{return n>0}", "printf true", "", time.Minute},
		{"malformed output", "pred p(n:Int){n>0}", "printf malformed", "", time.Minute},
		{"execution failure", "pred p(n:Int){n>0}", "echo failed >&2;exit 1", "a predicate failed", time.Minute},
		{"panic", "pred p(n:Int){n>0}", "echo panic:proof >&2;exit 2", "a predicate failed", time.Minute},
		{"build failure", "pred p(n:Int){n>0}", "", "failed", time.Minute},
		{"timeout", "pred p(n:Int){n>0}", "while :; do :; done", "predicate evaluation exceeded", 150 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			program := predicateMemoProgram(t, tc.source)
			body := "while [ \"$1\" != '-o' ]; do shift; done\nshift\nprintf '%s\\n' '#!/bin/sh' '" + tc.output + "' > \"$1\"\nchmod 700 \"$1\""
			if tc.name == "build failure" {
				body = "echo intentional-compiler-failure >&2;exit 1"
			}
			count := predicateMemoGoCounter(t, program.context, body)
			query := check.Query{Pred: program.info.Funcs["p"], Args: []constant.Value{constant.MakeInt64(1)}}
			query.Params = query.Pred.Params
			eval := evaluatorWithTimeoutMemo(program.files, program.info, program.module, program.context, tc.timeout, newPredicateMemo())
			for range 2 {
				_, err := eval([]check.Query{query})
				if tc.wantError == "" && err != nil || tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
					t.Fatalf("evaluation error=%v, want %q", err, tc.wantError)
				}
			}
			if count() != 2 {
				t.Fatalf("declined/failed evaluation was reused: %d builds", count())
			}
		})
	}
}

func TestPredicateMemoCompilationScope(t *testing.T) {
	t.Parallel()
	ctx := captureGoContext()
	if ctx.err != nil {
		t.Fatal(ctx.err)
	}
	actual := ctx.tool
	count := predicateMemoGoCounter(t, ctx, "exec '"+strings.ReplaceAll(actual, "'", "'\\''")+"' \"$@\"")
	for n := 1; n <= 2; n++ {
		loaded, module, err := loadCompilationInputs("../../examples/comptime", nil)
		if err != nil {
			t.Fatal(err)
		}
		usage := &goUsage{}
		if _, err := checkLoadedProgramTracked(loaded, module, ctx, captureEmbedsSnapshot, usage, nil); err != nil {
			t.Fatal(err)
		}
		if usage.executions.Invocations != 10 || usage.executions.MemoHits != 4 || len(usage.execution.invocations) != 10 {
			t.Fatalf("logical accounting: %+v", usage.executions)
		}
		if _, eligible := usage.execution.receipts(); eligible {
			t.Fatal("memo allowed enclosing reuse")
		}
		// Four value evaluators still run freshly. Two identical proof programs
		// each run once across recipe checks, result checks and final Facts.
		if got := count(); got != 6*n || !usage.evaluator {
			t.Fatalf("compilation %d: %d builds, evaluator usage=%v; want %d and true", n, got, usage.evaluator, 6*n)
		}
	}
}

func TestPredicateMemoModuleHook(t *testing.T) {
	t.Parallel()
	// The owned module hook must remain observable even on a memo hit.
	program := predicateMemoProgram(t, "pred p(n:Int){n>0}")
	actual := program.context.tool
	count := predicateMemoGoCounter(t, program.context, "exec '"+strings.ReplaceAll(actual, "'", "'\\''")+"' \"$@\"")
	original := program.context.moduleHook
	calls, state := 0, "one"
	program.context.moduleHook = func(mod []byte) []byte {
		calls++
		if original != nil {
			mod = original(mod)
		}
		if state == "large" {
			return fmt.Appendf(mod, "\n// %s\n", strings.Repeat("x", predicateMemoInputBytes))
		}
		return fmt.Appendf(mod, "\n// memo hook state %s\n", state)
	}
	query := check.Query{Pred: program.info.Funcs["p"], Args: []constant.Value{constant.MakeInt64(1)}}
	query.Params = query.Pred.Params
	eval := evaluatorWithTimeoutMemo(program.files, program.info, program.module, program.context, time.Minute, newPredicateMemo())
	for _, value := range []string{"one", "two", "one"} {
		state = value
		if result, err := eval([]check.Query{query}); err != nil || !slices.Equal(result, []bool{true}) {
			t.Fatalf("hook evaluation: %v, %v", result, err)
		}
	}
	if calls != 3 || count() != 2 {
		t.Fatalf("hook called %d times and built %d times, want 3 and 2", calls, count())
	}
	state = "large"
	for range 2 {
		if result, err := eval([]check.Query{query}); err != nil || !slices.Equal(result, []bool{true}) {
			t.Fatalf("input-budget fallback: %v, %v", result, err)
		}
	}
	if calls != 5 || count() != 4 {
		t.Fatalf("over-budget stage was reused: %d hook calls, %d builds", calls, count())
	}
}
