package gen

import (
	"go/ast"
	"go/token"

	"github.com/GiGurra/bork/internal/check"
)

// valueMethods let the runtime dispatch to structural operations without
// reflecting over a record's fields. Generic fields keep their runtime type.
func (g *gen) valueMethods(recv ast.Expr, fields []*check.Field) []ast.Decl {
	g.usesEqual, g.usesHash = true, true
	v, w, h := ast.NewIdent("v"), ast.NewIdent("w"), ast.NewIdent("h")
	var equal ast.Expr = ast.NewIdent("true")
	hash := []ast.Stmt{define(h, &ast.CallExpr{Fun: ast.NewIdent("uint64"), Args: []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: "0"}}})}
	for _, f := range fields {
		x := g.fieldRead(v, f)
		y := g.fieldRead(w, f)
		equal = &ast.BinaryExpr{X: equal, Op: token.LAND, Y: g.equalValue(x, y, f.Type)}
		hash = append(hash, assign(h, &ast.CallExpr{Fun: ast.NewIdent("_hashMix"), Args: []ast.Expr{h, g.hashValue(x, f.Type)}}))
	}
	hash = append(hash, &ast.ReturnStmt{Results: []ast.Expr{h}})
	assertOther := &ast.AssignStmt{Lhs: []ast.Expr{w, ast.NewIdent("ok")}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.TypeAssertExpr{X: ast.NewIdent("other"), Type: recv}}}
	method := func(n string, params []*ast.Field, result string, body []ast.Stmt) ast.Decl {
		return &ast.FuncDecl{
			Recv: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{v}, Type: recv}}},
			Name: ast.NewIdent(n),
			Type: &ast.FuncType{Params: &ast.FieldList{List: params}, Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent(result)}}}},
			Body: &ast.BlockStmt{List: body},
		}
	}
	return []ast.Decl{
		method("_borkEqual", []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("other")}, Type: ast.NewIdent("any")}}, "bool", []ast.Stmt{assertOther, &ast.ReturnStmt{Results: []ast.Expr{&ast.BinaryExpr{X: ast.NewIdent("ok"), Op: token.LAND, Y: &ast.CallExpr{Fun: &ast.SelectorExpr{X: v, Sel: ast.NewIdent("_equals")}, Args: []ast.Expr{w}}}}}}),
		method("_equals", []*ast.Field{{Names: []*ast.Ident{w}, Type: recv}}, "bool", []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{equal}}}),
		method("_borkHash", nil, "uint64", hash),
	}
}

func (g *gen) equalValue(a, b ast.Expr, t check.Type) ast.Expr {
	g.usesEqual = true
	if list, ok := t.(*check.List); ok {
		return &ast.CallExpr{Fun: &ast.IndexExpr{X: ast.NewIdent("_equalList"), Index: g.goType(list.Elem)}, Args: []ast.Expr{a, b, g.equalFunc(list.Elem)}}
	}
	if _, ok := basicGoNames[t]; ok {
		return &ast.BinaryExpr{X: a, Op: token.EQL, Y: b}
	}
	switch t.(type) {
	case *check.Record, *check.Map:
		return &ast.CallExpr{Fun: &ast.SelectorExpr{X: a, Sel: ast.NewIdent("_equals")}, Args: []ast.Expr{b}}
	}
	return &ast.CallExpr{Fun: ast.NewIdent("_equal"), Args: []ast.Expr{a, b}}
}

func (g *gen) hashValue(x ast.Expr, t check.Type) ast.Expr {
	g.usesHash = true
	if list, ok := t.(*check.List); ok {
		return &ast.CallExpr{Fun: &ast.IndexExpr{X: ast.NewIdent("_hashList"), Index: g.goType(list.Elem)}, Args: []ast.Expr{x, g.hashFunc(list.Elem)}}
	}
	if _, ok := basicGoNames[t]; ok {
		return &ast.CallExpr{Fun: &ast.IndexExpr{X: ast.NewIdent("_hashOf"), Index: g.goType(t)}, Args: []ast.Expr{x}}
	}
	switch t.(type) {
	case *check.Record, *check.Map:
		return &ast.CallExpr{Fun: &ast.SelectorExpr{X: x, Sel: ast.NewIdent("_borkHash")}, Args: nil}
	}
	return &ast.CallExpr{Fun: ast.NewIdent("_hash"), Args: []ast.Expr{x}}
}

func (g *gen) showValue(x ast.Expr, t check.Type) ast.Expr {
	g.usesShow = true
	if list, ok := t.(*check.List); ok {
		return &ast.CallExpr{Fun: &ast.IndexExpr{X: ast.NewIdent("_showList"), Index: g.goType(list.Elem)}, Args: []ast.Expr{x, g.showFunc(list.Elem)}}
	}
	if _, ok := t.(*check.FuncType); ok {
		return strLit("<function>")
	}
	if t == check.String {
		g.imports["strconv"] = true
		return &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("strconv"), Sel: ast.NewIdent("Quote")}, Args: []ast.Expr{x}}
	}
	if check.IsFloat(t) {
		bits := "64"
		if t == check.Float32 {
			bits = "32"
		}
		return &ast.CallExpr{Fun: ast.NewIdent("_fmtFloat"), Args: []ast.Expr{&ast.CallExpr{Fun: ast.NewIdent("float64"), Args: []ast.Expr{x}}, &ast.BasicLit{Kind: token.INT, Value: bits}}}
	}
	if t == check.Bool || check.IsInteger(t) {
		g.imports["strconv"] = true
		fn := "FormatInt"
		var args []ast.Expr
		switch t {
		case check.Bool:
			fn = "FormatBool"
			args = []ast.Expr{x}
		case check.Uint8, check.Uint16, check.Uint32, check.Uint64:
			fn = "FormatUint"
			args = []ast.Expr{&ast.CallExpr{Fun: ast.NewIdent("uint64"), Args: []ast.Expr{x}}, &ast.BasicLit{Kind: token.INT, Value: "10"}}
		default:
			args = []ast.Expr{&ast.CallExpr{Fun: ast.NewIdent("int64"), Args: []ast.Expr{x}}, &ast.BasicLit{Kind: token.INT, Value: "10"}}
		}
		return &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("strconv"), Sel: ast.NewIdent(fn)}, Args: args}
	}
	return &ast.CallExpr{Fun: ast.NewIdent("_show"), Args: []ast.Expr{x}}
}

func (g *gen) equalFunc(t check.Type) ast.Expr {
	a, b := ast.NewIdent("a"), ast.NewIdent("b")
	return &ast.FuncLit{Type: g.funcType(&check.FuncType{Params: []check.Type{t, t}, Result: check.Bool}, []*ast.Ident{a, b}), Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{g.equalValue(a, b, t)}}}}}
}

func (g *gen) hashFunc(t check.Type) ast.Expr {
	x := ast.NewIdent("x")
	return &ast.FuncLit{Type: g.funcType(&check.FuncType{Params: []check.Type{t}, Result: check.Uint64}, []*ast.Ident{x}), Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{g.hashValue(x, t)}}}}}
}

func (g *gen) showFunc(t check.Type) ast.Expr {
	x := ast.NewIdent("x")
	return &ast.FuncLit{Type: g.funcType(&check.FuncType{Params: []check.Type{t}, Result: check.String}, []*ast.Ident{x}), Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{g.showValue(x, t)}}}}}
}
