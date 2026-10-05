package check

import (
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
)

func TestContextPatternFixes(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		input        bool
	}{
		{"typo", "type A = sealed { Ready }\nfn f(x:A):Int{match(x){.Reedy=>1}}", false},
		{"ambiguous", "type A = sealed { Ready }\ntype B = sealed { Ready }\nfn f(x:A|B):Int{match(x){.Ready=>1,_=>0}}", false},
		{"specializations", "fn f(x:Option[Int]|Option[String]):Int{match(x){.Some{value:_}=>1,_=>0}}", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := func(source string) *diag.List {
				d := &diag.List{}
				files := prelude.Parse(d)
				file := syntax.Parse("main.bork", []byte(source), d)
				file.Package = "context"
				Program(append(files, file), file.Package, d, nil)
				return d
			}
			d := check(tc.source)
			var fixes []diag.Fix
			for _, diagnostic := range d.Sorted() {
				fixes = append(fixes, diagnostic.Fixes...)
			}
			if len(fixes) == 0 {
				t.Fatalf("no fixes: %s", d.Error())
			}
			for _, fix := range fixes {
				if fix.RequiresInput != tc.input || len(fix.Edits) != 1 {
					t.Fatalf("unexpected fix: %+v", fix)
				}
				edit := fix.Edits[0]
				if tc.input {
					if edit.Replacement != "Type.Some" {
						t.Fatalf("generic pattern fix must ask for an alias: %+v", edit)
					}
					continue
				}
				lines := strings.SplitAfter(tc.source, "\n")
				start := len(strings.Join(lines[:edit.Start.Line-1], "")) + edit.Start.Col - 1
				end := len(strings.Join(lines[:edit.End.Line-1], "")) + edit.End.Col - 1
				source := tc.source[:start] + edit.Replacement + tc.source[end:]
				if d := check(source); d.Len() != 0 {
					t.Fatalf("fix does not check: %s\n%s", source, d.Error())
				}
			}
		})
	}
}
