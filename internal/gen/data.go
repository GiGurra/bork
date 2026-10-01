package gen

import (
	"go/ast"
	"go/token"
	"strconv"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/syntax"
)

func (g *gen) selector(e *syntax.Selector) ([]ast.Stmt, ast.Expr) {
	if v := g.info.SelectorVariants[e]; v != nil {
		return nil, &ast.CompositeLit{Type: g.variantType(v)}
	}
	stmts, x := g.value(e.X)
	if x == nil {
		return stmts, nil
	}
	return stmts, &ast.SelectorExpr{X: paren(x), Sel: name(e.Name)}
}

func (g *gen) recordLit(e *syntax.RecordLit) ([]ast.Stmt, ast.Expr) {
	var typ ast.Expr
	var fields []*check.Field
	switch target := g.info.RecordTargets[e].(type) {
	case *check.Record:
		typ, fields = typeName(target.Name), target.Fields
	case *check.Variant:
		typ, fields = g.variantType(target), target.Fields
	}
	// Field values are evaluated in the order they are written.
	vals := make([]syntax.Expr, len(e.Fields))
	for i, fi := range e.Fields {
		vals[i] = fi.Value
	}
	stmts, xs := g.values(vals)
	if xs == nil {
		return stmts, nil
	}
	lit := &ast.CompositeLit{Type: typ}
	for i, fi := range e.Fields {
		var ft check.Type
		for _, f := range fields {
			if f.Name == fi.Name {
				ft = f.Type
			}
		}
		lit.Elts = append(lit.Elts, &ast.KeyValueExpr{Key: name(fi.Name), Value: g.convert(xs[i], g.info.Types[fi.Value], ft)})
	}
	return stmts, lit
}

// copyExpr lowers `x.copy(a = 1, b.c = 2)`. Records are Go struct
// values, so copying the struct and assigning the changed (possibly
// nested) fields never affects x.
func (g *gen) copyExpr(e *syntax.Copy) ([]ast.Stmt, ast.Expr) {
	exprs := []syntax.Expr{e.X}
	for _, u := range e.Updates {
		exprs = append(exprs, u.Value)
	}
	stmts, xs := g.values(exprs)
	if xs == nil {
		return stmts, nil
	}
	res := g.newTmp()
	stmts = append(stmts, define(res, xs[0]))
	rec := g.info.Types[e.X].(*check.Record)
	for i, u := range e.Updates {
		var lhs ast.Expr = res
		cur := rec
		var ft check.Type
		for _, field := range u.Path {
			lhs = &ast.SelectorExpr{X: lhs, Sel: name(field)}
			f := cur.Field(field)
			ft = f.Type
			if next, ok := f.Type.(*check.Record); ok {
				cur = next
			}
		}
		stmts = append(stmts, &ast.AssignStmt{Lhs: []ast.Expr{lhs}, Tok: token.ASSIGN, Rhs: []ast.Expr{g.convert(xs[i+1], g.info.Types[u.Value], ft)}})
	}
	return stmts, res
}

// try lowers `x?` to a type switch that keeps one case and returns the
// others from the function.
func (g *gen) try(e *syntax.Try) ([]ast.Stmt, ast.Expr) {
	info := g.info.Tries[e]
	stmts, x := g.value(e.X)
	if x == nil {
		return stmts, nil
	}
	x = g.convert(x, g.info.Types[e.X], g.info.Types[e.X])
	kept, v := g.newTmp(), g.newTmp()
	stmts = append(stmts, varDecl(kept, g.goType(info.Kept)))
	sw := &ast.TypeSwitchStmt{
		Assign: define(v, &ast.TypeAssertExpr{X: x}),
		Body:   &ast.BlockStmt{},
	}
	clause := func(types []ast.Expr, body ...ast.Stmt) {
		sw.Body.List = append(sw.Body.List, &ast.CaseClause{List: types, Body: body})
	}
	ret := func(x ast.Expr) ast.Stmt { return &ast.ReturnStmt{Results: []ast.Expr{x}} }
	if info.Option != nil {
		clause([]ast.Expr{g.variantType(info.Option.Variants[0])}, assign(kept, &ast.SelectorExpr{X: v, Sel: ast.NewIdent("value")}))
		clause(nil, ret(&ast.CompositeLit{Type: g.variantType(info.NoneOf.Variants[1])}))
	} else {
		clause([]ast.Expr{g.goType(info.Kept)}, assign(kept, v))
		for _, m := range info.Rest {
			clause([]ast.Expr{g.goType(m)}, ret(v))
		}
		clause(nil, unreachable()...)
	}
	return append(stmts, sw), kept
}

