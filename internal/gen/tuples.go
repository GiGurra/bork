package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
)

// Tuple tags preserve semantic distinctions erased by Go. Explicit Go
// conversions ignore struct tags, so generic call boundaries can retag values.
func tupleShapeKey(t check.Type) string {
	join := func(types []check.Type) string {
		parts := make([]string, len(types))
		for i, typ := range types {
			parts[i] = tupleShapeKey(typ)
		}
		return strings.Join(parts, ",")
	}
	switch t := t.(type) {
	case *check.Union:
		parts := make([]string, len(t.Members))
		for i, member := range t.Members {
			parts[i] = tupleShapeKey(member)
		}
		sort.Strings(parts)
		return "union[" + strings.Join(parts, ",") + "]"
	case *check.FuncType:
		return "func[" + join(t.Params) + "]" + t.Effects.String() + ":" + tupleShapeKey(t.Result)
	case *check.List:
		return "list[" + tupleShapeKey(t.Elem) + "]"
	case *check.Map:
		return "map[" + tupleShapeKey(t.Key) + "," + tupleShapeKey(t.Value) + "]"
	case *check.Seq:
		return "seq[" + tupleShapeKey(t.Elem) + "]" + t.Effects.String()
	case *check.Record:
		if t.Tuple {
			return "tuple[" + join(t.Args) + "]"
		}
		return tupleNamedKey(t.Name, t.Pkg) + "[" + join(t.Args) + "]"
	case *check.Sealed:
		return tupleNamedKey(t.Name, t.Pkg) + "[" + join(t.Args) + "]"
	case *check.Resource:
		return tupleNamedKey(t.Name, t.Pkg)
	case *check.Opaque:
		return tupleNamedKey(t.Name, t.Pkg)
	}
	return t.String()
}

func tupleNamedKey(name string, pkg *check.Package) string {
	if pkg == nil {
		return name
	}
	return pkg.Path + ":" + name
}

func hasTupleRepresentation(t check.Type) bool {
	switch t := t.(type) {
	case *check.Record:
		if t.Tuple {
			return true
		}
		for _, arg := range t.Args {
			if hasTupleRepresentation(arg) {
				return true
			}
		}
	case *check.Sealed:
		for _, arg := range t.Args {
			if hasTupleRepresentation(arg) {
				return true
			}
		}
	case *check.List:
		return hasTupleRepresentation(t.Elem)
	case *check.Map:
		return hasTupleRepresentation(t.Key) || hasTupleRepresentation(t.Value)
	case *check.Seq:
		return hasTupleRepresentation(t.Elem)
	case *check.FuncType:
		for _, p := range t.Params {
			if hasTupleRepresentation(p) {
				return true
			}
		}
		return hasTupleRepresentation(t.Result)
	case *check.Union:
		for _, member := range t.Members {
			if hasTupleRepresentation(member) {
				return true
			}
		}
	}
	return false
}

func (g *gen) parameterGoType(t check.Type, params []*check.TypeParam, args []check.Type) ast.Expr {
	saved := g.typeParamGoTypes
	overrides := make(map[*check.TypeParam]ast.Expr, len(saved)+len(params))
	for param, typ := range saved {
		overrides[param] = typ
	}
	for i, param := range params {
		overrides[param] = g.goType(args[i])
	}
	g.typeParamGoTypes = overrides
	defer func() { g.typeParamGoTypes = saved }()
	return g.goType(t)
}

func (g *gen) instanceArgument(inst *check.Instance, i int, value ast.Expr) ast.Expr {
	if inst.Func.Class != nil {
		return value // dictMethod exposes the checked, specialized signature
	}
	if adapted := g.okResultArgument(inst, i, value); adapted != nil {
		return adapted
	}
	if len(inst.TypeArgs) > 0 && hasTupleRepresentation(inst.Params[i]) {
		return g.representationConversion(value, inst.Params[i], inst.Func.Params[i], g.goType(inst.Params[i]), g.parameterGoType(inst.Func.Params[i], inst.Func.TypeParams, inst.TypeArgs), inst.Func.TypeParams, inst.TypeArgs, true)
	}
	return value
}

