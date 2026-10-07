package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
)

const witnessFacts = `pred positive(n: Int) { n > 0 }
fn need(x: Int where positive): Int { x }
derive fn needs(x: Int where positive): Int { x }
fn needFirst[A](n: Int where positive, value: A): Int { n }
`

// Unused definitions report the fact failures every expansion would report,
// and nothing that a target's shape, a staged selection or a dependent value
// could change.
func TestDeriveWitnessFacts(t *testing.T) {
	t.Parallel()
	const missing = "requires x to be positive, but that is not proven for n"
	cases := []struct{ name, source, want string }{
		{"function", `derive fn unused[T](n: Int): Int { need(n) }`, missing},
		{"helper", `derive fn unused[T](n: Int): Int { needs(n) }`, missing},
		{"generic callee", `derive fn unused[T](value: T, n: Int): Int { needFirst(n, value) }`, "requires n to be positive, but that is not proven for n"},
		{"helper body", `derive fn unused(n: Int): Int { need(n) }`, missing},
		{"template", `class C[T] { fn c(x: T, n: Int): Int }
derive instance c[T]: C[T] { fn c(x: T, n: Int): Int { need(n) } }`, missing},
		{"staged copy", `derive fn unused[T](n: Int): Int {
 comptime for (field in shape.fields[T]()) { _ = need(n) }
 0
}`, missing},
		{"staged branch", `derive fn unused[T](n: Int): Int { comptime if (shape.kind[T]() == shape.Record) { need(n) } else { 0 } }`, missing},
		{"comprehension", `derive fn unused[T](n: Int): List[Int] { [comptime for (field in shape.fields[T]()) need(n)] }`, missing},
		{"target tuple", `type Box[A] = { v: A }
derive fn unused[T](x: T, n: Int): Int { o = (x, n); b = Box { v: x }; _ = o; _ = b; need(n) }`, missing},
		{"dependent call argument", `derive fn unused[T](x: T, n: Int): String { comptime for (field in shape.fields[T]()) { _ = toString(field.read(x)) + toString(need(n)) }; "" }`, missing},

		{"declared", `derive fn unused[T](n: Int where positive): Int { need(n) }`, ""},
		{"condition", `derive fn unused[T](n: Int): Int { if (n > 0) { need(n) } else { 0 } }`, ""},
		{"guard", `derive fn unused[T](n: Int): Int { if (n <= 0) { return 0 }; need(n) }`, ""},
		{"field value", `derive fn unused[T](): Int { comptime for (field in shape.fields[T]()) { _ = need(field.index) }; 0 }`, ""},
		{"dependent condition", `derive fn unused[T](n: Int): Int { comptime for (field in shape.fields[T]()) { if (n > field.index) { _ = need(n) } }; 0 }`, ""},
		{"staged value", `derive fn unused[T](): Int { m = comptime if (shape.kind[T]() == shape.Record) { 1 } else { 0 }; need(m) }`, ""},
		{"staged exit", `derive fn unused[T](n: Int): Int { comptime for (field in shape.fields[T]()) { if (n <= 0) { return 0 } }; need(n) }`, ""},
		{"exhausted", `derive fn unused[T](x: T, n: Int): Int { if (n <= 0) { shape.exhausted[T](x) }; need(n) }`, ""},
		{"dependent return", `derive fn unused[T](x: T, n: Int): Int { if (n <= 0) { return x.size() }; need(n) }`, ""},
		{"staged never", `derive fn unused[T](n: Int): Int {
 if (n <= 0) { comptime if (shape.kind[T]() == shape.Record) { panic("a") } else { panic("b") } }
 need(n)
}`, ""},
		{"dependent match never", `derive fn unused[T](x: T, n: Int): Int { if (n <= 0) { match (x) { _: T => panic("bad") } }; need(n) }`, ""},
		{"helper result", `derive fn one[T](): Int { comptime if (shape.kind[T]() == shape.Record) { 1 } else { 0 } }
derive fn unused[T](): Int { need(one[T]()) }`, ""},
		{"target value", `pred valid[A](x: A) { true }
fn check[A](x: A where valid): Int { 1 }
derive fn unused[T](x: T): Int { check(x) }`, ""},
		// Proving a constant runs predicates; unused definitions never do.
		{"constant", `derive fn unused[T](): Int { need(0) }`, ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := witnessFacts + test.source + "\nfn main() {}"
			if strings.Contains(test.source, "shape.") {
				source = "import \"bork/shape\"\n" + source
			}
			checkPreludeSource(t, source, test.want)
		})
	}
}

