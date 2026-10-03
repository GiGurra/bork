package gen

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
)

// Mocks (see "Mocking in tests" in docs/requirements.md). In the
// program `bork test` builds, a function that some test mocks is
// generated as a dispatcher under its own name, which runs the mock in
// force for the current goroutine, if any, and otherwise the real body,
// renamed _real_<name>. Functions no test mocks, and every function of
// other builds, are generated as before.

// openMock is a mock statement whose block is being generated: its
// frame's Go variable, and how many scope blocks were open around it.
// A mock written directly in a scope block's body is scoped: it ends as
// one of the scope's finalizers, once the scope's tasks have finished,
// and its block's end only restores the test's labels.
type openMock struct {
	frame  *ast.Ident
	depth  int
	scoped bool
	// ambient is set for a with's publication of its marked values
	// (see ambientPush), which ends as a mock does, by restoring the
	// labels from before it.
	ambient bool
}

// scopeBody is a scope block being generated: its body, and the Go
// variable of its scope.
type scopeBody struct {
	body  *check.Block
	scope *ast.Ident
}

// mockBody is a mock's body being generated: inside it, the target's
// name means what it meant before the mock (see passthrough).
type mockBody struct {
	target *check.Func
	frame  *ast.Ident
}

// mockTargets numbers the functions the tests mock, which the runtime
// identifies them by.
func (g *gen) mockTargets(info *check.Info, notRun map[*check.Func]string) {
	for _, m := range info.Mocks {
		if _, skipped := notRun[m.MockIn]; skipped {
			continue // its test's body is not generated
		}
		if g.mockIDs == nil {
			g.mockIDs = map[*check.Func]int{}
		}
		if g.mockIDs[m.MockOf] == 0 {
			g.mockIDs[m.MockOf] = len(g.mockIDs) + 1
		}
		if len(m.MockOf.TypeParams) > 0 {
			// A generic mock's body is a generic Go function of its own
			// (Go has no generic function values): _mockBody<n>, with
			// what it captures from the test in a _mockEnv<n>.
			if g.genericMocks == nil {
				g.genericMocks = map[*check.Func]int{}
			}
			g.genericMocks[m] = len(g.genericMocks) + 1
		}
	}
}

// typeArgs instantiates the Go function fun with fn's type parameters,
// if it has any: fun[T, U].
func (g *gen) typeArgs(fun ast.Expr, fn *check.Func) ast.Expr {
	if len(fn.TypeParams) == 0 {
		return fun
	}
	idx := &ast.IndexListExpr{X: fun}
	for _, tp := range fn.TypeParams {
		idx.Indices = append(idx.Indices, g.goType(tp))
	}
	return idx
}

// typeParamFields declares fn's type parameters for a Go function.
func (g *gen) typeParamFields(fn *check.Func) *ast.FieldList {
	if len(fn.TypeParams) == 0 {
		return nil
	}
	tps := &ast.Field{Type: ast.NewIdent("any")}
	for _, tp := range fn.TypeParams {
		tps.Names = append(tps.Names, g.goType(tp).(*ast.Ident))
	}
	return &ast.FieldList{List: []*ast.Field{tps}}
}

// mangleTypeParams names fn's type parameters _T<n> in Go while a
// generic mock's body is generated, so they cannot hide the test's
// types or values of the same name (on), and back (off).
func (g *gen) mangleTypeParams(fn *check.Func, on bool) {
	for i, tp := range fn.TypeParams {
		if !on {
			delete(g.typeParamNames, tp)
			continue
		}
		if g.typeParamNames == nil {
			g.typeParamNames = map[*check.TypeParam]string{}
		}
		g.typeParamNames[tp] = "_T" + strconv.Itoa(i)
	}
}

// realName is the Go name of a function's real body: _real_<name> if a
// test mocks it.
func (g *gen) realName(fn *check.Func, goName string) string {
	if g.mockIDs[fn] != 0 {
		return "_real_" + goName
	}
	return goName
}

// mockFuncType is the Go type of a mock of fn: its parameters, then
// the ambient values it needs (names, if given, name both).
func (g *gen) mockFuncType(fn *check.Func, names []*ast.Ident) *ast.FuncType {
	params := append([]check.Type(nil), fn.Params...)
	for _, n := range fn.Needs {
		params = append(params, n.Type)
	}
	if fn.TrackCaller {
		params = append(params, check.String)
	}
	return g.funcType(&check.FuncType{Params: params, Result: fn.Result}, names)
}

