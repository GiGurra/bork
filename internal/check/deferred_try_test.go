package check

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
)

func TestDeferredTryRejected(t *testing.T) {
	for _, tc := range []struct{ name, source, boundary string }{
		{"package implicit", `Value = parseInt("1")?`, "package value initializer"},
		{"package explicit", `lazy Value = parseInt("1")?`, "package value initializer"},
		{"lazy inferred", `fn f(text: String): Int | ParseError { lazy n = parseInt(text)?; n }`, "lazy initializer"},
		{"lazy annotated", `fn f(text: String): Int | ParseError { lazy n: Int | ParseError = parseInt(text)?; n }`, "lazy initializer"},
		{"async inferred", `fn f(text: String): Int | ParseError { scope s { async(s) n = parseInt(text)?; n } }`, "async initializer"},
		{"async annotated", `fn f(text: String): Int | ParseError { scope s { async(s) n: Int | ParseError = parseInt(text)?; n } }`, "async initializer"},
		{"existing value", `fn f(value: String): Int | ParseError { lazy n = parseInt(value)?; n }`, "lazy initializer"},
		{"option", `fn f(x: Option[Int]): Option[Int] { lazy n: Option[Int] = x?; n }`, "lazy initializer"},
		{"grouped comment", "fn f(text: String): Int | ParseError { lazy n = (parseInt( /* keep */ text))?; n }", "lazy initializer"},
		{"nested initializer", `fn f(text: String): Int | ParseError { lazy n = { lazy m = parseInt(text)?; m }; n }`, "lazy initializer"},
		{"nested lambda", `fn f(text: String): Int | ParseError { lazy n = { g = () => parseInt(text)?; g() }; n }`, "lambda"},
		{"field default", `type Box = { text: String; lazy value: Int | ParseError = parseInt(text)? }`, "lazy initializer"},
		{"field copy", `type Box = { lazy value: Int | ParseError }
fn f(text: String, box: Box): Box { box.copy(value: parseInt(text)?) }`, "lazy initializer"},
		{"field recipe", `type Box = { lazy value: Int | ParseError }
fn f(text: String): Box { Box { value: parseInt(text)? } }`, "lazy initializer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := func(source string) *diag.List {
				d := &diag.List{}
				files := prelude.Parse(d)
				f := syntax.Parse("main.bork", []byte(source), d)
				f.Package = "test"
				Program(append(files, f), f.Package, d, nil)
				return d
			}
			d := check(tc.source)
			var found *diag.Diagnostic
			for _, diagnostic := range d.Sorted() {
				if strings.Contains(diagnostic.Msg, "? cannot be used in a "+tc.boundary) {
					found = &diagnostic
					break
				}
			}
			if found == nil {
				t.Fatalf("missing rejection: %s", d.Error())
			}
			if tc.boundary == "lambda" {
				return
			}
			data, err := json.Marshal(found)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"requires_input":true`) || !strings.Contains(string(data), `replace ? with match`) {
				t.Fatalf("missing structured fix: %s", data)
			}
			if len(found.Fixes) != 1 || len(found.Fixes[0].Edits) != 2 {
				t.Fatalf("unexpected fixes: %+v", found.Fixes)
			}
			edits := append([]diag.TextEdit(nil), found.Fixes[0].Edits...)
			sort.Slice(edits, func(i, j int) bool { return sourcePositionCompare(edits[i].Start, edits[j].Start) > 0 })
			offset := func(p diag.Pos) int {
				lines := strings.SplitAfter(tc.source, "\n")
				return len(strings.Join(lines[:p.Line-1], "")) + p.Col - 1
			}
			fixed := tc.source
			for _, edit := range edits {
				fixed = fixed[:offset(edit.Start)] + edit.Replacement + fixed[offset(edit.End):]
			}
			if d := check(fixed); d.Len() != 0 {
				t.Fatalf("match scaffold does not check: %s\n%s", fixed, d.Error())
			}
			if strings.Contains(tc.source, "/* keep */") && !strings.Contains(fixed, "/* keep */") {
				t.Fatal("fix lost operand comment")
			}
		})
	}
}
