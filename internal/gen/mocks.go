package gen

import (
	"go/ast"
	"go/token"
	"strconv"

	"github.com/GiGurra/bork/internal/check"
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
func (g *gen) mockTargets(info *check.Info) {
	for _, m := range info.Mocks {
		if g.mockIDs == nil {
			g.mockIDs = map[*check.Func]int{}
		}
		if g.mockIDs[m.MockOf] == 0 {
			g.mockIDs[m.MockOf] = len(g.mockIDs) + 1
		}
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
//			_f.called(func() []string { return []string{_show(card), _show(amount)} })
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
	returns := fn.Result != check.Unit && fn.Result != check.Never
	result := func(call ast.Expr) []ast.Stmt {
		if returns {
			return []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{call}}}
		}
		return []ast.Stmt{&ast.ExprStmt{X: call}, &ast.ReturnStmt{}}
	}
	from, f := ast.NewIdent("_from"), ast.NewIdent("_f")
	next := &ast.FuncDecl{
		Name: ast.NewIdent("_next_" + goName),
		Type: g.mockFuncType(fn, params),
	}
	next.Type.Params.List = append([]*ast.Field{{Names: []*ast.Ident{from}, Type: &ast.StarExpr{X: ast.NewIdent("_mockFrame")}}}, next.Type.Params.List...)
	mockCall := &ast.CallExpr{
		Fun:  &ast.TypeAssertExpr{X: &ast.SelectorExpr{X: f, Sel: ast.NewIdent("fn")}, Type: g.mockFuncType(fn, nil)},
		Args: args,
	}
	texts := &ast.FuncLit{
		Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: &ast.ArrayType{Elt: ast.NewIdent("string")}}}}},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{&ast.CompositeLit{Type: &ast.ArrayType{Elt: ast.NewIdent("string")}, Elts: shown}}}}},
	}
	found := append([]ast.Stmt{
		&ast.DeferStmt{Call: &ast.CallExpr{Fun: &ast.SelectorExpr{X: f, Sel: ast.NewIdent("done")}}},
		&ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: f, Sel: ast.NewIdent("called")}, Args: []ast.Expr{texts}}},
	}, result(mockCall)...)
	body := []ast.Stmt{&ast.IfStmt{
		Init: define(f, &ast.CallExpr{Fun: ast.NewIdent("_mockFind"), Args: []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(id)}, from}}),
		Cond: &ast.BinaryExpr{X: f, Op: token.NEQ, Y: ast.NewIdent("nil")},
		Body: &ast.BlockStmt{List: found},
	}}
	real := &ast.CallExpr{Fun: ast.NewIdent("_real_" + goName), Args: args}
	if returns {
		body = append(body, &ast.ReturnStmt{Results: []ast.Expr{real}})
	} else {
		body = append(body, &ast.ExprStmt{X: real})
	}
	next.Body = &ast.BlockStmt{List: body}
	dispatch := &ast.FuncDecl{Name: ast.NewIdent(goName), Type: g.mockFuncType(fn, params)}
	call := &ast.CallExpr{Fun: next.Name, Args: append([]ast.Expr{&ast.CallExpr{Fun: ast.NewIdent("_mockCurrent")}}, args...)}
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
//	_mf1 = _mockPush(1, "fetch", true, func(url string) any { ... }, nil)
//	defer _mf1.end()
//	calls := Mock{countFn: _mf1.count, callsFn: _mf1.calls}
//
// A mock directly in a scope's body ends with the scope instead, after
// its tasks: s.Defer(_mf1.finish), and its block's end (or a panic)
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
	savedResult, savedScopes, savedOwners, savedMocks := g.fnResult, g.openScopes, g.blockOwners, g.openMocks
	g.fnResult, g.openScopes, g.blockOwners, g.openMocks = m.Target.Result, nil, nil, nil
	g.mockBodies = append(g.mockBodies, mockBody{target: m.Target, frame: frame})
	var body []ast.Stmt
	if m.Target.Result == check.Unit {
		body = g.blockInto(m.Func.Body, sink{})
	} else {
		body = g.blockInto(m.Func.Body, sink{ret: true})
	}
	g.mockBodies = g.mockBodies[:len(g.mockBodies)-1]
	g.fnResult, g.openScopes, g.blockOwners, g.openMocks = savedResult, savedScopes, savedOwners, savedMocks
	lit := &ast.FuncLit{Type: g.mockFuncType(m.Target, names), Body: &ast.BlockStmt{List: body}}
	var parent ast.Expr = ast.NewIdent("nil")
	if n := len(g.openMocks); n > 0 {
		parent = g.openMocks[n-1].frame
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
			ast.NewIdent(strconv.FormatBool(m.Var != nil)),
			lit,
			parent,
		}}),
		&ast.DeferStmt{Call: &ast.CallExpr{Fun: &ast.SelectorExpr{X: frame, Sel: ast.NewIdent(deferred)}}},
	}
	if scope != nil {
		stmts = append(stmts, &ast.ExprStmt{X: &ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: scope, Sel: ast.NewIdent("Defer")},
			Args: []ast.Expr{&ast.SelectorExpr{X: frame, Sel: ast.NewIdent("finish")}},
		}})
	}
	g.openMocks = append(g.openMocks, openMock{frame: frame, depth: len(g.openScopes), scoped: scope != nil})
	if m.Var != nil {
		handle := &ast.CompositeLit{Type: g.goType(m.Var.Type), Elts: []ast.Expr{
			&ast.KeyValueExpr{Key: ast.NewIdent("countFn"), Value: &ast.SelectorExpr{X: frame, Sel: ast.NewIdent("count")}},
			&ast.KeyValueExpr{Key: ast.NewIdent("callsFn"), Value: &ast.SelectorExpr{X: frame, Sel: ast.NewIdent("calls")}},
		}}
		stmts = append(stmts, define(name(m.Var.Name), handle))
		if m.Var.Unused {
			stmts = append(stmts, assign(ast.NewIdent("_"), name(m.Var.Name)))
		}
	}
	return stmts
}

