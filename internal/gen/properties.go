package gen

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"reflect"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/syntax"
)

// Property tests run code on generated values: a test with parameters
// runs its body on them, and with auto properties on, every function of
// the package whose promises are trusted (rather than proven) is called
// on them, to check the promises and that it does not panic.
//
// Values are generated from a recorded sequence of random choices (see
// _choices in propertyRuntime), and every where clause filters the
// part of the value it applies to as it is generated: a parameter's,
// a list element's, or a record field's. A failing case is shrunk by
// replaying simpler choices through the same generators, so shrunk
// values keep their facts too.

// genFunc is a Go function generating values of a type.
type genFunc struct {
	t  check.Type
	id *ast.Ident
}

var (
	choicesVar = "_c"
	depthVar   = "_depth"
)

// propertyTest generates the property test called title as a Go
// function named goName. params are the generated parameters (with their where
// clauses in cons), and body runs on them. It returns nil and the type
// if a parameter has a type no values are generated for.
func (g *gen) propertyTest(title string, auto bool, params []*syntax.Param, types []check.Type, cons [][]*check.Constraint, body func() []ast.Stmt, goName *ast.Ident) (ast.Decl, check.Type) {
	for _, t := range types {
		if bad := ungeneratable(t, map[check.Type]bool{}); bad != nil {
			return nil, bad
		}
	}
	g.tmp = 0
	g.fnResult = check.Ok
	g.usesProps = true
	g.usesShow = true
	c := ast.NewIdent(choicesVar)
	var stmts []ast.Stmt
	defined := map[string]bool{}
	var deferred []ast.Stmt
	for _, i := range genOrder(params, cons) {
		p := params[i]
		var now []*check.Constraint
		for _, con := range cons[i] {
			if !refersToUndefined(con, defined) {
				now = append(now, con)
				continue
			}
			// Parameters whose facts name each other: checked once all
			// are made.
			what := fmt.Sprintf("%s: %s where %s", p.Name, check.TypeText(types[i], nil), con)
			deferred = append(deferred, g.atPath(name(p.Name), types[i], splitPath(con.Path), func(x ast.Expr, t check.Type) []ast.Stmt {
				if cond := g.propCond(con, x, t); cond != nil {
					return []ast.Stmt{rejectUnless(cond, what)}
				}
				return nil
			})...)
		}
		stmts = append(stmts, define(name(p.Name), g.genValue(types[i], now, &ast.BasicLit{Kind: token.INT, Value: "0"}, p.Name)))
		defined[p.Name] = true
	}
	var shown ast.Expr
	for i, p := range params {
		label := p.Name + " = "
		if i > 0 {
			label = ", " + label
		}
		part := &ast.BinaryExpr{X: strLit(label), Op: token.ADD, Y: &ast.CallExpr{Fun: ast.NewIdent("_show"), Args: []ast.Expr{name(p.Name)}}}
		if shown == nil {
			shown = part
		} else {
			shown = &ast.BinaryExpr{X: shown, Op: token.ADD, Y: part}
		}
	}
	stmts = append(stmts, deferred...)
	if shown != nil {
		stmts = append(stmts, &ast.AssignStmt{Lhs: []ast.Expr{&ast.SelectorExpr{X: c, Sel: ast.NewIdent("args")}}, Tok: token.ASSIGN, Rhs: []ast.Expr{shown}})
	}
	for _, p := range params {
		// A body need not use every parameter.
		stmts = append(stmts, &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent("_")}, Tok: token.ASSIGN, Rhs: []ast.Expr{name(p.Name)}})
	}
	stmts = append(stmts, body()...)
	cases := 0 // the default
	if len(params) == 0 {
		cases = 1
	}
	prop := &ast.FuncLit{
		Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{c}, Type: &ast.StarExpr{X: ast.NewIdent("_choices")}}}}},
		Body: &ast.BlockStmt{List: stmts},
	}
	run := &ast.CallExpr{Fun: ast.NewIdent("_property"), Args: []ast.Expr{
		strLit(title), &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(cases)}, ast.NewIdent(strconv.FormatBool(auto)), prop,
	}}
	return &ast.FuncDecl{
		Name: goName,
		Type: &ast.FuncType{Params: &ast.FieldList{}},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: run}}},
	}, nil
}

// autoPropertyCandidate reports whether fn gets an automatic property
// test: a function of the package being tested whose promises are
// trusted, because it is implemented in unsafe go and promises facts
// about its result, or because its body trusts a fact.
func (g *gen) autoPropertyCandidate(fn *check.Func) bool {
	// A function that needs ambient values reads inputs the property
	// cannot choose.
	if fn.Pkg == nil || !fn.Pkg.Root || fn.Prelude || fn.Test != nil || fn.Class != nil || fn.Of != nil || fn.Synthetic ||
		len(fn.TypeParams) > 0 || fn.Decl.Name == "main" || fn.Decl.IsPred || len(fn.Needs) > 0 {
		return false
	}
	for _, t := range fn.Params {
		if ungeneratable(t, map[check.Type]bool{}) != nil {
			return false
		}
	}
	if fn.Decl.IsGo() {
		return len(fn.ResultConstraints) > 0 || g.hasInvariants(fn.Result, map[check.Type]bool{})
	}
	return len(findNodes[*syntax.TrustStmt](fn.Decl.Body)) > 0
}

