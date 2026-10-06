package driver

import (
	"fmt"
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

func TestDeriveTemplateFactDescriptors(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
class Facts[T] {
 fn fields(x: T): List[List[String]]
 fn independent(x: T): List[List[String]]
 fn invariants(x: T): List[String]
}
derive fn description[T](fact: shape.Fact[T]): String { fact.text + fact.path }
derive instance facts[T]: Facts[T] {
 fn fields(x: T): List[List[String]] {
  [comptime for (field in shape.fields[T]())
    [comptime for (fact in field.facts) description[T](fact)]]
 }
 fn independent(x: T): List[List[String]] {
  [comptime for (field in shape.fields[T]())
    [comptime for (fact in field.facts) comptime if (fact.independent) fact.text]]
 }
 fn invariants(x: T): List[String] {
  [comptime for (fact in shape.facts[T]()) fact.text]
 }
}
pred positive(n: Int) { n > 0 }
pred atLeast(n: Int, lo: Int) { n >= lo }
pred ordered(r: Row) { r.hi >= r.lo }
type Row = {
 lo: Int where positive
 hi: Int where atLeast(lo)
 values: List[Int where positive]
 lazy next: Int where positive = lo + 1
} where ordered derive(Facts)
fn main() {
 row = Row { lo: 1, hi: 2, values: [3] }
 println(fields(row))
 println(independent(row))
 println(invariants(row))
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
	for _, expected := range []string{
		`[["positive"], ["atLeast(lo)"], ["positive.[]"], ["positive"]]`,
		`[["positive"], [], ["positive"], []]`,
		`["ordered"]`,
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("want %s in: %s", expected, output)
		}
	}
}

func TestDeriveTemplateVariantFacts(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
class Facts[T] { fn facts(x: T): List[List[String]] }
derive instance facts[T]: Facts[T] {
 fn facts(x: T): List[List[String]] {
  [comptime for (variant in shape.variants[T]()) [comptime for (fact in variant.facts) fact.text]]
 }
}
pred valid(x: Choice) { true }
type Choice = sealed { Full { n: Int } where valid, Empty } derive(Facts)
fn main() { println(facts(Choice.Full { n: 1 })) }`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `[["valid"], []]`) {
		t.Fatalf("variant facts: %s", output)
	}
}

func TestDeriveTemplateFactHandleDoesNotEscape(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
class Facts[T] { fn facts(x: T): List[shape.Fact[T]] }
derive instance facts[T]: Facts[T] {
 fn facts(x: T): List[shape.Fact[T]] { shape.facts[T]() }
}
pred valid(x: Row) { true }
type Row = {} where valid derive(Facts)
fn main() {}`, "cannot escape")
}

func TestDeriveTemplateConstrainedHeadFacts(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
class Facts[T] { fn facts(x: T): List[String] }
derive instance facts[T]: Facts[T] {
 fn facts(x: T): List[String] { [comptime for (fact in shape.facts[T]()) fact.text] }
}
pred original(x: Row) { true }
pred additional(x: Row) { true }
type Row = {} where original
type Checked = Row where additional
derive Facts for Checked
fn main() { row: Checked = Row {}; println(facts(row)) }`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `["original", "additional"]`) {
		t.Fatalf("constrained head facts: %s", output)
	}
}

func TestDeriveTemplateFactPrivacy(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
fn main() { println(shape.facts[Int]()) }`
	checkPreludeSource(t, source, "only during derive template expansion")
}

func TestDeriveTemplateDefaults(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
class Defaults[T] { fn defaults(x: T): List[String] }
derive instance defaults[T]: Defaults[T] {
 fn defaults(x: T): List[String] {
  [comptime for (field in shape.fields[T]())
    comptime if (!field.computed && field.hasDefault)
      toString(field.default())]
 }
}
pred positive(n: Int) { n > 0 }
type Row = {
 n: Int where positive = 2
 text: String = "hello"
 noDefault: Bool
 lazy next: Int = n + 1
} derive(Defaults)
type Box[A] = { values: List[A] = [] } derive(Defaults)
fn main() {
 println(defaults(Row { noDefault: false }))
 println(defaults(Box[Int] {}))
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
	if !strings.Contains(string(output), `["2", "hello"]`) || !strings.Contains(string(output), `["[]"]`) {
		t.Fatalf("defaults: %s", output)
	}
}

func TestDeriveTemplateDefaultChecks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, fields, operation, want string }{
		{"missing", "n: Int", "field.default()", "has no declared default"},
		{"computed", "n: Int, lazy next: Int = n + 1", "field.default()", "requires a complete owner value"},
		{"arguments", "n: Int = 1", "field.default(2)", "takes no arguments"},
		{"type arguments", "n: Int = 1", "field.default[Int]()", "takes no arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkPreludeSource(t, `import "bork/shape"
class Defaults[T] { fn defaults(x: T): List[String] }
derive instance defaults[T]: Defaults[T] {
 fn defaults(x: T): List[String] {
  [comptime for (field in shape.fields[T]()) comptime if (field.name != "n" || shape.fields[T]().length() == 1) toString[field.Type](`+tc.operation+`)]
 }
}
type Row = { `+tc.fields+` } derive(Defaults)
fn main() {}`, tc.want)
		})
	}
}

func TestDeriveTemplateDefaultRetainsFacts(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred positive(n: Int) { n > 0 }
fn required(n: Int where positive): Int { n }
class Defaults[T] { fn defaults(x: T): List[Int] }
derive instance defaults[T]: Defaults[T] {
 fn defaults(x: T): List[Int] {
  [comptime for (field in shape.fields[T]()) required(field.default())]
 }
}
type Row = { n: Int where positive = 2 } derive(Defaults)
fn main() { println(defaults(Row {})) }`
	dir := validatorFixture(t, source)
	executable, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `[2]`) {
		t.Fatalf("default facts: %s", output)
	}
}

