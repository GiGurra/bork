package gen

import (
	"go/ast"
	"go/constant"
	"go/token"
	"strconv"

	"github.com/GiGurra/bork/internal/check"
)

func (g *gen) selector(e *check.Select) ([]ast.Stmt, ast.Expr) {
	stmts, x := g.value(e.X)
	if x == nil {
		return stmts, nil
	}
	return stmts, g.fieldRead(paren(x), e.Field)
}

func (g *gen) recordLit(e *check.RecordLit) ([]ast.Stmt, ast.Expr) {
	var typ ast.Expr
	if e.Record != nil {
		typ = g.goType(e.Record)
	} else {
		typ = g.variantType(e.Variant)
	}
	// Only eager fields enter source-order evaluation; recipes retain all arguments.
	var vals []check.Expr
	for _, field := range e.Fields {
		if field.Thunk == nil {
			vals = append(vals, field.Value)
		}
	}
	stmts, xs := g.values(vals)
	if xs == nil && len(vals) != 0 {
		return stmts, nil
	}
	lit := &ast.CompositeLit{Type: typ}
	var computed []*check.FieldValue
	i := 0
	for _, field := range e.Fields {
		if field.Field.Computed {
			computed = append(computed, field)
			continue
		}
		var value ast.Expr
		if field.Thunk != nil {
			value = g.fieldCell(field.Thunk, field.Lazy)
		} else {
			value = g.convert(xs[i], field.Value.Type(), field.Field.Type)
			i++
		}
		lit.Elts = append(lit.Elts, &ast.KeyValueExpr{Key: name(field.Name), Value: value})
	}
	if len(computed) > 0 {
		root := g.newTmp()
		g.nameCandidate(e.Candidate, root)
		stmts = append(stmts, define(root, lit))
		for _, field := range computed {
			stmts = append(stmts, &ast.AssignStmt{Lhs: []ast.Expr{&ast.SelectorExpr{X: root, Sel: name(field.Name)}}, Tok: token.ASSIGN, Rhs: []ast.Expr{g.fieldCell(field.Thunk, field.Lazy)}})
		}
		return stmts, root
	}
	return stmts, lit
}

func (g *gen) copyExpr(e *check.Copy) ([]ast.Stmt, ast.Expr) {
	exprs := []check.Expr{e.X}
	for _, update := range e.Updates {
		if update.Thunk == nil {
			exprs = append(exprs, update.Value)
		}
	}
	stmts, xs := g.values(exprs)
	if xs == nil {
		return stmts, nil
	}
	result := g.newTmp()
	if e.Candidate != nil {
		g.nameCandidate(e.Candidate, result)
	}
	stmts = append(stmts, define(result, xs[0]))
	rec := e.X.Type().(*check.Record)
	i := 1
	for _, update := range e.Updates {
		var value ast.Expr
		if update.Thunk != nil {
			value = g.fieldCell(update.Thunk, update.Lazy)
		} else {
			value = g.convert(xs[i], update.Value.Type(), update.Field.Type)
			i++
		}
		stmts = append(stmts, g.copyField(result, rec, update.Path, value)...)
	}
	return stmts, result
}

// A nested update copies each memo-backed parent before replacing its cell.
func (g *gen) copyField(root ast.Expr, rec *check.Record, path []string, value ast.Expr) []ast.Stmt {
	field := rec.Field(path[0])
	lhs := &ast.SelectorExpr{X: root, Sel: name(field.Name)}
	if len(path) == 1 {
		return []ast.Stmt{&ast.AssignStmt{Lhs: []ast.Expr{lhs}, Tok: token.ASSIGN, Rhs: []ast.Expr{value}}}
	}
	child := g.newTmp()
	stmts := []ast.Stmt{define(child, g.fieldRead(root, field))}
	stmts = append(stmts, g.copyField(child, field.Type.(*check.Record), path[1:], value)...)
	stmts = append(stmts, &ast.AssignStmt{Lhs: []ast.Expr{lhs}, Tok: token.ASSIGN, Rhs: []ast.Expr{g.fieldResolved(child, field)}})
	return stmts
}

