package syntax

import (
	"errors"
	goparser "go/parser"
	goscanner "go/scanner"
	gotoken "go/token"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
)

// Parse parses one source file. Syntax errors are added to diags; the
// returned file holds whatever could be parsed.
func Parse(path string, src []byte, diags *diag.List) *File {
	toks, comments := Lex(path, src, diags)
	p := &parser{toks: toks, diags: diags}
	f := &File{Path: path, Comments: comments}
	for {
		p.skipSemis()
		if p.at(EOF) {
			break
		}
		switch {
		case p.at(KwFn):
			if fn := p.funcDecl(); fn != nil {
				f.Funcs = append(f.Funcs, fn)
			}
		case p.at(KwType):
			if td := p.typeDecl(); td != nil {
				f.Types = append(f.Types, td)
			}
		default:
			p.errorf(p.tok().Pos, "expected a declaration ('fn' or 'type'), found %s", p.tok().Kind)
			p.syncTopLevel()
		}
	}
	return f
}

// bailout aborts parsing of the current declaration after an error.
type bailout struct{}

type parser struct {
	toks  []Token
	i     int
	diags *diag.List
}

func (p *parser) tok() Token     { return p.toks[p.i] }
func (p *parser) at(k Kind) bool { return p.toks[p.i].Kind == k }
func (p *parser) peekKind() Kind { return p.toks[min(p.i+1, len(p.toks)-1)].Kind }
func (p *parser) next() Token    { t := p.toks[p.i]; p.i = min(p.i+1, len(p.toks)-1); return t }
func (p *parser) skipSemis() {
	for p.at(Semi) {
		p.next()
	}
}
func (p *parser) errorf(pos diag.Pos, format string, args ...any) {
	p.diags.Add(pos, format, args...)
}

// skipNewlines skips newline-terminators, which are meaningless inside
// parentheses (e.g. arguments split over several lines).
func (p *parser) skipNewlines() {
	for p.at(Semi) && p.tok().Text == "\n" {
		p.next()
	}
}

func (p *parser) expect(k Kind, what string) Token {
	if !p.at(k) {
		p.errorf(p.tok().Pos, "expected %s %s, found %s", k, what, p.tok().Kind)
		panic(bailout{})
	}
	return p.next()
}

// syncTopLevel skips ahead to the next declaration at the start of a line.
func (p *parser) syncTopLevel() {
	for !p.at(EOF) {
		if (p.at(KwFn) || p.at(KwType)) && (p.i == 0 || p.toks[p.i-1].Kind == Semi) {
			return
		}
		p.next()
	}
}

// recoverDecl handles a bailout inside a declaration: skip to the next
// declaration and report that nothing was parsed.
func (p *parser) recoverDecl(failed func()) {
	if r := recover(); r != nil {
		if _, ok := r.(bailout); !ok {
			panic(r)
		}
		failed()
		p.next()
		p.syncTopLevel()
	}
}

// list parses items separated by commas or newlines, up to the closing
// token, which it consumes. A trailing comma is allowed.
func (p *parser) list(closing Kind, what string, item func()) {
	p.skipSemis()
	for !p.at(closing) {
		item()
		sep := false
		if p.at(Comma) {
			p.next()
			sep = true
		}
		if p.at(Semi) {
			p.skipSemis()
			sep = true
		}
		if !sep && !p.at(closing) {
			p.errorf(p.tok().Pos, "expected ',', a newline, or %s after %s, found %s", closing, what, p.tok().Kind)
			panic(bailout{})
		}
	}
	p.next()
}

func (p *parser) typeDecl() (td *TypeDecl) {
	defer p.recoverDecl(func() { td = nil })
	pos := p.expect(KwType, "").Pos
	name := p.expect(TIdent, "(type name)")
	td = &TypeDecl{Pos: pos, Name: name.Text}
	p.expect(Assign, "after the type name")
	switch {
	case p.at(KwSealed):
		td.Kind = SealedType
		p.next()
		p.expect(LBrace, "to start the list of variants")
		p.list(RBrace, "a variant", func() {
			vname := p.expect(TIdent, "(variant name)")
			v := &VariantDecl{Pos: vname.Pos, Name: vname.Text}
			if p.at(LBrace) {
				v.Fields = p.fieldDecls()
			}
			td.Variants = append(td.Variants, v)
		})
	case p.at(LBrace):
		td.Kind = RecordType
		td.Fields = p.fieldDecls()
	default:
		td.Kind = AliasType
		td.Alias = p.typeExpr()
	}
	if !p.at(Semi) && !p.at(EOF) {
		p.errorf(p.tok().Pos, "expected end of line after type declaration, found %s", p.tok().Kind)
		panic(bailout{})
	}
	return td
}

