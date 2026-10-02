package gen

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/syntax"
)

// Tests generates a Go program that runs the package's tests and
// reports the results. It is built in test mode: facts the compiler
// takes on trust (`trust`, and what `unsafe go` functions promise) are
// checked at runtime, so a wrong one fails the test that reaches it.
// Inference rules, also taken on trust, get property tests that look
// for counterexamples (see ruleTest).
func Tests(files []*syntax.File, info *check.Info) ([]byte, error) {
	g := newGen(info)
	g.testMode = true
	g.usesTests = true
	var roots []*check.Func
	list := &ast.CompositeLit{Type: &ast.ArrayType{Elt: ast.NewIdent("_test")}}
	for i, fn := range info.Tests {
		roots = append(roots, fn.Calls...)
		goName := ast.NewIdent("_test" + strconv.Itoa(i+1))
		g.extraFuncs = append(g.extraFuncs, g.testFunc(fn, goName))
		list.Elts = append(list.Elts, &ast.CompositeLit{Elts: []ast.Expr{
			&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(fn.Test.Name)},
			goName,
		}})
	}
	for i, r := range info.Rules {
		if r.Pkg != nil && !r.Pkg.Root {
			continue // an imported package's rules are tested with it
		}
		goName := ast.NewIdent("_rule" + strconv.Itoa(i+1))
		decl, untried := g.ruleTest(r, goName)
		if decl == nil {
			list.Elts = append(list.Elts, &ast.CompositeLit{Elts: []ast.Expr{
				strLit(fmt.Sprintf("rule %s (no values are generated for %s)", r.Decl.Name, untried)),
				ast.NewIdent("nil"),
			}})
			continue
		}
		for _, atoms := range [][]*check.RuleAtom{r.Premises, r.Conclusions} {
			for _, a := range atoms {
				roots = append(roots, a.Pred)
			}
		}
		g.extraFuncs = append(g.extraFuncs, decl)
		list.Elts = append(list.Elts, &ast.CompositeLit{Elts: []ast.Expr{
			strLit("rule " + r.Decl.Name),
			goName,
		}})
	}
	main := &ast.FuncDecl{
		Name: ast.NewIdent("main"),
		Type: &ast.FuncType{Params: &ast.FieldList{}},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("_runTests"), Args: []ast.Expr{list}}}}},
	}
	return generate(g, files, roots, main)
}

// testFunc generates a test's body as a function.
func (g *gen) testFunc(fn *check.Func, goName *ast.Ident) ast.Decl {
	g.tmp = 0
	g.fnResult = check.Unit
	return &ast.FuncDecl{
		Name: goName,
		Type: &ast.FuncType{Params: &ast.FieldList{}},
		Body: &ast.BlockStmt{List: g.blockInto(fn.Decl.Body, sink{})},
	}
}

// at is a position as a Go string literal, for messages.
func at(pos fmt.Stringer) ast.Expr {
	return &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(pos.String())}
}

// trustCheck checks a `trust p(x)` in test mode.
func (g *gen) trustCheck(s *syntax.TrustStmt) []ast.Stmt {
	stmts, cond := g.value(s.Call)
	if cond == nil {
		return stmts
	}
	msg := fmt.Sprintf("%s: trusted fact does not hold: ", s.Pos)
	g.usesShow = true
	text := &ast.BinaryExpr{X: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(msg + exprText(s.Call))}, Op: token.ADD, Y: g.argsShown(s.Call)}
	return append(stmts, &ast.IfStmt{
		Cond: &ast.UnaryExpr{Op: token.NOT, X: paren(cond)},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{text}}}}},
	})
}

// argsShown renders the first argument of a trusted call for the
// message: " (x = 0)".
func (g *gen) argsShown(call *syntax.Call) ast.Expr {
	if len(call.Args) == 0 {
		return &ast.BasicLit{Kind: token.STRING, Value: `""`}
	}
	_, x := g.value(call.Args[0])
	if x == nil {
		return &ast.BasicLit{Kind: token.STRING, Value: `""`}
	}
	show := &ast.CallExpr{Fun: ast.NewIdent("_show"), Args: []ast.Expr{g.typed(x, g.info.Types[call.Args[0]])}}
	return &ast.BinaryExpr{
		X:  &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(" (" + exprText(call.Args[0]) + " = ")},
		Op: token.ADD,
		Y:  &ast.BinaryExpr{X: show, Op: token.ADD, Y: &ast.BasicLit{Kind: token.STRING, Value: `")"`}},
	}
}

