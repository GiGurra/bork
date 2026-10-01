package syntax

import (
	"github.com/GiGurra/bork/internal/diag"
)

// Lex splits src into tokens. Like Go, a newline ends a statement when
// the line's last token could end one (an identifier, a literal,
// `return`, `)`, or `}`); the lexer then emits a Semi token. Comments are
// returned separately.
func Lex(file string, src []byte, diags *diag.List) ([]Token, []Comment) {
	lx := &lexer{file: file, src: src, line: 1, col: 1, diags: diags}
	lx.run()
	return lx.toks, lx.comments
}

type lexer struct {
	file     string
	src      []byte
	off      int
	line     int
	col      int
	toks     []Token
	comments []Comment
	diags    *diag.List
}

func (lx *lexer) pos() diag.Pos { return diag.Pos{File: lx.file, Line: lx.line, Col: lx.col} }

func (lx *lexer) peek(n int) byte {
	if lx.off+n < len(lx.src) {
		return lx.src[lx.off+n]
	}
	return 0
}

func (lx *lexer) advance() byte {
	c := lx.src[lx.off]
	lx.off++
	if c == '\n' {
		lx.line++
		lx.col = 1
	} else {
		lx.col++
	}
	return c
}

func (lx *lexer) emit(k Kind, text string, pos diag.Pos) {
	lx.toks = append(lx.toks, Token{Kind: k, Text: text, Pos: pos})
}

// endsStatement reports whether a newline after the last emitted token
// should terminate the statement.
func (lx *lexer) endsStatement() bool {
	if len(lx.toks) == 0 {
		return false
	}
	switch lx.toks[len(lx.toks)-1].Kind {
	case TIdent, TInt, TString, KwTrue, KwFalse, KwReturn, RParen, RBrace, RBrack, Quest, Underscore:
		return true
	}
	return false
}

func (lx *lexer) newline(pos diag.Pos) {
	if lx.endsStatement() {
		lx.emit(Semi, "\n", pos)
	}
}

func (lx *lexer) run() {
	for lx.off < len(lx.src) {
		c := lx.peek(0)
		pos := lx.pos()
		switch {
		case c == '\n':
			lx.newline(pos)
			lx.advance()
		case c == ' ' || c == '\t' || c == '\r':
			lx.advance()
		case c == '/' && lx.peek(1) == '/':
			lx.lineComment(pos)
		case c == '/' && lx.peek(1) == '*':
			lx.blockComment(pos)
		case isLetter(c):
			lx.ident(pos)
		case isDigit(c):
			lx.number(pos)
		case c == '"':
			lx.string(pos)
		default:
			lx.operator(pos)
		}
	}
	lx.newline(lx.pos())
	lx.emit(EOF, "", lx.pos())
}

func (lx *lexer) lineComment(pos diag.Pos) {
	start := lx.off
	for lx.off < len(lx.src) && lx.peek(0) != '\n' {
		lx.advance()
	}
	lx.comments = append(lx.comments, Comment{Text: string(lx.src[start:lx.off]), Pos: pos})
}

func (lx *lexer) blockComment(pos diag.Pos) {
	start := lx.off
	lx.advance()
	lx.advance()
	hasNewline := false
	for {
		if lx.off >= len(lx.src) {
			lx.diags.Add(pos, "comment is not terminated")
			break
		}
		if lx.peek(0) == '*' && lx.peek(1) == '/' {
			lx.advance()
			lx.advance()
			break
		}
		if lx.advance() == '\n' {
			hasNewline = true
		}
	}
	lx.comments = append(lx.comments, Comment{Text: string(lx.src[start:lx.off]), Pos: pos})
	// A multi-line comment acts like a newline, as in Go.
	if hasNewline {
		lx.newline(pos)
	}
}