// autoProperty generates the automatic property test of fn: it calls
// fn on generated arguments. Test mode checks its promises.
func (g *gen) autoProperty(fn *check.Func, goName *ast.Ident) ast.Decl {
	body := func() []ast.Stmt {
		var args []ast.Expr
		for _, p := range fn.Decl.Params {
			args = append(args, name(p.Name))
		}
		if fn.TrackCaller {
			args = append(args, at(fn.Decl.Pos))
		}
		call := &ast.CallExpr{Fun: g.funcName(fn), Args: args}
		if fn.Result == check.Ok || fn.Result == check.Never {
			return []ast.Stmt{&ast.ExprStmt{X: call}}
		}
		return []ast.Stmt{&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent("_")}, Tok: token.ASSIGN, Rhs: []ast.Expr{call}}}
	}
	originalBody := body
	body = func() []ast.Stmt {
		var stmts []ast.Stmt
		if fn.Requires != nil {
			pre, cond := g.value(fn.Requires)
			stmts = append(stmts, pre...)
			stmts = append(stmts, rejectUnless(cond, "function-level where requirements"))
		}
		return append(stmts, originalBody()...)
	}
	decl, _ := g.propertyTest(autoPropertyName(fn), true, fn.Decl.Params, fn.Params, fn.ParamConstraints, body, goName)
	return decl
}

// genOrder is the order to generate parameters in: each after the
// ones its facts name (`hi: Int where atLeast(lo)` after lo), and
// otherwise as declared. Parameters whose facts name each other are
// taken in declared order.
func genOrder(params []*syntax.Param, cons [][]*check.Constraint) []int {
	var order []int
	done := make([]bool, len(params))
	defined := map[string]bool{}
	for len(order) < len(params) {
		next := -1
		for i := range params {
			if done[i] {
				continue
			}
			if next < 0 {
				next = i
			}
			ready := true
			for _, con := range cons[i] {
				ready = ready && !refersToUndefined(con, defined)
			}
			if ready {
				next = i
				break
			}
		}
		done[next] = true
		order = append(order, next)
		defined[params[next].Name] = true
	}
	return order
}

// autoPropertyName is the name of fn's automatic property test:
// "property validate", or "property Account.close" for a method.
func autoPropertyName(fn *check.Func) string {
	if fn.Decl.IsMethod {
		return "property " + check.TypeText(fn.Params[0], nil) + "." + fn.Decl.Name
	}
	return "property " + fn.Decl.Name
}

// refersToUndefined reports whether con names a parameter not yet
// generated.
func refersToUndefined(con *check.Constraint, defined map[string]bool) bool {
	for _, alt := range con.Or {
		if refersToUndefined(alt, defined) {
			return true
		}
	}
	for _, a := range con.Args {
		if a.Const == nil && !defined[a.Param] {
			return true
		}
	}
	return false
}

// ungeneratable returns a type inside t that no values are generated
// for, or nil if there is none.
func ungeneratable(t check.Type, seen map[check.Type]bool) check.Type {
	if seen[t] {
		return nil
	}
	seen[t] = true
	fields := func(fs []*check.Field) check.Type {
		for _, f := range fs {
			if f.Computed {
				continue
			}
			if bad := ungeneratable(f.Type, seen); bad != nil {
				return bad
			}
		}
		return nil
	}
	switch t := t.(type) {
	case *check.Record:
		return fields(t.Fields)
	case *check.Sealed:
		for _, v := range t.Variants {
			if bad := fields(v.Fields); bad != nil {
				return bad
			}
		}
		return nil
	case *check.Union:
		for _, m := range t.Members {
			if m == check.Ok {
				continue
			}
			if bad := ungeneratable(m, seen); bad != nil {
				return bad
			}
		}
		return nil
	case *check.List:
		return ungeneratable(t.Elem, seen)
	case *check.Map:
		if bad := ungeneratable(t.Key, seen); bad != nil {
			return bad
		}
		return ungeneratable(t.Value, seen)
	}
	if t == check.Bool || t == check.String || t == check.Rune || check.IsNumeric(t) {
		return nil
	}
	return t
}

