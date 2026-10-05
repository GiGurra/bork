package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestDeriveTemplateLabels(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
class Labels[T] { fn labels(x: T): List[String] }
derive instance labels[T]: Labels[T] {
 fn labels(x: T): List[String] {
  [comptime for (field in shape.fields[T]()) comptime if (!field.computed) field.name]
 }
}
type Item = { name: String, count: Int }
derive Labels for Item
type Empty = {} derive(Labels)
type Box[A] = { value: A } derive(Labels)
fn main() {
 println(labels(Item { name: "one", count: 1 }))
 println(labels(Empty {}))
 println(labels(Box { value: true }))
}`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `["name", "count"]`) || !strings.Contains(string(output), `["value"]`) {
		t.Fatalf("labels: %s", output)
	}
}

func TestDeriveTemplateBranchSelection(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
class Labels[T] { fn labels(x: T): List[String] }
derive instance labels[T]: Labels[T] {
 fn labels(x: T): List[String] {
  comptime match (shape.kind[T]()) {
   shape.Record => {
    fields = shape.fields[T]()
    [comptime for (f in fields) comptime if (!f.computed) f.name]
   }
   shape.Sealed => [comptime for (v in shape.variants[T]()) v.name]
   _ => []
  }
 }
}
type Item = { n: Int, lazy next: Int = n + 1 } derive(Labels)
type Choice = sealed { First { n: Int }, Second } derive(Labels)
fn main() {
 println(labels(Item { n: 1 }))
 println(labels(Choice.Second))
}`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `["n"]`) || !strings.Contains(string(output), `["First", "Second"]`) {
		t.Fatalf("labels: %s", output)
	}
}

func TestDeriveTemplateChecks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"scalar annotation", `class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] { fn c(x: T): String { bad: Int = "wrong"; bad } }
type X = {} derive(C)
fn main() {}`, "must be Int"},
		{"target application", `class C[T] { fn c(x: T): Int }
derive instance c[T]: C[T] { fn c(x: T[Int]): Int { 1 } }
fn main() {}`, "cannot have type arguments"},
		{"runtime metadata", `import "bork/shape"
fn main() { println(shape.fields[Int]()) }`, "only during derive template expansion"},
		{"signature", `class C[T] { fn c(x: T): Int }
derive instance c[T]: C[T] { fn c(x: T): String { "wrong" } }
fn main() {}`, "must match its class"},
		{"missing method", `class C[T] { fn c(x: T): Int }
derive instance c[T]: C[T] {}
fn main() {}`, "missing method c"},
		{"unsafe template", `class C[T] { fn c(x: T): Int }
derive instance c[T]: C[T] { fn c(x: T): Int unsafe go { return 1 } }
fn main() {}`, "must have Bork bodies"},
		{"missing stage", `import "bork/shape"
class C[T] { fn c(x: T) }
derive instance c[T]: C[T] { fn c(x: T) { for (f in shape.fields[T]()) {} } }
type X = {} derive(C)
fn main() {}`, "add comptime"},
		{"escape", `import "bork/shape"
class C[T] { fn c(x: T): Int }
derive instance c[T]: C[T] { fn c(x: T): Int { shape.fields[T]() } }
type X = {} derive(C)
fn main() {}`, "cannot escape into runtime"},
	} {
		t.Run(tc.name, func(t *testing.T) { checkPreludeSource(t, tc.source, tc.want) })
	}
}

func TestDeriveTemplateFieldReads(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
fn text[A](x: A): String { toString(x) }
class Values[T] { fn values(x: T): List[String] }
derive instance values[T]: Values[T] {
 fn values(x: T): List[String] {
  [comptime for (f in shape.fields[T]()) text[f.Type](f.read(x))]
 }
}
type Item = { name: String, n: Int, lazy next: Int = n + 1 } derive(Values)
type Box[A] = { value: A } derive(Values)
fn main() {
 println(values(Item { name: "one", n: 1 }))
 println(values(Box { value: true }))
}`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `["one", "1", "2"]`) || !strings.Contains(string(output), `["true"]`) {
		t.Fatalf("values: %s", output)
	}
}

func TestDeriveTemplateInferredFieldBounds(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
class Text[T] { fn text(x: T): String }
instance textInt: Text[Int] { fn text(x: Int): String { toString(x) } }
class Values[T] { fn values(x: T): List[String] }
derive instance values[T]: Values[T] {
 fn values(x: T): List[String] {
  [comptime for (f in shape.fields[T]()) text[f.Type](f.read(x))]
 }
}
type Box[A] = { value: A } derive(Values)
fn main() { println(values(Box { value: 7 })) }`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `["7"]`) {
		t.Fatalf("values: %s", output)
	}
}

