package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
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

func className(class *check.Class) *ast.Ident {
	if check.IsShow(class) {
		return ast.NewIdent("_Show")
	}
	return typeName(class.Name, class.Pkg)
}

// classType is the Go type of class's dictionary for type t.
func (g *gen) classType(class *check.Class, t check.Type) ast.Expr {
	return &ast.IndexExpr{X: className(class), Index: g.goType(t)}
}

// classDecl declares a class's dictionary struct.
func (g *gen) classDecl(class *check.Class) ast.Decl {
	st := &ast.StructType{Fields: &ast.FieldList{}}
	metadataType, err := parser.ParseExpr("map[string]func() any")
	if err != nil {
		panic(err)
	}
	st.Fields.List = append(st.Fields.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent("_metadata")}, Type: metadataType})
	for _, m := range class.Methods {
		st.Fields.List = append(st.Fields.List, &ast.Field{
			Names: []*ast.Ident{name(m.Decl.Name)},
			Type:  g.funcType(&check.FuncType{Params: m.Params, Result: m.Result}, nil),
		})
	}
	if check.IsCodec(class, "Decode") {
		st.Fields.List = append(st.Fields.List,
			&ast.Field{Names: []*ast.Ident{ast.NewIdent("kind")}, Type: ast.NewIdent("string")},
			&ast.Field{Names: []*ast.Ident{ast.NewIdent("optional")}, Type: ast.NewIdent("bool")})
	}
	if class.ForeignRecord != nil {
		g.usesForeign = true
		g.goType(g.info.Named["GoValueError"])
		for _, spec := range []struct{ name, typ string }{
			{"New", "func() any"}, {"FromGo", "func(any) (T, []" + g.typeText(g.info.Named["GoValueError"]) + ")"}, {"ToGo", "func(T) any"}, {"Fields", "func() []_borkGoStructField"},
		} {
			typ, err := parser.ParseExpr(spec.typ)
			if err != nil {
				panic(err)
			}
			st.Fields.List = append(st.Fields.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent(spec.name)}, Type: typ})
		}
	}
	return &ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{
		Name:       className(class),
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
	collision := false
	if !class.Prelude && !check.IsCodec(class, "Decode") && !check.IsCodec(class, "Encode") {
		for _, bound := range tp.Bounds {
			collision = collision || (bound != class && bound.Name == class.Name)
		}
	}
	if !collision {
		return ast.NewIdent("_d_" + tp.Name + "_" + class.Name)
	}
	counts := map[string]int{}
	reserved := map[string]bool{}
	for _, bound := range tp.Bounds {
		counts[bound.Name]++
		reserved[bound.Name] = true
	}
	tags := map[*check.Class]string{}
	for _, bound := range tp.Bounds {
		tag := bound.Name
		if counts[tag] > 1 && !bound.Prelude && !check.IsCodec(bound, "Decode") && !check.IsCodec(bound, "Encode") {
			// Reserve every declared class name before assigning alternatives,
			// retaining the standard codec bridge names even with collisions.
			stem := className(bound).Name
			if stem == bound.Name {
				stem += "_local"
			}
			tag = stem
			for serial := 2; reserved[tag]; serial++ {
				tag = fmt.Sprintf("%s_%d", stem, serial)
			}
			reserved[tag] = true
		}
		tags[bound] = tag
	}
	tag := tags[class]
	if tag == "" {
		tag = class.Name
	}
	return ast.NewIdent("_d_" + tp.Name + "_" + tag)
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
	savedScope := g.shapeScope
	g.shapeScope = ci
	defer func() { g.shapeScope = savedScope }()
	lit := &ast.CompositeLit{Type: g.classType(ci.Class, ci.Type)}
	if ci.Class.ForeignRecord != nil {
		lit = g.foreignDictionary(ci)
	}
	if len(ci.Metadata) > 0 {
		lit.Elts = append(lit.Elts, &ast.KeyValueExpr{Key: ast.NewIdent("_metadata"), Value: g.instanceMetadata(ci)})
	}
	for i, m := range ci.Methods {
		var fn ast.Expr = g.funcName(m)
		if len(ci.TypeParams) > 0 || len(ci.Captures) > 0 {
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
			callable := fn
			if len(ci.TypeParams) > 0 {
				callable = idx
			}
			args = append(args, g.shapeCaptureArguments(ci)...)
			for _, p := range m.TypeParams {
				if g.typeMembership[p] {
					args = append(args, membershipName(p))
				}
			}
			call := &ast.CallExpr{Fun: callable, Args: args}
			var body []ast.Stmt
			if m.Result == check.Ok {
				body = []ast.Stmt{&ast.ExprStmt{X: call}}
			} else {
				body = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{call}}}
			}
			fn = &ast.FuncLit{Type: g.funcType(ft, names), Body: &ast.BlockStmt{List: body}}
		}
		method := ci.Class.Methods[i]
		actual := &check.FuncType{Params: m.Params, Result: m.Result}
		declared := &check.FuncType{Params: method.Params, Result: method.Result}
		if hasTupleRepresentation(actual) {
			params, args := []*check.TypeParam{ci.Class.Param}, []check.Type{ci.Type}
			fn = g.representationConversion(fn, actual, declared, g.goType(actual), g.parameterGoType(declared, params, args), params, args, true)
		}
		lit.Elts = append(lit.Elts, &ast.KeyValueExpr{Key: name(method.Decl.Name), Value: fn})
	}
	if check.IsCodec(ci.Class, "Decode") {
		kind, _ := parser.ParseExpr(g.decodeKind(ci.Type))
		lit.Elts = append(lit.Elts,
			&ast.KeyValueExpr{Key: ast.NewIdent("kind"), Value: kind},
			&ast.KeyValueExpr{Key: ast.NewIdent("optional"), Value: ast.NewIdent(fmt.Sprintf("%t", check.IsOption(ci.Type)))})
	}
	return &ast.FuncDecl{
		Name: ast.NewIdent(instName(ci)),
		Type: &ast.FuncType{
			TypeParams: typeParamList(ci.TypeParams),
			Params:     &ast.FieldList{List: append(g.shapeDictionaryParameters(ci), g.membershipParams(ci.TypeParams)...)},
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
	if d.Builtin {
		// Built-in equality and rendering dispatch through universal helpers.
		method := "equals"
		if check.IsShow(d.Class) {
			method = "show"
		}
		fun, _ := g.dictMethod(d, method)
		return &ast.CompositeLit{Type: g.classType(d.Class, d.Type), Elts: []ast.Expr{
			&ast.KeyValueExpr{Key: ast.NewIdent(method), Value: fun},
		}}
	}
	if tuple, ok := d.Type.(*check.Record); ok && tuple.Tuple {
		// Retag generic tuple implementations through their checked method
		// signatures instead of returning a dictionary with formal Go tags.
		var fields []ast.Expr
		fields = append(fields, &ast.KeyValueExpr{Key: ast.NewIdent("_metadata"), Value: &ast.SelectorExpr{X: g.declaredDictionary(d), Sel: ast.NewIdent("_metadata")}})
		for _, method := range d.Class.Methods {
			var implementation *check.Func
			for _, candidate := range d.Inst.Methods {
				if candidate.Decl.Name == method.Decl.Name {
					implementation = candidate
				}
			}
			signature := check.SubstituteType(&check.FuncType{Params: method.Params, Result: method.Result}, []*check.TypeParam{d.Class.Param}, []check.Type{d.Type}).(*check.FuncType)
			copy := *implementation
			copy.Class = nil
			copy.TemplateScope = nil
			reference := &check.Instance{Func: &copy, TypeArgs: d.TypeArgs, Dicts: d.Args, Params: signature.Params, Result: signature.Result}
			fields = append(fields, &ast.KeyValueExpr{Key: name(method.Decl.Name), Value: g.funcRef(reference, g.dictionaryCaptureArguments(d)...)})
		}
		return &ast.CompositeLit{Type: g.classType(d.Class, d.Type), Elts: fields}
	}

	return g.declaredDictionary(d)
}

func (g *gen) declaredDictionary(d *check.Dict) ast.Expr {
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
	if len(d.Captures) > 0 {
		_, captures := g.values(d.Captures)
		args = append(args, captures...)
	} else {
		args = append(args, g.shapeCaptureArguments(d.Inst)...)
	}
	args = append(args, g.membershipArgs(d.Inst.TypeParams, d.TypeArgs)...)
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
		fun := &ast.SelectorExpr{X: dictParam(d.Param, d.Class), Sel: name(method)}
		for _, source := range d.Class.Methods {
			if source.Decl.Name == method {
				return g.tupleMethod(d, source, []check.Type{d.Type}, fun, nil)
			}
		}
		return fun, nil
	}
	if d.Builtin {
		if check.IsShow(d.Class) {
			g.usesShow = true
			return &ast.IndexExpr{X: ast.NewIdent("_strOf"), Index: g.goType(d.Type)}, nil
		}
		g.usesEqual = true
		return &ast.IndexExpr{X: ast.NewIdent("_equalOf"), Index: g.goType(d.Type)}, nil
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
	return g.tupleMethod(d, impl, d.TypeArgs, fun, dicts)
}

func (g *gen) tupleMethod(d *check.Dict, source *check.Func, typeArgs []check.Type, fun ast.Expr, dicts []ast.Expr) (ast.Expr, []ast.Expr) {
	var method *check.Func
	for _, candidate := range d.Class.Methods {
		if candidate.Decl.Name == source.Decl.Name {
			method = candidate
		}
	}
	declared := &check.FuncType{Params: method.Params, Result: method.Result}
	implementation := &check.FuncType{Params: source.Params, Result: source.Result}
	if !hasTupleRepresentation(declared) && !hasTupleRepresentation(implementation) && len(shapeCaptures(source)) == 0 && len(g.membershipParams(source.TypeParams)) == 0 {
		return fun, dicts
	}
	want := check.SubstituteType(declared, []*check.TypeParam{d.Class.Param}, []check.Type{d.Type}).(*check.FuncType)
	copy := *source
	copy.Class = nil
	inst := &check.Instance{Func: &copy, TypeArgs: typeArgs, Params: want.Params, Result: want.Result}
	var names []*ast.Ident
	args := append([]ast.Expr(nil), dicts...)
	for i := range want.Params {
		n := ast.NewIdent(fmt.Sprintf("_p%d", i))
		names = append(names, n)
		args = append(args, g.instanceArgument(inst, i, n))
	}
	if len(d.Captures) > 0 {
		_, captures := g.values(d.Captures)
		args = append(args, captures...)
	} else {
		args = append(args, g.shapeCaptureArguments(source.TemplateScope)...)
	}
	args = append(args, g.membershipArgs(source.TypeParams, typeArgs)...)
	call := &ast.CallExpr{Fun: fun, Args: args}
	body := []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{g.instanceResult(inst, call)}}}
	if want.Result == check.Ok || want.Result == check.Never {
		body = []ast.Stmt{&ast.ExprStmt{X: call}}
	}
	return &ast.FuncLit{Type: g.funcType(want, names), Body: &ast.BlockStmt{List: body}}, nil
}

