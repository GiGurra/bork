// Package gen lowers a checked bork package to Go source.
//
// The output is a lowering, not a one-to-one translation: bork is
// expression-oriented, so `if` and blocks that produce values become Go
// statements writing to temporaries. Go code is built with go/ast and
// printed with go/printer, so it is always syntactically valid.
package gen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/printer"
	goscanner "go/scanner"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/syntax"
)

// Package generates a Go `package main` source file.
func Package(files []*syntax.File, info *check.Info) ([]byte, error) {
	// Every function of the program's packages is emitted (unsafe go
	// code may call them); the prelude's only if used.
	var roots []*check.Func
	for _, f := range files {
		for _, fd := range f.Funcs {
			if fn := info.FuncOf[fd]; fn != nil && !fn.Prelude {
				roots = append(roots, fn)
			}
		}
	}
	return generate(newGen(info), files, roots, nil)
}

func newGen(info *check.Info) *gen {
	return &gen{info: info, imports: map[string]bool{}, usedTypes: map[check.Type]bool{}}
}

// EvalProgram generates a program that runs the given predicate calls
// on constants and prints each result (true or false) on its own line.
// The compiler uses it to evaluate predicates at compile time.
func EvalProgram(files []*syntax.File, info *check.Info, queries []check.Query) ([]byte, error) {
	g := newGen(info)
	var roots []*check.Func
	body := &ast.BlockStmt{}
	for _, q := range queries {
		var setup []ast.Stmt
		x := g.query(q, &roots, &setup)
		body.List = append(body.List, &ast.BlockStmt{List: append(setup, &ast.ExprStmt{X: &ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: ast.NewIdent("fmt"), Sel: ast.NewIdent("Println")},
			Args: []ast.Expr{x},
		}})})
	}
	main := &ast.FuncDecl{Name: ast.NewIdent("main"), Type: &ast.FuncType{Params: &ast.FieldList{}}, Body: body}
	return generate(g, files, roots, main)
}

// query is the Go expression for a query, adding the predicates it
// calls to roots, and any statements it needs first to setup.
func (g *gen) query(q check.Query, roots *[]*check.Func, setup *[]ast.Stmt) ast.Expr {
	join := func(parts []check.Query, op token.Token) ast.Expr {
		var x ast.Expr
		for _, p := range parts {
			px := g.query(p, roots, setup)
			if x == nil {
				x = px
			} else {
				x = &ast.BinaryExpr{X: x, Op: op, Y: px}
			}
		}
		return &ast.ParenExpr{X: x}
	}
	switch {
	case q.Or != nil:
		return join(q.Or, token.LOR)
	case q.And != nil:
		return join(q.And, token.LAND)
	}
	*roots = append(*roots, q.Pred)
	var args []ast.Expr
	if q.Subject != nil {
		stmts, x := g.value(q.Subject)
		*setup = append(*setup, stmts...)
		args = append(args, g.convert(x, q.Subject.Type(), q.Params[0]))
	}
	for _, v := range q.Args {
		args = append(args, g.constant(v, q.Params[len(args)]))
	}
	fun := ast.Expr(g.funcName(q.Pred))
	if len(q.TypeArgs) > 0 {
		idx := &ast.IndexListExpr{X: fun}
		for _, t := range q.TypeArgs {
			idx.Indices = append(idx.Indices, g.goType(t))
		}
		fun = idx
	}
	return &ast.CallExpr{Fun: fun, Args: args}
}

// constant is the Go expression for a constant of bork type t.
func (g *gen) constant(v constant.Value, t check.Type) ast.Expr {
	switch v.Kind() {
	case constant.String:
		return &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(constant.StringVal(v))}
	case constant.Bool:
		return ast.NewIdent(strconv.FormatBool(constant.BoolVal(v)))
	}
	return g.typed(constLit(v, t), t)
}

