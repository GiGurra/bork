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
	"github.com/GiGurra/bork/internal/diag"
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
	g := &gen{info: info, imports: map[string]bool{}, bindImports: map[string]string{}, usedTypes: map[check.Type]bool{}, comptimeReads: map[*check.Var]bool{}}
	for _, node := range info.Comptimes {
		for _, capture := range node.Captures {
			g.comptimeReads[capture] = true
		}
	}
	return g
}

// EvalProgram generates a program that runs the given predicate calls
// on constants and prints each result (true or false) on its own line.
// The compiler uses it to evaluate predicates at compile time.
func EvalProgram(files []*syntax.File, info *check.Info, queries []check.Query) ([]byte, error) {
	return evalProgram(files, info, queries, false)
}

// EvalComptimeProgram uses explicit-computation restrictions for proof execution.
func EvalComptimeProgram(files []*syntax.File, info *check.Info, queries []check.Query) ([]byte, error) {
	return evalProgram(files, info, queries, true)
}

// ClosedProofProgram emits only statically audited predicate selections. It
// excludes unrelated instances, package initializers and foreign types; callers
// must also certify the emitted runtime support before reusing any result.
func ClosedProofProgram(files []*syntax.File, info *check.Info, queries []check.Query) ([]byte, error) {
	audit := check.AuditExecutionQueries(info, queries)
	if audit.Decline != "" {
		return nil, fmt.Errorf("proof closure unavailable: %s", audit.Decline)
	}
	// Calls can retain dependencies from already evaluated comptime bodies.
	// Decline rather than emitting helpers outside the audited typed closure.
	allowed := map[check.ExecutionDeclaration]bool{}
	for _, declaration := range audit.Declarations {
		declaration.Signature = ""
		allowed[declaration] = true
	}
	var roots []*check.Func
	var collect func(check.Query)
	collect = func(query check.Query) {
		if query.Pred != nil {
			roots = append(roots, query.Pred)
		}
		for _, part := range query.And {
			collect(part)
		}
		for _, part := range query.Or {
			collect(part)
		}
	}
	for _, query := range queries {
		collect(query)
	}
	g := newGen(info)
	g.proofMode = true
	for fn := range g.reachable(roots) {
		pkg := ""
		if fn.Pkg != nil {
			pkg = fn.Pkg.Path
		}
		if fn.Decl == nil || info.FuncOf[fn.Decl] != fn || !allowed[check.ExecutionDeclaration{Package: pkg, Name: fn.Decl.Name, Position: fn.Decl.Pos}] {
			return nil, fmt.Errorf("proof generator dependency outside audited closure")
		}
	}
	selected := *info
	selected.ClassInstances = nil
	selected.PackageBindings = nil
	selected.Ambients = nil
	selected.Classes = nil
	for _, class := range info.Classes {
		if check.IsEq(class) {
			selected.Classes = append(selected.Classes, class)
		}
	}
	return evalProgramMode(files, &selected, queries, false, true)
}

func evalProgram(files []*syntax.File, info *check.Info, queries []check.Query, comptime bool) ([]byte, error) {
	return evalProgramMode(files, info, queries, comptime, false)
}