func TestDeriveTemplateTransitiveBounds(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
class C[T] { fn c(x: T): List[String] }
instance cInt: C[Int] { fn c(x: Int): List[String] { [toString(x)] } }
derive instance c[T]: C[T] {
 fn c(x: T): List[String] {
  [comptime for (f in shape.fields[T]()) toString(c[f.Type](f.read(x)))]
 }
}
type Outer[A] = { inner: Inner[A] } derive(C)
type Inner[A] = { value: A } derive(C)
lazy saved = c(Outer { inner: Inner { value: 7 } })
fn main() { println(saved); println(c(Outer { inner: Inner { value: 8 } })) }`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "7") || !strings.Contains(string(output), "8") {
		t.Fatalf("transitive: %s", output)
	}
}

func TestDeriveTemplateRejectedCandidateBounds(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
class D[T] { fn d(x: T): String }
class E[T] { fn e(x: T): String }
class C[T] { fn c(x: T): String }
type Pair[A, B] = { a: A, b: B }
instance constrained[A: D, B: E]: C[Pair[A, B]] { fn c(x: Pair[A, B]): String { "constrained" } }
instance fallback[A, B]: C[Pair[A, B]] { fn c(x: Pair[A, B]): String { "fallback" } }
class Values[T] { fn values(x: T): List[String] }
derive instance values[T]: Values[T] {
 fn values(x: T): List[String] { [comptime for (f in shape.fields[T]()) c[f.Type](f.read(x))] }
}
type Outer[A] = { value: Pair[A, Int] } derive(Values)
fn main() { println(values(Outer { value: Pair { a: true, b: 7 } })) }`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "fallback") {
		t.Fatalf("candidate: %s", output)
	}
}

func TestDeriveTemplateHelpers(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
derive fn stored[T](field: shape.Field[T]): Bool { !field.computed }
derive fn fieldText[T](field: shape.Field[T], x: T): String {
 return toString(field.read(x))
}
class Values[T] { fn values(x: T): List[String] }
derive instance values[T]: Values[T] {
 fn values(x: T): List[String] {
  [comptime for (field in shape.fields[T]()) comptime if (stored[T](field)) fieldText[T](field, x)]
 }
}
type Item = { n: Int, lazy next: Int = n + 1 } derive(Values)
type Box[A] = { value: A } derive(Values)
fn main() { println(values(Item { n: 7 })); println(values(Box { value: true })) }`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `["7"]`) || !strings.Contains(string(output), `["true"]`) || strings.Contains(string(output), "8") {
		t.Fatalf("helpers: %s", output)
	}
}

func TestDeriveTemplateBoundedHelper(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
class Text[T] { fn text(x: T): String }
instance textInt: Text[Int] { fn text(x: Int): String { toString(x) } }
derive fn textField[A, F: Text](field: shape.Field[A], x: A): String { toString(field.read(x)) }
class Values[T] { fn values(x: T): List[String] }
derive instance values[T]: Values[T] {
 fn values(x: T): List[String] {
  [comptime for (field in shape.fields[T]()) textField[T, field.Type](field, x)]
 }
}
type Box[A] = { value: A } derive(Values)
fn main() { println(values(Box { value: 7 })) }`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `["7"]`) {
		t.Fatalf("helper: %s", output)
	}
}

func TestDeriveTemplateProjectedFacts(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred positive(n: Int) { n > 0 }
class Values[T] { fn values(x: T): List[String] }
derive instance values[T]: Values[T] {
 fn values(x: T): List[String] {
  [comptime for (field in shape.fields[T]()) { bad: field.Type = -1; toString(bad) }]
 }
}
type Item = { n: Int where positive } derive(Values)
fn main() {}`
	checkPreludeSource(t, source, "positive")
}

func TestDeriveTemplateHelperFactSpecializations(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred positive(n: Int) { n > 0 }
pred negative(n: Int) { n < 0 }
derive fn echo[A](value: A): A { value }
class Values[T] { fn values(x: T): List[String] }
derive instance values[T]: Values[T] {
 fn values(x: T): List[String] {
  [comptime for (field in shape.fields[T]()) toString(echo[field.Type](field.read(x)))]
 }
}
type Item = { above: Int where positive, below: Int where negative } derive(Values)
fn main() { println(values(Item { above: 1, below: -1 })) }`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `["1", "-1"]`) {
		t.Fatalf("facts: %s", output)
	}
}

