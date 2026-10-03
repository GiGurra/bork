// Package syntax contains bork's lexer, syntax tree, and parser.
package syntax

import "github.com/GiGurra/bork/internal/diag"

// Kind is the kind of a token.
type Kind int

const (
	EOF Kind = iota
	Illegal
	Semi // explicit ';' or a newline that ends a statement

	TIdent
	TInt
	TFloat
	TRune
	TString
	TInterp // s"...": the text from the opening quote to the closing one
	TGoCode // the raw Go inside `unsafe go { ... }`

	// Keywords
	KwFn
	KwIf
	KwElse
	KwReturn
	KwTrue
	KwFalse
	KwType
	KwSealed
	KwMatch
	KwUnsafe
	KwPred
	KwWhere
	KwAnd
	KwTrust
	KwRule
	KwOr

	// Delimiters
	LParen
	RParen
	LBrace
	RBrace
	LBrack
	RBrack
	Comma
	Colon
	Dot
	Ellipsis   // ... in a list pattern
	Underscore // the wildcard pattern _

	// Operators
	Assign // =
	Plus   // +
	Minus  // -
	Star   // *
	Slash  // /
	Pct    // %
	Not    // !
	AndAnd // &&
	OrOr   // ||
	Eq     // ==
	NotEq  // !=
	Lt     // <
	LtEq   // <=
	Gt     // >
	GtEq   // >=
	Pipe   // |
	PipeGt // |>
	Arrow  // =>
	Quest  // ?
)

var kindNames = map[Kind]string{
	EOF: "end of file", Illegal: "illegal token", Semi: "newline or ';'",
	TIdent: "identifier", TInt: "integer literal", TFloat: "float literal", TRune: "rune literal", TGoCode: "Go code", TString: "string literal", TInterp: "interpolated string",
	KwFn: "'fn'", KwIf: "'if'", KwElse: "'else'", KwReturn: "'return'", KwTrue: "'true'", KwFalse: "'false'",
	KwType: "'type'", KwSealed: "'sealed'", KwMatch: "'match'", KwUnsafe: "'unsafe'", KwPred: "'pred'", KwWhere: "'where'", KwAnd: "'and'", KwTrust: "'trust'", KwRule: "'rule'", KwOr: "'or'",
	LBrack: "'['", RBrack: "']'", Dot: "'.'", Ellipsis: "'...'", Underscore: "'_'", Pipe: "'|'", PipeGt: "'|>'", Arrow: "'=>'", Quest: "'?'",
	LParen: "'('", RParen: "')'", LBrace: "'{'", RBrace: "'}'", Comma: "','", Colon: "':'",
	Assign: "'='", Plus: "'+'", Minus: "'-'", Star: "'*'", Slash: "'/'", Pct: "'%'", Not: "'!'",
	AndAnd: "'&&'", OrOr: "'||'", Eq: "'=='", NotEq: "'!='", Lt: "'<'", LtEq: "'<='", Gt: "'>'", GtEq: "'>='",
}

func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return "unknown token"
}

var keywords = map[string]Kind{
	"fn":     KwFn,
	"if":     KwIf,
	"else":   KwElse,
	"return": KwReturn,
	"true":   KwTrue,
	"false":  KwFalse,
	"type":   KwType,
	"sealed": KwSealed,
	"match":  KwMatch,
	"unsafe": KwUnsafe,
	"pred":   KwPred,
	"where":  KwWhere,
	"and":    KwAnd,
	"trust":  KwTrust,
	"rule":   KwRule,
	"or":     KwOr,
}

// Token is a lexed token. Text holds the source text for identifiers
// and literals.
type Token struct {
	Kind Kind
	Text string
	Pos  diag.Pos
	End  diag.Pos // position immediately after the token
}

// Comment is a source comment, kept for future tooling (formatter,
// editor support). The parser does not use comments.
type Comment struct {
	Text string
	Pos  diag.Pos
	End  diag.Pos // position immediately after the token
}
