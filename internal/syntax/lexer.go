package syntax

import (
	"strings"

	"github.com/GiGurra/bork/internal/diag"
)

// Lex splits src into tokens. Like Go, a newline ends a statement when
// the line's last token could end one (an identifier, a literal,
// `return`, `)`, or `}`); the lexer then emits a Semi token. Comments are
// returned separately.
func Lex(file string, src []byte, diags *diag.List) ([]Token, []Comment) {
	return lexAt(file, src, 1, 1, diags, false)
}

// lexAt lexes src as if it started at line:col of file (used for the
// expressions inside interpolated strings).
func lexAt(file string, src []byte, line, col int, diags *diag.List, compiler bool) ([]Token, []Comment) {
	lx := &lexer{file: file, src: src, line: line, col: col, diags: diags, compiler: compiler}
	lx.run()
	return lx.toks, lx.comments
}

// LexCompiler permits reserved names in compiler-owned sources and formatting.
func LexCompiler(file string, src []byte, diags *diag.List) ([]Token, []Comment) {
	return lexAt(file, src, 1, 1, diags, true)
}

type lexer struct {
	compiler bool
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
	lx.toks = append(lx.toks, Token{Kind: k, Text: text, Pos: pos, End: lx.pos()})
}

// endsStatement reports whether a newline after the last emitted token
// should terminate the statement.
func (lx *lexer) endsStatement() bool {
	if len(lx.toks) == 0 {
		return false
	}
	switch lx.toks[len(lx.toks)-1].Kind {
	case TIdent, TInt, TFloat, TRune, TString, TInterp, TGoCode, KwTrue, KwFalse, KwReturn, KwBreak, KwContinue, RParen, RBrace, RBrack, Quest, Underscore:
		return true
	}
	return false
}

func (lx *lexer) newline(pos diag.Pos) {
	if lx.endsStatement() && !lx.continuationAhead() {
		lx.emit(Semi, "\n", pos)
	}
}

// continuationAhead recognizes leading pipes and selectors across whitespace
// and comments. A selector needs an existing receiver, not a return/break head.
//
//	users
//	  |> filter(u => u.age >= 18)
func (lx *lexer) continuationAhead() bool {
	i := lx.off
	for i < len(lx.src) {
		switch c := lx.src[i]; {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '/' && i+1 < len(lx.src) && lx.src[i+1] == '/':
			for i < len(lx.src) && lx.src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(lx.src) && lx.src[i+1] == '*':
			i += 2
			for i+1 < len(lx.src) && (lx.src[i] != '*' || lx.src[i+1] != '/') {
				i++
			}
			if i+1 >= len(lx.src) {
				return false
			}
			i += 2
		default:
			if c == '|' && i+1 < len(lx.src) && lx.src[i+1] == '>' {
				return true
			}
			if c != '.' || i+1 >= len(lx.src) || !isLetter(lx.src[i+1]) || len(lx.toks) == 0 {
				return false
			}
			switch lx.toks[len(lx.toks)-1].Kind {
			case KwReturn, KwBreak, KwContinue, Underscore:
				return false
			}
			return true
		}
	}
	return false
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
		case lx.off == 0 && lx.line == 1 && c == '#' && lx.peek(1) == '!':
			lx.lineComment(pos)
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
		case c == '\'':
			lx.runeLit(pos)
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
			lx.diags.AddCode(pos, "syntax.error", "comment is not terminated")
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
	if text == "s" && lx.peek(0) == '"' {
		lx.interp(pos)
		return
	}
	if k, ok := keywords[text]; ok {
		lx.emit(k, text, pos)
		if k == KwUnsafe {
			lx.goCode()
		}
		return
	}
	if text == "_" {
		lx.emit(Underscore, text, pos)
		return
	}
	if text[0] == '_' && !lx.compiler {
		lx.diags.AddCode(pos, "syntax.error", "identifiers cannot start with '_' (reserved for the compiler)")
	}
	lx.emit(TIdent, text, pos)
	if lx.peek(0) == '"' {
		lx.interp(lx.pos())
	}
}

