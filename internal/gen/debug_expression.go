package gen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"

	"github.com/GiGurra/bork/internal/check"
)

// DebugExpression translates using compiler-owned names, types and lowering.
// Locals come from Delve before presentation rewriting, so their types and
// variable identities describe the selected frame rather than source guesses.
func DebugExpression(source string, metadata *DebugMap, locals []check.DebugLocal) (string, error) {
	locals = append([]check.DebugLocal(nil), locals...)
	for i := range locals {
		if label, ok := metadata.Names[locals[i].GoName]; ok {
			locals[i].Name = label
		}
		locals[i].Type = strings.ReplaceAll(locals[i].Type, " ", "")
		if j := strings.IndexByte(locals[i].Type, '('); j >= 0 {
			locals[i].Type = locals[i].Type[:j]
		}
	}
	expr, info, err := check.DebugExpression(source, metadata.Expressions, locals)
	if err != nil {
		return "", err
	}
	stmts, out := newGen(info).value(expr)
	if len(stmts) != 0 || out == nil {
		return "", fmt.Errorf("debug expression: this expression requires execution")
	}
	// Delve evaluates this syntax without injecting calls into the target. Check
	// the lowered tree as well so future compiler changes cannot widen the subset.
	safe := true
	ast.Inspect(out, func(node ast.Node) bool {
		switch node.(type) {
		case nil, *ast.Ident, *ast.BasicLit, *ast.ParenExpr, *ast.SelectorExpr, *ast.UnaryExpr, *ast.BinaryExpr:
		default:
			safe = false
		}
		return safe
	})
	if !safe {
		return "", fmt.Errorf("debug expression: this expression requires execution")
	}
	var result bytes.Buffer
	err = printer.Fprint(&result, token.NewFileSet(), out)
	return result.String(), err
}

func (g *gen) debugExpressions(m *DebugMap) {
	m.Expressions = map[string]check.DebugShape{}
	for key, typ := range m.Types {
		m.Expressions[strings.ReplaceAll(key, " ", "")] = check.DebugShape{Name: typ.Name, Kind: "opaque"}
	}
	m.Expressions["interface{}"] = check.DebugShape{Name: "union", Kind: "opaque"}
	for typ, goName := range basicGoNames {
		if strings.HasPrefix(goName, "_") {
			goName = "main." + goName
		}
		m.Expressions[goName] = check.DebugShape{Name: typ.String(), Kind: "scalar"}
	}
	for typ, expr := range g.debugTypes {
		key := debugGoType(expr, m.Types)
		if key == "" {
			continue
		}
		record, ok := typ.(*check.Record)
		if !ok || record.Tuple || record.Foreign != nil || record.GoMirror != nil || len(record.GoFields) != 0 {
			continue
		}
		shape := check.DebugShape{Name: record.String(), Kind: "record", Fields: map[string]check.DebugMember{}}
		for _, field := range record.Fields {
			shape.Fields[field.Name] = check.DebugMember{Type: debugGoType(g.goType(field.Type), m.Types), Deferred: field.Lazy || field.Computed}
		}
		m.Expressions[strings.ReplaceAll(key, " ", "")] = shape
	}
	note := func(v *check.Var) {
		if v != nil && v.Name != "_" && !strings.HasPrefix(v.Name, "_") {
			if goName := varIdent(v).Name; goName != v.Name {
				m.Names[goName] = v.Name
			}
		}
	}
	for _, fn := range g.info.FuncOf {
		for _, v := range fn.ParamVars {
			note(v)
		}
		check.WalkComptime(fn.Body, func(x check.Expr) bool {
			switch x := x.(type) {
			case *check.VarRef:
				note(x.Var)
			case *check.Block:
				for _, s := range x.Stmts {
					if binding, ok := s.(*check.Let); ok {
						note(binding.Var)
					}
				}
			case *check.Lambda:
				for _, v := range x.Params {
					note(v)
				}
			}
			return true
		})
	}
}

// DebugEvaluateName makes Delve's simple local/field paths usable as bork
// watch expressions. More complex Go-only paths cannot be re-evaluated safely.
func DebugEvaluateName(source string, metadata *DebugMap) (string, bool) {
	expr, err := parser.ParseExpr(source)
	if err != nil {
		return "", false
	}
	safe := true
	ast.Inspect(expr, func(node ast.Node) bool {
		switch n := node.(type) {
		case nil, *ast.SelectorExpr, *ast.ParenExpr:
		case *ast.Ident:
			if label, ok := metadata.Names[n.Name]; ok {
				n.Name = label
			}
		default:
			safe = false
		}
		return safe
	})
	if !safe {
		return "", false
	}
	var out bytes.Buffer
	if printer.Fprint(&out, token.NewFileSet(), expr) != nil {
		return "", false
	}
	return out.String(), true
}
