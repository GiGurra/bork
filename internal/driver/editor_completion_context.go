package driver

import (
	"strings"

	"github.com/GiGurra/bork/internal/check"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// EditorContextCompletions uses compiler tokens to recover unfinished editor
// syntax and queries the last checked graph for its semantic candidates.
func (a *EditorAnalysis) EditorContextCompletions(file, src string, pos, at diag.Pos) ([]EditorCompletion, bool) {
	tokens, _ := syntax.Lex(file, []byte(src), &diag.List{})
	var out []EditorCompletion
	add := func(c EditorCompletion) { out = append(out, c) }
	before := func(a, b diag.Pos) bool { return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col }
	var prior []syntax.Token
	for _, t := range tokens {
		if before(t.Pos, at) && t.Kind != syntax.Semi && t.Kind != syntax.EOF {
			prior = append(prior, t)
		}
	}
	if len(prior) > 0 && prior[len(prior)-1].Kind == syntax.Dot {
		if arms, ok := a.editorContextPatternCompletions(file, src, prior[:len(prior)-1], prior[len(prior)-1].Pos); ok {
			previous := prior[len(prior)-2]
			if previous.Kind == syntax.LBrace || previous.Kind == syntax.Comma {
				return arms, true
			}
			// An unfinished newline dot can start either the next arm or a
			// continued selector. Keep both sets until syntax distinguishes them.
			out = append(out, arms...)
		}
		if len(prior) > 1 {
			receiver := prior[len(prior)-2]
			members := a.EditorPackageSymbols(file, receiver.Text)
			if len(members) == 0 {
				members = a.EditorMembers(a.editorSnapshotPosition(file, src, receiver.Pos))
			}
			if receiver.Kind == syntax.TIdent {
				if named := a.EditorNamedMembers(a.editorSnapshotPosition(file, src, pos), receiver.Text); len(named) > 0 {
					members = named
				}
			}
			for _, c := range members {
				add(c)
			}
		}
		return out, true
	}
	// Compiler tokens identify the innermost unfinished call or literal.
	var stack []int
	for i, t := range prior {
		switch t.Kind {
		case syntax.LBrace, syntax.LParen, syntax.LBrack:
			stack = append(stack, i)
		case syntax.RBrace, syntax.RParen, syntax.RBrack:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if len(stack) > 0 {
		open := stack[len(stack)-1]
		if prior[open].Kind == syntax.LBrace && open > 0 {
			// Match scrutinees are queried through checked source identities.
			if prior[open-1].Kind == syntax.RParen {
				depth := 1
				for i := open - 2; i >= 0; i-- {
					if prior[i].Kind == syntax.RParen {
						depth++
					}
					if prior[i].Kind == syntax.LParen {
						depth--
						if depth == 0 {
							if i > 0 && prior[i-1].Kind == syntax.KwMatch && i+1 < open-1 && editorLabelPosition(prior[open+1:]) {
								for _, c := range a.EditorMatchArms(a.editorSnapshotPosition(file, src, prior[i-1].Pos)) {
									add(c)
								}
								return out, true
							}
							break
						}
					}
				}
			}
			// Resolve the literal/pattern head through compiler type syntax.
			headStart := open - 1
			depth := 0
			for headStart >= 0 {
				t := prior[headStart]
				if t.Kind == syntax.RBrack {
					depth++
				} else if t.Kind == syntax.LBrack {
					depth--
				} else if depth == 0 && t.Kind != syntax.TIdent && t.Kind != syntax.Dot {
					break
				}
				headStart--
			}
			headStart++
			if headStart < open {
				lo, _ := editorByteOffset(src, prior[headStart].Pos)
				hi, _ := editorByteOffset(src, prior[open-1].End)
				fields := a.EditorTypeFields(file, src[lo:hi])
				if len(fields) > 0 && editorLabelPosition(prior[open+1:]) {
					for _, c := range fields {
						if !editorCompletionLabelUsed(prior[open+1:], c.Name) {
							c.Text = c.Name + ": "
							if len(stack) > 1 {
								parent := stack[len(stack)-2]
								if prior[parent].Kind == syntax.LBrace && parent > 0 && prior[parent-1].Kind == syntax.RParen {
									// Destructuring patterns bind a field by shorthand.
									for j := parent - 1; j >= 0; j-- {
										if prior[j].Kind == syntax.KwMatch {
											c.Text = c.Name
											break
										}
										if prior[j].Kind == syntax.LBrace {
											break
										}
									}
								}
							}
							add(c)
						}
					}
					return out, true
				}
			}
		}
		if prior[open].Kind == syntax.LParen && open > 0 {
			calleeIndex := open - 1
			if prior[calleeIndex].Kind == syntax.RBrack {
				depth := 1
				for j := calleeIndex - 1; j >= 0; j-- {
					if prior[j].Kind == syntax.RBrack {
						depth++
					}
					if prior[j].Kind == syntax.LBrack {
						depth--
						if depth == 0 && j > 0 {
							calleeIndex = j - 1
							break
						}
					}
				}
			}
			callee := prior[calleeIndex]
			name := callee.Text
			if calleeIndex >= 2 && prior[calleeIndex-1].Kind == syntax.Dot {
				name = prior[calleeIndex-2].Text + "." + name
			}
			callable := a.EditorNamedCallable(file, name)
			if callable == nil {
				if result, err := a.Describe(a.editorSnapshotPosition(file, src, callee.Pos)); err == nil {
					callable = result.Callable
				}
			}
			if callable != nil && callable.NamedArguments && editorLabelPosition(prior[open+1:]) {
				lo, _ := editorByteOffset(src, prior[open].End)
				hi, _ := editorByteOffset(src, at)
				var supplied []syntax.Argument
				parsed := syntax.Parse("completion.bork", []byte("fn Completion() { completion("+src[lo:hi]+") }"), &diag.List{})
				if len(parsed.Funcs) > 0 && parsed.Funcs[0].Body != nil {
					if call, ok := parsed.Funcs[0].Body.Tail.(*syntax.Call); ok {
						supplied = call.Arguments
					}
				}
				for _, parameter := range check.EditorRemainingParameters(callable, supplied) {
					if !editorCompletionLabelUsed(prior[open+1:], parameter.Name) {
						add(EditorCompletion{Name: parameter.Name, Text: parameter.Name + ": ", Detail: parameter.Type, Kind: "variable"})
					}
				}
			}
		}
	}

	return out, false
}

func (a *EditorAnalysis) editorContextPatternCompletions(file, src string, prior []syntax.Token, dot diag.Pos) ([]EditorCompletion, bool) {
	if len(prior) == 0 || prior[len(prior)-1].Kind != syntax.LBrace && prior[len(prior)-1].Kind != syntax.Comma && prior[len(prior)-1].End.Line >= dot.Line {
		return nil, false
	}
	depth := 0
	for i := len(prior) - 1; i >= 0; i-- {
		switch prior[i].Kind {
		case syntax.RBrace:
			depth++
		case syntax.LBrace:
			if depth > 0 {
				depth--
				continue
			}
			if i == 0 || prior[i-1].Kind != syntax.RParen {
				return nil, false
			}
			parens := 1
			for j := i - 2; j >= 0; j-- {
				if prior[j].Kind == syntax.RParen {
					parens++
				}
				if prior[j].Kind != syntax.LParen {
					continue
				}
				parens--
				if parens != 0 {
					continue
				}
				if j == 0 || prior[j-1].Kind != syntax.KwMatch {
					return nil, false
				}
				return a.editorMatchArms(a.editorSnapshotPosition(file, src, prior[j-1].Pos), true), true
			}
			return nil, false
		}
	}
	return nil, false
}

func editorCompletionLabelUsed(tokens []syntax.Token, name string) bool {
	depth := 0
	for i, t := range tokens {
		if depth == 0 && t.Kind == syntax.TIdent && t.Text == name && i+1 < len(tokens) && tokens[i+1].Kind == syntax.Colon {
			return true
		}
		switch t.Kind {
		case syntax.LBrace, syntax.LParen, syntax.LBrack:
			depth++
		case syntax.RBrace, syntax.RParen, syntax.RBrack:
			depth--
		}
	}
	return false
}

func editorByteOffset(src string, p diag.Pos) (int, error) {
	lines := strings.SplitAfter(src, "\n")
	if p.Line < 1 || p.Line > len(lines) {
		return 0, nil
	}
	offset := 0
	for _, line := range lines[:p.Line-1] {
		offset += len(line)
	}
	return offset + min(max(p.Col-1, 0), len(lines[p.Line-1])), nil
}

func editorLabelPosition(tokens []syntax.Token) bool {
	depth := 0
	label := true
	for _, t := range tokens {
		if depth == 0 {
			if t.Kind == syntax.Comma {
				label = true
			}
			if t.Kind == syntax.Colon || t.Kind == syntax.Arrow {
				label = false
			}
		}
		switch t.Kind {
		case syntax.LBrace, syntax.LParen, syntax.LBrack:
			depth++
		case syntax.RBrace, syntax.RParen, syntax.RBrack:
			depth--
		}
	}
	return label
}