func TestDeriveTemplateMetadataAliasFacts(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred positive(n: Int) { n > 0 }
type Positive = Int where positive
derive fn accepts(x: Positive): Bool { true }
class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] {
 fn c(x: T): String { comptime if (accepts(0)) { "bad" } else { "other" } }
}
type Item = {} derive(C)
fn main() {}`
	checkPreludeSource(t, source, "metadata annotations cannot introduce runtime facts")
}

func TestDeriveTemplateRuntimeBranches(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
class Values[T] { fn values(x: T, enabled: Bool): List[String] }
derive instance values[T]: Values[T] {
 fn values(x: T, enabled: Bool): List[String] {
  if (enabled) {
   fields = shape.fields[T]()
   [comptime for (f in fields) { value: f.Type = f.read(x); toString(value) }]
  } else { [] }
 }
}
type Item = { n: Int, text: String } derive(Values)
fn main() {
 println(values(Item { n: 3, text: "yes" }, true))
 println(values(Item { n: 3, text: "yes" }, false))
}`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `["3", "yes"]`) || !strings.Contains(string(output), `[]`) {
		t.Fatalf("runtime branches: %s", output)
	}
}

func TestDeriveTemplateHandwrittenInstanceBounds(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
class D[T] { fn d(x: T): String }
instance dInt: D[Int] { fn d(x: Int): String { toString(x) } }
class C[T] { fn c(x: T): String }
instance cList[A: D]: C[List[A]] { fn c(x: List[A]): String { "list" } }
derive instance c[T]: C[T] {
 fn c(x: T): String { toString([comptime for (f in shape.fields[T]()) c[f.Type](f.read(x))]) }
}
type Box[A] = { value: List[A] } derive(C)
fn main() { println(c(Box { value: [7] })) }`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "list") {
		t.Fatalf("handwritten bound: %s", output)
	}
}

func TestDeriveTemplateDefinitionNames(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] {
 fn c(x: T): String {
  comptime if (shape.kind[T]() == shape.Record) { "record" } else { missingFunction() }
 }
}
fn main() {}`, "undefined name in derive definition: missingFunction")
	checkPreludeSource(t, `derive fn unused[T](x: T): String { missingFunction() }
fn main() {}`, "undefined name in derive definition: missingFunction")
}

func TestDeriveTemplateSharedWorkBudget(t *testing.T) {
	// Branching argument evaluation must consume the same budget as helper
	// bodies. Copying a budget before evaluating arguments refunds this work.
	expression := "true"
	for range 16 {
		expression = "choose(" + expression + "," + expression + ")"
	}
	source := `derive fn choose(a: Bool, b: Bool): Bool { a }
class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] {
 fn c(x: T): String { comptime if (` + expression + `) { "yes" } else { "no" } }
}
type Item = {} derive(C)
fn main() {}`
	checkPreludeSource(t, source, "compile-time work limit")
}

func TestDeriveTemplatePrivateRepresentations(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ declaration, query, want string }{
		{"type Secret = private { n: Int }", "shape.fields[T]()", "cannot inspect private representation"},
		{"type Secret = sealed { hidden { n: Int }, Public }", "shape.variants[T]()", "cannot inspect private variants"},
	} {
		dir := t.TempDir()
		sources := map[string]string{
			"bork.mod":           "module example.com/derive\n",
			"foreign/types.bork": tc.declaration,
			"main.bork": `import "example.com/derive/foreign"
import "bork/shape"
class C[T] { fn c(x: T): List[String] }
derive instance c[T]: C[T] { fn c(x: T): List[String] { [comptime for (f in ` + tc.query + `) f.name] } }
derive C for foreign.Secret
fn main() {}`,
		}
		for path, source := range sources {
			path = filepath.Join(dir, path)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(source), 0644); err != nil {
				t.Fatal(err)
			}
		}
		_, _, err := Check(dir)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("want %q; got %v", tc.want, err)
		}
	}
}