// genValue is a Go expression generating a value of type t that meets
// cons. depth is how deeply the value is nested, and what names it for
// messages.
func (g *gen) genValue(t check.Type, cons []*check.Constraint, depth ast.Expr, what string) ast.Expr {
	var top, nested []*check.Constraint
	top = append(top, check.TypeConstraints(t)...)
	for _, con := range cons {
		if con.Path == "" {
			top = append(top, con)
		} else {
			nested = append(nested, con)
		}
	}
	var base ast.Expr
	switch {
	case t == check.Bool || t == check.String || t == check.Rune || check.IsNumeric(t):
		base = g.genBasic(t, top)
	case len(nested) == 0:
		base = &ast.CallExpr{Fun: g.genFuncFor(t), Args: []ast.Expr{ast.NewIdent(choicesVar), depth}}
	default:
		// Facts inside the value: generated in place.
		base = g.funcLit(t, g.genBody(t, nested, depth, what))
	}
	if len(top) == 0 {
		return base
	}
	// Draw until the value has its facts.
	v, i := ast.NewIdent("_v"), ast.NewIdent("_i")
	var cond ast.Expr
	var texts []string
	for _, con := range top {
		c := g.propCond(con, v, t)
		if c == nil {
			continue
		}
		texts = append(texts, con.String())
		if cond == nil {
			cond = c
		} else {
			cond = &ast.BinaryExpr{X: paren(cond), Op: token.LAND, Y: paren(c)}
		}
	}
	if cond == nil {
		return base
	}
	msg := fmt.Sprintf("no value for %s that is %s in 100 tries", what, strings.Join(texts, " and "))
	loop := &ast.ForStmt{
		Init: define(i, &ast.BasicLit{Kind: token.INT, Value: "0"}),
		Post: &ast.IncDecStmt{X: i, Tok: token.INC},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			define(v, base),
			&ast.IfStmt{Cond: cond, Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{v}}}}},
			&ast.IfStmt{
				Cond: &ast.BinaryExpr{X: i, Op: token.EQL, Y: &ast.BasicLit{Kind: token.INT, Value: "99"}},
				Body: &ast.BlockStmt{List: []ast.Stmt{rejectStmt(msg)}},
			},
		}},
	}
	return g.funcLit(t, []ast.Stmt{loop})
}

// funcLit is a Go function literal returning a value of type t, called
// at once.
func (g *gen) funcLit(t check.Type, body []ast.Stmt) ast.Expr {
	return &ast.CallExpr{Fun: &ast.FuncLit{
		Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: g.goType(t)}}}},
		Body: &ast.BlockStmt{List: body},
	}}
}

func rejectStmt(why string) ast.Stmt {
	return &ast.ExprStmt{X: &ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: ast.NewIdent(choicesVar), Sel: ast.NewIdent("reject")},
		Args: []ast.Expr{strLit(why)},
	}}
}

// rejectUnless rejects the case if cond does not hold.
func rejectUnless(cond ast.Expr, what string) ast.Stmt {
	return &ast.IfStmt{
		Cond: &ast.UnaryExpr{Op: token.NOT, X: paren(cond)},
		Body: &ast.BlockStmt{List: []ast.Stmt{rejectStmt("the case does not meet " + what)}},
	}
}

// propCond is the Go condition that con holds for x, and makes sure
// the predicates it calls are emitted.
func (g *gen) propCond(con *check.Constraint, x ast.Expr, t check.Type) ast.Expr {
	g.propRoots = append(g.propRoots, constraintPreds(con)...)
	return g.constraintCond(con, x, t)
}

// genFuncFor returns the Go function generating values of t (without
// facts beyond those of its record fields), declaring it the first time.
func (g *gen) genFuncFor(t check.Type) *ast.Ident {
	for _, f := range g.genFuncs {
		if check.Identical(f.t, t) {
			return f.id
		}
	}
	id := ast.NewIdent("_gen" + strconv.Itoa(len(g.genFuncs)+1))
	g.genFuncs = append(g.genFuncs, genFunc{t: t, id: id})
	tmp := g.tmp
	body := g.genBody(t, nil, ast.NewIdent(depthVar), t.String())
	g.tmp = tmp
	g.extraFuncs = append(g.extraFuncs, &ast.FuncDecl{
		Name: id,
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{
				{Names: []*ast.Ident{ast.NewIdent(choicesVar)}, Type: &ast.StarExpr{X: ast.NewIdent("_choices")}},
				{Names: []*ast.Ident{ast.NewIdent(depthVar)}, Type: ast.NewIdent("int")},
			}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: g.goType(t)}}},
		},
		Body: &ast.BlockStmt{List: body},
	})
	return id
}