// endMocks ends the mocks pushed since the first `from` of openMocks
// (innermost first), and forgets them.
func (g *gen) endMocks(from int) []ast.Stmt {
	var out []ast.Stmt
	for i := len(g.openMocks) - 1; i >= from; i-- {
		out = append(out, endMock(g.openMocks[i]))
	}
	return out
}

// endMock ends a mock at the end of its block; a scoped one only
// restores the labels (its scope ends it).
func endMock(m openMock) ast.Stmt {
	method := "end"
	if m.scoped {
		method = "restore"
	}
	return &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: m.frame, Sel: ast.NewIdent(method)}}}
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

// nextCall calls fn as it was before the mock of frame: the outer mock
// or the real function.
func (g *gen) nextCall(fn *check.Func, frame *ast.Ident, args []ast.Expr) ast.Expr {
	return &ast.CallExpr{
		Fun:  ast.NewIdent("_next_" + g.funcName(fn).Name),
		Args: append([]ast.Expr{&ast.SelectorExpr{X: frame, Sel: ast.NewIdent("parent")}}, args...),
	}
}

// nextRef is fn as a value, as it was before the mock of frame.
// The ambient values it needs are those in force where it is named.
func (g *gen) nextRef(fn *check.Func, frame *ast.Ident, needs []ast.Expr) ast.Expr {
	var names []*ast.Ident
	var args []ast.Expr
	for i := range fn.Params {
		p := ast.NewIdent("_a" + strconv.Itoa(i))
		names = append(names, p)
		args = append(args, p)
	}
	call := g.nextCall(fn, frame, append(args, needs...))
	var body ast.Stmt = &ast.ExprStmt{X: call}
	if fn.Result != check.Unit && fn.Result != check.Never {
		body = &ast.ReturnStmt{Results: []ast.Expr{call}}
	}
	return &ast.FuncLit{Type: g.funcType(&check.FuncType{Params: fn.Params, Result: fn.Result}, names), Body: &ast.BlockStmt{List: []ast.Stmt{body}}}
}

// mockRuntime keeps the mocks in force: a chain of frames, one per
// mock statement, each pointing to the frame in force where it was
// pushed. A frame is attached to goroutines through Go's profiler
// labels: it has a label set of its own (the labels before it, and
// bork.mock), and _mockFrames maps that label set to it. The Go
// runtime copies a goroutine's labels to every goroutine it starts, so
// tasks, servers, and goroutines started by Go code see the mocks in
// force where they were started.
const mockRuntime = `package main

import (
	"context"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type _mockFrame struct {
	parent *_mockFrame
	id     int    // the mocked function's
	name   string // as the mock statement wrote it
	fn     any    // the mock: a func of the function's Go type
	record bool   // whether calls are recorded as text
	ctx      context.Context // the frame's labels
	prev     context.Context // the labels to restore when it ends
	restored atomic.Bool
	ended    atomic.Bool

	mu     sync.Mutex
	idle   *sync.Cond
	active int // calls running now
	n      int64
	texts  []string
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
func (f *_mockFrame) called(args func() []string) {
	text := ""
	if f.record {
		text = f.name + "(" + strings.Join(args(), ", ") + ")"
	}
	f.mu.Lock()
	f.n++
	if f.record {
		f.texts = append(f.texts, text)
	}
	f.mu.Unlock()
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
func _mockPush(id int, name string, record bool, fn any, parent *_mockFrame) *_mockFrame {
	_labelsCheck.Do(_labelsCheckLayout)
	base := context.Background()
	if p := _labels(); p != nil {
		if v, ok := _mockFrames.Load(p); ok {
			base = v.(*_mockFrame).ctx
		} else {
			base = pprof.WithLabels(base, pprof.Labels(_labelList(p)...))
		}
	}
	f := &_mockFrame{parent: parent, id: id, name: name, fn: fn, record: record, prev: base}
	f.idle = sync.NewCond(&f.mu)
	f.ctx = pprof.WithLabels(base, pprof.Labels("bork.mock", strconv.FormatInt(_mockSeq.Add(1), 10)))
	pprof.SetGoroutineLabels(f.ctx)
	_mockFrames.Store(_labels(), f)
	return f
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
		pprof.SetGoroutineLabels(f.prev)
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