// okResultArgument adapts a function with no result (Go func(...)) to a
// generic parameter whose result is a type parameter bound to Ok, as in
// fork(s, () => ...) giving a Task[Ok]: Go sees func(...) _Ok.
func (g *gen) okResultArgument(inst *check.Instance, i int, value ast.Expr) ast.Expr {
	declared, ok := inst.Func.Params[i].(*check.FuncType)
	if !ok {
		return nil
	}
	param, ok := declared.Result.(*check.TypeParam)
	if !ok {
		return nil
	}
	bound := false
	for j, p := range inst.Func.TypeParams {
		if p == param && inst.TypeArgs[j] == check.Ok {
			bound = true
		}
	}
	if !bound {
		return nil
	}
	actual := inst.Params[i].(*check.FuncType)
	fun := ast.NewIdent("_okWork")
	signature := &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: g.goType(check.Ok)}}}}
	var args []ast.Expr
	for j, p := range actual.Params {
		arg := ast.NewIdent(fmt.Sprintf("_okArg%d", j))
		signature.Params.List = append(signature.Params.List, &ast.Field{Names: []*ast.Ident{arg}, Type: g.goType(p)})
		args = append(args, arg)
	}
	body := []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: fun, Args: args}}, &ast.ReturnStmt{Results: []ast.Expr{g.okValue()}}}
	// Bind the function first, so it is evaluated once, where it is given.
	return &ast.CallExpr{
		Fun:  &ast.FuncLit{Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{fun}, Type: g.goType(actual)}}}, Results: &ast.FieldList{List: []*ast.Field{{Type: signature}}}}, Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{&ast.FuncLit{Type: signature, Body: &ast.BlockStmt{List: body}}}}}}},
		Args: []ast.Expr{value},
	}
}

func (g *gen) tupleCodec(d *check.Dict, tuple *check.Record) ast.Expr {
	g.usesDerive = true
	g.goType(g.codecType("Field"))
	json := g.codecType("Value").(*check.Sealed)
	array := g.text(g.variantType(json.Variant("Array")))
	var source strings.Builder
	if d.Class.Name == "Encode" {
		fmt.Fprintf(&source, "func(x %s) %s { return %s{items: []%s{", g.typeText(tuple), g.typeText(json), array, g.typeText(json))
		for i, field := range tuple.Fields {
			fun, args := g.dictMethod(d.Args[i], "encode")
			args = append(args, &ast.SelectorExpr{X: ast.NewIdent("x"), Sel: name(field.Name)})
			source.WriteString(g.text(&ast.CallExpr{Fun: fun, Args: args}) + ",")
		}
		source.WriteString("}} }")
	} else {
		errorType := g.typeText(g.codecType("DecodeError"))
		fmt.Fprintf(&source, "func(json %s) any { a, ok := json.(%s); if !ok { %s }; if len(a.items) != %d { %s }; var result %s;", g.typeText(json), array, g.decodeError(`""`, `"expected an array, found " + _jsonKind(json)`), len(tuple.Fields), g.decodeError(`""`, strconv.Quote(fmt.Sprintf("expected an array of length %d", len(tuple.Fields)))), g.typeText(tuple))
		for i, field := range tuple.Fields {
			fun, args := g.dictMethod(d.Args[i], "decode")
			args = append(args, &ast.IndexExpr{X: &ast.SelectorExpr{X: ast.NewIdent("a"), Sel: ast.NewIdent("items")}, Index: ast.NewIdent(strconv.Itoa(i))})
			fmt.Fprintf(&source, "r%d := %s; if e, ok := r%d.(%s); ok { %s }; result.%s = r%d.(%s);", i, g.text(&ast.CallExpr{Fun: fun, Args: args}), i, errorType, g.decodeError(strconv.Quote(fmt.Sprintf("[%d]", i))+" + e.path", "e.message"), name(field.Name).Name, i, g.typeText(field.Type))
		}

		for _, field := range tuple.Fields {
			for _, con := range field.Constraints {
				statements := g.atFailurePath(g.fieldRead(ast.NewIdent("result"), field), field.Type, splitPath(con.Path), strLit("["+field.Name+"]"), func(x ast.Expr, t check.Type, path ast.Expr) []ast.Stmt {
					cond := g.constraintCond(con, x, t)
					if cond == nil {
						return nil
					}
					setup, failurePath, message := g.constraintFailure(con, x, t, path)
					ret := &ast.ReturnStmt{Results: []ast.Expr{&ast.CompositeLit{Type: g.goType(g.codecType("DecodeError")), Elts: []ast.Expr{
						&ast.KeyValueExpr{Key: ast.NewIdent("path"), Value: failurePath},
						&ast.KeyValueExpr{Key: ast.NewIdent("message"), Value: message},
					}}}}
					return []ast.Stmt{&ast.IfStmt{Cond: &ast.UnaryExpr{Op: token.NOT, X: paren(cond)}, Body: &ast.BlockStmt{List: append(setup, ret)}}}
				})
				for _, statement := range statements {
					source.WriteString(g.text(statement) + ";")
				}
			}
		}
		source.WriteString("return result }")
	}
	expr, err := parser.ParseExpr(source.String())
	if err != nil {
		panic(fmt.Sprintf("tuple codec: %v\n%s", err, source.String()))
	}
	return expr
}