func evalProgramMode(files []*syntax.File, info *check.Info, queries []check.Query, comptime, proof bool) ([]byte, error) {
	g := newGen(info)
	g.evalMode = true
	g.comptimeMode = comptime
	g.proofMode = proof
	g.artifactMode = proof
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
	for i, v := range q.Values {
		stmts, x := g.value(v)
		*setup = append(*setup, stmts...)
		args = append(args, g.convert(x, v.Type(), q.Params[i]))
	}
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
	var dicts []ast.Expr
	for _, dict := range q.Dicts {
		dicts = append(dicts, g.dict(dict))
	}
	return &ast.CallExpr{Fun: fun, Args: append(dicts, args...)}
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
	for _, class := range info.Classes {
		// Dictionary declarations refer to method signatures even when no
		// instance is reachable. Register those types before emitting types.
		for _, method := range class.Methods {
			for _, param := range method.Params {
				g.goType(param)
			}
			g.goType(method.Result)
		}
		if check.IsGoStruct(class) {
			g.goType(info.Named["GoValueError"])
		}
	}
	if main != nil {
		g.imports["fmt"] = true
	}
	// Instances are used through their dictionaries, so all are emitted.
	for _, ci := range info.ClassInstances {
		roots = append(roots, ci.Methods...)
	}
	for _, binding := range info.PackageBindings {
		roots = append(roots, binding.Boundary.Calls...)
	}
	// Incoming propagated values are checked with their facts.
	roots = append(roots, g.ambientPreds()...)
	emit := g.reachable(roots)
	if main != nil {
		emit[info.Funcs["main"]] = false
	}
	var funcs []ast.Decl
	for _, binding := range info.PackageBindings {
		funcs = append(funcs, g.packageLazy(binding))
	}
	var goFuncs []string
	for _, f := range files {
		for _, fd := range f.Funcs {
			if fn := info.FuncOf[fd]; fn == nil || !emit[fn] {
				continue
			}
			if fd.GoBody != nil {
				fn := info.FuncOf[fd]
				if strings.Contains(fd.GoBody.Body, "_borkFanIn") || strings.Contains(fd.GoBody.Body, "_borkReceiveChoice") {
					g.usesFanIn = true
					g.usesScopes = true
					g.goType(info.Named["Cancelled"])
					g.goType(info.Named["Closed"])
				}
				if strings.Contains(fd.GoBody.Body, "_borkParallel") {
					g.usesParallel = true
					g.usesScopes = true
				}
				if strings.Contains(fd.GoBody.Body, "_borkIoFailure") {
					g.usesIoFailure = true
				}
				if strings.Contains(fd.GoBody.Body, "_borkBytes") {
					g.usesBytes = true
				}
				if strings.Contains(fd.GoBody.Body, "_borkLogged") || strings.Contains(fd.GoBody.Body, "_borkPropagated") || strings.Contains(fd.GoBody.Body, "_borkBindPropagated") {
					g.usesAmbients = true
				}
				if strings.Contains(fd.GoBody.Body, "_borkMap") {
					g.usesMap = true
				}
				if strings.Contains(fd.GoBody.Body, "_borkOk") || strings.Contains(fd.GoBody.Body, "_Unit") {
					g.usesOk = true
				}
				if strings.Contains(fd.GoBody.Body, "_borkScope") || strings.Contains(fd.GoBody.Body, "_borkNewResourceHandle") || strings.Contains(fd.GoBody.Body, "_borkResourceHandle") {
					g.usesScopes = true
				}
				if strings.Contains(fd.GoBody.Body, "_borkSome") || strings.Contains(fd.GoBody.Body, "_borkNone") || strings.Contains(fd.GoBody.Body, "_borkOptionGet") {
					g.usesOptionHelpers = true
				}
				goName := g.funcName(fn).Name
				if g.mockIDs[fn] != 0 {
					funcs = append(funcs, g.dispatchers(fn)...)
				}
				if g.testMode && (len(fn.ResultConstraints) > 0 || g.hasInvariants(fn.Result, map[check.Type]bool{})) {
					// Check what the Go code promises.
					wrapper := g.checkedWrapper(fn).(*ast.FuncDecl)
					wrapper.Name = ast.NewIdent(g.realName(fn, goName))
					funcs = append(funcs, wrapper)
					goName = "_unchecked_" + goName
				} else {
					goName = g.realName(fn, goName)
				}
				text, err := g.goFunc(fd, goName)
				if err != nil {
					return nil, err
				}
				goFuncs = append(goFuncs, text)
				continue
			}
			if fd.GoBind != nil {
				fn := info.FuncOf[fd]
				if g.mockIDs[fn] != 0 {
					funcs = append(funcs, g.dispatchers(fn)...)
				}
				text, err := g.bindFunc(fd, g.realName(fn, g.funcName(fn).Name))
				if err != nil {
					return nil, err
				}
				goFuncs = append(goFuncs, text)
				continue
			}
			decl := g.funcDecl(fd)
			if fn := info.FuncOf[fd]; g.mockIDs[fn] != 0 {
				funcs = append(funcs, g.dispatchers(fn)...)
				decl.Name = ast.NewIdent(g.realName(fn, decl.Name.Name))
			}
			funcs = append(funcs, decl)
		}
	}
	if g.usesBind {
		// The wrappers' runtime returns both.
		g.goType(info.Named["GoError"])
		g.goType(info.Named["GoValueError"])
	}
	for _, ci := range info.ClassInstances {
		for _, m := range ci.Methods {
			if m.Derived != nil {
				goFuncs = append(goFuncs, g.derivedFunc(m))
			}
		}
		funcs = append(funcs, g.instanceDecl(ci))
	}
	if main != nil {
		funcs = append(funcs, main)
	}
	// Types come last, once it is known which prelude types are used.
	decls := append(g.typeDecls(), funcs...)
	decls = append(decls, g.extraFuncs...)
	if !g.proofMode {
		for _, f := range files {
			for _, td := range f.Types {
				if td.GoName == nil {
					continue
				}
				for _, pkg := range info.Packages {
					if pkg.Path != f.Package {
						continue
					}
					t := pkg.TypeNamed(td.Name)
					if t == nil || check.GoTypeOf(t) == nil {
						continue
					}
					canonical := g.goType(t)
					alias := typeName(td.Name, pkg)
					if alias.Name != canonical.(*ast.Ident).Name {
						decls = append(decls, &ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{Name: alias, Assign: 1, Type: canonical}}})
					}
				}
			}
		}
	}
	if g.usesOptionHelpers {
		for _, t := range info.TypeOrder {
			if st, ok := t.(*check.Sealed); ok && st.Prelude && st.Name == "Option" {
				g.usedTypes[st] = true
			}
		}
		goFuncs = append(goFuncs, optionHelpers)
	}
	if g.usesMirror {
		goFuncs = append(goFuncs, g.mirrorRuntime())
	}
	if g.usesOpaque {
		goFuncs = append(goFuncs, opaqueRuntime)
	}
	for i := len(info.Classes) - 1; i >= 0; i-- {
		decls = append([]ast.Decl{g.classDecl(info.Classes[i])}, decls...)
	}
	if g.usesScopes && !g.testMode && !g.evalMode {
		for _, declaration := range decls {
			if entry, ok := declaration.(*ast.FuncDecl); ok && entry.Name.Name == "main" {
				entry.Name = ast.NewIdent("_borkMain")
				decls = append(decls, &ast.FuncDecl{Name: ast.NewIdent("main"), Type: &ast.FuncType{Params: &ast.FieldList{}}, Body: &ast.BlockStmt{List: []ast.Stmt{
					&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("_borkMain")}},
					&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("_borkSignalExit")}},
				}}})
				break
			}
		}
	}
	embedded := g.embedDecls()
	runtime, runtimeFset, err := g.runtimeDecls()
	if err != nil {
		return nil, err
	}
	bindPaths := map[string]bool{}
	for path := range g.bindImports {
		bindPaths[path] = true
	}
	if len(g.imports)+len(bindPaths) > 0 {
		imp := &ast.GenDecl{Tok: token.IMPORT, Lparen: 1}
		for _, path := range sortedKeys(g.imports) {
			imp.Specs = append(imp.Specs, &ast.ImportSpec{Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(path)}})
		}
		for _, path := range sortedKeys(bindPaths) {
			imp.Specs = append(imp.Specs, &ast.ImportSpec{Name: ast.NewIdent(g.bindImports[path]), Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(path)}})
		}
		decls = append([]ast.Decl{imp}, decls...)
	}
	decls, runtime, goFuncs, err = g.pruneHelpers(decls, runtime, goFuncs)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString("// Code generated by bork. DO NOT EDIT.\n\npackage main\n")
	printDecls := func(fset *token.FileSet, ds []ast.Decl) error {
		parenHeaders(ds)
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
	buf.WriteString(embedded)
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
	if g.debugSource != "" {
		return g.mapDebugSource(out)
	}
	return out, nil
}

