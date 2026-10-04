package driver

import (
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/doccomment"
	"github.com/GiGurra/bork/internal/syntax"
)

// EditorSignatureHelp recovers the current call with compiler tokens, then uses
// checked identities or normal compiler lookup. It does not recheck a package.
type EditorSignatureHelp struct {
	*check.EditorSignature
	Documentation   string
	ActiveParameter *int
}

func (a *EditorAnalysis) SignatureHelp(path, current string, at diag.Pos) *EditorSignatureHelp {
	file, from := a.editorFile(path)
	if file == nil {
		return nil
	}
	tokens, _ := syntax.Lex(path, []byte(current), &diag.List{})
	var prior []syntax.Token
	for _, token := range tokens {
		if !editorSignatureBefore(token.Pos, at) {
			break
		}
		if token.Kind == syntax.TGoCode && editorSignatureBefore(at, token.End) {
			return nil
		}
		if token.Kind != syntax.EOF && token.Kind != syntax.Semi {
			prior = append(prior, token)
		}
	}
	var stack []int
	for i, token := range prior {
		switch token.Kind {
		case syntax.LParen, syntax.LBrack, syntax.LBrace:
			stack = append(stack, i)
		case syntax.RParen, syntax.RBrack, syntax.RBrace:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	for i := len(stack) - 1; i >= 0; i-- {
		open := stack[i]
		if prior[open].Kind != syntax.LParen {
			continue
		}
		start, end, ok := editorSignatureHead(prior, open)
		if !ok {
			continue
		}
		signature := (*check.EditorSignature)(nil)
		openOffset, _ := editorByteOffset(current, prior[open].End)
		if openOffset <= len(file.Source) && current[:openOffset] == file.Source[:openOffset] {
			signature = check.EditorCheckedSignature(a.program.info, from, prior[open].Pos)
		}
		if signature == nil {
			if start > 0 && prior[start-1].Kind == syntax.Dot {
				return nil
			}
			name := prior[end].Text
			var receiver check.Type
			snapshot := a.editorSnapshotPosition(path, current, prior[end].Pos)
			symbols := a.EditorSymbols(snapshot)
			local := false
			if start == end && (end == 0 || prior[end-1].Kind != syntax.Dot) {
				for _, symbol := range symbols {
					if symbol.Name == name && symbol.typeOf != nil && (symbol.Kind == "variable" || symbol.Kind == "parameter") {
						signature = check.EditorFunctionSignature(name, symbol.typeOf, from)
						local = true
						break
					}
				}
			}
			if !local {
				if end >= 2 && prior[end-1].Kind == syntax.Dot {
					if start == end {
						return nil
					}
					prefix := prior[end-2]
					for _, symbol := range symbols {
						if start == end-2 && symbol.Name == prefix.Text && symbol.typeOf != nil && (symbol.Kind == "variable" || symbol.Kind == "parameter") {
							receiver = symbol.typeOf
							break
						}
					}
					if receiver == nil {
						name = editorSignatureName(prior[start : end+1])
					}
				}
				var typeArgs []*syntax.TypeExpr
				if end+1 < open {
					lo, _ := editorByteOffset(current, prior[end+1].Pos)
					hi, _ := editorByteOffset(current, prior[open-1].End)
					parsed := syntax.Parse("signature-query.bork", []byte("fn SignatureQuery() { Query"+current[lo:hi]+"(0) }"), &diag.List{})
					if len(parsed.Funcs) > 0 && parsed.Funcs[0].Body != nil {
						if call, ok := parsed.Funcs[0].Body.Tail.(*syntax.Call); ok {
							typeArgs = call.TypeArgs
						}
					}
					if len(typeArgs) == 0 {
						return nil
					}
				}
				signature = check.EditorNamedSignature(a.program.info, from, name, receiver, typeArgs, snapshot)
			}
		}
		if signature == nil {
			return nil
		}
		if signature.Name == "" {
			signature.Name = editorSignatureName(prior[start : end+1])
		}
		previous, currentName := editorSignatureArguments(prior[open+1:])
		if start > 0 && prior[start-1].Kind == syntax.PipeGt && signature.ImplicitParameters == 0 {
			signature.ImplicitParameters = 1
		}
		for j := 0; j < signature.ImplicitParameters; j++ {
			previous = append([]syntax.Argument{{}}, previous...)
		}
		out := &EditorSignatureHelp{EditorSignature: signature, ActiveParameter: check.EditorActiveParameter(signature.Callable, previous, currentName)}
		if signature.Definition != nil {
			for _, source := range a.program.files {
				if source.Path == signature.Definition.File {
					out.Documentation = doccomment.At(source, *signature.Definition)
					break
				}
			}
		}
		return out
	}
	return nil
}

func editorSignatureBefore(a, b diag.Pos) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col
}

func editorSignatureHead(tokens []syntax.Token, open int) (start, end int, ok bool) {
	end = open - 1
	if end < 0 {
		return
	}
	if tokens[end].Kind == syntax.RBrack {
		depth := 1
		end--
		for end >= 0 {
			if tokens[end].Kind == syntax.RBrack {
				depth++
			}
			if tokens[end].Kind == syntax.LBrack {
				depth--
				if depth == 0 {
					end--
					break
				}
			}
			end--
		}
	}
	if end < 0 || tokens[end].Kind != syntax.TIdent {
		return
	}
	start = end
	for start >= 2 && tokens[start-1].Kind == syntax.Dot && tokens[start-2].Kind == syntax.TIdent {
		start -= 2
	}
	if start > 0 && (tokens[start-1].Kind == syntax.KwFn || tokens[start-1].Kind == syntax.KwPred) {
		return
	}
	// A method declaration follows the receiver parameter list.
	if start > 0 && tokens[start-1].Kind == syntax.RParen {
		depth := 1
		for j := start - 2; j >= 0; j-- {
			if tokens[j].Kind == syntax.RParen {
				depth++
			}
			if tokens[j].Kind == syntax.LParen {
				depth--
				if depth == 0 {
					if j > 0 && tokens[j-1].Kind == syntax.KwFn {
						return
					}
					break
				}
			}
		}
	}
	ok = true
	return
}
func editorSignatureName(tokens []syntax.Token) string {
	var out strings.Builder
	for _, token := range tokens {
		if token.Kind == syntax.Dot {
			out.WriteByte('.')
		} else {
			out.WriteString(token.Text)
		}
	}
	return out.String()
}
func editorSignatureArguments(tokens []syntax.Token) ([]syntax.Argument, string) {
	var previous []syntax.Argument
	start, depth := 0, 0
	label := func(part []syntax.Token) string {
		if len(part) >= 2 && part[0].Kind == syntax.TIdent && part[1].Kind == syntax.Colon {
			return part[0].Text
		}
		return ""
	}
	for i, token := range tokens {
		if token.Kind == syntax.Comma && depth == 0 {
			previous = append(previous, syntax.Argument{Name: label(tokens[start:i])})
			start = i + 1
		}
		switch token.Kind {
		case syntax.LParen, syntax.LBrack, syntax.LBrace:
			depth++
		case syntax.RParen, syntax.RBrack, syntax.RBrace:
			depth--
		}
	}
	return previous, label(tokens[start:])
}