// number lexes an integer (`42`, `1_000`, `0xFF`, `0b1010`, `0o17`) or
// a float (`1.5`, `2e10`, `1.5e-3`). The checker validates the digits.
func (lx *lexer) number(pos diag.Pos) {
	start := lx.off
	kind := TInt
	digits := func() {
		for lx.off < len(lx.src) && (isDigit(lx.peek(0)) || lx.peek(0) == '_') {
			lx.advance()
		}
	}
	if lx.peek(0) == '0' && strings.IndexByte("xXbBoO", lx.peek(1)) >= 0 {
		lx.advance()
		lx.advance()
		for lx.off < len(lx.src) && (isLetter(lx.peek(0)) || isDigit(lx.peek(0))) {
			lx.advance()
		}
	} else {
		digits()
		// A '.' must be followed by a digit, so `5.copy(...)` stays a
		// selector on an Int.
		if lx.peek(0) == '.' && isDigit(lx.peek(1)) {
			kind = TFloat
			lx.advance()
			digits()
		}
		if lx.peek(0) == 'e' || lx.peek(0) == 'E' {
			kind = TFloat
			lx.advance()
			if lx.peek(0) == '+' || lx.peek(0) == '-' {
				lx.advance()
			}
			digits()
		}
	}
	if lx.off < len(lx.src) && isLetter(lx.peek(0)) {
		lx.diags.AddCode(lx.pos(), "syntax.error", "unexpected character %q in number", lx.peek(0))
		for lx.off < len(lx.src) && (isLetter(lx.peek(0)) || isDigit(lx.peek(0))) {
			lx.advance()
		}
	}
	lx.emit(kind, string(lx.src[start:lx.off]), pos)
}

// goCode lexes the raw Go of `unsafe go { ... }`, right after `unsafe`.
// The Go code is not split into bork tokens: it becomes one TGoCode
// token holding the text between the braces, positioned at the '{'.
// Braces inside Go strings, runes, and comments are skipped.
func (lx *lexer) goCode() {
	saveOff, saveLine, saveCol := lx.off, lx.line, lx.col
	skipSpaces := func() {
		for lx.peek(0) == ' ' || lx.peek(0) == '\t' {
			lx.advance()
		}
	}
	skipSpaces()
	if lx.peek(0) != 'g' || lx.peek(1) != 'o' || isLetter(lx.peek(2)) || isDigit(lx.peek(2)) {
		lx.off, lx.line, lx.col = saveOff, saveLine, saveCol
		return
	}
	lx.advance()
	lx.advance()
	skipSpaces()
	if lx.peek(0) != '{' {
		lx.off, lx.line, lx.col = saveOff, saveLine, saveCol
		return
	}
	pos := lx.pos()
	lx.advance()
	start := lx.off
	depth := 1
	// skipTo consumes up to and including the closing delimiter.
	skipTo := func(close byte, escapes, multiline bool) {
		for lx.off < len(lx.src) {
			c := lx.advance()
			switch {
			case c == close:
				return
			case c == '\\' && escapes && lx.off < len(lx.src):
				lx.advance()
			case c == '\n' && !multiline:
				return
			}
		}
	}
	for depth > 0 {
		if lx.off >= len(lx.src) {
			lx.diags.AddCode(pos, "syntax.error", "unsafe go block is not closed (missing '}')")
			return
		}
		c := lx.advance()
		switch {
		case c == '{':
			depth++
		case c == '}':
			depth--
		case c == '"':
			skipTo('"', true, false)
		case c == '\'':
			skipTo('\'', true, false)
		case c == '`':
			skipTo('`', false, true)
		case c == '/' && lx.peek(0) == '/':
			skipTo('\n', false, false)
		case c == '/' && lx.peek(0) == '*':
			lx.advance()
			for lx.off < len(lx.src) && (lx.peek(0) != '*' || lx.peek(1) != '/') {
				lx.advance()
			}
			if lx.off < len(lx.src) {
				lx.advance()
				lx.advance()
			}
		}
	}
	lx.emit(TGoCode, string(lx.src[start:lx.off-1]), pos)
}

