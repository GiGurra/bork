package check

import (
	"fmt"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
)

func TestSourceIndexCheckedIdentities(t *testing.T) {
	diags := &diag.List{}
	files := prelude.Parse(diags)
	model := syntax.Parse("model.bork", []byte(`pred Positive(value: Int) { value > 0 }
type Item = { value: Int }
type Alias = Item
type Choice = sealed { Some { value: Int }, Empty }
fn New = Item.new
fn Take(value: Int where Positive): Int where Positive { value }
Version = 3
`), diags)
	model.Package = "example.com/index/model"
	consumer := syntax.Parse("use.bork", []byte(`import "example.com/index/model"
fn Use(input: model.Alias): Int {
 other = model.New(value: input.value)
 match (other) { model.Alias { value } => value + model.Version }
}
fn Choice(value: model.Choice): Int {
 match (value) { model.Choice.Some { value: result } => result, model.Choice.Empty => 0 }
}
`), diags)
	consumer.Package = "example.com/index/use"
	files = append(files, model, consumer)
	info := Program(files, consumer.Package, diags, nil)
	if diags.Len() != 0 {
		t.Fatal(diags.Error())
	}
	index := BuildSourceIndex(files, info)
	foundRaw := false
	for _, symbol := range index.Symbols() {
		if symbol.Name == "Take" {
			foundRaw = symbol.Declaration == model.Funcs[len(model.Funcs)-1].Pos && symbol.Definition.Col == 4
		}
	}
	if !foundRaw {
		t.Fatal("raw declaration position was not retained")
	}
	find := func(file *syntax.File, fragment string) *SourceReference {
		t.Helper()
		offset := strings.Index(file.Source, fragment)
		if offset < 0 {
			t.Fatal(fragment)
		}
		pos := diag.Pos{File: file.Path, Line: strings.Count(file.Source[:offset], "\n") + 1, Col: offset - strings.LastIndex(file.Source[:offset], "\n")}
		ref := index.At(pos)
		if ref == nil {
			t.Fatalf("no identity for %q at %s", fragment, pos)
		}
		return ref
	}
	for _, tc := range []struct {
		decl, use string
		file      *syntax.File
	}{
		{"Alias =", "Alias):", consumer},
		{"value: Int }", "value)", consumer},
		{"New =", "New(value", consumer},
		{"Version =", "Version }", consumer},
		{"Some {", "Some {", consumer},
		{"Empty }", "Empty =>", consumer},
		{"Positive(value", "Positive):", model},
	} {
		declaration := find(model, tc.decl)
		use := find(tc.file, tc.use)
		if declaration.Definition != use.Definition {
			t.Fatalf("%q resolves to %s, want %s", tc.use, use.Definition, declaration.Definition)
		}
	}
	field := find(model, "value: Int }")
	foundShorthand := false
	for _, ref := range index.References(field.Definition) {
		foundShorthand = foundShorthand || ref.Suffix == ": value"
	}
	if !foundShorthand {
		t.Fatal("field shorthand not represented")
	}
}

func TestSourceIndexExactPositions(t *testing.T) {
	for _, src := range []string{
		"type Item = { item: Int }\nfn Use(item: Item): Int { item.item }\n",
		"fn Use(): String { s\"${{ local = 1; local }}\" }\nfn Other(local: Int): Int { local }\n",
		"type Inner = { value: Int }\ntype Outer = { inner: Inner }\nfn Use(o: Outer): Outer { o.copy(inner . value: 1) }\n",
	} {
		d := &diag.List{}
		files := prelude.Parse(d)
		file := syntax.Parse("exact.bork", []byte(src), d)
		file.Package = "exact"
		files = append(files, file)
		info := Program(files, file.Package, d, nil)
		if d.Len() != 0 {
			t.Fatal(d.Error())
		}
		index := BuildSourceIndex(files, info)
		for _, symbol := range index.Symbols() {
			if symbol.Definition.File != file.Path {
				continue
			}
			if symbol.Name == "item" || symbol.Name == "local" || symbol.Name == "value" {
				refs := index.References(symbol.Definition)
				if len(refs) != 2 {
					t.Fatalf("%s: %s has wrong references: %+v", src, symbol.Name, refs)
				}
				for _, ref := range refs {
					if symbol.Name == "local" && ref.Start.Line != symbol.Definition.Line {
						t.Fatalf("interpolation declaration escaped its function: %+v", ref)
					}
				}
			}
		}
	}
}

func TestContextPatternIdentitiesAndVisibility(t *testing.T) {
	for _, private := range []bool{false, true} {
		d := &diag.List{}
		files := prelude.Parse(d)
		model := syntax.Parse("model.bork", []byte("type Choice = sealed { Some { value: Int }, Empty, hidden }\n"), d)
		model.Package = "example.com/context/model"
		source := "import \"example.com/context/model\"\nfn read(x: model.Choice | String): Int { match (x) { . /* note */ Some { value } => value, . Empty => 0, _ => 1 } }\n"
		if private {
			source = "import \"example.com/context/model\"\nfn read(x: model.Choice): Int { match (x) { .hidden => 0, _ => 1 } }\n"
		}
		file := syntax.Parse("use.bork", []byte(source), d)
		file.Package = "example.com/context/use"
		files = append(files, model, file)
		info := Program(files, file.Package, d, nil)
		if private {
			if !strings.Contains(d.Error(), "not exported") {
				t.Fatalf("private context variant: %s", d.Error())
			}
			continue
		}
		if d.Len() != 0 {
			t.Fatal(d.Error())
		}
		index := BuildSourceIndex(files, info)
		for _, name := range []string{"Some", "Empty"} {
			offset := strings.Index(source, name)
			pos := diag.Pos{File: file.Path, Line: 2, Col: offset - strings.LastIndex(source[:offset], "\n")}
			ref := index.At(pos)
			if ref == nil || ref.Start != pos || ref.Name != name || ref.Definition.File != model.Path {
				t.Fatalf("context pattern %s: %+v", name, ref)
			}
			found := false
			for _, token := range SemanticTokens(file, info) {
				if token.Start == pos && token.Kind == "enumMember" && token.End.Col == pos.Col+len(name) {
					found = true
				}
			}
			if !found {
				t.Fatalf("context variant %s lacks semantic classification at %s", name, pos)
			}
		}
	}
}

