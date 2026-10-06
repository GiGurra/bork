package driver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
)

func describeAt(t *testing.T, source, fragment, where string) *descriptionResult {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	offset := strings.Index(source, fragment)
	if offset < 0 {
		t.Fatalf("missing source fragment %q", fragment)
	}
	line := strings.Count(source[:offset], "\n") + 1
	column := offset - strings.LastIndex(source[:offset], "\n")
	result, err := Describe(fmt.Sprintf("%s:%d:%d", path, line, column), where)
	if err != nil {
		t.Fatal(err)
	}
	return &descriptionResult{result.Type, result.Proof != nil && result.Proof.Proven, result.Proof != nil && result.Proof.Reason != "", result.Definition != nil, len(result.Facts)}
}

type descriptionResult struct {
	typ                     string
	proven, reason, defined bool
	facts                   int
}

func TestDescribeProofs(t *testing.T) {
	t.Parallel()
	source := `pred positive(n: Int) { n > 0 }
pred small(n: Int) { n < 10 }
fn bounded(n: Int where positive): Int where positive { n }
fn scenario(xs: List[Int], n: Int) uses io {
  println(n) // before
  if (positive(n)) { println(n) /* guarded */ }
  if (!notEmpty(xs)) { return }
  ys = xs
  println(ys)
  println(bounded(1))
  println(7)
  println(0)
}
`
	cases := []struct {
		name, fragment, where, typ string
		proven, defined            bool
		facts                      int
	}{
		{"before guard", "n) // before", "positive", "Int", false, true, 0},
		{"inside guard", "n) /* guarded", "positive", "Int", true, true, 1},
		{"alias after early return", "ys)", "notEmpty", "List[Int]", true, true, 1},
		{"callee promise", "(1))", "positive", "Int", true, true, 1},
		{"constant true", "7)", "positive and small", "Int", true, false, 0},
		{"constant false", "0)", "positive", "Int", false, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := describeAt(t, source, tc.fragment, tc.where)
			if result.typ != tc.typ || result.proven != tc.proven || result.reason == tc.proven || result.defined != tc.defined || result.facts != tc.facts {
				t.Fatalf("unexpected description: %+v", result)
			}
		})
	}
}

func TestDescribeBoundPatternFacts(t *testing.T) {
	t.Parallel()
	source := `pred positive(n: Int) { n > 0 }
fn identity(n: Int): Int { n }
fn scenario(n: Int): Int {
  match (n) {
    value: Int where positive => identity(value) // guarded
    value: Int => identity(value) // fallback
  }
}
`
	guarded := describeAt(t, source, "value) // guarded", "positive")
	if !guarded.proven || guarded.facts == 0 {
		t.Fatalf("missing successful pattern facts: %+v", guarded)
	}
	fallback := describeAt(t, source, "value) // fallback", "positive")
	if fallback.proven || fallback.facts != 0 {
		t.Fatalf("guard facts leaked into fallback: %+v", fallback)
	}
}