// exprText shows a simple expression as written, for messages.
func exprText(x syntax.Expr) string {
	switch x := x.(type) {
	case *syntax.Ident:
		return x.Name
	case *syntax.Selector:
		return exprText(x.X) + "." + x.Name
	case *syntax.Call:
		args := make([]string, len(x.Args))
		for i, a := range x.Args {
			args[i] = exprText(a)
		}
		return exprText(x.Fun) + "(" + strings.Join(args, ", ") + ")"
	case *syntax.StringLit:
		return strconv.Quote(x.Value)
	case *syntax.IntLit:
		return x.Text
	}
	return "..."
}

// checkedWrapper generates, in test mode, the function fn (implemented
// in unsafe go, now named _unchecked_<name>) as a wrapper that checks
// what fn's signature promises about its result.
func (g *gen) checkedWrapper(fn *check.Func) ast.Decl {
	decl := g.signature(fn.Decl)
	var args []ast.Expr
	for _, p := range fn.Decl.Params {
		args = append(args, name(p.Name))
	}
	var impl ast.Expr = ast.NewIdent("_unchecked_" + g.funcName(fn).Name)
	if len(fn.TypeParams) > 0 {
		idx := &ast.IndexListExpr{X: impl}
		for _, tp := range fn.TypeParams {
			idx.Indices = append(idx.Indices, name(tp.Name))
		}
		impl = idx
	}
	r := ast.NewIdent("_r")
	body := []ast.Stmt{define(r, &ast.CallExpr{Fun: impl, Args: args})}
	for _, mc := range fn.ResultConstraints {
		var checks []ast.Stmt
		v := ast.Expr(r)
		if !check.Identical(mc.Type, fn.Result) {
			v = g.newTmp()
		}
		for _, con := range mc.Constraints {
			checks = append(checks, g.atPath(v, mc.Type, splitPath(con.Path), func(x ast.Expr, t check.Type) []ast.Stmt {
				return g.contractCheck(fn, con, x, t)
			})...)
		}
		if check.Identical(mc.Type, fn.Result) {
			body = append(body, checks...)
			continue
		}
		// One member of a union result.
		ok := g.newTmp()
		body = append(body, &ast.IfStmt{
			Init: &ast.AssignStmt{Lhs: []ast.Expr{v, ok}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.TypeAssertExpr{X: r, Type: g.goType(mc.Type)}}},
			Cond: ok,
			Body: &ast.BlockStmt{List: checks},
		})
	}
	// Records it builds must keep their fields' where clauses.
	body = append(body, g.invariantChecks(fn, r, fn.Result, map[check.Type]bool{})...)
	decl.Body = &ast.BlockStmt{List: append(body, &ast.ReturnStmt{Results: []ast.Expr{r}})}
	return decl
}

// invariantPreds lists the predicates that invariantChecks may call for
// values of type t.
func invariantPreds(t check.Type, seen map[check.Type]bool) []*check.Func {
	if seen[t] {
		return nil
	}
	seen[t] = true
	var out []*check.Func
	fields := func(fs []*check.Field) {
		for _, f := range fs {
			for _, con := range f.Constraints {
				out = append(out, constraintPreds(con)...)
			}
			out = append(out, invariantPreds(f.Type, seen)...)
		}
	}
	switch t := t.(type) {
	case *check.Record:
		fields(t.Fields)
	case *check.Sealed:
		for _, v := range t.Variants {
			fields(v.Fields)
		}
	case *check.Union:
		for _, m := range t.Members {
			out = append(out, invariantPreds(m, seen)...)
		}
	case *check.List:
		out = append(out, invariantPreds(t.Elem, seen)...)
	}
	return out
}

func constraintPreds(con *check.Constraint) []*check.Func {
	var out []*check.Func
	for _, alt := range con.Or {
		out = append(out, constraintPreds(alt)...)
	}
	if con.Pred != nil {
		out = append(out, con.Pred)
	}
	return out
}