func TestDeriveTemplateDefaultLexicalOwner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sources := map[string]string{
		"bork.mod": "module example.com/derive\n",
		"foreign/types.bork": `type Mode = sealed { First, Second }
type Row = { mode: Mode = Mode.Second }`,
		"main.bork": `import "bork/shape"
import "example.com/derive/foreign"
type Mode = sealed { First }
class Defaults[T] { fn defaults(x: T): List[String] }
derive instance defaults[T]: Defaults[T] {
 fn defaults(x: T): List[String] {
  [comptime for (field in shape.fields[T]()) toString(field.default())]
 }
}
derive Defaults for foreign.Row
fn main() { println(defaults(foreign.Row {})) }`,
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
	if !strings.Contains(string(output), "Second") {
		t.Fatalf("lexical default: %s", output)
	}
}

func TestDeriveTemplateDefaultOutputBudget(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
class Defaults[T] { fn defaults(x: T): List[String] }
derive instance defaults[T]: Defaults[T] {
 fn defaults(x: T): List[String] { [comptime for (field in shape.fields[T]()) field.default()] }
}
type Row = { value: String = "`+strings.Repeat("x", 100001)+`" } derive(Defaults)
fn main() {}`, "compile-time work limit")
}

func TestDeriveTemplateImplicitDefaultOutputBudget(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
class Defaults[T] { fn defaults(x: T): List[String] }
derive instance defaults[T]: Defaults[T] {
 fn defaults(x: T): List[String] { [comptime for (field in shape.fields[T]()) toString(field.default())] }
}
type Huge = { payload: String = "`+strings.Repeat("x", 100001)+`" }
type Row = { child: Huge = Huge {} } derive(Defaults)
fn main() {}`, "compile-time work limit")
}

func TestDeriveTemplateUnrequestedLiteralCallTypes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, declaration, call, want string }{
		{"positional", "fn accept(n: Int): Int { n }", `accept("wrong")`, "argument n must be Int"},
		{"named", "fn accept(n: Int, text: String): Int { n }", `accept(text: "ok", n: false)`, "argument n must be Int"},
		{"generic independent parameter", "fn accept[A](x: A, n: Int): Int { n }", `accept[T](x, "wrong")`, "argument n must be Int"},
		{"container", "fn accept(xs: List[Int]): Int { xs.length() }", `accept(["wrong"])`, "must be Int"},
		{"derive helper", "derive fn accept(n: Int): Int { n }", `accept(false)`, "argument n must be Int"},
		{"generic derive helper", "derive fn accept[A](x: A, n: Int): Int { n }", `accept[T](x, "wrong")`, "argument n must be Int"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkPreludeSource(t, tc.declaration+`
class C[T] { fn c(x: T): Int }
derive instance c[T]: C[T] { fn c(x: T): Int { `+tc.call+` } }
fn main() {}`, tc.want)
		})
	}
}

func TestDeriveTemplateRuntimeLoopDiagnostic(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `class C[T] { fn c(x: T): Int }
derive instance c[T]: C[T] { fn c(x: T): Int { for (n in 2) {}; 1 } }
type Row = {} derive(C)
fn main() {}`, "for requires a List or Seq")
}

func TestDeriveTemplateTupleScopes(t *testing.T) {
	t.Parallel()
	source := `class Label[T]{fn label(x:T):String}
derive fn pair[T](x:T):(T,String){ (x,"tuple") }
derive fn take[T](p:(T,String)):String{ p.1 }
derive instance labels[T]:Label[T]{
 fn label(x:T):String{
  (_, text) = pair[T](x)
  selected = take[T]((x,text))
  (_, (nested,)) = (x, (selected,))
  match ((nested,)) { (word,) => word }
 }
}
type Item={} derive(Label)
fn main(){println(label(Item{}))}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil || string(output) != "tuple\n" {
		t.Fatalf("tuple scope: %s, %v", output, err)
	}
	checkPreludeSource(t, `derive fn bad[T](x:T):String{ (text,) = (text,); text }
fn main(){}`, "undefined local in derive definition: text")
	checkPreludeSource(t, `derive fn bad[T](x:T):String{ match ((x,)) { (word,) => "ok" }; word }
fn main(){}`, "undefined local in derive definition: word")
	checkPreludeSource(t, `derive fn bad[T](x:T):(Int,String){ (1,2) }
fn main(){}`, "found (Int, Int)")
}

func TestDeriveTemplateIsPattern(t *testing.T) {
	t.Parallel()
	source := `class Label[T]{fn label(x:T):String}
derive instance labels[T]:Label[T]{
 fn label(x:T):String{ if (x is T) { "yes" } else { "no" } }
}
type Item={} derive(Label)
fn main(){println(label(Item{}))}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil || string(output) != "yes\n" {
		t.Fatalf("is pattern: %s, %v", output, err)
	}
	checkPreludeSource(t, `class Label[T]{fn label(x:T):String}
derive instance labels[T]:Label[T]{fn label(x:T):String{"x"}}
type Pair=(Int,String) derive(Label)
fn main(){}`, "tuple aliases cannot derive custom classes")
}