// genBody is the statements of a function returning a generated value
// of t, a list, map, record, sealed type, or union, whose parts meet
// the nested constraints.
func (g *gen) genBody(t check.Type, nested []*check.Constraint, depth ast.Expr, what string) []ast.Stmt {
	deeper := &ast.BinaryExpr{X: depth, Op: token.ADD, Y: &ast.BasicLit{Kind: token.INT, Value: "1"}}
	// within is the constraints of nested that apply to the part step
	// leads to, with the step taken off their paths.
	within := func(step string) []*check.Constraint {
		var out []*check.Constraint
		for _, con := range nested {
			if steps := splitPath(con.Path); steps[0] == step {
				cp := *con
				cp.Path = strings.TrimPrefix(con.Path, "."+step)
				out = append(out, &cp)
			}
		}
		return out
	}
	c := ast.NewIdent(choicesVar)
	ret := func(x ast.Expr) []ast.Stmt { return []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{x}}} }
	fields := func(typ ast.Expr, owner string, fs []*check.Field, nominal []*check.Constraint) ast.Expr {
		if len(independentFields(fs)) != len(fs) {
			return g.genComputedRecord(t, typ, owner, fs, nominal, deeper, within)
		}
		lit := &ast.CompositeLit{Type: typ}
		params := make([]*syntax.Param, len(fs))
		constraints := make([][]*check.Constraint, len(fs))
		values := map[string]string{}
		for i, field := range fs {
			params[i] = &syntax.Param{Name: field.Name}
			constraints[i] = append(append([]*check.Constraint{}, field.Constraints...), within(field.Name)...)
			values[field.Name] = g.newTmp().Name
		}
		defined := map[string]bool{}
		var body, deferred []ast.Stmt
		for _, i := range genOrder(params, constraints) {
			field := fs[i]
			var now []*check.Constraint
			for _, con := range constraints[i] {
				bound := fieldConstraint(con, func(n string) string { return values[n] })
				if !refersToUndefined(con, defined) {
					now = append(now, bound)
					continue
				}
				what := owner + "." + field.Name + " where " + con.String()
				deferred = append(deferred, g.atPath(ast.NewIdent(values[field.Name]), field.Type, splitPath(con.Path), func(x ast.Expr, t check.Type) []ast.Stmt {
					if cond := g.propCond(bound, x, t); cond != nil {
						return []ast.Stmt{rejectUnless(cond, what)}
					}
					return nil
				})...)
			}
			body = append(body, define(ast.NewIdent(values[field.Name]), g.genValue(field.Type, now, deeper, owner+"."+field.Name)))
			defined[field.Name] = true
		}
		for _, field := range fs {
			lit.Elts = append(lit.Elts, &ast.KeyValueExpr{Key: name(field.Name), Value: g.fieldResolved(ast.NewIdent(values[field.Name]), field)})
		}
		body = append(body, deferred...)
		value := g.newTmp()
		body = append(body, define(value, lit))
		for _, con := range nominal {
			if cond := g.propCond(con, value, t); cond != nil {
				body = append(body, rejectUnless(cond, owner+" where "+con.String()))
			}
		}
		body = append(body, ret(value)...)
		return &ast.CallExpr{Fun: &ast.FuncLit{Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: typ}}}}, Body: &ast.BlockStmt{List: body}}}
	}

	switch t := t.(type) {
	case *check.List:
		elem := g.genValue(t.Elem, within("[]"), deeper, "elements of "+what)
		return ret(&ast.CallExpr{
			Fun:  &ast.IndexExpr{X: ast.NewIdent("_genList"), Index: g.goType(t.Elem)},
			Args: []ast.Expr{c, depth, g.elemFunc(t.Elem, elem)},
		})
	case *check.Map:
		key := g.genValue(t.Key, nil, deeper, "keys of "+what)
		val := g.genValue(t.Value, nil, deeper, "values of "+what)
		ks := &ast.CallExpr{
			Fun:  &ast.IndexListExpr{X: ast.NewIdent("_genPairs"), Indices: []ast.Expr{g.goType(t.Key), g.goType(t.Value)}},
			Args: []ast.Expr{c, depth, g.elemFunc(t.Key, key), g.elemFunc(t.Value, val)},
		}
		k, v := ast.NewIdent("_ks"), ast.NewIdent("_vs")
		return []ast.Stmt{
			&ast.AssignStmt{Lhs: []ast.Expr{k, v}, Tok: token.DEFINE, Rhs: []ast.Expr{ks}},
			&ast.ReturnStmt{Results: []ast.Expr{&ast.CallExpr{
				Fun:  &ast.IndexListExpr{X: ast.NewIdent("_mapOf"), Indices: []ast.Expr{g.goType(t.Key), g.goType(t.Value)}},
				Args: []ast.Expr{k, v},
			}}},
		}
	case *check.Record:
		return ret(fields(g.goType(t), t.Name, t.Fields, nil))
	case *check.Sealed:
		// Variants that do not contain the type again come first: they
		// are the simplest, and the only ones taken below maxGenDepth.
		var order []*check.Variant
		for _, v := range t.Variants {
			if !variantRecurses(v, t) {
				order = append(order, v)
			}
		}
		terminal := len(order)
		for _, v := range t.Variants {
			if variantRecurses(v, t) {
				order = append(order, v)
			}
		}
		var cases []ast.Stmt
		for i, v := range order {
			cc := &ast.CaseClause{Body: ret(fields(g.variantType(v), t.Name+"."+v.Name, v.Fields, v.Constraints))}
			if i < len(order)-1 {
				cc.List = []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(i)}}
			}
			cases = append(cases, cc)
		}
		return []ast.Stmt{&ast.SwitchStmt{
			Tag: &ast.CallExpr{
				Fun: &ast.SelectorExpr{X: c, Sel: ast.NewIdent("variant")},
				Args: []ast.Expr{depth, &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(len(order))},
					&ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(terminal)}},
			},
			Body: &ast.BlockStmt{List: cases},
		}}
	case *check.Union:
		var cases []ast.Stmt
		for i, m := range t.Members {
			var x ast.Expr
			if m == check.Ok {
				g.usesOk = true
				x = &ast.CompositeLit{Type: ast.NewIdent("_Ok")}
			} else {
				x = g.genValue(m, nil, deeper, what)
			}
			cc := &ast.CaseClause{Body: ret(x)}
			if i < len(t.Members)-1 {
				cc.List = []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(i)}}
			}
			cases = append(cases, cc)
		}
		return []ast.Stmt{&ast.SwitchStmt{
			Tag: &ast.CallExpr{
				Fun: &ast.SelectorExpr{X: c, Sel: ast.NewIdent("variant")},
				Args: []ast.Expr{depth, &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(len(t.Members))},
					&ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(len(t.Members))}},
			},
			Body: &ast.BlockStmt{List: cases},
		}}
	}
	panic(fmt.Sprintf("no generator for %s", t))
}