func TestDeriveTemplateFieldOwner(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
class C[T] { fn c(x: T): List[String] }
derive instance c[T]: C[T] {
 fn c(x: T): List[String] { [comptime for (f in shape.fields[T]()) toString(f.read(Other { n: 1 }))] }
}
type Other = { n: Int }
type Item = { n: Int } derive(C)
fn main() {}`, "field.read requires its proven owner Item, found Other")
	checkPreludeSource(t, `derive fn same[A](x: A): A { x }
fn same(x: Int): Int { x }
fn main() {}`, "derive helper same conflicts with an existing name")
}

func TestDeriveTemplateDeclaredDefaults(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
class Defaults[T] { fn defaults(x: T): List[String] }
derive instance defaults[T]: Defaults[T] {
 fn defaults(x: T): List[String] {
  [comptime for (f in shape.fields[T]()) comptime if (f.hasDefault && !f.computed) f.name]
 }
}
type Item = { n: Int = 2, required: Bool, lazy doubled: Int = n + n } derive(Defaults)
fn main() { println(defaults(Item { required: true })) }`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `["n"]`) {
		t.Fatalf("defaults: %s", output)
	}
}

func TestDeriveTemplateNominalHelperSpecializations(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sources := map[string]string{
		"bork.mod":         "module example.com/derive\n",
		"left/types.bork":  "type Same = { value: Int }",
		"right/types.bork": "type Same = { value: String }",
		"main.bork": `import "bork/shape"
import "example.com/derive/left"
import "example.com/derive/right"
derive fn echo[A](value: A): A { value }
class Values[T] { fn values(x: T): List[String] }
derive instance values[T]: Values[T] {
 fn values(x: T): List[String] {
  [comptime for (f in shape.fields[T]()) toString(echo[f.Type](f.read(x)))]
 }
}
type Item = { a: left.Same, b: right.Same } derive(Values)
fn main() { println(values(Item { a: left.Same { value: 2 }, b: right.Same { value: "other" } })) }`,
	}
	for path, source := range sources {
		path = filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "other") || !strings.Contains(string(output), "2") {
		t.Fatalf("nominal helper types: %s", output)
	}
}

func TestDeriveTemplateMetadataEqualityTypes(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] { fn c(x: T): String { comptime if (1 == "1") { "yes" } else { "no" } } }
type Item = {} derive(C)
fn main() {}`, "metadata equality requires operands of the same type")
}

func TestDeriveTemplateMetadataMatchTypes(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] {
 fn c(x: T): String { comptime match (true) { true => "yes", "wrong" => "bad", _ => "no" } }
}
type Item = {} derive(C)
fn main() {}`, "metadata equality requires operands of the same type")
}

func TestDeriveTemplateDefinitionTypes(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `derive fn unused[A](x: Missing): A { x }
fn main() {}`, "undefined type in derive definition: Missing")
	checkPreludeSource(t, `derive fn unused[A: Missing](x: A): A { x }
fn main() {}`, "unknown class Missing")
	checkPreludeSource(t, `derive fn unused[A](x: A, x: A): A { x }
fn main() {}`, "derive parameter x is declared twice")
}

