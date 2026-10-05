package check

import (
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// EditorInlayOptions selects compiler-backed annotations for a checked snapshot.
type EditorInlayOptions struct{ Types, Parameters, Facts bool }
type EditorInlay struct {
	Pos       diag.Pos
	Label     string
	Parameter bool
}

// EditorInlays uses recorded source bindings and resolved argument identities.
// Optional facts share the hover prover, walking each function only once.
func EditorInlays(info *Info, file *syntax.File, from *Package, options EditorInlayOptions) []EditorInlay {
	out := []EditorInlay{}
	names := map[diag.Pos]int{}
	addBinding := func(pos diag.Pos, name, label string) {
		pos.Col += len(name)
		if i, ok := names[pos]; ok {
			out[i].Label += label
			return
		}
		names[pos] = len(out)
		out = append(out, EditorInlay{Pos: pos, Label: label})
	}
	if options.Types {
		for binding, typ := range info.bindings {
			if binding.Pos.File != file.Path || binding.Type != nil || strings.HasPrefix(binding.Name, "_") || typ == nil || typ == Invalid {
				continue
			}
			addBinding(binding.Pos, binding.Name, ": "+TypeText(typ, from))
		}
	}
	if options.Types {
		var addPattern func(*Pat)
		addPattern = func(p *Pat) {
			if p == nil {
				return
			}
			pos := bindPos(p.bindNode)
			if p.Bind != "" && pos.File == file.Path {
				addBinding(pos, p.Bind, ": "+TypeText(p.BindType, from))
			}
			for _, field := range p.Fields {
				addPattern(field.Pat)
			}
		}
		for _, pat := range info.tuplePats {
			addPattern(pat)
		}
	}
	if options.Parameters {
		for call, instance := range info.instances {
			if call.Pos.File != file.Path || call.Pipe.Line != 0 || instance.Func == nil || instance.Func.Decl == nil {
				continue
			}
			params := instance.Func.Decl.Params
			mapped := info.args(call)
			for i, arg := range call.Args {
				if i >= len(call.Arguments) || call.Arguments[i].Name != "" {
					continue
				}
				index := slices.Index(mapped, arg)
				if index < 0 || index >= len(params) {
					continue
				}
				name := params[index].Name
				if strings.HasPrefix(name, "_") {
					continue
				}
				if id, ok := arg.(*syntax.Ident); ok && id.Name == name {
					continue
				}
				pos := call.Arguments[i].ValueStart
				if pos.Line == 0 {
					pos = arg.Position()
				}
				out = append(out, EditorInlay{Pos: pos, Label: name + ":", Parameter: true})
			}
		}
	}
	if options.Facts {
		roots := map[*Func]bool{}
		for _, decl := range file.Funcs {
			if fn := info.FuncOf[decl]; fn != nil {
				roots[fn] = true
			}
		}
		for _, fn := range info.Tests {
			if fn.Test.Pos.File == file.Path {
				roots[fn] = true
			}
		}
		for fn := range roots {
			if fn.Body == nil {
				continue
			}
			bindings := map[diag.Pos][]*Let{}
			WalkComptime(fn.Body, func(x Expr) bool {
				if block, ok := x.(*Block); ok {
					for _, stmt := range block.Stmts {
						if let, ok := stmt.(*Let); ok && let.Var.Pos.File == file.Path && !strings.HasPrefix(let.Var.Name, "_") {
							bindings[let.Value.Pos()] = append(bindings[let.Value.Pos()], let)
						}
					}
				}
				return true
			})
			if len(bindings) == 0 {
				continue
			}
			f := &factChecker{info: info, diags: &diag.List{}, paths: map[*Func][]branch{}, active: map[string]bool{}, params: map[*Var]*VarRef{}, predParams: map[*Var]*Func{}, lambdaArgs: map[*Var]lambdaArg{}}
			f.validators = validationContexts(info)
			seen := map[*Let]bool{}
			f.observe = func(pos diag.Pos, e env) {
				for _, let := range bindings[pos] {
					if seen[let] {
						continue
					}
					seen[let] = true
					var labels []string
					for _, fact := range describedFacts(f, Reference(let.Var), e, from) {
						if fact.Path == "" {
							labels = append(labels, fact.Constraint)
						}
					}
					if len(labels) > 0 {
						addBinding(let.Var.Pos, let.Var.Name, " where "+strings.Join(labels, " + "))
					}
				}
			}
			f.function(fn)
		}
	}
	slices.SortFunc(out, func(a, b EditorInlay) int {
		if a.Pos.Line != b.Pos.Line {
			return a.Pos.Line - b.Pos.Line
		}
		if a.Pos.Col != b.Pos.Col {
			return a.Pos.Col - b.Pos.Col
		}
		return strings.Compare(a.Label, b.Label)
	})
	return out
}