// fieldDecls parses `{ name: Type, ... }`.
func (p *parser) fieldDecls() []*FieldDecl {
	var fields []*FieldDecl
	p.expect(LBrace, "to start the fields")
	p.list(RBrace, "a field", func() {
		fname := p.expect(TIdent, "(field name)")
		p.expect(Colon, "after field name")
		fields = append(fields, &FieldDecl{Pos: fname.Pos, Name: fname.Text, Type: p.typeExpr()})
	})
	return fields
}

func (p *parser) funcDecl() (fn *FuncDecl) {
	defer p.recoverDecl(func() { fn = nil })
	pos := p.expect(KwFn, "").Pos
	name := p.expect(TIdent, "(function name)")
	fn = &FuncDecl{Pos: pos, Name: name.Text}
	p.expect(LParen, "to start the parameter list")
	p.skipNewlines()
	for !p.at(RParen) {
		pname := p.expect(TIdent, "(parameter name)")
		p.expect(Colon, "after parameter name")
		fn.Params = append(fn.Params, &Param{Pos: pname.Pos, Name: pname.Text, Type: p.typeExpr()})
		p.skipNewlines()
		if !p.at(Comma) {
			break
		}
		p.next()
		p.skipNewlines()
	}
	p.expect(RParen, "to end the parameter list")
	if p.at(Colon) {
		p.next()
		fn.Result = p.typeExpr()
	}
	if p.at(KwUnsafe) {
		p.next()
		t := p.expect(TGoCode, "after 'unsafe' (write `unsafe go { ... }`)")
		fn.GoBody = p.goCode(t)
	} else {
		fn.Body = p.block()
	}
	if !p.at(Semi) && !p.at(EOF) {
		p.errorf(p.tok().Pos, "expected end of line after function body, found %s", p.tok().Kind)
		panic(bailout{})
	}
	return fn
}

// typeExpr parses a type: `Name`, `Name[Args]`, or a union `A | B`.
func (p *parser) typeExpr() *TypeExpr {
	first := p.typeAtom()
	if !p.at(Pipe) {
		return first
	}
	u := &TypeExpr{Pos: first.Pos, Union: []*TypeExpr{first}}
	for p.at(Pipe) {
		p.next()
		p.skipNewlines()
		u.Union = append(u.Union, p.typeAtom())
	}
	return u
}

func (p *parser) typeAtom() *TypeExpr {
	if p.at(LParen) {
		p.next()
		t := p.typeExpr()
		p.expect(RParen, "to close the type")
		return t
	}
	t := p.expect(TIdent, "(type name)")
	te := &TypeExpr{Pos: t.Pos, Name: t.Text}
	if p.at(LBrack) {
		p.next()
		for {
			te.Args = append(te.Args, p.typeExpr())
			if !p.at(Comma) {
				break
			}
			p.next()
		}
		p.expect(RBrack, "to close the type arguments")
	}
	return te
}

func (p *parser) block() *Block {
	b := &Block{Pos: p.expect(LBrace, "to start a block").Pos}
	for {
		p.skipSemis()
		if p.at(RBrace) {
			p.next()
			return b
		}
		if p.at(EOF) {
			p.errorf(b.Pos, "block is not closed (missing '}')")
			panic(bailout{})
		}
		var stmt Stmt
		switch {
		case p.at(TIdent) && p.peekKind() == Assign:
			name := p.next()
			p.next() // '='
			stmt = &Binding{Pos: name.Pos, Name: name.Text, Value: p.expr()}
		case p.at(TIdent) && p.peekKind() == Colon:
			name := p.next()
			p.next() // ':'
			typ := p.typeExpr()
			p.expect(Assign, "after the binding's type")
			stmt = &Binding{Pos: name.Pos, Name: name.Text, Type: typ, Value: p.expr()}
		default:
			stmt = &ExprStmt{X: p.expr()}
		}
		if p.at(RBrace) {
			p.next()
			if es, ok := stmt.(*ExprStmt); ok {
				b.Tail = es.X
			} else {
				b.Stmts = append(b.Stmts, stmt)
			}
			return b
		}
		if !p.at(Semi) {
			p.errorf(p.tok().Pos, "expected end of line or '}', found %s", p.tok().Kind)
			panic(bailout{})
		}
		b.Stmts = append(b.Stmts, stmt)
		// A statement followed only by newlines and '}' is the tail.
		save := p.i
		p.skipSemis()
		if p.at(RBrace) {
			if es, ok := stmt.(*ExprStmt); ok {
				b.Stmts = b.Stmts[:len(b.Stmts)-1]
				b.Tail = es.X
			}
			p.next()
			return b
		}
		p.i = save
	}
}

