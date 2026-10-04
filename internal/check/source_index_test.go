package check

import (
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