func TestDeriveTemplateSourceNavigation(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
derive fn echo[A](value: A): A { value }
class C[T] { fn c(x: T): List[String] }
derive instance c[T]: C[T] {
 fn c(x: T): List[String] {
  fields = shape.fields[T]()
  [comptime for (f in fields) toString(echo[f.Type](f.read(x)))]
 }
}
type Item = { n: Int } derive(C)
fn main() { println(c(Item { n: 1 })) }`
	dir := validatorFixture(t, source)
	analysis, err := NewSession().Analyze(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "main.bork")
	lines := strings.Split(source, "\n")
	for _, tc := range []struct {
		line           int
		text           string
		definitionLine int
	}{
		{4, "C[T]", 3},
		{7, "echo[", 2},
		{7, "fields)", 6},
		{7, "f.Type", 7},
	} {
		col := strings.Index(lines[tc.line-1], tc.text) + 1
		ref := analysis.ReferenceAt(diag.Pos{File: path, Line: tc.line, Col: col})
		if col == 0 || ref == nil || ref.Definition.Line != tc.definitionLine {
			t.Fatalf("%s: reference %+v", tc.text, ref)
		}
	}
	foundHelper := false
	for _, symbol := range analysis.Symbols() {
		if symbol.Name == "echo" && symbol.Kind == "function" && symbol.Definition.Line == 2 {
			foundHelper = true
		}
		if strings.HasPrefix(symbol.Name, "_derive_") {
			t.Fatalf("generated symbol leaked: %+v", symbol)
		}
	}
	if !foundHelper {
		t.Fatal("helper declaration missing")
	}
	for _, site := range []struct {
		line   int
		helper bool
	}{{7, true}, {11, false}} {
		found := false
		for _, item := range analysis.EditorSymbols(diag.Pos{File: path, Line: site.line, Col: 20}) {
			if strings.HasPrefix(item.Name, "_derive_") {
				t.Fatalf("generated completion leaked: %+v", item)
			}
			if item.Name == "echo" {
				found = true
			}
		}
		if found != site.helper {
			t.Fatalf("helper completion at line %d: %t", site.line, found)
		}
	}
	callColumn := strings.Index(lines[6], "echo[f.Type](") + len("echo[f.Type](") + 1
	if help := analysis.SignatureHelp(path, source, diag.Pos{File: path, Line: 7, Col: callColumn}); help == nil || help.Name != "echo" || help.Callable == nil || len(help.Callable.Parameters) != 1 {
		t.Fatalf("helper signature help: %+v", help)
	}
	helperColumn := strings.Index(lines[6], "echo[") + 1
	foundToken := false
	for _, token := range analysis.SemanticTokens(path) {
		if token.Start.Line == 7 && token.Start.Col == helperColumn && token.Kind == "function" {
			foundToken = true
		}
	}
	if !foundToken {
		t.Fatal("helper call missing semantic function classification")
	}
}

func TestDeriveTemplateSequenceQueries(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
derive fn count[T](fields: List[shape.Field[T]]): Int { fields.length() }
class Count[T] { fn countFields(x: T): Int }
derive instance count[T]: Count[T] {
 fn countFields(x: T): Int {
  fields = shape.fields[T]()
  comptime if (fields.isEmpty()) { 0 } else { count[T](fields) }
 }
}
type Item = { n: Int, name: String } derive(Count)
type Empty = {} derive(Count)
fn main() { println(countFields(Item { n: 1, name: "one" })); println(countFields(Empty {})) }`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != "2\n0" {
		t.Fatalf("sequence queries: %s", output)
	}
}

func TestDeriveTemplateMetadataMatchLiterals(t *testing.T) {
	t.Parallel()
	for _, pattern := range []string{"9223372036854775808", "1.5", "'x'"} {
		t.Run(pattern, func(t *testing.T) {
			checkPreludeSource(t, `class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] {
 fn c(x: T): String { comptime match (0) { 0 => "yes", `+pattern+` => "bad", _ => "no" } }
}
type Item = {} derive(C)
fn main() {}`, "comptime match literal must be a supported compile-time value")
		})
	}
}

func TestDeriveTemplateMetadataMatchHelper(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
derive fn hasFields[T](fields: List[shape.Field[T]]): Bool {
 comptime match (fields.isEmpty()) { true => false, false => true }
}
class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] {
 fn c(x: T): String { comptime if (hasFields[T](shape.fields[T]())) { "fields" } else { "empty" } }
}
type Item = { n: Int } derive(C)
type Empty = {} derive(C)
fn main() { println(c(Item { n: 1 })); println(c(Empty {})) }`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != "fields\nempty" {
		t.Fatalf("metadata match helper: %s", output)
	}
}

func TestDeriveTemplateFail(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] {
 fn c(x: T): String {
  comptime match (shape.kind[T]()) {
   shape.Record => "record",
   _ => shape.fail(shape.name[T]() + ": only records are supported")
  }
 }
}
type Choice = sealed { First, Second } derive(C)
fn main() {}`, "Choice: only records are supported (template operation at")
	checkPreludeSource(t, `import "bork/shape"
class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] { fn c(x: T): String { shape.fail(1) } }
type Item = {} derive(C)
fn main() {}`, "shape.fail message must be a compile-time String")
	checkPreludeSource(t, `import "bork/shape"
fn main() { shape.fail("no") }`, "shape operations are available only during derive template expansion")
}