func TestDeriveTemplateSealedViews(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
derive fn text[A,F](field:shape.Field[A],payload:F):String { toString(field.read(payload)) }
class Render[T]{fn render(x:T):String}
derive instance render[T]:Render[T]{
 fn render(x:T):String{
  comptime for (variant in shape.variants[T]()) {
   rendered = variant.project(x).map((payload:variant.Type) => {
    fields:List[String] = [comptime for (field in variant.fields) text[T,variant.Type](field,payload)]
    variant.name + "(" + fields.join(",") + ")"
   })
   if (rendered.isSome()) { return rendered.getOr("") }
  }
  "unreachable"
 }
}
type Choice[A]=sealed{Empty, One{item:A}, Pair{number:Int,text:String}} derive(Render)
fn main(){println(render(Choice[Int].Empty));println(render(Choice.One{item:"hello"}));println(render(Choice[Int].Pair{number:3,text:"world"}))}`
	for _, annotated := range []bool{false, true} {
		t.Run(fmt.Sprint(annotated), func(t *testing.T) {
			program := source
			if !annotated {
				program = strings.Replace(program, "(payload:variant.Type)", "payload", 1)
			}
			executable, err := buildFixtureOutput(t, validatorFixture(t, program))
			if err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(executable).CombinedOutput()
			if err != nil || string(output) != "Empty()\nOne(hello)\nPair(3,world)\n" {
				t.Fatalf("sealed view: %s, %v", output, err)
			}
		})
	}

}

func TestDeriveTemplateSealedViewBoundaries(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
class C[T]{fn c(x:T):String}
derive instance c[T]:C[T]{
 fn c(x:T):String{
  comptime for (v in shape.variants[T]()) {
   comptime for (f in v.fields) { toString(f.read(x)) }
  }
  "x"
 }
}
type Choice=sealed{One{item:Int}} derive(C)
fn main(){}`, "field.read requires the proven payload view")
	checkPreludeSource(t, `import "bork/shape"
class C[T]{fn c(x:T):String}
derive instance c[T]:C[T]{
 fn c(x:T):String{
  comptime for (v in shape.variants[T]()) { v.project(1) }
  "x"
 }
}
type Choice=sealed{One{item:Int}} derive(C)
fn main(){}`, "variant.project requires its proven sealed owner Choice, found Int")
	checkPreludeSource(t, `import "bork/shape"
class C[T]{fn c(x:T):String}
derive instance c[T]:C[T]{
 fn c(x:T):String{
  comptime for (v in shape.variants[T]()) {
   v.project(x).map(payload => {
    comptime for (other in shape.variants[T]()) {
     comptime for (f in other.fields) { toString(f.read(payload)) }
    }
    "x"
   })
  }
  "x"
 }
}
type Choice=sealed{One{item:Int},Two{item:Int}} derive(C)
fn main(){}`, "field.read requires the proven payload view")
}

func TestDeriveTemplateSealedViewReadOnly(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
class C[T]{fn c(x:T):String}
derive instance c[T]:C[T]{
 fn c(x:T):String{
  comptime for (v in shape.variants[T]()) {
   v.project(x).map(payload => { payload.copy(value:x); "x" })
  }
  "x"
 }
}
type Choice=sealed{One{item:Int}} derive(C)
fn main(){}`, "cannot copy")
}

func TestDeriveTemplateSealedViewFacts(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred positive(n:Int){n>0}
fn required(n:Int where positive):String{toString(n)}
fn traced[T](x:T) uses io:T {println("project");x}
class Render[T]{fn render(x:T) uses io:String}
derive instance render[T]:Render[T]{
 fn render(x:T) uses io:String{
  comptime for (v in shape.variants[T]()) {
   rendered = v.project(traced[T](x)).map(payload => {
    fields:List[String]=[comptime for (f in v.fields) required(f.read(payload))]
    fields.join(",")
   })
   if (rendered.isSome()) {return rendered.getOr("")}
  }
  "missing"
 }
}
type Choice=sealed{One{item:Int where positive, lazy next:Int where positive=item+1}} derive(Render)
fn main(){println(render(Choice.One{item:2}))}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil || string(output) != "project\n2,3\n" {
		t.Fatalf("payload facts: %s, %v", output, err)
	}
}

func TestDeriveTemplateBuilder(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred positive(n:Int){n>0}
class Build[T]{fn build(n:Int):T|shape.ValidationError}
derive instance build[T]:Build[T]{
 fn build(n:Int):T|shape.ValidationError{
  initial = shape.builder[T]()
  steps:List[(initial.Type)=>initial.Type] = [comptime for (f in shape.fields[T]()) comptime if (!f.computed && !f.hasDefault)
   (state:initial.Type)=>state.set(f,n)]
  steps.fold(initial,(state,step)=>step(state)).finish()
 }
}
type Row={n:Int where positive, extra:Int=4, lazy next:Int where positive=n+1} derive(Build)
fn main(){result=build[Row](2);println(result);println(match(result){row:Row=>row.next, _:shape.ValidationError=>-1});println(build[Row](-1))}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("builder: %s, %v", output, err)
	}
	for _, expected := range []string{"Row { n: 2, extra: 4 }", "\n3\n", "path: \".n\"", "positive"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("want %q in %s", expected, output)
		}
	}
}

func TestDeriveTemplateBuilderLazyDependencies(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
pred positive(n:Int){n>0}
class Build[T]{fn build(n:Int):T|shape.ValidationError}
derive instance build[T]:Build[T]{
 fn build(n:Int):T|shape.ValidationError{
  initial=shape.builder[T]()
  steps:List[(initial.Type)=>initial.Type]=[comptime for(f in shape.fields[T]()) comptime if(!f.computed)
   (state:initial.Type)=>state.set(f,n)]
  steps.fold(initial,(state,step)=>step(state)).finish()
 }
}
type Row={n:Int, lazy extra:Int where positive=n+Value} derive(Build)
lazy Value:Int=match(build[Row](2)){row:Row=>row.n,_:shape.ValidationError=>0}
fn main(){println(Value)}`, "cycle")
}

