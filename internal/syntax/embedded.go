package syntax

import (
	"crypto/sha256"
	"slices"
	"sync"

	"github.com/GiGurra/bork/internal/diag"
)

type sourceKey struct {
	path    string
	content [sha256.Size]byte
}

type lexedSource struct {
	text     string
	tokens   []Token
	comments []Comment
}

var embeddedSources sync.Map // sourceKey -> *lexedSource

// ParseEmbedded reuses the immutable tokens of embedded prelude/standard sources.
// Every call builds a fresh syntax tree: the checker expands calls in that tree.
// User files use Parse, so edits do not grow this bounded compiler-source cache.
func ParseEmbedded(path string, src []byte, diags *diag.List) *File {
	key := sourceKey{path: path, content: sha256.Sum256(src)}
	var source *lexedSource
	if cached, ok := embeddedSources.Load(key); ok {
		source = cached.(*lexedSource)
	} else {
		before := diags.Len()
		tokens, comments := LexCompiler(path, src, diags)
		source = &lexedSource{text: string(src), tokens: tokens, comments: comments}
		// Replay lexing failures normally rather than caching their diagnostics.
		if diags.Len() == before {
			cached, _ := embeddedSources.LoadOrStore(key, source)
			source = cached.(*lexedSource)
		}
	}
	// Comments escape through File and may be changed by callers. Tokens remain
	// private to the parser, which only reads them.
	return parse(path, source.text, source.tokens, slices.Clone(source.comments), diags, true)
}