// hasInvariants reports whether values of type t can hold records whose
// fields have where clauses.
func (g *gen) hasInvariants(t check.Type, seen map[check.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t := t.(type) {
	case *check.Record:
		for _, f := range t.Fields {
			if len(f.Constraints) > 0 || g.hasInvariants(f.Type, seen) {
				return true
			}
		}
	case *check.Sealed:
		for _, v := range t.Variants {
			for _, f := range v.Fields {
				if len(f.Constraints) > 0 || g.hasInvariants(f.Type, seen) {
					return true
				}
			}
		}
	case *check.Union:
		for _, m := range t.Members {
			if g.hasInvariants(m, seen) {
				return true
			}
		}
	case *check.List:
		return g.hasInvariants(t.Elem, seen)
	}
	return false
}

// invariantChecks checks that the records in x (of type t), which fn
// returned, keep their fields' where clauses. Types already being
// checked are not entered again, so recursive types are checked only to
// a fixed depth.
func (g *gen) invariantChecks(fn *check.Func, x ast.Expr, t check.Type, seen map[check.Type]bool) []ast.Stmt {
	if seen[t] || !g.hasInvariants(t, map[check.Type]bool{}) {
		return nil
	}
	seen[t] = true
	defer delete(seen, t)
	fields := func(owner string, x ast.Expr, fs []*check.Field) []ast.Stmt {
		var out []ast.Stmt
		for _, f := range fs {
			v := &ast.SelectorExpr{X: x, Sel: name(f.Name)}
			for _, con := range f.Constraints {
				what := fmt.Sprintf("%s whose %s is not %s", owner, f.Name, con)
				out = append(out, g.atPath(v, f.Type, splitPath(con.Path), func(y ast.Expr, yt check.Type) []ast.Stmt {
					return g.invariantCheck(fn, con, y, yt, what)
				})...)
			}
			out = append(out, g.invariantChecks(fn, v, f.Type, seen)...)
		}
		return out
	}
	switch t := t.(type) {
	case *check.Record:
		return fields(t.Name, x, t.Fields)
	case *check.Sealed:
		var out []ast.Stmt
		for _, v := range t.Variants {
			if !g.hasInvariants(&check.Record{Fields: v.Fields}, map[check.Type]bool{}) {
				continue
			}
			val, ok := g.newTmp(), g.newTmp()
			body := fields(t.Name+"."+v.Name, val, v.Fields)
			out = append(out, &ast.IfStmt{
				Init: &ast.AssignStmt{Lhs: []ast.Expr{val, ok}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.TypeAssertExpr{X: x, Type: g.variantType(v)}}},
				Cond: ok,
				Body: &ast.BlockStmt{List: body},
			})
		}
		return out
	case *check.Union:
		var out []ast.Stmt
		for _, m := range t.Members {
			if !g.hasInvariants(m, map[check.Type]bool{}) {
				continue
			}
			val, ok := g.newTmp(), g.newTmp()
			out = append(out, &ast.IfStmt{
				Init: &ast.AssignStmt{Lhs: []ast.Expr{val, ok}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.TypeAssertExpr{X: x, Type: g.goType(m)}}},
				Cond: ok,
				Body: &ast.BlockStmt{List: g.invariantChecks(fn, val, m, seen)},
			})
		}
		return out
	case *check.List:
		el := g.newTmp()
		return []ast.Stmt{&ast.RangeStmt{
			Key: ast.NewIdent("_"), Value: el, Tok: token.DEFINE, X: x,
			Body: &ast.BlockStmt{List: g.invariantChecks(fn, el, t.Elem, seen)},
		}}
	}
	return nil
}

// invariantCheck panics if con does not hold for x, a field value of a
// record fn returned.
func (g *gen) invariantCheck(fn *check.Func, con *check.Constraint, x ast.Expr, t check.Type, what string) []ast.Stmt {
	cond := g.constraintCond(con, x, t)
	if cond == nil {
		return nil
	}
	g.usesShow = true
	msg := fmt.Sprintf("%s: %s returned %s %s: ", fn.Decl.Pos, fn.Decl.Name, article(what), what)
	text := &ast.BinaryExpr{X: strLit(msg), Op: token.ADD, Y: &ast.CallExpr{Fun: ast.NewIdent("_show"), Args: []ast.Expr{x}}}
	return []ast.Stmt{&ast.IfStmt{
		Cond: &ast.UnaryExpr{Op: token.NOT, X: paren(cond)},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{text}}}}},
	}}
}