func TestDeriveTemplateBuilderStates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body, want string }{
		{"missing", `initial=shape.builder[T]();initial.finish()`, `message: "is missing"`},
		{"duplicate", `initial=shape.builder[T]();steps:List[(initial.Type)=>initial.Type]=[comptime for(f in shape.fields[T]()) (state:initial.Type)=>state.set(f,n).set(f,n+1)];steps.fold(initial,(state,step)=>step(state)).finish()`, "supplied more than once"},
		{"fork", `initial=shape.builder[T]();steps:List[(initial.Type)=>initial.Type]=[comptime for(f in shape.fields[T]()) (state:initial.Type)=>state.set(f,n)];_=steps.fold(initial,(state,step)=>step(state)).finish();initial.finish()`, `message: "is missing"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := fmt.Sprintf(`import "bork/shape"
class Build[T]{fn build(n:Int):T|shape.ValidationError}
derive instance build[T]:Build[T]{fn build(n:Int):T|shape.ValidationError{%s}}
type Row={n:Int} derive(Build)
fn main(){println(build[Row](2))}`, tc.body)
			executable, err := buildFixtureOutput(t, validatorFixture(t, source))
			if err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(executable).CombinedOutput()
			if err != nil || !strings.Contains(string(output), tc.want) {
				t.Fatalf("want %q in %s: %v", tc.want, output, err)
			}
		})
	}
}

func TestDeriveTemplateBuilderSealed(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred positive(n:Int){n>0}
class Build[T]{fn build(n:Int):T|shape.ValidationError}
derive instance build[T]:Build[T]{
 fn build(n:Int):T|shape.ValidationError{
  results:List[T|shape.ValidationError]=[comptime for(v in shape.variants[T]()) comptime if(v.name=="One") {
   initial=v.builder()
   steps:List[(initial.Type)=>initial.Type]=[comptime for(f in v.fields) (state:initial.Type)=>state.set(f,n)]
   steps.fold(initial,(state,step)=>step(state)).finish()
  }]
  results.get(0).getOr(shape.ValidationError{path:"",message:"requires One"})
 }
}
type Choice[A]=sealed{One{item:Int where positive},Empty} derive(Build)
fn main(){println(build[Choice[String]](2));println(build[Choice[Int]](-1))}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	if !strings.Contains(string(output), "item: 2") || !strings.Contains(string(output), `path: ".item"`) {
		t.Fatal(string(output))
	}
}

func TestDeriveTemplateBuilderNestedFacts(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred positive(n:Int){n>0}
pred atLeast(n:Int,lo:Int){n>=lo}
pred ordered(row:Row){row.hi>=row.lo && row.values.length()>0}
class Build[T]{fn build(n:Int):T|shape.ValidationError}
derive instance build[T]:Build[T]{
 fn build(n:Int):T|shape.ValidationError{
  initial=shape.builder[T]()
  steps:List[(initial.Type)=>initial.Type]=[comptime for(f in shape.fields[T]()) {
   comptime if(f.name=="lo") {(state:initial.Type)=>state.set(f,2)} else {comptime if(f.name=="hi") {(state:initial.Type)=>state.set(f,3)} else {(state:initial.Type)=>state.set(f,[n])}}
  }]
  steps.fold(initial,(state,step)=>step(state)).finish()
 }
}
type Row={lo:Int where positive,hi:Int where atLeast(lo),values:List[Int where positive]} where ordered derive(Build)
fn main(){println(build[Row](2));println(build[Row](-1))}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	if !strings.Contains(string(output), "values: [2]") || !strings.Contains(string(output), `path: ".values[0]"`) {
		t.Fatal(string(output))
	}
}

func TestDeriveTemplateBuilderBoundaries(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
class Build[T]{fn build():String}
derive instance build[T]:Build[T]{fn build():String{
 initial=shape.builder[T]()
 comptime for(f in shape.fields[T]()){_=initial.set(f,"wrong")}
 "ok"
}}
type Row={n:Int} derive(Build)
fn main(){}`, "builder.set field n must be Int, found String")
	checkPreludeSource(t, `import "bork/shape"
class Build[T]{fn build():String}
derive instance build[T]:Build[T]{fn build():String{
 initial=shape.builder[T]()
 comptime for(f in shape.fields[Other]()){_=initial.set(f,1)}
 "ok"
}}
type Other={n:Int}
type Row={n:Int} derive(Build)
fn main(){}`, "builder.set requires a field from its exact owner")
	checkPreludeSource(t, `import "bork/shape"
class Build[T]{fn build():String}
derive instance build[T]:Build[T]{fn build():String{
 initial=shape.builder[T]()
 comptime for(f in shape.fields[T]()){_=initial.set(f,1)}
 "ok"
}}
type Row={n:Int,lazy next:Int=n+1} derive(Build)
fn main(){}`, "builder.set cannot supply computed field next")
	checkPreludeSource(t, `import "bork/shape"
class Build[T]{fn build():String}
derive instance build[T]:Build[T]{fn build():String{
 initial=shape.builder[T]()
 _=initial.copy(duplicate:"")
 "ok"
}}
type Row={n:Int} derive(Build)
fn main(){}`, "cannot copy")
}

func TestDeriveTemplateBuilderProvenance(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred positive(n:Int){n>0}
class Build[T]{fn build(n:Int):T|shape.ValidationError}
derive fn supply[A,B](state:B,field:shape.Field[A],value:Int):B {state.set(field,value)}
derive instance build[T]:Build[T]{fn build(n:Int):T|shape.ValidationError{
 initial=shape.builder[T]()
 steps:List[(initial.Type)=>initial.Type]=[comptime for(f in shape.fields[T]()) (state:initial.Type)=>supply[T,initial.Type](state,f,n)]
 steps.fold(initial,(state,step)=>step(state)).finish()
}}
type Row={n:Int where positive} derive(Build)
fn main(){match(build[Row](-1)){
 error:shape.ValidationError=>match(error.obligation){
  .Some(fact)=>{println(fact.field);println(fact.index);println(fact.source.contains("main.bork:"));println(fact.owner.contains("Row"))}
  .None=>println("missing provenance")
 }
 _:Row=>println("unexpected success")
}}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil || string(output) != "n\n0\ntrue\ntrue\n" {
		t.Fatalf("provenance: %s, %v", output, err)
	}
}

func TestDeriveTemplateBuilderGenericPayload(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
class Clone[T]{fn clone(x:T):T|shape.ValidationError}
derive fn supply[A,B,V](state:B,field:shape.Field[A],value:V):B {state.set(field,value)}
derive instance clone[T]:Clone[T]{fn clone(x:T):T|shape.ValidationError{
 initial=shape.builder[T]()
 steps:List[(initial.Type)=>initial.Type]=[comptime for(f in shape.fields[T]()) (state:initial.Type)=>supply[T,initial.Type,f.Type](state,f,f.read(x))]
 steps.fold(initial,(state,step)=>step(state)).finish()
}}
type Box[A]={value:A} derive(Clone)
fn main(){println(clone(Box[Int]{value:2}));println(clone(Box[String]{value:"ok"}))}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil || !strings.Contains(string(output), "value: 2") || !strings.Contains(string(output), `value: "ok"`) {
		t.Fatalf("generic payload: %s, %v", output, err)
	}
}