func (lx *lexer) string(pos diag.Pos) {
	start := lx.off
	lx.advance() // opening quote
	for {
		if lx.off >= len(lx.src) || lx.peek(0) == '\n' {
			lx.diags.AddCode(pos, "syntax.error", "string literal is not terminated")
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
		if lx.peek(0) == '.' && lx.peek(1) == '.' {
			lx.advance()
			lx.advance()
			lx.emit(Ellipsis, "", pos)
			return
		}
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
		lx.diags.AddCode(pos, "syntax.single-ampersand", "unexpected character '&' (did you mean '&&'?)")
		end := lx.pos()
		lx.diags.Suggest(pos, "syntax.single-ampersand", end, diag.Fix{
			Message: "replace & with &&",
			Edits:   []diag.TextEdit{{Start: pos, End: end, Replacement: "&&"}},
		})
	case '|':
		if lx.off < len(lx.src) && lx.peek(0) == '|' {
			lx.advance()
			lx.emit(OrOr, "", pos)
			return
		}
		if lx.off < len(lx.src) && lx.peek(0) == '>' {
			lx.advance()
			lx.emit(PipeGt, "", pos)
			return
		}
		lx.emit(Pipe, "", pos)
	default:
		lx.diags.AddCode(pos, "syntax.error", "unexpected character %q", c)
	}
}

func isLetter(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// runeLit lexes a rune literal: 'a', '\n', '\u00e5'. The checker
// validates it.
func (lx *lexer) runeLit(pos diag.Pos) {
	start := lx.off
	lx.advance() // opening quote
	for {
		if lx.off >= len(lx.src) || lx.peek(0) == '\n' {
			lx.diags.AddCode(pos, "syntax.error", "rune literal is not terminated")
			lx.emit(TRune, "'?'", pos)
			return
		}
		c := lx.advance()
		if c == '\\' && lx.off < len(lx.src) {
			lx.advance()
			continue
		}
		if c == '\'' {
			break
		}
	}
	lx.emit(TRune, string(lx.src[start:lx.off]), pos)
}

// interp lexes the string part after an adjacent prefix. Inside
// ${...}, quotes start nested string literals, so s"${f("x")}" is one
// token. The parser splits the text into literal parts and expressions.
func (lx *lexer) interp(pos diag.Pos) {
	start := lx.off
	end := quotedEnd(lx.src, start, true)
	if end < 0 {
		for lx.off < len(lx.src) && lx.peek(0) != '\n' {
			lx.advance()
		}
		lx.diags.AddCode(pos, "syntax.error", "string literal is not terminated")
		lx.emit(TInterp, `""`, pos)
		return
	}
	for lx.off < end {
		lx.advance()
	}
	lx.emit(TInterp, string(lx.src[start:end]), pos)
}

// quotedEnd and braceEnd share the rules for nested literals and comments in
// hole expressions. Both return the position after their closing delimiter.
func quotedEnd(src []byte, start int, interpolated bool) int {
	quote := src[start]
	for i := start + 1; i < len(src); i++ {
		switch src[i] {
		case '\n':
			return -1
		case '\\':
			i++
		case quote:
			return i + 1
		case '$':
			if !interpolated || i+1 >= len(src) {
				continue
			}
			switch src[i+1] {
			case '$':
				i++
			case '{':
				end := braceEnd(src, i+2)
				if end < 0 {
					return -1
				}
				i = end - 1
			}
		}
	}
	return -1
}

func braceEnd(src []byte, from int) int {
	depth := 1
	for i := from; i < len(src); i++ {
		switch src[i] {
		case '\n':
			return -1
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		case '\'', '"':
			interpolated := src[i] == '"' && i > 0 && (isLetter(src[i-1]) || isDigit(src[i-1]))
			end := quotedEnd(src, i, interpolated)
			if end < 0 {
				return -1
			}
			i = end - 1
		case '/':
			if i+1 < len(src) && src[i+1] == '/' {
				return -1 // single-line interpolated strings cannot resume after //
			}
			if i+1 < len(src) && src[i+1] == '*' {
				i += 2
				for i+1 < len(src) && (src[i] != '*' || src[i+1] != '/') {
					if src[i] == '\n' {
						return -1
					}
					i++
				}
				if i+1 >= len(src) {
					return -1
				}
				i++
			}
		}
	}
	return -1
}