func TestDescribeMethodsAndDefinitions(t *testing.T) {
	t.Parallel()
	source := `type User = { age: Int }
fn (u: User) ageText(): String { toString(u.age) }
fn add(x: Int, y: Int = 2): Int { x + y }
fn log(n: Int) uses io { println(n) }
fn scenario(xs: List[Int], u: User) uses io {
  println(add(1))
  println(xs.map(x => x + 1))
  println(u.ageText())
  println(u.age)
  log(1)
}
`
	for _, tc := range []struct{ fragment, typ string }{
		{"add(1)", "(Int, Int) => Int"},
		{"(1))", "Int"},
		{"map(x", "((Int) => Int) => List[Int]"},
		{"ageText())", "() => String"},
		{"age)", "Int"},
		{"log(1)", "(Int) uses io => Ok"},
	} {
		t.Run(tc.fragment, func(t *testing.T) {
			t.Parallel()
			result := describeAt(t, source, tc.fragment, "")
			if result.typ != tc.typ || !result.defined {
				t.Fatalf("unexpected description: %+v", result)
			}
		})
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Describe(path+":7:11", "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, method := range result.Methods {
		if method.Name == "first" {
			found = method.Type == "() => Int" && len(method.Requires) == 1 && method.Requires[0] == "xs: notEmpty"
			if method.Definition == nil || method.Definition.File != "prelude/lists.bork" {
				t.Fatalf("prelude method must point to its embedded topic file: %+v", method.Definition)
			}
		}
	}
	if !found {
		t.Fatalf("missing first method with inferred type and requirement: %+v", result.Methods)
	}
}

func TestDescribeFoldedExpressions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	source := "pred positive(x: Int8) { x > 0 }\nfn example() uses io {\n  n: Int8 = 128 - 1\n  println(n)\n}\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, column := range []int{13, 17, 19} {
		result, err := Describe(fmt.Sprintf("%s:3:%d", path, column), "positive")
		if err != nil {
			t.Fatal(err)
		}
		if result.Type != "Int8" || result.Expression != "128 - 1" || !result.Proof.Proven {
			t.Fatalf("expected the complete folded expression, got %+v", result)
		}
	}
	for _, expression := range []string{"(1 + 2) * (3 + 4)", "1 * (2 + 3)", "(1 + 2) * 3"} {
		if err := os.WriteFile(path, []byte("fn example() uses io { println("+expression+") }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		column := len("fn example() uses io { println(") + strings.Index(expression, "1") + 1
		result, err := Describe(fmt.Sprintf("%s:1:%d", path, column), "")
		if err != nil {
			t.Fatal(err)
		}
		if result.Expression != expression {
			t.Fatalf("expected balanced %q, got %q", expression, result.Expression)
		}
	}
}

func TestDescribeInterpolationCallees(t *testing.T) {
	t.Parallel()
	source := `type User = { n: Int }
fn add(x: Int): Int { x + 1 }
fn (u: User) number(): Int { u.n }
fn example(u: User) uses io {
  println(s"value ${add(1)} and ${u.number()}")
  println((add)(1))
  println((u.number)())
}
`
	for _, tc := range []struct{ fragment, typ string }{
		{"add(1)", "(Int) => Int"},
		{"(1)}", "Int"},
		{"number()}", "() => Int"},
		{"()}", "Int"},
		{"add)(1)", "(Int) => Int"},
		{"number)()", "() => Int"},
		{"())", "Int"},
	} {
		result := describeAt(t, source, tc.fragment, "")
		if result.typ != tc.typ || !result.defined {
			t.Fatalf("interpolated %s: %+v", tc.fragment, result)
		}
	}
}

func TestDescribeFunctionFieldResult(t *testing.T) {
	t.Parallel()
	source := "type Holder = { f: (Int) => Int }\nfn example(h: Holder) uses io { println(h.f(1)) }\n"
	result := describeAt(t, source, "(1))", "")
	if result.typ != "Int" {
		t.Fatalf("unexpected field call result: %+v", result)
	}
}

func TestDescribeFoldedInterpolation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	source := "fn example() uses io { println(s\"value ${(1 + 2) * 3}\") }\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Describe(fmt.Sprintf("%s:1:%d", path, strings.Index(source, "1")+1), "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Expression != "(1 + 2) * 3" {
		t.Fatalf("unbalanced folded interpolation: %q", result.Expression)
	}
}

func TestDescribeRulesAndConstraintArguments(t *testing.T) {
	t.Parallel()
	source := `pred positive(x: Int) { x > 0 }
pred nonNegative(x: Int) { x >= 0 }
pred atLeast(x: Int, minimum: Int) { x >= minimum }
rule weaken(x: Int) { positive(x) => nonNegative(x) }
fn example(n: Int where positive, m: Int where atLeast(n)) uses io {
  println(n) // by rule
  println(m) // relational
  println(3) // constants
}
`
	for _, tc := range []struct{ fragment, where string }{
		{"n) // by rule", "nonNegative"},
		{"m) // relational", "atLeast(n)"},
		{"3) // constants", "atLeast(2)"},
	} {
		result := describeAt(t, source, tc.fragment, tc.where)
		if !result.proven || result.typ != "Int" {
			t.Fatalf("unexpected rule/argument proof: %+v", result)
		}
	}
}

func TestDescribeComparisonGuards(t *testing.T) {
	t.Parallel()
	source := `pred positive(n: Int) { n > 0 }
pred ordered(n: Int, bound: Int) { n <= bound }
fn example(n: Int, bound: Int) uses io {
  if (n > 0 && n <= bound) { println(n) } // guarded
  println(n) // outside
}
`
	for _, tc := range []struct {
		fragment, where string
		proven          bool
	}{
		{"n) } // guarded", "positive", true},
		{"n) } // guarded", "ordered(bound)", true},
		{"n) // outside", "positive", false},
	} {
		result := describeAt(t, source, tc.fragment, tc.where)
		if result.proven != tc.proven {
			t.Fatalf("%s at %s: %+v", tc.where, tc.fragment, result)
		}
	}
}

func TestDescribeGolden(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "..", "testdata", "cases", "describe_queries")
	data, err := os.ReadFile(filepath.Join(dir, "queries.json"))
	if err != nil {
		t.Fatal(err)
	}
	var queries []struct {
		Line, Column int
		Where        string
	}
	if err := json.Unmarshal(data, &queries); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, query := range queries {
		result, err := Describe(fmt.Sprintf("%s:%d:%d", filepath.Join(dir, "main.bork"), query.Line, query.Column), query.Where)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(&out).Encode(result); err != nil {
			t.Fatal(err)
		}
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	compare(t, filepath.Join(dir, "expected_describe.jsonl"), strings.ReplaceAll(out.String(), absDir+string(filepath.Separator), ""))
}

func TestDescribeMethodVisibility(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files := map[string]string{
		"bork.mod": "module example.com/query\n",
		"money/main.bork": `type User = { n: Int }
fn New(): User { User { n: 1 } }
fn (u: User) Describe(): String { "money" }
fn (u: User) Format(): String { "money" }
fn (u: User) Unique(): String { "unique" }
fn (u: User) hidden(): String { "private" }
`,
		"other/main.bork": `import "example.com/query/money"
fn (u: money.User) Format(): String { "other" }
fn Tag(): String { "other" }
`,
		"main.bork": `import "example.com/query/money"
import "example.com/query/other"
fn (u: money.User) Describe(): String { "root" }
fn main() {
  user = money.New()
  println(other.Tag())
  println(user)
}
`,
	}
	for name, source := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "main.bork")
	result, err := Describe(path+":7:11", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Methods) != 3 || result.Methods[0].Name != "Describe" || result.Methods[0].Definition.File != path || result.Methods[1].Name != "Format" || result.Methods[1].Ambiguity == "" || result.Methods[1].Definition != nil {
		t.Fatalf("method precedence, visibility, or ambiguity mismatch: %+v", result.Methods)
	}
	if result.Methods[2].Name != "Unique" || result.Methods[2].Definition.File != filepath.Join(dir, "money/main.bork") {
		t.Fatalf("imported method must have an absolute definition: %+v", result.Methods[2])
	}
	result, err = Describe(path+":5:10", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Definition == nil || result.Definition.File != filepath.Join(dir, "money/main.bork") {
		t.Fatalf("imported definition must have an absolute path: %+v", result.Definition)
	}
	result, err = Describe(path+":6:17", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Definition == nil || result.Definition.File != filepath.Join(dir, "other/main.bork") {
		t.Fatalf("imported function must have an absolute path: %+v", result.Definition)
	}
}

func TestDescribeInvalidQueries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte("fn example(n: Int) uses io { println(n) }\npred positive(n: Int) { n > 0 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ position, where, message string }{
		{path, "", "expected file:line:column"},
		{path + ":0:1", "", "positive integers"},
		{path + ":1:0", "", "positive integers"},
		{path + ":999:1", "", "outside the source"},
		{path + ":1:3", "", "no expression or local name"},
		{path + ":1:38", "missing", "unknown predicate"},
		{path + ":1:38", "notEmpty", "applies to"},
		{path + ":1:38", "positive and missing", "unknown predicate"},
		{path + ":1:38", "positive and notEmpty", "applies to"},
		{path + ":1:38", "(", "invalid where query"},
	} {
		t.Run(tc.position+tc.where, func(t *testing.T) {
			t.Parallel()
			_, err := Describe(tc.position, tc.where)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected %q, got %v", tc.message, err)
			}
		})
	}
}

func TestDescribeBytePositionsAndDeclarations(t *testing.T) {
	t.Parallel()
	source := `pred positive(n: Int) { n > 0 }
fn example(n: Int where positive) uses io {
	println("å"); println(n) // utf8
	bound: Int where positive = n
	println(bound)
	println(s"value ${n}")
}
`
	for _, tc := range []struct{ fragment, where string }{
		{"n) // utf8", "positive"},
		{"n: Int where positive", "positive"},
		{"bound: Int", "positive"},
		{"n}", "positive"},
	} {
		t.Run(tc.fragment, func(t *testing.T) {
			t.Parallel()
			// The parameter fragment appears first in the predicate; select
			// the constrained function parameter by its more specific text.
			result := describeAt(t, source, tc.fragment, tc.where)
			if result.typ != "Int" || !result.proven || !result.defined {
				t.Fatalf("unexpected declaration/byte-position query: %+v", result)
			}
		})
	}
}

func TestDescribeMethodReferences(t *testing.T) {
	t.Parallel()
	source := "fn main() {\n f: (List[Int]) => Int = List.length\n println(f([1]))\n println(List.length([1]))\n}\n"
	for _, fragment := range []string{"List.length", "length\n", "List.length([", "length(["} {
		result := describeAt(t, source, fragment, "")
		if result.typ != "(List[Int]) => Int" || !result.defined {
			t.Fatalf("unexpected method reference description: %+v", result)
		}
	}
}

func TestDescribeNamedArguments(t *testing.T) {
	t.Parallel()
	source := `fn config(host: String, port: Int = 8080): String { host }
fn scenario() { _ = config(port: 9000, host: "local") }
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Describe(path+":2:21", "")
	if err != nil {
		t.Fatal(err)
	}
	callable := result.Callable
	if callable == nil || !callable.NamedArguments || !callable.ParameterNamesAreAPI || len(callable.Parameters) != 2 || callable.Parameters[0].Name != "host" || callable.Parameters[1].Default != "8080" {
		t.Fatalf("missing callable API: %+v", callable)
	}
	label, err := Describe(path+":2:28", "")
	if err != nil {
		t.Fatal(err)
	}
	if label.Type != "Int" || label.Definition == nil || label.Definition.Line != 1 || label.Definition.Col != 25 {
		t.Fatalf("label must select the declared parameter: %+v", label)
	}
	value, err := Describe(path+":2:34", "")
	if err != nil {
		t.Fatal(err)
	}
	if value.Type != "Int" || value.Callable != nil {
		t.Fatalf("unexpected argument description: %+v", value)
	}
}

func TestDescribeOwnedScopeLifetimes(t *testing.T) {
	t.Parallel()
	source := `fn roll(prev: OwnedScope in app, task: Task[Int] in prev, app: Scope) uses state {
  next = openScope(app)
  fresh = fork(next.scope, () => 1); _ = fresh
  closeScope(prev)
  closeScope(next)
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		fragment, typ, belongs string
	}{
		{"task: Task", "Task[Int]", "owned scope prev"},
		{"prev: OwnedScope", "OwnedScope", "parameter app"},
		{"fresh =", "Task[Int]", "owned scope next"},
		{"scope, ()", "Scope", "owned scope next"},
	}
	for _, c := range cases {
		offset := strings.Index(source, c.fragment)
		line := strings.Count(source[:offset], "\n") + 1
		column := offset - strings.LastIndex(source[:offset], "\n")
		result, err := Describe(fmt.Sprintf("%s:%d:%d", path, line, column), "")
		if err != nil {
			t.Fatal(err)
		}
		if result.Type != c.typ || strings.Join(result.BelongsTo, ", ") != c.belongs {
			t.Errorf("%q: got %s belonging to %v, want %s belonging to %s", c.fragment, result.Type, result.BelongsTo, c.typ, c.belongs)
		}
	}
}

func TestDescribeContextConstructors(t *testing.T) {
	t.Parallel()
	source := `type Config = { port: Int }
type State = sealed { Ready, Value { value: Int } }
fn main() {
  config: Config = .{ port: 80 }
  state: State = .Ready
  value: State = .Value { value: 1 }
  option: Option[Int] = .Some(2)
  println(config); println(state); println(value); println(option)
}
`
	for _, tc := range []struct{ fragment, typ string }{
		{".{ port", "Config"}, {".Ready", "State"}, {"Ready\n", "State"},
		{".Value {", "State"}, {"Value { value: 1", "State"}, {".Some", "Option[Int]"},
	} {
		t.Run(tc.fragment, func(t *testing.T) {
			t.Parallel()
			result := describeAt(t, source, tc.fragment, "")
			if result.typ != tc.typ || !result.defined {
				t.Fatalf("unexpected context constructor description: %+v", result)
			}
		})
	}
}

func TestDescribeMocks(t *testing.T) {
	t.Parallel()
	source := `pred positive(n: Int) { n > 0 }
fn Clamp(n: Int where positive) uses io: Int where positive { n }
fn main() { println(Clamp(1)) }
test "mocked" {
  calls = mock Clamp(m) { m /* body */ }
  assertEqual(Clamp(2), 2)
  assertEqual(calls.count(), 1)
}
`
	cases := []struct {
		name, fragment, where, typ string
		proven, defined            bool
	}{
		{"handle", "calls = mock", "", "Mock[ClampCall]", false, true},
		{"target", "Clamp(m)", "", "(Int) uses io => Int", false, true},
		{"parameter", "m) {", "positive", "Int", true, true},
		{"parameter in the body", "m /* body", "positive", "Int", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := describeAt(t, source, c.fragment, c.where)
			if got.typ != c.typ || got.proven != c.proven || got.defined != c.defined {
				t.Fatalf("got %+v, want type %s, proven %v, defined %v", got, c.typ, c.proven, c.defined)
			}
		})
	}
}

