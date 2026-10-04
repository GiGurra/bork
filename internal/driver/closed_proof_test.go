package driver

import (
	"bytes"
	"go/constant"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/gen"
)

func proofProgramOutput(t testing.TB, program *compiledProgram, source []byte, mode string) []byte {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "proof")
	if err := buildGoWithMode(program.files, source, exe, program.module, program.context, mode); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Env = slices.Clone(program.context.processEnv)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("proof execution: %v\n%s", err, output)
	}
	return output
}

func TestClosedProofProgramParity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source string }{
		{"resolved comptime", "pred p(n:Int){comptime{1+1}+n>0}"},
		{"helper", "fn twice(n:Int):Int{n+n}\npred p(n:Int){twice(n)>0}"},
		{"intrinsic", "pred p(n:Int){\"x\".byteLength()>n}"},
		{"record", "type R={n:Int}\npred p(n:Int){r=R{n:n};r.n>0}"},
		{"record default", "type R={n:Int=2}\npred p(n:Int){R{}.n+n>0}"},
		{"function reference", "fn positive(n:Int):Bool{n>0}\npred p(n:Int){f=positive;f(n)}"},
		{"callback", "pred p(n:Int){f=()=>n>0;f()}"},
		{"recursive", "fn nonnegative(n:Int):Bool{if(n>0){nonnegative(n-1)}else{n==0}}\npred p(n:Int){nonnegative(n)}"},
		{"default argument", "fn add(n:Int,other:Int=2):Int{n+other}\npred p(n:Int){add(n)>0}"},
		{"interpolation", "pred p(n:Int){s\"number $n\".byteLength()>0}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			program := predicateMemoProgram(t, tc.source)
			query := check.Query{Pred: program.info.Funcs["p"], Params: program.info.Funcs["p"].Params, Args: []constant.Value{constant.MakeInt64(1)}}
			negative := query
			negative.Args = []constant.Value{constant.MakeInt64(-1)}
			queries := []check.Query{query, negative, {And: []check.Query{query, negative}}, {Or: []check.Query{query, negative}}}
			before, err := gen.Package(program.files, program.info)
			if err != nil {
				t.Fatal(err)
			}
			full, err := gen.EvalProgram(program.files, program.info, queries)
			if err != nil {
				t.Fatal(err)
			}
			closed, err := gen.ClosedProofProgram(program.files, program.info, queries)
			if err != nil {
				t.Fatal(err)
			}
			after, err := gen.Package(program.files, program.info)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("closed generation mutated the checked program")
			}
			want := proofProgramOutput(t, program, full, "full-proof-parity")
			got := proofProgramOutput(t, program, closed, "closed-proof-parity")
			if !bytes.Equal(got, want) {
				t.Fatalf("full %q, closed %q", want, got)
			}
			result, err := evaluatorWithTimeoutObserved(program.files, program.info, program.module, program.context, 0, nil, nil)(queries)
			if err != nil || len(result) != len(queries) {
				t.Fatalf("driver evaluation: %v, %v", result, err)
			}
			for i, word := range strings.Fields(string(want)) {
				if result[i] != (word == "true") {
					t.Fatal("driver changed query result")
				}
			}
		})
	}
}

func TestClosedProofProgramDeclinesUnknownClosure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, wantError string }{
		{"unsafe", "pred p(n:Int) unsafe go{return n>0}", ""},
		{"transitive unsafe", "fn positive(n:Int):Bool unsafe go{return n>0}\nfn helper(n:Int):Bool{positive(n)}\npred p(n:Int){helper(n)}", ""},
		{"callback unsafe", "fn positive(n:Int):Bool unsafe go{return n>0}\npred p(n:Int){f=positive;f(n)}", ""},
		{"replaced comptime dependency", "fn genericCompare[T: Ord](a:T):Bool{compare(a,a)==0}\npred p(n:Int){comptime{genericCompare(1)} && n>0}", ""},
		{"generic", "fn id[T](n:T):T{n}\npred p(n:Int){id(n)>0}", ""},
		{"lazy", "pred p(n:Int){lazy result=n>0;result}", ""},
		{"runtime package value", "lazy value=1\npred p(n:Int){value>0}", "cannot force a runtime lazy cell"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			program := predicateMemoProgram(t, tc.source)
			queries := []check.Query{{Pred: program.info.Funcs["p"], Params: program.info.Funcs["p"].Params, Args: []constant.Value{constant.MakeInt64(1)}}}
			if _, err := gen.ClosedProofProgram(program.files, program.info, queries); err == nil {
				t.Fatal("unknown closure admitted")
			}
			result, err := evaluatorWithTimeoutObserved(program.files, program.info, program.module, program.context, 0, nil, nil)(queries)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("fallback error: %v, want %s", err, tc.wantError)
				}
				return
			}
			if err != nil || !slices.Equal(result, []bool{true}) {
				t.Fatalf("fallback failed: %v, %v", result, err)
			}
		})
	}
}

// These fixtures include unrelated instances and foreign declarations, the
// declarations that originally made ordinary evaluators expensive to build.
func TestClosedProofExamples(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"config", "http_server"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			program, queries := closedProofQueries(t, "../../examples/"+name)
			full, err := gen.EvalProgram(program.files, program.info, queries)
			if err != nil {
				t.Fatal(err)
			}
			closed, err := gen.ClosedProofProgram(program.files, program.info, queries)
			if err != nil {
				t.Fatal(err)
			}
			if len(closed) >= len(full) {
				t.Fatal("proof program did not shrink")
			}
			want := proofProgramOutput(t, program, full, "full-proof-example")
			got := proofProgramOutput(t, program, closed, "closed-proof-example")
			if !bytes.Equal(got, want) {
				t.Fatalf("full %q, closed %q", want, got)
			}
		})
	}
}
