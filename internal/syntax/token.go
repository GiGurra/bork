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
	TString

	// Keywords
	KwFn
	KwIf
	KwElse
	KwReturn
	KwTrue
	KwFalse

	// Delimiters
	LParen
	RParen
	LBrace
	RBrace
	Comma
	Colon

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
)

var kindNames = map[Kind]string{
	EOF: "end of file", Illegal: "illegal token", Semi: "newline or ';'",
	TIdent: "identifier", TInt: "integer literal", TString: "string literal",
	KwFn: "'fn'", KwIf: "'if'", KwElse: "'else'", KwReturn: "'return'", KwTrue: "'true'", KwFalse: "'false'",
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
}

// Token is a lexed token. Text holds the source text for identifiers
// and literals.
type Token struct {
	Kind Kind
	Text string
	Pos  diag.Pos
}

// Comment is a source comment, kept for future tooling (formatter,
// editor support). The parser does not use comments.
type Comment struct {
	Text string
	Pos  diag.Pos
}
