package check

import (
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
)

func TestPositionalVariantFixes(t *testing.T) {
	for _, tc := range []struct {
		name, source, code string
		input              bool
	}{
		{"constructor", `fn main(){println(Option.Some { value: { n=1; n+2 } })}`, "type.variant_payload_form", false},
		{"nested context constructor", `type Config={value:Int};fn main(){x:Option[Config]=.Some{value:.{value:1}};println(x)}`, "type.variant_payload_form", false},
		{"nested pattern", `fn f(x:Option[List[Int]]):Int{match(x){.Some { value: [n, ...] }=>n,_=>0}}`, "type.variant_payload_form", false},
		{"shorthand pattern", `fn f(x:Option[Int]):Int{match(x){Option.Some { value }=>value,Option.None=>0}}`, "type.variant_payload_form", false},
		{"partly inferred type", `type A[T,U]=sealed{Value(T)};fn main(){println(A.Value(1))}`, "type.constructor_inference", true},
		{"deferred missing type", `fn main(){println(["x"].fold(Option.None,(acc,x)=>acc))}`, "type.variant_expected", true},
		{"missing type", `fn main(){println(Option.None)}`, "type.variant_expected", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(text string) *diag.List {
				d := &diag.List{}
				files := prelude.Parse(d)
				file := syntax.Parse("main.bork", []byte(text), d)
				file.Package = "fix"
				Program(append(files, file), file.Package, d, nil)
				return d
			}
			d := run(tc.source)
			var found *diag.Fix
			for _, diagnostic := range d.Sorted() {
				if diagnostic.Code == tc.code && len(diagnostic.Fixes) > 0 {
					fix := diagnostic.Fixes[0]
					found = &fix
					break
				}
			}
			if found == nil {
				t.Fatalf("missing fix: %s", d.Error())
			}
			if found.RequiresInput != tc.input {
				t.Fatalf("input flag: %+v", found)
			}
			text := tc.source
			// Source edits are ordered from left to right and applied in reverse.
			for i := len(found.Edits) - 1; i >= 0; i-- {
				edit := found.Edits[i]
				replacement := edit.Replacement
				if tc.input {
					replacement = strings.ReplaceAll(replacement, "_", "Int")
				}
				text = text[:edit.Start.Col-1] + replacement + text[edit.End.Col-1:]
			}
			if d := run(text); d.Len() != 0 {
				t.Fatalf("fixed source failed: %s\n%s", text, d.Error())
			}
		})
	}
}