// elemFunc wraps x, generating a value of type t, in a function.
func (g *gen) elemFunc(t check.Type, x ast.Expr) ast.Expr {
	return &ast.FuncLit{
		Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: g.goType(t)}}}},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{x}}}},
	}
}

// variantRecurses reports whether v can hold a value of its sealed type
// t again, other than inside a list or map (which can be empty).
func variantRecurses(v *check.Variant, t *check.Sealed) bool {
	seen := map[check.Type]bool{}
	var contains func(x check.Type) bool
	contains = func(x check.Type) bool {
		if check.Identical(x, t) {
			return true
		}
		if seen[x] {
			return false
		}
		seen[x] = true
		switch x := x.(type) {
		case *check.Record:
			for _, f := range x.Fields {
				if !f.Computed && contains(f.Type) {
					return true
				}
			}
		case *check.Sealed:
			// It may stop at a variant that does not contain t.
			for _, xv := range x.Variants {
				recurses := false
				for _, f := range xv.Fields {
					recurses = recurses || (!f.Computed && contains(f.Type))
				}
				if !recurses {
					return false
				}
			}
			return len(x.Variants) > 0
		case *check.Union:
			for _, m := range x.Members {
				if contains(m) {
					return true
				}
			}
		}
		return false
	}
	for _, f := range v.Fields {
		if !f.Computed && contains(f.Type) {
			return true
		}
	}
	return false
}

// genBasic generates a Bool, String or number. The pool of values tried
// often holds edge cases, and the constants that cons and their
// predicates' bodies mention, and their neighbours.
func (g *gen) genBasic(t check.Type, cons []*check.Constraint) ast.Expr {
	c := ast.NewIdent(choicesVar)
	call := func(fn string, args ...ast.Expr) ast.Expr {
		return &ast.CallExpr{Fun: &ast.SelectorExpr{X: c, Sel: ast.NewIdent(fn)}, Args: args}
	}
	ints, floats, strs := hints(cons)
	switch {
	case t == check.Bool:
		return call("bool")
	case t == check.Rune:
		g.goType(t)
		return call("rune")
	case t == check.String:
		pool := &ast.CompositeLit{Type: &ast.ArrayType{Elt: ast.NewIdent("string")}}
		seen := map[string]bool{}
		for _, s := range append(append([]string{}, stringPool...), strs...) {
			if !seen[s] {
				seen[s] = true
				pool.Elts = append(pool.Elts, strLit(s))
			}
		}
		return call("string", pool)
	case check.IsFloat(t):
		pool := &ast.CompositeLit{Type: &ast.ArrayType{Elt: ast.NewIdent("float64")}}
		for _, f := range append(append([]float64{}, floatPool...), floats...) {
			pool.Elts = append(pool.Elts, &ast.BasicLit{Kind: token.FLOAT, Value: strconv.FormatFloat(f, 'g', -1, 64)})
		}
		x := call("float", pool)
		if t == check.Float32 {
			return &ast.CallExpr{Fun: ast.NewIdent("float32"), Args: []ast.Expr{x}}
		}
		return x
	}
	// Integers: the pool's values in range, simplest first.
	lo, hi := check.IntRange(t)
	var vals []constant.Value
	for _, n := range intPool {
		vals = append(vals, constant.MakeInt64(n))
	}
	vals = append(vals, ints...)
	vals = append(vals, lo, hi)
	var pool []constant.Value
	for _, v := range vals {
		if constant.Compare(v, token.LSS, lo) || constant.Compare(v, token.GTR, hi) {
			continue
		}
		dup := false
		for _, p := range pool {
			dup = dup || constant.Compare(p, token.EQL, v)
		}
		if !dup {
			pool = append(pool, v)
		}
	}
	unsigned := constant.Sign(lo) >= 0
	elt := "int64"
	if unsigned {
		elt = "uint64"
	}
	lit := &ast.CompositeLit{Type: &ast.ArrayType{Elt: ast.NewIdent(elt)}}
	for _, v := range pool {
		lit.Elts = append(lit.Elts, &ast.BasicLit{Kind: token.INT, Value: v.ExactString()})
	}
	var x ast.Expr
	if unsigned {
		x = call("uint", &ast.BasicLit{Kind: token.INT, Value: hi.ExactString()}, lit)
	} else {
		x = call("int", &ast.BasicLit{Kind: token.INT, Value: lo.ExactString()}, &ast.BasicLit{Kind: token.INT, Value: hi.ExactString()}, lit)
	}
	if goName := basicGoNames[t]; goName != elt {
		x = &ast.CallExpr{Fun: ast.NewIdent(goName), Args: []ast.Expr{x}}
	}
	return x
}