type gen struct {
	debugSource    string
	debugFiles     map[string]bool
	debugTypes     map[check.Type]ast.Expr
	artifactMode   bool
	proofMode      bool
	candidateNames map[*check.Var]*ast.Ident
	info           *check.Info
	tmp            int
	fnResult       check.Type // result type of the function being generated
	callerAt       ast.Expr   // hidden caller location in an internal helper
	imports        map[string]bool
	// bindImports holds the Go packages bindings call, by import path,
	// with the names they are imported as.
	bindImports map[string]string
	// usedTypes holds the declared types the generated code refers to.
	usedTypes map[check.Type]bool
	// Runtime support the program needs.
	usesShow         bool
	usesConvert      bool
	usesIs           bool
	usesAssert       bool
	usesTests        bool
	usesSnaps        bool
	usesProps        bool
	usesScopes       bool
	usesParallel     bool
	usesFanIn        bool
	usesDerive       bool
	usesEqual        bool
	usesHash         bool
	usesOk           bool
	usesMap          bool
	usesLazy         bool
	usesAsync        bool
	evalMode         bool
	comptimeCaptures map[*check.Var]check.Expr
	comptimeResults  map[*check.Comptime]ast.Expr
	comptimePackages map[*check.PackageBinding]ast.Expr
	comptimeMode     bool
	// Captures disappear from emitted recipes; retain valid Go bindings for their declarations.
	comptimeReads    map[*check.Var]bool
	usesSeq          bool
	usesSeqFirst     bool
	usesSeqUnfold    bool
	usesLoopCleanup  bool
	yieldName        *ast.Ident
	loops            []loopFrame
	usesDecodeSchema bool
	usesGoStruct     bool
	usesBytes        bool
	usesIoFailure    bool
	usesOpaque       bool
	usesMirror       bool
	usesBind         bool
	usesBindContexts bool
	// usesOptionHelpers is set when Go code uses _borkSome, _borkNone,
	// or _borkOptionGet.
	usesOptionHelpers bool
	// openScopes lists the Go variables of the scope blocks around the
	// code being generated, which are closed before returning, and
	// blockOwners the owner variables bound in the blocks around it, with
	// how many scope blocks were open where each was bound.
	openScopes  []*ast.Ident
	blockOwners []blockOwner
	// testMode generates checks of trusted facts (see Tests), and
	// extraFuncs holds functions to emit besides the reachable ones.
	testMode             bool
	extraFuncs           []ast.Decl
	invariantDiagnostics map[*check.Func]bool
	// genFuncs are the functions generating values for property tests,
	// and propRoots the predicates their facts call.
	genFuncs  []genFunc
	propRoots []*check.Func
	// mockIDs numbers the functions the tests mock (see mocks.go), which
	// get dispatchers; openMocks are the mock statements whose blocks
	// are being generated, mockBodies the mocks' bodies, and mockN
	// numbers mock statements.
	mockIDs   map[*check.Func]int
	openMocks []openMock
	// outerMocks are the mock statements open around a generate's
	// producer being generated, which a mock in it passes calls on to.
	outerMocks []openMock
	mockBodies []mockBody
	mockN      int
	// genericMocks numbers the mocks of generic functions (by their
	// Func), and captures collects what the one being generated uses.
	genericMocks map[*check.Func]int
	captures     []*mockCapture
	// typeParamNames renames type parameters in Go (see
	// mangleTypeParams), and mockErrors are what generating a generic
	// mock's body found wrong.
	typeParamNames   map[*check.TypeParam]string
	typeParamGoTypes map[*check.TypeParam]ast.Expr
	tupleConversions map[string]*tupleConversion
	mockErrors       diag.List
	usesMocks        bool
	// usesAmbients is set when the program publishes or reads logged
	// or propagated ambient values (ambientRuntime). labelGuard, while
	// a Go function's body is generated, is set if a with in it
	// publishes values (see guardLabels).
	usesAmbients bool
	labelGuard   *bool
	// blocks are the blocks being generated, and scopeBodies the scope
	// blocks, so a mock can tell whether it is directly in a scope's
	// body.
	blocks      []*check.Block
	scopeBodies []scopeBody
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
		for _, callee := range goBodyFunctions(fn) {
			visit(callee)
		}
		if fn.Derived != nil && fn.Of.Class.Name == "Decode" {
			// A derived decoder checks the fields' where clauses.
			for _, pred := range invariantPreds(fn.Of.Type, map[check.Type]bool{}) {
				visit(pred)
			}
		}
		if (g.testMode && fn.Decl.GoBody != nil) || fn.Decl.GoBind != nil {
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
	// The audited proof selection excludes lazy/default helper execution and
	// foreign mirrors. Do not pull unrelated type declarations into its closure.
	if g.proofMode {
		return emit
	}
	// Mirror helpers are generated for declarations, including helpers used
	// inside unsafe Go bodies, so their field predicates must be available.
	for _, t := range g.info.TypeOrder {
		var groups [][]*check.Field
		switch t := t.(type) {
		case *check.Record:
			groups = append(groups, t.Fields)
		case *check.Sealed:
			for _, variant := range t.Variants {
				groups = append(groups, variant.Fields)
			}
		}
		for _, fields := range groups {
			for _, field := range fields {
				for _, callee := range field.DefaultCalls {
					visit(callee)
				}
			}
		}
		if r, ok := t.(*check.Record); ok && r.GoMirror != nil {
			for _, pred := range invariantPreds(r, map[check.Type]bool{}) {
				visit(pred)
			}
		}
	}
	return emit
}

// goBodyFunctions finds package helpers mentioned by unsafe Go, using the
// same bare-name rule as packageAliases. Tests and fact evaluation emit only
// reachable functions, so Bork call metadata alone is insufficient.
func goBodyFunctions(fn *check.Func) []*check.Func {
	if fn.Decl.GoBody == nil || fn.Pkg == nil {
		return nil
	}
	var out []*check.Func
	var scanner goscanner.Scanner
	src := []byte(fn.Decl.GoBody.Body)
	scanner.Init(token.NewFileSet().AddFile("", -1, len(src)), src, nil, 0)
	previous := token.ILLEGAL
	for {
		_, tok, text := scanner.Scan()
		selector := previous == token.PERIOD
		previous = tok
		if tok == token.EOF {
			break
		}
		if tok != token.IDENT || selector {
			continue
		}
		if helper := fn.Pkg.Funcs[text]; helper != nil && helper != fn && len(helper.TypeParams) == 0 {
			out = append(out, helper)
		}
	}
	return out
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
		g.goImport(path)
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
	fmt.Fprintf(&buf, " {%s/*line %s:%d:%d*/%s}\n", g.packageAliases(fd), file, pos.Line, pos.Col+1, g.goBodyAliases(fd))
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
	previous := token.ILLEGAL
	for {
		_, tok, lit := sc.Scan()
		selector := previous == token.PERIOD
		previous = tok
		if tok == token.EOF {
			break
		}
		if tok != token.IDENT || selector || seen[lit] {
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
	if s != "" && s[0] >= '0' && s[0] <= '9' {
		return ast.NewIdent("E" + s)
	}
	if goReserved[s] {
		return ast.NewIdent(s + "_")
	}
	return ast.NewIdent(s)
}

// varIdent is the Go name of a variable.
func varIdent(v *check.Var) *ast.Ident {
	if v.GoName != "" {
		return name(v.GoName)
	}
	return name(v.Name)
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
	case *check.Seq:
		return "Seq"
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
	for i, p := range fn.Decl.Params {
		paramName := name(p.Name)
		if fn.Decl.Constructor != nil {
			paramName = varIdent(fn.ParamVars[i])
		}
		ftype.Params.List = append(ftype.Params.List, &ast.Field{
			Names: []*ast.Ident{paramName},
			Type:  g.goType(fn.Params[i]),
		})
	}
	// The ambient values it needs are hidden parameters, after the
	// others.
	for _, v := range fn.NeedVars {
		ftype.Params.List = append(ftype.Params.List, &ast.Field{
			Names: []*ast.Ident{varIdent(v)},
			Type:  g.goType(v.Type),
		})
	}
	if fn.TrackCaller {
		ftype.Params.List = append(ftype.Params.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent("_callerAt")}, Type: ast.NewIdent("string")})
	}
	if fn.Result != check.Ok && fn.Result != check.Never {
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
	savedCaller := g.callerAt
	defer func() { g.callerAt = savedCaller }()
	g.callerAt = nil
	if fn.TrackCaller {
		g.callerAt = ast.NewIdent("_callerAt")
	}
	decl := g.signature(fd)
	// An owned scope given to the function is closed if it ends early.
	// A parameter declared in another closes before it: its fallback is
	// deferred after that one's.
	var drops []ast.Stmt
	deferred := map[int]bool{}
	var drop func(i int)
	drop = func(i int) {
		if deferred[i] || fn.Params[i] != check.OwnedScope {
			return
		}
		deferred[i] = true
		if t := fn.ParamIn[i]; t >= 0 {
			drop(t)
		}
		drops = append(drops, dropOwner(varIdent(fn.ParamVars[i]).Name)...)
	}
	for i := range fn.Params {
		drop(i)
	}
	k := sink{ret: fn.Result != check.Ok}
	drops = append(g.debugLine(fd.Pos), drops...)
	decl.Body = &ast.BlockStmt{List: append(drops, g.guardLabels(func() []ast.Stmt { return g.blockInto(fn.Body, k) })...)}
	return decl
}

// dropOwner defers the fallback of the owner variable v: the owned
// scope it still holds when the function ends (by a return, ?, or a
// panic) is closed then. Passing the owner on disarms it, by clearing
// v, so b.scope reads a copy that stays set (borrowedName), which
// lambdas can capture.
func dropOwner(v string) []ast.Stmt {
	drop := &ast.CallExpr{Fun: ast.NewIdent("_dropScope"), Args: []ast.Expr{name(v)}}
	return []ast.Stmt{
		define(borrowedName(v), name(v)),
		assign(ast.NewIdent("_"), borrowedName(v)),
		&ast.DeferStmt{Call: &ast.CallExpr{Fun: &ast.FuncLit{Type: &ast.FuncType{Params: &ast.FieldList{}}, Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: drop}}}}}},
	}
}