func (g *gen) representationFieldGoType(owner check.Type, field *check.Field, params []*check.TypeParam, args []check.Type, declaration bool) ast.Expr {
	saved := g.typeParamGoTypes
	if declaration {
		overrides := make(map[*check.TypeParam]ast.Expr, len(saved)+len(params))
		for param, typ := range saved {
			overrides[param] = typ
		}
		for i, param := range params {
			overrides[param] = g.goType(args[i])
		}
		g.typeParamGoTypes = overrides
	}
	defer func() { g.typeParamGoTypes = saved }()
	if original, fieldParams, fieldArgs := tupleFieldDeclaration(owner, field); original != nil {
		return g.parameterGoType(original.Type, fieldParams, fieldArgs)
	}
	return g.goType(field.Type)
}

type tupleConversion struct {
	name      *ast.Ident
	recursive bool
}

// representationConversion converts the declared Go layout of a generic
// signature to its checked specialization, or back for an argument. Generic
// unions can collapse, so retagging sometimes also needs element conversions.
func (g *gen) representationConversion(value ast.Expr, from, to check.Type, fromGo, toGo ast.Expr, params []*check.TypeParam, args []check.Type, toDeclaration bool) ast.Expr {
	_, fromUnion := from.(*check.Union)
	_, toUnion := to.(*check.Union)
	needsUnionConversion := fromUnion && hasTupleRepresentation(from) || toUnion && hasTupleRepresentation(to)
	if g.text(fromGo) == g.text(toGo) && !needsUnionConversion {
		return value
	}
	key := tupleShapeKey(from) + "->" + tupleShapeKey(to) + ":" + g.text(fromGo) + "->" + g.text(toGo)
	if active := g.tupleConversions[key]; active != nil {
		active.recursive = true
		return &ast.CallExpr{Fun: active.name, Args: []ast.Expr{value}}
	}
	if g.tupleConversions == nil {
		g.tupleConversions = map[string]*tupleConversion{}
	}
	active := &tupleConversion{name: g.newTmp()}
	g.tupleConversions[key] = active
	defer delete(g.tupleConversions, key)
	repr := func(t check.Type, declaration bool) ast.Expr {
		if declaration {
			return g.parameterGoType(t, params, args)
		}
		return g.goType(t)
	}
	child := func(x ast.Expr, a, b check.Type) ast.Expr {
		return g.representationConversion(x, a, b, repr(a, !toDeclaration), repr(b, toDeclaration), params, args, toDeclaration)
	}
	closure := func(body []ast.Stmt) ast.Expr {
		signature := &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("_tupleValue")}, Type: fromGo}}}, Results: &ast.FieldList{List: []*ast.Field{{Type: toGo}}}}
		converter := &ast.FuncLit{Type: signature, Body: &ast.BlockStmt{List: body}}
		if !active.recursive {
			return &ast.CallExpr{Fun: converter, Args: []ast.Expr{value}}
		}
		input := ast.NewIdent("_tupleInput")
		wrapper := &ast.FuncLit{Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{input}, Type: fromGo}}}, Results: signature.Results}, Body: &ast.BlockStmt{List: []ast.Stmt{
			varDecl(active.name, signature),
			assign(active.name, converter),
			&ast.ReturnStmt{Results: []ast.Expr{&ast.CallExpr{Fun: active.name, Args: []ast.Expr{input}}}},
		}}}
		return &ast.CallExpr{Fun: wrapper, Args: []ast.Expr{value}}
	}

	x := ast.NewIdent("_tupleValue")
	var declaredUnion *check.Union
	if toDeclaration {
		declaredUnion, _ = to.(*check.Union)
	} else {
		declaredUnion, _ = from.(*check.Union)
	}
	if declaredUnion != nil && hasTupleRepresentation(declaredUnion) {
		var body []ast.Stmt
		for _, original := range declaredUnion.Members {
			bound := check.SubstituteType(original, params, args)
			a, b := original, bound
			if toDeclaration {
				a, b = bound, original
			}
			if !hasTupleRepresentation(a) && !hasTupleRepresentation(b) {
				continue
			}
			if _, expands := a.(*check.Union); expands {
				continue // a type parameter's stored value already has its concrete representation
			}
			v, ok := ast.NewIdent("_member"), ast.NewIdent("_ok")
			body = append(body, &ast.IfStmt{Init: &ast.AssignStmt{Lhs: []ast.Expr{v, ok}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.TypeAssertExpr{X: x, Type: repr(a, !toDeclaration)}}}, Cond: ok, Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{child(v, a, b)}}}}})
		}
		if toUnion {
			body = append(body, &ast.ReturnStmt{Results: []ast.Expr{x}})
		} else {
			body = append(body, &ast.ReturnStmt{Results: []ast.Expr{&ast.TypeAssertExpr{X: x, Type: toGo}}})
		}
		return closure(body)
	}
	if a, ok := from.(*check.Record); ok && a.Tuple {
		if b, ok := to.(*check.Record); ok && b.Tuple {
			result := &ast.CompositeLit{Type: toGo}
			for i, f := range a.Fields {
				read := &ast.SelectorExpr{X: x, Sel: name(f.Name)}
				result.Elts = append(result.Elts, &ast.KeyValueExpr{Key: name(f.Name), Value: child(read, f.Type, b.Fields[i].Type)})
			}
			return closure([]ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{result}}})
		}
	}
	nominalFields := func(root ast.Expr, sourceOwner, targetOwner check.Type, sourceFields, targetFields []*check.Field, typ ast.Expr) ast.Expr {
		result := &ast.CompositeLit{Type: typ}
		for i, field := range sourceFields {
			target := targetFields[i]
			fromFieldGo := g.representationFieldGoType(sourceOwner, field, params, args, !toDeclaration)
			toFieldGo := g.representationFieldGoType(targetOwner, target, params, args, toDeclaration)
			read := g.fieldRead(root, field)
			converted := g.representationConversion(read, field.Type, target.Type, fromFieldGo, toFieldGo, params, args, toDeclaration)
			if field.Lazy {
				g.usesLazy = true
				callback := &ast.FuncLit{Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: toFieldGo}}}}, Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{converted}}}}}
				converted = &ast.CallExpr{Fun: &ast.IndexExpr{X: ast.NewIdent("_lazyNew"), Index: toFieldGo}, Args: []ast.Expr{callback}}
			}
			result.Elts = append(result.Elts, &ast.KeyValueExpr{Key: name(field.Name), Value: converted})
		}
		return result
	}
	if a, ok := from.(*check.Record); ok && !a.Tuple {
		if b, ok := to.(*check.Record); ok && !b.Tuple {
			return closure([]ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{nominalFields(x, a, b, a.Fields, b.Fields, toGo)}}})
		}
	}
	if a, ok := from.(*check.Sealed); ok {
		if b, ok := to.(*check.Sealed); ok {
			variantGoType := func(v *check.Variant, declaration bool) ast.Expr {
				typ := repr(v.Parent, declaration)
				switch typ := typ.(type) {
				case *ast.Ident:
					typ.Name += "_" + v.Name
				case *ast.IndexListExpr:
					typ.X.(*ast.Ident).Name += "_" + v.Name
				}
				return typ
			}
			v := ast.NewIdent("_tupleVariant")
			sw := &ast.TypeSwitchStmt{Assign: define(v, &ast.TypeAssertExpr{X: x}), Body: &ast.BlockStmt{}}
			for i, source := range a.Variants {
				target := b.Variants[i]
				result := nominalFields(v, a, b, source.Fields, target.Fields, variantGoType(target, toDeclaration))
				sw.Body.List = append(sw.Body.List, &ast.CaseClause{List: []ast.Expr{variantGoType(source, !toDeclaration)}, Body: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{result}}}})
			}
			return closure(append([]ast.Stmt{sw}, unreachable()...))
		}
	}

	if a, ok := from.(*check.List); ok {
		if b, ok := to.(*check.List); ok {
			result, i := ast.NewIdent("_tupleResult"), ast.NewIdent("i")
			body := []ast.Stmt{define(result, &ast.CallExpr{Fun: ast.NewIdent("make"), Args: []ast.Expr{toGo, &ast.CallExpr{Fun: ast.NewIdent("len"), Args: []ast.Expr{x}}}}), &ast.RangeStmt{Key: i, Tok: token.DEFINE, X: x, Body: &ast.BlockStmt{List: []ast.Stmt{&ast.AssignStmt{Lhs: []ast.Expr{&ast.IndexExpr{X: result, Index: i}}, Tok: token.ASSIGN, Rhs: []ast.Expr{child(&ast.IndexExpr{X: x, Index: i}, a.Elem, b.Elem)}}}}}, &ast.ReturnStmt{Results: []ast.Expr{result}}}
			return closure(body)
		}
	}
	if a, ok := from.(*check.Map); ok {
		if b, ok := to.(*check.Map); ok {
			result, k, v := ast.NewIdent("_tupleResult"), ast.NewIdent("k"), ast.NewIdent("v")
			callback := &ast.FuncLit{Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{k}, Type: repr(a.Key, !toDeclaration)}, {Names: []*ast.Ident{v}, Type: repr(a.Value, !toDeclaration)}}}, Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("bool")}}}}, Body: &ast.BlockStmt{List: []ast.Stmt{assign(result, &ast.CallExpr{Fun: &ast.SelectorExpr{X: result, Sel: ast.NewIdent("put")}, Args: []ast.Expr{child(k, a.Key, b.Key), child(v, a.Value, b.Value)}}), &ast.ReturnStmt{Results: []ast.Expr{ast.NewIdent("true")}}}}}
			return closure([]ast.Stmt{&ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{&ast.ValueSpec{Names: []*ast.Ident{result}, Type: toGo}}}}, &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: x, Sel: ast.NewIdent("each")}, Args: []ast.Expr{callback}}}, &ast.ReturnStmt{Results: []ast.Expr{result}}})
		}
	}
	if a, ok := from.(*check.Seq); ok {
		if b, ok := to.(*check.Seq); ok {
			callback := &ast.FuncLit{Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{x}, Type: repr(a.Elem, !toDeclaration)}}}, Results: &ast.FieldList{List: []*ast.Field{{Type: repr(b.Elem, toDeclaration)}}}}, Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{child(x, a.Elem, b.Elem)}}}}}
			return closure([]ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{&ast.CallExpr{Fun: ast.NewIdent("_seqmap"), Args: []ast.Expr{x, callback}}}}})
		}
	}
	if a, ok := from.(*check.FuncType); ok {
		if b, ok := to.(*check.FuncType); ok {
			signature := &ast.FuncType{Params: &ast.FieldList{}}
			var values []ast.Expr
			for i, p := range b.Params {
				n := ast.NewIdent(fmt.Sprintf("_p%d", i))
				signature.Params.List = append(signature.Params.List, &ast.Field{Names: []*ast.Ident{n}, Type: repr(p, toDeclaration)})
				values = append(values, g.representationConversion(n, p, a.Params[i], repr(p, toDeclaration), repr(a.Params[i], !toDeclaration), params, args, !toDeclaration))
			}
			call := &ast.CallExpr{Fun: x, Args: values}
			var body []ast.Stmt
			if b.Result == check.Ok || b.Result == check.Never {
				body = []ast.Stmt{&ast.ExprStmt{X: call}}
			} else {
				signature.Results = &ast.FieldList{List: []*ast.Field{{Type: repr(b.Result, toDeclaration)}}}
				body = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{child(call, a.Result, b.Result)}}}
			}
			return closure([]ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{&ast.FuncLit{Type: signature, Body: &ast.BlockStmt{List: body}}}}})
		}
	}
	if _, union := from.(*check.Union); union {
		if _, staysUnion := to.(*check.Union); !staysUnion && g.text(fromGo) == "any" {
			return &ast.TypeAssertExpr{X: value, Type: toGo}
		}
	}
	return &ast.CallExpr{Fun: toGo, Args: []ast.Expr{value}}
}

