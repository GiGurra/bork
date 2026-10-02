package gen

import (
	"go/ast"
	"go/token"

	"github.com/GiGurra/bork/internal/check"
)

// Classes are lowered to dictionary passing:
//
//   - a class is a Go struct of functions, one per method:
//     type Show[T any] struct{ show func(T) string };
//   - an instance's methods are Go functions (_i_showInt_show), and the
//     instance itself a function that builds the struct (_i_showInt());
//   - a function with bounded type parameters takes the instances it
//     needs first: func sum[T any](_d_T_Monoid Monoid[T], xs []T) T.
//
// A call whose instance is known calls the method directly; only code
// that is generic over the instance goes through the struct.

// classType is the Go type of class's dictionary for type t.
func (g *gen) classType(class *check.Class, t check.Type) ast.Expr {
	return &ast.IndexExpr{X: typeName(class.Name, class.Pkg), Index: g.goType(t)}
}

// classDecl declares a class's dictionary struct.
func (g *gen) classDecl(class *check.Class) ast.Decl {
	st := &ast.StructType{Fields: &ast.FieldList{}}
	for _, m := range class.Methods {
		st.Fields.List = append(st.Fields.List, &ast.Field{
			Names: []*ast.Ident{name(m.Decl.Name)},
			Type:  g.funcType(&check.FuncType{Params: m.Params, Result: m.Result}, nil),
		})
	}
	return &ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{
		Name:       typeName(class.Name, class.Pkg),
		TypeParams: typeParamList([]*check.TypeParam{class.Param}),
		Type:       st,
	}}}
}

// instName is the Go name of the function that builds an instance.
func instName(ci *check.ClassInstance) string {
	prefix := "_"
	if ci.Pkg != nil && ci.Pkg.GoPrefix != "" {
		prefix = ci.Pkg.GoPrefix
	}
	return "_i" + prefix + ci.Name
}

// dictParam is the Go parameter holding the instance of class for the
// type parameter tp.
func dictParam(tp *check.TypeParam, class *check.Class) *ast.Ident {
	return ast.NewIdent("_d_" + tp.Name + "_" + class.Name)
}

// dictParams are the parameters for the bounds of type parameters.
func (g *gen) dictParams(tps []*check.TypeParam) []*ast.Field {
	var out []*ast.Field
	for _, tp := range tps {
		for _, b := range tp.Bounds {
			out = append(out, &ast.Field{Names: []*ast.Ident{dictParam(tp, b)}, Type: g.classType(b, tp)})
		}
	}
	return out
}

// instanceDecl declares the function that builds an instance.
func (g *gen) instanceDecl(ci *check.ClassInstance) ast.Decl {
	lit := &ast.CompositeLit{Type: g.classType(ci.Class, ci.Type)}
	for i, m := range ci.Methods {
		var fn ast.Expr = g.funcName(m)
		if len(ci.TypeParams) > 0 {
			// The method needs the instance's own instances: a closure.
			idx := &ast.IndexListExpr{X: fn}
			var args []ast.Expr
			for _, tp := range ci.TypeParams {
				idx.Indices = append(idx.Indices, name(tp.Name))
				for _, b := range tp.Bounds {
					args = append(args, dictParam(tp, b))
				}
			}
			ft := &check.FuncType{Params: m.Params, Result: m.Result}
			var names []*ast.Ident
			for j := range m.Params {
				names = append(names, ast.NewIdent("_p"+string(rune('0'+j))))
				args = append(args, names[j])
			}
			call := &ast.CallExpr{Fun: idx, Args: args}
			var body []ast.Stmt
			if m.Result == check.Unit {
				body = []ast.Stmt{&ast.ExprStmt{X: call}}
			} else {
				body = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{call}}}
			}
			fn = &ast.FuncLit{Type: g.funcType(ft, names), Body: &ast.BlockStmt{List: body}}
		}
		lit.Elts = append(lit.Elts, &ast.KeyValueExpr{Key: name(ci.Class.Methods[i].Decl.Name), Value: fn})
	}
	return &ast.FuncDecl{
		Name: ast.NewIdent(instName(ci)),
		Type: &ast.FuncType{
			TypeParams: typeParamList(ci.TypeParams),
			Params:     &ast.FieldList{List: g.dictParams(ci.TypeParams)},
			Results:    &ast.FieldList{List: []*ast.Field{{Type: g.classType(ci.Class, ci.Type)}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{lit}}}},
	}
}

// dict is the Go expression for an instance: a dictionary parameter, or
// a call building a declared instance.
func (g *gen) dict(d *check.Dict) ast.Expr {
	if d.Param != nil {
		return dictParam(d.Param, d.Class)
	}
	var fun ast.Expr = ast.NewIdent(instName(d.Inst))
	if len(d.TypeArgs) > 0 {
		idx := &ast.IndexListExpr{X: fun}
		for _, t := range d.TypeArgs {
			idx.Indices = append(idx.Indices, g.goType(t))
		}
		fun = idx
	}
	var args []ast.Expr
	for _, a := range d.Args {
		args = append(args, g.dict(a))
	}
	return &ast.CallExpr{Fun: fun, Args: args}
}

// methodFunc is the Go function a use of a class method stands for: the
// instance's method itself when the instance is known, or the field of
// the dictionary.
func (g *gen) methodFunc(inst *check.Instance) (fun ast.Expr, dicts []ast.Expr) {
	return g.dictMethod(inst.Dicts[0], inst.Func.Decl.Name)
}

// dictMethod is the Go function for a method of the instance d, and the
// instances to pass it first.
func (g *gen) dictMethod(d *check.Dict, method string) (fun ast.Expr, dicts []ast.Expr) {
	if d.Param != nil {
		return &ast.SelectorExpr{X: dictParam(d.Param, d.Class), Sel: name(method)}, nil
	}
	var impl *check.Func
	for _, m := range d.Inst.Methods {
		if m.Decl.Name == method {
			impl = m
		}
	}
	fun = g.funcName(impl)
	if len(d.TypeArgs) > 0 {
		idx := &ast.IndexListExpr{X: fun}
		for _, t := range d.TypeArgs {
			idx.Indices = append(idx.Indices, g.goType(t))
		}
		fun = idx
	}
	for _, a := range d.Args {
		dicts = append(dicts, g.dict(a))
	}
	return fun, dicts
}

// funcRef is the Go value of a function used as a value (inst), when it
// is a class method or needs instances: those are filled in, by a
// closure if needed.
func (g *gen) funcRef(inst *check.Instance) ast.Expr {
	var fun ast.Expr
	var dicts []ast.Expr
	if inst.Func.Class != nil {
		fun, dicts = g.methodFunc(inst)
	} else {
		fun = g.instance(inst)
		for _, d := range inst.Dicts {
			dicts = append(dicts, g.dict(d))
		}
	}
	if len(dicts) == 0 {
		return fun
	}
	ft := &check.FuncType{Params: inst.Params, Result: inst.Result}
	var names []*ast.Ident
	args := dicts
	for j := range inst.Params {
		names = append(names, ast.NewIdent("_p"+string(rune('0'+j))))
		args = append(args, names[j])
	}
	call := &ast.CallExpr{Fun: fun, Args: args}
	body := []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{call}}}
	if inst.Result == check.Unit {
		body = []ast.Stmt{&ast.ExprStmt{X: call}}
	}
	return &ast.FuncLit{Type: g.funcType(ft, names), Body: &ast.BlockStmt{List: body}}
}