// generate generates the functions reachable from roots, and the types
// and runtime they need. With a main, the program's own main is
// replaced.
func generate(g *gen, files []*syntax.File, roots []*check.Func, main *ast.FuncDecl) ([]byte, error) {
	info := g.info
	if main != nil {
		g.imports["fmt"] = true
	}
	// Instances are used through their dictionaries, so all are emitted.
	for _, ci := range info.ClassInstances {
		roots = append(roots, ci.Methods...)
	}
	emit := g.reachable(roots)
	if main != nil {
		emit[info.Funcs["main"]] = false
	}
	var funcs []ast.Decl
	var goFuncs []string
	usesOptionHelpers := false
	for _, f := range files {
		for _, fd := range f.Funcs {
			if fn := info.FuncOf[fd]; fn == nil || !emit[fn] {
				continue
			}
			if fd.GoBody != nil {
				fn := info.FuncOf[fd]
				if strings.Contains(fd.GoBody.Body, "_borkBytes") {
					g.usesBytes = true
				}
				if strings.Contains(fd.GoBody.Body, "_borkMap") {
					g.usesMap = true
				}
				if strings.Contains(fd.GoBody.Body, "_borkScope") {
					g.usesScopes = true
				}
				if strings.Contains(fd.GoBody.Body, "_borkSome") || strings.Contains(fd.GoBody.Body, "_borkNone") || strings.Contains(fd.GoBody.Body, "_borkOptionGet") {
					usesOptionHelpers = true
				}
				goName := g.funcName(fn).Name
				if g.testMode && (len(fn.ResultConstraints) > 0 || g.hasInvariants(fn.Result, map[check.Type]bool{})) {
					// Check what the Go code promises.
					funcs = append(funcs, g.checkedWrapper(fn))
					goName = "_unchecked_" + goName
				}
				text, err := g.goFunc(fd, goName)
				if err != nil {
					return nil, err
				}
				goFuncs = append(goFuncs, text)
				continue
			}
			funcs = append(funcs, g.funcDecl(fd))
		}
	}
	for _, ci := range info.ClassInstances {
		for _, m := range ci.Methods {
			if m.Derived != nil {
				goFuncs = append(goFuncs, g.derivedFunc(m))
			}
		}
		funcs = append(funcs, g.instanceDecl(ci))
	}
	funcs = append(funcs, g.extraFuncs...)
	if main != nil {
		funcs = append(funcs, main)
	}
	if usesOptionHelpers {
		for _, t := range info.TypeOrder {
			if st, ok := t.(*check.Sealed); ok && st.Prelude && st.Name == "Option" {
				g.usedTypes[st] = true
			}
		}
		goFuncs = append(goFuncs, optionHelpers)
	}
	// Types come last, once it is known which prelude types are used.
	decls := append(g.typeDecls(), funcs...)
	for i := len(info.Classes) - 1; i >= 0; i-- {
		decls = append([]ast.Decl{g.classDecl(info.Classes[i])}, decls...)
	}
	runtime, runtimeFset, err := g.runtimeDecls()
	if err != nil {
		return nil, err
	}
	if len(g.imports) > 0 {
		imp := &ast.GenDecl{Tok: token.IMPORT, Lparen: 1}
		for _, path := range sortedKeys(g.imports) {
			imp.Specs = append(imp.Specs, &ast.ImportSpec{Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(path)}})
		}
		decls = append([]ast.Decl{imp}, decls...)
	}
	var buf bytes.Buffer
	buf.WriteString("// Code generated by bork. DO NOT EDIT.\n\npackage main\n")
	printDecls := func(fset *token.FileSet, ds []ast.Decl) error {
		for _, d := range ds {
			buf.WriteString("\n")
			if err := printer.Fprint(&buf, fset, d); err != nil {
				return fmt.Errorf("printing generated Go: %w", err)
			}
			buf.WriteString("\n")
		}
		return nil
	}
	if err := printDecls(token.NewFileSet(), decls); err != nil {
		return nil, err
	}
	if len(runtime) > 0 {
		buf.WriteString("\n// bork runtime\n")
		if err := printDecls(runtimeFset, runtime); err != nil {
			return nil, err
		}
	}
	// Functions implemented in Go come last: each starts with a line
	// directive, so the Go compiler reports errors in them at their bork
	// positions.
	if len(goFuncs) > 0 {
		buf.WriteString("\n// unsafe go functions\n")
		for _, text := range goFuncs {
			buf.WriteString("\n" + text)
		}
	}
	out, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("generated Go is invalid (compiler bug): %w\n%s", err, buf.String())
	}
	return out, nil
}

type gen struct {
	info     *check.Info
	tmp      int
	fnResult check.Type // result type of the function being generated
	imports  map[string]bool
	// usedTypes holds the declared types the generated code refers to.
	usedTypes map[check.Type]bool
	// Runtime support the program needs.
	usesShow         bool
	usesConvert      bool
	usesIs           bool
	usesAssert       bool
	usesTests        bool
	usesSnaps        bool
	usesRules        bool
	usesScopes       bool
	usesDerive       bool
	usesEqual        bool
	usesHash         bool
	usesUnit         bool
	usesMap          bool
	usesDecodeSchema bool
	usesBytes        bool
	// openScopes lists the Go variables of the scope blocks around the
	// code being generated, which are closed before returning.
	openScopes []*ast.Ident
	// testMode generates checks of trusted facts (see Tests), and
	// extraFuncs holds functions to emit besides the reachable ones.
	testMode   bool
	extraFuncs []ast.Decl
}

// reachable lists the functions to emit: the roots, and the functions
// they (transitively) call.
func (g *gen) reachable(roots []*check.Func) map[*check.Func]bool {
	emit := map[*check.Func]bool{}
	var visit func(fn *check.Func)
	visit = func(fn *check.Func) {
		if emit[fn] {
			return
		}
		emit[fn] = true
		for _, callee := range fn.Calls {
			visit(callee)
		}
		if fn.Derived != nil && fn.Of.Class.Name == "Decode" {
			// A derived decoder checks the fields' where clauses.
			for _, pred := range invariantPreds(fn.Of.Type, map[check.Type]bool{}) {
				visit(pred)
			}
		}
		if g.testMode && fn.Decl.GoBody != nil {
			// Test mode checks what it promises, and the records it
			// returns (see checkedWrapper).
			for _, mc := range fn.ResultConstraints {
				for _, con := range mc.Constraints {
					for _, pred := range constraintPreds(con) {
						visit(pred)
					}
				}
			}
			for _, pred := range invariantPreds(fn.Result, map[check.Type]bool{}) {
				visit(pred)
			}
		}
	}
	for _, fn := range roots {
		visit(fn)
	}
	return emit
}

// goFunc generates a function implemented with `unsafe go { ... }`: the
// signature is generated, and the body is the Go code as written.
func (g *gen) goFunc(fd *syntax.FuncDecl, goName string) (string, error) {
	for _, p := range fd.Params {
		if goReserved[p.Name] {
			return "", fmt.Errorf("%s: parameter %s of unsafe go function %s is a reserved name in Go; rename it", p.Pos, p.Name, fd.Name)
		}
	}
	for _, path := range fd.GoBody.Imports {
		g.imports[path] = true
	}
	var buf bytes.Buffer
	sig := g.signature(fd)
	sig.Name = ast.NewIdent(goName)
	if err := printer.Fprint(&buf, token.NewFileSet(), sig); err != nil {
		return "", err
	}
	pos := fd.GoBody.Pos
	file := pos.File
	if abs, err := filepath.Abs(file); err == nil {
		file = abs
	}
	fmt.Fprintf(&buf, " {%s/*line %s:%d:%d*/%s}\n", g.packageAliases(fd), file, pos.Line, pos.Col+1, fd.GoBody.Body)
	return buf.String(), nil
}