// hints are the constants that cons mention, as arguments or in their
// predicates' bodies, and their neighbours: values near the edges of
// what the constraints accept.
func hints(cons []*check.Constraint) (ints []constant.Value, floats []float64, strs []string) {
	add := func(v constant.Value) {
		switch v.Kind() {
		case constant.Int:
			one := constant.MakeInt64(1)
			ints = append(ints, v, constant.BinaryOp(v, token.SUB, one), constant.BinaryOp(v, token.ADD, one), constant.UnaryOp(token.SUB, v, 0))
			f, _ := constant.Float64Val(v)
			floats = append(floats, f)
		case constant.Float:
			f, _ := constant.Float64Val(v)
			floats = append(floats, f, -f)
		case constant.String:
			s := constant.StringVal(v)
			strs = append(strs, s, s+"a", "a"+s)
		}
	}
	var visit func(con *check.Constraint)
	visit = func(con *check.Constraint) {
		for _, alt := range con.Or {
			visit(alt)
		}
		for _, a := range con.Args {
			if a.Const != nil {
				add(a.Const)
			}
		}
		if con.Pred != nil && con.Pred.Decl != nil && con.Pred.Decl.Body != nil {
			for _, lit := range findNodes[*syntax.IntLit](con.Pred.Decl.Body) {
				if v := constant.MakeFromLiteral(lit.Text, token.INT, 0); v.Kind() == constant.Int {
					add(v)
				}
			}
			for _, lit := range findNodes[*syntax.StringLit](con.Pred.Decl.Body) {
				add(constant.MakeString(lit.Value))
			}
		}
	}
	for _, con := range cons {
		visit(con)
	}
	return ints, floats, strs
}

// findNodes lists the nodes of type T in a syntax tree.
func findNodes[T any](root any) []T {
	var out []T
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if n, ok := v.Interface().(T); ok {
				out = append(out, n)
			}
			walk(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(root))
	return out
}