// dispatchers generates, for a function a test mocks, the dispatcher
// under its name and _next_<name>, which runs the mock in force from a
// frame on (or the real body):
//
//	func charge(card Card, amount int64) any {
//		return _next_charge(_mockCurrent(), card, amount)
//	}
//
//	func _next_charge(_from *_mockFrame, card Card, amount int64) any {
//		if _f := _mockFind(1, _from); _f != nil {
//			defer _f.done()
//			_f.called(func() []string { return []string{_show(card), _show(amount)} },
//				func() any { return ChargeCall{card: card, amount: amount} })
//			return _f.fn.(func(Card, int64) any)(card, amount)
//		}
//		return _real_charge(card, amount)
//	}
func (g *gen) dispatchers(fn *check.Func) []ast.Decl {
	g.usesMocks = true
	g.usesShow = true
	goName := g.funcName(fn).Name
	id := g.mockIDs[fn]
	var params []*ast.Ident
	var args, shown []ast.Expr
	for i := range fn.Params {
		p := ast.NewIdent("p" + strconv.Itoa(i))
		if i < len(fn.Decl.Params) {
			p = name(fn.Decl.Params[i].Name)
		}
		params = append(params, p)
		args = append(args, p)
		shown = append(shown, &ast.CallExpr{Fun: ast.NewIdent("_show"), Args: []ast.Expr{p}})
	}
	for _, v := range fn.NeedVars {
		params = append(params, varIdent(v))
		args = append(args, varIdent(v))
	}
	if fn.TrackCaller {
		params = append(params, ast.NewIdent("_callerAt"))
		args = append(args, ast.NewIdent("_callerAt"))
	}
	returns := fn.Result != check.Unit && fn.Result != check.Never
	result := func(call ast.Expr) []ast.Stmt {
		if returns {
			return []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{call}}}
		}
		return []ast.Stmt{&ast.ExprStmt{X: call}, &ast.ReturnStmt{}}
	}
	// A generic function's instances of its bounds come first.
	dictFields := g.dictParams(fn.TypeParams)
	var dicts []ast.Expr
	for _, d := range dictFields {
		dicts = append(dicts, d.Names[0])
	}
	from, f := ast.NewIdent("_from"), ast.NewIdent("_f")
	next := &ast.FuncDecl{
		Name: ast.NewIdent("_next_" + goName),
		Type: g.mockFuncType(fn, params),
	}
	next.Type.TypeParams = g.typeParamFields(fn)
	next.Type.Params.List = append(append([]*ast.Field{{Names: []*ast.Ident{from}, Type: &ast.StarExpr{X: ast.NewIdent("_mockFrame")}}}, dictFields...), next.Type.Params.List...)
	var mockCall ast.Expr = &ast.CallExpr{
		Fun:  &ast.TypeAssertExpr{X: &ast.SelectorExpr{X: f, Sel: ast.NewIdent("fn")}, Type: g.mockFuncType(fn, nil)},
		Args: args,
	}
	texts := &ast.FuncLit{
		Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: &ast.ArrayType{Elt: ast.NewIdent("string")}}}}},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{&ast.CompositeLit{Type: &ast.ArrayType{Elt: ast.NewIdent("string")}, Elts: shown}}}}},
	}
	var typed ast.Expr = ast.NewIdent("nil")
	if rec := g.info.MockCalls[fn]; rec != nil {
		// The call record, for the handle's args, expect and waitFor.
		lit := &ast.CompositeLit{Type: g.goType(rec)}
		for _, field := range rec.Fields {
			for i, p := range fn.Decl.Params {
				if p.Name == field.Name || field.Name == "p"+strconv.Itoa(i) {
					lit.Elts = append(lit.Elts, &ast.KeyValueExpr{Key: name(field.Name), Value: params[i]})
					break
				}
			}
		}
		typed = &ast.FuncLit{
			Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("any")}}}},
			Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{lit}}}},
		}
	}
	found := []ast.Stmt{
		&ast.DeferStmt{Call: &ast.CallExpr{Fun: &ast.SelectorExpr{X: f, Sel: ast.NewIdent("done")}}},
		&ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: f, Sel: ast.NewIdent("called")}, Args: []ast.Expr{texts, typed}}},
	}
	if len(fn.TypeParams) == 0 {
		found = append(found, result(mockCall)...)
	} else {
		// The mock's frame holds its body's environment, whose type
		// says which generic body to run.
		env := ast.NewIdent("_env")
		sw := &ast.TypeSwitchStmt{
			Assign: define(env, &ast.TypeAssertExpr{X: &ast.SelectorExpr{X: f, Sel: ast.NewIdent("fn")}}),
			Body:   &ast.BlockStmt{},
		}
		for _, m := range g.info.Mocks {
			k := g.genericMocks[m]
			if m.MockOf != fn || k == 0 {
				continue
			}
			call := &ast.CallExpr{
				Fun:  g.typeArgs(ast.NewIdent("_mockBody"+strconv.Itoa(k)), fn),
				Args: append(append([]ast.Expr{env, f}, dicts...), args...),
			}
			sw.Body.List = append(sw.Body.List, &ast.CaseClause{
				List: []ast.Expr{&ast.StarExpr{X: ast.NewIdent("_mockEnv" + strconv.Itoa(k))}},
				Body: result(call),
			})
		}
		found = append(found, sw, &ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{strLit("bork: a mock of " + fn.Decl.Name + " has no body")}}})
	}
	body := []ast.Stmt{&ast.IfStmt{
		Init: define(f, &ast.CallExpr{Fun: ast.NewIdent("_mockFind"), Args: []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(id)}, from}}),
		Cond: &ast.BinaryExpr{X: f, Op: token.NEQ, Y: ast.NewIdent("nil")},
		Body: &ast.BlockStmt{List: found},
	}}
	real := &ast.CallExpr{Fun: g.typeArgs(ast.NewIdent("_real_"+goName), fn), Args: append(append([]ast.Expr(nil), dicts...), args...)}
	if returns {
		body = append(body, &ast.ReturnStmt{Results: []ast.Expr{real}})
	} else {
		body = append(body, &ast.ExprStmt{X: real})
	}
	next.Body = &ast.BlockStmt{List: body}
	dispatch := &ast.FuncDecl{Name: ast.NewIdent(goName), Type: g.mockFuncType(fn, params)}
	dispatch.Type.TypeParams = g.typeParamFields(fn)
	dispatch.Type.Params.List = append(g.dictParams(fn.TypeParams), dispatch.Type.Params.List...)
	call := &ast.CallExpr{Fun: g.typeArgs(next.Name, fn), Args: append(append([]ast.Expr{&ast.CallExpr{Fun: ast.NewIdent("_mockCurrent")}}, dicts...), args...)}
	if returns {
		dispatch.Body = &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{call}}}}
	} else {
		dispatch.Body = &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: call}}}
	}
	return []ast.Decl{dispatch, next}
}

