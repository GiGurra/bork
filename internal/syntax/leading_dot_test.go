package syntax

import (
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestLeadingDotPatternLineBoundaries(t *testing.T) {
	for _, pattern := range []string{
		".Some(n)",
		".None",
		".Some(.Some((a, b)))",
		".Wrap { values: [.Some(n), ...rest] }",
		".Wrap { value: .Some(\") => in\") }",
		".Some(\n      n\n    )",
	} {
		for _, gap := range []string{"\n", "\n\n// next pattern\n", " /* next\npattern */ "} {
			for _, body := range []string{
				"for {\n    row in rows" + gap + pattern + " in row\n  } yield row",
				"match x {\n    .First => x" + gap + pattern + " => x\n  }",
			} {
				t.Run(body, func(t *testing.T) {
					d := &diag.List{}
					Parse("pattern.bork", []byte("fn f() {\n  "+body+"\n}"), d)
					if d.Len() != 0 {
						t.Fatal(d.Error())
					}
				})
			}
		}
	}
}

func TestComprehensionLeadingDotChains(t *testing.T) {
	source := `fn f() {
  for {
    row in rows
      .map(r => r)
    .Some(n) in row
      // Still a source continuation.
      .filter(x => true)
    label = n
      .toString()
    if label
      .isEmpty()
  } yield label
    .trim()
}`
	d := &diag.List{}
	file := Parse("chain.bork", []byte(source), d)
	if d.Len() != 0 {
		t.Fatal(d.Error())
	}
	g := file.Funcs[0].Body.Tail.(*Generate)
	outer := g.Body.Stmts[0].(*ExprStmt).X.(*For)
	if call, ok := outer.Items.(*Call); !ok || call.Fun.(*Selector).Name != "map" {
		t.Fatalf("source lost its chain: %#v", outer.Items)
	}
	inner := outer.Body.Stmts[0].(*ExprStmt).X.(*For)
	if call, ok := inner.Items.(*Call); !ok || call.Fun.(*Selector).Name != "filter" {
		t.Fatalf("refutable source lost its chain: %#v", inner.Items)
	}
	m := inner.Body.Stmts[0].(*ExprStmt).X.(*Match)
	block := m.Arms[0].Body.(*Block)
	filter := block.Stmts[1].(*ExprStmt).X.(*If)
	yield := filter.Then.Stmts[0].(*ExprStmt).X.(*Yield)
	if call, ok := yield.Value.(*Call); !ok || call.Fun.(*Selector).Name != "trim" {
		t.Fatalf("yield lost its chain: %#v", yield.Value)
	}
}