// propertyRuntime runs property tests, and generates their values.
const propertyRuntime = `package main

import (
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"os"
	"strconv"
)

// _choices are the random choices one case of a property test makes.
// Its values are generated from them, so a case can be replayed, and
// shrunk by replaying simpler choices: 0 is always the simplest.
type _choices struct {
	prefix []uint64   // choices to replay
	rng    *rand.Rand // past prefix: random choices, or 0 when shrinking
	drawn  []uint64
	size   int    // how large lists and numbers grow
	args   string // the generated parameters, shown
}

// _rejected is the panic of a case whose values could not be made to
// meet their where clauses.
type _rejected struct{ why string }

func (c *_choices) reject(why string) { panic(_rejected{why}) }

// draw chooses a number below n, or any number if n is 0.
func (c *_choices) draw(n uint64) uint64 {
	if len(c.drawn) >= 100000 {
		c.reject("a case made too many choices")
	}
	var v uint64
	if i := len(c.drawn); i < len(c.prefix) {
		v = c.prefix[i]
		if n > 0 && v >= n {
			v = n - 1
		}
	} else if c.rng != nil {
		v = c.rng.Uint64()
		if n > 0 {
			v %= n
		}
	}
	c.drawn = append(c.drawn, v)
	return v
}

// variant chooses one of n variants. Below the deepest nesting, only
// the first terminal ones (which do not nest further) are chosen.
func (c *_choices) variant(depth, n, terminal int) int {
	if depth >= 20 {
		c.reject("a value nested too deeply")
	}
	if depth >= 5 && terminal > 0 {
		n = terminal
	}
	return int(c.draw(uint64(n)))
}

// more decides whether a list or map with n elements gets another.
func (c *_choices) more(depth, n int) bool {
	if depth >= 5 || n >= c.size>>depth {
		return false
	}
	return c.draw(8) != 0
}

func _genList[E any](c *_choices, depth int, elem func() E) []E {
	xs := []E{}
	for c.more(depth, len(xs)) {
		xs = append(xs, elem())
	}
	return xs
}

func _genPairs[K, V any](c *_choices, depth int, key func() K, val func() V) ([]K, []V) {
	var ks []K
	var vs []V
	for c.more(depth, len(ks)) {
		ks = append(ks, key())
		vs = append(vs, val())
	}
	return ks, vs
}

func (c *_choices) bool() bool { return c.draw(2) == 1 }

// small is a number near 0: 0, 1, -1, 2, -2, ...
func (c *_choices) small() int64 {
	u := c.draw(uint64(2*c.size*c.size + 3))
	if u%2 == 1 {
		return int64(u/2) + 1
	}
	return -int64(u / 2)
}

func (c *_choices) int(lo, hi int64, pool []int64) int64 {
	switch c.draw(4) {
	case 0, 1:
		v := c.small()
		if lo >= 0 && v < 0 {
			v = -v
		}
		return min(max(v, lo), hi)
	case 2:
		return pool[c.draw(uint64(len(pool)))]
	}
	span := uint64(hi) - uint64(lo)
	if span == math.MaxUint64 {
		return int64(c.draw(0))
	}
	return lo + int64(c.draw(span+1))
}

func (c *_choices) uint(hi uint64, pool []uint64) uint64 {
	switch c.draw(4) {
	case 0, 1:
		v := c.small()
		if v < 0 {
			v = -v
		}
		return min(uint64(v), hi)
	case 2:
		return pool[c.draw(uint64(len(pool)))]
	}
	if hi == math.MaxUint64 {
		return c.draw(0)
	}
	return c.draw(hi + 1)
}

var _floatSpecials = []float64{math.Copysign(0, -1), math.Inf(1), math.Inf(-1), math.NaN()}

func (c *_choices) float(pool []float64) float64 {
	switch c.draw(5) {
	case 0:
		return float64(c.small())
	case 1:
		return pool[c.draw(uint64(len(pool)))]
	case 2:
		return float64(c.small()) / 100
	case 4:
		return _floatSpecials[c.draw(uint64(len(_floatSpecials)))]
	}
	return (float64(c.draw(1<<53))/(1<<53)*2 - 1) * 1e12
}

var _alphabet = []string{"a", "b", "c", "x", "y", "z", " ", "A", "Z", "0", "9", "-", "_", ".", "é", "日", "\n", "\t", "\"", "\\", "😀"}

func (c *_choices) rune() _Rune {
 n := c.int(0, 0x10ffff-0x800, []int64{0, 'a', 'å', '界', 0xfffd, 0x10ffff-0x800})
 if n >= 0xd800 { n += 0x800 }
 return _Rune(n)
}

func (c *_choices) string(pool []string) string {
	if c.draw(3) == 2 {
		return pool[c.draw(uint64(len(pool)))]
	}
	s := ""
	for n := 0; c.more(0, n); n++ {
		s += _alphabet[c.draw(uint64(len(_alphabet)))]
	}
	return s
}

// _property runs a property test: prop on many cases of generated
// values (cases of them, or by default 100 or $BORK_CASES), from a seed
// that is the test's name hashed, or $BORK_SEED. A failing case is
// shrunk, and reported with the seed that reproduces it. What the cases
// print is not shown (when tests run one at a time). An automatic property whose parameters cannot be
// generated is skipped rather than failed.
func _property(name string, cases int, auto bool, prop func(*_choices)) {
	fixed := cases != 0
	if n, err := strconv.Atoi(os.Getenv("BORK_CASES")); err == nil && n > 0 && cases == 0 {
		cases = n
	}
	if cases == 0 {
		cases = 100
	}
	seed, err := strconv.ParseInt(os.Getenv("BORK_SEED"), 10, 64)
	if err != nil {
		h := fnv.New64a()
		h.Write([]byte(name))
		seed = int64(h.Sum64() >> 1)
	}
	// Run one at a time, its cases' output is not shown. (Tests running
	// in parallel share os.Stdout, so it is left alone.)
	if null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil && !_tests.parallel {
		stdout := os.Stdout
		os.Stdout = null
		defer func() {
			os.Stdout = stdout
			_ = null.Close()
		}()
	}
	rng := rand.New(rand.NewSource(seed))
	passed, rejected := 0, 0
	for i := 0; passed < cases; i++ {
		c := &_choices{rng: rng, size: 1 + min(i, cases)*30/cases}
		status, msg := _tryCase(prop, c)
		switch status {
		case 0:
			passed++
		case 2:
			rejected++
			if rejected > cases && auto {
				panic(_skip{fmt.Sprintf("could not generate the parameters: %s", msg)})
			}
			if rejected > cases {
				panic(fmt.Sprintf("could not generate the parameters: %s (%d of %d cases were rejected)", msg, rejected, i+1))
			}
		default:
			shrunk := &_choices{prefix: _shrink(prop, c.size, c.drawn), size: c.size}
			if s, m := _tryCase(prop, shrunk); s == 1 {
				msg = m
			} else {
				shrunk = c
			}
			report := msg
			if shrunk.args != "" {
				report += "\nfor " + shrunk.args
				if shrunk.args != c.args {
					report += " (shrunk from " + c.args + ")"
				}
			}
			repro := fmt.Sprintf("--seed %d", seed)
			if !fixed && cases != 100 {
				// The values' sizes depend on the number of cases.
				repro += fmt.Sprintf(" --cases %d", cases)
			}
			panic(report + fmt.Sprintf("\ncase %d, seed %d (bork test %s)", i+1, seed, repro))
		}
	}
}

// _tryCase runs prop on c: status 0 if it passes, 1 if it fails (with
// the message), and 2 if the case is rejected (with the reason).
func _tryCase(prop func(*_choices), c *_choices) (status int, msg string) {
	defer func() {
		if r := recover(); r != nil {
			if rej, ok := r.(_rejected); ok {
				status, msg = 2, rej.why
			} else {
				status, msg = 1, fmt.Sprint(r)
			}
		}
	}()
	prop(c)
	return 0, ""
}

// _shrink looks for simpler choices than best that still fail: fewer,
// or the same number but smaller. It removes and zeroes runs of them,
// and lowers each one, until nothing helps.
func _shrink(prop func(*_choices), size int, best []uint64) []uint64 {
	budget := 2000
	try := func(cand []uint64) bool {
		if budget == 0 {
			return false
		}
		budget--
		c := &_choices{prefix: cand, size: size}
		if status, _ := _tryCase(prop, c); status == 1 && _simpler(c.drawn, best) {
			best = c.drawn
			return true
		}
		return false
	}
	for improved := true; improved && budget > 0; {
		improved = false
		for k := 8; k >= 1; k /= 2 {
			for i := len(best) - k; i >= 0; i-- {
				if i+k <= len(best) && try(append(append([]uint64{}, best[:i]...), best[i+k:]...)) {
					improved = true
				}
			}
		}
		for k := 8; k >= 1; k /= 2 {
			for i := 0; i+k <= len(best); i++ {
				cand := append([]uint64{}, best...)
				zero := true
				for j := i; j < i+k; j++ {
					zero = zero && cand[j] == 0
					cand[j] = 0
				}
				if !zero && try(cand) {
					improved = true
				}
			}
		}
		for i := 0; i < len(best); i++ {
			// The smallest choices first: a choice's values need not
			// fail in order (small() alternates signs).
			for v := uint64(0); i < len(best) && v < min(best[i], 8); v++ {
				cand := append([]uint64{}, best...)
				cand[i] = v
				if try(cand) {
					improved = true
					break
				}
			}
			if i >= len(best) {
				break
			}
			lo, hi := uint64(0), best[i]
			for lo < hi && i < len(best) {
				mid := lo + (hi-lo)/2
				cand := append([]uint64{}, best...)
				cand[i] = mid
				if try(cand) {
					improved = true
					hi = mid
				} else {
					lo = mid + 1
				}
			}
		}
	}
	return best
}

// _simpler reports whether choices a are simpler than b: fewer, or as
// many but smaller at the first difference.
func _simpler(a, b []uint64) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
`