// packageAliases lets the Go code of a function in an imported package
// use the package's own types and functions by their bork names, which
// are prefixed in Go: `type Amount = _money_Amount;`.
func (g *gen) packageAliases(fd *syntax.FuncDecl) string {
	fn := g.info.FuncOf[fd]
	pkg := fn.Pkg
	if pkg == nil || pkg.GoPrefix == "" {
		return ""
	}
	var out strings.Builder
	seen := map[string]bool{}
	var sc goscanner.Scanner
	src := []byte(fd.GoBody.Body)
	sc.Init(token.NewFileSet().AddFile("", -1, len(src)), src, nil, 0)
	for {
		_, tok, lit := sc.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.IDENT || seen[lit] {
			continue
		}
		seen[lit] = true
		if t := pkg.TypeNamed(lit); t != nil && len(check.TypeArgs(t)) == 0 {
			g.goType(t)
			fmt.Fprintf(&out, "type %s = %s%s; ", lit, pkg.GoPrefix, lit)
		} else if t, v, ok := strings.Cut(lit, "_"); ok && isVariantOf(pkg.TypeNamed(t), v) {
			g.goType(pkg.TypeNamed(t))
			fmt.Fprintf(&out, "type %s = %s%s; ", lit, pkg.GoPrefix, lit)
		} else if f := pkg.Funcs[lit]; f != nil && f != fn && len(f.TypeParams) == 0 {
			fmt.Fprintf(&out, "%s := %s%s; _ = %s; ", lit, pkg.GoPrefix, lit, lit)
		}
	}
	return out.String()
}

// isVariantOf reports whether t is a non-generic sealed type with a
// variant named v.
func isVariantOf(t check.Type, v string) bool {
	st, ok := t.(*check.Sealed)
	if !ok || len(check.TypeArgs(st)) > 0 {
		return false
	}
	for _, x := range st.Variants {
		if x.Name == v {
			return true
		}
	}
	return false
}

// goReserved holds names a bork identifier may not use verbatim in Go:
// Go keywords, predeclared identifiers, and imported package names.
var goReserved = map[string]bool{}

func init() {
	for _, s := range []string{
		"break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough",
		"for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range",
		"return", "select", "struct", "switch", "type", "var",
		"any", "append", "bool", "byte", "cap", "clear", "close", "comparable", "complex",
		"complex64", "complex128", "copy", "delete", "error", "false", "float32", "float64",
		"imag", "int", "int8", "int16", "int32", "int64", "iota", "len", "make", "max", "min",
		"new", "nil", "panic", "print", "println", "real", "recover", "rune", "string", "true",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"fmt", "strconv", "init", "String",
	} {
		goReserved[s] = true
	}
}

// name maps a bork identifier to a Go identifier. bork identifiers
// cannot start with '_', so temporaries ('_tN') never collide.
func name(s string) *ast.Ident {
	if goReserved[s] {
		return ast.NewIdent(s + "_")
	}
	return ast.NewIdent(s)
}

// funcName is the Go name of a function. A prelude function the package
// replaced (but the prelude still uses) gets a name of its own.
func (g *gen) funcName(fn *check.Func) *ast.Ident {
	if fn.Decl != nil && fn.Decl.IsMethod {
		// _m_List_first, or _pm_List_first for the prelude's.
		prefix := "_m_"
		if fn.Prelude {
			prefix = "_pm_"
		} else if fn.Pkg != nil {
			prefix = fn.Pkg.GoPrefix + "_m_"
		}
		return ast.NewIdent(prefix + g.methodTag(fn.Params[0]) + "_" + fn.Decl.Name)
	}
	if fn.Of != nil {
		return ast.NewIdent(instName(fn.Of) + "_" + fn.Decl.Name)
	}
	if fn.Prelude && g.info.Funcs[fn.Decl.Name] != fn {
		return ast.NewIdent("_prelude_" + fn.Decl.Name)
	}
	if fn.Pkg != nil && fn.Pkg.GoPrefix != "" {
		return ast.NewIdent(fn.Pkg.GoPrefix + fn.Decl.Name)
	}
	return name(fn.Decl.Name)
}

// methodTag names a receiver type in a method's Go name.
func (g *gen) methodTag(t check.Type) string {
	switch t := t.(type) {
	case *check.List:
		return "List"
	case *check.Map:
		return "Map"
	case *check.Record:
		return typeName(t.Name, t.Pkg).Name
	case *check.Sealed:
		return typeName(t.Name, t.Pkg).Name
	case *check.Resource:
		return typeName(t.Name, t.Pkg).Name
	}
	return t.String()
}

func (g *gen) newTmp() *ast.Ident {
	g.tmp++
	return ast.NewIdent("_t" + strconv.Itoa(g.tmp))
}

// signature generates a function declaration without a body.
func (g *gen) signature(fd *syntax.FuncDecl) *ast.FuncDecl {
	fn := g.info.FuncOf[fd]
	// The instances its type parameters' bounds need come first.
	ftype := &ast.FuncType{Params: &ast.FieldList{List: g.dictParams(fn.TypeParams)}}
	for i, p := range fd.Params {
		ftype.Params.List = append(ftype.Params.List, &ast.Field{
			Names: []*ast.Ident{name(p.Name)},
			Type:  g.goType(fn.Params[i]),
		})
	}
	if fn.Result != check.Unit && fn.Result != check.Never {
		ftype.Results = &ast.FieldList{List: []*ast.Field{{Type: g.goType(fn.Result)}}}
	}
	if len(fn.TypeParams) > 0 {
		tps := &ast.Field{Type: ast.NewIdent("any")}
		for _, tp := range fn.TypeParams {
			tps.Names = append(tps.Names, name(tp.Name))
		}
		ftype.TypeParams = &ast.FieldList{List: []*ast.Field{tps}}
	}
	goName := g.funcName(fn)
	if fd.Name == "main" {
		goName = ast.NewIdent("main")
	}
	return &ast.FuncDecl{Name: goName, Type: ftype}
}

func (g *gen) funcDecl(fd *syntax.FuncDecl) *ast.FuncDecl {
	fn := g.info.FuncOf[fd]
	g.tmp = 0
	g.fnResult = fn.Result
	decl := g.signature(fd)
	if fn.Result == check.Unit {
		decl.Body = &ast.BlockStmt{List: g.blockInto(fn.Body, sink{})}
	} else {
		decl.Body = &ast.BlockStmt{List: g.blockInto(fn.Body, sink{ret: true})}
	}
	return decl
}