// funcRef is the Go value of a function used as a value (inst), when it
// is a class method or needs instances: those are filled in, by a
// closure if needed.
func (g *gen) funcRef(inst *check.Instance, needs ...ast.Expr) ast.Expr {
	var fun ast.Expr
	var dicts []ast.Expr
	if inst.Func.Class == nil {
		needs = append(g.shapeCaptureArguments(inst.Func.TemplateScope), needs...)
		needs = append(needs, g.membershipArgs(inst.Func.TypeParams, inst.TypeArgs)...)
	}
	if inst.Func.Class != nil {
		fun, dicts = g.methodFunc(inst)
	} else {
		fun = g.instance(inst)
		for _, d := range inst.Dicts {
			dicts = append(dicts, g.dict(d))
		}
	}
	if len(dicts) == 0 && len(needs) == 0 && !collapsedUnionSignature(inst) && (len(inst.TypeArgs) == 0 || !hasTupleRepresentation(&check.FuncType{Params: inst.Params, Result: inst.Result})) {
		if hasTupleRepresentation(&check.FuncType{Params: inst.Params, Result: inst.Result}) {
			return &ast.CallExpr{Fun: g.funcType(&check.FuncType{Params: inst.Params, Result: inst.Result}, nil), Args: []ast.Expr{fun}}
		}
		return fun
	}
	ft := &check.FuncType{Params: inst.Params, Result: inst.Result}
	var names []*ast.Ident
	args := dicts
	for j := range inst.Params {
		names = append(names, ast.NewIdent(fmt.Sprintf("_p%d", j)))
		args = append(args, g.instanceArgument(inst, j, names[j]))
	}
	args = append(args, needs...)
	call := &ast.CallExpr{Fun: fun, Args: args}
	body := []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{g.instanceResult(inst, call)}}}
	if inst.Result == check.Ok || inst.Result == check.Never {
		body = []ast.Stmt{&ast.ExprStmt{X: call}}
	}
	return &ast.FuncLit{Type: g.funcType(ft, names), Body: &ast.BlockStmt{List: body}}
}