func TestDeriveTemplateBuilderEvaluation(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
fn traced[A](label:String,value:A) uses io:A {println(label);value}
class Build[T]{fn build(n:Int) uses io:T|shape.ValidationError}
derive instance build[T]:Build[T]{fn build(n:Int) uses io:T|shape.ValidationError{
 initial=shape.builder[T]()
 steps:List[(initial.Type) uses io=>initial.Type]=[comptime for(f in shape.fields[T]()) (state:initial.Type)=>traced("state",state).set(f,traced("input",n))]
 steps.fold(initial,(state,step)=>step(state)).finish()
}}
type Row={n:Int} derive(Build)
fn main(){println(build[Row](2))}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil || string(output) != "state\ninput\nRow { n: 2 }\n" {
		t.Fatalf("evaluation: %s, %v", output, err)
	}
}

func TestDeriveTemplateBuilderOwnerFacts(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred valid(row:Row){row.n>0}
class Build[T]{fn build(n:Int):T|shape.ValidationError}
derive instance build[T]:Build[T]{fn build(n:Int):T|shape.ValidationError{
 initial=shape.builder[T]()
 steps:List[(initial.Type)=>initial.Type]=[comptime for(f in shape.fields[T]()) (state:initial.Type)=>state.set(f,n)]
 steps.fold(initial,(state,step)=>step(state)).finish()
}}
type Row={n:Int} where valid derive(Build)
fn main(){match(build[Row](-1)){
 error:shape.ValidationError=>{println(error.path);println(error.obligation.map(fact=>fact.field).getOr("missing"));println(error.obligation.map(fact=>fact.index).getOr(-1))}
 _:Row=>println("unexpected success")
}}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil || string(output) != ".n\n\n0\n" {
		t.Fatalf("owner facts: %s, %v", output, err)
	}
}

