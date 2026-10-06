package check

import (
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
)

func TestOkTasks(t *testing.T) {
	for _, tc := range []struct {
		name, source, want string
	}{
		{"statement", `fn main() { scope s { fork(s, () => println("x")) } }`, ""},
		{"annotated and awaited", `fn main() { scope s { t: Task[Ok] = fork(s, () => println("x")); await(t) } }`, ""},
		{"function tail", `fn start(s: Scope) uses io { fork(s, () => println("x")) }`, ""},
		{"return", `fn start(s: Scope) uses io { return fork(s, () => println("x")) }`, ""},
		{"if without else", `fn main() { scope s { if (true) { fork(s, () => println("x")) } } }`, ""},
		{"match arms join Ok", `fn main() { scope s { match (1) { 1 => fork(s, () => println("x")); _ => {} } } }`, ""},
		{"lambda", `fn main() { scope s { [1].forEach(i => fork(s, () => println(i))) } }`, ""},
		{"never returns", `fn main() { scope s { t: Task[Ok] = fork(s, () => panic("x")); await(t) } }`, ""},
		{"returned task", `fn start(s: Scope) uses io: Task[Ok] { fork(s, () => println("x")) }`, ""},
		{"valued task", `fn main() { scope s { fork(s, () => 1) } }`, "value of type Task[Int] is not used"},
		{"union with Ok", `fn main() { scope s { checkpoint(s) } }`, "value of type Ok | Cancelled is not used"},
		{"tail of union result", `fn start(s: Scope) uses io: Ok | Cancelled { fork(s, () => println("x")) }`, "but its body produces Task[Ok]"},
		{"other Ok type arguments", `fn f(xs: List[Ok]) {}`, "List[Ok] is not allowed"},
		{"Ok task list", `fn main() { scope s { _ = [fork(s, () => println("x"))].awaitAll() } }`, "T of awaitAll cannot be Ok"},
		{"spawn", `fn main() { scope s { t = spawn(s, () => 1); println(await(t)) } }`, "spawn was removed; use fork"},
		{"launch", `fn main() { scope s { launch(s, () => println("x")) } }`, "launch was removed; use fork"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &diag.List{}
			files := prelude.Parse(d)
			file := syntax.Parse("main.bork", []byte(tc.source), d)
			file.Package = "example.com/tasks"
			files = append(files, file)
			Program(files, file.Package, d, nil)
			if tc.want == "" {
				if d.Len() != 0 {
					t.Fatalf("unexpected diagnostics: %s", d.Error())
				}
				return
			}
			if d.Len() != 1 || !strings.Contains(d.Error(), tc.want) {
				t.Fatalf("want one diagnostic containing %q, got: %s", tc.want, d.Error())
			}
		})
	}
}

func TestRemovedTaskCallFix(t *testing.T) {
	d := &diag.List{}
	files := prelude.Parse(d)
	file := syntax.Parse("main.bork", []byte("fn main() {\n  scope s {\n    launch(s, () => println(\"x\"))\n  }\n}\n"), d)
	file.Package = "example.com/tasks"
	Program(append(files, file), file.Package, d, nil)
	got := d.Sorted()
	if len(got) != 1 || got[0].Code != "migration.launch" || len(got[0].Fixes) != 1 {
		t.Fatalf("diagnostics: %+v", got)
	}
	edits := got[0].Fixes[0].Edits
	if len(edits) != 1 || edits[0].Replacement != "fork" || edits[0].Start.Line != 3 || edits[0].Start.Col != 5 || edits[0].End.Col != 11 {
		t.Fatalf("edits: %+v", edits)
	}
}
