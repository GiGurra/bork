package gen

import (
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
)

// Packages only the prelude imports cost nothing until a program uses them.
func TestPreludeImportsEmittedOnlyWhenUsed(t *testing.T) {
	for _, tc := range []struct {
		source string
		shape  bool
	}{
		{`fn main() uses io { println("hi") }`, false},
		{"import \"bork/shape\"\nfn main() uses io { println(shape.Tag { name: \"a\", value: \"b\" }) }", true},
	} {
		var diags diag.List
		files := prelude.Parse(&diags)
		root := syntax.Parse("main.bork", []byte(tc.source), &diags)
		root.Package = "example.com/main"
		files = append(files, root)
		info := check.Program(files, root.Package, &diags, nil)
		if diags.Len() > 0 {
			t.Fatalf("check: %v", diags.Sorted())
		}
		generated, err := Package(files, info)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(generated), "_shape_"); got != tc.shape {
			t.Errorf("%s: emits bork/shape declarations = %t, want %t", tc.source, got, tc.shape)
		}
	}
}