// value lowers an expression whose result is needed. It returns the
// statements to run first and the Go expression holding the result.
// The expression is nil when e never produces a value (type Never).
func (g *gen) value(e check.Expr) ([]ast.Stmt, ast.Expr) {
	t := e.Type()
	switch e := e.(type) {
	case *check.Const:
		switch e.Value.Kind() {
		case constant.String:
			return nil, &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(constant.StringVal(e.Value))}
		case constant.Bool:
			return nil, ast.NewIdent(strconv.FormatBool(constant.BoolVal(e.Value)))
		}
		return nil, constLit(e.Value, t)
	case *check.Interp:
		return g.interp(e)
	case *check.FuncRef:
		if inst := e.Inst; inst.Func.Class != nil || len(inst.Dicts) > 0 {
			return nil, g.funcRef(inst)
		}
		return nil, g.instance(e.Inst)
	case *check.VarRef:
		return nil, name(e.Var.Name)
	case *check.Lambda:
		return nil, g.lambda(e)
	case *check.ListLit:
		return g.listLit(e)
	case *check.MapLit:
		return g.mapLit(e)
	case *check.Unary:
		stmts, x := g.value(e.X)
		if x == nil {
			return stmts, nil
		}
		op := token.SUB
		if e.Op == syntax.Not {
			op = token.NOT
		}
		return stmts, &ast.UnaryExpr{Op: op, X: paren(x)}
	case *check.Binary:
		return g.binary(e)
	case *check.Call, *check.CallBuiltin, *check.CallValue:
		stmts, call := g.call(e)
		if call == nil {
			return stmts, nil
		}
		if t == check.Never {
			stmts = append(stmts, &ast.ExprStmt{X: call})
			if b, ok := e.(*check.CallBuiltin); !ok || b.Builtin != check.BuiltinPanic {
				// Go does not know that the function never returns.
				stmts = append(stmts, unreachable()...)
			}
			return stmts, nil
		}
		return stmts, call
	case *check.If, *check.Match:
		if t == check.Never || t == check.Unit {
			return g.effect(e), nil
		}
		res := g.newTmp()
		stmts := []ast.Stmt{varDecl(res, g.goType(t))}
		return append(stmts, g.into(e, sink{res: res, resType: t})...), res
	case *check.Block:
		if len(e.Stmts) == 0 && e.Tail != nil {
			return g.value(e.Tail)
		}
		if t == check.Never || t == check.Unit {
			return g.effect(e), nil
		}
		res := g.newTmp()
		return []ast.Stmt{varDecl(res, g.goType(t)), &ast.BlockStmt{List: g.blockInto(e, sink{res: res, resType: t})}}, res
	case *check.ScopeBlock:
		if t == check.Never || t == check.Unit {
			return g.effect(e), nil
		}
		res := g.newTmp()
		return append([]ast.Stmt{varDecl(res, g.goType(t))}, g.scopeInto(e, sink{res: res, resType: t})...), res
	case *check.Return:
		return g.returnStmt(e), nil
	case *check.Select:
		return g.selector(e)
	case *check.VariantValue:
		return nil, &ast.CompositeLit{Type: g.variantType(e.Variant)}
	case *check.RecordLit:
		return g.recordLit(e)
	case *check.Copy:
		return g.copyExpr(e)
	case *check.Try:
		return g.try(e)
	}
	panic(fmt.Sprintf("unhandled expression %T", e))
}

// values lowers several expressions that are evaluated left to right,
// as in a call's arguments. If a later expression needs statements to
// run first, earlier results are saved in temporaries so evaluation
// order is preserved. Returns nil expressions if any one never
// produces a value.
func (g *gen) values(es []check.Expr) ([]ast.Stmt, []ast.Expr) {
	var out []ast.Stmt
	xs := []ast.Expr{}
	for _, e := range es {
		stmts, x := g.value(e)
		if len(stmts) > 0 {
			for j := range xs {
				if !stable(xs[j]) {
					t := g.newTmp()
					out = append(out, define(t, xs[j]))
					xs[j] = t
				}
			}
			out = append(out, stmts...)
		}
		if x == nil {
			return out, nil
		}
		xs = append(xs, x)
	}
	return out, xs
}

// stable reports whether evaluating x later gives the same result:
// literals and identifiers (bork bindings are immutable).
func stable(x ast.Expr) bool {
	switch x.(type) {
	case *ast.BasicLit, *ast.Ident:
		return true
	}
	return isConst(x)
}

// constLit is the Go literal for a constant of numeric type t.
func constLit(v constant.Value, t check.Type) ast.Expr {
	neg := constant.Sign(v) < 0
	if neg {
		v = constant.UnaryOp(token.SUB, v, 0)
	}
	lit := &ast.BasicLit{Kind: token.INT, Value: v.ExactString()}
	if check.IsFloat(t) {
		var text string
		if t == check.Float32 {
			f, _ := constant.Float32Val(v)
			text = strconv.FormatFloat(float64(f), 'g', -1, 32)
		} else {
			f, _ := constant.Float64Val(v)
			text = strconv.FormatFloat(f, 'g', -1, 64)
		}
		lit = &ast.BasicLit{Kind: token.FLOAT, Value: text}
	}
	if neg {
		return &ast.UnaryExpr{Op: token.SUB, X: lit}
	}
	return lit
}

var binaryOps = map[syntax.Kind]token.Token{
	syntax.Plus: token.ADD, syntax.Minus: token.SUB, syntax.Star: token.MUL,
	syntax.Slash: token.QUO, syntax.Pct: token.REM,
	syntax.Eq: token.EQL, syntax.NotEq: token.NEQ,
	syntax.Lt: token.LSS, syntax.LtEq: token.LEQ, syntax.Gt: token.GTR, syntax.GtEq: token.GEQ,
	syntax.AndAnd: token.LAND, syntax.OrOr: token.LOR,
}