// mockStmt generates a mock statement: its frame is pushed now, its
// parent being the innermost mock open around it, and ended when its
// block ends (see endMocks), or, if the test panics, by a deferred end.
//
//	var _mf1 *_mockFrame
//	_mf1 = _mockPush(1, "fetch", "main.bork:3:3", true, func(url string) any { ... }, nil)
//	defer _mf1.end()
//	calls := Mock[FetchCall]{countFn: _mf1.count, callsFn: _mf1.calls,
//		argsFn: _mockArgsFn[FetchCall](_mf1), ...}
//
// Its block's normal end calls _mf1.endOK() and then _mockVerify(_mf1),
// which checks what the handle's expect calls declared; on a panic,
// the deferred end checks nothing.
//
// A mock directly in a scope's body ends with the scope instead, after
// its tasks: s.Defer(func() { _mf1.finishIn(s) }), and its block's end (or a panic)
// restores the test goroutine's labels with _mf1.restore().
func (g *gen) mockStmt(m *check.Mock) []ast.Stmt {
	g.usesMocks = true
	g.mockN++
	frame := ast.NewIdent("_mf" + strconv.Itoa(g.mockN))
	names := make([]*ast.Ident, len(m.Func.ParamVars))
	for i, p := range m.Func.ParamVars {
		names[i] = name(p.Name)
	}
	for _, v := range m.Func.NeedVars {
		names = append(names, varIdent(v))
	}
	savedCaller := g.callerAt
	g.callerAt = nil
	if m.Target.TrackCaller {
		names = append(names, ast.NewIdent("_callerAt"))
		g.callerAt = ast.NewIdent("_callerAt")
	}
	savedResult, savedScopes, savedOwners, savedMocks := g.fnResult, g.openScopes, g.blockOwners, g.openMocks
	g.fnResult, g.openScopes, g.blockOwners, g.openMocks = m.Target.Result, nil, nil, nil
	g.mockBodies = append(g.mockBodies, mockBody{target: m.Target, frame: frame})
	// A loop around the mock is not the body's to break or continue.
	savedYield, savedLoops := g.yieldName, g.loops
	g.yieldName, g.loops = nil, nil
	var capture *mockCapture
	if g.genericMocks[m.Func] != 0 {
		capture = &mockCapture{from: m.Pos, to: m.Func.Body.End, seen: map[string]bool{}, target: m.Target, text: m.Text}
		g.captures = append(g.captures, capture)
		// The body's code names the type parameters as its Go function
		// does (genericMockBody).
		g.mangleTypeParams(m.Target, true)
	}
	body := g.guardLabels(func() []ast.Stmt { return g.blockInto(m.Func.Body, sink{ret: m.Target.Result != check.Unit}) })
	g.mockBodies = g.mockBodies[:len(g.mockBodies)-1]
	g.yieldName, g.loops = savedYield, savedLoops
	if capture != nil {
		g.captures = g.captures[:len(g.captures)-1]
		g.mangleTypeParams(m.Target, false)
	}
	g.callerAt = savedCaller
	g.fnResult, g.openScopes, g.blockOwners, g.openMocks = savedResult, savedScopes, savedOwners, savedMocks
	var lit ast.Expr = &ast.FuncLit{Type: g.mockFuncType(m.Target, names), Body: &ast.BlockStmt{List: body}}
	if k := g.genericMocks[m.Func]; k != 0 {
		lit = g.genericMockBody(m, k, frame, names, body, capture)
	}
	var parent ast.Expr = ast.NewIdent("nil")
	open := append(append([]openMock(nil), g.outerMocks...), g.openMocks...)
	for i := len(open) - 1; i >= 0; i-- {
		if !open[i].ambient {
			parent = open[i].frame
			break
		}
	}
	var scope *ast.Ident
	if n, b := len(g.scopeBodies), len(g.blocks); n > 0 && b > 0 && g.scopeBodies[n-1].body == g.blocks[b-1] {
		scope = g.scopeBodies[n-1].scope
	}
	deferred := "end"
	if scope != nil {
		deferred = "restore"
	}
	stmts := []ast.Stmt{
		&ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{&ast.ValueSpec{Names: []*ast.Ident{frame}, Type: &ast.StarExpr{X: ast.NewIdent("_mockFrame")}}}}},
		assign(frame, &ast.CallExpr{Fun: ast.NewIdent("_mockPush"), Args: []ast.Expr{
			&ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(g.mockIDs[m.Target])},
			strLit(m.Text),
			at(m.Pos),
			ast.NewIdent(strconv.FormatBool(m.Var != nil)),
			lit,
			parent,
		}}),
		&ast.DeferStmt{Call: &ast.CallExpr{Fun: &ast.SelectorExpr{X: frame, Sel: ast.NewIdent(deferred)}}},
	}
	if scope != nil {
		stmts = append(stmts, &ast.ExprStmt{X: &ast.CallExpr{
			Fun: &ast.SelectorExpr{X: scope, Sel: ast.NewIdent("Defer")},
			Args: []ast.Expr{&ast.FuncLit{
				Type: &ast.FuncType{Params: &ast.FieldList{}},
				Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: frame, Sel: ast.NewIdent("finishIn")}, Args: []ast.Expr{scope}}}}},
			}},
		}})
	}
	g.openMocks = append(g.openMocks, openMock{frame: frame, depth: len(g.openScopes), scoped: scope != nil})
	if m.Var != nil {
		rec := g.goType(g.info.MockCalls[m.Target])
		generic := func(fn string) ast.Expr {
			return &ast.CallExpr{Fun: &ast.IndexExpr{X: ast.NewIdent(fn), Index: rec}, Args: []ast.Expr{frame}}
		}
		handle := &ast.CompositeLit{Type: g.goType(m.Var.Type), Elts: []ast.Expr{
			&ast.KeyValueExpr{Key: ast.NewIdent("countFn"), Value: &ast.SelectorExpr{X: frame, Sel: ast.NewIdent("count")}},
			&ast.KeyValueExpr{Key: ast.NewIdent("callsFn"), Value: &ast.SelectorExpr{X: frame, Sel: ast.NewIdent("calls")}},
			&ast.KeyValueExpr{Key: ast.NewIdent("argsFn"), Value: generic("_mockArgsFn")},
			&ast.KeyValueExpr{Key: ast.NewIdent("expectFn"), Value: generic("_mockExpectFn")},
			&ast.KeyValueExpr{Key: ast.NewIdent("waitFn"), Value: generic("_mockWaitFn")},
		}}
		stmts = append(stmts, define(name(m.Var.Name), handle))
		if m.Var.Unused {
			stmts = append(stmts, assign(ast.NewIdent("_"), name(m.Var.Name)))
		}
	}
	return stmts
}

