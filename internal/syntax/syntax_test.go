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

func TestParseUses(t *testing.T) {
	src := `fn serve(addr: String, h: (Int) uses io + state => Int | Ok) uses net + state: Ok | IoError {
}
fn update(f: (Int) uses nothing => Int): Int unsafe go { return 0 }
fn pure(): Int { 1 }`
	diags := &diag.List{}
	f := Parse("t.bork", []byte(src), diags)
	if diags.Len() != 0 {
		t.Fatalf("unexpected errors: %s", diags.Error())
	}
	names := func(u *Uses) []string {
		var out []string
		for _, e := range u.Effects {
			out = append(out, e.Name)
		}
		return out
	}
	serve := f.Funcs[0]
	if got := names(serve.Uses); len(got) != 2 || got[0] != "net" || got[1] != "state" {
		t.Fatalf("serve uses %v", got)
	}
	if len(serve.Result.Union) != 2 {
		t.Fatalf("serve's result should be a union, got %+v", serve.Result)
	}
	h := serve.Params[1].Type.Func
	if got := names(h.Uses); len(got) != 2 || got[0] != "io" || len(h.Result.Union) != 2 {
		t.Fatalf("h uses %v, result %+v", got, h.Result)
	}
	f2 := f.Funcs[1].Params[0].Type.Func
	if f2.Uses == nil || len(f2.Uses.Effects) != 0 {
		t.Fatalf("uses nothing parsed as %+v", f2.Uses)
	}
	if f.Funcs[1].Uses != nil || f.Funcs[2].Uses != nil {
		t.Fatalf("functions without uses got some")
	}
}

func TestUsesIsStillAName(t *testing.T) {
	src := `type R = { uses: Int, nothing: Int }
fn uses(nothing: Int): Int { io = nothing; io }
fn f(g: (Int) => Int): Int { g(1) }`
	diags := &diag.List{}
	f := Parse("t.bork", []byte(src), diags)
	if diags.Len() != 0 {
		t.Fatalf("unexpected errors: %s", diags.Error())
	}
	if len(f.Funcs) != 2 || f.Funcs[0].Name != "uses" || f.Funcs[0].Uses != nil {
		t.Fatalf("unexpected functions: %+v", f.Funcs)
	}
}

func TestParseUsesErrors(t *testing.T) {
	for src, want := range map[string]string{
		"fn f() uses {}":              "expected an effect name or nothing after uses",
		"fn f() uses nothing + io {}": "uses nothing cannot be combined with effects",
		"fn f() uses io + nothing {}": "uses nothing cannot be combined with effects",
		"fn f(g: (Int) uses io) {}":   "expected => after a function type's effects",
	} {
		diags := &diag.List{}
		Parse("t.bork", []byte(src), diags)
		if !strings.Contains(diags.Error(), want) {
			t.Errorf("%s: got %q, want %q", src, diags.Error(), want)
		}
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

func TestLexInterpolatedString(t *testing.T) {
	// Quotes inside ${...} start nested strings, not the end of the string.
	got := kinds(`s"a ${f("}")} $b" + s`)
	want := "interpolated string '+' identifier newline or ';' end of file"
	if got != want {
		t.Errorf("got: %s\nwant: %s", got, want)
	}
}

func TestParseGoImportAliases(t *testing.T) {
	diags := &diag.List{}
	f := Parse("t.bork", []byte("fn f(): Int unsafe go {\n import draw \"math/rand/v2\"\n return draw.Int64()\n}\n"), diags)
	if diags.Len() != 0 {
		t.Fatal(diags.Error())
	}
	gc := f.Funcs[0].GoBody
	if len(gc.Imports) != 1 || gc.ImportAliases["math/rand/v2"] != "draw" || strings.Contains(gc.Body, "import") {
		t.Fatalf("unexpected imports: %#v", gc)
	}
	for _, text := range []string{
		"import _ \"math\"",
		"import . \"math\"",
		"import first \"math\"\n import second \"math\"",
	} {
		diags := &diag.List{}
		Parse("t.bork", []byte("fn f() unsafe go {\n"+text+"\n}\n"), diags)
		if diags.Len() == 0 {
			t.Errorf("accepted invalid import: %s", text)
		}
	}
}

func TestParseAmbientMarkers(t *testing.T) {
	diags := &diag.List{}
	f := Parse("t.bork", []byte("logged ambient a: String\npropagated(\"traceparent\") logged ambient b: String\nambient c: Int\n"), diags)
	if diags.Len() != 0 {
		t.Fatal(diags.Error())
	}
	a, b, c := f.Ambients[0], f.Ambients[1], f.Ambients[2]
	if a.Logged == nil || a.Propagated != nil || b.Logged == nil || b.Propagated == nil || b.Propagated.Header != "traceparent" || c.Logged != nil || c.Propagated != nil {
		t.Fatalf("markers: %+v %+v %+v", a, b, c)
	}
	for src, want := range map[string]string{
		"logged logged ambient a: String":                   "logged is given twice",
		`propagated("x") propagated("y") ambient a: String`: "propagated is given twice",
		"propagated ambient a: String":                      "expected '(' after propagated",
		"propagated(header) ambient a: String":              "expected string literal (the header that carries the value)",
		`propagated("x-\$a") ambient a: String`:             "the header name must be a plain string",
		"logged fn f() {}":                                  "expected ambient after the markers logged and propagated",
		`propagated("x") logged secret a: String`:           "expected ambient after the markers",
	} {
		diags := &diag.List{}
		Parse("t.bork", []byte(src), diags)
		if !strings.Contains(diags.Error(), want) {
			t.Errorf("%s: got %q, want %q", src, diags.Error(), want)
		}
	}
}

func TestParseGeneratedConstructors(t *testing.T) {
	diags := &diag.List{}
	file := Parse("t.bork", []byte("fn New = Config.new\nfn Imported = settings.Config.new\n"), diags)
	if diags.Len() != 0 {
		t.Fatal(diags.Error())
	}
	if len(file.Funcs) != 2 || file.Funcs[0].Constructor.Name != "Config" || file.Funcs[1].Constructor.Name != "settings.Config" || file.Funcs[0].Body != nil || len(file.Funcs[0].Params) != 0 {
		t.Fatalf("unexpected declarations: %+v", file.Funcs)
	}
	for _, source := range []string{
		"pred New = Config.new",
		"fn (c: Config) New = Config.new",
		"class Make[T] { fn New = Config.new }",
		"instance MakeInt: Make[Int] { fn New = Config.new }",
		"fn New = Config.copy",
		"fn New = Config.new()",
		"fn New[T] = Config.new",
	} {
		diags := &diag.List{}
		Parse("t.bork", []byte(source+"\n"), diags)
		if diags.Len() == 0 {
			t.Errorf("accepted invalid generated declaration: %s", source)
		}
	}
}

func TestComptimeContextualBlock(t *testing.T) {
	diags := &diag.List{}
	file := Parse("test.bork", []byte("fn comptime(n:Int):Int{n}\nfn f():Int{comptime{comptime(1)}}"), diags)
	if diags.Len() != 0 {
		t.Fatal(diags.Error())
	}
	block, ok := file.Funcs[1].Body.Tail.(*Comptime)
	if !ok {
		t.Fatalf("expected comptime block, got %T", file.Funcs[1].Body.Tail)
	}
	if _, ok := block.Body.Tail.(*Call); !ok {
		t.Fatalf("expected ordinary call, got %T", block.Body.Tail)
	}
}