func TestDeriveTemplateBuilderGenericLazyDependencies(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
type Cell={}
instance render:Show[Cell]{fn show(cell:Cell):String{toString(Value)}}
pred valid[A](n:A){s"$n"!=""}
class Build[T]{fn build():T|shape.ValidationError}
derive instance build[T]:Build[T]{fn build():T|shape.ValidationError{shape.builder[T]().finish()}}
type Row[A]={item:A where valid} derive(Build)
lazy Value:Int={_=build[Row[Cell]]();1}
fn main(){println(Value)}`, "cycle")
}

func TestDeriveTemplateBuilderProvenanceReuse(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred positive(row:Row){row.n>0}
type Row={n:Int=-1} derive(Probe)
type First=Row where positive
type Second=Row where positive
class Probe[T]{fn probe():List[String]}
derive fn failure[A]():String {
 match(shape.builder[A]().finish()) {
  error:shape.ValidationError=>error.obligation.map(fact=>fact.source).getOr("missing")
  _=>"success"
 }
}
derive instance probe[T]:Probe[T]{fn probe():List[String]{[
 match(shape.builder[First]().finish()) {error:shape.ValidationError=>error.obligation.map(fact=>fact.source).getOr("missing"),_:Row=>"success"},
 match(shape.builder[Second]().finish()) {error:shape.ValidationError=>error.obligation.map(fact=>fact.source).getOr("missing"),_:Row=>"success"},
 failure[First](),failure[Second]()
]}}
fn main(){println(probe[Row]())}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil || strings.Count(string(output), "main.bork:4:") != 2 || strings.Count(string(output), "main.bork:5:") != 2 {
		t.Fatalf("source anchors: %s, %v", output, err)
	}
}

func TestDeriveTemplateBuilderHeadProof(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred positive(row:Row){row.n>0}
type Row={n:Int=2} derive(Probe)
type Positive=Row where positive
fn require(row:Positive):Int {row.n}
class Probe[T]{fn probe():Int}
derive instance probe[T]:Probe[T]{fn probe():Int{
 match(shape.builder[Positive]().finish()) {
  _:shape.ValidationError=>-1
  row:Row=>require(row)
 }
}}
fn main(){println(probe[Row]())}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil || string(output) != "2\n" {
		t.Fatalf("head proof: %s, %v", output, err)
	}
}

func TestDeriveTemplatePositionalMetadataAndView(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
class Render[T]{fn render(x:T):String}
derive instance render[T]:Render[T]{fn render(x:T):String{
 parts:List[String]=[comptime for(v in shape.variants[T]()) {
  fields:List[String]=[comptime for(f in v.fields) s"${f.positional}:${f.index}:${f.name}"]
  v.project(x).map(view=>{
   values:List[String]=[comptime for(f in v.fields) toString(f.read(view))]
   s"${v.positional}:${fields.join(",")}:${values.join(",")}"
  }).getOr("")
 }]
 parts.join("|")
}}
type Choice[A]=sealed{Pair(Int,A),Named{value:A},Empty} derive(Render)
fn main(){println(render(Choice[String].Pair(2,"ok")));println(render(Choice[Int].Named{value:4}))}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil || string(output) != "true:true:0:,true:1::2,ok||\n|false:false:0:value:4|\n" {
		t.Fatalf("positional views: %s, %v", output, err)
	}
}

func TestDeriveTemplatePositionalBuilder(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred positive(n:Int){n>0}
class Build[T]{fn build(n:Int):T|shape.ValidationError}
derive instance build[T]:Build[T]{fn build(n:Int):T|shape.ValidationError{
 result:List[T|shape.ValidationError]=[comptime for(v in shape.variants[T]()) comptime if(v.positional) {
  initial=v.builder()
  steps:List[(initial.Type)=>initial.Type]=[comptime for(f in v.fields) (state:initial.Type)=>state.set(f,n)]
  steps.fold(initial,(state,step)=>step(state)).finish()
 }]
 result.get(0).getOr(shape.ValidationError{path:"",message:"missing positional payload"})
}}
type Choice=sealed{Pair(Int where positive,Int),Empty} derive(Build)
fn main(){println(build[Choice](2));match(build[Choice](-1)) {e:shape.ValidationError=>{println(e.path);println(e.obligation.map(o=>o.field).getOr("missing"))},_:Choice=>println("unexpected success")}}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil || string(output) != "Choice.Pair(2, 2)\n[0]\n[0]\n" {
		t.Fatalf("positional construction: %s, %v", output, err)
	}
}

func TestDeriveTemplateTupleBuilderFacts(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred positive(n:Int){n>0}
class Probe[T]{fn probe(n:Int):String}
derive instance probe[T]:Probe[T]{fn probe(n:Int):String{
 initial=shape.builder[(Int where positive,Int)]()
 steps:List[(initial.Type)=>initial.Type]=[comptime for(f in shape.fields[(Int where positive,Int)]()) (state:initial.Type)=>state.set(f,n)]
 match(steps.fold(initial,(state,step)=>step(state)).finish()) {
  error:shape.ValidationError=>s"${error.path}:${error.obligation.map(o=>o.field).getOr("owner")}:error"
  _=>"success"
 }
}}
type Row={} derive(Probe)
fn main(){println(probe[Row](2));println(probe[Row](-1))}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil || string(output) != "success\n[0]:[0]:error\n" {
		t.Fatalf("tuple builder facts: %s, %v", output, err)
	}
}

func TestDeriveTemplateBuilderNestedHeadFacts(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred positive(n:Int){n>0}
class Build[T]{fn build(n:Int):String}
derive instance build[T]:Build[T]{fn build(n:Int):String{
 initial=shape.builder[Box[Int where positive]]()
 steps:List[(initial.Type)=>initial.Type]=[comptime for(f in shape.fields[Box[Int]]()) (state:initial.Type)=>state.set(f,n)]
 match(steps.fold(initial,(state,step)=>step(state)).finish()) {
  error:shape.ValidationError=>error.path
  _=>"success"
 }
}}
type Box[A]={value:A}
type Row={} derive(Build)
fn main(){println(build[Row](2));println(build[Row](-1))}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil || string(output) != "success\n.value\n" {
		t.Fatalf("nested head facts: %s, %v", output, err)
	}
}

func TestDeriveTemplateMemoizedHelperWorkBudget(t *testing.T) {
	fields := make([]string, 256)
	for i := range fields {
		fields[i] = fmt.Sprintf("field%d:Int", i)
	}
	source := `import "bork/shape"
class Label[T]{fn label(x:T):String}
derive fn names[A](x:A):String {
 labels:List[String]=[comptime for(f in shape.fields[A]()) f.name]
 labels.join(",")
}
derive instance labels[T]:Label[T]{fn label(x:T):String {
 words:List[String]=[comptime for(f in shape.fields[T]()) names[T](x)]
 words.join("|")
}}
type Row={` + strings.Join(fields, ",") + `} derive(Label)
fn main(){}`
	checkPreludeSource(t, source, "compile-time work limit")
}

func TestDeriveTemplateBuilderSealedHeadFacts(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
pred positive(n:Int){n>0}
type Choice[A]=sealed{Full{item:A},Empty}
type Positive=Choice[Int where positive]
class Build[T]{fn build(n:Int):String}
derive instance build[T]:Build[T]{fn build(n:Int):String{
 results:List[String]=[comptime for(v in shape.variants[Positive]()) comptime if(v.name=="Full") {
  initial=v.builder()
  steps:List[(initial.Type)=>initial.Type]=[comptime for(f in v.fields) (state:initial.Type)=>state.set(f,n)]
  match(steps.fold(initial,(state,step)=>step(state)).finish()) {error:shape.ValidationError=>error.path,_=>"success"}
 }]
 results.get(0).getOr("missing")
}}
type Row={} derive(Build)
fn main(){println(build[Row](2));println(build[Row](-1))}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	if err != nil || string(output) != "success\n.item\n" {
		t.Fatalf("sealed nested head facts: %s, %v", output, err)
	}
}

func TestDeriveTemplateUnrequestedConcreteDataflow(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `derive fn unused(x: Int): String { value = x + 1; value }
fn main() {}`, "derive expression must be String, found Int")
	checkPreludeSource(t, `derive fn unused(x: Int): Int { if (x) { 1 } else { 2 } }
fn main() {}`, "derive expression must be Bool, found Int")
}

func TestDeriveTemplateUnrequestedConcreteCall(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `fn accept(x: Bool): String { "ok" }
derive fn unused(value: Int): String { accept(value) }
fn main() {}`, "argument 1 to accept must be Bool, found Int")
}

func TestDeriveTemplateUnrequestedContextualLambdas(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `derive fn unused(): Int {
  value: (Int) => String = x => x + 1
  0
}

fn main() {}`, "derive expression must be String, found Int")
	checkPreludeSource(t, `derive fn unused(): Int {
  value: (Int) => String = x => { return x + 1 }
  0
}
fn main() {}`, "derive expression must be String, found Int")
	checkPreludeSource(t, `derive fn unused(): Int {
  value: (Int) => Int = (x: String) => 1
  0
}
fn main() {}`, "parameter x must be Int here, found String")
	checkPreludeSource(t, `derive fn unused(): Int {
  value: (Int) => String = x => { return "yes" }
  0
}
fn main() {}`, "")
	checkPreludeSource(t, `derive fn unused(): Int {
  value: (Int) => Int = (x, y) => 1
  0
}
fn main() {}`, "expected a function taking 1 argument(s), but this lambda takes 2")
}

func TestDeriveTemplateUnrequestedMetadataScalarTypes(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](): Int {
  comptime for (field in shape.fields[T]()) { value: Int = field.name }
  0
}
fn main() {}`, "derive expression must be Int, found String")
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](): Int {
  fields = shape.fields[T]()
  comptime for (field in fields) { if (field.name) { 1 } else { 2 } }
  0
}
fn main() {}`, "derive expression must be Bool, found String")
	checkPreludeSource(t, `import "bork/shape"