// borrowedName is the Go variable holding the scope of the owner
// variable v, for b.scope.
func borrowedName(v string) *ast.Ident {
	return ast.NewIdent("_scopeOf_" + name(v).Name)
}

// value lowers an expression whose result is needed. It returns the
// statements to run first and the Go expression holding the result.
// The expression is nil when e never produces a value, including when
// one of its subexpressions diverges. Ok has a concrete value.
func (g *gen) value(e check.Expr) ([]ast.Stmt, ast.Expr) {
	t := e.Type()
	switch e := e.(type) {
	case *check.Comptime:
		if result := g.comptimeResults[e]; result != nil {
			return nil, result
		}
		if e.Value == nil {
			if !g.evalMode {
				panic("unevaluated comptime value reached generation")
			}
			// Unrelated instance bodies may be emitted in a probe. They cannot run
			// an unresolved computation; actual recipe dependencies are prepared first.
			thunk := &ast.FuncLit{Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: g.goType(e.Type())}}}}, Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote("unresolved comptime dependency")}}}}}}}
			return nil, &ast.CallExpr{Fun: thunk}
		}
		stmts, x := g.value(e.Value)
		if x != nil {
			x = g.convert(x, e.Value.Type(), e.Type())
			if _, ok := e.Type().(*check.Union); ok {
				x = &ast.CallExpr{Fun: g.goType(e.Type()), Args: []ast.Expr{x}}
			}
		}
		return stmts, x
	case *check.FloatBits:
		g.imports["math"] = true
		name := "Float64frombits"
		if e.Type() == check.Float32 {
			name = "Float32frombits"
		}
		return nil, &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("math"), Sel: ast.NewIdent(name)}, Args: []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: strconv.FormatUint(e.Bits, 10)}}}

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
		g.genericCall(e.Inst, e.Pos())
		stmts, needs := g.values(e.Needs)
		if e.Inst.Func.TrackCaller {
			needs = append(needs, g.callerLocation(e.Pos()))
		}
		if frame := g.passthrough(e.Inst.Func); frame != nil {
			return stmts, g.nextRef(e.Inst, frame, needs)
		}
		if inst := e.Inst; inst.Func.Class != nil || len(inst.Dicts) > 0 || collapsedUnion(inst) || len(needs) > 0 || len(inst.TypeArgs) > 0 && hasTupleRepresentation(&check.FuncType{Params: inst.Params, Result: inst.Result}) {
			return stmts, g.funcRef(inst, needs...)
		}
		return nil, g.instance(e.Inst)
	case *check.VarRef:
		if result := g.comptimePackages[e.Var.PackageBinding]; result != nil {
			return nil, result
		}
		if value := g.comptimeCaptures[e.Var]; value != nil {
			return g.value(value)
		}
		g.captured(e.Var, false)
		if candidate := g.candidateNames[e.Var]; candidate != nil {
			return nil, candidate
		}
		if e.Var.Let != nil && e.Var.Let.Initializer != nil {
			return nil, &ast.CallExpr{Fun: &ast.SelectorExpr{X: varIdent(e.Var), Sel: ast.NewIdent("get")}}
		}
		if e.Type() == check.OwnedScope {
			// An owner is only used to pass it on, which disarms the
			// fallback of its variable.
			if len(g.loops) > 0 {
				root := g.loops[len(g.loops)-1].cleanup
				root.used = true
				return nil, &ast.CallExpr{Fun: ast.NewIdent("_takeLoopOwner"), Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: root.name}, &ast.UnaryExpr{Op: token.AND, X: varIdent(e.Var)}}}
			}
			return nil, &ast.CallExpr{Fun: ast.NewIdent("_takeScope"), Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: varIdent(e.Var)}}}
		}
		return nil, varIdent(e.Var)
	case *check.SeqCall:
		return g.seqCall(e)
	case *check.Generate:
		return nil, g.generateSeq(e)
	case *check.For, *check.Yield, *check.LoopControl:
		return g.effect(e), nil
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
		switch e.Op {
		case syntax.Not:
			op = token.NOT
		case syntax.Caret:
			op = token.XOR
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
			if b, ok := e.(*check.CallBuiltin); !ok || b.Builtin != check.BuiltinPanic && b.Builtin != check.BuiltinTodo {
				// Go does not know that the function never returns.
				stmts = append(stmts, unreachable()...)
			}
			return stmts, nil
		}
		if t == check.Ok {
			return append(stmts, &ast.ExprStmt{X: call}), g.okValue()
		}
		return stmts, call
	case *check.If, *check.Match:
		if t == check.Never {
			return g.effect(e), nil
		}
		if t == check.Ok {
			return g.effect(e), g.okValue()
		}
		res := g.newTmp()
		stmts := []ast.Stmt{varDecl(res, g.goType(t))}
		return append(stmts, g.into(e, sink{res: res, resType: t})...), res
	case *check.Block:
		if len(e.Stmts) == 0 && e.Tail != nil {
			return g.value(e.Tail)
		}
		if t == check.Never {
			return g.effect(e), nil
		}
		if t == check.Ok {
			return g.effect(e), g.okValue()
		}
		res := g.newTmp()
		if t == check.OwnedScope {
			// The owner the block gives keeps a fallback until its use.
			return []ast.Stmt{varDecl(res, g.goType(t)), dropOwner(res.Name)[2], &ast.BlockStmt{List: g.blockInto(e, sink{res: res, resType: t})}}, &ast.CallExpr{Fun: ast.NewIdent("_takeScope"), Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: res}}}
		}
		return []ast.Stmt{varDecl(res, g.goType(t)), &ast.BlockStmt{List: g.blockInto(e, sink{res: res, resType: t})}}, res
	case *check.ScopeBlock:
		if t == check.Never {
			return g.effect(e), nil
		}
		if t == check.Ok {
			return g.effect(e), g.okValue()
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
					var save []ast.Stmt
					save, xs[j] = g.save(xs[j], es[j].Type())
					out = append(out, save...)
				}
			}
		}
		if x == nil {
			// Earlier arguments still run, but the call cannot consume
			// their values. Keep saved temporaries used in the Go output.
			for _, prior := range xs {
				out = append(out, assign(ast.NewIdent("_"), prior))
			}
			return append(out, stmts...), nil
		}
		out = append(out, stmts...)
		xs = append(xs, x)
	}
	return out, xs
}

