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
	g.typeMembership = map[*check.TypeParam]bool{}
	var mark func(check.Type)
	mark = func(t check.Type) {
		switch t := t.(type) {
		case *check.TypeParam:
			g.typeMembership[t] = true
		case *check.Union:
			for _, m := range t.Members {
				mark(m)
			}
		}
	}
	var pattern func(*check.Pat)
	pattern = func(p *check.Pat) {
		if p == nil {
			return
		}
		for _, m := range p.Members {
			mark(m)
		}
		pattern(p.Sub)
		for _, f := range p.Fields {
			pattern(f.Pat)
		}
		for _, e := range p.Elems {
			pattern(e)
		}
		pattern(p.Rest)
	}
	funcs := map[*check.Func]bool{}
	for _, fn := range g.info.FuncOf {
		funcs[fn] = true
	}
	for _, fn := range g.info.ExpandedFunctions {
		funcs[fn] = true
	}
	for _, fn := range g.info.Mocks {
		funcs[fn] = true
	}
	for _, ci := range g.info.ClassInstances {
		for _, fn := range ci.Methods {
			funcs[fn] = true
		}
		for _, fn := range ci.Metadata {
			funcs[fn] = true
		}
	}
	for fn := range funcs {
		check.WalkComptime(fn.Body, func(e check.Expr) bool {
			switch e := e.(type) {
			case *check.Match:
				for _, a := range e.Arms {
					pattern(a.Pat)
				}
			case *check.Try:
				if e.Option == nil {
					mark(e.Kept)
					for _, t := range e.Rest {
						mark(t)
					}
				}
			}
			return true
		})
	}
	for {
		before := len(g.typeMembership)
		propagate := func(params []*check.TypeParam, args []check.Type) {
			for i, p := range params {
				if g.typeMembership[p] && i < len(args) {
					mark(args[i])
				}
			}
		}
		var dictionary func(*check.Dict)
		dictionary = func(d *check.Dict) {
			if d == nil {
				return
			}
			if d.Inst != nil {
				propagate(d.Inst.TypeParams, d.TypeArgs)
			}
			for _, a := range d.Args {
				dictionary(a)
			}
		}
		for fn := range funcs {
			check.WalkComptime(fn.Body, func(e check.Expr) bool {
				var inst *check.Instance
				switch e := e.(type) {
				case *check.Call:
					inst = e.Inst
				case *check.FuncRef:
					inst = e.Inst
				case *check.CallBuiltin:
					dictionary(e.Dictionary)
				}
				if inst != nil {
					propagate(inst.Func.TypeParams, inst.TypeArgs)
					for _, d := range inst.Dicts {
						dictionary(d)
					}
				}
				return true
			})
		}
		if len(g.typeMembership) == before {
			break
		}
	}
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