func (g *gen) genComputedRecord(ownerType check.Type, typ ast.Expr, owner string, fields []*check.Field, nominal []*check.Constraint, depth ast.Expr, within func(string) []*check.Constraint) ast.Expr {
	lit := &ast.CompositeLit{Type: typ}
	var body []ast.Stmt
	for _, field := range fields {
		if field.Computed {
			continue
		}
		var simple []*check.Constraint
		for _, constraint := range append(append([]*check.Constraint{}, field.Constraints...), within(field.Name)...) {
			if !constraint.HasSiblingArgs() {
				simple = append(simple, constraint)
			}
		}
		value := g.newTmp()
		body = append(body, define(value, g.genValue(field.Type, simple, depth, owner+"."+field.Name)))
		lit.Elts = append(lit.Elts, &ast.KeyValueExpr{Key: name(field.Name), Value: g.fieldResolved(value, field)})
	}
	value := g.newTmp()
	body = append(body, define(value, lit))
	body = append(body, g.computedCells(value, fields, ownerType)...)
	for _, field := range fields {
		for _, constraint := range append(append([]*check.Constraint{}, field.Constraints...), within(field.Name)...) {
			bound := fieldConstraint(constraint, func(sibling string) string {
				for _, candidate := range fields {
					if candidate.Name == sibling {
						return g.fieldReadText(value.Name, candidate)
					}
				}
				return sibling
			})
			body = append(body, g.atPath(g.fieldRead(value, field), field.Type, splitPath(constraint.Path), func(x ast.Expr, t check.Type) []ast.Stmt {
				if cond := g.propCond(bound, x, t); cond != nil {
					return []ast.Stmt{rejectUnless(cond, owner+"."+field.Name+" where "+constraint.String())}
				}
				return nil
			})...)
		}
	}
	for _, constraint := range nominal {
		if cond := g.propCond(constraint, value, ownerType); cond != nil {
			body = append(body, rejectUnless(cond, owner+" where "+constraint.String()))
		}
	}
	body = append(body, &ast.ReturnStmt{Results: []ast.Expr{value}})
	return &ast.CallExpr{Fun: &ast.FuncLit{Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: typ}}}}, Body: &ast.BlockStmt{List: body}}}
}