// save evaluates x, of type t, into a temporary now, and gives the
// expression that reads it later. A new owner waiting in one is closed
// if what runs before its call returns early or panics.
func (g *gen) save(x ast.Expr, t check.Type) ([]ast.Stmt, ast.Expr) {
	tmp := g.newTmp()
	if t != check.OwnedScope {
		return []ast.Stmt{define(tmp, x)}, tmp
	}
	return []ast.Stmt{define(tmp, x), dropOwner(tmp.Name)[2]}, &ast.CallExpr{Fun: ast.NewIdent("_takeScope"), Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: tmp}}}
}

// stable reports whether evaluating x later gives the same result:
// literals and identifiers (bork bindings are immutable).
func stable(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.BasicLit, *ast.Ident:
		return true
	case *ast.CallExpr:
		// Taking an owner out of its variable is left for the call, so
		// that its fallback still closes it if an argument after it
		// returns early or panics.
		if f, ok := x.Fun.(*ast.Ident); ok && f.Name == "_takeScope" {
			return true
		}
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
	syntax.Amp: token.AND, syntax.Pipe: token.OR, syntax.Caret: token.XOR, syntax.Shl: token.SHL, syntax.Shr: token.SHR,
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
		if e.BuildRead != nil {
			if !e.BuildRead.Captured {
				panic("compiler bug: build input was not captured")
			}
			value := &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(string(e.BuildRead.Data))}
			if e.BuildRead.Kind == "ReadBytes" {
				return nil, &ast.CallExpr{Fun: g.goType(e.Type()), Args: []ast.Expr{value}}
			}
			return nil, value
		}
		if e.Embedded != nil {
			return nil, g.embedCall(e)
		}
		if e.Func.Prelude && e.Func.Decl.Name == "scopeOf" {
			// b.scope borrows the owner's scope, leaving b armed.
			if v, ok := e.Args[0].(*check.VarRef); ok {
				g.captured(v.Var, true)
				return nil, borrowedName(varIdent(v.Var).Name)
			}
		}
		stmts, xs := g.values(e.EvaluationArgs())
		if xs == nil {
			return stmts, nil
		}
		if e.ArgOrder != nil {
			ordered := make([]ast.Expr, len(xs))
			for i, param := range e.ArgOrder {
				x := xs[i]
				if !stable(x) {
					var save []ast.Stmt
					save, x = g.save(x, e.Args[param].Type())
					stmts = append(stmts, save...)
				}
				ordered[param] = x
			}
			xs = ordered
		}
		inst := e.Inst
		for i := range xs {
			xs[i] = g.convert(xs[i], e.Args[i].Type(), inst.Params[i])
			xs[i] = g.instanceArgument(inst, i, xs[i])
		}
		stmts = append(stmts, g.takeOwnersLast(e.Args, inst.Params, xs)...)
		if inst.Func.Class != nil {
			fun, dicts := g.methodFunc(inst)
			return stmts, g.instanceResult(inst, &ast.CallExpr{Fun: fun, Args: append(dicts, xs...)})
		}
		g.genericCall(inst, e.Pos())
		// The ambient values it needs follow the arguments.
		needStmts, needs := g.values(e.Needs)
		stmts = append(stmts, needStmts...)
		if inst.Func.TrackCaller {
			needs = append(needs, g.callerLocation(e.Pos()))
		}
		if frame := g.passthrough(inst.Func); frame != nil {
			return stmts, g.nextCall(inst, frame, append(xs, needs...))
		}
		var dicts []ast.Expr
		for _, d := range inst.Dicts {
			dicts = append(dicts, g.dict(d))
		}
		return stmts, g.instanceResult(inst, &ast.CallExpr{Fun: g.instance(inst), Args: append(append(dicts, xs...), needs...)})
	}
	panic(fmt.Sprintf("unhandled call %T", e))
}

// takeOwnersLast evaluates the other arguments of a call that takes an
// owner first, so that the owner leaves its variable (and its fallback)
// only once nothing else can panic before the callee has it.
func (g *gen) takeOwnersLast(args []check.Expr, params []check.Type, xs []ast.Expr) []ast.Stmt {
	owner := false
	for _, a := range args {
		owner = owner || a.Type() == check.OwnedScope
	}
	if !owner {
		return nil
	}
	var stmts []ast.Stmt
	for i, a := range args {
		if _, simple := xs[i].(*ast.Ident); simple || a.Type() == check.OwnedScope {
			continue
		}
		tmp := g.newTmp()
		stmts = append(stmts, typedVar(tmp, g.goType(params[i]), xs[i]))
		xs[i] = tmp
	}
	return stmts
}

// A generic union returns any even when specialization collapses the union
// to one member. Recover that member's Go type at the call boundary.
func collapsedUnion(inst *check.Instance) bool {
	_, declared := inst.Func.Result.(*check.Union)
	_, specialized := inst.Result.(*check.Union)
	return declared && !specialized
}

func (g *gen) instanceResult(inst *check.Instance, call ast.Expr) ast.Expr {
	if inst.Func.Class != nil {
		return g.convert(call, inst.Result, inst.Result)
	}
	if collapsedUnion(inst) {
		if len(inst.TypeArgs) > 0 && hasTupleRepresentation(inst.Result) {
			return g.representationConversion(call, inst.Func.Result, inst.Result, g.parameterGoType(inst.Func.Result, inst.Func.TypeParams, inst.TypeArgs), g.goType(inst.Result), inst.Func.TypeParams, inst.TypeArgs, false)
		}
		return &ast.TypeAssertExpr{X: call, Type: g.goType(inst.Result)}
	}
	if hasTupleRepresentation(inst.Result) {
		if len(inst.TypeArgs) > 0 {
			return g.representationConversion(call, inst.Func.Result, inst.Result, g.parameterGoType(inst.Func.Result, inst.Func.TypeParams, inst.TypeArgs), g.goType(inst.Result), inst.Func.TypeParams, inst.TypeArgs, false)
		}
		return &ast.CallExpr{Fun: g.goType(inst.Result), Args: []ast.Expr{call}}
	}
	return call
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
	savedYield, savedLoops := g.yieldName, g.loops
	g.yieldName, g.loops = nil, nil
	defer func() { g.yieldName, g.loops = savedYield, savedLoops }()
	saved, savedScopes, savedOwners, savedMocks := g.fnResult, g.openScopes, g.blockOwners, g.openMocks
	g.fnResult, g.openScopes, g.blockOwners, g.openMocks = ft.Result, nil, nil, nil
	defer func() { g.openScopes, g.blockOwners, g.openMocks = savedScopes, savedOwners, savedMocks }()
	body := g.guardLabels(func() []ast.Stmt {
		if ft.Result == check.Ok {
			return g.effect(e.Body)
		}
		return g.tailReturn(e.Body)
	})
	g.fnResult = saved
	return &ast.FuncLit{Type: g.funcType(ft, names), Body: &ast.BlockStmt{List: body}}
}

