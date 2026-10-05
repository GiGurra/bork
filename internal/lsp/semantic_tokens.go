package lsp

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/driver"
)

var semanticTokenTypes = []string{
	"namespace", "type", "typeParameter", "parameter", "variable", "property",
	"enumMember", "function", "method", "keyword", "string", "number", "operator", "comment", "class",
}
var semanticTokenModifiers = []string{
	"declaration", "readonly", "static", "defaultLibrary", "predicate", "effect", "goBinding", "rebinding",
}

type semanticTokensResult struct {
	Data []uint32 `json:"data"`
}

func semanticTokensCapability() any {
	return map[string]any{
		"legend": map[string]any{"tokenTypes": semanticTokenTypes, "tokenModifiers": semanticTokenModifiers},
		"full":   true, "range": true,
	}
}

func (s *server) semanticTokens(path, source string, requested *sourceRange) (semanticTokensResult, error) {
	result := semanticTokensResult{Data: []uint32{}}
	if requested != nil {
		start, err := byteOffset(source, requested.Start)
		if err != nil {
			return result, err
		}
		end, err := byteOffset(source, requested.End)
		if err != nil || end < start {
			return result, fmt.Errorf("invalid semantic token range")
		}
	}
	var tokens []check.SemanticToken
	pkg := s.state(path)
	if pkg != nil && pkg.analysis != nil && !pkg.stale && pkg.analysis.Sources()[path] == source {
		tokens = pkg.analysis.SemanticTokens(path)
	} else {
		tokens = driver.LexicalSemanticTokens(path, source)
	}
	lines := strings.Split(source, "\n")
	previous := position{}
	for _, token := range tokens {
		kind := token.Kind
		predicate, effect := kind == "predicate", kind == "effect"
		if predicate {
			kind = "function"
		}
		if effect {
			kind = "keyword"
		}
		typeIndex := slices.Index(semanticTokenTypes, kind)
		if typeIndex < 0 {
			continue
		}
		var modifiers uint32
		for i, enabled := range []bool{token.Declaration, token.Readonly, token.Static, token.Builtin, predicate, effect, token.GoBinding, token.Rebinding} {
			if enabled {
				modifiers |= 1 << i
			}
		}
		// Split multiline tokens for clients without multiline-token support.
		for line := token.Start.Line; line <= token.End.Line && line <= len(lines); line++ {
			if line < 1 {
				continue
			}
			text := strings.TrimSuffix(lines[line-1], "\r")
			start, end := 0, len(text)
			if line == token.Start.Line {
				start = token.Start.Col - 1
			}
			if line == token.End.Line {
				end = min(end, token.End.Col-1)
			}
			if start < 0 || start > len(text) || end > len(text) || end <= start {
				continue
			}
			from := position{Line: line - 1, Character: semanticUTF16Width(text[:start])}
			to := position{Line: line - 1, Character: from.Character + semanticUTF16Width(text[start:end])}
			if requested != nil {
				if from.Line < requested.Start.Line || from.Line > requested.End.Line {
					continue
				}
				if from.Line == requested.Start.Line {
					from.Character = max(from.Character, requested.Start.Character)
				}
				if to.Line == requested.End.Line {
					to.Character = min(to.Character, requested.End.Character)
				}
			}
			length := to.Character - from.Character
			if length <= 0 {
				continue
			}
			deltaLine, deltaStart := from.Line-previous.Line, from.Character
			if deltaLine == 0 {
				deltaStart -= previous.Character
			}
			if deltaLine < 0 || deltaStart < 0 {
				return result, fmt.Errorf("compiler semantic tokens are not ordered")
			}
			result.Data = append(result.Data, uint32(deltaLine), uint32(deltaStart), uint32(length), uint32(typeIndex), modifiers)
			previous = from
		}
	}
	return result, nil
}

func semanticUTF16Width(text string) int {
	width := 0
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		text = text[size:]
		width++
		if r > 0xffff {
			width++
		}
	}
	return width
}