fn accept(value: Bool): Int { 1 }
derive fn unused[T](): Int {
  comptime for (field in shape.fields[T]()) { value = accept(field.name) }
  0
}
fn main() {}`, "argument 1 to accept must be Bool, found String")
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](): Int {
  comptime for (variant in shape.variants[T]()) {
    fields = variant.fields
    comptime for (field in fields) {
      comptime for (fact in field.facts) { value: Int = fact.path }
    }
  }
  0
}
fn main() {}`, "derive expression must be Int, found String")
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](): Int {
  comptime for (field in shape.fields[T]()) {
    name: String = field.name
    index: Int = field.index
    positional: Bool = field.positional
    computed: Bool = field.computed
    hasDefault: Bool = field.hasDefault
  }
  0
}
fn main() {}`, "")
}

func TestDeriveTemplateRuntimeForHeaderScopes(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `derive fn unused(): Int {
  for (i = 0; i < 3; i = i + 1) { value = i + 1 }
  0
}
fn main() {}`, "")
	checkPreludeSource(t, `derive fn unused(): Int {
  for (i = 0; i < 3; i = i + 1) {}
  i
}
fn main() {}`, "undefined local in derive definition: i")
}

func TestDeriveTemplateConcreteCheckingDefersAmbientCalls(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `ambient trace: String
fn readTrace() needs trace: String { trace }
derive fn unused() needs trace: String { readTrace() }
derive fn provided(): String { with (trace: "yes") { readTrace() } }
fn main() {}`, "")
}

func TestDeriveTemplateRuntimeForCarriesAndPostTargets(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `derive fn unused(): Int {
  for (i = 0; i < 3; i = i + 1) { i = i + 1 }
  0
}
fn main() {}`, "")
	checkPreludeSource(t, `derive fn unused(): Int {
  for (i = 0; i < 3; missing = 0) {}
  0
}
fn main() {}`, "undefined loop post target in derive definition: missing")
}

func TestDeriveTemplateDeferredHelperValidationWorkBudget(t *testing.T) {
	t.Parallel()
	var fields strings.Builder
	for i := 0; i < 128; i++ {
		fmt.Fprintf(&fields, "f%d: Int = 1,", i)
	}
	source := `import "bork/shape"
class C[T] { fn c(x: T): Bool }
derive fn finish[A, S](value: S): Bool {
  match (value.finish()) {
    _: A => true,
    _: shape.ValidationError => false
  }
}
derive instance c[T]: C[T] {
  fn c(x: T): Bool {
    builder = shape.builder[T]()
    _ = [comptime for (field in shape.fields[T]()) finish[T, builder.Type](builder)]
    true
  }
}
type Row = {` + fields.String() + `} derive (C)
fn main() {}`
	checkPreludeSource(t, source, "derive template expansion exceeds its compile-time work limit")
}

func TestDeriveTemplateUnrequestedConcreteGenericCalls(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `fn identity[A](value: A): A { value }
derive fn unused(value: Int): String { identity[Int](value) }
fn main() {}`, "derive expression must be String, found Int")
	checkPreludeSource(t, `fn identity[A](value: A): A { value }
derive fn unused(value: Int): Int { identity[Int](value) }
derive fn symbolic[A](value: A): A { identity[A](value) }
fn main() {}`, "")
}

func TestDeriveTemplateConcreteCheckingDefersDependentGenericContext(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `fn empty[A](): List[A] { [] }
derive fn emptyFor[T](): List[T] { empty() }
derive fn local[T](): List[T] { values: List[T] = empty(); values }
fn main() {}`, "")
}

func TestDeriveTemplateStagedForPreservesRuntimeCarryScope(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](): Int {
  for (i = 0; i < 3; i = i + 1) {
    comptime for (field in shape.fields[T]()) { i = i + 1 }
  }
  0
}
fn main() {}`, "")
}

func TestDeriveTemplatePositionalTargetPatternIsNotBareType(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `class Label[T] { fn label(x: T): Bool }
derive instance label[T]: Label[T] { fn label(x: T): Bool { x is T(_) } }
type Row = {} derive (Label)
fn main() {}`, "is patterns cannot bind names")
}

func TestDeriveTemplateUnrequestedMetadataQueryTypes(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](): Int { value: Int = shape.name[T](); 0 }
fn main() {}`, "derive expression must be Int, found String")
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](): Int { if (shape.owner[T]()) { 1 } else { 2 } }
fn main() {}`, "derive expression must be Bool, found String")
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](): Int {
  fields = shape.fields[T]()
  value: String = fields.length()
  0
}
fn main() {}`, "derive expression must be String, found Int")
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](): Int {
  fields = shape.fields[T]()
  value: Int = fields.isEmpty()
  0
}
fn main() {}`, "derive expression must be Int, found Bool")
}

func TestDeriveTemplateUnrequestedMetadataParameterTypes(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](field: shape.Field[T]): Int { value: Int = field.name; 0 }
fn main() {}`, "derive expression must be Int, found String")
	checkPreludeSource(t, `import s "bork/shape"
derive fn unused[T](variants: List[s.Variant[T]]): Int {
  comptime for (variant in variants) {
    comptime for (field in variant.fields) {
      comptime for (fact in field.facts) { value: Int = fact.text }
    }
  }
  0
}
fn main() {}`, "derive expression must be Int, found String")
}

func TestDeriveTemplateUnrequestedMatchKeepsOuterTypes(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `derive fn unused[T](x: T, outer: Int): String {
  match (x) { _: T => outer }
}
fn main() {}`, "derive expression must be String, found Int")
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](x: T): Int {
  comptime for (field in shape.fields[T]()) {
    match (x) { _: T => { value: Int = field.name; 0 } }
  }
  0
}
fn main() {}`, "derive expression must be Int, found String")
}

