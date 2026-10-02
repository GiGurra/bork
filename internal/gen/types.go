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
//   - List[T] becomes a slice []T, never modified once built
//   - a function type becomes a Go func type
//   - a type parameter becomes a Go type parameter
func (g *gen) goType(t check.Type) ast.Expr {
	switch t := t.(type) {
	case *check.TypeParam:
		return name(t.Name)
	case *check.List:
		return &ast.ArrayType{Elt: g.goType(t.Elem)}
	case *check.FuncType:
		return g.funcType(t, nil)
	case *check.Record:
		g.usedTypes[baseOf(t)] = true
		return g.instantiated(typeName(t.Name), t)
	case *check.Sealed:
		g.usedTypes[baseOf(t)] = true
		return g.instantiated(typeName(t.Name), t)
	case *check.Union:
		for _, m := range t.Members {
			g.goType(m) // the members' declarations are needed
		}
		return ast.NewIdent("any")
	case *check.Resource:
		g.usedTypes[t] = true
		return typeName(t.Name)
	}
	if t == check.Scope {
		g.usesScopes = true
		return &ast.StarExpr{X: ast.NewIdent("_Scope")}
	}
	if n, ok := basicGoNames[t]; ok {
		return ast.NewIdent(n)
	}
	panic(fmt.Sprintf("no Go type for %s", t))
}

// funcType is the Go func type for t, with the given parameter names
// (or none).
func (g *gen) funcType(t *check.FuncType, names []*ast.Ident) *ast.FuncType {
	ft := &ast.FuncType{Params: &ast.FieldList{}}
	for i, p := range t.Params {
		f := &ast.Field{Type: g.goType(p)}
		if names != nil {
			f.Names = []*ast.Ident{names[i]}
		}
		ft.Params.List = append(ft.Params.List, f)
	}
	if t.Result != check.Unit && t.Result != check.Never {
		ft.Results = &ast.FieldList{List: []*ast.Field{{Type: g.goType(t.Result)}}}
	}
	return ft
}

var basicGoNames = map[check.Type]string{
	check.Int: "int64", check.Int8: "int8", check.Int16: "int16", check.Int32: "int32",
	check.Uint8: "uint8", check.Uint16: "uint16", check.Uint32: "uint32", check.Uint64: "uint64",
	check.Float32: "float32", check.Float: "float64",
	check.Bool: "bool", check.String: "string",
}

// baseOf is the declared type t is an instance of (t itself if it is
// not an instance).
func baseOf(t check.Type) check.Type {
	switch t := t.(type) {
	case *check.Record:
		if t.Base != nil {
			return t.Base
		}
	case *check.Sealed:
		if t.Base != nil {
			return t.Base
		}
	}
	return t
}

// instantiated is the Go type n, with t's type arguments if t is
// generic: `Pair[int64, string]`.
func (g *gen) instantiated(n *ast.Ident, t check.Type) ast.Expr {
	args := check.TypeArgs(t)
	if len(args) == 0 {
		return n
	}
	idx := &ast.IndexListExpr{X: n}
	for _, a := range args {
		idx.Indices = append(idx.Indices, g.goType(a))
	}
	return idx
}

// typeParamList is a Go type parameter list `[A any, B any]`, or nil.
func typeParamList(params []*check.TypeParam) *ast.FieldList {
	if len(params) == 0 {
		return nil
	}
	f := &ast.Field{Type: ast.NewIdent("any")}
	for _, p := range params {
		f.Names = append(f.Names, name(p.Name))
	}
	return &ast.FieldList{List: []*ast.Field{f}}
}

// typeName maps a declared bork type name to its Go name.
func typeName(s string) *ast.Ident { return name(s) }

// variantType is the Go struct type of a sealed type's variant.
func (g *gen) variantType(v *check.Variant) ast.Expr {
	g.usedTypes[baseOf(v.Parent)] = true
	return g.instantiated(ast.NewIdent(typeName(v.Parent.Name).Name+"_"+v.Name), v.Parent)
}

func markerMethod(sealedName string) string { return "is" + typeName(sealedName).Name }

