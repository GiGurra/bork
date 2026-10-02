package driver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	source := `pred positive(n: Int) { n > 0 }
pred small(n: Int) { n < 10 }
fn bounded(n: Int where positive): Int where positive { n }
fn scenario(xs: List[Int], n: Int) {
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
			result := describeAt(t, source, tc.fragment, tc.where)
			if result.typ != tc.typ || result.proven != tc.proven || result.reason == tc.proven || result.defined != tc.defined || result.facts != tc.facts {
				t.Fatalf("unexpected description: %+v", result)
			}
		})
	}
}

func TestDescribeMethodsAndDefinitions(t *testing.T) {
	source := `type User = { age: Int }
fn (u: User) ageText(): String { toString(u.age) }
fn add(x: Int, y: Int = 2): Int { x + y }
fn scenario(xs: List[Int], u: User) {
  println(add(1))
  println(xs.map(x => x + 1))
  println(u.ageText())
  println(u.age)
}
`
	for _, tc := range []struct{ fragment, typ string }{
		{"add(1)", "(Int, Int) => Int"},
		{"(1))", "Int"},
		{"map(x", "((Int) => Int) => List[Int]"},
		{"ageText())", "() => String"},
		{"age)", "Int"},
	} {
		t.Run(tc.fragment, func(t *testing.T) {
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
	result, err := Describe(path+":6:11", "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, method := range result.Methods {
		if method.Name == "first" {
			found = method.Type == "() => Int" && len(method.Requires) == 1 && method.Requires[0] == "xs: notEmpty"
		}
	}
	if !found {
		t.Fatalf("missing first method with inferred type and requirement: %+v", result.Methods)
	}
}

func TestDescribeFoldedExpressions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	source := "pred positive(x: Int8) { x > 0 }\nfn example() {\n  n: Int8 = 128 - 1\n  println(n)\n}\n"
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
		if err := os.WriteFile(path, []byte("fn example() { println("+expression+") }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		column := len("fn example() { println(") + strings.Index(expression, "1") + 1
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
	source := `type User = { n: Int }
fn add(x: Int): Int { x + 1 }
fn (u: User) number(): Int { u.n }
fn example(u: User) {
  println(s"value ${add(1)} and ${u.number()}")
}
`
	for _, tc := range []struct{ fragment, typ string }{
		{"add(1)", "(Int) => Int"},
		{"(1)}", "Int"},
		{"number()}", "() => Int"},
		{"()}", "Int"},
	} {
		result := describeAt(t, source, tc.fragment, "")
		if result.typ != tc.typ || !result.defined {
			t.Fatalf("interpolated %s: %+v", tc.fragment, result)
		}
	}
}

func TestDescribeRulesAndConstraintArguments(t *testing.T) {
	source := `pred positive(x: Int) { x > 0 }
pred nonNegative(x: Int) { x >= 0 }
pred atLeast(x: Int, minimum: Int) { x >= minimum }
rule weaken(x: Int) { positive(x) => nonNegative(x) }
fn example(n: Int where positive, m: Int where atLeast(n)) {
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

func TestDescribeGolden(t *testing.T) {
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
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte("fn example(n: Int) { println(n) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ position, where, message string }{
		{path, "", "expected file:line:column"},
		{path + ":0:1", "", "positive integers"},
		{path + ":1:0", "", "positive integers"},
		{path + ":999:1", "", "outside the source"},
		{path + ":1:3", "", "no expression or local name"},
		{path + ":1:29", "missing", "unknown predicate"},
		{path + ":1:29", "notEmpty", "applies to"},
		{path + ":1:29", "(", "invalid where query"},
	} {
		t.Run(tc.position+tc.where, func(t *testing.T) {
			_, err := Describe(tc.position, tc.where)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected %q, got %v", tc.message, err)
			}
		})
	}
}

func TestDescribeBytePositionsAndDeclarations(t *testing.T) {
	source := `pred positive(n: Int) { n > 0 }
fn example(n: Int where positive) {
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
			// The parameter fragment appears first in the predicate; select
			// the constrained function parameter by its more specific text.
			result := describeAt(t, source, tc.fragment, tc.where)
			if result.typ != "Int" || !result.proven || !result.defined {
				t.Fatalf("unexpected declaration/byte-position query: %+v", result)
			}
		})
	}
}