func (g *gen) binary(e *check.Binary) ([]ast.Stmt, ast.Expr) {
	if e.Op == syntax.AndAnd || e.Op == syntax.OrOr {
		sx, x := g.value(e.X)
		if x == nil {
			return sx, nil
		}
		sy, y := g.value(e.Y)
		if len(sy) == 0 && y != nil {
			return sx, &ast.BinaryExpr{X: paren(x), Op: binaryOps[e.Op], Y: paren(y)}
		}
		// The right side needs statements: run them only when the left
		// side does not already decide the result.
		res := g.newTmp()
		var cond ast.Expr = res
		if e.Op == syntax.OrOr {
			cond = &ast.UnaryExpr{Op: token.NOT, X: res}
		}
		body := sy
		if y != nil {
			body = append(body, assign(res, y))
		}
		stmts := append(sx, define(res, x), &ast.IfStmt{Cond: cond, Body: &ast.BlockStmt{List: body}})
		return stmts, res
	}
	stmts, xs := g.values([]check.Expr{e.X, e.Y})
	if xs == nil {
		return stmts, nil
	}
	if e.Op == syntax.Eq || e.Op == syntax.NotEq {
		// Both sides as the wider type, when one is a union holding the
		// other's.
		t := e.X.Type()
		if ty := e.Y.Type(); !check.Identical(t, ty) && check.Assignable(t, ty) {
			t = ty
		}
		xs[0], xs[1] = g.convert(xs[0], e.X.Type(), t), g.convert(xs[1], e.Y.Type(), t)
		if needsDeepEqual(t, map[check.Type]bool{}) {
			// Go's == does not compare slices, nor what type parameters
			// stand for.
			g.usesEqual = true
			eq := g.equalValue(xs[0], xs[1], t)
			if e.Op == syntax.NotEq {
				eq = &ast.UnaryExpr{Op: token.NOT, X: eq}
			}
			return stmts, eq
		}
	}
	return stmts, &ast.BinaryExpr{X: paren(xs[0]), Op: binaryOps[e.Op], Y: paren(xs[1])}
}

// call lowers a call: the statements to run first, and the call (nil
// if an argument never produces a value).
func (g *gen) call(e check.Expr) ([]ast.Stmt, ast.Expr) {
	switch e := e.(type) {
	case *check.CallValue:
		// A function value is evaluated before the arguments.
		stmts, xs := g.values(append([]check.Expr{e.Fun}, e.Args...))
		if xs == nil {
			return stmts, nil
		}
		ft := e.Fun.Type().(*check.FuncType)
		args := xs[1:]
		for i := range args {
			args[i] = g.convert(args[i], e.Args[i].Type(), ft.Params[i])
		}
		return stmts, &ast.CallExpr{Fun: xs[0], Args: args}
	case *check.CallBuiltin:
		stmts, xs := g.values(e.Args)
		if xs == nil {
			return stmts, nil
		}
		return stmts, g.builtinCall(e, xs)
	case *check.Call:
		stmts, xs := g.values(e.Args)
		if xs == nil {
			return stmts, nil
		}
		inst := e.Inst
		for i := range xs {
			xs[i] = g.convert(xs[i], e.Args[i].Type(), inst.Params[i])
		}
		if inst.Func.Class != nil {
			fun, dicts := g.methodFunc(inst)
			return stmts, &ast.CallExpr{Fun: fun, Args: append(dicts, xs...)}
		}
		var dicts []ast.Expr
		for _, d := range inst.Dicts {
			dicts = append(dicts, g.dict(d))
		}
		return stmts, &ast.CallExpr{Fun: g.instance(inst), Args: append(dicts, xs...)}
	}
	panic(fmt.Sprintf("unhandled call %T", e))
}

// instance is the Go expression for a function, instantiated with its
// type arguments if it is generic.
func (g *gen) instance(inst *check.Instance) ast.Expr {
	fun := g.funcName(inst.Func)
	if len(inst.TypeArgs) == 0 {
		return fun
	}
	idx := &ast.IndexListExpr{X: fun}
	for _, t := range inst.TypeArgs {
		idx.Indices = append(idx.Indices, g.goType(t))
	}
	return idx
}

// lambda lowers a lambda to a Go function literal.
func (g *gen) lambda(e *check.Lambda) ast.Expr {
	ft := e.Type().(*check.FuncType)
	names := make([]*ast.Ident, len(e.Params))
	for i, p := range e.Params {
		names[i] = name(p.Name)
	}
	saved, savedScopes := g.fnResult, g.openScopes
	g.fnResult, g.openScopes = ft.Result, nil
	defer func() { g.openScopes = savedScopes }()
	var body []ast.Stmt
	if ft.Result == check.Unit {
		body = g.effect(e.Body)
	} else {
		body = g.tailReturn(e.Body)
	}
	g.fnResult = saved
	return &ast.FuncLit{Type: g.funcType(ft, names), Body: &ast.BlockStmt{List: body}}
}

// listLit lowers a list literal to a slice literal.
func (g *gen) listLit(e *check.ListLit) ([]ast.Stmt, ast.Expr) {
	lt := e.Type().(*check.List)
	stmts, xs := g.values(e.Elems)
	if xs == nil {
		return stmts, nil
	}
	for i := range xs {
		xs[i] = g.convert(xs[i], e.Elems[i].Type(), lt.Elem)
	}
	return stmts, &ast.CompositeLit{Type: g.goType(lt), Elts: xs}
}

// mapLit builds a map literal: _mapOf([]K{keys...}, []V{values...}).
func (g *gen) mapLit(e *check.MapLit) ([]ast.Stmt, ast.Expr) {
	mt := e.Type().(*check.Map)
	all := append(append([]check.Expr{}, e.Keys...), e.Values...)
	stmts, xs := g.values(all)
	if xs == nil {
		return stmts, nil
	}
	n := len(e.Keys)
	for i := range xs {
		want := mt.Key
		if i >= n {
			want = mt.Value
		}
		xs[i] = g.convert(xs[i], all[i].Type(), want)
	}
	g.goType(mt)
	of := &ast.IndexListExpr{X: ast.NewIdent("_mapOf"), Indices: []ast.Expr{g.goType(mt.Key), g.goType(mt.Value)}}
	return stmts, &ast.CallExpr{Fun: of, Args: []ast.Expr{
		&ast.CompositeLit{Type: &ast.ArrayType{Elt: g.goType(mt.Key)}, Elts: xs[:n]},
		&ast.CompositeLit{Type: &ast.ArrayType{Elt: g.goType(mt.Value)}, Elts: xs[n:]},
	}}
}