func TestDeriveTemplateStringWorkBudget(t *testing.T) {
	checkPreludeSource(t, `class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] {
 fn c(x: T): String {
  a = "0123456789"
  b = a + a
  part3 = b + b
  d = part3 + part3
  e = d + d
  f = e + e
  g = f + f
  h = g + g
  i = h + h
  j = i + i
  k = j + j
  l = k + k
  m = l + l
  n = m + m
  n
 }
}
type Item = {} derive(C)
fn main() {}`, "derive template expansion exceeds its compile-time work limit")
}

func TestDeriveTemplateChangingSpecializationDepth(t *testing.T) {
	checkPreludeSource(t, `derive fn grow[T](x: T): String { grow[List[T]]([x]) }
class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] { fn c(x: T): String { grow[T](x) } }
type Item = {} derive(C)
fn main() {}`, "derive template expansion exceeds its compile-time depth limit")
}

func TestDeriveTemplateFailInSignature(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] { fn c(x: T = shape.fail("invalid default")): String { "record" } }
fn main() {}`, "template parameters cannot have defaults or scope annotations")
}

func TestDeriveTemplateMetadataBlockScopes(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `derive fn bad(): String {
  result = comptime match (true) { true => { hidden = "x"; hidden }, _ => "no" }
  hidden
 }
class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] { fn c(x: T): String { bad() } }
type Item = {} derive(C)
fn main() {}`, "undefined local in derive definition: hidden")
}

func TestDeriveTemplateHelperDocumentation(t *testing.T) {
	t.Parallel()
	dir := validatorFixture(t, `// Echo specializes ordinary runtime values inside a template.
derive fn Echo[T](x: T): T { x }
fn main() {}`)
	output, err := Doc(dir, DocOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "derive fn Echo[T](x: T): T") {
		t.Fatalf("missing helper API: %s", output)
	}
	if strings.Contains(string(output), "_derive_") {
		t.Fatalf("generated helper API leaked: %s", output)
	}
}

func TestDeriveTemplateUnrequestedScopes(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `derive fn unused(): String {
  result = comptime match (true) { true => { hidden = "x"; hidden }, _ => "no" }
  hidden
 }
fn main() {}`, "undefined local in derive definition: hidden")
	checkPreludeSource(t, `derive fn unused(): Int { x = x; x }
fn main() {}`, "undefined local in derive definition: x")
	checkPreludeSource(t, `derive fn unused(x: Int): Int { x = 1; x }
fn main() {}`, "x is already defined in an enclosing derive scope")
	checkPreludeSource(t, `derive fn unused(): String {
 comptime match (true) { true => { local = "yes"; local }, _ => { local = "no"; local } }
}
fn main() {}`, "")
}

func TestDeriveTemplateUnrequestedLiteralTypes(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `derive fn unused(): String { 1 }
fn main() {}`, "derive expression must be String, found Int")
	checkPreludeSource(t, `derive fn unused(): String { bad: Bool = "wrong"; "ok" }
fn main() {}`, "derive expression must be Bool, found String")
	checkPreludeSource(t, `class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] { fn c(x: T): String { comptime if (true) { "yes" } else { 1 } } }
fn main() {}`, "derive expression must be String, found Int")
	checkPreludeSource(t, `derive fn unused(): Int32 { 1 }
derive fn generic[T](x: T): T { x }
fn main() {}`, "")
}

func TestDeriveTemplateNoNativeEvaluation(t *testing.T) {
	t.Parallel()
	dir := validatorFixture(t, `import "bork/shape"
class Labels[T] { fn labels(x: T): List[String] }
derive instance labels[T]: Labels[T] {
 fn labels(x: T): List[String] { [comptime for (f in shape.fields[T]()) f.name] }
}
type Item = { name: String, count: Int = 1 } derive(Labels)
fn main() {}`)
	loaded, module, err := loadCompilationInputs(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := captureGoContext()
	launcher := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 83\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx.tool = launcher
	if _, err := checkLoadedProgramObserved(loaded, module, ctx, captureEmbedsSnapshot, nil); err != nil {
		t.Fatalf("derive checking must succeed without launching the native tool: %v", err)
	}
}

func TestDeriveTemplateNamedHelperArguments(t *testing.T) {
	t.Parallel()
	dir := validatorFixture(t, `derive fn choose(number: Int, text: String): String { text }
