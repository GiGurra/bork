package check

import (
	"fmt"
	"slices"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

type EditorExtractionParameter struct {
	Name        string
	Type        Type
	Site        diag.Pos
	WrittenType string
}

type EditorExtraction struct {
	Function      *Func
	Parameters    []EditorExtractionParameter
	Result        Type
	WrittenResult string
	Effects       Effects
}

// EditorExtractExpression resolves an exact parser expression range against the
// checked graph. Captures use source identities, including references eliminated
// by constant folding; effects use the same traversal as signature checking.
func EditorExtractExpression(info *Info, file *syntax.File, start, end diag.Pos) (*EditorExtraction, error) {
	var selected syntax.Expr
	for _, span := range file.ExpressionSpans {
		if span.Start == start && span.End == end && info.types[span.Expr] != nil {
			selected = span.Expr
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("select a complete expression")
	}
	fn := info.exprOwners[selected]
	if fn == nil || fn.Body == nil {
		return nil, fmt.Errorf("select an expression in a function or test body")
	}
	if fn.MockOf != nil {
		return nil, fmt.Errorf("mock bodies have a contextual target binding")
	}
	var value Expr
	anchor := sourceTokenPos(selected)
	WalkComptime(fn.Body, func(x Expr) bool {
		if value == nil && x.TokenPos() == anchor && identical(x.Type(), info.types[selected]) {
			value = x
			return false
		}
		return true
	})
	if value == nil {
		return nil, fmt.Errorf("the selection has no checked expression")
	}
	transfersControl := false
	WalkComptime(value, func(x Expr) bool {
		switch x.(type) {
		case *Lambda, *Comptime:
			return false
		case *Return, *Try, *LoopControl, *ScopeBlock:
			transfersControl = true
		}
		return !transfersControl
	})
	if transfersControl {
		return nil, fmt.Errorf("the selection transfers control outside its new function")
	}
	u := &effectUses{from: fn.Pkg}
	u.expr(value)
	if u.used&EffOpen != 0 {
		return nil, fmt.Errorf("the selection uses caller-dependent effects")
	}
	out := &EditorExtraction{Function: fn, Result: info.types[selected], Effects: u.used}
	if fn.Decl.Result != nil && identical(fn.Result, out.Result) {
		out.WrittenResult = writtenTypeText(fn.Decl.Result)
	}
	inside := func(pos diag.Pos) bool {
		return pos.File == start.File && compareEditorPosition(pos, start) >= 0 && compareEditorPosition(pos, end) < 0
	}
	seen := map[any]int{}
	for id, decl := range info.defs {
		if !inside(id.Pos) {
			continue
		}
		var pos diag.Pos
		switch decl := decl.(type) {
		case *syntax.Param:
			pos = decl.Pos
		case *syntax.Binding:
			if decl.Package {
				continue
			}
			pos = decl.Pos
		case *syntax.FieldPat:
			pos = decl.Pos
		case syntax.Pattern:
			pos = decl.Position()
		case syntax.Expr:
			pos = decl.Position()
		default:
			return nil, fmt.Errorf("the selection captures a value that cannot be passed explicitly")
		}
		if inside(pos) {
			continue
		}
		if binding, ok := decl.(*syntax.Binding); ok && (binding.Lazy || binding.AsyncScope != nil) {
			return nil, fmt.Errorf("passing a deferred capture would change evaluation order")
		}
		if i, ok := seen[decl]; ok {
			if compareEditorPosition(id.Pos, out.Parameters[i].Site) < 0 {
				out.Parameters[i].Site = id.Pos
			}
			continue
		}
		typ := info.types[id]
		if typ == nil || typ == Invalid {
			return nil, fmt.Errorf("the selection has an unresolved capture")
		}
		seen[decl] = len(out.Parameters)
		written := ""
		switch decl := decl.(type) {
		case *syntax.Binding:
			if decl.Type != nil && identical(info.bindings[decl], typ) {
				written = writtenTypeText(decl.Type)
			}
		case *syntax.Param:
			for _, owner := range info.FuncOf {
				for i, param := range owner.Decl.Params {
					if param == decl && decl.Type != nil && identical(owner.Params[i], typ) {
						written = writtenTypeText(decl.Type)
					}
				}
			}
		}
		out.Parameters = append(out.Parameters, EditorExtractionParameter{Name: id.Name, Type: typ, Site: id.Pos, WrittenType: written})
	}
	slices.SortFunc(out.Parameters, func(a, b EditorExtractionParameter) int { return compareEditorPosition(a.Site, b.Site) })
	return out, nil
}

func compareEditorPosition(a, b diag.Pos) int {
	if a.Line != b.Line {
		return a.Line - b.Line
	}
	return a.Col - b.Col
}
