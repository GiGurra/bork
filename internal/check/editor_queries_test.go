package check

import (
	"maps"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
)

func TestEditorTypeQueriesPreserveSourceIndexes(t *testing.T) {
	d := &diag.List{}
	files := prelude.Parse(d)
	file := syntax.Parse("main.bork", []byte("pred positive(n: Int) { n > 0 }\ntype Box[T] = { value: T }\nfn main() {}\n"), d)
	file.Package = "example.com/app"
	files = append(files, file)
	info := Program(files, file.Package, d, nil)
	if d.Len() != 0 {
		t.Fatal(d.Error())
	}
	var from *Package
	for _, pkg := range info.Packages {
		if pkg.Path == file.Package {
			from = pkg
		}
	}
	written := maps.Clone(info.writtenTypes)
	uses := maps.Clone(info.typeUses)
	predicates := maps.Clone(info.predicateRefs)
	for i := 0; i < 20; i++ {
		query := syntax.Parse("query.bork", []byte("type Query = Box[Int where positive]\n"), d).Types[0].Alias
		if typ := EditorType(info, from, query); typ == Invalid {
			t.Fatal("valid type query failed")
		}
	}
	if !maps.Equal(written, info.writtenTypes) || !maps.Equal(uses, info.typeUses) || !maps.Equal(predicates, info.predicateRefs) {
		t.Fatal("editor query changed source indexes")
	}
}

func TestFactQueryCallArgument(t *testing.T) {
	info := executionAuditProgram(t, `pred above(x: Int, threshold: Int) { x > threshold }
fn minimum(): Int { 0 }
fn identity(value: Int): Int { value }
fn main() {}`)
	fn := info.Funcs["identity"]
	value := fn.Body.Tail
	_, _, err := DescribeFacts(info, fn, value, value.Pos(), "above(minimum())", nil)
	if err == nil || !strings.Contains(err.Error(), "predicate arguments are constants or parameter names") {
		t.Fatalf("want invalid predicate argument diagnostic, got %v", err)
	}
}
