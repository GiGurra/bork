package gen

import (
	"go/ast"
	"go/constant"
	"go/token"
	"strconv"

	"github.com/GiGurra/bork/internal/check"
)

// ruleTest generates a property test for an inference rule. The
// compiler takes rules on trust, so in test mode they are tried on many
// values of their variables (every combination of a few interesting
// values, or a fixed random sample of them): wherever the premises hold,
// the conclusions must hold too. If a variable has a type that no values
// are generated for, it returns nil and that type.
func (g *gen) ruleTest(r *check.Rule, goName *ast.Ident) (ast.Decl, check.Type) {
	g.tmp = 0
	g.fnResult = check.Unit
	ix := ast.NewIdent("_ix")
	sizes := &ast.CompositeLit{Type: &ast.ArrayType{Elt: ast.NewIdent("int")}}
	var body []ast.Stmt
	var shown ast.Expr
	for i, p := range r.Decl.Params {
		pool := g.valuePool(r.VarTypes[i])
		if pool == nil {
			return nil, r.VarTypes[i]
		}
		sizes.Elts = append(sizes.Elts, &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(len(pool.Elts))})
		body = append(body, define(name(p.Name), &ast.IndexExpr{X: pool, Index: &ast.IndexExpr{X: ix, Index: &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(i)}}}))
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
	g.usesShow = true
	for _, x := range r.Decl.Premises {
		stmts, cond := g.value(x)
		body = append(body, stmts...)
		body = append(body, &ast.IfStmt{
			Cond: &ast.UnaryExpr{Op: token.NOT, X: paren(cond)},
			Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{}}},
		})
	}
	for _, x := range r.Decl.Conclusions {
		stmts, cond := g.value(x)
		body = append(body, stmts...)
		msg := &ast.BinaryExpr{
			X:  &ast.BinaryExpr{X: strLit(r.Decl.Pos.String() + ": rule " + r.Decl.Name + " does not hold for "), Op: token.ADD, Y: shown},
			Op: token.ADD,
			Y:  strLit(": the premises hold, but " + exprText(x) + " is false"),
		}
		body = append(body, &ast.IfStmt{
			Cond: &ast.UnaryExpr{Op: token.NOT, X: paren(cond)},
			Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{msg}}}}},
		})
	}
	g.usesRules = true
	each := &ast.FuncLit{
		Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ix}, Type: &ast.ArrayType{Elt: ast.NewIdent("int")}}}}},
		Body: &ast.BlockStmt{List: body},
	}
	return &ast.FuncDecl{
		Name: goName,
		Type: &ast.FuncType{Params: &ast.FieldList{}},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("_forCases"), Args: []ast.Expr{sizes, each}}}}},
	}, nil
}

// valuePool is a Go slice of interesting values of type t, or nil if t
// is not a type values are generated for.
func (g *gen) valuePool(t check.Type) *ast.CompositeLit {
	lit := &ast.CompositeLit{Type: &ast.ArrayType{Elt: g.goType(t)}}
	switch {
	case t == check.Bool:
		lit.Elts = []ast.Expr{ast.NewIdent("false"), ast.NewIdent("true")}
	case t == check.String:
		for _, s := range stringPool {
			lit.Elts = append(lit.Elts, strLit(s))
		}
	case check.IsFloat(t):
		for _, f := range floatPool {
			lit.Elts = append(lit.Elts, constLit(constant.MakeFloat64(f), t))
		}
	case check.IsInteger(t):
		lo, hi := check.IntRange(t)
		for _, n := range intPool {
			v := constant.MakeInt64(n)
			if constant.Compare(v, token.GTR, lo) && constant.Compare(v, token.LSS, hi) {
				lit.Elts = append(lit.Elts, constLit(v, t))
			}
		}
		lit.Elts = append(lit.Elts, constLit(lo, t), constLit(hi, t))
	default:
		return nil
	}
	return lit
}

// The pools start with the simplest values, so the first
// counterexample found is a simple one.
var (
	intPool    = []int64{0, 1, -1, 2, -2, 3, -3, 10, -10, 100, -100, 1000, -1000, 1000000, -1000000}
	floatPool  = []float64{0, 1, -1, 0.5, -0.5, 1.5, -1.5, 100, -100, 1e9, -1e9}
	stringPool = []string{"", " ", "a", "b", "ab", "A", " a ", "hello", "Hello, World!", "123", "-1", "é", "\n"}
)

func strLit(s string) *ast.BasicLit {
	return &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(s)}
}

const rulesRuntime = `package main

import "math/rand"

// _forCases calls each with every combination of indexes below sizes,
// or with a fixed random sample of them if there are too many.
func _forCases(sizes []int, each func([]int)) {
	const most = 20000
	total := 1
	for _, n := range sizes {
		total *= n
		if total > most {
			break
		}
	}
	ix := make([]int, len(sizes))
	if total <= most {
		// The last index changes fastest.
		for c := 0; c < total; c++ {
			rest := c
			for i := len(sizes) - 1; i >= 0; i-- {
				ix[i] = rest % sizes[i]
				rest /= sizes[i]
			}
			each(ix)
		}
		return
	}
	rng := rand.New(rand.NewSource(1))
	for c := 0; c < most; c++ {
		for i, n := range sizes {
			ix[i] = rng.Intn(n)
		}
		each(ix)
	}
}
`
