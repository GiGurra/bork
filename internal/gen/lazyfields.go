package gen

import (
	"go/ast"
	"go/token"

	"github.com/GiGurra/bork/internal/check"
)

func (g *gen) fieldStorageType(field *check.Field) ast.Expr {
	typ := g.goType(field.Type)
	if !field.Lazy {
		return typ
	}
	g.usesLazy = true
	return &ast.StarExpr{X: &ast.IndexExpr{X: ast.NewIdent("_lazyCell"), Index: typ}}
}
func (g *gen) fieldRead(root ast.Expr, field *check.Field) ast.Expr {
	value := &ast.SelectorExpr{X: root, Sel: name(field.Name)}
	if !field.Lazy {
		return value
	}
	return &ast.CallExpr{Fun: &ast.SelectorExpr{X: value, Sel: ast.NewIdent("get")}}
}
func (g *gen) fieldReadSuffix(field *check.Field) string {
	suffix := name(field.Name).Name
	if field.Lazy {
		suffix += ".get()"
	}
	return suffix
}
func (g *gen) fieldReadText(root string, field *check.Field) string {
	return "(" + root + ")." + g.fieldReadSuffix(field)
}
func (g *gen) fieldResolved(value ast.Expr, field *check.Field) ast.Expr {
	if !field.Lazy {
		return value
	}
	g.usesLazy = true
	return &ast.CallExpr{Fun: &ast.IndexExpr{X: ast.NewIdent("_lazyResolved"), Index: g.goType(field.Type)}, Args: []ast.Expr{value}}
}
func (g *gen) fieldCell(thunk *check.Lambda, metadata *check.LazyDescription) ast.Expr {
	g.usesLazy = true
	constructor := "_lazyNew"
	if g.evalMode && metadata != nil && metadata.Effects == "nothing" {
		constructor = "_lazyConstNew"
	}
	return &ast.CallExpr{Fun: &ast.IndexExpr{X: ast.NewIdent(constructor), Index: g.goType(thunk.Type().(*check.FuncType).Result)}, Args: []ast.Expr{g.lambda(thunk)}}
}
func (g *gen) patternFieldRead(root ast.Expr, typ check.Type, variant *check.Variant, fieldName string) ast.Expr {
	var fields []*check.Field
	if variant != nil {
		fields = variant.Fields
	} else if record, ok := typ.(*check.Record); ok {
		fields = record.Fields
	}
	for _, field := range fields {
		if field.Name == fieldName {
			return g.fieldRead(root, field)
		}
	}
	return &ast.SelectorExpr{X: root, Sel: name(fieldName)}
}

func independentFields(fields []*check.Field) []*check.Field {
	var data []*check.Field
	for _, field := range fields {
		if !field.Computed {
			data = append(data, field)
		}
	}
	return data
}

func (g *gen) computedCells(root *ast.Ident, fields []*check.Field, typ check.Type) []ast.Stmt {
	var stmts []ast.Stmt
	for _, field := range fields {
		if !field.Computed {
			continue
		}
		recipe := check.ComputedFieldInitializer(field, root.Name, typ)
		cell := g.fieldCell(recipe, &check.LazyDescription{Kind: "computed field", Effects: "nothing"})
		stmts = append(stmts, &ast.AssignStmt{Lhs: []ast.Expr{&ast.SelectorExpr{X: root, Sel: name(field.Name)}}, Tok: token.ASSIGN, Rhs: []ast.Expr{cell}})
	}
	return stmts
}

func (g *gen) nameCandidate(candidate *check.Var, name *ast.Ident) {
	if g.candidateNames == nil {
		g.candidateNames = map[*check.Var]*ast.Ident{}
	}
	g.candidateNames[candidate] = name
}

func hasComputedFields(fields []*check.Field) bool {
	for _, field := range fields {
		if field.Computed {
			return true
		}
	}
	return false
}