// try lowers `x?` to a type switch that keeps one case and returns the
// others from the function.
func (g *gen) try(e *check.Try) ([]ast.Stmt, ast.Expr) {
	info := &e.TryInfo
	stmts, x := g.value(e.X)
	if x == nil {
		return stmts, nil
	}
	x = g.convert(x, e.X.Type(), e.X.Type())
	kept, v := g.newTmp(), g.newTmp()
	stmts = append(stmts, varDecl(kept, g.goType(info.Kept)))
	sw := &ast.TypeSwitchStmt{
		Assign: define(v, &ast.TypeAssertExpr{X: x}),
		Body:   &ast.BlockStmt{},
	}
	clause := func(types []ast.Expr, body ...ast.Stmt) {
		sw.Body.List = append(sw.Body.List, &ast.CaseClause{List: types, Body: body})
	}
	ret := func(x ast.Expr) []ast.Stmt { return g.returning(x) }
	if info.Option != nil {
		clause([]ast.Expr{g.variantType(info.Option.Variants[0])}, assign(kept, &ast.SelectorExpr{X: v, Sel: ast.NewIdent("value")}))
		clause(nil, ret(&ast.CompositeLit{Type: g.variantType(info.NoneOf.Variants[1])})...)
	} else {
		clause([]ast.Expr{g.goType(info.Kept)}, assign(kept, v))
		for _, m := range info.Rest {
			clause([]ast.Expr{g.goType(m)}, ret(v)...)
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

// matchStmt lowers a match, each arm's value into k. Where the arms
// only test the top-level type or variant, it becomes a Go type switch;
// literals on a basic type become an expression switch; and nested
// patterns become an if-else chain of tests.
func (g *gen) matchStmt(m *check.Match, k sink) []ast.Stmt {
	st := m.X.Type()
	stmts, x := g.value(m.X)
	if x == nil {
		return stmts
	}
	x = g.typed(g.convert(x, st, st), st)
	v := g.newTmp()
	pats := make([]*check.Pat, len(m.Arms))
	flat, lits := true, true
	for i, arm := range m.Arms {
		p := arm.Pat
		pats[i] = p
		if p.HasGuard() {
			flat, lits = false, false
		}
		if !flatPattern(p) {
			flat = false
		}
		if p.Kind != check.PatLit && p.Kind != check.PatWild {
			lits = false
		}
	}
	switch st.(type) {
	case *check.Sealed, *check.Union:
		if flat {
			return append(stmts, g.typeSwitch(m, pats, x, v, k))
		}
	default:
		if lits {
			return append(stmts, g.literalSwitch(m, pats, x, v, k))
		}
	}
	stmts = append(stmts, define(v, x))
	var chain *ast.IfStmt
	var last *ast.IfStmt
	var final *ast.BlockStmt
	for i, arm := range m.Arms {
		body := &ast.BlockStmt{List: append(g.binds(pats[i], v, false), g.into(arm.Body, k)...)}
		conds := g.tests(pats[i], v)
		if len(conds) == 0 {
			final = body
			break
		}
		s := &ast.IfStmt{Cond: and(conds), Body: body}
		if chain == nil {
			chain = s
		} else {
			last.Else = s
		}
		last = s
	}
	if final == nil {
		final = &ast.BlockStmt{List: unreachable()}
	}
	if chain == nil {
		// The arm's names are bound in a block of its own, as in an
		// if's branch: another match beside it may bind them too.
		return append(stmts, assign(ast.NewIdent("_"), v), final)
	}
	last.Else = final
	return append(stmts, chain)
}

// flatPattern reports whether p only tests its top-level type or
// variant, and binds the rest.
func flatPattern(p *check.Pat) bool {
	fieldsWild := func(fs []*check.PatField) bool {
		for _, f := range fs {
			if f.Pat.Kind != check.PatWild {
				return false
			}
		}
		return true
	}
	switch p.Kind {
	case check.PatWild:
		return true
	case check.PatVariant:
		return fieldsWild(p.Fields)
	case check.PatType:
		if p.Sub == nil || p.Sub.Kind == check.PatWild {
			return true
		}
		if len(p.Members) == 1 && (p.Sub.Kind == check.PatVariant || p.Sub.Kind == check.PatRecord) && p.Sub.Bind == "" {
			return fieldsWild(p.Sub.Fields)
		}
	}
	return false
}

func and(conds []ast.Expr) ast.Expr {
	x := conds[0]
	for _, c := range conds[1:] {
		x = &ast.BinaryExpr{X: x, Op: token.LAND, Y: c}
	}
	return x
}

func (g *gen) typeSwitch(m *check.Match, pats []*check.Pat, x ast.Expr, v *ast.Ident, k sink) ast.Stmt {
	sw := &ast.TypeSwitchStmt{Assign: &ast.ExprStmt{X: &ast.TypeAssertExpr{X: x}}, Body: &ast.BlockStmt{}}
	for _, p := range pats {
		if binds(p) {
			// Go requires the switch's variable to be used.
			sw.Assign = define(v, &ast.TypeAssertExpr{X: x})
		}
	}
	hasDefault := false
	for i, arm := range m.Arms {
		p := pats[i]
		var list []ast.Expr
		switch p.Kind {
		case check.PatVariant:
			list = []ast.Expr{g.variantType(p.Variant)}
		case check.PatType:
			switch {
			case p.Sub != nil && p.Sub.Kind == check.PatVariant:
				list = []ast.Expr{g.variantType(p.Sub.Variant)}
			default:
				for _, mt := range p.Members {
					list = append(list, g.goType(mt))
				}
			}
		}
		if list == nil {
			hasDefault = true
		}
		// In a single-type case, Go has narrowed v to that type.
		body := append(g.binds(p, v, len(list) == 1), g.into(arm.Body, k)...)
		sw.Body.List = append(sw.Body.List, &ast.CaseClause{List: list, Body: body})
		if list == nil {
			break
		}
	}
	if !hasDefault {
		sw.Body.List = append(sw.Body.List, &ast.CaseClause{Body: unreachable()})
	}
	return sw
}

// binds reports whether p binds any names.
func binds(p *check.Pat) bool {
	if p == nil {
		return false
	}
	if p.Bind != "" || binds(p.Sub) {
		return true
	}
	for _, f := range p.Fields {
		if binds(f.Pat) {
			return true
		}
	}
	return false
}

func (g *gen) literalSwitch(m *check.Match, pats []*check.Pat, x ast.Expr, v *ast.Ident, k sink) ast.Stmt {
	sw := &ast.SwitchStmt{Init: define(v, x), Tag: v, Body: &ast.BlockStmt{}}
	hasDefault := false
	for i, arm := range m.Arms {
		var list []ast.Expr
		if pats[i].Kind == check.PatLit {
			list = []ast.Expr{g.litExpr(pats[i])}
		} else {
			hasDefault = true
		}
		body := append(g.binds(pats[i], v, true), g.into(arm.Body, k)...)
		sw.Body.List = append(sw.Body.List, &ast.CaseClause{List: list, Body: body})
		if list == nil {
			break
		}
	}
	if !hasDefault {
		sw.Body.List = append(sw.Body.List, &ast.CaseClause{Body: unreachable()})
	}
	return sw
}

func (g *gen) litExpr(p *check.Pat) ast.Expr {
	switch p.Lit.Kind() {
	case constant.String:
		return &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(constant.StringVal(p.Lit))}
	case constant.Bool:
		return ast.NewIdent(strconv.FormatBool(constant.BoolVal(p.Lit)))
	}
	return constLit(p.Lit, p.Type)
}

// tests are the conditions under which the value x matches p.
func (g *gen) tests(p *check.Pat, x ast.Expr) []ast.Expr {
	conds := g.patternTests(p, x)
	if p.Guard != nil {
		stmts, value := g.value(p.Guard)
		body := append(g.binds(p, x, false), stmts...)
		body = append(body, &ast.ReturnStmt{Results: []ast.Expr{value}})
		guard := &ast.CallExpr{Fun: &ast.FuncLit{
			Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("bool")}}}},
			Body: &ast.BlockStmt{List: body},
		}}
		conds = append(conds, guard)
	}
	return conds
}