func (lx *lexer) ident(pos diag.Pos) {
	start := lx.off
	for lx.off < len(lx.src) && (isLetter(lx.peek(0)) || isDigit(lx.peek(0))) {
		lx.advance()
	}
	text := string(lx.src[start:lx.off])
	if k, ok := keywords[text]; ok {
		lx.emit(k, text, pos)
		return
	}
	if text == "_" {
		lx.emit(Underscore, text, pos)
		return
	}
	if text[0] == '_' {
		lx.diags.Add(pos, "identifiers cannot start with '_' (reserved for the compiler)")
	}
	lx.emit(TIdent, text, pos)
}

func (lx *lexer) number(pos diag.Pos) {
	start := lx.off
	for lx.off < len(lx.src) && (isDigit(lx.peek(0)) || lx.peek(0) == '_') {
		lx.advance()
	}
	if lx.off < len(lx.src) && isLetter(lx.peek(0)) {
		lx.diags.Add(lx.pos(), "unexpected character %q in number", lx.peek(0))
		for lx.off < len(lx.src) && (isLetter(lx.peek(0)) || isDigit(lx.peek(0))) {
			lx.advance()
		}
	}
	lx.emit(TInt, string(lx.src[start:lx.off]), pos)
}

func (lx *lexer) string(pos diag.Pos) {
	start := lx.off
	lx.advance() // opening quote
	for {
		if lx.off >= len(lx.src) || lx.peek(0) == '\n' {
			lx.diags.Add(pos, "string literal is not terminated")
			lx.emit(TString, `""`, pos)
			return
		}
		c := lx.advance()
		if c == '\\' && lx.off < len(lx.src) {
			lx.advance()
			continue
		}
		if c == '"' {
			break
		}
	}
	lx.emit(TString, string(lx.src[start:lx.off]), pos)
}

func (lx *lexer) operator(pos diag.Pos) {
	c := lx.advance()
	two := func(next byte, ifTwo, ifOne Kind) {
		if lx.off < len(lx.src) && lx.peek(0) == next {
			lx.advance()
			lx.emit(ifTwo, "", pos)
			return
		}
		lx.emit(ifOne, "", pos)
	}
	switch c {
	case '(':
		lx.emit(LParen, "", pos)
	case ')':
		lx.emit(RParen, "", pos)
	case '{':
		lx.emit(LBrace, "", pos)
	case '}':
		lx.emit(RBrace, "", pos)
	case ',':
		lx.emit(Comma, "", pos)
	case '[':
		lx.emit(LBrack, "", pos)
	case ']':
		lx.emit(RBrack, "", pos)
	case '.':
		lx.emit(Dot, "", pos)
	case '?':
		lx.emit(Quest, "", pos)
	case ':':
		lx.emit(Colon, "", pos)
	case ';':
		lx.emit(Semi, ";", pos)
	case '+':
		lx.emit(Plus, "", pos)
	case '-':
		lx.emit(Minus, "", pos)
	case '*':
		lx.emit(Star, "", pos)
	case '/':
		lx.emit(Slash, "", pos)
	case '%':
		lx.emit(Pct, "", pos)
	case '=':
		if lx.off < len(lx.src) && lx.peek(0) == '>' {
			lx.advance()
			lx.emit(Arrow, "", pos)
			return
		}
		two('=', Eq, Assign)
	case '!':
		two('=', NotEq, Not)
	case '<':
		two('=', LtEq, Lt)
	case '>':
		two('=', GtEq, Gt)
	case '&':
		if lx.off < len(lx.src) && lx.peek(0) == '&' {
			lx.advance()
			lx.emit(AndAnd, "", pos)
			return
		}
		lx.diags.Add(pos, "unexpected character '&' (did you mean '&&'?)")
	case '|':
		if lx.off < len(lx.src) && lx.peek(0) == '|' {
			lx.advance()
			lx.emit(OrOr, "", pos)
			return
		}
		lx.emit(Pipe, "", pos)
	default:
		lx.diags.Add(pos, "unexpected character %q", c)
	}
}

func isLetter(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