// builtinCall lowers a call of a builtin, given its arguments.
func (g *gen) builtinCall(e *check.CallBuiltin, args []ast.Expr) ast.Expr {
	fmtCall := func(fn string, args ...ast.Expr) ast.Expr {
		g.imports["fmt"] = true
		return &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("fmt"), Sel: ast.NewIdent(fn)}, Args: args}
	}
	switch e.Builtin {
	case check.BuiltinPrintln:
		for i := range args {
			args[i] = g.str(args[i], e.Args[i].Type())
		}
		return fmtCall("Println", args...)
	case check.BuiltinToString:
		return g.stringOf(args[0], e.Args[0].Type())
	case check.BuiltinConvert:
		return g.conversion(e, args[0])
	case check.BuiltinPanic:
		return &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: args}
	case check.BuiltinAssert:
		g.usesAssert = true
		return &ast.CallExpr{Fun: ast.NewIdent("_assert"), Args: []ast.Expr{args[0], at(e.Pos())}}
	case check.BuiltinAssertEqual:
		g.usesAssert = true
		t := e.Args[0].Type()
		actual := g.typed(args[0], t)
		expected := g.convert(args[1], e.Args[1].Type(), t)
		if check.IsNumeric(t) && isConst(expected) {
			expected = &ast.CallExpr{Fun: g.goType(t), Args: []ast.Expr{expected}}
		}
		return &ast.CallExpr{Fun: &ast.IndexExpr{X: ast.NewIdent("_assertEqual"), Index: g.goType(t)}, Args: []ast.Expr{actual, expected, at(e.Pos())}}
	case check.BuiltinAssertSnapshot:
		if !g.testMode {
			msg := e.Pos().String() + ": assertSnapshot works only in tests (bork test)"
			return &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{strLit(msg)}}
		}
		g.usesSnaps = true
		text := g.stringOf(args[0], e.Args[0].Type())
		return &ast.CallExpr{Fun: ast.NewIdent("_assertSnapshot"), Args: []ast.Expr{text, at(e.Pos())}}
	}
	panic(fmt.Sprintf("unhandled builtin %s", e.Name))
}

// effect lowers an expression evaluated only for its effect.
func (g *gen) effect(e check.Expr) []ast.Stmt {
	switch e := e.(type) {
	case *check.Call, *check.CallBuiltin, *check.CallValue:
		if e.Type() == check.Never {
			stmts, _ := g.value(e)
			return stmts
		}
		stmts, call := g.call(e)
		if call == nil {
			return stmts
		}
		return append(stmts, &ast.ExprStmt{X: call})
	case *check.If:
		return g.ifChain(e, sink{})
	case *check.Match:
		return g.matchStmt(e, sink{})
	case *check.Block:
		return []ast.Stmt{&ast.BlockStmt{List: g.blockInto(e, sink{})}}
	case *check.ScopeBlock:
		return g.scopeInto(e, sink{})
	case *check.Return:
		return g.returnStmt(e)
	}
	stmts, x := g.value(e)
	if x != nil {
		stmts = append(stmts, assign(ast.NewIdent("_"), x))
	}
	return stmts
}

// sink says what to do with the value of a branch (an if-branch, a
// match arm, a block's tail): assign it to res, return it from the
// function, or (neither) evaluate it only for its effect.
type sink struct {
	res     *ast.Ident
	resType check.Type
	ret     bool
}

// into lowers e into the context k.
func (g *gen) into(e check.Expr, k sink) []ast.Stmt {
	switch {
	case k.ret:
		return g.tailReturn(e)
	case k.res == nil:
		return g.effect(e)
	}
	switch e := e.(type) {
	case *check.Block:
		return g.blockInto(e, k)
	case *check.ScopeBlock:
		return g.scopeInto(e, k)
	case *check.If:
		return g.ifChain(e, k)
	case *check.Match:
		return g.matchStmt(e, k)
	}
	if e.Type() == check.Unit {
		// Unit into a union holding it.
		return append(g.effect(e), assign(k.res, g.unitValue()))
	}
	stmts, x := g.value(e)
	if x != nil {
		stmts = append(stmts, assign(k.res, g.convert(x, e.Type(), k.resType)))
	}
	return stmts
}

// unitValue is the Go value of Unit in a union.
func (g *gen) unitValue() ast.Expr {
	return &ast.CompositeLit{Type: g.goType(check.Unit)}
}

// blockInto lowers a block: its statements, then its tail into k.
func (g *gen) blockInto(b *check.Block, k sink) []ast.Stmt {
	out := g.stmts(b.Stmts)
	if g.diverges(b.Stmts) {
		return out
	}
	if b.Tail == nil {
		// A block without a value, where a union holding Unit is wanted.
		switch {
		case k.ret && g.fnResult != check.Unit && g.fnResult != check.Never:
			out = append(out, g.returning(g.unitValue())...)
		case k.res != nil:
			out = append(out, assign(k.res, g.unitValue()))
		}
		return out
	}
	return append(out, g.into(b.Tail, k)...)
}