func (g *gen) patternTests(p *check.Pat, x ast.Expr) []ast.Expr {
	switch p.Kind {
	case check.PatLit:
		if p.Lit.Kind() == constant.Bool {
			if constant.BoolVal(p.Lit) {
				return []ast.Expr{x}
			}
			return []ast.Expr{&ast.UnaryExpr{Op: token.NOT, X: x}}
		}
		return []ast.Expr{&ast.BinaryExpr{X: x, Op: token.EQL, Y: g.litExpr(p)}}
	case check.PatVariant:
		vt := g.variantType(p.Variant)
		conds := []ast.Expr{g.isType(vt, x)}
		return append(conds, g.fieldTests(p, &ast.TypeAssertExpr{X: x, Type: vt})...)
	case check.PatRecord:
		return g.fieldTests(p, x)
	case check.PatType:
		var alts []ast.Expr
		for _, m := range p.Members {
			alts = append(alts, g.isType(g.goType(m), x))
		}
		test := alts[0]
		for _, a := range alts[1:] {
			test = &ast.BinaryExpr{X: test, Op: token.LOR, Y: a}
		}
		if len(alts) > 1 {
			test = &ast.ParenExpr{X: test}
		}
		conds := []ast.Expr{test}
		if p.Sub != nil && len(p.Members) == 1 {
			conds = append(conds, g.tests(p.Sub, g.narrow(p, x))...)
		}
		return conds
	case check.PatList:
		op := token.EQL
		if p.Rest != nil {
			op = token.GEQ
		}
		n := &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(len(p.Elems))}
		conds := []ast.Expr{&ast.BinaryExpr{X: &ast.CallExpr{Fun: ast.NewIdent("len"), Args: []ast.Expr{x}}, Op: op, Y: n}}
		for i, e := range p.Elems {
			conds = append(conds, g.tests(e, listIndex(x, i))...)
		}
		return conds
	}
	return nil
}

