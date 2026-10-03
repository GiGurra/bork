package gen

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/syntax"
)

// constraintFailure explains a failed check without changing its acceptance
// condition. Only transparent comparisons and AND groups are expanded: an OR
// or an opaque body retains the predicate-name diagnostic.
func (g *gen) constraintFailure(con *check.Constraint, x ast.Expr, t check.Type, base ast.Expr, written ...*check.Constraint) ([]ast.Stmt, ast.Expr, ast.Expr) {
	display := con
	if len(written) > 0 {
		display = written[0]
	}
	fallback := stringLit("must be " + display.String())
	call, ok := g.constraintCond(con, x, t).(*ast.CallExpr)
	if !ok || con.Pred == nil {
		return nil, base, fallback
	}
	fn := con.Pred
	atoms := diagnosticAtoms(fn.Body)
	if len(atoms) == 0 {
		return nil, base, fallback
	}
	// A single scalar unary fact already names the failing condition clearly.
	if len(atoms) == 1 && len(fn.Params) == 1 && diagnosticPath(atoms[0], fn.ParamVars[0]) == "" {
		return nil, base, fallback
	}
	if g.invariantDiagnostics == nil {
		g.invariantDiagnostics = map[*check.Func]bool{}
	}
	helper := "_explain_" + g.funcName(fn).Name
	if !g.invariantDiagnostics[fn] {
		g.invariantDiagnostics[fn] = true
		decl := g.signature(fn.Decl)
		decl.Name = ast.NewIdent(helper)
		decl.Type.Params.List = append(decl.Type.Params.List,
			&ast.Field{Names: []*ast.Ident{ast.NewIdent("_factNames")}, Type: &ast.ArrayType{Elt: ast.NewIdent("string")}},
			&ast.Field{Names: []*ast.Ident{ast.NewIdent("_factName")}, Type: ast.NewIdent("string")})
		decl.Type.Results = &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}, {Type: ast.NewIdent("string")}}}
		decl.Body = &ast.BlockStmt{}
		saved := g.tmp
		for _, atom := range atoms {
			setup, cond := g.value(atom)
			decl.Body.List = append(decl.Body.List, setup...)
			message := &ast.BinaryExpr{X: diagnosticText(atom, fn.ParamVars), Op: token.ADD, Y: ast.NewIdent("_factName")}
			message.X = &ast.BinaryExpr{X: stringLit("must satisfy "), Op: token.ADD, Y: message.X}
			message.Y = &ast.BinaryExpr{X: &ast.BinaryExpr{X: stringLit(" ("), Op: token.ADD, Y: message.Y}, Op: token.ADD, Y: stringLit(")")}
			decl.Body.List = append(decl.Body.List, &ast.IfStmt{Cond: &ast.UnaryExpr{Op: token.NOT, X: paren(cond)}, Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{stringLit(diagnosticPath(atom, fn.ParamVars[0])), message}}}}})
		}
		decl.Body.List = append(decl.Body.List, &ast.ReturnStmt{Results: []ast.Expr{stringLit(""), &ast.BinaryExpr{X: stringLit("must be "), Op: token.ADD, Y: ast.NewIdent("_factName")}}})
		g.tmp = saved
		g.extraFuncs = append(g.extraFuncs, decl)
	}
	// Reuse the instantiated predicate call, including its dictionaries and type
	// arguments. Its arguments are already the completed field/candidate values.
	call.Fun = diagnosticCallee(call.Fun, helper)
	labels := []ast.Expr{stringLit("value")}
	if _, ok := t.(*check.Record); ok {
		labels[0] = stringLit("")
	}
	for _, a := range display.Args {
		label := a.String()
		if a.Const == nil {
			label = a.Param
		}
		labels = append(labels, stringLit(label))
	}
	call.Args = append(call.Args, &ast.CompositeLit{Type: &ast.ArrayType{Elt: ast.NewIdent("string")}, Elts: labels}, stringLit(display.String()))
	path, msg := g.newTmp(), g.newTmp()
	stmt := &ast.AssignStmt{Lhs: []ast.Expr{path, msg}, Tok: token.DEFINE, Rhs: []ast.Expr{call}}
	return []ast.Stmt{stmt}, &ast.BinaryExpr{X: base, Op: token.ADD, Y: path}, msg
}

func stringLit(s string) *ast.BasicLit {
	return &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(s)}
}

func diagnosticCallee(x ast.Expr, name string) ast.Expr {
	switch x := x.(type) {
	case *ast.IndexExpr:
		cp := *x
		cp.X = ast.NewIdent(name)
		return &cp
	case *ast.IndexListExpr:
		cp := *x
		cp.X = ast.NewIdent(name)
		return &cp
	}
	return ast.NewIdent(name)
}