// stmts lowers a block's statements (not its tail).
func (g *gen) stmts(list []check.Stmt) []ast.Stmt {
	var out []ast.Stmt
	for _, s := range list {
		switch s := s.(type) {
		case *check.Let:
			stmts, x := g.value(s.Value)
			out = append(out, stmts...)
			if x == nil {
				return out
			}
			bt, vt := s.Var.Type, s.Value.Type()
			if s.Var.Name == "_" {
				out = append(out, assign(ast.NewIdent("_"), x))
				continue
			}
			if s.Declared || (check.IsNumeric(vt) && isConst(x)) {
				// A declared type is kept, and an untyped Go constant would
				// get Go's default type (int, float64).
				out = append(out, typedVar(name(s.Var.Name), g.goType(bt), g.convert(x, vt, bt)))
			} else {
				out = append(out, define(name(s.Var.Name), g.convert(x, vt, vt)))
			}
			if s.Var.Unused {
				out = append(out, assign(ast.NewIdent("_"), name(s.Var.Name)))
			}
		case *check.ExprStmt:
			out = append(out, g.effect(s.X)...)
		case *check.Trust:
			if g.testMode {
				out = append(out, g.trustCheck(s)...)
			}
		}
	}
	return out
}

// tailReturn lowers an expression in return position. A tail `if`,
// `match`, or block becomes Go control flow that returns from each
// branch, which keeps the generated code close to hand-written Go.
func (g *gen) tailReturn(e check.Expr) []ast.Stmt {
	ret := sink{ret: true}
	switch e := e.(type) {
	case *check.If:
		if e.Else != nil {
			return g.ifChain(e, ret)
		}
	case *check.Match:
		return g.matchStmt(e, ret)
	case *check.Block:
		if len(e.Stmts) == 0 {
			if e.Tail == nil {
				return nil
			}
			return g.tailReturn(e.Tail)
		}
		return []ast.Stmt{&ast.BlockStmt{List: g.blockInto(e, ret)}}
	case *check.ScopeBlock:
		return g.scopeInto(e, ret)
	case *check.Return:
		return g.returnStmt(e)
	}
	if e.Type() == check.Unit && g.fnResult != check.Unit {
		return append(g.effect(e), g.returning(g.unitValue())...)
	}
	stmts, x := g.value(e)
	if x == nil {
		return stmts
	}
	return append(stmts, g.returning(g.convert(x, e.Type(), g.fnResult))...)
}

// scopeInto lowers `scope s { ... }`, its value into k. The scope is
// closed when the block ends, before any return from inside it, and
// (by a deferred close) when it panics.
func (g *gen) scopeInto(e *check.ScopeBlock, k sink) []ast.Stmt {
	g.usesScopes = true
	s := name(e.Var.Name)
	closeCall := &ast.CallExpr{Fun: &ast.SelectorExpr{X: s, Sel: ast.NewIdent("close")}}
	// A scope inside another one in the function is cancelled with it.
	var parent ast.Expr = ast.NewIdent("nil")
	if n := len(g.openScopes); n > 0 {
		parent = g.openScopes[n-1]
	}
	// The cleanup policy is computed before the scope opens.
	var stmts, policy []ast.Stmt
	for _, p := range e.Policies {
		pstmts, px := g.value(p)
		stmts = append(stmts, pstmts...)
		if px != nil {
			fn := g.info.Funcs["setScopePolicy"]
			policy = append(policy, &ast.ExprStmt{X: &ast.CallExpr{Fun: g.funcName(fn), Args: []ast.Expr{s, px}}})
		}
	}
	stmts = append(stmts,
		define(s, &ast.CallExpr{Fun: ast.NewIdent("_newScope"), Args: []ast.Expr{parent, &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(e.Var.Name)}}}),
		// A panic in the block closes the scope too.
		&ast.DeferStmt{Call: &ast.CallExpr{Fun: &ast.SelectorExpr{X: s, Sel: ast.NewIdent("abort")}}},
	)
	stmts = append(stmts, policy...)
	g.openScopes = append(g.openScopes, s)
	stmts = append(stmts, g.blockInto(e.Body, k)...)
	g.openScopes = g.openScopes[:len(g.openScopes)-1]
	if !k.ret && e.Body.Type() != check.Never {
		stmts = append(stmts, &ast.ExprStmt{X: closeCall})
	}
	return []ast.Stmt{&ast.BlockStmt{List: stmts}}
}

// returning returns the given results (none or one) from the function,
// first closing the scopes around the return.
func (g *gen) returning(results ...ast.Expr) []ast.Stmt {
	if len(g.openScopes) == 0 {
		return []ast.Stmt{&ast.ReturnStmt{Results: results}}
	}
	var stmts []ast.Stmt
	if len(results) > 0 {
		// The result is computed while the scopes are open.
		r := g.newTmp()
		stmts = append(stmts, typedVar(r, g.goType(g.fnResult), results[0]))
		results = []ast.Expr{r}
	}
	for i := len(g.openScopes) - 1; i >= 0; i-- {
		stmts = append(stmts, &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: g.openScopes[i], Sel: ast.NewIdent("close")}}})
	}
	return append(stmts, &ast.ReturnStmt{Results: results})
}

// diverges reports whether a statement list ends by never finishing.
func (g *gen) diverges(stmts []check.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	if es, ok := stmts[len(stmts)-1].(*check.ExprStmt); ok {
		return es.X.Type() == check.Never
	}
	return false
}

// ifChain lowers an if/else-if/else chain, each branch into k. An
// `else if` whose condition needs no setup statements becomes a Go
// `else if`.
func (g *gen) ifChain(e *check.If, k sink) []ast.Stmt {
	stmts, cond := g.value(e.Cond)
	if cond == nil {
		return stmts
	}
	s := &ast.IfStmt{Cond: cond, Body: &ast.BlockStmt{List: g.blockInto(e.Then, k)}}
	switch els := e.Else.(type) {
	case *check.Block:
		s.Else = &ast.BlockStmt{List: g.blockInto(els, k)}
	case *check.If:
		rest := g.ifChain(els, k)
		if len(rest) == 1 {
			s.Else = rest[0].(*ast.IfStmt)
		} else {
			s.Else = &ast.BlockStmt{List: rest}
		}
	}
	return append(stmts, s)
}

func (g *gen) returnStmt(e *check.Return) []ast.Stmt {
	if e.Value == nil {
		return g.returning()
	}
	stmts, x := g.value(e.Value)
	if x == nil {
		return stmts
	}
	return append(stmts, g.returning(g.convert(x, e.Value.Type(), g.fnResult))...)
}