// Without shape operations a helper is an ordinary function, so the ordinary
// checker is the oracle for its independent facts.
func TestDeriveWitnessFactsMatchOrdinaryFunctions(t *testing.T) {
	t.Parallel()
	bodies := []string{
		`(n: Int): Int { need(n) }`,
		`(n: Int where positive): Int { need(n) }`,
		`(n: Int): Int { if (n > 0) { need(n) } else { 0 } }`,
		`(n: Int): Int { if (n <= 0) { return 0 }; need(n) }`,
		`(n: Int): Int { m = n + 1; need(m) }`,
		`(n: Int, m: Int where positive): Int { if (n == m) { need(n) } else { need(m) } }`,
		`(xs: List[Int]): Int { if (xs.isEmpty()) { 0 } else { need(xs.length()) } }`,
		`(n: Int): Int { work = (k: Int) => if (k > 0) { need(k) } else { 0 }; work(n) }`,
		`(n: Int): Int { work = (k: Int) => need(k); work(n) }`,
	}
	for _, body := range bodies {
		_, _, ordinary := Check(validatorFixture(t, witnessFacts+"fn unused"+body+"\nfn main() {}"))
		_, _, derived := Check(validatorFixture(t, witnessFacts+"derive fn unused"+body+"\nfn main() {}"))
		if (ordinary == nil) != (derived == nil) {
			t.Fatalf("%s: ordinary %v, derive %v", body, ordinary, derived)
		}
		if ordinary != nil && !strings.Contains(derived.Error(), "need requires") {
			t.Fatalf("%s: derive %v", body, derived)
		}
	}
}

// A definition's failure is reported once, at the definition, rather than
// again at every request whose expansion repeats it.
func TestDeriveWitnessFactsReportedOnce(t *testing.T) {
	t.Parallel()
	_, _, err := Check(validatorFixture(t, witnessFacts+`class C[T] { fn c(x: T, n: Int): Int }
derive instance c[T]: C[T] { fn c(x: T, n: Int): Int { need(n) } }
type R = { a: Int } derive (C)
type Q = { a: Int } derive (C)
fn main() {}`))
	if err == nil || strings.Count(err.Error(), "need requires") != 1 || strings.Contains(err.Error(), "derive template at") {
		t.Fatalf("want one definition diagnostic, got %v", err)
	}
}

// Expansions name specialized helpers differently; the definition's
// diagnostic still replaces theirs.
func TestDeriveWitnessHelperFactsReportedOnce(t *testing.T) {
	t.Parallel()
	_, _, err := Check(validatorFixture(t, witnessFacts+`derive fn g[T](m: Int where positive): Int { m }
class C[T] { fn c(x: T, n: Int): Int }
derive instance c[T]: C[T] { fn c(x: T, n: Int): Int { g[T](n) } }
type R = { a: Int } derive (C)
fn main() {}`))
	if err == nil || strings.Count(err.Error(), "requires m to be positive") != 1 || strings.Contains(err.Error(), "derive template at") {
		t.Fatalf("want one definition diagnostic, got %v", err)
	}
}