// Binary operator precedence, lowest first.
var precedence = map[Kind]int{
	OrOr:   1,
	AndAnd: 2,
	Eq:     3, NotEq: 3, Lt: 3, LtEq: 3, Gt: 3, GtEq: 3,
	Plus: 4, Minus: 4,
	Star: 5, Slash: 5, Pct: 5,
}

func (p *parser) expr() Expr { return p.binary(1) }

func (p *parser) binary(minPrec int) Expr {
	x := p.unary()
	for {
		op := p.tok()
		prec, ok := precedence[op.Kind]
		if !ok || prec < minPrec {
			return x
		}
		p.next()
		p.skipNewlines() // an operator at the end of a line continues the expression
		y := p.binary(prec + 1)
		x = &Binary{Pos: op.Pos, Op: op.Kind, X: x, Y: y}
	}
}

func (p *parser) unary() Expr {
	if p.at(Minus) || p.at(Not) {
		op := p.next()
		return &Unary{Pos: op.Pos, Op: op.Kind, X: p.unary()}
	}
	return p.postfix(p.primary())
}

func (p *parser) postfix(x Expr) Expr {
	for {
		switch {
		case p.at(LParen):
			call := &Call{Pos: p.next().Pos, Fun: x}
			p.skipNewlines()
			for !p.at(RParen) {
				call.Args = append(call.Args, p.expr())
				p.skipNewlines()
				if !p.at(Comma) {
					break
				}
				p.next()
				p.skipNewlines()
			}
			p.expect(RParen, "to end the argument list")
			x = call
		case p.at(Dot):
			p.next()
			name := p.expect(TIdent, "after '.'")
			if name.Text == "copy" && p.at(LParen) {
				x = p.copyExpr(x, name.Pos)
			} else {
				x = &Selector{Pos: name.Pos, X: x, Name: name.Text}
			}
		case p.at(Quest):
			x = &Try{Pos: p.next().Pos, X: x}
		case p.at(LBrace) && isTypePath(x):
			x = p.recordLit(x)
		default:
			return x
		}
	}
}

// isTypePath reports whether x could name a record type or variant
// (`User`, `Shape.Circle`), so a following '{' starts a record literal.
func isTypePath(x Expr) bool {
	switch x := x.(type) {
	case *Ident:
		return true
	case *Selector:
		_, ok := x.X.(*Ident)
		return ok
	}
	return false
}

func (p *parser) recordLit(typ Expr) Expr {
	lit := &RecordLit{Type: typ}
	p.expect(LBrace, "")
	p.list(RBrace, "a field", func() {
		fname := p.expect(TIdent, "(field name)")
		p.expect(Colon, "after field name")
		p.skipNewlines()
		lit.Fields = append(lit.Fields, &FieldInit{Pos: fname.Pos, Name: fname.Text, Value: p.expr()})
	})
	return lit
}

func (p *parser) copyExpr(x Expr, pos diag.Pos) Expr {
	c := &Copy{Pos: pos, X: x}
	p.expect(LParen, "")
	p.list(RParen, "a field update", func() {
		first := p.expect(TIdent, "(field name)")
		u := &CopyUpdate{Pos: first.Pos, Path: []string{first.Text}}
		for p.at(Dot) {
			p.next()
			u.Path = append(u.Path, p.expect(TIdent, "(field name)").Text)
		}
		p.expect(Assign, "after the field path (write `field = value`)")
		p.skipNewlines()
		u.Value = p.expr()
		c.Updates = append(c.Updates, u)
	})
	return c
}

func (p *parser) matchExpr() Expr {
	m := &Match{Pos: p.next().Pos}
	p.expect(LParen, "after 'match' (write 'match (value) { ... }')")
	p.skipNewlines()
	m.X = p.expr()
	p.skipNewlines()
	p.expect(RParen, "to close the matched value")
	p.expect(LBrace, "to start the match arms")
	p.list(RBrace, "a match arm", func() {
		pat := p.pattern()
		p.expect(Arrow, "after the pattern")
		p.skipNewlines()
		m.Arms = append(m.Arms, &Arm{Pattern: pat, Body: p.expr()})
	})
	return m
}

func (p *parser) pattern() Pattern {
	t := p.tok()
	switch t.Kind {
	case Underscore:
		p.next()
		return &WildcardPat{Pos: t.Pos}
	case TInt, TFloat, TRune, TString, KwTrue, KwFalse, Minus:
		return &LitPat{Pos: t.Pos, Value: p.unary()}
	case TIdent:
		if p.peekKind() == Colon {
			p.next()
			p.next()
			return &TypePat{Pos: t.Pos, Name: t.Text, Type: p.typeExpr()}
		}
		vp := &VariantPat{Pos: t.Pos, Path: []string{p.next().Text}}
		for p.at(Dot) {
			p.next()
			vp.Path = append(vp.Path, p.expect(TIdent, "(variant name)").Text)
		}
		if p.at(LBrace) {
			p.next()
			p.list(RBrace, "a field pattern", func() {
				f := p.expect(TIdent, "(field name)")
				fp := &FieldPat{Pos: f.Pos, Field: f.Text, Bind: f.Text}
				if p.at(Colon) {
					p.next()
					fp.Bind = p.expect(TIdent, "(name to bind)").Text
				}
				vp.Fields = append(vp.Fields, fp)
			})
		}
		return vp
	}
	p.errorf(t.Pos, "expected a pattern, found %s", t.Kind)
	panic(bailout{})
}