// listLit lowers a list literal to a slice literal.
func (g *gen) listLit(e *check.ListLit) ([]ast.Stmt, ast.Expr) {
	lt := e.Type().(*check.List)
	if e.Nil {
		return nil, &ast.CallExpr{Fun: g.goType(lt), Args: []ast.Expr{ast.NewIdent("nil")}}
	}
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
	case check.BuiltinCallerLocation:
		return g.callerLocation(e.Pos())
	case check.BuiltinPrintln:
		for i := range args {
			args[i] = g.str(args[i], e.Args[i].Type())
		}
		return fmtCall("Println", args...)
	case check.BuiltinToString:
		return g.stringOf(args[0], e.Args[0].Type())
	case check.BuiltinConvert:
		return g.conversion(e, args[0])
	case check.BuiltinDbg:
		g.imports["os"] = true
		value := ast.NewIdent("value")
		t := g.goType(e.Type())
		location := e.Pos()
		location.Col = 0
		debugText := e.DebugText
		if v, ok := e.Args[0].(*check.VarRef); ok && v.Var.Let != nil && v.Var.Let.Initializer != nil {
			if v.Var.Let.Deferred == check.AsyncBinding {
				debugText += " (awaits async)"
			} else {
				debugText += " (forces lazy)"
			}
		} else if read, ok := e.Args[0].(*check.Select); ok && read.Field != nil && read.Field.Lazy {
			debugText += " (forces lazy)"
		}
		label := &ast.BinaryExpr{X: g.callerLocation(location), Op: token.ADD, Y: strLit(" " + debugText)}
		print := fmtCall("Fprintf", &ast.SelectorExpr{X: ast.NewIdent("os"), Sel: ast.NewIdent("Stderr")}, strLit("%s = %s\n"), label, g.stringOf(value, e.Type()))
		return &ast.CallExpr{Fun: &ast.FuncLit{
			Type: &ast.FuncType{
				Params:  &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{value}, Type: t}}},
				Results: &ast.FieldList{List: []*ast.Field{{Type: t}}},
			},
			Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: print}, &ast.ReturnStmt{Results: []ast.Expr{value}}}},
		}, Args: []ast.Expr{args[0]}}
	case check.BuiltinTodo:
		var message ast.Expr = strLit(fmt.Sprintf("%s:%d: todo", e.Pos().File, e.Pos().Line))
		if len(args) == 1 {
			message = &ast.BinaryExpr{X: strLit(fmt.Sprintf("%s:%d: todo: ", e.Pos().File, e.Pos().Line)), Op: token.ADD, Y: args[0]}
		}
		return &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{message}}
	case check.BuiltinPanic:
		return &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: args}
	case check.BuiltinAssertIsFailure:
		message := fmtCall("Sprintf", strLit(": expected %s, got %s (%s)"), strLit(e.Expected), g.stringOf(args[0], e.Args[0].Type()), g.assertIsTypeText(args[0], e.Args[0].Type()))
		return &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{&ast.BinaryExpr{X: g.callerLocation(e.Pos()), Op: token.ADD, Y: message}}}
	case check.BuiltinAssert:
		g.usesAssert = true
		return &ast.CallExpr{Fun: ast.NewIdent("_assert"), Args: []ast.Expr{args[0], g.callerLocation(e.Pos())}}
	case check.BuiltinAssertEqual:
		g.usesAssert = true
		t := e.Args[0].Type()
		actual := g.typed(args[0], t)
		expected := g.convert(args[1], e.Args[1].Type(), t)
		if (check.IsNumeric(t) || t == check.Rune) && isConst(expected) {
			expected = &ast.CallExpr{Fun: g.goType(t), Args: []ast.Expr{expected}}
		}
		return &ast.CallExpr{Fun: &ast.IndexExpr{X: ast.NewIdent("_assertEqual"), Index: g.goType(t)}, Args: []ast.Expr{actual, expected, g.callerLocation(e.Pos())}}
	case check.BuiltinAssertSnapshot:
		if !g.testMode {
			msg := &ast.BinaryExpr{X: g.callerLocation(e.Pos()), Op: token.ADD, Y: strLit(": assertSnapshot works only in tests (bork test)")}
			return &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{msg}}
		}
		g.usesSnaps = true
		text := g.stringOf(args[0], e.Args[0].Type())
		return &ast.CallExpr{Fun: ast.NewIdent("_assertSnapshot"), Args: []ast.Expr{text, g.callerLocation(e.Pos())}}
	}
	panic(fmt.Sprintf("unhandled builtin %s", e.Name))
}