func TestDescribeNeeds(t *testing.T) {
	t.Parallel()
	source := `ambient traceId: String
ambient locale: String

fn tag(msg: String) needs traceId + locale?: String {
  s"[${traceId}] ${msg} ${locale.getOr("")}"
}

fn use() needs traceId: String {
  tag("x")
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	offset := strings.Index(source, `tag("x")`)
	line := strings.Count(source[:offset], "\n") + 1
	column := offset - strings.LastIndex(source[:offset], "\n")
	result, err := Describe(fmt.Sprintf("%s:%d:%d", path, line, column), "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Callable == nil || strings.Join(result.Callable.Needs, " + ") != "locale? + traceId" {
		t.Fatalf("got %+v, want needs locale? + traceId", result.Callable)
	}
}

func TestDescribeSpecializedConstructors(t *testing.T) {
	t.Parallel()
	source := `type Duration = go "time.Duration"
type Handle = resource
type Box[T] = { values: List[T] }
type State[T] = sealed { Empty, Value { value: T } }
fn main() {
 x = Box[Option[Int]] { values: [.None] }
 y = State[String].Value { value: "text" }
 z = State[Int].Empty
 d = Option[Duration].None
 h = Option[Handle].None
 println(x); println(y); println(z); println(d); println(h)
}
`
	for _, tc := range []struct {
		fragment, typ string
		defined       bool
	}{
		{"Box[Option", "Box[Option[Int]]", true},
		{"Option[Int]]", "Option[Int]", true},
		{"Int]]", "Int", false},
		{"Duration].None", "Duration", true},
		{"Handle].None", "Handle", true},
		{"State[String]", "State[String]", true},
		{"String].Value", "String", false},
		{"Value { value: \"text\"", "State[String]", true},
		{"State[Int].Empty", "State[Int]", true},
		{"Empty\n", "State[Int]", true},
	} {
		t.Run(tc.fragment, func(t *testing.T) {
			t.Parallel()
			r := describeAt(t, source, tc.fragment, "")
			if r.typ != tc.typ || r.defined != tc.defined {
				t.Fatalf("want %s (definition %v), got %+v", tc.typ, tc.defined, r)
			}
		})
	}
}

func TestDescribeSpecializedDefaults(t *testing.T) {
	t.Parallel()
	source := `pred positive(x: Int) { x > 0 }
fn take(x: Option[(Int) uses io => Int] = Option[(Int) uses io => Int].None,
 y: Option[Int] = Option[Int where positive].Some(1), z: Option[Seq[Int] uses io] = Option[Seq[Int] uses io].None) uses io { println(x); println(y); println(z) }
fn main() { take() }
`
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Describe(fmt.Sprintf("%s:4:13", path), "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Callable == nil || len(result.Callable.Parameters) != 3 {
		t.Fatalf("missing callable: %+v", result)
	}
	for i, want := range []string{"Option[(Int) uses io => Int].None", "Option[Int where positive].Some(1)", "Option[Seq[Int] uses io].None"} {
		if got := result.Callable.Parameters[i].Default; got != want {
			t.Fatalf("default %d: got %q, want %q", i, got, want)
		}
	}
}

func TestDescribeRecordConversion(t *testing.T) {
	t.Parallel()
	source := `type A = { n: Int }
type B = { n: Int, text: String }
type Err = { reason: String }
fn fail(flag: Bool): Int | Err { if (flag) { Err { reason: "failed" } } else { 1 } }
fn convert(a: A, flag: Bool): B | Err { a.into[B](n: fail(flag), text: "ok") }
fn main() {}
`
	for _, token := range []string{"into[B]", "[B]", "(n:"} {
		result := describeAt(t, source, token, "")
		if result.typ != "B | Err" {
			t.Errorf("%s describes %s; want B | Err", token, result.typ)
		}
	}
	result := describeAt(t, source, "(flag), text:", "")
	if result.typ != "Int | Err" {
		t.Errorf("override describes %s; want Int | Err", result.typ)
	}
}

func TestDescribeFunctionRequirementProof(t *testing.T) {
	t.Parallel()
	source := "pred positive(n: Int) { n > 0 }\nfn value(n: Int) where positive(n): Int { n }\n"
	result := describeAt(t, source, "n }", "positive")
	if !result.proven || result.facts != 1 {
		t.Fatalf("function requirement missing from body facts: %+v", result)
	}
}

func TestDescribeNamedInterpolation(t *testing.T) {
	t.Parallel()
	source := `type Builder = {}
type Output = { text: String }
fn Tag(parts: StaticParts): Builder { Builder {} }
fn (b: Builder) Interpolate[T: Show](value: T): Builder { b }
fn (b: Builder) Finish(): Output { Output { text: "" } }
fn add(x: Int): Int { x + 1 }
fn example(name: String) uses io {
  println(Tag"$name ${add(1)} ${Tag"nested".text} ${1 + 2}".text)
}
`
	for _, tc := range []struct{ fragment, typ string }{
		{`Tag"$name`, "Output"},
		{`name ${`, "String"},
		{"add(1)", "(Int) => Int"},
		{"(1)}", "Int"},
		{`Tag"nested`, "Output"},
		{"1 + 2", "Int"},
	} {
		t.Run(tc.fragment, func(t *testing.T) {
			result := describeAt(t, source, tc.fragment, "")
			if result.typ != tc.typ {
				t.Fatalf("interpolated %s: %+v", tc.fragment, result)
			}
		})
	}
	qualified := `import dbsql "bork/sql"
fn example(value: Int) {
  statement = dbsql.SQL"SELECT $value"
  _ = statement
}
`
	for _, spelling := range []string{"dbsql.SQL", "dbsql    .    SQL", "dbsql/*comment*/.SQL"} {
		for _, prefix := range []string{spelling, `SQL"`} {
			result := describeAt(t, strings.Replace(qualified, "dbsql.SQL", spelling, 1), prefix, "")
			if result.typ != "dbsql.Statement" || !result.defined {
				t.Fatalf("qualified interpolator %s: %+v", prefix, result)
			}
		}
	}
}