// convert adjusts a Go expression of bork type from for use where type
// to is expected. Number constants headed for a union (Go `any`) must be
// typed explicitly, or Go would store an Int as int rather than int64.
//
// Likewise, a variant literal (`Shape_Rect{...}`) has its own Go struct
// type; where the sealed type is meant (a binding, a comparison, a
// match), it is converted to the sealed type's interface.
func (g *gen) convert(x ast.Expr, from, to check.Type) ast.Expr {
	if to == nil || !check.Identical(from, to) {
		x = g.typed(x, from)
	}
	if _, ok := to.(*check.Sealed); ok {
		if _, isLit := x.(*ast.CompositeLit); isLit {
			return &ast.CallExpr{Fun: g.goType(to), Args: []ast.Expr{x}}
		}
	}
	return x
}

// paren wraps operator expressions in parentheses, so nested operators
// keep bork's grouping regardless of Go's precedence rules.
func paren(x ast.Expr) ast.Expr {
	switch x.(type) {
	case *ast.BinaryExpr, *ast.UnaryExpr:
		return &ast.ParenExpr{X: x}
	}
	return x
}

// isConst reports whether x is a Go constant expression (literals and
// operators on them), which Go would give a default type.
func isConst(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.BasicLit:
		return true
	case *ast.ParenExpr:
		return isConst(x.X)
	case *ast.UnaryExpr:
		return isConst(x.X)
	case *ast.BinaryExpr:
		return isConst(x.X) && isConst(x.Y)
	}
	return false
}

func typedVar(id *ast.Ident, typ, value ast.Expr) ast.Stmt {
	return &ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{
		&ast.ValueSpec{Names: []*ast.Ident{id}, Type: typ, Values: []ast.Expr{value}},
	}}}
}

func define(lhs *ast.Ident, rhs ast.Expr) ast.Stmt {
	return &ast.AssignStmt{Lhs: []ast.Expr{lhs}, Tok: token.DEFINE, Rhs: []ast.Expr{rhs}}
}

func assign(lhs *ast.Ident, rhs ast.Expr) ast.Stmt {
	return &ast.AssignStmt{Lhs: []ast.Expr{lhs}, Tok: token.ASSIGN, Rhs: []ast.Expr{rhs}}
}

func varDecl(id *ast.Ident, typ ast.Expr) ast.Stmt {
	return &ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{
		&ast.ValueSpec{Names: []*ast.Ident{id}, Type: typ},
	}}}
}

// typed gives a number constant x its Go type explicitly, for contexts
// where Go would otherwise pick a default type.
func (g *gen) typed(x ast.Expr, t check.Type) ast.Expr {
	if check.IsNumeric(t) && isConst(x) {
		return &ast.CallExpr{Fun: g.goType(t), Args: []ast.Expr{x}}
	}
	return x
}

// conversion lowers `toInt8(x)` and friends. A checked conversion calls
// the runtime, which produces the value or an OutOfRange.
func (g *gen) conversion(e *check.CallBuiltin, arg ast.Expr) ast.Expr {
	conv := e.Conv
	if conv == nil {
		// A constant, already checked to fit.
		return &ast.CallExpr{Fun: g.goType(e.Type()), Args: []ast.Expr{arg}}
	}
	if !conv.Checked {
		if check.Identical(conv.From, conv.To) {
			return arg
		}
		return &ast.CallExpr{Fun: g.goType(conv.To), Args: []ast.Expr{arg}}
	}
	g.usesConvert = true
	g.goType(g.info.OutOfRange)
	fn := "_convInt"
	if check.IsFloat(conv.From) {
		fn = "_convFloat"
	}
	return &ast.CallExpr{
		Fun:  &ast.IndexExpr{X: ast.NewIdent(fn), Index: g.goType(conv.To)},
		Args: []ast.Expr{arg, &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(conv.To.String())}},
	}
}

// str prepares x, of type t, for printing: floats (also inside a union)
// go through the runtime's _str, so they print in bork's format.
func (g *gen) str(x ast.Expr, t check.Type) ast.Expr {
	x = g.typed(x, t)
	if _, ok := t.(*check.List); ok {
		return g.showValue(x, t)
	}
	if needsStr(t) {
		g.usesShow = true
		return &ast.CallExpr{Fun: ast.NewIdent("_str"), Args: []ast.Expr{x}}
	}
	return x
}

// needsStr reports whether values of type t print differently in bork
// than Go prints them.
func needsStr(t check.Type) bool {
	switch t.(type) {
	case *check.Union, *check.List, *check.Map, *check.FuncType, *check.TypeParam:
		return true
	}
	return check.IsFloat(t)
}

// stringOf renders x, of type t, as toString does.
func (g *gen) stringOf(x ast.Expr, t check.Type) ast.Expr {
	switch {
	case t == check.String:
		return x
	case needsStr(t):
		return g.str(x, t)
	}
	g.imports["fmt"] = true
	return &ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: ast.NewIdent("fmt"), Sel: ast.NewIdent("Sprint")},
		Args: []ast.Expr{g.typed(x, t)},
	}
}

// interp lowers s"..." to a string concatenation.
func (g *gen) interp(e *check.Interp) ([]ast.Stmt, ast.Expr) {
	stmts, xs := g.values(e.Exprs)
	if xs == nil {
		return stmts, nil
	}
	var out ast.Expr
	add := func(x ast.Expr) {
		if out == nil {
			out = x
		} else {
			out = &ast.BinaryExpr{X: out, Op: token.ADD, Y: paren(x)}
		}
	}
	lit := func(s string) ast.Expr { return &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(s)} }
	for i, part := range e.Parts {
		if part != "" {
			add(lit(part))
		}
		if i < len(xs) {
			add(g.stringOf(xs[i], e.Exprs[i].Type()))
		}
	}
	if out == nil {
		return stmts, lit("")
	}
	return stmts, out
}