// mockCapture collects the test's variables a generic mock's body uses:
// those declared outside the mock statement (from..to).
type mockCapture struct {
	from, to diag.Pos
	vars     []capturedVar
	seen     map[string]bool
	target   *check.Func
	text     string // the mock's target as written
}

// capturedVar is a Go variable of the test that a generic mock's body
// uses, and its Go type.
type capturedVar struct {
	name *ast.Ident
	typ  ast.Expr
}

// captured notes that code being generated uses v, if a generic mock's
// body is being generated and v is the test's; borrowed is for the
// scope it borrows from an owned scope (b.scope), _scopeOf_b.
func (g *gen) captured(v *check.Var, borrowed bool) {
	if len(g.captures) == 0 {
		return
	}
	c := g.captures[len(g.captures)-1]
	p := v.Pos
	inside := p.File == c.from.File &&
		(p.Line > c.from.Line || p.Line == c.from.Line && p.Col >= c.from.Col) &&
		(p.Line < c.to.Line || p.Line == c.to.Line && p.Col <= c.to.Col)
	n, t := name(v.Name), ast.Expr(nil)
	if borrowed {
		n, t = borrowedName(v.Name), &ast.StarExpr{X: ast.NewIdent("_Scope")}
	}
	if inside || c.seen[n.Name] {
		return
	}
	if t == nil {
		saved := g.captures
		g.captures = nil // the test's types, not the body's
		t = g.goType(v.Type)
		g.captures = saved
	}
	c.seen[n.Name] = true
	c.vars = append(c.vars, capturedVar{name: n, typ: t})
}