// A select's lowered code calls prelude helpers; describe shows what was
// written instead.
func TestDescribeSelect(t *testing.T) {
	t.Parallel()
	source := `fn f(s: Scope, ch: Channel[Int], out: Channel[Int]) uses state: String | Cancelled {
  select {
    x = ch.receive(s) => s"$x"
    y = out.send(s, 2) => s"$y"
  }
}
fn main() {}
`
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for at, want := range map[string]string{"2:3": "String | Cancelled", "3:5": "Int | Closed", "3:9": "Channel[Int]", "4:5": "Ok | Closed", "4:9": "Channel[Int]"} {
		result, err := Describe(path+":"+at, "")
		if err != nil {
			t.Fatal(err)
		}
		if result.Type != want || result.Definition != nil && strings.Contains(result.Definition.File, "prelude") {
			t.Errorf("%s: type %s, defined at %v; want %s in the source", at, result.Type, result.Definition, want)
		}
	}
}

func TestDescribeOwnership(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files := map[string]string{
		"bork.mod": "module example.com/own\nunsafe \"example.com/own\"\n",
		"main.bork": `type Conn = resource
fn connect(s: Scope) uses io: Conn unsafe go {
  return Conn{handle: 1, owner: s.Own(func() {})}
}
fn query(c: Conn): String unsafe go {
  return ""
}
fn borrow(c: Conn): String { query(c) }
fn maybe(s: Scope, flag: Bool) uses io: Conn | String {
  if (flag) { "none" } else { connect(s) }
}
fn early(s: Scope, w: Scope, flag: Bool) uses io: String {
  if (flag) {
    a = connect(s)
    _ = move(a, w)
    return "moved"
  }
  outer = connect(s)
  if (flag) { _ = move(outer, w) }
  match (maybe(s, flag)) {
    c: Conn => {
      _ = move(c, w)
      "matched"
    }
    other: String => other
  }
}
fn main() {
  scope s {
    a = connect(s)
    println(query(a))
    scope w { _ = move(a, w) }
    b = connect(s)
    fork(s, () => println(query(b)))
    d = connect(s)
    f = () => query(d)
    println(f())
    e = connect(s)
    for (i in [1, 2]) { println(query(e)); println(i) }
  }
}
fn more(s: Scope, w: Scope, v: Scope, flag: Bool) uses io: String {
  a = connect(s)
  if (flag) { _ = move(a, w) } else { _ = move(a, v) }
  b = connect(s)
  if (flag) { _ = move(b, w) } else { return "early" }
  e = connect(s)
  for (x in [e]) { c = e; println(query(c)); println(query(x)) }
  k = connect(s)
  fork(s, () => println(query(k)))
  k2 = k; println(query(k2))
  t = connect(s)
  scope u { fork(u, () => println(query(t))) }
  query(t)
}
`,
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "main.bork")
	borrowed := "borrowed (not acquired here), so it cannot be moved; attach it instead"
	for _, tc := range []struct{ pos, want string }{
		{"8:11", borrowed},
		{"8:36", borrowed},
		{"14:5", "moved to scope w at line 15"},
		{"18:3", "possibly moved to scope w at line 19"},
		{"21:5", "moved to scope w at line 22"},
		{"30:5", "moved to scope w at line 32"},
		{"31:19", "owned by scope s and acquired here, so move can hand it to another scope"},
		{"33:5", "owned by scope s, and kept by the task of s started at line 34, so it cannot be moved"},
		{"39:39", "owned by scope s, but acquired outside this loop, so it cannot be moved here"},
		{"43:3", "moved to scope w or scope v at line 44"},
		{"45:3", "moved to scope w at line 46"},
		{"48:20", "owned by parameter s, but acquired outside this loop, so it cannot be moved here"},
		{"48:8", borrowed},
		{"51:3", "owned by parameter s, and kept by the task of s started at line 50, so it cannot be moved"},
		{"52:3", "owned by parameter s and acquired here, so move can hand it to another scope"},
		{"36:21", "owned by scope s, but acquired outside this lambda (or lazy, async or comptime body), so it cannot be moved here"},
	} {
		result, err := Describe(path+":"+tc.pos, "")
		if err != nil {
			t.Fatal(err)
		}
		if result.Ownership != tc.want {
			t.Errorf("%s: ownership %q, want %q", tc.pos, result.Ownership, tc.want)
		}
	}
}

