package check

import (
	"maps"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
)

func TestEditorTypeQueriesPreserveSourceIndexes(t *testing.T) {
	d := &diag.List{}
	files := prelude.Parse(d)
	file := syntax.Parse("main.bork", []byte("type Box[T] = { value: T }\nfn main() {}\n"), d)
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
	for i := 0; i < 20; i++ {
		query := syntax.Parse("query.bork", []byte("type Query = Box[Int]\n"), d).Types[0].Alias
		if typ := EditorType(info, from, query); typ == Invalid {
			t.Fatal("valid type query failed")
		}
	}
	if !maps.Equal(written, info.writtenTypes) || !maps.Equal(uses, info.typeUses) {
		t.Fatal("editor query changed source indexes")
	}
}