func article(w string) string {
	if w != "" && strings.ContainsRune("AEIOUaeiou", rune(w[0])) {
		return "an"
	}
	return "a"
}

func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	return strings.Split(path[1:], ".")
}

// atPath runs leaf on the parts of x (of bork type t) that path leads
// to: every element of a list, a field of a record, or a field of
// whichever variant has it.
func (g *gen) atPath(x ast.Expr, t check.Type, steps []string, leaf func(ast.Expr, check.Type) []ast.Stmt) []ast.Stmt {
	if len(steps) == 0 {
		return leaf(x, t)
	}
	step, rest := steps[0], steps[1:]
	switch t := t.(type) {
	case *check.List:
		el := g.newTmp()
		return []ast.Stmt{&ast.RangeStmt{
			Key: ast.NewIdent("_"), Value: el, Tok: token.DEFINE, X: x,
			Body: &ast.BlockStmt{List: g.atPath(el, t.Elem, rest, leaf)},
		}}
	case *check.Record:
		if f := t.Field(step); f != nil {
			return g.atPath(&ast.SelectorExpr{X: x, Sel: name(step)}, f.Type, rest, leaf)
		}
	case *check.Sealed:
		var out []ast.Stmt
		for _, v := range t.Variants {
			f := v.Field(step)
			if f == nil {
				continue
			}
			val, ok := g.newTmp(), g.newTmp()
			out = append(out, &ast.IfStmt{
				Init: &ast.AssignStmt{Lhs: []ast.Expr{val, ok}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.TypeAssertExpr{X: x, Type: g.variantType(v)}}},
				Cond: ok,
				Body: &ast.BlockStmt{List: g.atPath(&ast.SelectorExpr{X: val, Sel: name(step)}, f.Type, rest, leaf)},
			})
		}
		return out
	}
	return nil
}

// contractCheck checks con on the value x (of bork type t), which fn
// returned, and panics if it does not hold.
func (g *gen) contractCheck(fn *check.Func, con *check.Constraint, x ast.Expr, t check.Type) []ast.Stmt {
	cond := g.constraintCond(con, x, t)
	if cond == nil {
		return nil
	}
	g.usesShow = true
	what := "a result that is " + con.String()
	if con.Path != "" {
		what = "results whose parts are " + con.String()
	}
	msg := fmt.Sprintf("%s: %s promised %s, but returned ", fn.Decl.Pos, fn.Decl.Name, what)
	text := &ast.BinaryExpr{
		X:  &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(msg)},
		Op: token.ADD,
		Y:  &ast.CallExpr{Fun: ast.NewIdent("_show"), Args: []ast.Expr{x}},
	}
	return []ast.Stmt{&ast.IfStmt{
		Cond: &ast.UnaryExpr{Op: token.NOT, X: paren(cond)},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{text}}}}},
	}}
}

// constraintCond is the Go condition that con holds for x, of type t.
func (g *gen) constraintCond(con *check.Constraint, x ast.Expr, t check.Type) ast.Expr {
	if con.Or != nil {
		var out ast.Expr
		for _, alt := range con.Or {
			c := g.constraintCond(alt, x, t)
			if c == nil {
				return nil
			}
			if out == nil {
				out = c
			} else {
				out = &ast.BinaryExpr{X: paren(out), Op: token.LOR, Y: paren(c)}
			}
		}
		return out
	}
	if con.PredParam != "" {
		// The function value the caller passed.
		return &ast.CallExpr{Fun: name(con.PredParam), Args: []ast.Expr{x}}
	}
	inst := con.Pred.InstanceFor(t)
	if inst == nil {
		return nil
	}
	args := []ast.Expr{x}
	for i, a := range con.Args {
		if a.Const != nil {
			args = append(args, g.constant(a.Const, inst.Params[i+1]))
		} else {
			args = append(args, name(a.Param))
		}
	}
	return &ast.CallExpr{Fun: g.instance(inst), Args: args}
}