// effect lowers an expression evaluated only for its effect.
func (g *gen) effect(e check.Expr) []ast.Stmt {
	switch e := e.(type) {
	case *check.SeqCall:
		stmts, call := g.seqCall(e)
		if call != nil {
			stmts = append(stmts, &ast.ExprStmt{X: call})
		}
		return stmts
	case *check.For:
		return g.forSeq(e)
	case *check.Yield:
		return g.yieldSeq(e)
	case *check.LoopControl:
		return g.loopControl(e)
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
	if e.Type() == check.Ok {
		// Ok into a union holding it.
		return append(g.effect(e), assign(k.res, g.okValue()))
	}
	stmts, x := g.value(e)
	if x != nil {
		stmts = append(stmts, assign(k.res, g.convert(x, e.Type(), k.resType)))
	}
	return stmts
}

// okValue is the Go value of Ok in a union.
func (g *gen) okValue() ast.Expr {
	return &ast.CompositeLit{Type: g.goType(check.Ok)}
}

// blockInto lowers a block: its statements, then its tail into k.
func (g *gen) blockInto(b *check.Block, k sink) []ast.Stmt {
	mark := len(g.blockOwners)
	// The block's mocks end with it.
	mocks := len(g.openMocks)
	g.blocks = append(g.blocks, b)
	defer func() {
		g.blockOwners = g.blockOwners[:mark]
		g.openMocks = g.openMocks[:mocks]
		g.blocks = g.blocks[:len(g.blocks)-1]
	}()
	out := g.stmts(b.Stmts)
	if g.diverges(b.Stmts) {
		return out
	}
	if len(b.Labels) > 0 {
		out = append(out, g.ambientPush(b.Labels)...)
	}
	if b.Tail == nil {
		// A block without a value, where a union holding Ok is wanted.
		switch {
		case k.ret && g.fnResult != check.Ok && g.fnResult != check.Never:
			return append(out, g.returning(g.okValue())...)
		case k.res != nil:
			out = append(out, assign(k.res, g.okValue()))
		}
		return append(out, g.endMocks(mocks)...)
	}
	out = append(out, g.debugLine(b.Tail.Pos())...)
	out = append(out, g.into(b.Tail, k)...)
	if k.ret || b.Tail.Type() == check.Never {
		return out // returning ended them
	}
	return append(out, g.endMocks(mocks)...)
}

// stmts lowers a block's statements (not its tail).
func (g *gen) stmts(list []check.Stmt) []ast.Stmt {
	var out []ast.Stmt
	for _, s := range list {
		switch statement := s.(type) {
		case *check.Let:
			out = append(out, g.debugLine(statement.Var.Pos)...)
		case *check.ExprStmt:
			out = append(out, g.debugLine(statement.X.Pos())...)
		case *check.Trust:
			out = append(out, g.debugLine(statement.Pos)...)
		case *check.Mock:
			out = append(out, g.debugLine(statement.Pos)...)
		}
		switch s := s.(type) {
		case *check.Let:
			if s.Initializer != nil {
				g.usesLazy = true
				constructor := "_lazyNew"
				args := []ast.Expr{g.lambda(s.Initializer)}
				if s.Deferred == check.AsyncBinding {
					g.usesAsync, g.usesScopes = true, true
					constructor = "_asyncNew"
					stmts, scope := g.value(s.AsyncScope)
					out = append(out, stmts...)
					if scope == nil {
						return out
					}
					args = append([]ast.Expr{scope}, args...)
				}
				if g.evalMode && s.Lazy != nil && s.Lazy.Effects == "nothing" {
					constructor = "_lazyConstNew"
				}
				ctor := &ast.IndexExpr{X: ast.NewIdent(constructor), Index: g.goType(s.Var.Type)}
				out = append(out, define(varIdent(s.Var), &ast.CallExpr{Fun: ctor, Args: args}))
				if s.Var.Unused || g.comptimeReads[s.Var] {
					out = append(out, assign(ast.NewIdent("_"), varIdent(s.Var)))
				}
				continue
			}
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
			if s.Declared || ((check.IsNumeric(vt) || vt == check.Rune) && isConst(x)) {
				// A declared type is kept, and an untyped Go constant would
				// get Go's default type (int, float64).
				out = append(out, typedVar(varIdent(s.Var), g.goType(bt), g.convert(x, vt, bt)))
			} else {
				out = append(out, define(varIdent(s.Var), g.convert(x, vt, vt)))
			}
			if s.Var.Unused || g.comptimeReads[s.Var] {
				out = append(out, assign(ast.NewIdent("_"), varIdent(s.Var)))
			}
			if bt == check.OwnedScope {
				if len(g.loops) > 0 {
					root := g.loops[len(g.loops)-1].cleanup
					root.used = true
					out = append(out, dropOwner(varIdent(s.Var).Name)[:2]...)
					out = append(out, &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: root.name, Sel: ast.NewIdent("owner")}, Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: varIdent(s.Var)}}}})
				} else {
					out = append(out, dropOwner(varIdent(s.Var).Name)...)
				}
				g.blockOwners = append(g.blockOwners, blockOwner{varIdent(s.Var).Name, len(g.openScopes)})
			}
		case *check.ExprStmt:
			out = append(out, g.effect(s.X)...)
		case *check.Trust:
			if g.testMode {
				out = append(out, g.trustCheck(s)...)
			}
		case *check.Mock:
			out = append(out, g.mockStmt(s)...)
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
				return g.blockInto(e, ret)
			}
			return g.tailReturn(e.Tail)
		}
		return []ast.Stmt{&ast.BlockStmt{List: g.blockInto(e, ret)}}
	case *check.ScopeBlock:
		return g.scopeInto(e, ret)
	case *check.Return:
		return g.returnStmt(e)
	}
	if e.Type() == check.Ok && g.fnResult != check.Ok {
		return append(g.effect(e), g.returning(g.okValue())...)
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
	s := varIdent(e.Var)
	closeCall := &ast.CallExpr{Fun: &ast.SelectorExpr{X: s, Sel: ast.NewIdent("close")}}
	// A scope inside another one in the function is cancelled with it.
	var parent ast.Expr = ast.NewIdent("nil")
	if n := len(g.openScopes); n > 0 {
		parent = g.openScopes[n-1]
	}
	// The cleanup policy is computed before the scope opens.
	stmts, policies := g.values(e.Policies)
	if policies == nil {
		return stmts
	}
	var policy []ast.Stmt
	for _, px := range policies {
		value := g.newTmp()
		stmts = append(stmts, define(value, px))
		fn := g.info.Funcs["setScopePolicy"]
		policy = append(policy, &ast.ExprStmt{X: &ast.CallExpr{Fun: g.funcName(fn), Args: []ast.Expr{s, value}}})
	}
	stmts = append(stmts, define(s, &ast.CallExpr{Fun: ast.NewIdent("_newScope"), Args: []ast.Expr{parent, &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(e.Var.Name)}}}))
	if len(g.loops) > 0 {
		root := g.loops[len(g.loops)-1].cleanup
		root.used = true
		stmts = append(stmts, &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: root.name, Sel: ast.NewIdent("scope")}, Args: []ast.Expr{s}}})
		closeCall = &ast.CallExpr{Fun: &ast.SelectorExpr{X: root.name, Sel: ast.NewIdent("closeScope")}, Args: []ast.Expr{s}}
	} else {
		stmts = append(stmts, &ast.DeferStmt{Call: &ast.CallExpr{Fun: &ast.SelectorExpr{X: s, Sel: ast.NewIdent("abort")}}})
	}
	stmts = append(stmts, policy...)
	g.openScopes = append(g.openScopes, s)
	g.scopeBodies = append(g.scopeBodies, scopeBody{body: e.Body, scope: s})
	stmts = append(stmts, g.blockInto(e.Body, k)...)
	g.scopeBodies = g.scopeBodies[:len(g.scopeBodies)-1]
	g.openScopes = g.openScopes[:len(g.openScopes)-1]
	if !k.ret && e.Body.Type() != check.Never {
		stmts = append(stmts, &ast.ExprStmt{X: closeCall})
	}
	return []ast.Stmt{&ast.BlockStmt{List: stmts}}
}

// blockOwner is an owner variable, bound where scopes scope blocks
// were open.
type blockOwner struct {
	name   string
	scopes int
}

