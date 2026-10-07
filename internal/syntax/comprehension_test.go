package syntax

import (
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestComprehensionDesugars(t *testing.T) {
	src := `fn f(orders: List[Order]): Seq[(Int, Int)] {
  for {
    order in orders

    (i, line) in order.lines.indexed(); if line.active
    points: Int = score(line)
    (a, b) = (i, points)
  } yield (a, b)
}`
	diags := &diag.List{}
	file := Parse("t.bork", []byte(src), diags)
	if diags.Len() != 0 {
		t.Fatalf("unexpected errors: %s", diags.Error())
	}
	g, ok := file.Funcs[0].Body.Tail.(*Generate)
	if !ok || !g.Comprehension || g.Elem != nil {
		t.Fatalf("expected a comprehension generator, got %#v", file.Funcs[0].Body.Tail)
	}
	outer := g.Body.Stmts[0].(*ExprStmt).X.(*For)
	if outer.Name != "order" || outer.Items.(*Ident).Name != "orders" {
		t.Fatalf("unexpected outer generator: %#v", outer)
	}
	inner := outer.Body.Stmts[0].(*ExprStmt).X.(*For)
	if _, ok := inner.Pattern.(*TuplePat); !ok || inner.Name != "" {
		t.Fatalf("expected a tuple pattern generator, got %#v", inner)
	}
	filter := inner.Body.Stmts[0].(*ExprStmt).X.(*If)
	if filter.Else != nil {
		t.Fatalf("a filter has no else: %#v", filter)
	}
	stmts := filter.Then.Stmts
	if len(stmts) != 3 {
		t.Fatalf("expected two bindings and the yield, got %d statements", len(stmts))
	}
	if b := stmts[0].(*Binding); b.Name != "points" || b.Type == nil {
		t.Fatalf("unexpected binding: %#v", b)
	}
	if _, ok := stmts[1].(*TupleBinding); !ok {
		t.Fatalf("expected a tuple binding, got %#v", stmts[1])
	}
	if y := stmts[2].(*ExprStmt).X.(*Yield); y.Pos.Line != 8 {
		t.Fatalf("the yield keeps its position, got %v", y.Pos)
	}
}

func TestInfiniteLoopStaysALoop(t *testing.T) {
	for _, body := range []string{"break", "(a, b) = (1, 2)\n    break", "x = 1\n    break", "in = 1\n    break"} {
		src := "fn f() {\n  for {\n    " + body + "\n  }\n}"
		diags := &diag.List{}
		file := Parse("t.bork", []byte(src), diags)
		if diags.Len() != 0 {
			t.Fatalf("%q: unexpected errors: %s", body, diags.Error())
		}
		if f, ok := file.Funcs[0].Body.Tail.(*For); !ok || f.Form() != ForInfinite {
			t.Fatalf("%q: expected an infinite loop, got %#v", body, file.Funcs[0].Body.Tail)
		}
	}
}

func TestComprehensionErrors(t *testing.T) {
	cases := map[string]string{
		"_ = for { x in xs }":                     "a comprehension ends with } yield value",
		"_ = for { x in xs }\n  yield x":          "a comprehension's yield goes on the line of its closing '}'",
		"for { break } yield 1":                   "only a comprehension ends with yield",
		"_ = for { x in xs; if x { 1 } } yield x": "a comprehension's filter is if cond alone",
		"_ = for { x in xs; if x else } yield x":  "a comprehension's filter is if cond alone",
		"_ = for { x in xs; f(x) } yield x":       "a comprehension line is a generator",
		"_ = for { x in xs; lazy y = x } yield y": "a comprehension line is a generator",
		"_ = for { x in xs; trust p(x) } yield x": "a comprehension line is a generator",
	}
	for body, want := range cases {
		diags := &diag.List{}
		Parse("t.bork", []byte("fn f() {\n  "+body+"\n}"), diags)
		if diags.Len() == 0 || !strings.Contains(diags.Error(), want) {
			t.Errorf("%q: expected %q, got %q", body, want, diags.Error())
		}
	}
}