func diagnosticAtoms(x check.Expr) []check.Expr {
	if b, ok := x.(*check.Block); ok {
		if len(b.Stmts) != 0 {
			return nil
		}
		x = b.Tail
	}
	b, ok := x.(*check.Binary)
	if !ok {
		return nil
	}
	if b.Op == syntax.AndAnd {
		a, c := diagnosticAtoms(b.X), diagnosticAtoms(b.Y)
		if len(a) == 0 || len(c) == 0 {
			return nil
		}
		return append(a, c...)
	}
	switch b.Op {
	case syntax.Eq, syntax.NotEq, syntax.Lt, syntax.LtEq, syntax.Gt, syntax.GtEq:
		if diagnosticStable(b.X) && diagnosticStable(b.Y) {
			return []check.Expr{b}
		}
	}
	return nil
}

func diagnosticStable(x check.Expr) bool {
	switch x := x.(type) {
	case *check.Const, *check.VarRef:
		return true
	case *check.Select:
		return diagnosticStable(x.X)
	}
	return false
}

// The path names the first projection read from the candidate. Other inputs
// remain visible in the message; scalar subjects retain their existing path.
func diagnosticPath(x check.Expr, subject *check.Var) string {
	if b, ok := x.(*check.Binary); ok {
		if p := diagnosticPath(b.X, subject); p != "" {
			return p
		}
		return diagnosticPath(b.Y, subject)
	}
	var parts []string
	for {
		s, ok := x.(*check.Select)
		if !ok {
			break
		}
		parts = append([]string{s.Name}, parts...)
		x = s.X
	}
	if v, ok := x.(*check.VarRef); ok && v.Var == subject && len(parts) > 0 {
		return "." + strings.Join(parts, ".")
	}
	return ""
}

func diagnosticText(x check.Expr, params []*check.Var) ast.Expr {
	switch x := x.(type) {
	case *check.Const:
		return stringLit(check.CArg{Const: x.Value}.String())
	case *check.VarRef:
		for i, p := range params {
			if p == x.Var {
				return &ast.IndexExpr{X: ast.NewIdent("_factNames"), Index: &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(i)}}
			}
		}
	case *check.Select:
		// The candidate record's name is omitted, leaving paths like bodyLimitBytes.
		if v, ok := x.X.(*check.VarRef); ok && v.Var == params[0] {
			if _, record := v.Type().(*check.Record); record {
				return stringLit(x.Name)
			}
		}
		return &ast.BinaryExpr{X: diagnosticText(x.X, params), Op: token.ADD, Y: stringLit("." + x.Name)}
	case *check.Binary:
		ops := map[syntax.Kind]string{syntax.Eq: " == ", syntax.NotEq: " != ", syntax.Lt: " < ", syntax.LtEq: " <= ", syntax.Gt: " > ", syntax.GtEq: " >= "}
		return &ast.BinaryExpr{X: &ast.BinaryExpr{X: diagnosticText(x.X, params), Op: token.ADD, Y: stringLit(ops[x.Op])}, Op: token.ADD, Y: diagnosticText(x.Y, params)}
	}
	return stringLit("condition")
}

// atFailurePath also carries list indices and nested field paths into errors.
func (g *gen) atFailurePath(x ast.Expr, t check.Type, steps []string, path ast.Expr, leaf func(ast.Expr, check.Type, ast.Expr) []ast.Stmt) []ast.Stmt {
	if len(steps) == 0 {
		return leaf(x, t, path)
	}
	step, rest := steps[0], steps[1:]
	switch t := t.(type) {
	case *check.List:
		el, index := g.newTmp(), g.newTmp()
		g.imports["strconv"] = true
		indexed := &ast.BinaryExpr{X: &ast.BinaryExpr{X: path, Op: token.ADD, Y: stringLit("[")}, Op: token.ADD, Y: &ast.BinaryExpr{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("strconv"), Sel: ast.NewIdent("Itoa")}, Args: []ast.Expr{index}}, Op: token.ADD, Y: stringLit("]")}}
		return []ast.Stmt{&ast.RangeStmt{
			Key: index, Value: el, Tok: token.DEFINE, X: x,
			Body: &ast.BlockStmt{List: g.atFailurePath(el, t.Elem, rest, indexed, leaf)},
		}}
	case *check.Record:
		if f := t.Field(step); f != nil {
			return g.atFailurePath(&ast.SelectorExpr{X: x, Sel: name(step)}, f.Type, rest, &ast.BinaryExpr{X: path, Op: token.ADD, Y: stringLit("." + step)}, leaf)
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
				Body: &ast.BlockStmt{List: g.atFailurePath(&ast.SelectorExpr{X: val, Sel: name(step)}, f.Type, rest, diagnosticFieldPath(path, t, step), leaf)},
			})
		}
		return out
	}
	return nil
}

// Option encodes its payload directly, so value is not a JSON path component.
func diagnosticFieldPath(path ast.Expr, typ check.Type, field string) ast.Expr {
	if check.IsOption(typ) && field == "value" {
		return path
	}
	return &ast.BinaryExpr{X: path, Op: token.ADD, Y: stringLit("." + field)}
}
