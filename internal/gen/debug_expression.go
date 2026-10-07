package gen

import (
	"bytes"
	"fmt"
	"github.com/GiGurra/bork/internal/diag"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"sort"
	"strings"

	"github.com/GiGurra/bork/internal/check"
)

// DebugExpression translates using compiler-owned names, types and lowering.
// Locals come from Delve before presentation rewriting, so their types and
// variable identities describe the selected frame rather than source guesses.
func DebugExpression(source string, metadata *DebugMap, site diag.Pos, locals []check.DebugLocal) (string, error) {
	text, _, err := DebugExpressionTyped(source, metadata, site, locals)
	return text, err
}

// DebugExpressionTyped also returns the checked scalar Go type for presentation.
func DebugExpressionTyped(source string, metadata *DebugMap, site diag.Pos, locals []check.DebugLocal) (string, string, error) {

	var bindings *DebugBindings
	for i := range metadata.Bindings {
		scope := &metadata.Bindings[i]
		if scope.Start.File == site.File && scope.Start.Line <= site.Line && site.Line <= scope.End.Line {
			if bindings != nil {
				return "", "", fmt.Errorf("debug expression: ambiguous source frame location")
			}
			bindings = scope
		}
	}
	if bindings == nil {
		return "", "", fmt.Errorf("debug expression: selected frame has no compiler source context")
	}
	if bindings.Ambiguous {
		return "", "", fmt.Errorf("debug expression: selected frame has ambiguous generated variable names")
	}
	var available []check.DebugLocal
	for _, local := range locals {
		label, known := bindings.Names[local.GoName]
		if !known {
			continue
		}
		local.Name = label
		local.Type = strings.ReplaceAll(local.Type, " ", "")
		if j := strings.IndexByte(local.Type, '('); j >= 0 {
			local.Type = local.Type[:j]
		}
		available = append(available, local)
	}
	locals = available
	expr, info, err := check.DebugExpression(source, metadata.Expressions, locals)
	if err != nil {
		return "", "", err
	}
	g := newGen(info)
	g.debugExpression = true
	stmts, out := g.value(expr)
	if len(stmts) != 0 || out == nil {
		return "", "", fmt.Errorf("debug expression: this expression requires execution")
	}
	// Delve evaluates this syntax without injecting calls into the target. Check
	// the lowered tree as well so future compiler changes cannot widen the subset.
	safe := true
	ast.Inspect(out, func(node ast.Node) bool {
		switch n := node.(type) {
		case nil, *ast.Ident, *ast.BasicLit, *ast.ParenExpr, *ast.SelectorExpr, *ast.UnaryExpr, *ast.BinaryExpr:
		case *ast.CallExpr:
			if !g.debugConversions[n] {
				safe = false
			}
		default:
			safe = false
		}
		return safe
	})
	if !safe {
		return "", "", fmt.Errorf("debug expression: this expression requires execution")
	}

	// Normal generated assignments supply literal types contextually. Delve has
	// no assignment context: retain the checked scalar type with a conversion,
	// which Delve evaluates itself without calling code in the paused process.
	if _, constant := expr.(*check.Const); constant {
		if goType, scalar := basicGoNames[expr.Type()]; scalar {
			if expr.Type() == check.Rune {
				goType = "int32"
			}
			target, parseErr := parser.ParseExpr(goType)
			if parseErr != nil {
				return "", "", parseErr
			}
			out = &ast.CallExpr{Fun: target, Args: []ast.Expr{out}}
		}
	}
	var result bytes.Buffer
	err = printer.Fprint(&result, token.NewFileSet(), out)
	resultType := basicGoNames[expr.Type()]
	if strings.HasPrefix(resultType, "_") {
		resultType = "main." + resultType
	}
	return result.String(), resultType, err
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

	m.ExpressionNames = map[string]string{}
	alias := func(goName, sourceName string) {
		if previous, exists := m.ExpressionNames[goName]; exists && previous != sourceName {
			m.ExpressionNames[goName] = ""
		} else if !exists {
			m.ExpressionNames[goName] = sourceName
		}
	}
	for typ := range g.debugTypes {
		if record, ok := typ.(*check.Record); ok {
			for _, f := range record.Fields {
				alias(name(f.Name).Name, f.Name)
			}
		}
	}
	for _, fn := range g.info.FuncOf {
		if fn.Decl == nil || fn.Body == nil || !g.debugFiles[fn.Decl.Pos.File] {
			continue
		}
		scope := DebugBindings{Start: fn.Decl.Pos, End: fn.Body.End, Names: map[string]string{}}
		scope.Start.File, _ = filepath.Abs(scope.Start.File)
		scope.End.File = scope.Start.File
		note := func(v *check.Var) {
			if v == nil || v.Name == "_" || strings.HasPrefix(v.Name, "_") {
				return
			}
			goName := varIdent(v).Name
			if previous, exists := scope.Names[goName]; exists && previous != v.Name {
				scope.Ambiguous = true
			}
			scope.Names[goName] = v.Name
			alias(goName, v.Name)
		}
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
		m.Bindings = append(m.Bindings, scope)
	}

	sort.Slice(m.Bindings, func(i, j int) bool {
		a, b := m.Bindings[i].Start, m.Bindings[j].Start
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
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
			if label, ok := metadata.ExpressionNames[n.Name]; ok {
				if label == "" {
					safe = false
				} else {
					n.Name = label
				}
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

// DebugSourceName resolves a raw local identity within its source function.
func DebugSourceName(goName string, metadata *DebugMap, site diag.Pos) (string, bool) {
	for _, scope := range metadata.Bindings {
		if scope.Start.File == site.File && scope.Start.Line <= site.Line && site.Line <= scope.End.Line && !scope.Ambiguous {
			label, ok := scope.Names[goName]
			return label, ok
		}
	}
	label, ok := metadata.ExpressionNames[goName]
	return label, ok && label != ""
}
