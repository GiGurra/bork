package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"

	"github.com/GiGurra/bork/internal/check"
)

// goType maps a bork type to the Go type that represents it.
//
//   - records become Go structs (values, so copies are cheap and == works)
//   - a sealed type becomes an interface, with one struct per variant
//   - Option[T] becomes the runtime's generic Option[T]
//   - a union becomes `any`; matching on it uses a type switch
func (g *gen) goType(t check.Type) ast.Expr {
	switch t := t.(type) {
	case *check.Record:
		return typeName(t.Name)
	case *check.Sealed:
		if check.IsOption(t) {
			g.usesOption = true
			return &ast.IndexExpr{X: ast.NewIdent("Option"), Index: g.goType(t.Args[0])}
		}
		return typeName(t.Name)
	case *check.Union:
		return ast.NewIdent("any")
	}
	switch t {
	case check.Int:
		return ast.NewIdent("int64")
	case check.Bool:
		return ast.NewIdent("bool")
	case check.String:
		return ast.NewIdent("string")
	}
	panic(fmt.Sprintf("no Go type for %s", t))
}

// typeName maps a declared bork type name to its Go name.
func typeName(s string) *ast.Ident { return name(s) }

// variantType is the Go struct type of a sealed type's variant.
func (g *gen) variantType(v *check.Variant) ast.Expr {
	if check.IsOption(v.Parent) {
		g.usesOption = true
		return &ast.IndexExpr{X: ast.NewIdent("Option_" + v.Name), Index: g.goType(v.Parent.Args[0])}
	}
	return ast.NewIdent(typeName(v.Parent.Name).Name + "_" + v.Name)
}

func markerMethod(sealedName string) string { return "is" + typeName(sealedName).Name }

// typeDecls generates Go declarations for the package's records and
// sealed types, including String methods so values print in bork
// syntax.
func (g *gen) typeDecls() []ast.Decl {
	var decls []ast.Decl
	for _, t := range g.info.TypeOrder {
		switch t := t.(type) {
		case *check.Record:
			decls = append(decls, g.structDecl(typeName(t.Name), t.Fields))
			decls = append(decls, g.stringMethod(typeName(t.Name), t.Name, t.Fields, true))
		case *check.Sealed:
			marker := markerMethod(t.Name)
			decls = append(decls, &ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{
				Name: typeName(t.Name),
				Type: &ast.InterfaceType{Methods: &ast.FieldList{List: []*ast.Field{{
					Names: []*ast.Ident{ast.NewIdent(marker)},
					Type:  &ast.FuncType{Params: &ast.FieldList{}},
				}}}},
			}}})
			for _, v := range t.Variants {
				vt := g.variantType(v).(*ast.Ident)
				decls = append(decls, g.structDecl(vt, v.Fields))
				decls = append(decls, &ast.FuncDecl{
					Recv: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent(vt.Name)}}},
					Name: ast.NewIdent(marker),
					Type: &ast.FuncType{Params: &ast.FieldList{}},
					Body: &ast.BlockStmt{},
				})
				decls = append(decls, g.stringMethod(ast.NewIdent(vt.Name), t.Name+"."+v.Name, v.Fields, false))
			}
		}
	}
	return decls
}

func (g *gen) structDecl(n *ast.Ident, fields []*check.Field) ast.Decl {
	st := &ast.StructType{Fields: &ast.FieldList{}}
	for _, f := range fields {
		st.Fields.List = append(st.Fields.List, &ast.Field{Names: []*ast.Ident{name(f.Name)}, Type: g.goType(f.Type)})
	}
	return &ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{Name: ast.NewIdent(n.Name), Type: st}}}
}

// stringMethod generates `func (v T) String() string` rendering the
// value as `Label { field: value, ... }`. String fields are quoted.
// A variant without fields renders as just its label.
func (g *gen) stringMethod(recv *ast.Ident, label string, fields []*check.Field, isRecord bool) ast.Decl {
	strLit := func(s string) ast.Expr { return &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(s)} }
	var result ast.Expr
	switch {
	case len(fields) == 0 && isRecord:
		result = strLit(label + " {}")
	case len(fields) == 0:
		result = strLit(label)
	default:
		g.usesShow = true
		for i, f := range fields {
			prefix := ", " + f.Name + ": "
			if i == 0 {
				prefix = label + " { " + f.Name + ": "
			}
			show := &ast.CallExpr{Fun: ast.NewIdent("_show"), Args: []ast.Expr{
				&ast.SelectorExpr{X: ast.NewIdent("v"), Sel: name(f.Name)},
			}}
			part := &ast.BinaryExpr{X: strLit(prefix), Op: token.ADD, Y: show}
			if result == nil {
				result = part
			} else {
				result = &ast.BinaryExpr{X: result, Op: token.ADD, Y: part}
			}
		}
		result = &ast.BinaryExpr{X: result, Op: token.ADD, Y: strLit(" }")}
	}
	recvName := ast.NewIdent("v")
	if len(fields) == 0 {
		recvName = ast.NewIdent("_")
	}
	return &ast.FuncDecl{
		Recv: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{recvName}, Type: ast.NewIdent(recv.Name)}}},
		Name: ast.NewIdent("String"),
		Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}}}},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{result}}}},
	}
}

// The runtime is hand-written Go that generated programs share. It is
// parsed (not pasted) so it goes through the same printer as everything
// else.
const optionRuntime = `package main

// Option is bork's built-in Option[T]: sealed { Some { value: T }, None }.
type Option[T any] interface{ isOption(T) }

type Option_Some[T any] struct{ value T }

type Option_None[T any] struct{}

func (Option_Some[T]) isOption(T) {}

func (Option_None[T]) isOption(T) {}

func (v Option_Some[T]) String() string { return "Option.Some { value: " + _show(v.value) + " }" }

func (Option_None[T]) String() string { return "Option.None" }
`

const showRuntime = `package main

import (
	"fmt"
	"strconv"
)

// _show renders a field value for String methods: strings are quoted,
// everything else is printed as usual.
func _show(x any) string {
	if s, ok := x.(string); ok {
		return strconv.Quote(s)
	}
	return fmt.Sprint(x)
}
`

// runtimeDecls parses the runtime support the program needs. The
// declarations keep their positions in the returned file set, so their
// comments print correctly.
func (g *gen) runtimeDecls() ([]ast.Decl, *token.FileSet, error) {
	var src []string
	if g.usesOption {
		g.usesShow = true
		src = append(src, optionRuntime)
	}
	if g.usesShow {
		src = append(src, showRuntime)
	}
	fset := token.NewFileSet()
	var decls []ast.Decl
	for i, s := range src {
		f, err := parser.ParseFile(fset, fmt.Sprintf("runtime%d.go", i), s, parser.ParseComments)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing bork runtime (compiler bug): %w", err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			g.imports[path] = true
		}
		for _, d := range f.Decls {
			if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
				continue
			}
			decls = append(decls, d)
		}
	}
	return decls, fset, nil
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