class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] { fn c(x: T): String { choose(text: "named", number: 1) } }
type Item = {} derive(C)
fn main() { println(c(Item {})) }`)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != "named" {
		t.Fatalf("named metadata helper: %s", output)
	}
}

func TestDeriveTemplateNamedHelperEvaluationOrder(t *testing.T) {
	t.Parallel()
	dir := validatorFixture(t, `derive fn concat(first: String, second: String): String { first + second }
fn next(text: String) uses io: String { println(text); text }
class C[T] { fn c(x: T) uses io: String }
derive instance c[T]: C[T] {
 fn c(x: T) uses io: String { concat(second: next("second"), first: next("first")) }
}
type Item = {} derive(C)
fn main() { println(c(Item {})) }`)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != "second\nfirst\nfirstsecond" {
		t.Fatalf("named runtime helper order: %s", output)
	}
}

func TestDeriveTemplateUnrequestedCallShapes(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `fn ordinary(x: Int): Int { x }
derive fn unused(): Int { ordinary() }
fn main() {}`, "derive definition call to ordinary takes 1 to 1 arguments, found 0")
	checkPreludeSource(t, `derive fn helper[T](x: T): T { x }
derive fn unused(): Int { helper(1) }
fn main() {}`, "derive definition call to helper requires 1 type arguments")
	checkPreludeSource(t, `derive fn helper(x: Int): Int { x }
derive fn unused(): Int { helper(wrong: 1) }
fn main() {}`, "helper has no parameter named wrong")
	checkPreludeSource(t, `fn ordinary(x: Int = 1): Int { x }
derive fn unused(): Int { ordinary() }
fn main() {}`, "")
}

func TestDeriveTemplateUnrequestedIntrinsicArguments(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
derive fn unused(): String { shape.fail(wrong: "bad") }
fn main() {}`, "shape.fail has no parameter named wrong")
	checkPreludeSource(t, `import "bork/shape"
derive fn unused(): String { shape.name() }
fn main() {}`, "derive definition call to shape.name requires 1 type arguments")
}

func TestDeriveTemplateLocalCallableScope(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `derive fn run(count: (Int) => Int): Int { count(1) }
fn main() {}`, "")
}

func TestDeriveTemplateNamedConstructorDefaults(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `type Item = { x: Int = 1, y: Int }
fn New = Item.new
derive fn build(): Item { New(y: 2) }
fn main() {}`, "")
}

func TestDeriveTemplateSymbolicTypePatternScope(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `derive fn inspect[T](x: T): String { match (x) { T => "same" } }
fn main() {}`, "")
}

func TestDeriveTemplateSymbolicTypePatternExpansion(t *testing.T) {
	t.Parallel()
	dir := validatorFixture(t, `derive fn inspect[T](x: T): String { match (x) { T => "same" } }
class C[T] { fn c(x: T): String }
derive instance c[T]: C[T] { fn c(x: T): String { inspect[T](x) } }
type Item = {} derive(C)
fn main() { println(c(Item {})) }`)
	analysis, err := NewSession().Analyze(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range analysis.Symbols() {
		if strings.HasPrefix(symbol.Name, "_derive_") {
			t.Fatalf("generated pattern binding leaked: %+v", symbol)
		}
	}
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != "same" {
		t.Fatalf("symbolic type pattern: %s", output)
	}
}

func TestDeriveTemplateMetadataHelperInSignature(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `pred above(x: Int, minimum: Int) { x > minimum }
derive fn minimum(): Int { 0 }
class C[T] { fn c(x: T, n: Int): String }
derive instance c[T]: C[T] { fn c(x: T, n: Int where above(minimum())): String { "ok" } }
fn main() {}`, "where clauses on the methods of classes and instances are not supported yet")
}

func TestDeriveTemplateCannotStrengthenClassParameters(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `pred positive(x: Int) { x > 0 }
fn needsPositive(n: Int where positive): String { "ok" }
class C[T] { fn c(x: T, n: Int): String }
derive instance c[T]: C[T] {
 fn c(x: T, n: Int where positive): String { needsPositive(n) }
}
type Item = {} derive(C)
fn main() { println(c(Item {}, -1)) }`, "where clauses on the methods of classes and instances are not supported yet")
}