// unreachable is the body of a default case that cannot happen; it also
// tells Go the switch always ends.
func unreachable() []ast.Stmt {
	return []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{
		Fun:  ast.NewIdent("panic"),
		Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote("bork: unreachable")}},
	}}}
}

// matchStmt lowers a match, each arm's value into k. Matching a sealed
// type or a union becomes a Go type switch; matching an Int, String, or
// Bool against literals becomes an expression switch.
func (g *gen) matchStmt(m *syntax.Match, k sink) []ast.Stmt {
	st := g.info.Types[m.X]
	stmts, x := g.value(m.X)
	if x == nil {
		return stmts
	}
	x = g.typed(g.convert(x, st, st), st)
	v := g.newTmp()
	switch st.(type) {
	case *check.Sealed, *check.Union:
		return append(stmts, g.typeSwitch(m, x, v, st, k))
	}
	hasLit := false
	for _, arm := range m.Arms {
		if _, ok := arm.Pattern.(*syntax.LitPat); ok {
			hasLit = true
		}
	}
	if !hasLit {
		// The first arm matches everything (later arms were reported as
		// unreachable by the checker).
		arm := m.Arms[0]
		stmts = append(stmts, define(v, x))
		stmts = append(stmts, g.bindPattern(arm.Pattern, v)...)
		return append(stmts, g.into(arm.Body, k)...)
	}
	sw := &ast.SwitchStmt{Init: define(v, x), Tag: v, Body: &ast.BlockStmt{}}
	hasDefault := false
	for _, arm := range m.Arms {
		var list []ast.Expr
		if lp, ok := arm.Pattern.(*syntax.LitPat); ok {
			_, lit := g.value(lp.Value)
			list = []ast.Expr{lit}
		} else {
			hasDefault = true
		}
		body := append(g.bindPattern(arm.Pattern, v), g.into(arm.Body, k)...)
		sw.Body.List = append(sw.Body.List, &ast.CaseClause{List: list, Body: body})
	}
	if !hasDefault {
		sw.Body.List = append(sw.Body.List, &ast.CaseClause{Body: unreachable()})
	}
	return append(stmts, sw)
}

func (g *gen) typeSwitch(m *syntax.Match, x ast.Expr, v *ast.Ident, st check.Type, k sink) ast.Stmt {
	sw := &ast.TypeSwitchStmt{Assign: define(v, &ast.TypeAssertExpr{X: x}), Body: &ast.BlockStmt{}}
	hasDefault := false
	for _, arm := range m.Arms {
		var list []ast.Expr
		switch p := arm.Pattern.(type) {
		case *syntax.VariantPat:
			if variant := g.info.PatVariants[p]; variant != nil {
				list = []ast.Expr{g.variantType(variant)}
			} else {
				list = g.caseTypes(g.info.PatTypes[p], st)
			}
		case *syntax.TypePat:
			list = g.caseTypes(g.info.PatTypes[p], st)
		}
		if list == nil {
			hasDefault = true
		}
		body := append(g.bindPattern(arm.Pattern, v), g.into(arm.Body, k)...)
		sw.Body.List = append(sw.Body.List, &ast.CaseClause{List: list, Body: body})
	}
	if !hasDefault {
		// Using v here keeps Go from reporting it as unused when no arm
		// binds anything.
		body := append([]ast.Stmt{assign(ast.NewIdent("_"), v)}, unreachable()...)
		sw.Body.List = append(sw.Body.List, &ast.CaseClause{Body: body})
	}
	return sw
}

// caseTypes lists the Go types a type pattern of type t tests for, when
// matching a value of type st. A pattern covering all of st becomes the
// default case (nil).
func (g *gen) caseTypes(t, st check.Type) []ast.Expr {
	if check.Identical(t, st) {
		return nil
	}
	if u, ok := t.(*check.Union); ok {
		var list []ast.Expr
		for _, m := range u.Members {
			list = append(list, g.goType(m))
		}
		return list
	}
	return []ast.Expr{g.goType(t)}
}

// bindPattern binds the names a pattern introduces, from the matched
// value v (which Go has already narrowed to the pattern's type in a
// single-type case clause).
func (g *gen) bindPattern(p syntax.Pattern, v *ast.Ident) []ast.Stmt {
	var out []ast.Stmt
	bind := func(n string, val ast.Expr, node any) {
		out = append(out, define(name(n), val))
		if g.info.Unused[node] {
			out = append(out, assign(ast.NewIdent("_"), name(n)))
		}
	}
	switch p := p.(type) {
	case *syntax.TypePat:
		bind(p.Name, v, p)
	case *syntax.VariantPat:
		for _, fp := range p.Fields {
			bind(fp.Bind, &ast.SelectorExpr{X: v, Sel: name(fp.Field)}, fp)
		}
	}
	return out
}
