package gen

import (
	"go/ast"

	"github.com/GiGurra/bork/internal/check"
)

func (g *gen) showInstance(t check.Type) *check.ClassInstance {
	for _, ci := range g.info.ClassInstances {
		if check.IsShow(ci.Class) && baseOf(ci.Type) == baseOf(t) {
			return ci
		}
	}
	return nil
}

// showMethods attach the universal instance to every representation of
// the declared type, including each variant of a sealed type.
func (g *gen) showMethods(recv ast.Expr, t check.Type) []ast.Decl {
	ci := g.showInstance(t)
	if ci == nil {
		return nil
	}
	g.usesShow = true
	var params []*check.TypeParam
	var args []check.Type
	switch t := t.(type) {
	case *check.Record:
		params = t.TypeParams
		args = ci.Type.(*check.Record).Args
	case *check.Sealed:
		params = t.TypeParams
		args = ci.Type.(*check.Sealed).Args
	}
	bound := map[*check.TypeParam]check.Type{}
	for i, arg := range args {
		bound[arg.(*check.TypeParam)] = params[i]
	}
	var fun ast.Expr = g.funcName(ci.Methods[0])
	var callArgs []ast.Expr
	if len(ci.TypeParams) > 0 {
		idx := &ast.IndexListExpr{X: fun}
		for _, tp := range ci.TypeParams {
			actual := bound[tp]
			idx.Indices = append(idx.Indices, g.goType(actual))
			for _, class := range tp.Bounds {
				callArgs = append(callArgs, g.dict(&check.Dict{Class: class, Type: actual, Builtin: true}))
			}
		}
		fun = idx
	}
	callArgs = append(callArgs, ast.NewIdent("v"))
	return []ast.Decl{&ast.FuncDecl{
		Recv: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("v")}, Type: recv}}},
		Name: ast.NewIdent("_borkShow"),
		Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}}}},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{&ast.CallExpr{Fun: fun, Args: callArgs}}}}},
	}}
}

func (g *gen) showStringMethod(recv ast.Expr, t check.Type, label string, fields []*check.Field, record bool, positional ...bool) ast.Decl {
	if g.showInstance(t) == nil {
		return g.stringMethod(recv, label, fields, record, positional...)
	}
	return &ast.FuncDecl{
		Recv: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("v")}, Type: recv}}},
		Name: ast.NewIdent("String"),
		Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}}}},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{&ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("v"), Sel: ast.NewIdent("_borkShow")}}}}}},
	}
}
