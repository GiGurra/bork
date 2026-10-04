// Package format normalizes bork source whitespace without changing its layout.
package format

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

type item struct {
	kind       syntax.Kind
	text       string
	start, end int
	line       int
	comment    bool
	unary      bool
	contextDot bool
	chain      bool
	loopIn     bool
	// with is set for the word with where it may start a with block
	// (not after a '.'), which is spaced from its '('.
	with bool
}

type delimiter struct {
	line  int
	chain bool
}

// Source formats one file. Existing line breaks are retained, blank-line runs
// become one blank line, and nested delimiters use two spaces. Comments, literals,
// interpolations and unsafe Go bodies retain their exact text, except CRLF line
// comment endings become LF. Lexically invalid input is rejected; incomplete
// or ill-typed programs, including reserved compiler identifiers, can still be formatted.
func Source(path string, src []byte) ([]byte, error) {
	d := &diag.List{}
	tokens, comments := syntax.LexCompiler(path, src, d)
	if d.Len() != 0 {
		return nil, fmt.Errorf("%s", d.Error())
	}
	lines := []int{0}
	for i, b := range src {
		if b == '\n' {
			lines = append(lines, i+1)
		}
	}
	offset := func(p diag.Pos) int { return lines[p.Line-1] + p.Col - 1 }
	var items []item
	var prev syntax.Kind
	for _, t := range tokens {
		if t.Kind == syntax.EOF || t.Kind == syntax.Semi && t.Text != ";" {
			if t.Kind == syntax.Semi {
				prev = syntax.Semi
			}
			continue
		}
		text := t.Text
		// Operators have no text and print as their name; an empty Go body has
		// none either, and prints as "{}" below.
		if text == "" && t.Kind != syntax.TGoCode {
			text = strings.Trim(t.Kind.String(), "'")
		}
		if t.Kind == syntax.TInterp {
			text = string(src[offset(t.Pos):offset(t.End)])
		}
		if t.Kind == syntax.TGoCode {
			text = "{" + text + "}"
		}
		start := offset(t.Pos)
		u := t.Kind == syntax.Not || t.Kind == syntax.Minus && !endsExpr(prev)
		loopIn := t.Kind == syntax.TIdent && t.Text == "in" && len(items) >= 3 && items[len(items)-1].kind == syntax.TIdent && items[len(items)-2].kind == syntax.LParen && items[len(items)-3].kind == syntax.KwFor
		w := t.Kind == syntax.TIdent && t.Text == "with" && prev != syntax.Dot && prev != syntax.KwFn && prev != syntax.RParen
		chain := t.Kind == syntax.Dot && endsExpr(prev) || prev == syntax.Dot && len(items) > 0 && !items[len(items)-1].contextDot
		items = append(items, item{chain: chain, loopIn: loopIn, kind: t.Kind, text: text, start: start, end: offset(t.End), line: t.Pos.Line, unary: u, contextDot: t.Kind == syntax.Dot && !endsExpr(prev), with: w})
		prev = t.Kind
	}
	for _, c := range comments {
		start := offset(c.Pos)
		text := c.Text
		if strings.HasPrefix(text, "//") {
			text = strings.TrimRight(text, "\r")
		}
		items = append(items, item{text: text, start: start, end: start + len(c.Text), comment: true})
	}
	slices.SortFunc(items, func(a, b item) int { return a.start - b.start })
	var out strings.Builder
	var delimiters []delimiter
	chainLine := false
	for i, it := range items {
		gapStart := 0
		if i > 0 {
			gapStart = items[i-1].end
		}
		gap := src[gapStart:it.start]
		newlines := bytes.Count(gap, []byte{'\n'})
		if i == 0 || newlines > 0 {
			if i > 0 {
				out.WriteByte('\n')
				if newlines > 1 {
					out.WriteByte('\n')
				}
			}
			remaining := len(delimiters)
			// All leading closing delimiters dedent the line, including }).
			for j := i; j < len(items) && closing(items[j].kind) && !items[j].comment; j++ {
				remaining = max(0, remaining-1)
				if j+1 < len(items) && bytes.Contains(src[items[j].end:items[j+1].start], []byte{'\n'}) {
					break
				}
			}
			chainLine = !it.comment && it.chain
			if remaining < len(delimiters) {
				chainLine = chainLine || delimiters[remaining].chain
				// Split closers share the opening line's indentation baseline.
				if delimiters[remaining].chain {
					line := delimiters[remaining].line
					for remaining > 0 && delimiters[remaining-1].line == line {
						remaining--
					}
				}
			}
			indent := levels(delimiters[:remaining])
			if chainLine || !it.comment && it.kind == syntax.PipeGt {
				indent++
			}
			out.WriteString(strings.Repeat("  ", max(0, indent)))
		} else if space(items[i-1], it) || joinsTokens(items[i-1], it) {
			out.WriteByte(' ')
		}
		// The lexer consumes the word "go" as part of TGoCode.
		if it.kind == syntax.TGoCode && !it.comment {
			out.WriteString("go ")
		}
		out.WriteString(it.text)
		if !it.comment {
			switch it.kind {
			case syntax.LBrace, syntax.LParen, syntax.LBrack:
				delimiters = append(delimiters, delimiter{line: it.line, chain: chainLine})
			case syntax.RBrace, syntax.RParen, syntax.RBrack:
				delimiters = delimiters[:max(0, len(delimiters)-1)]
			}
		}
	}
	if len(items) > 0 {
		out.WriteByte('\n')
	}
	return []byte(out.String()), nil
}

