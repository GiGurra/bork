package gen

import (
	"go/ast"
	"go/token"

	"github.com/GiGurra/bork/internal/check"
)

// Carried names (see docs/design/loops.md). Every value of a carried
// name is a Go variable assigned once, except the state variables the
// paths that meet assign: a loop's latch, which holds each carried
// name's value between iterations and after the loop, and the variable
// of a join after an if, match or block statement. No closure captures
// a state variable while it can still change.

// carryStart declares the latches of loop e's carried names, holding
// their first values: those of the names from outside the loop (outer),
// then the header names, which come into scope (header). ok is false
// when a first value never finishes.
func (g *gen) carryStart(e *check.For) (outer, header []ast.Stmt, ok bool) {
	for _, c := range e.Carries {
		stmts := &header
		if !c.Header() {
			stmts = &outer
		}
		var x ast.Expr
		from := c.Head.Type
		if c.Header() {
			var s []ast.Stmt
			s, x = g.value(c.Init)
			*stmts = append(*stmts, s...)
			if x == nil {
				return outer, header, false
			}
			// The header names after it may read it.
			*stmts = append(*stmts, typedVar(varIdent(c.Head), g.goType(c.Head.Type), g.convert(x, c.Init.Type(), c.Head.Type)), assign(ast.NewIdent("_"), varIdent(c.Head)))
			x = varIdent(c.Head)
		} else {
			x = varIdent(c.Outer)
		}
		if g.reuseList(e, c) {
			// Copy once: aliases outside this loop retain their backing array.
			x = &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent(g.goImport("slices")), Sel: ast.NewIdent("Clone")}, Args: []ast.Expr{x}}
		}
		*stmts = append(*stmts, typedVar(varIdent(c.Latch), g.goType(c.Head.Type), g.convert(x, from, c.Head.Type)))
		if c.After != nil {
			if g.carryStates == nil {
				g.carryStates = map[*check.Var]*ast.Ident{}
			}
			g.carryStates[c.After] = varIdent(c.Latch)
		}
	}
	return outer, header, true
}

// carryHeads binds an iteration's copies of the latches.
func (g *gen) carryHeads(e *check.For) []ast.Stmt {
	var stmts []ast.Stmt
	for _, c := range e.Carries {
		stmts = append(stmts, define(varIdent(c.Head), varIdent(c.Latch)), assign(ast.NewIdent("_"), varIdent(c.Head)))
	}
	return stmts
}

// carryPost is the Go loop's post statement: the next values of the
// names the post clause rebinds, computed from copies of the latches.
func (g *gen) carryPost(e *check.For) ast.Stmt {
	var lhs, results []ast.Expr
	var types []*ast.Field
	var body []ast.Stmt
	for _, c := range e.Carries {
		body = append(body, define(varIdent(c.Latch), varIdent(c.Latch)), assign(ast.NewIdent("_"), varIdent(c.Latch)))
	}
	for _, c := range e.Carries {
		if c.Post == nil {
			continue
		}
		s, x := g.value(c.Post)
		body = append(body, s...)
		if x == nil {
			return nil
		}
		lhs = append(lhs, varIdent(c.Latch))
		results = append(results, g.convert(x, c.Post.Type(), c.Head.Type))
		types = append(types, &ast.Field{Type: g.goType(c.Head.Type)})
	}
	if len(lhs) == 0 {
		return nil
	}
	body = append(body, &ast.ReturnStmt{Results: results})
	next := &ast.CallExpr{Fun: &ast.FuncLit{Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: types}}, Body: &ast.BlockStmt{List: body}}}
	return &ast.AssignStmt{Lhs: lhs, Tok: token.ASSIGN, Rhs: []ast.Expr{next}}
}

// carryAfter binds the values the names a loop carried from outside it
// have after it.
func (g *gen) carryAfter(e *check.For) []ast.Stmt {
	var stmts []ast.Stmt
	for _, c := range e.Carries {
		if c.After != nil {
			stmts = append(stmts, define(varIdent(c.After), varIdent(c.Latch)), assign(ast.NewIdent("_"), varIdent(c.After)))
		}
	}
	return stmts
}

// carryEdges passes carried values on where paths meet.
func (g *gen) carryEdges(edges []*check.CarryEdge) []ast.Stmt {
	var stmts []ast.Stmt
	for _, edge := range edges {
		to := g.carryStates[edge.To]
		if to == nil {
			to = varIdent(edge.To)
		}
		stmts = append(stmts, assign(to, varIdent(edge.From)))
	}
	return stmts
}

// joined wraps the statements of an if, match or block statement whose
// branches give carried names new values: each join's variable holds
// the value before it, and the branches that give another assign it.
func (g *gen) joined(joins []*check.Join, stmts []ast.Stmt) []ast.Stmt {
	if len(joins) == 0 {
		return stmts
	}
	var out []ast.Stmt
	for _, j := range joins {
		if j.Prior != nil {
			out = append(out, typedVar(varIdent(j.Var), g.goType(j.Var.Type), varIdent(j.Prior)))
		} else {
			out = append(out, &ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{&ast.ValueSpec{Names: []*ast.Ident{varIdent(j.Var)}, Type: g.goType(j.Var.Type)}}}})
		}
	}
	out = append(out, stmts...)
	for _, j := range joins {
		out = append(out, assign(ast.NewIdent("_"), varIdent(j.Var)))
	}
	return out
}