// genericCall checks a call (or function value) inst in a generic
// mock's body: Go cannot compile a generic body that reaches its own
// target at a type built from its type parameters (decode[List[T]]
// from decode[T]), since each instance would need another.
func (g *gen) genericCall(inst *check.Instance, pos diag.Pos) {
	if len(g.captures) == 0 || len(inst.TypeArgs) == 0 {
		return
	}
	c := g.captures[len(g.captures)-1]
	for _, a := range inst.TypeArgs {
		if _, bare := a.(*check.TypeParam); bare || !check.MentionsTypeParams(a, c.target.TypeParams) {
			continue
		}
		if check.Reaches(inst.Func, c.target) {
			via := ""
			if inst.Func != c.target {
				via = fmt.Sprintf(" (through %s)", inst.Func.Decl.Name)
			}
			g.mockErrors.AddCode(pos, "mock.generic-recursion", "the mock of %s reaches %s again%s at type %s, made from its own type parameters; a generic mock's body can reach it only at the same type parameters (Go compiles one body per instantiation, and this one would need endlessly many)", c.text, c.text, via, check.TypeText(a, nil))
			return
		}
	}
}

// genericMockBody declares the body of the generic mock m as the Go
// function _mockBody<k>, generic in the target's type parameters, with
// what it uses of the test in a _mockEnv<k>, and gives the environment
// that the frame holds:
//
//	type _mockEnv1 struct{ fallback string }
//	func _mockBody1[T any](_env *_mockEnv1, _mf1 *_mockFrame, dicts..., text string) T {
//		fallback := _env.fallback
//		...
//	}
//
// and &_mockEnv1{fallback: fallback} for _mockPush.
func (g *gen) genericMockBody(m *check.Mock, k int, frame *ast.Ident, names []*ast.Ident, body []ast.Stmt, c *mockCapture) ast.Expr {
	envName := ast.NewIdent("_mockEnv" + strconv.Itoa(k))
	env := ast.NewIdent("_env")
	st := &ast.StructType{Fields: &ast.FieldList{}}
	lit := &ast.CompositeLit{Type: envName}
	var unpack []ast.Stmt
	for _, v := range c.vars {
		st.Fields.List = append(st.Fields.List, &ast.Field{Names: []*ast.Ident{v.name}, Type: v.typ})
		lit.Elts = append(lit.Elts, &ast.KeyValueExpr{Key: v.name, Value: v.name})
		unpack = append(unpack,
			define(v.name, &ast.SelectorExpr{X: env, Sel: v.name}),
			assign(ast.NewIdent("_"), v.name))
	}
	g.mangleTypeParams(m.Target, true)
	defer g.mangleTypeParams(m.Target, false)
	ft := g.mockFuncType(m.Target, names)
	ft.TypeParams = g.typeParamFields(m.Target)
	ft.Params.List = append(append([]*ast.Field{
		{Names: []*ast.Ident{env}, Type: &ast.StarExpr{X: envName}},
		{Names: []*ast.Ident{frame}, Type: &ast.StarExpr{X: ast.NewIdent("_mockFrame")}},
	}, g.dictParams(m.Target.TypeParams)...), ft.Params.List...)
	g.extraFuncs = append(g.extraFuncs,
		&ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{Name: envName, Type: st}}},
		&ast.FuncDecl{Name: ast.NewIdent("_mockBody" + strconv.Itoa(k)), Type: ft, Body: &ast.BlockStmt{List: append(unpack, body...)}},
	)
	return &ast.UnaryExpr{Op: token.AND, X: lit}
}

// endMocks ends the mocks pushed since the first `from` of openMocks
// (innermost first), at the normal end of their block, then checks
// their expectations.
func (g *gen) endMocks(from int) []ast.Stmt {
	var ending []openMock
	for i := len(g.openMocks) - 1; i >= from; i-- {
		ending = append(ending, g.openMocks[i])
	}
	return endMocksOK(ending)
}