func TestDescribeTailCalls(t *testing.T) {
	t.Parallel()
	source := `fn count(n: Int): Int {
  if (n == 0) { 0 } else { count(n - 1) }
}

fn fact(n: Int): Int {
  if (n == 0) { 1 } else { n * fact(n - 1) }
}

fn main() {
  println(count(3), fact(3))
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	describeAt := func(needle string) *check.TailCall {
		t.Helper()
		offset := strings.Index(source, needle)
		line := strings.Count(source[:offset], "\n") + 1
		column := offset - strings.LastIndex(source[:offset], "\n")
		result, err := Describe(fmt.Sprintf("%s:%d:%d", path, line, column), "")
		if err != nil {
			t.Fatal(err)
		}
		return result.TailCall
	}
	if tc := describeAt("count(n - 1)"); tc == nil || !tc.Jump {
		t.Fatalf("got %+v, want a jump", tc)
	}
	if tc := describeAt("fact(n - 1)"); tc == nil || tc.Jump || tc.Reason != "its result is used after it returns" {
		t.Fatalf("got %+v, want an ordinary call whose result is used", tc)
	}
	if tc := describeAt("count(3)"); tc != nil {
		t.Fatalf("got %+v for a call from another function", tc)
	}
}

func TestDescribeLoops(t *testing.T) {
	t.Parallel()
	source := `pred nonNegative(n: Int) { n >= 0 }

fn main() {
  for (i: Int where nonNegative = 3; i > 0; i = i - 1) {
    println(i)
  }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	offset := strings.Index(source, "println(i)") + len("println(")
	line := strings.Count(source[:offset], "\n") + 1
	column := offset - strings.LastIndex(source[:offset], "\n")
	result, err := Describe(fmt.Sprintf("%s:%d:%d", path, line, column), "")
	if err != nil {
		t.Fatal(err)
	}
	var facts []string
	for _, fact := range result.Facts {
		facts = append(facts, fact.Constraint)
	}
	if result.Type != "Int" || !slices.Contains(facts, "nonNegative") {
		t.Fatalf("got %s with facts %v, want Int known nonNegative", result.Type, facts)
	}
}

// A carried name's values that no binding gives are defined where the
// name was first bound, and keep its declared facts after the loop.
func TestDescribeCarried(t *testing.T) {
	t.Parallel()
	source := `pred nonNegative(n: Int) { n >= 0 }

fn main() {
  total: Int where nonNegative = 0
  for (x in [1, 2]) {
    if (nonNegative(x)) {
      total = x
    }
  }
  println(total)
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	offset := strings.Index(source, "println(total)") + len("println(")
	line := strings.Count(source[:offset], "\n") + 1
	column := offset - strings.LastIndex(source[:offset], "\n")
	result, err := Describe(fmt.Sprintf("%s:%d:%d", path, line, column), "")
	if err != nil {
		t.Fatal(err)
	}
	var facts []string
	for _, fact := range result.Facts {
		facts = append(facts, fact.Constraint)
	}
	if result.Definition == nil || result.Definition.Line != 4 || !slices.Contains(facts, "nonNegative") {
		t.Fatalf("got definition %v with facts %v, want line 4 known nonNegative", result.Definition, facts)
	}
}

func TestDescribePositionalVariantConstructors(t *testing.T) {
	source := `type Pair[T]=sealed{Pair(T,String),Empty}
fn main(){println(Pair[Int].Pair(1,"x"),Option.Some(3))}`
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		fragment, typ string
		arity         int
	}{
		{"Pair(1", "(Int, String) => Pair[Int]", 2},
		{"Some(3", "(Int) => Option[Int]", 1},
	} {
		offset := strings.Index(source, tc.fragment)
		column := offset - strings.LastIndex(source[:offset], "\n")
		result, err := Describe(fmt.Sprintf("%s:2:%d", path, column), "")
		if err != nil {
			t.Fatal(err)
		}
		if result.Type != tc.typ || result.Callable == nil || len(result.Callable.Parameters) != tc.arity || result.Callable.NamedArguments || result.Callable.ParameterNamesAreAPI {
			t.Fatalf("%s: %+v", tc.fragment, result)
		}
		for _, p := range result.Callable.Parameters {
			if p.Name != "" {
				t.Fatalf("exposed internal name: %+v", p)
			}
		}
		resultColumn := column + strings.Index(tc.fragment, "(")
		value, err := Describe(fmt.Sprintf("%s:2:%d", path, resultColumn), "")
		if err != nil {
			t.Fatal(err)
		}
		wantResult := strings.Split(tc.typ, " => ")[1]
		if value.Type != wantResult || value.Callable != nil {
			t.Fatalf("constructor result: %+v", value)
		}
	}
}

func TestDescribeGoTaggedField(t *testing.T) {
	t.Parallel()
	source := `type Row = { value: Int go { json: "wire_value" } } derive (GoStruct)
fn read(row: Row): Int { row.value }
`
	result := describeAt(t, source, "value }", "")
	if result.typ != "Int" || !result.defined {
		t.Fatalf("tagged field query: %+v", result)
	}
}