// Delimiters opened on the same line contribute one indentation level, so a
// lambda or match inside a call has the same indentation as an ordinary block.
func levels(delimiters []delimiter) int {
	n := 0
	for i, d := range delimiters {
		if i == 0 || d.line != delimiters[i-1].line {
			n++
			if d.chain {
				n++
			}
		}
	}
	return n
}

// Incomplete programs can contain adjacent punctuation that would become a
// different token if joined. A dot followed by a number also needs a boundary
// because the preceding token could be a number (1 . 2 must not become 1.2).
func joinsTokens(a, b item) bool {
	if a.kind == syntax.Dot && (b.kind == syntax.Dot || b.kind == syntax.TInt || b.kind == syntax.TFloat) {
		return true
	}
	d := &diag.List{}
	left := a.text
	if a.kind == syntax.TInterp && strings.HasPrefix(left, `"`) {
		left = "s" + left // a custom prefix is in the preceding formatter item
	}
	ts, _ := syntax.Lex("", []byte(left+b.text), d)
	var kinds []syntax.Kind
	for _, t := range ts {
		if t.Kind != syntax.EOF && (t.Kind != syntax.Semi || t.Text == ";") {
			kinds = append(kinds, t.Kind)
		}
	}
	return d.Len() != 0 || len(kinds) != 2 || kinds[0] != a.kind || kinds[1] != b.kind
}

func closing(k syntax.Kind) bool {
	return k == syntax.RBrace || k == syntax.RParen || k == syntax.RBrack
}

func endsExpr(k syntax.Kind) bool {
	switch k {
	case syntax.TIdent, syntax.TInt, syntax.TFloat, syntax.TRune, syntax.TString, syntax.TInterp,
		syntax.KwTrue, syntax.KwFalse, syntax.RParen, syntax.RBrack, syntax.RBrace, syntax.Quest:
		return true
	}
	return false
}

func space(a, b item) bool {
	if a.kind == syntax.TIdent && b.kind == syntax.TInterp && a.end == b.start {
		return false
	}
	if a.comment || b.comment {
		return true
	}
	if a.kind == syntax.Semi {
		return true
	}
	if a.kind == syntax.LParen || a.kind == syntax.LBrack || a.kind == syntax.Not || a.unary {
		return false
	}
	switch b.kind {
	case syntax.Dot:
		return b.contextDot && a.kind != syntax.Dot
	case syntax.Comma, syntax.Colon, syntax.Quest, syntax.Semi, syntax.RParen, syntax.RBrack:
		return false
	case syntax.LParen:
		if a.kind == syntax.TIdent && a.text == "derive" || a.with {
			return true
		}
		return a.kind != syntax.TIdent && a.kind != syntax.RParen && a.kind != syntax.RBrack
	case syntax.LBrack:
		if a.kind == syntax.KwGenerate {
			return false
		}
		if a.loopIn {
			return true
		}
		return a.kind != syntax.TIdent && a.kind != syntax.RBrack
	case syntax.RBrace:
		return a.kind != syntax.LBrace && a.kind != syntax.Colon
	}
	switch a.kind {
	case syntax.Dot, syntax.LParen, syntax.LBrack, syntax.Ellipsis:
		return false
	case syntax.LBrace:
		return b.kind != syntax.Colon
	case syntax.Not, syntax.Minus:
		return !a.unary
	}
	return true
}
