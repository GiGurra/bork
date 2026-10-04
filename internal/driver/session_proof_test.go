package driver

import (
	"bytes"
	"go/constant"
	"maps"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSessionProofDependencyParity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(ModFile, "module example.com/proofs\n")
	helper := `fn Twice(n:Int):Int{n+n}`
	write("lib/lib.bork", helper)
	source := `import "example.com/proofs/lib"
pred Positive(n:Int){lib.Twice(n)>0}
fn main(){n:Int where Positive=1;println(n)}`
	write("main.bork", source)
	session := newOwnedFixtureSession("GOPACKAGESDRIVER=off")
	requireSessionProofContext(t)
	parity := func(wantError bool) {
		t.Helper()
		got, err := session.Emit(root)
		want, cleanErr := emitOwnedSessionFixture(root, session.goSettings)
		if (err != nil) != wantError || (cleanErr != nil) != wantError {
			t.Fatalf("errors: reuse %v clean %v expected %v", err, cleanErr, wantError)
		}
		if err != nil {
			if err.Error() != cleanErr.Error() {
				t.Fatalf("diagnostic changed: reuse %v clean %v", err, cleanErr)
			}
		} else if !bytes.Equal(got, want) {
			t.Fatal("emission differs from clean")
		}
	}
	parity(false)
	if session.Stats().ProofMisses == 0 {
		t.Fatal("fixture did not execute a proof")
	}
	hits := session.Stats().ProofHits
	write("main.bork", source+"\n")
	parity(false)
	if session.Stats().ProofHits <= hits {
		t.Fatal("unchanged proof did not reuse")
	}
	misses := session.Stats().ProofMisses
	write("lib/lib.bork", `fn Twice(n:Int):Int{n-n}`)
	parity(true)
	if session.Stats().ProofMisses <= misses {
		t.Fatal("helper edit did not invalidate")
	}
	write("lib/lib.bork", helper)
	parity(false)
	misses = session.Stats().ProofMisses
	badSource := strings.Replace(source, "Positive=1", "Positive=-1", 1)
	write("main.bork", badSource)
	parity(true)
	if session.Stats().ProofMisses <= misses {
		t.Fatal("argument edit did not invalidate")
	}
	hits = session.Stats().ProofHits
	write("main.bork", "\n"+badSource)
	parity(true)
	if session.Stats().ProofHits <= hits {
		t.Fatal("false proof was not reused with fresh diagnostic locations")
	}
}

func TestSessionProofEditorParity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "main.bork")
	source := `fn twice(n:Int):Int{n+n}
pred Positive(n:Int){twice(n)>0}
fn main(){n:Int where Positive=1;println(n)}`
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	session := newOwnedFixtureSession("GOPACKAGESDRIVER=off")
	requireSessionProofContext(t)
	for _, text := range []string{source, source + "\n", source + "\n\n"} {
		overlays := map[string]string{path: text}
		got, err := session.Analyze(root, overlays)
		clean, cleanErr := newOwnedFixtureSession("GOPACKAGESDRIVER=off").Analyze(root, overlays)
		if err != nil || cleanErr != nil {
			t.Fatalf("editor error: reuse %v clean %v", err, cleanErr)
		}
		if !reflect.DeepEqual(got.Sources(), clean.Sources()) || !reflect.DeepEqual(got.Warnings(), clean.Warnings()) {
			t.Fatal("editor snapshot differs")
		}
	}
	if stats := session.Stats(); stats.ProofHits < 2 || stats.ProofMisses == 0 {
		t.Fatalf("no editor proof reuse: %+v", stats)
	}
}

func TestSessionProofUnknownClosureFresh(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for name, text := range map[string]string{ModFile: "module example.com/proofs\nunsafe \"example.com/proofs\"\n", "main.bork": `pred Positive(n:Int) unsafe go{return n>0}
fn main(){n:Int where Positive=1;println(n)}`} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	session := newOwnedFixtureSession("GOPACKAGESDRIVER=off")
	for range 2 {
		got, err := session.Emit(root)
		want, cleanErr := emitOwnedSessionFixture(root, session.goSettings)
		if err != nil || cleanErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("fallback parity: %v %v", err, cleanErr)
		}
	}
	if stats := session.Stats(); stats.ProofHits != 0 || stats.ProofMisses != 0 || stats.ProofDeclines < 2 || stats.Bypasses != 2 {
		t.Fatalf("unknown closure reused: %+v", stats)
	}
}