// Interpolation validators run for expansions, not for unused definitions.
func TestDeriveWitnessStartsNoValidator(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/sql"
derive fn q[T](name: String): String {
  statement = sql.SQL"SELECT '$name'"
  statement.Render(sql.Dialect.Sqlite)
}
fn main() {}`, "")
}

// Checking a witness leaves no editor data at the template's positions.
func TestDeriveWitnessLeavesNoEditorData(t *testing.T) {
	t.Parallel()
	source := witnessFacts + `class C[T] { fn c(x: T, n: Int): Int }
derive instance c[T]: C[T] { fn c(x: T, n: Int): Int { m = n + 1; work = (k: Int) => k; work(m) } }
type R = { a: Int } derive (C)
fn main() {}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	analysis, err := NewSession().Analyze(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	hints, err := analysis.EditorInlays(path, check.EditorInlayOptions{Types: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, hint := range hints {
		if strings.Count(hint.Label, ":") > strings.Count(hint.Label, "=>")+1 {
			t.Fatalf("duplicated inlay hint: %+v", hints)
		}
	}
}

// Requested templates and their field-dependent obligations still build and
// run when no definition-level failure exists.
func TestDeriveWitnessRequestedTemplate(t *testing.T) {
	t.Parallel()
	source := "import \"bork/shape\"\n" + witnessFacts + `class Sum[T] { fn sum(x: T, n: Int): Int }
derive instance sum[T]: Sum[T] {
 fn sum(x: T, n: Int): Int {
  if (n <= 0) { return 0 }
  parts: List[Int] = [comptime for (field in shape.fields[T]()) comptime if (!field.computed) need(n) + field.read(x)]
  parts.fold(0, (total, part) => total + part)
 }
}
type Item = { a: Int where positive, b: Int } derive (Sum)
fn main() { println(sum(Item { a: 2, b: 3 }, 1)) }`
	if _, _, err := Check(validatorFixture(t, source)); err != nil {
		t.Fatal(err)
	}
}

// Witnesses must cover real templates, not only the ones written for these
// tests; a definition that cannot be checked is silently skipped.
func TestDeriveWitnessCoverage(t *testing.T) {
	t.Parallel()
	for dir, want := range map[string][]string{
		"../../examples/derive_labels":               {"labels"},
		"../../testdata/cases/foreign_record_custom": {"column", "entry", "table"},
		"../std/enum": {"CheckEnum", "BuildValue", "BuildFallback", "values", "name", "parse", "index"},
	} {
		_, info, err := Check(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		got := map[string]bool{}
		for _, fn := range info.DefinitionWitnesses {
			got[fn.Decl.Name] = true
		}
		for _, name := range want {
			if !got[name] {
				t.Errorf("%s: no witness for %s (have %v)", dir, name, got)
			}
		}
	}
}

// Unused definitions report the lifetime violations every expansion would
// report, and nothing that depends on what a dependent call does with
// ownership or on which staged copies a target selects.
func TestDeriveWitnessLifetimes(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, source, want string }{
		{"released use", `derive fn u[T](path: String) uses io: String | fs.Error {
  file = scope s { fs.Open(path, s)? }
  fs.ReadAllText(file)
}`, "file may be released: it belongs to scope s"},
		{"escaping result", `derive fn u[T](path: String) uses io: fs.File | fs.Error { scope s { fs.Open(path, s) } }`, "function u cannot return this value: it belongs to scope s"},
		{"released capture", `derive fn u[T](path: String) uses io: String | fs.Error {
  read = scope s { file = fs.Open(path, s)?; () => fs.Path(file) }
  read()
}`, "read may be released: it belongs to scope s"},
		{"unsafe go scope", `fn keep(s: Scope, f: fs.File) unsafe go { }
derive fn u[T](app: Scope, path: String) uses io: Ok | fs.Error {
  scope inner { file = fs.Open(path, inner)?; keep(app, file) }
}`, "but keep may keep it until app closes"},
		{"staged copy", `derive fn u[T](path: String) uses io: Ok | fs.Error {
  comptime for (field in shape.fields[T]()) { file = scope s { fs.Open(path, s)? }; _ = fs.ReadAllText(file)? }
}`, "file may be released"},
		{"open owner", `derive fn u[T](app: Scope) { owner = openScope(app) }`, "owned scope owner is still open"},

		{"kept in scope", `derive fn u[T](path: String) uses io: String | fs.Error { scope s { file = fs.Open(path, s)?; fs.ReadAllText(file) } }`, ""},
		{"staged close", `derive fn u[T](app: Scope) uses state {
  owner = openScope(app)
  comptime if (shape.kind[T]() == shape.Record) { closeScope(owner) } else { closeScope(owner) }
}`, ""},
		{"owner to helper", `derive fn closer[T](o: OwnedScope) uses state { closeScope(o) }
derive fn u[T](app: Scope) uses state { owner = openScope(app); closer[T](owner) }`, ""},
		{"dependent call", `derive fn u[T](x: T, path: String) uses io: String | fs.Error {
  scope s { file = fs.Open(path, s)?; comptime for (field in shape.fields[T]()) { _ = toString(field.read(x)) + fs.Path(file) }; fs.ReadAllText(file) }
}`, ""},
		{"recursive type", `type Node = { value: Int, children: List[Node] }
derive fn h[T](n: Node): Int { 0 }
derive fn u[T](n: Node): Int { h[T](n) }`, ""},
		{"helper acquires", `derive fn opener[T](s: Scope, path: String) uses io: fs.File | fs.Error { fs.Open(path, s) }
derive fn u[T](app: Scope, path: String) uses io: fs.File | fs.Error { scope s { f = opener[T](s, path)?; move(f, app) } }`, ""},
		{"staged attach", `derive fn u[T](app: Scope, path: String) uses io: fs.File | fs.Error {
  scope s { f = fs.Open(path, s)?; comptime if (shape.kind[T]() == shape.Record) { attach(f, app) } else { attach(f, app) }; f }
}`, ""},
		{"close again in copy", `derive fn u[T](app: Scope) uses state {
  owner = openScope(app); closeScope(owner)
  comptime for (field in shape.fields[T]()) { closeScope(owner); _ = 1 }
}`, "owned scope owner was already closed"},
		{"move again in copy", `derive fn u[T](app: Scope, other: Scope, path: String) uses io: Ok | fs.Error {
  scope s { f = fs.Open(path, s)?; move(f, app); comptime for (field in shape.fields[T]()) { move(f, other); _ = 1 } }
}`, ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := test.source + "\nfn main() {}"
			if strings.Contains(test.source, "fs.") {
				source = "import \"bork/fs\"\n" + source
			}
			if strings.Contains(test.source, "shape.") {
				source = "import \"bork/shape\"\n" + source
			}
			_, _, err := Check(validatorFixture(t, source))
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("want %q, got %v", test.want, err)
			}
		})
	}
}

// Expanded helpers report under generated names; the definition reports once.
func TestDeriveWitnessHelperLifetimesReportedOnce(t *testing.T) {
	t.Parallel()
	_, _, err := Check(validatorFixture(t, `import "bork/fs"
derive fn u[T](path: String) uses io: fs.File | fs.Error { scope s { fs.Open(path, s) } }
class C[T] { fn c(x: T, path: String) uses io: fs.File | fs.Error }
derive instance c[T]: C[T] { fn c(x: T, path: String) uses io: fs.File | fs.Error { u[T](path) } }
type R = { a: Int } derive (C)
type Q = { b: Int } derive (C)
fn main() {}`))
	if err == nil || strings.Count(err.Error(), "cannot return this value") != 1 || !strings.Contains(err.Error(), "function u cannot") {
		t.Fatalf("want one definition diagnostic, got %v", err)
	}
}

// Without shape operations a helper is an ordinary function, so the ordinary
// checker is the oracle for its lifetimes.
func TestDeriveWitnessLifetimesMatchOrdinaryFunctions(t *testing.T) {
	t.Parallel()
	bodies := []string{
		`(path: String) uses io: String | fs.Error { file = scope s { fs.Open(path, s)? }; fs.ReadAllText(file) }`,
		`(path: String) uses io: String | fs.Error { scope s { file = fs.Open(path, s)?; fs.ReadAllText(file) } }`,
		`(path: String) uses io: fs.File | fs.Error { scope s { fs.Open(path, s) } }`,
		`(app: Scope, path: String) uses io: fs.File | fs.Error { scope s { file = fs.Open(path, s)?; move(file, app) } }`,
		`(app: Scope, path: String) uses io: fs.File | fs.Error { scope s { file = fs.Open(path, s)?; attach(file, app) } }`,
		`(app: Scope) uses state { owner = openScope(app); closeScope(owner) }`,
		`(app: Scope) uses state { owner = openScope(app); closeScope(owner); closeScope(owner) }`,
	}
	for _, body := range bodies {
		imports := ""
		if strings.Contains(body, "fs.") {
			imports = "import \"bork/fs\"\n"
		}
		_, _, ordinary := Check(validatorFixture(t, imports+"fn unused"+body+"\nfn main() {}"))
		_, _, derived := Check(validatorFixture(t, imports+"derive fn unused[T]"+body+"\nfn main() {}"))
		if (ordinary == nil) != (derived == nil) {
			t.Fatalf("%s: ordinary %v, derive %v", body, ordinary, derived)
		}
	}
}