// returning returns the given results (none or one) from the function,
// first closing the scopes around the return. The owners bound inside
// each scope block close before it, as they would at its end (their
// tasks may use what it releases, or own its children).
func (g *gen) returning(results ...ast.Expr) []ast.Stmt {
	if len(g.loops) > 0 {
		frame := g.loops[len(g.loops)-1]
		frame.exit.used = true
		if g.fnResult == check.OwnedScope {
			frame.cleanup.used = true
		}
		var stmts []ast.Stmt
		if len(results) > 0 {
			stmts = append(stmts, assign(frame.exit.result, results[0]))
		}
		stmts = append(stmts, assign(frame.exit.flag, ast.NewIdent("true")))
		return append(stmts, g.loopControl(&check.LoopControl{})...)
	}
	if len(g.openScopes) == 0 && len(g.openMocks) == 0 {
		return []ast.Stmt{&ast.ReturnStmt{Results: results}}
	}
	var stmts []ast.Stmt
	if len(results) > 0 {
		// The result is computed while the scopes are open.
		r := g.newTmp()
		stmts = append(stmts, typedVar(r, g.goType(g.fnResult), results[0]))
		results = []ast.Expr{r}
		if g.fnResult == check.OwnedScope {
			// A returned owner is closed if a scope fails to close.
			stmts = append(stmts, dropOwner(r.Name)[2])
			results = []ast.Expr{&ast.CallExpr{Fun: ast.NewIdent("_takeScope"), Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: r}}}}
		}
	}
	// Scopes, owners and mocks end innermost first.
	m := len(g.openMocks) - 1
	for i := len(g.openScopes) - 1; i >= -1; i-- {
		var ending []openMock
		for ; m >= 0 && g.openMocks[m].depth > i; m-- {
			ending = append(ending, g.openMocks[m])
		}
		stmts = append(stmts, endMocksOK(ending)...)
		if i < 0 {
			break
		}
		for j := len(g.blockOwners) - 1; j >= 0; j-- {
			if o := g.blockOwners[j]; o.scopes == i+1 {
				take := &ast.CallExpr{Fun: ast.NewIdent("_takeScope"), Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: name(o.name)}}}
				stmts = append(stmts, &ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("_dropScope"), Args: []ast.Expr{take}}})
			}
		}
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
		s.Else = &ast.BlockStmt{List: rest}
		if len(rest) == 1 {
			if chained, ok := rest[0].(*ast.IfStmt); ok {
				s.Else = chained
			}
		}
	}
	return append(stmts, s)
}

func (g *gen) returnStmt(e *check.Return) []ast.Stmt {
	if e.Value == nil {
		return g.returning()
	}
	if g.fnResult == check.Ok && e.Value.Type() == check.Ok {
		return append(g.effect(e.Value), g.returning()...)
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
	if hasTupleRepresentation(to) {
		return &ast.CallExpr{Fun: g.goType(to), Args: []ast.Expr{x}}
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

// parenHeaders parenthesizes the composite literals in the headers of
// if, for, and switch statements: there, Go reads `P{` as the start of
// the statement's block. Only literals outside brackets need it (not
// those in call arguments, indexes, or other literals), and each
// literal is wrapped rather than the whole header: go/printer drops
// parentheses around a whole header whose literal has a generic type.
// (So a header that is nothing but a literal of a generic type would
// still print wrongly; none is generated.)
func parenHeaders(decls []ast.Decl) {
	var wrap func(x ast.Expr) ast.Expr
	wrap = func(x ast.Expr) ast.Expr {
		switch n := x.(type) {
		case *ast.CompositeLit:
			return &ast.ParenExpr{X: n}
		case *ast.SelectorExpr:
			n.X = wrap(n.X)
		case *ast.CallExpr:
			n.Fun = wrap(n.Fun)
		case *ast.IndexExpr:
			n.X = wrap(n.X)
		case *ast.IndexListExpr:
			n.X = wrap(n.X)
		case *ast.SliceExpr:
			n.X = wrap(n.X)
		case *ast.TypeAssertExpr:
			n.X = wrap(n.X)
		case *ast.StarExpr:
			n.X = wrap(n.X)
		case *ast.UnaryExpr:
			n.X = wrap(n.X)
		case *ast.BinaryExpr:
			n.X, n.Y = wrap(n.X), wrap(n.Y)
		}
		// Anything else (a parenthesized expression, a function literal,
		// a name) holds no literal Go could misread.
		return x
	}
	expr := func(x *ast.Expr) {
		if *x == nil {
			return
		}
		// Parentheses around the whole header would be dropped.
		for {
			p, ok := (*x).(*ast.ParenExpr)
			if !ok {
				break
			}
			*x = p.X
		}
		*x = wrap(*x)
	}
	stmt := func(s ast.Stmt) {
		switch s := s.(type) {
		case *ast.AssignStmt:
			for i := range s.Lhs {
				expr(&s.Lhs[i])
			}
			for i := range s.Rhs {
				expr(&s.Rhs[i])
			}
		case *ast.ExprStmt:
			expr(&s.X)
		case *ast.IncDecStmt:
			expr(&s.X)
		case *ast.SendStmt:
			expr(&s.Chan)
			expr(&s.Value)
		}
	}
	for _, d := range decls {
		ast.Inspect(d, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.IfStmt:
				stmt(n.Init)
				expr(&n.Cond)
			case *ast.ForStmt:
				stmt(n.Init)
				expr(&n.Cond)
				stmt(n.Post)
			case *ast.RangeStmt:
				expr(&n.X)
			case *ast.SwitchStmt:
				stmt(n.Init)
				expr(&n.Tag)
			case *ast.TypeSwitchStmt:
				stmt(n.Init)
				stmt(n.Assign)
			}
			return true
		})
	}
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

// typed gives a number or rune constant x its Go type explicitly, for contexts
// where Go would otherwise pick a default type.
func (g *gen) typed(x ast.Expr, t check.Type) ast.Expr {
	if (check.IsNumeric(t) || t == check.Rune) && isConst(x) {
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
	case *check.Seq, *check.Union, *check.List, *check.Map, *check.FuncType, *check.TypeParam, *check.Record, *check.Sealed:
		return true
	}
	return check.IsFloat(t) || t == check.Rune
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

// Report bork type names even when the input is represented by Go's any.
func (g *gen) assertIsTypeText(value ast.Expr, typ check.Type) ast.Expr {
	union, ok := typ.(*check.Union)
	if !ok {
		return strLit(check.TypeText(typ, nil))
	}
	body := []ast.Stmt{}
	for _, member := range union.Members {
		body = append(body, &ast.IfStmt{Cond: g.isType(g.goType(member), value), Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{strLit(check.TypeText(member, nil))}}}}})
	}
	body = append(body, &ast.ReturnStmt{Results: []ast.Expr{strLit(check.TypeText(typ, nil))}})
	return &ast.CallExpr{Fun: &ast.FuncLit{Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}}}}, Body: &ast.BlockStmt{List: body}}}
}
