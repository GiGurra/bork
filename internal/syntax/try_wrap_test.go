package syntax

import (
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestTryMapperHeads(t *testing.T) {
	for _, source := range []string{
		`match x? { _ => 1 }`,
		`match x?{ _ => 1 }`,
		`match (x?{ e => Wrapped { cause: e } }) { _ => 1 }`,
		`if x? { true }`,
		`if x?{ true }`,
		`if (x?{ _ => false }) { true }`,
		`for x? { break }`,
		`for (x?{ _ => false }) { break }`,
	} {
		t.Run(source, func(t *testing.T) {
			d := &diag.List{}
			f := Parse("head.bork", []byte("fn f() { "+source+" }"), d)
			if d.Len() != 0 {
				t.Fatal(d.Error())
			}
			wraps := 0
			for _, span := range f.ExpressionSpans {
				if x, ok := span.Expr.(*Try); ok && x.Wrap != nil {
					wraps++
				}
			}
			if strings.Contains(source, "(x?") != (wraps > 0) {
				t.Fatalf("unexpected wrappers: %d", wraps)
			}
		})
	}
}

func TestTryMapperSpacingFix(t *testing.T) {
	for _, gap := range []string{" ", " /* context */ "} {
		source := "fn f() { x?" + gap + "{ e => e } }"
		d := &diag.List{}
		Parse("mapper.bork", []byte(source), d)
		if d.Len() != 1 {
			t.Fatalf("diagnostics: %s", d.Error())
		}
		diagnostic := d.Sorted()[0]
		if diagnostic.Code != "syntax.try_mapper_spacing" || len(diagnostic.Fixes) != 1 || len(diagnostic.Fixes[0].Edits) != 2 {
			t.Fatalf("missing spacing fix: %+v", diagnostic)
		}
		edits := diagnostic.Fixes[0].Edits
		for i := len(edits) - 1; i >= 0; i-- {
			edit := edits[i]
			source = source[:edit.Start.Col-1] + edit.Replacement + source[edit.End.Col-1:]
		}
		if !strings.Contains(source, "?{"+gap) {
			t.Fatalf("fix lost the gap's comments: %s", source)
		}
		fixed := &diag.List{}
		Parse("mapper.bork", []byte(source), fixed)
		if fixed.Len() != 0 {
			t.Fatalf("invalid fix: %s", fixed.Error())
		}
	}
}
