package syntax

import (
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func kinds(src string) string {
	toks, _ := Lex("t.bork", []byte(src), &diag.List{})
	var parts []string
	for _, t := range toks {
		parts = append(parts, t.Kind.String())
	}
	return strings.Join(parts, " ")
}

func TestLexNewlinesEndStatements(t *testing.T) {
	cases := map[string]string{
		// A newline after an identifier, literal, ')' or '}' ends the statement.
		"x = 1\ny = 2": "identifier '=' integer literal newline or ';' identifier '=' integer literal newline or ';' end of file",
		// A newline after an operator does not.
		"a +\nb": "identifier '+' identifier newline or ';' end of file",
		// Nor after '(' or ','.
		"f(\n1,\n2)": "identifier '(' integer literal ',' integer literal ')' newline or ';' end of file",
		// Comments are skipped; a line comment keeps the newline.
		"x // note\ny": "identifier newline or ';' identifier newline or ';' end of file",
		"return\n":     "'return' newline or ';' end of file",
	}
	for src, want := range cases {
		if got := kinds(src); got != want {
			t.Errorf("Lex(%q)\n got: %s\nwant: %s", src, got, want)
		}
	}
}

func TestLexKeepsComments(t *testing.T) {
	_, comments := Lex("t.bork", []byte("// one\nx /* two */"), &diag.List{})
	if len(comments) != 2 || comments[0].Text != "// one" || comments[1].Text != "/* two */" {
		t.Fatalf("unexpected comments: %+v", comments)
	}
}

func TestLexRejectsLeadingUnderscore(t *testing.T) {
	diags := &diag.List{}
	Lex("t.bork", []byte("_t1"), diags)
	if diags.Len() != 1 {
		t.Fatalf("expected one diagnostic, got %q", diags.Error())
	}
}

func TestParseFunction(t *testing.T) {
	src := `fn add(
  a: Int,
  b: Int,
): Int {
  sum = a + b * 2
  sum
}`
	diags := &diag.List{}
	f := Parse("t.bork", []byte(src), diags)
	if diags.Len() != 0 {
		t.Fatalf("unexpected errors: %s", diags.Error())
	}
	if len(f.Funcs) != 1 {
		t.Fatalf("expected 1 function, got %d", len(f.Funcs))
	}
	fn := f.Funcs[0]
	if fn.Name != "add" || len(fn.Params) != 2 || fn.Result.Name != "Int" {
		t.Fatalf("unexpected signature: %+v", fn)
	}
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(fn.Body.Stmts))
	}
	if id, ok := fn.Body.Tail.(*Ident); !ok || id.Name != "sum" {
		t.Fatalf("expected tail 'sum', got %#v", fn.Body.Tail)
	}
	// Precedence: a + (b * 2)
	bin := fn.Body.Stmts[0].(*Binding).Value.(*Binary)
	if bin.Op != Plus {
		t.Fatalf("expected '+' at the top, got %s", bin.Op)
	}
	if inner, ok := bin.Y.(*Binary); !ok || inner.Op != Star {
		t.Fatalf("expected b * 2 on the right, got %#v", bin.Y)
	}
}

func TestParseRecoversAtNextFunction(t *testing.T) {
	src := "fn broken( {\n}\n\nfn ok() {\n}\n"
	diags := &diag.List{}
	f := Parse("t.bork", []byte(src), diags)
	if diags.Len() == 0 {
		t.Fatal("expected a syntax error")
	}
	if len(f.Funcs) != 1 || f.Funcs[0].Name != "ok" {
		t.Fatalf("expected parsing to recover and find 'ok', got %d funcs", len(f.Funcs))
	}
}

func TestLexNumbers(t *testing.T) {
	cases := map[string]string{
		"1_000 0xFF 0b10 0o7": "integer literal integer literal integer literal integer literal newline or ';' end of file",
		"1.5 2e10 1.5e-3":     "float literal float literal float literal newline or ';' end of file",
		// A '.' needs a digit after it to make a float.
		"5.copy": "integer literal '.' identifier newline or ';' end of file",
	}
	for src, want := range cases {
		if got := kinds(src); got != want {
			t.Errorf("Lex(%q)\n got: %s\nwant: %s", src, got, want)
		}
	}
}

func TestLexGoCode(t *testing.T) {
	src := "fn f(): String unsafe go {\n  s := \"}\" + `{` // }\n  /* { */ if true { return s }\n  return '}'\n}\nx"
	toks, _ := Lex("t.bork", []byte(src), &diag.List{})
	var code *Token
	for i := range toks {
		if toks[i].Kind == TGoCode {
			code = &toks[i]
		}
	}
	if code == nil {
		t.Fatalf("no Go code token in %s", kinds(src))
	}
	want := "\n  s := \"}\" + `{` // }\n  /* { */ if true { return s }\n  return '}'\n"
	if code.Text != want {
		t.Errorf("Go code = %q, want %q", code.Text, want)
	}
	if last := toks[len(toks)-3]; last.Kind != TIdent || last.Text != "x" {
		t.Errorf("lexing did not resume after the Go code: %s", kinds(src))
	}
}

func TestParseGoCodeImports(t *testing.T) {
	diags := &diag.List{}
	f := Parse("t.bork", []byte("fn f(): Int unsafe go {\n  import \"math\"\n  return int64(math.Sqrt(4))\n}\n"), diags)
	if diags.Len() != 0 {
		t.Fatal(diags.Error())
	}
	gc := f.Funcs[0].GoBody
	if len(gc.Imports) != 1 || gc.Imports[0] != "math" || strings.Contains(gc.Body, "import") {
		t.Errorf("imports = %q, body = %q", gc.Imports, gc.Body)
	}
}