func (p *parser) primary() Expr {
	t := p.tok()
	switch t.Kind {
	case TInt:
		p.next()
		return &IntLit{Pos: t.Pos, Text: t.Text}
	case TFloat:
		p.next()
		return &FloatLit{Pos: t.Pos, Text: t.Text}
	case TRune:
		p.next()
		return &RuneLit{Pos: t.Pos, Text: t.Text}
	case TString:
		p.next()
		v, err := strconv.Unquote(t.Text)
		if err != nil {
			p.errorf(t.Pos, "invalid string literal %s", t.Text)
		}
		return &StringLit{Pos: t.Pos, Value: v}
	case KwTrue, KwFalse:
		p.next()
		return &BoolLit{Pos: t.Pos, Value: t.Kind == KwTrue}
	case TIdent:
		p.next()
		return &Ident{Pos: t.Pos, Name: t.Text}
	case LParen:
		p.next()
		p.skipNewlines()
		x := p.expr()
		p.skipNewlines()
		p.expect(RParen, "to close the parenthesis")
		return x
	case LBrace:
		return p.block()
	case KwIf:
		return p.ifExpr()
	case KwMatch:
		return p.matchExpr()
	case KwReturn:
		p.next()
		r := &Return{Pos: t.Pos}
		if !p.at(Semi) && !p.at(RBrace) && !p.at(RParen) && !p.at(EOF) {
			r.Value = p.expr()
		}
		return r
	}
	p.errorf(t.Pos, "expected an expression, found %s", t.Kind)
	panic(bailout{})
}

func (p *parser) ifExpr() Expr {
	e := &If{Pos: p.next().Pos}
	p.expect(LParen, "after 'if' (conditions are written as 'if (cond)')")
	p.skipNewlines()
	e.Cond = p.expr()
	p.skipNewlines()
	p.expect(RParen, "to close the condition")
	e.Then = p.block()
	if p.at(Semi) && p.tok().Text == "\n" && p.peekKind() == KwElse {
		p.errorf(p.toks[p.i+1].Pos, "'else' must be on the same line as the closing '}' of the if-block")
		panic(bailout{})
	}
	if p.at(KwElse) {
		p.next()
		if p.at(KwIf) {
			e.Else = p.ifExpr()
		} else {
			e.Else = p.block()
		}
	}
	return e
}

// goCode splits an `unsafe go { ... }` body into its leading
// `import "path"` lines and the Go statements, and checks that the
// statements are valid Go syntax. Syntax errors are reported at their
// bork positions; Go type errors are reported later, by the Go
// compiler (the generated code maps positions back to bork files).
func (p *parser) goCode(t Token) *GoCode {
	gc := &GoCode{Pos: t.Pos}
	lines := strings.Split(t.Text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		rest, ok := strings.CutPrefix(trimmed, "import ")
		if !ok {
			break
		}
		path, err := strconv.Unquote(strings.TrimSpace(rest))
		if err != nil {
			p.errorf(p.goPos(t.Pos, i, 0), "expected `import \"path\"` in unsafe go block")
		} else {
			gc.Imports = append(gc.Imports, path)
		}
		lines[i] = ""
	}
	gc.Body = strings.Join(lines, "\n")

	const prefix = "package p\n\nfunc _() {"
	fset := gotoken.NewFileSet()
	_, err := goparser.ParseFile(fset, "", prefix+gc.Body+"}\n", 0)
	var list goscanner.ErrorList
	if errors.As(err, &list) && len(list) > 0 {
		// Only the first error: later ones tend to follow from it.
		e := list[0]
		line, col := e.Pos.Line-3, e.Pos.Column
		if line == 0 {
			col -= len("func _() {")
		}
		p.errorf(p.goPos(t.Pos, line, col-1), "in unsafe go block: %s", e.Msg)
	}
	return gc
}

// goPos is the bork position of a place in a Go code token: line lines
// after the '{' at start, and col columns into that line (or after the
// '{', on its line).
func (p *parser) goPos(start diag.Pos, line, col int) diag.Pos {
	if line == 0 {
		return diag.Pos{File: start.File, Line: start.Line, Col: start.Col + 1 + col}
	}
	return diag.Pos{File: start.File, Line: start.Line + line, Col: col + 1}
}