// typeDecls generates Go declarations for the package's records and
// sealed types, including String methods so values print in bork
// syntax. Prelude types are only declared if the program uses them.
func (g *gen) typeDecls() []ast.Decl {
	needed := func(t check.Type) bool {
		switch t := t.(type) {
		case *check.Record:
			return !t.Prelude || g.usedTypes[t]
		case *check.Sealed:
			return !t.Prelude || g.usedTypes[t]
		case *check.Resource:
			return !t.Prelude || g.usedTypes[t]
		}
		return false
	}
	// Declaring a type can make it use more prelude types (its fields).
	for {
		n := len(g.usedTypes)
		for _, t := range g.info.TypeOrder {
			if needed(t) {
				g.typeDecl(t)
			}
		}
		if len(g.usedTypes) == n {
			break
		}
	}
	var decls []ast.Decl
	for _, t := range g.info.TypeOrder {
		if needed(t) {
			decls = append(decls, g.typeDecl(t)...)
		}
	}
	return decls
}

func (g *gen) typeDecl(t check.Type) []ast.Decl {
	var decls []ast.Decl
	switch t := t.(type) {
	case *check.Resource:
		// A handle that unsafe go code fills in: File{handle: f}.
		src := fmt.Sprintf("package main\ntype %[1]s struct{ handle any }\nfunc (%[1]s) String() string { return \"<%[2]s>\" }\n", typeName(t.Name).Name, t.Name)
		f, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
		if err != nil {
			panic(err)
		}
		decls = append(decls, f.Decls...)
	case *check.Record:
		recv := g.instantiated(typeName(t.Name), t)
		decls = append(decls, g.structDecl(typeName(t.Name), t.TypeParams, t.Fields))
		decls = append(decls, g.stringMethod(recv, t.Name, t.Fields, true))
	case *check.Sealed:
		// The interface's marker method mentions the type parameters, so
		// that Option[int64] and Option[string] are different types.
		marker := markerMethod(t.Name)
		markerType := &ast.FuncType{Params: &ast.FieldList{}}
		for _, p := range t.TypeParams {
			markerType.Params.List = append(markerType.Params.List, &ast.Field{Type: name(p.Name)})
		}
		decls = append(decls, &ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{
			Name:       typeName(t.Name),
			TypeParams: typeParamList(t.TypeParams),
			Type: &ast.InterfaceType{Methods: &ast.FieldList{List: []*ast.Field{{
				Names: []*ast.Ident{ast.NewIdent(marker)},
				Type:  markerType,
			}}}},
		}}})
		for _, v := range t.Variants {
			vname := ast.NewIdent(typeName(t.Name).Name + "_" + v.Name)
			recv := g.instantiated(vname, t)
			decls = append(decls, g.structDecl(vname, t.TypeParams, v.Fields))
			decls = append(decls, &ast.FuncDecl{
				Recv: &ast.FieldList{List: []*ast.Field{{Type: recv}}},
				Name: ast.NewIdent(marker),
				Type: markerType,
				Body: &ast.BlockStmt{},
			})
			decls = append(decls, g.stringMethod(recv, t.Name+"."+v.Name, v.Fields, false))
		}
	}
	return decls
}

func (g *gen) structDecl(n *ast.Ident, params []*check.TypeParam, fields []*check.Field) ast.Decl {
	st := &ast.StructType{Fields: &ast.FieldList{}}
	for _, f := range fields {
		st.Fields.List = append(st.Fields.List, &ast.Field{Names: []*ast.Ident{name(f.Name)}, Type: g.goType(f.Type)})
	}
	return &ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{Name: ast.NewIdent(n.Name), TypeParams: typeParamList(params), Type: st}}}
}

// stringMethod generates `func (v T) String() string` rendering the
// value as `Label { field: value, ... }`. String fields are quoted.
// A variant without fields renders as just its label.
func (g *gen) stringMethod(recv ast.Expr, label string, fields []*check.Field, isRecord bool) ast.Decl {
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
		Recv: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{recvName}, Type: recv}}},
		Name: ast.NewIdent("String"),
		Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}}}},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{result}}}},
	}
}

// The runtime is hand-written Go that generated programs share. It is
// parsed (not pasted) so it goes through the same printer as everything
// else.
const assertRuntime = `package main

func _assert(ok bool, at string) {
	if !ok {
		panic(at + ": assertion failed")
	}
}

func _assertEqual[T comparable](actual, expected T, at string) {
	if actual != expected {
		panic(at + ": expected " + _show(expected) + ", got " + _show(actual))
	}
}
`

const testRuntime = `package main

import (
	"fmt"
	"os"
)

type _test struct {
	name string
	run  func() // nil for a test that cannot run; name says why
}

// _runTests runs the tests, each until it fails (panics), and reports.
func _runTests(tests []_test) {
	failed, skipped := 0, 0
	for _, t := range tests {
		if t.run == nil {
			skipped++
			fmt.Printf("skip  %s\n", t.name)
		} else if msg := _runTest(t.run); msg != "" {
			failed++
			fmt.Printf("FAIL  %s\n      %s\n", t.name, msg)
		} else {
			fmt.Printf("ok    %s\n", t.name)
		}
	}
	if skipped > 0 {
		fmt.Printf("%d passed, %d failed, %d skipped\n", len(tests)-failed-skipped, failed, skipped)
	} else {
		fmt.Printf("%d passed, %d failed\n", len(tests)-failed, failed)
	}
	if failed > 0 {
		os.Exit(1)
	}
}

func _runTest(run func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	run()
	return ""
}
`

