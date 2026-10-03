package gen

import (
	"go/ast"
	"go/token"
	"strconv"

	"github.com/GiGurra/bork/internal/check"
)

// ruleTest uses the property generators for rules as well as parameterized
// tests, so trusted implications are checked on composites and special floats.
func (g *gen) ruleTest(r *check.Rule, goName *ast.Ident) (ast.Decl, check.Type) {
	body := func() []ast.Stmt {
		var stmts []ast.Stmt
		for _, x := range r.PremiseExprs {
			before, cond := g.value(x)
			stmts = append(stmts, before...)
			stmts = append(stmts, &ast.IfStmt{Cond: &ast.UnaryExpr{Op: token.NOT, X: paren(cond)}, Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{}}}})
		}
		for i, x := range r.ConclusionExprs {
			before, cond := g.value(x)
			stmts = append(stmts, before...)
			msg := strLit(r.Decl.Pos.String() + ": rule " + r.Decl.Name + ": the premises hold, but " + r.ConclusionTexts[i] + " is false")
			stmts = append(stmts, &ast.IfStmt{Cond: &ast.UnaryExpr{Op: token.NOT, X: paren(cond)}, Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{msg}}}}}})
		}
		return stmts
	}
	return g.propertyTest("rule "+r.Decl.Name, false, r.Decl.Params, r.VarTypes, make([][]*check.Constraint, len(r.Vars)), body, goName)
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
