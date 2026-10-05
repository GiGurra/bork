package syntax

import (
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestTupleGroupingAndFunctions(t *testing.T) {
	source := `fn f(a: (Int,), b: (Int, String), c: (Int), d: (Int, String) => Bool): (Int,) {
  grouped = (1)
  single = (1,)
  pair = (1, "one")
  function = (n: Int, s: String) => n > 0
  (number, (label,)) = (1, ("one",))
  single
}`
	d := &diag.List{}
	file := Parse("tuples.bork", []byte(source), d)
	if d.Len() != 0 {
		t.Fatal(d.Error())
	}
	fn := file.Funcs[0]
	if len(fn.Params[0].Type.Tuple) != 1 || len(fn.Params[1].Type.Tuple) != 2 || fn.Params[2].Type.Name != "Int" || fn.Params[3].Type.Func == nil {
		t.Fatal("tuple, grouped and function types were confused")
	}
	statements := fn.Body.Stmts
	if _, ok := statements[0].(*Binding).Value.(*IntLit); !ok {
		t.Fatal("grouped expression became a tuple")
	}
	if tuple, ok := statements[1].(*Binding).Value.(*TupleLit); !ok || len(tuple.Elems) != 1 {
		t.Fatal("singleton tuple not recognized")
	}
	if tuple, ok := statements[2].(*Binding).Value.(*TupleLit); !ok || len(tuple.Elems) != 2 {
		t.Fatal("pair not recognized")
	}
	if _, ok := statements[3].(*Binding).Value.(*Lambda); !ok {
		t.Fatal("lambda became a tuple")
	}
	if _, ok := statements[4].(*TupleBinding); !ok {
		t.Fatal("tuple binding not recognized")
	}
}

func TestTupleSelectorsKeepFloats(t *testing.T) {
	d := &diag.List{}
	tokens, _ := Lex("tuples.bork", []byte("pair.0.12\n1.25\n"), d)
	want := []Kind{TIdent, Dot, TInt, Dot, TInt, Semi, TFloat, Semi, EOF}
	if d.Len() != 0 || len(tokens) != len(want) {
		t.Fatalf("tokens: %+v; %s", tokens, d.Error())
	}
	for i, kind := range want {
		if tokens[i].Kind != kind {
			t.Fatalf("token %d = %s, want %s", i, tokens[i].Kind, kind)
		}
	}
}

func TestTupleSyntaxErrorsRecover(t *testing.T) {
	for _, source := range []string{
		"fn f() { println(()) }",
		"fn f(): () { 1 }",
		"fn f() { (a) = 1 }",
		"fn f() { println((1, 2).0x0) }",
	} {
		d := &diag.List{}
		Parse("tuples.bork", []byte(source), d)
		if d.Len() == 0 {
			t.Errorf("accepted invalid tuple syntax: %s", source)
		}
	}
}