func TestLoopTupleBindingIdentities(t *testing.T) {
	d := &diag.List{}
	files := prelude.Parse(d)
	file := syntax.Parse("loops.bork", []byte(`fn main() {
  for (index, (number, _)) in [(2, "two")].indexed() { println(index, number) }
  for (index, (number, _)) in [(3, "three")].indexed() { println(index, number) }
}
`), d)
	file.Package = "loops"
	files = append(files, file)
	info := Program(files, file.Package, d, nil)
	if d.Len() != 0 {
		t.Fatal(d.Error())
	}
	index := BuildSourceIndex(files, info)
	definitions := map[diag.Pos]bool{}
	for _, symbol := range index.Symbols() {
		if symbol.Definition.File != file.Path || symbol.Name != "index" && symbol.Name != "number" {
			continue
		}
		definitions[symbol.Definition] = true
		refs := index.References(symbol.Definition)
		if len(refs) != 2 {
			t.Fatalf("%s: references = %+v", symbol.Name, refs)
		}
		for _, ref := range refs {
			if ref.Start.Line != symbol.Definition.Line {
				t.Fatalf("binding escaped its loop: %+v", ref)
			}
		}
		found := false
		for _, token := range SemanticTokens(file, info) {
			if token.Start == symbol.Definition && token.Kind == "variable" {
				found = true
			}
		}
		if !found {
			t.Fatalf("no semantic declaration for %+v", symbol)
		}
	}
	if len(definitions) != 4 {
		t.Fatalf("got %d distinct loop bindings, want 4", len(definitions))
	}
}

func TestComprehensionBindingIdentities(t *testing.T) {
	d := &diag.List{}
	files := prelude.Parse(d)
	file := syntax.Parse("comprehension.bork", []byte(`fn main() {
  pairs = for {
    row in [[1, 2], [3]]
    (index, cell) in row.indexed()
    if cell > index
    total = cell + index
  } yield (row, total)
  println(pairs.toList())
}
`), d)
	file.Package = "comprehension"
	files = append(files, file)
	info := Program(files, file.Package, d, nil)
	if d.Len() != 0 {
		t.Fatal(d.Error())
	}
	index := BuildSourceIndex(files, info)
	// Each name's references: its later clauses and the yield.
	want := map[string][]int{"row": {3, 4, 7}, "index": {4, 5, 6}, "cell": {4, 5, 6}, "total": {6, 7}}
	tokens := SemanticTokens(file, info)
	for _, symbol := range index.Symbols() {
		lines, ok := want[symbol.Name]
		if symbol.Definition.File != file.Path || !ok {
			continue
		}
		delete(want, symbol.Name)
		var got []int
		for _, ref := range index.References(symbol.Definition) {
			got = append(got, ref.Start.Line)
		}
		if fmt.Sprint(got) != fmt.Sprint(lines) {
			t.Errorf("%s: reference lines = %v, want %v", symbol.Name, got, lines)
		}
		found := false
		for _, token := range tokens {
			if token.Start == symbol.Definition && token.Kind == "variable" && token.Declaration {
				found = true
			}
		}
		if !found {
			t.Errorf("no semantic declaration for %+v", symbol)
		}
	}
	if len(want) != 0 {
		t.Errorf("no symbols for %v", want)
	}
}

func TestRefutableGeneratorIdentities(t *testing.T) {
	d := &diag.List{}
	files := prelude.Parse(d)
	file := syntax.Parse("refutable.bork", []byte(`fn main() {
  values = for {
    .Some(value) in [Option.Some(1), Option.None]
    if value > 0
  } yield value
  println(values.toList())
}
`), d)
	file.Package = "refutable"
	files = append(files, file)
	info := Program(files, file.Package, d, nil)
	if d.Len() != 0 {
		t.Fatal(d.Error())
	}
	index := BuildSourceIndex(files, info)
	found := false
	for _, symbol := range index.Symbols() {
		if strings.HasPrefix(symbol.Name, "_") {
			t.Fatalf("hidden generator element is a symbol: %+v", symbol)
		}
		if symbol.Name != "value" || symbol.Definition.File != file.Path {
			continue
		}
		found = true
		var lines []int
		for _, ref := range index.References(symbol.Definition) {
			lines = append(lines, ref.Start.Line)
		}
		if fmt.Sprint(lines) != "[3 4 5]" {
			t.Fatalf("value: reference lines = %v", lines)
		}
	}
	if !found {
		t.Fatal("no symbol for value")
	}
	for _, token := range SemanticTokens(file, info) {
		if token.Start.Line == 3 && token.Start.Col == 5 && token.Kind == "variable" {
			t.Fatalf("hidden generator element has a semantic token: %+v", token)
		}
	}
}