const scopeRuntime = `package main

// _Scope is a bork scope: finalizers that run, last registered first,
// when the scope closes.
type _Scope struct {
	finalizers []func()
}

// Defer registers f to run when the scope closes. Resources opened in
// unsafe go code register their finalizers with it.
func (s *_Scope) Defer(f func()) { s.finalizers = append(s.finalizers, f) }

// close runs the finalizers. A scope is closed when its block ends, and
// again (doing nothing, unless a finalizer panicked) by a deferred call
// that makes panics close it too.
func (s *_Scope) close() {
	for len(s.finalizers) > 0 {
		f := s.finalizers[len(s.finalizers)-1]
		s.finalizers = s.finalizers[:len(s.finalizers)-1]
		f()
	}
}

func (s *_Scope) String() string { return "<scope>" }
`

const isRuntime = `package main

// _is reports whether x holds a value of type T.
func _is[T any](x any) bool {
	_, ok := x.(T)
	return ok
}
`

const convertRuntime = `package main

import "math"

type _integer interface {
	~int8 | ~int16 | ~int32 | ~int64 | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

type _float interface{ ~float32 | ~float64 }

// _convInt converts between integer types: the value, or OutOfRange.
func _convInt[T, F _integer](x F, target string) any {
	y := T(x)
	if F(y) != x || (y < 0) != (x < 0) {
		return OutOfRange{value: _show(x), target: target}
	}
	return y
}

// _convFloat converts a float to an integer type, dropping the
// fraction: the value, or OutOfRange (also for NaN and infinities).
func _convFloat[T _integer, F _float](x F, target string) any {
	f := math.Trunc(float64(x))
	bits := 0
	for v := T(1); v != 0; v <<= 1 {
		bits++
	}
	lo, hi := 0.0, math.Ldexp(1, bits)
	if T(0)-1 < 0 {
		lo, hi = -math.Ldexp(1, bits-1), math.Ldexp(1, bits-1)
	}
	if !(f >= lo && f < hi) {
		return OutOfRange{value: _show(x), target: target}
	}
	return T(f)
}
`

const showRuntime = `package main

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// _show renders a field value for String methods: strings are quoted,
// everything else is printed as by _str.
func _show(x any) string {
	if s, ok := x.(string); ok {
		return strconv.Quote(s)
	}
	return _str(x)
}

// _str renders a value as println and toString show it. Floats always
// look like floats (3.0, not 3), and use an exponent only when very
// large or small.
func _str(x any) string {
	switch x := x.(type) {
	case float64:
		return _fmtFloat(x, 64)
	case float32:
		return _fmtFloat(float64(x), 32)
	case fmt.Stringer, string:
		return fmt.Sprint(x)
	}
	switch v := reflect.ValueOf(x); v.Kind() {
	case reflect.Slice:
		parts := make([]string, v.Len())
		for i := range parts {
			parts[i] = _show(v.Index(i).Interface())
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case reflect.Func:
		return "<function>"
	}
	return fmt.Sprint(x)
}

func _fmtFloat(f float64, bits int) string {
	if a := math.Abs(f); math.IsInf(f, 0) || math.IsNaN(f) || (a != 0 && (a < 1e-6 || a >= 1e21)) {
		return strconv.FormatFloat(f, 'g', -1, bits)
	}
	s := strconv.FormatFloat(f, 'f', -1, bits)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}
`

// runtimeDecls parses the runtime support the program needs. The
// declarations keep their positions in the returned file set, so their
// comments print correctly.
func (g *gen) runtimeDecls() ([]ast.Decl, *token.FileSet, error) {
	var src []string
	if g.usesAssert {
		g.usesShow = true
		src = append(src, assertRuntime)
	}
	if g.usesTests {
		src = append(src, testRuntime)
	}
	if g.usesRules {
		src = append(src, rulesRuntime)
	}
	if g.usesScopes {
		src = append(src, scopeRuntime)
	}
	if g.usesIs {
		src = append(src, isRuntime)
	}
	if g.usesConvert {
		g.usesShow = true
		src = append(src, convertRuntime)
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