func listIndex(x ast.Expr, i int) ast.Expr {
	return &ast.IndexExpr{X: x, Index: &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(i)}}
}

func (g *gen) fieldTests(pattern *check.Pat, x ast.Expr) []ast.Expr {
	var conds []ast.Expr
	for _, f := range pattern.Fields {
		conds = append(conds, g.tests(f.Pat, g.patternFieldRead(x, pattern.Type, pattern.Variant, f.Name))...)
	}
	return conds
}

// isType tests whether the interface value x holds a value of Go type t.
func (g *gen) isType(t, x ast.Expr) ast.Expr {
	g.usesIs = true
	return &ast.CallExpr{Fun: &ast.IndexExpr{X: ast.NewIdent("_is"), Index: t}, Args: []ast.Expr{x}}
}

// narrow is x as the type a PatType lets through.
func (g *gen) narrow(p *check.Pat, x ast.Expr) ast.Expr {
	if len(p.Members) == 1 {
		return &ast.TypeAssertExpr{X: x, Type: g.goType(p.Members[0])}
	}
	return x
}

// binds binds the names p introduces, from the value x it matched.
// narrowed says x already has p's own Go type (as in a type switch
// case), so variants and union members need no type assertion.
func (g *gen) binds(p *check.Pat, x ast.Expr, narrowed bool) []ast.Stmt {
	var out []ast.Stmt
	if p.Bind != "" {
		val := x
		if p.Kind == check.PatType && !narrowed {
			val = g.narrow(p, x)
		}
		out = append(out, define(name(p.Bind), val))
		if p.Var.Unused {
			out = append(out, assign(ast.NewIdent("_"), name(p.Bind)))
		}
	}
	switch p.Kind {
	case check.PatVariant:
		vx := x
		if !narrowed {
			vx = &ast.TypeAssertExpr{X: x, Type: g.variantType(p.Variant)}
		}
		for _, f := range p.Fields {
			out = append(out, g.binds(f.Pat, g.patternFieldRead(vx, p.Type, p.Variant, f.Name), false)...)
		}
	case check.PatRecord:
		for _, f := range p.Fields {
			out = append(out, g.binds(f.Pat, g.patternFieldRead(x, p.Type, p.Variant, f.Name), false)...)
		}
	case check.PatList:
		for i, e := range p.Elems {
			out = append(out, g.binds(e, listIndex(x, i), false)...)
		}
		if p.Rest != nil {
			rest := &ast.SliceExpr{X: x, Low: &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(len(p.Elems))}}
			out = append(out, g.binds(p.Rest, rest, false)...)
		}
	case check.PatType:
		if p.Sub != nil && len(p.Members) == 1 {
			sx := x
			if !narrowed {
				sx = g.narrow(p, x)
			}
			// In a type switch, a case for a variant narrows further.
			out = append(out, g.binds(p.Sub, sx, narrowed)...)
		}
	}
	return out
}
