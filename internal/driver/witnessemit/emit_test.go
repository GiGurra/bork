// Package witnessemit_test shows that derive definition witnesses never
// change the emitted program. It toggles check.DefinitionWitnesses, so it is
// a package of its own: no other test shares its process.
package witnessemit_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/driver"
)

// Unused helpers here use dictionaries, generic instantiations, tuples,
// lambdas, scopes, embeds and comptime: each could leave an instance, a
// specialization or a captured file behind if witness checking leaked.
const unusedDefinitions = `import "bork/shape"
import "bork/embed"
pred positive(n: Int) { n > 0 }
type Box[A] = { value: A }
type Pair = { left: Int, right: String } derive (Labels)
class Labels[T] { fn labels(x: T): List[String] }
derive instance labels[T]: Labels[T] {
  fn labels(x: T): List[String] {
    [comptime for (field in shape.fields[T]()) comptime if (!field.computed) field.name + toString(field.read(x))]
  }
}
derive fn shown[T](x: T, n: Int where positive): String {
  box = Box[(Int, Float)] { value: (n, 1.5) }
  same = [x] == [x]
  text = embed.ReadString("data.txt")
  value = comptime { 40 + 2 }
  keep = (k: Int) => k + n
  toString(box.value) + toString(same) + text + toString(value) + toString(keep(1)) + toString(x)
}
derive fn counted[T](): Int {
  total: List[Int] = [comptime for (field in shape.fields[T]()) field.index]
  total.length()
}
fn main() { println(labels(Pair { left: 1, right: "x" })) }
`

func TestDefinitionWitnessesLeaveEmitUnchanged(t *testing.T) {
	inline := t.TempDir()
	for name, content := range map[string]string{"main.bork": unusedDefinitions, "data.txt": "data"} {
		if err := os.WriteFile(filepath.Join(inline, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{
		inline,
		"../../../examples/derive_labels",
		"../../../testdata/cases/foreign_record_custom",
		"../../../testdata/cases/typed_tags",
		"../../../testdata/cases/enum_fallback",
		"../../../testdata/cases/variant_docs",
	} {
		check.DefinitionWitnesses = false
		without, err := driver.Emit(dir)
		check.DefinitionWitnesses = true
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		_, info, err := driver.Check(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		if len(info.DefinitionWitnesses) == 0 {
			t.Fatalf("%s: no definition witnesses", dir)
		}
		with, err := driver.Emit(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		if !bytes.Equal(without, with) {
			t.Errorf("%s: definition witnesses changed the emitted program", dir)
		}
	}
}

// Editor data is the program's own: inlays, semantic tokens and references
// are the same with and without witnesses, for requested and unused
// definitions with constructors, tuples, lambdas, matches and interpolation.
func TestDefinitionWitnessesLeaveEditorDataUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	source := `import "bork/shape"
pred positive(n: Int) { n > 0 }
type P = { a: Int, b: String }
class C[T] { fn c(x: T, n: Int): String }
derive instance c[T]: C[T] {
  fn c(x: T, n: Int): String {
    pair = (n, "k")
    p = P { a: n, b: "$n" }
    work = (k: Int) => k + 1
    picked = match (work(n)) { 0 => "zero", _ => p.b }
    labels: List[String] = [comptime for (field in shape.fields[T]()) field.name + picked]
    toString(pair) + toString(labels)
  }
}
derive fn unused[T](x: T, n: Int where positive): String {
  q = P { a: n, b: "x" }
  pair = (x, q)
  _ = pair
  match (n) { 1 => "one", _ => q.b }
}
type R = { a: Int } derive (C)
fn main() { println(c(R { a: 1 }, 2)) }
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	editorData := func() string {
		analysis, err := driver.NewSession().Analyze(dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		inlays, err := analysis.EditorInlays(path, check.EditorInlayOptions{Types: true, Parameters: true})
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%+v\n%+v\n%+v", inlays, analysis.SemanticTokens(path), analysis.AllReferences())
	}
	check.DefinitionWitnesses = false
	without := editorData()
	check.DefinitionWitnesses = true
	if with := editorData(); with != without {
		t.Errorf("definition witnesses changed editor data:\nwithout: %s\nwith:    %s", without, with)
	}
}