// endMocksOK ends mocks at the normal end of their block (a scoped one
// only restores the labels: its scope ends and checks it), then checks
// the others' expectations, all of them, so every unmet one is
// reported.
func endMocksOK(ending []openMock) []ast.Stmt {
	var out []ast.Stmt
	var check []ast.Expr
	for _, m := range ending {
		method := "endOK"
		if m.ambient {
			out = append(out, &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: m.frame, Sel: ast.NewIdent("restore")}}})
			continue
		}
		if m.scoped {
			method = "restoreOK"
		} else {
			check = append(check, m.frame)
		}
		out = append(out, &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: m.frame, Sel: ast.NewIdent(method)}}})
	}
	if len(check) > 0 {
		out = append(out, &ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("_mockVerify"), Args: check}})
	}
	return out
}

// passthrough is the frame of the mock whose body names fn, if code in
// a mock's body of fn is being generated: there, fn means what it meant
// before the mock.
func (g *gen) passthrough(fn *check.Func) *ast.Ident {
	for i := len(g.mockBodies) - 1; i >= 0; i-- {
		if g.mockBodies[i].target == fn {
			return g.mockBodies[i].frame
		}
	}
	return nil
}

// nextCall calls fn (inst) as it was before the mock of frame: the
// outer mock or the real function.
func (g *gen) nextCall(inst *check.Instance, frame *ast.Ident, args []ast.Expr) ast.Expr {
	var fun ast.Expr = ast.NewIdent("_next_" + g.funcName(inst.Func).Name)
	if len(inst.TypeArgs) > 0 {
		idx := &ast.IndexListExpr{X: fun}
		for _, t := range inst.TypeArgs {
			idx.Indices = append(idx.Indices, g.goType(t))
		}
		fun = idx
	}
	all := []ast.Expr{&ast.SelectorExpr{X: frame, Sel: ast.NewIdent("parent")}}
	for _, d := range inst.Dicts {
		all = append(all, g.dict(d))
	}
	return g.instanceResult(inst, &ast.CallExpr{Fun: fun, Args: append(all, args...)})
}

// nextRef is fn (inst) as a value, as it was before the mock of frame.
// The ambient values it needs are those in force where it is named.
func (g *gen) nextRef(inst *check.Instance, frame *ast.Ident, needs []ast.Expr) ast.Expr {
	var names []*ast.Ident
	var args []ast.Expr
	for i := range inst.Params {
		p := ast.NewIdent("_a" + strconv.Itoa(i))
		names = append(names, p)
		args = append(args, p)
	}
	call := g.nextCall(inst, frame, append(args, needs...))
	var body ast.Stmt = &ast.ExprStmt{X: call}
	if inst.Result != check.Unit && inst.Result != check.Never {
		body = &ast.ReturnStmt{Results: []ast.Expr{call}}
	}
	ft := g.funcType(&check.FuncType{Params: inst.Params, Result: inst.Result}, names)
	return &ast.FuncLit{Type: ft, Body: &ast.BlockStmt{List: []ast.Stmt{body}}}
}

