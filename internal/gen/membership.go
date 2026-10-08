package gen

import (
	"go/ast"
	"go/token"

	"github.com/GiGurra/bork/internal/check"
)

// A Go type parameter instantiated with any cannot distinguish a Bork union
// from its other members. Pass a membership predicate only for parameters
// inspected by a pattern or ?, and forward it through generic calls.
func (g *gen) membershipNeeds() {
	g.typeMembership = check.RuntimeMembershipParameters(g.info)
}

func membershipName(p *check.TypeParam) *ast.Ident {
	return ast.NewIdent("_type_" + p.Name)
}

func membershipType() *ast.FuncType {
	return &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("any")}}}, Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("bool")}}}}
}

func (g *gen) membershipParams(params []*check.TypeParam) []*ast.Field {
	var fields []*ast.Field
	for _, p := range params {
		if g.typeMembership[p] {
			fields = append(fields, &ast.Field{Names: []*ast.Ident{membershipName(p)}, Type: membershipType()})
		}
	}
	return fields
}

func (g *gen) membershipArgs(params []*check.TypeParam, args []check.Type) []ast.Expr {
	var values []ast.Expr
	for i, p := range params {
		if !g.typeMembership[p] {
			continue
		}
		t := args[i]
		if tp, ok := t.(*check.TypeParam); ok {
			values = append(values, membershipName(tp))
			continue
		}
		x := ast.NewIdent("_value")
		ft := membershipType()
		ft.Params.List[0].Names = []*ast.Ident{x}
		values = append(values, &ast.FuncLit{Type: ft, Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{g.memberOf(t, x)}}}}})
	}
	return values
}

func (g *gen) memberOf(t check.Type, x ast.Expr) ast.Expr {
	switch t := t.(type) {
	case *check.TypeParam:
		return &ast.CallExpr{Fun: membershipName(t), Args: []ast.Expr{x}}
	case *check.Union:
		var test ast.Expr
		for _, m := range t.Members {
			next := g.memberOf(m, x)
			if test == nil {
				test = next
			} else {
				test = &ast.BinaryExpr{X: test, Op: token.LOR, Y: next}
			}
		}
		return &ast.ParenExpr{X: test}
	default:
		return g.isType(g.goType(t), x)
	}
}

func needsMembership(t check.Type) bool {
	switch t := t.(type) {
	case *check.TypeParam:
		return true
	case *check.Union:
		for _, m := range t.Members {
			if needsMembership(m) {
				return true
			}
		}
	}
	return false
}

func isUnion(t check.Type) bool { _, ok := t.(*check.Union); return ok }