func TestDeriveTemplateFactDiagnosticsUseRequest(t *testing.T) {
	t.Parallel()
	for _, expression := range []string{"required(-1)", "required(n)"} {
		t.Run(expression, func(t *testing.T) {
			source := `class C[T] { fn c(x: T, n: Int): Int }; pred positive(x: Int) { x > 0 }
fn required(value: Int where positive): Int { value }
derive instance c[T]: C[T] { fn c(x: T, n: Int): Int { ` + expression + ` } }
type Row = {} derive(C)
fn main() {}`
			dir := t.TempDir()
			path := filepath.Join(dir, "main.bork")
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(dir)
			if err == nil || !strings.Contains(err.Error(), path+":4:") || !strings.Contains(err.Error(), "derive template at "+path+":3:") {
				t.Fatalf("expected fact failure at the derive request with template provenance, got %v", err)
			}
		})
	}
}

func TestDeriveTemplateHelperRequirementFactDiagnosticsUseRequest(t *testing.T) {
	t.Parallel()
	for _, expression := range []string{"required(-1)", "required(n)"} {
		t.Run(expression, func(t *testing.T) {
			source := `pred positive(x: Int) { x > 0 }; fn required(value: Int where positive): Int { value }
derive fn helper(n: Int) where positive(` + expression + `): Int { n }
class C[T] { fn c(x: T, n: Int): Int }; derive instance c[T]: C[T] { fn c(x: T, n: Int): Int { helper(n) } }
type Row = {} derive(C)
fn main() {}`
			dir := t.TempDir()
			path := filepath.Join(dir, "main.bork")
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(dir)
			if err == nil || !strings.Contains(err.Error(), path+":4:") || !strings.Contains(err.Error(), "derive template at "+path+":2:") {
				t.Fatalf("expected helper requirement failure at request with helper provenance, got %v", err)
			}
		})
	}
}

func TestDeriveTemplateUnrequestedMetadataLoopQuickFix(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
derive fn unused[T](): Int {
  fields = shape.fields[T]()
  for (field in fields) { value = field.name }
  0
}
fn main() {}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Check(dir)
	failure, ok := err.(*DiagError)
	if !ok {
		t.Fatalf("expected definition diagnostic, got %v", err)
	}
	for _, diagnostic := range failure.Diags.Sorted() {
		if strings.Contains(diagnostic.Msg, "add comptime") {
			if len(diagnostic.Fixes) != 1 || len(diagnostic.Fixes[0].Edits) != 1 || diagnostic.Fixes[0].Edits[0].Replacement != "comptime " {
				t.Fatalf("missing prefix quick fix: %#v", diagnostic)
			}
			return
		}
	}
	t.Fatalf("missing runtime metadata iteration diagnostic: %v", err)
}

func TestDeriveTemplateUnrequestedMetadataKindTypes(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](): Int { if (shape.kind[T]()) { 1 } else { 2 } }
fn main() {}`, "derive expression must be Bool")
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](): Int { value: String = shape.Record; 0 }
fn main() {}`, "derive expression must be String")
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](): Bool { shape.kind[T]() == shape.Record }
fn main() {}`, "")
}

func TestDeriveTemplateUnrequestedDescriptorMemberContracts(t *testing.T) {
	for _, item := range []struct{ name, code, error string }{
		{"field property", "field.nmae", "shape descriptor has no member nmae"},
		{"fact property", "fact.txt", "shape descriptor has no member txt"},
		{"variant property", "variant.computed", "shape descriptor has no member computed"},
		{"read arity", "field.read()", "shape descriptor read takes 1 argument(s)"},
		{"default arity", "field.default(1)", "shape descriptor default takes 0 argument(s)"},
		{"project arity", "variant.project()", "shape descriptor project takes 1 argument(s)"},
		{"property called", "field.name()", "shape descriptor member name is a property"},
		{"field type called", "field.Type()", "shape descriptor member Type is a property"},
		{"variant type called", "variant.Type()", "shape descriptor member Type is a property"},
		{"sequence member", "fields.nmae", "shape descriptor has no member nmae"},
		{"sequence arity", "fields.length(1)", "metadata sequence length takes no arguments"},
		{"sequence type", "fields.length[Int]()", "requires its descriptor element type"},
	} {
		t.Run(item.name, func(t *testing.T) {
			source := `import "bork/shape"
derive fn inspect[T](field: shape.Field[T], variant: shape.Variant[T], fact: shape.Fact[T], fields: List[shape.Field[T]]): Ok {
 _ = ` + item.code + `
}
fn main() {}`
			checkPreludeSource(t, source, item.error)
		})
	}
}

func TestDeriveTemplateUnrequestedSequenceElementAlias(t *testing.T) {
	checkPreludeSource(t, `import "bork/shape"
type F = shape.Field[Int]
derive fn unused(fields: List[F]): Int { fields.length[F]() }
fn main() {}`, "")
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](fields: List[shape.Field[T]]): Int { fields.length[Int]() + 1 }
fn main() {}`, "requires its descriptor element type")
	checkPreludeSource(t, `import "bork/shape"
derive fn unused[T](fields: List[shape.Field[T]]): Int { fields.length[shape.Variant[T]]() }
fn main() {}`, "requires its descriptor element type")
}

func TestDeriveTemplateNestedSequenceContractSingleDiagnostic(t *testing.T) {
	dir := validatorFixture(t, `import "bork/shape"
fn accept(value: Int): Int { value }
derive fn unused[T](fields: List[shape.Field[T]]): Int { accept(fields.length[Int]()) }
fn main() {}`)
	_, _, err := Check(dir)
	if err == nil || strings.Count(err.Error(), "requires its descriptor element type") != 1 {
		t.Fatalf("expected one nested contract diagnostic, got %v", err)
	}
}
