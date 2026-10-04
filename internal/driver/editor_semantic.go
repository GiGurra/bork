package driver

import (
	"slices"

	"github.com/GiGurra/bork/internal/check"
)

// SemanticTokens returns owned classifications from this checked snapshot.
// The per-file index is retained for repeat requests; no facts are evaluated.
func (a *EditorAnalysis) SemanticTokens(path string) []check.SemanticToken {
	if tokens, ok := a.semantic[path]; ok {
		return slices.Clone(tokens)
	}
	file, _ := a.editorFile(path)
	if file == nil {
		return nil
	}
	tokens := check.SemanticTokens(file, a.program.info)
	if a.semantic == nil {
		a.semantic = map[string][]check.SemanticToken{}
	}
	a.semantic[path] = tokens
	return slices.Clone(tokens)
}

// LexicalSemanticTokens classifies current source through the compiler lexer
// when no matching successful snapshot is available.
func LexicalSemanticTokens(path, source string) []check.SemanticToken {
	return check.LexicalSemanticTokens(path, source)
}