// mockRuntime keeps the mocks in force: a chain of frames, one per
// mock statement, each pointing to the frame in force where it was
// pushed. A frame is attached to goroutines through Go's profiler
// labels: it has a label set of its own (the labels before it, and
// bork.mock), and _mockFrames maps that label set to it, and to the
// label sets a with makes from it while it is in force. The Go
// runtime copies a goroutine's labels to every goroutine it starts, so
// tasks, servers, and goroutines started by Go code see the mocks in
// force where they were started.
const mockRuntime = `package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

type _mockFrame struct {
	parent *_mockFrame
	id     int    // the mocked function's
	name   string // as the mock statement wrote it
	at     string // the mock statement's position
	fn     any    // the mock: a func of the function's Go type
	record bool   // whether calls are recorded (it has a handle)
	prev     unsafe.Pointer // the labels to restore when it ends
	restored atomic.Bool
	ok       atomic.Bool // its block ended normally
	checked  atomic.Bool // its expectations were checked (or never will be)
	ended    atomic.Bool

	mu      sync.Mutex
	idle    *sync.Cond
	changed *sync.Cond // a call was counted
	active  int        // calls running now
	n       int64
	texts   []string
	args    []any // call records, as texts
	expects []_mockExpect
}

// _mockExpect is what an expect call on a mock's handle declared: how
// many calls (that match, if where) it must answer.
type _mockExpect struct {
	match  func(any) bool
	where  bool
	lo, hi int64 // hi < 0: no limit
	at string
}

// _mockFrames maps a label set (its pointer) to the frame it belongs
// to. Frames stay in it after they end: a task started under one may
// still run, and finds the frames outside it through it.
var _mockFrames sync.Map

var _mockSeq atomic.Int64

// _mockCurrent is the frame in force on this goroutine, or nil.
func _mockCurrent() *_mockFrame {
	p := _labels()
	if p == nil {
		return nil
	}
	f, _ := _mockFrames.Load(p)
	frame, _ := f.(*_mockFrame)
	return frame
}

// _mockFind is the innermost frame from f on that mocks function id and
// has not ended, or nil. The call it is found for runs until done.
func _mockFind(id int, f *_mockFrame) *_mockFrame {
	for ; f != nil; f = f.parent {
		if f.id != id {
			continue
		}
		f.mu.Lock()
		if f.ended.Load() {
			f.mu.Unlock()
			continue
		}
		f.active++
		f.mu.Unlock()
		return f
	}
	return nil
}

func (f *_mockFrame) done() {
	f.mu.Lock()
	f.active--
	if f.active == 0 {
		f.idle.Broadcast()
	}
	f.mu.Unlock()
}

// called counts a call, and records its arguments if the mock has a
// handle.
func (f *_mockFrame) called(args func() []string, typed func() any) {
	text := ""
	var record any
	if f.record {
		text = f.name + "(" + strings.Join(args(), ", ") + ")"
		if typed != nil {
			record = typed()
		}
	}
	f.mu.Lock()
	f.n++
	if f.record {
		f.texts = append(f.texts, text)
		f.args = append(f.args, record)
	}
	f.changed.Broadcast()
	f.mu.Unlock()
}

// matching counts the calls that match (all of them, unless where).
// The caller holds f.mu.
func (f *_mockFrame) matching(match func(any) bool, where bool) int64 {
	if !where {
		return f.n
	}
	var n int64
	for _, a := range f.args {
		if match(a) {
			n++
		}
	}
	return n
}

func _mockArgsFn[A any](f *_mockFrame) func() []A {
	return func() []A {
		f.mu.Lock()
		defer f.mu.Unlock()
		out := make([]A, len(f.args))
		for i, a := range f.args {
			out[i] = a.(A)
		}
		return out
	}
}

func _mockExpectFn[A any](f *_mockFrame) func(func(A) bool, bool, int64, int64, int64, string) {
	return func(match func(A) bool, where bool, times, atLeast, atMost int64, at string) {
		e := _mockExpect{match: func(a any) bool { return match(a.(A)) }, where: where, lo: 1, hi: -1, at: at}
		bad := func(why string) {
			panic(fmt.Sprintf("%s: cannot expect (times: %d, atLeast: %d, atMost: %d) of the mock of %s: %s", at, times, atLeast, atMost, f.name, why))
		}
		switch {
		case f.ended.Load() || f.checked.Load():
			panic(fmt.Sprintf("%s: an expectation on the mock of %s came after it ended, so it could never be checked; declare expectations while the mock is in force", at, f.name))
		case times < -1 || atLeast < -1 || atMost < -1:
			bad("bounds cannot be negative (-1 means not given)")
		case times >= 0 && (atLeast >= 0 || atMost >= 0):
			bad("give times, or atLeast and atMost, not both")
		case atLeast >= 0 && atMost >= 0 && atLeast > atMost:
			bad("atLeast is more than atMost")
		case times >= 0:
			e.lo, e.hi = times, times
		case atLeast >= 0 || atMost >= 0:
			e.lo, e.hi = max(atLeast, 0), atMost
		}
		f.mu.Lock()
		f.expects = append(f.expects, e)
		f.mu.Unlock()
	}
}

func _mockWaitFn[A any](f *_mockFrame) func(func(A) bool, bool, int64, int64, string) {
	return func(match func(A) bool, where bool, calls, ms int64, at string) {
		m := func(a any) bool { return match(a.(A)) }
		if calls < 0 || ms < 0 {
			panic(fmt.Sprintf("%s: cannot wait for %d calls of the mock of %s for %dms: neither can be negative", at, calls, f.name, ms))
		}
		ms = min(ms, int64(math.MaxInt64/time.Millisecond))
		wait := time.Duration(ms) * time.Millisecond
		deadline := time.Now().Add(wait)
		timer := time.AfterFunc(wait, func() {
			f.mu.Lock()
			f.changed.Broadcast()
			f.mu.Unlock()
		})
		defer timer.Stop()
		f.mu.Lock()
		defer f.mu.Unlock()
		for {
			n, err := f.matchingSafe(m, where, at)
			if err != "" {
				panic(err)
			}
			if n >= calls {
				return
			}
			if !time.Now().Before(deadline) {
				panic(fmt.Sprintf("%s: waited %dms for %s of the mock of %s, but it answered %d%s", at, ms, _mockCalls(calls, where), f.name, n, f.callList()))
			}
			f.changed.Wait()
		}
	}
}

// _mockCalls is "1 call", "2 matching calls".
func _mockCalls(n int64, where bool) string {
	s := strconv.FormatInt(n, 10)
	if where {
		s += " matching"
	}
	if n == 1 {
		return s + " call"
	}
	return s + " calls"
}

// callList lists the calls answered, for a failure. The caller holds
// f.mu.
func (f *_mockFrame) callList() string {
	if len(f.texts) == 0 {
		return ""
	}
	return ":\n" + strings.Join(f.texts, "\n")
}

// failures lists the expectations the mock did not meet (once: later
// calls give none).
func (f *_mockFrame) failures() (failed []string) {
	if f.checked.Swap(true) {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.expects {
		n, err := f.matchingSafe(e.match, e.where, e.at)
		if err != "" {
			failed = append(failed, err)
			continue
		}
		if n >= e.lo && (e.hi < 0 || n <= e.hi) {
			continue
		}
		var want string
		switch {
		case e.lo == 0 && e.hi == 0:
			want = "no calls"
			if e.where {
				want = "no matching calls"
			}
		case e.lo == e.hi:
			want = "exactly " + _mockCalls(e.lo, e.where)
		case e.hi < 0:
			want = "at least " + _mockCalls(e.lo, e.where)
		case e.lo == 0:
			want = "at most " + _mockCalls(e.hi, e.where)
		default:
			want = fmt.Sprintf("between %d and %s", e.lo, _mockCalls(e.hi, e.where))
		}
		got := strconv.FormatInt(n, 10)
		if e.where {
			got += " matching"
		}
		failed = append(failed, fmt.Sprintf("%s: the mock of %s expected %s, but answered %s", e.at, f.name, want, got))
	}
	if len(failed) > 0 && len(f.texts) > 0 {
		// The calls, once, after the mock's last failure.
		failed[len(failed)-1] += f.callList()
	}
	return failed
}

// matchingSafe is matching, with a matcher's panic as a message. The
// caller holds f.mu.
func (f *_mockFrame) matchingSafe(match func(any) bool, where bool, at string) (n int64, err string) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Sprintf("%s: a matcher given to the mock of %s panicked: %v", at, f.name, r)
		}
	}()
	return f.matching(match, where), ""
}

// _mockVerify fails the test if the mocks, which ended normally, did not
// meet their expectations: every unmet one is reported.
func _mockVerify(frames ...*_mockFrame) {
	var failed []string
	for _, f := range frames {
		failed = append(failed, f.failures()...)
	}
	if len(failed) > 0 {
		panic(strings.Join(failed, "\n"))
	}
}

// endOK ends the mock at the normal end of its block (which then
// checks it, with _mockVerify).
func (f *_mockFrame) endOK() {
	f.ok.Store(true)
	f.end()
}

// restoreOK is restore at the normal end of the block of a mock in a
// scope's body: its scope checks it (finishIn) once its tasks are done.
func (f *_mockFrame) restoreOK() {
	f.ok.Store(true)
	f.restore()
}

// finishIn ends a mock in a scope's body with the scope s, and checks
// its expectations, unless the block or a task of s failed (the test
// then reports that failure only).
func (f *_mockFrame) finishIn(s interface{ failed() bool }) {
	f.finish()
	if !f.ok.Load() || s.failed() {
		f.checked.Store(true)
		return
	}
	_mockVerify(f)
}


func (f *_mockFrame) count() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

func (f *_mockFrame) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.texts...)
}

// _mockPush puts a mock of function id in force on this goroutine, and
// on the goroutines it starts from now on, until the frame ends; parent
// is the mock open around it in the test. Its labels add bork.mock to
// the goroutine's labels, keeping those.
func _mockPush(id int, name, at string, record bool, fn any, parent *_mockFrame) *_mockFrame {
	f := &_mockFrame{parent: parent, id: id, name: name, at: at, fn: fn, record: record, prev: _labels()}
	f.idle = sync.NewCond(&f.mu)
	f.changed = sync.NewCond(&f.mu)
	_labelsSet("bork.mock", strconv.FormatInt(_mockSeq.Add(1), 10))
	_mockFrames.Store(_labels(), f)
	return f
}

// Labels other code sets (a with's published values) keep the frame
// in force.
func init() {
	_labelsMoved = func(from, to unsafe.Pointer) {
		if f, ok := _mockFrames.Load(from); ok {
			_mockFrames.Store(to, f)
		}
	}
}

// end ends the mock at the end of its block: restore, then finish.
func (f *_mockFrame) end() {
	f.restore()
	f.finish()
}

// restore gives the goroutine that pushed the mock its labels from
// before (once), so the goroutines it starts from now on do not see it.
func (f *_mockFrame) restore() {
	if !f.restored.Swap(true) {
		_setLabels(f.prev)
	}
}

// finish ends the mock (once): later calls find the frames outside it,
// and calls of it still running (on other tasks) are waited for, so the
// mock never runs after its block (or its scope's tasks).
func (f *_mockFrame) finish() {
	if f.ended.Swap(true) {
		return
	}
	f.mu.Lock()
	for f.active > 0 {
		f.idle.Wait()
	}
	f.fn = nil // what it captured can go
	f.mu.Unlock()
}
`
