package syntax

import (
	"strconv"

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
		if !p.at(KwFn) {
			p.errorf(p.tok().Pos, "expected a declaration ('fn'), found %s", p.tok().Kind)
			p.syncTopLevel()
			continue
		}
		if fn := p.funcDecl(); fn != nil {
			f.Funcs = append(f.Funcs, fn)
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

// syncTopLevel skips ahead to the next 'fn' at the start of a line.
func (p *parser) syncTopLevel() {
	for !p.at(EOF) {
		if p.at(KwFn) && (p.i == 0 || p.toks[p.i-1].Kind == Semi) {
			return
		}
		p.next()
	}
}

func (p *parser) funcDecl() (fn *FuncDecl) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			fn = nil
			p.next()
			p.syncTopLevel()
		}
	}()
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
	fn.Body = p.block()
	if !p.at(Semi) && !p.at(EOF) {
		p.errorf(p.tok().Pos, "expected end of line after function body, found %s", p.tok().Kind)
		panic(bailout{})
	}
	return fn
}

func (p *parser) typeExpr() *TypeExpr {
	t := p.expect(TIdent, "(type name)")
	return &TypeExpr{Pos: t.Pos, Name: t.Text}
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
		if p.at(TIdent) && p.peekKind() == Assign {
			name := p.next()
			p.next() // '='
			stmt = &Binding{Pos: name.Pos, Name: name.Text, Value: p.expr()}
		} else {
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
	for p.at(LParen) {
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
	}
	return x
}

func (p *parser) primary() Expr {
	t := p.tok()
	switch t.Kind {
	case TInt:
		p.next()
		return &IntLit{Pos: t.Pos, Text: t.Text}
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