func TestSessionProofEvaluatorInputsAndOwnership(t *testing.T) {
	t.Parallel()
	program := predicateMemoProgram(t, `pred p(n:Int){n>0}`)
	program.context = requireSessionProofContext(t)
	query := check.Query{Pred: program.info.Funcs["p"], Params: program.info.Funcs["p"].Params, Args: []constant.Value{constant.MakeInt64(1)}}
	cache := (&Session{}).proofCache()
	usage := &goUsage{proofs: cache}
	evaluate := func(ctx *goContext) []bool {
		t.Helper()
		result, err := evaluatorWithTimeoutObserved(program.files, program.info, program.module, ctx, 0, nil, usage)([]check.Query{query})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := evaluate(program.context)
	result[0] = false
	if got := evaluate(program.context); !slices.Equal(got, []bool{true}) || cache.hits != 1 {
		t.Fatal("cached results alias caller or no hit")
	}
	context := *program.context
	context.processEnv = append(slices.Clone(context.processEnv), "BORK_PROOF_INPUT=changed")
	evaluate(&context)
	if cache.misses != 2 {
		t.Fatal("execution environment did not invalidate")
	}
	originalHook := context.moduleHook
	context.moduleHook = func(mod []byte) []byte {
		if originalHook != nil {
			mod = originalHook(mod)
		}
		return append(mod, []byte("\n// proof manifest changed\n")...)
	}
	evaluate(&context)
	if cache.misses != 3 {
		t.Fatal("actual staged module did not invalidate")
	}
	usage.proofs = (&Session{}).proofCache()
	evaluate(&context)
	if usage.proofs.hits != 0 || usage.proofs.misses != 1 {
		t.Fatal("results escaped Session owner")
	}
	for key, value := range map[string]string{"GOFLAGS": "-trimpath", "GOWORK": "/unsupported/workspace", "GOTOOLCHAIN": "go1.27.1"} {
		unsupported := context
		unsupported.values = maps.Clone(context.values)
		unsupported.values[key] = value
		before := usage.proofs.declines
		evaluate(&unsupported)
		if usage.proofs.hits != 0 || usage.proofs.declines <= before {
			t.Fatalf("unsupported %s qualified", key)
		}
	}
}

func TestSessionProofFailuresFresh(t *testing.T) {
	t.Parallel()
	program := predicateMemoProgram(t, `pred p(n:Int){if(n>0){panic("failure")}else{false}}`)
	program.context = requireSessionProofContext(t)
	query := check.Query{Pred: program.info.Funcs["p"], Params: program.info.Funcs["p"].Params, Args: []constant.Value{constant.MakeInt64(1)}}
	usage := &goUsage{proofs: (&Session{}).proofCache()}
	evaluate := evaluatorWithTimeoutObserved(program.files, program.info, program.module, program.context, 0, nil, usage)
	for range 2 {
		if _, err := evaluate([]check.Query{query}); err == nil || !strings.Contains(err.Error(), "failure") {
			t.Fatalf("expected native failure: %v", err)
		}
	}
	if usage.proofs.hits != 0 || usage.proofs.misses != 2 || len(usage.proofs.memo.results) != 0 {
		t.Fatal("failure reused")
	}
}

func TestSessionProofWrapperFresh(t *testing.T) {
	t.Parallel()
	program := predicateMemoProgram(t, `pred p(n:Int){n>0}`)
	program.context = requireSessionProofContext(t)
	actual := program.context.tool
	count := predicateMemoGoCounter(t, program.context, "exec '"+strings.ReplaceAll(actual, "'", "'\\''")+"' \"$@\"")
	query := check.Query{Pred: program.info.Funcs["p"], Params: program.info.Funcs["p"].Params, Args: []constant.Value{constant.MakeInt64(1)}}
	usage := &goUsage{proofs: (&Session{}).proofCache()}
	evaluate := evaluatorWithTimeoutObserved(program.files, program.info, program.module, program.context, 0, nil, usage)
	for range 2 {
		if _, err := evaluate([]check.Query{query}); err != nil {
			t.Fatal(err)
		}
	}
	if count() != 2 || usage.proofs.hits != 0 || usage.proofs.declines != 2 {
		t.Fatal("wrapper was reused")
	}
}

func TestSessionProofStageToolchain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := &goContext{values: map[string]string{"GOVERSION": "go1.27.1"}}
	for _, tc := range []struct {
		source string
		want   bool
	}{
		{"module example.com/proof\ngo 1.26.0\n", true},
		{"module example.com/proof\ngo 1.28.0\n", false},
		{"module example.com/proof\ngo 1.26.0\ntoolchain go1.28.0\n", false},
		{"module example.com/proof\n", false},
		{"malformed", false},
	} {
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(tc.source), 0600); err != nil {
			t.Fatal(err)
		}
		if got := sessionProofStage(dir, ctx); got != tc.want {
			t.Fatalf("toolchain eligibility %v want %v", got, tc.want)
		}
	}
}

func requireSessionProofContext(t *testing.T) *goContext {
	t.Helper()
	context := captureSessionGoContextWithSettings(nil, []string{"GOPACKAGESDRIVER=off"})
	if context.err != nil {
		t.Fatal(context.err)
	}
	if context.validation == nil {
		t.Skip("proof reuse requires a supported native Go context")
	}
	return context
}