func tupleFieldDeclaration(owner check.Type, field *check.Field) (original *check.Field, params []*check.TypeParam, args []check.Type) {
	switch owner := owner.(type) {
	case *check.Record:
		if owner.Base != nil && !owner.Tuple {
			return owner.Base.Field(field.Name), owner.Base.TypeParams, owner.Args
		}
	case *check.Sealed:
		if owner.Base != nil {
			for _, variant := range owner.Base.Variants {
				for _, candidate := range variant.Fields {
					if candidate.Decl == field.Decl {
						return candidate, owner.Base.TypeParams, owner.Args
					}
				}
			}
		}
	}
	return nil, nil, nil
}

func (g *gen) tupleFieldRead(root ast.Expr, owner check.Type, field *check.Field) ast.Expr {
	value := g.fieldRead(root, field)
	if hasTupleRepresentation(field.Type) {
		if original, params, args := tupleFieldDeclaration(owner, field); original != nil {
			return g.representationConversion(value, original.Type, field.Type, g.parameterGoType(original.Type, params, args), g.goType(field.Type), params, args, false)
		}
	}
	return value
}

func (g *gen) tupleFieldCell(thunk *check.Lambda, metadata *check.LazyDescription, owner check.Type, field *check.Field) ast.Expr {
	if hasTupleRepresentation(field.Type) {
		if original, params, args := tupleFieldDeclaration(owner, field); original != nil {
			source := thunk.Type().(*check.FuncType)
			target := &check.FuncType{Result: original.Type}
			callback := g.representationConversion(g.lambda(thunk), source, target, g.goType(source), g.parameterGoType(target, params, args), params, args, true)
			g.usesLazy = true
			constructor := "_lazyNew"
			if g.evalMode && metadata != nil && metadata.Effects == "nothing" {
				constructor = "_lazyConstNew"
			}
			return &ast.CallExpr{Fun: &ast.IndexExpr{X: ast.NewIdent(constructor), Index: g.parameterGoType(original.Type, params, args)}, Args: []ast.Expr{callback}}
		}
	}
	return g.fieldCell(thunk, metadata)
}
