package gen

import (
	"go/ast"
	"go/token"

	"github.com/GiGurra/bork/internal/check"
)

// customText renders with the instances selected in the caller's package.
// The callback is local, so nested printing has no global renderer state.
func (g *gen) customText(x ast.Expr, t check.Type, dicts []*check.Dict) ast.Expr {
	g.usesShow = true
	value, custom := ast.NewIdent("value"), ast.NewIdent("custom")
	sw := &ast.TypeSwitchStmt{Assign: &ast.AssignStmt{Lhs: []ast.Expr{value}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.TypeAssertExpr{X: ast.NewIdent("x")}}}, Body: &ast.BlockStmt{}}
	for _, d := range dicts {
		fun, args := g.dictMethod(d, "show")
		args = append(args, value)
		sw.Body.List = append(sw.Body.List, &ast.CaseClause{List: []ast.Expr{g.goType(d.Type)}, Body: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{&ast.CallExpr{Fun: fun, Args: args}, ast.NewIdent("true")}}}})
	}
	callback := &ast.FuncLit{
		Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("x")}, Type: ast.NewIdent("any")}}}, Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}, {Type: ast.NewIdent("bool")}}}},
		Body: &ast.BlockStmt{List: []ast.Stmt{sw, &ast.ReturnStmt{Results: []ast.Expr{strLit(""), ast.NewIdent("false")}}}},
	}
	body := &ast.BlockStmt{List: []ast.Stmt{define(custom, callback), &ast.ReturnStmt{Results: []ast.Expr{&ast.CallExpr{Fun: ast.NewIdent("_strWith"), Args: []ast.Expr{ast.NewIdent("x"), custom}}}}}}
	return &ast.CallExpr{Fun: &ast.FuncLit{Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("x")}, Type: g.goType(t)}}}, Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}}}}, Body: body}, Args: []ast.Expr{g.typed(x, t)}}
}

func (g *gen) showWithValue(x ast.Expr, t check.Type, custom ast.Expr) ast.Expr {
	if list, ok := t.(*check.List); ok {
		value := ast.NewIdent("x")
		show := &ast.FuncLit{Type: g.funcType(&check.FuncType{Params: []check.Type{list.Elem}, Result: check.String}, []*ast.Ident{value}), Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{g.showWithValue(value, list.Elem, custom)}}}}}
		return &ast.CallExpr{Fun: &ast.IndexExpr{X: ast.NewIdent("_showList"), Index: g.goType(list.Elem)}, Args: []ast.Expr{x, show}}
	}
	return &ast.CallExpr{Fun: ast.NewIdent("_showWith"), Args: []ast.Expr{x, custom}}
}
