package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
)

func (g *gen) tupleIdentity(tuple *check.Record) ast.Expr {
	fields := &ast.FieldList{}
	for i, field := range tuple.Fields {
		fields.List = append(fields.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent("T" + strconv.Itoa(i))}, Type: g.tupleElementIdentity(field.Type)})
	}
	return &ast.StructType{Fields: fields}
}

func (g *gen) tupleElementIdentity(t check.Type) ast.Expr {
	fields := &ast.FieldList{}
	add := func(name string, typ check.Type) {
		fields.List = append(fields.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent(name)}, Type: g.tupleElementIdentity(typ)})
	}
	effects := func(e check.Effects) {
		fields.List[len(fields.List)-1].Tag = &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote("bork:" + strconv.Quote(e.String()))}
	}
	switch t := t.(type) {
	case *check.Union:
		for i, member := range t.Members {
			add("U"+strconv.Itoa(i), member)
		}
	case *check.FuncType:
		for i, param := range t.Params {
			add("P"+strconv.Itoa(i), param)
		}
		add("R", t.Result)
		effects(t.Effects)
	case *check.List:
		return &ast.ArrayType{Elt: g.tupleElementIdentity(t.Elem)}
	case *check.Map:
		add("K", t.Key)
		add("V", t.Value)
	case *check.Seq:
		add("S", t.Elem)
		effects(t.Effects)
	case *check.Record, *check.Sealed:
		args := check.TypeArgs(t)
		if tuple, ok := t.(*check.Record); ok && tuple.Tuple || len(args) == 0 {
			return g.goType(t)
		}
		fields.List = append(fields.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent("N")}, Type: g.goType(t)})
		for i, arg := range args {
			add("A"+strconv.Itoa(i), arg)
		}
	default:
		if t == check.Never {
			fields.List = append(fields.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent("Never")}, Type: &ast.StructType{Fields: &ast.FieldList{}}})
			break
		}
		return g.goType(t)
	}
	return &ast.StructType{Fields: fields}
}

func (g *gen) tupleCodec(d *check.Dict, tuple *check.Record) ast.Expr {
	json := g.info.Named["Json"].(*check.Sealed)
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
		errorType := g.typeText(g.info.Named["DecodeError"])
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
					ret := &ast.ReturnStmt{Results: []ast.Expr{&ast.CompositeLit{Type: g.goType(g.info.Named["DecodeError"]), Elts: []ast.Expr{
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
