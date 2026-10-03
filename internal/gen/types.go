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
//   - Map[K, V] becomes the runtime's _Map[K, V], never modified once built
//   - a function type becomes a Go func type
//   - a type parameter becomes a Go type parameter
func (g *gen) goType(t check.Type) ast.Expr {
	switch t := t.(type) {
	case *check.TypeParam:
		return name(t.Name)
	case *check.List:
		return &ast.ArrayType{Elt: g.goType(t.Elem)}
	case *check.Map:
		g.usesMap = true
		return &ast.IndexListExpr{X: ast.NewIdent("_Map"), Indices: []ast.Expr{g.goType(t.Key), g.goType(t.Value)}}
	case *check.FuncType:
		return g.funcType(t, nil)
	case *check.Record:
		g.usedTypes[baseOf(t)] = true
		return g.instantiated(typeName(t.Name, t.Pkg), t)
	case *check.Sealed:
		g.usedTypes[baseOf(t)] = true
		return g.instantiated(typeName(t.Name, t.Pkg), t)
	case *check.Union:
		for _, m := range t.Members {
			g.goType(m) // the members' declarations are needed
		}
		return ast.NewIdent("any")
	case *check.Opaque:
		g.usedTypes[t] = true
		g.usesOpaque = true
		return typeName(t.Name, t.Pkg)
	case *check.Resource:
		g.usedTypes[t] = true
		return typeName(t.Name, t.Pkg)
	}
	if t == check.Bytes {
		g.usesBytes = true
		return ast.NewIdent("_Bytes")
	}
	if t == check.Scope {
		g.usesScopes = true
		return &ast.StarExpr{X: ast.NewIdent("_Scope")}
	}
	if t == check.Unit {
		// Only as a union member: the value of `Unit | IoError` that
		// says nothing went wrong.
		g.usesUnit = true
		return ast.NewIdent("_Unit")
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

// typeName maps a declared bork type name to its Go name. Types of
// imported packages get the package's prefix.
func typeName(s string, pkg *check.Package) *ast.Ident {
	if pkg != nil && pkg.GoPrefix != "" {
		return ast.NewIdent(pkg.GoPrefix + s)
	}
	return name(s)
}

// variantType is the Go struct type of a sealed type's variant.
func (g *gen) variantType(v *check.Variant) ast.Expr {
	g.usedTypes[baseOf(v.Parent)] = true
	return g.instantiated(ast.NewIdent(typeName(v.Parent.Name, v.Parent.Pkg).Name+"_"+v.Name), v.Parent)
}

func markerMethod(t *check.Sealed) string { return "is" + typeName(t.Name, t.Pkg).Name }

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
		case *check.Opaque:
			return true
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
	case *check.Opaque:
		return g.opaqueDecl(t.Name, t.Pkg, t.GoType, false)
	case *check.Resource:
		if t.GoType != nil {
			return g.opaqueDecl(t.Name, t.Pkg, t.GoType, true)
		}
		// A handle that unsafe go code fills in, and the owner that
		// closes it once the last scope it is attached to closes:
		// File{handle: f, owner: s.Own(func() { f.Close() })}.
		g.usesScopes = true
		src := fmt.Sprintf("package main\ntype %[1]s struct{ handle any; owner *_Owner }\nfunc (%[1]s) String() string { return \"<%[2]s>\" }\nfunc (r %[1]s) _ownerOf() *_Owner { return r.owner }\nfunc (r %[1]s) _borkRebind(s *_Scope) { if h, ok := r.handle.(interface { _borkRebind(*_Scope) }); ok { h._borkRebind(s) } }\n", typeName(t.Name, t.Pkg).Name, t.Name)
		f, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
		if err != nil {
			panic(err)
		}
		decls = append(decls, f.Decls...)
	case *check.Record:
		if t.GoMirror != nil {
			decls = append(decls, g.mirrorDecl(t)...)
		}
		recv := g.instantiated(typeName(t.Name, t.Pkg), t)
		decls = append(decls, g.structDecl(typeName(t.Name, t.Pkg), t.TypeParams, t.Fields))
		decls = append(decls, g.showStringMethod(recv, t, t.Name, t.Fields, true))
		decls = append(decls, g.showMethods(recv, t)...)
		decls = append(decls, g.valueMethods(recv, t.Fields)...)
	case *check.Sealed:
		// The interface's marker method mentions the type parameters, so
		// that Option[int64] and Option[string] are different types.
		marker := markerMethod(t)
		markerType := &ast.FuncType{Params: &ast.FieldList{}}
		for _, p := range t.TypeParams {
			markerType.Params.List = append(markerType.Params.List, &ast.Field{Type: name(p.Name)})
		}
		decls = append(decls, &ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{
			Name:       typeName(t.Name, t.Pkg),
			TypeParams: typeParamList(t.TypeParams),
			Type: &ast.InterfaceType{Methods: &ast.FieldList{List: []*ast.Field{{
				Names: []*ast.Ident{ast.NewIdent(marker)},
				Type:  markerType,
			}}}},
		}}})
		for _, v := range t.Variants {
			vname := ast.NewIdent(typeName(t.Name, t.Pkg).Name + "_" + v.Name)
			recv := g.instantiated(vname, t)
			decls = append(decls, g.structDecl(vname, t.TypeParams, v.Fields))
			decls = append(decls, &ast.FuncDecl{
				Recv: &ast.FieldList{List: []*ast.Field{{Type: recv}}},
				Name: ast.NewIdent(marker),
				Type: markerType,
				Body: &ast.BlockStmt{},
			})
			decls = append(decls, g.showStringMethod(recv, t, t.Name+"."+v.Name, v.Fields, false))
			decls = append(decls, g.showMethods(recv, t)...)
			decls = append(decls, g.valueMethods(recv, v.Fields)...)
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
			show := g.showValue(&ast.SelectorExpr{X: ast.NewIdent("v"), Sel: name(f.Name)}, f.Type)
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

func _assertEqual[T any](actual, expected T, at string) {
	if !_equal(actual, expected) {
		panic(at + ": expected " + _show(expected) + ", got " + _show(actual))
	}
}
`

const testRuntime = `package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

type _test struct {
	name string
	snap string // the base name of the test's snapshot files
	run  func() // nil for a test that cannot run; name says why
}

// _skip is the panic of a test that finds it cannot run.
type _skip struct{ why string }

// The test running now, for assertSnapshot.
var _tests struct {
	sync.Mutex
	current   *_test
	snapshots int      // assertSnapshot calls so far in this test
	written   []string // snapshot files written in this test
}

// _runTests runs the tests, each until it fails (panics), and reports.
func _runTests(tests []_test) {
	failed, skipped := 0, 0
	for i, t := range tests {
		if t.run == nil {
			skipped++
			fmt.Printf("skip  %s\n", t.name)
			continue
		}
		_tests.Lock()
		_tests.current, _tests.snapshots, _tests.written = &tests[i], 0, nil
		_tests.Unlock()
		if msg, skip := _runTest(t.run); skip {
			skipped++
			fmt.Printf("skip  %s (%s)\n", t.name, msg)
		} else if msg != "" {
			failed++
			fmt.Printf("FAIL  %s\n      %s\n", t.name, _indent(msg))
		} else {
			fmt.Printf("ok    %s\n", t.name)
		}
		_tests.Lock()
		for _, path := range _tests.written {
			fmt.Printf("      wrote %s\n", path)
		}
		_tests.current = nil
		_tests.Unlock()
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

// _indent indents a failure message's lines after the first.
func _indent(msg string) string {
	lines := strings.Split(msg, "\n")
	for i := 1; i < len(lines); i++ {
		if lines[i] != "" {
			lines[i] = "      " + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

func _runTest(run func()) (msg string, skip bool) {
	defer func() {
		if r := recover(); r != nil {
			if s, ok := r.(_skip); ok {
				msg, skip = s.why, true
			} else {
				msg = fmt.Sprint(r)
			}
		}
	}()
	run()
	return "", false
}
`

// snapshotRuntime is assertSnapshot. The driver passes the snapshot
// directory in $BORK_SNAPSHOTS, and sets $BORK_UPDATE_SNAPSHOTS for
// bork test --update.
const snapshotRuntime = `package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// _assertSnapshot compares text with the running test's next snapshot
// file: <test>.snap for its first assertSnapshot, then <test>.2.snap,
// and so on. A file holds the text and a newline (\r\n line endings
// read as \n). When updating, it writes the file instead if it is
// missing or different.
func _assertSnapshot(text, at string) {
	_tests.Lock()
	t := _tests.current
	if t == nil {
		_tests.Unlock()
		panic(at + ": assertSnapshot works only while a test runs")
	}
	if t.snap == "" {
		_tests.Unlock()
		panic(at + ": assertSnapshot works only in tests, not in rules or property tests")
	}
	_tests.snapshots++
	file := t.snap + ".snap"
	if _tests.snapshots > 1 {
		file = fmt.Sprintf("%s.%d.snap", t.snap, _tests.snapshots)
	}
	_tests.Unlock()
	path := filepath.Join(os.Getenv("BORK_SNAPSHOTS"), file)
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		panic(at + ": " + err.Error())
	}
	found := err == nil
	old = []byte(strings.ReplaceAll(string(old), "\r\n", "\n"))
	if found && string(old) == text+"\n" {
		return
	}
	if os.Getenv("BORK_UPDATE_SNAPSHOTS") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			panic(at + ": " + err.Error())
		}
		if err := os.WriteFile(path, []byte(text+"\n"), 0o644); err != nil {
			panic(at + ": " + err.Error())
		}
		_tests.Lock()
		_tests.written = append(_tests.written, path)
		_tests.Unlock()
		return
	}
	if !found {
		panic(at + ": no snapshot " + path + " yet (bork test --update writes it); the value is:\n" + text)
	}
	panic(at + ": snapshot " + path + " does not match (- snapshot, + actual; bork test --update rewrites it):\n" +
		_snapshotDiff(strings.TrimSuffix(string(old), "\n"), text))
}

// _snapshotDiff shows the lines that differ between old and new, with
// two lines of context around them.
func _snapshotDiff(old, new string) string {
	a, b := strings.Split(old, "\n"), strings.Split(new, "\n")
	// The lines both start and end with are kept as they are, and only
	// the middle is diffed.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	// lcs[i][j] is the length of the longest common subsequence of
	// ma[i:] and mb[j:]. A middle too large to compare line by line is
	// shown as removed and added.
	if len(ma)*len(mb) > 10000000 {
		ma, mb = nil, nil
	}
	lcs := make([][]int, len(ma)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(mb)+1)
	}
	for i := len(ma) - 1; i >= 0; i-- {
		for j := len(mb) - 1; j >= 0; j-- {
			if ma[i] == mb[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var lines []string
	var changed []bool
	add := func(prefix, line string, change bool) {
		if line == "" {
			prefix = strings.TrimSpace(prefix)
		}
		lines = append(lines, prefix+line)
		changed = append(changed, change)
	}
	for _, line := range a[:pre] {
		add("  ", line, false)
	}
	if ma == nil && mb == nil {
		for _, line := range a[pre : len(a)-suf] {
			add("- ", line, true)
		}
		for _, line := range b[pre : len(b)-suf] {
			add("+ ", line, true)
		}
	}
	i, j := 0, 0
	for i < len(ma) || j < len(mb) {
		switch {
		case i < len(ma) && j < len(mb) && ma[i] == mb[j]:
			add("  ", ma[i], false)
			i, j = i+1, j+1
		case i < len(ma) && (j == len(mb) || lcs[i+1][j] >= lcs[i][j+1]):
			add("- ", ma[i], true)
			i++
		default:
			add("+ ", mb[j], true)
			j++
		}
	}
	for _, line := range a[len(a)-suf:] {
		add("  ", line, false)
	}
	// Runs of two or more lines far from a change are left out.
	const context = 2
	shown := make([]bool, len(lines))
	for k := range lines {
		for d := max(0, k-context); d <= min(len(lines)-1, k+context); d++ {
			shown[k] = shown[k] || changed[d]
		}
	}
	var out []string
	for k := 0; k < len(lines); {
		end := k
		for end < len(lines) && !shown[end] {
			end++
		}
		switch {
		case end-k >= 2:
			out = append(out, "  ...")
			k = end
		default:
			out = append(out, lines[k])
			k++
		}
	}
	return strings.Join(out, "\n")
}
`

const scopeRuntime = `package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"sync"
	"sync/atomic"
	"time"
)

// _Scope is a bork scope: tasks it waits for, and finalizers that run,
// last registered first, when it closes. Its context is cancelled by
// cancel, by a deadline, by a task that panics, by a panic in its
// block, and with the scope it is nested in.
type _Scope struct {
	mu         sync.Mutex
	finalizers []func()
	running    sync.WaitGroup
	tasks      []*_task
	ctx        context.Context
	cancel     context.CancelCauseFunc
	// taskTimeout is how long close waits for the scope's tasks once it
	// has cancelled them, and finalizerTimeout how long it waits for
	// each finalizer; 0 waits for as long as it takes (the default).
	// Tasks and finalizers that take longer are orphaned: left running,
	// while the scope goes on closing.
	taskTimeout      time.Duration
	finalizerTimeout time.Duration
	// logFailures logs the failures at the scope's end instead of
	// raising them.
	logFailures bool
	closed      bool
	name    string
}

// Root scopes inherit process-signal cancellation; nested scopes inherit
// their parent's cancellation. The handler lives for the program's lifetime.
var _mainContext, _ = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

// _newScope opens the scope name inside parent (nil for none).
func _newScope(parent *_Scope, name string) *_Scope {
	ctx := _mainContext
	if parent != nil {
		ctx = parent.ctx
	}
	s := _scopeWith(ctx)
	s.name = name
	return s
}

// _scopeWith opens a scope that is cancelled with ctx.
func _scopeWith(ctx context.Context) *_Scope {
	s := &_Scope{name: "(a scope)"}
	s.ctx, s.cancel = context.WithCancelCause(ctx)
	return s
}

var (
	_errCancelled  = errors.New("cancelled")
	_errScopeEnded = errors.New("the scope ended")
)

// _cancelReason says why a scope was cancelled, or "" if it was not.
func (s *_Scope) _cancelReason() string {
	if s.ctx.Err() == nil {
		return ""
	}
	return context.Cause(s.ctx).Error()
}

// _sleep waits d, or until the scope is cancelled; it reports whether
// it slept the whole time.
func (s *_Scope) _sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-s.ctx.Done():
		return false
	}
}

// _Owner closes a resource once every scope it is attached to has
// closed: the scope it was opened in, and those attach added.
type _Owner struct {
	mu      sync.Mutex
	count   int
	closeFn func()
}

// Own makes closeFn close a resource when the scope closes, or, if the
// resource is attached to other scopes too, when the last of them does.
func (s *_Scope) Own(closeFn func()) *_Owner {
	o := &_Owner{count: 1, closeFn: closeFn}
	s.Defer(o.release)
	return o
}

// attach keeps the resource open until s closes too. It reports false
// if the resource is already closed.
func (o *_Owner) attach(s *_Scope) bool {
	o.mu.Lock()
	if o.count == 0 {
		o.mu.Unlock()
		return false
	}
	o.count++
	o.mu.Unlock()
	s.Defer(o.release)
	return true
}

func (o *_Owner) release() {
	o.mu.Lock()
	o.count--
	last := o.count == 0
	o.mu.Unlock()
	if last {
		o.closeFn()
	}
}

// _task is a goroutine a scope started (spawn, launch).
type _task struct {
	done     chan struct{}
	result   any
	failure  any
	reported atomic.Bool
}

// Defer registers f to run when the scope closes. Resources opened in
// unsafe go code register their finalizers with it.
func (s *_Scope) Defer(f func()) {
	s.mu.Lock()
	s.finalizers = append(s.finalizers, f)
	s.mu.Unlock()
}

// Go runs work on a goroutine the scope waits for before it closes. A
// task that panics cancels the scope, so its siblings stop too.
func (s *_Scope) Go(work func() any) *_task {
	t := &_task{done: make(chan struct{})}
	s.mu.Lock()
	s.tasks = append(s.tasks, t)
	s.mu.Unlock()
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer close(t.done)
		defer func() {
			if r := recover(); r != nil {
				t.failure = r
				s.cancel(errors.New("a task failed"))
				s.mu.Lock()
				orphan := s.closed && s.tasks == nil
				s.mu.Unlock()
				if orphan && !t.reported.Swap(true) {
					slog.Error("bork: an orphaned task failed", "scope", s.name, "failure", fmt.Sprint(r))
				}
			}
		}()
		t.result = work()
	}()
	return t
}

// Await waits for the task, and gives its result; a task that panicked
// panics again here.
func (t *_task) Await() any {
	<-t.done
	if t.failure != nil {
		t.reported.Store(true)
		panic(t.failure)
	}
	return t.result
}

// close ends the scope, however its block ended: it cancels the scope,
// so its tasks learn that it is ending (how they stop, quickly or with
// cleanup of their own, is up to them), waits for them, and then runs
// the finalizers. A task that panicked without being awaited panics the
// scope's routine. A scope is closed once: when its block ends, or by
// abort when the block panics.
//
// Every finalizer runs, even when others fail. Failures (finalizers that
// panicked, and tasks that panicked without being awaited) are raised as
// one panic once the scope is closed.
func (s *_Scope) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel(_errScopeEnded)
	s.waitForTasks()
	var failures []any
	for {
		s.mu.Lock()
		if len(s.finalizers) == 0 {
			s.mu.Unlock()
			break
		}
		f := s.finalizers[len(s.finalizers)-1]
		s.finalizers = s.finalizers[:len(s.finalizers)-1]
		s.mu.Unlock()
		if r := s.finalize(f); r != nil {
			failures = append(failures, r)
		}
	}
	s.mu.Lock()
	tasks := s.tasks
	s.tasks = nil
	s.mu.Unlock()
	var taskFailures []any
	for _, t := range tasks {
		if t.failure != nil && !t.reported.Swap(true) {
			taskFailures = append(taskFailures, t.failure)
		}
	}
	failures = append(taskFailures, failures...)
	if s.logFailures {
		for _, f := range failures {
			slog.Error("bork: a failure while the scope closed", "scope", s.name, "failure", fmt.Sprint(f))
		}
		return
	}
	switch len(failures) {
	case 0:
	case 1:
		panic(failures[0])
	default:
		msg := fmt.Sprint(failures[0])
		for _, f := range failures[1:] {
			msg += "; and then: " + fmt.Sprint(f)
		}
		panic(msg)
	}
}

// finalize runs one finalizer under the scope's policy, and gives what
// it panicked with, if it did.
func (s *_Scope) finalize(f func()) (failure any) {
	run := func() (failure any) {
		defer func() { failure = recover() }()
		f()
		return nil
	}
	if s.finalizerTimeout <= 0 {
		return run()
	}
	done := make(chan any, 1)
	go func() { done <- run() }()
	t := time.NewTimer(s.finalizerTimeout)
	defer t.Stop()
	select {
	case r := <-done:
		return r
	case <-t.C:
		slog.Warn("bork: a finalizer did not finish in time; it is orphaned, and the scope goes on closing", "scope", s.name, "timeout", s.finalizerTimeout)
		return nil
	}
}

// waitForTasks waits for the scope's tasks, or, with a task timeout,
// until it passes: tasks still running then are orphaned (they keep
// running, and may find the scope's resources closed).
func (s *_Scope) waitForTasks() {
	if s.taskTimeout <= 0 {
		s.running.Wait()
		return
	}
	done := make(chan struct{})
	go func() {
		s.running.Wait()
		close(done)
	}()
	t := time.NewTimer(s.taskTimeout)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		slog.Warn("bork: tasks did not stop in time after the scope ended; they are orphaned, and the scope goes on closing", "scope", s.name, "timeout", s.taskTimeout)
	}
}

// abort is deferred when a scope opens, so that a panic in its block
// closes it too. After a close, it does nothing.
func (s *_Scope) abort() {
	s.close()
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
	if v, ok := x.(interface{ _borkShow() string }); ok { return v._borkShow() }
	if s, ok := x.(string); ok {
		return strconv.Quote(s)
	}
	return _str(x)
}

// _str renders a value as println and toString show it. Floats always
// look like floats (3.0, not 3), and use an exponent only when very
// large or small.
func _strOf[T any](x T) string { return _str(x) }

func _str(x any) string {
	if v, ok := x.(interface{ _borkShow() string }); ok { return v._borkShow() }
	switch x := x.(type) {
	case float64:
		return _fmtFloat(x, 64)
	case float32:
		return _fmtFloat(float64(x), 32)
	case fmt.Stringer:
		return x.String()
	case string:
		return x
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

func _showList[T any](xs []T, show func(T) string) string {
	parts := make([]string, len(xs))
	for i, x := range xs { parts[i] = show(x) }
	return "[" + strings.Join(parts, ", ") + "]"
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
	if g.usesDecodeSchema {
		src = append(src, decodeSchemaHelpers)
	}
	if g.usesAssert {
		g.usesShow = true
		g.usesEqual = true
		src = append(src, assertRuntime)
	}
	if g.usesTests {
		src = append(src, testRuntime)
	}
	if g.usesSnaps {
		src = append(src, snapshotRuntime)
	}
	if g.usesProps {
		src = append(src, propertyRuntime)
	}
	if g.usesScopes {
		src = append(src, scopeRuntime, scopeHelpers)
	}
	if g.usesIoFailure {
		src = append(src, ioFailureHelpers)
	}
	if g.usesBytes {
		src = append(src, bytesRuntime)
	}
	if g.usesBind {
		src = append(src, bindRuntime)
	}
	if g.usesUnit {
		src = append(src, unitRuntime)
	}
	if g.usesMap {
		g.usesHash = true
		g.usesShow = true
		g.usesEqual = true
		src = append(src, mapRuntime, mapHelpers)
	}
	if g.usesHash {
		src = append(src, hashRuntime)
	}
	if g.usesEqual {
		src = append(src, equalRuntime)
	}
	if g.usesDerive {
		src = append(src, deriveRuntime)
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

// needsDeepEqual reports whether values of type t need _equal rather
// than Go's ==: they may hold lists, or are of a type parameter.
func needsDeepEqual(t check.Type, seen map[check.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	if t == check.Bytes {
		return true
	}
	fields := func(fs []*check.Field) bool {
		for _, f := range fs {
			if needsDeepEqual(f.Type, seen) {
				return true
			}
		}
		return false
	}
	switch t := t.(type) {
	case *check.List, *check.Map, *check.TypeParam:
		return true
	case *check.Record:
		return fields(t.Fields)
	case *check.Sealed:
		for _, v := range t.Variants {
			if fields(v.Fields) {
				return true
			}
		}
	case *check.Union:
		for _, m := range t.Members {
			if needsDeepEqual(m, seen) {
				return true
			}
		}
	}
	return false
}

// equalRuntime is structural equality for values Go's == cannot compare:
// lists count as equal when their elements are (a nil list is empty).
const equalRuntime = `package main

import (
	"reflect"
	"strings"
)

func _equal(a, b any) bool {
	if x, ok := a.(interface{ _borkEqual(any) bool }); ok {
		return x._borkEqual(b)
	}
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool); return ok && x == y
	case string:
		y, ok := b.(string); return ok && x == y
	case int64:
		y, ok := b.(int64); return ok && x == y
	case int32:
		y, ok := b.(int32); return ok && x == y
	case int16:
		y, ok := b.(int16); return ok && x == y
	case int8:
		y, ok := b.(int8); return ok && x == y
	case uint64:
		y, ok := b.(uint64); return ok && x == y
	case uint32:
		y, ok := b.(uint32); return ok && x == y
	case uint16:
		y, ok := b.(uint16); return ok && x == y
	case uint8:
		y, ok := b.(uint8); return ok && x == y
	case float64:
		y, ok := b.(float64); return ok && x == y
	case float32:
		y, ok := b.(float32); return ok && x == y
	}
	return _equalValues(reflect.ValueOf(a), reflect.ValueOf(b))
}

func _equalList[T any](a, b []T, equal func(T, T) bool) bool {
	if len(a) != len(b) { return false }
	for i := range a {
		if !equal(a[i], b[i]) { return false }
	}
	return true
}

func _equalOf[T any](a, b T) bool { return _equal(a, b) }

func _equalValues(x, y reflect.Value) bool {
	if !x.IsValid() || !y.IsValid() {
		return x.IsValid() == y.IsValid()
	}
	if x.Type() != y.Type() {
		return false
	}
	if x.Kind() == reflect.Struct && x.CanInterface() {
		if v, ok := x.Interface().(interface{ _borkEqual(any) bool }); ok && y.CanInterface() {
			return v._borkEqual(y.Interface())
		}
	}
	switch x.Kind() {
	case reflect.Interface:
		if x.IsNil() || y.IsNil() {
			return x.IsNil() == y.IsNil()
		}
		return _equalValues(x.Elem(), y.Elem())
	case reflect.Slice:
		if x.Len() != y.Len() {
			return false
		}
		for i := 0; i < x.Len(); i++ {
			if !_equalValues(x.Index(i), y.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Struct:
		if strings.HasPrefix(x.Type().Name(), "_Map[") {
			return _equalMapHook(x, y)
		}
		for i := 0; i < x.NumField(); i++ {
			if !_equalValues(x.Field(i), y.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Bool:
		return x.Bool() == y.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return x.Int() == y.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return x.Uint() == y.Uint()
	case reflect.Float32, reflect.Float64:
		return x.Float() == y.Float()
	case reflect.String:
		return x.String() == y.String()
	}
	return false
}

// _equalMapHook compares maps, when the program has any.
var _equalMapHook func(x, y reflect.Value) bool
`

// mapRuntime is the representation of Map[K, V], a persistent map:
// put and remove copy only the path to what changed, and share the
// rest with the map they started from. It is a hash array mapped trie
// (HAMT) from each key to its position, and a persistent vector of the
// entries in the order their keys were first added (as Scala's
// VectorMap). Removing leaves a hole in the vector; the map is rebuilt
// when holes outnumber entries.
const mapRuntime = `package main

import (
	"cmp"
	"math/bits"
	"reflect"
	"slices"
	"strings"
)

// _Map is a bork map. The zero value is the empty map. Its core keeps
// the entries: in the order their keys were added (_mapCore, the
// default), sorted by key (_sortedCore), or in no order (_hashCore).
type _Map[K, V any] struct{ core _mapImpl }

type _mapImpl interface {
	find(k any) *_mapEntry
	with(k, v any) _mapImpl
	without(k any) _mapImpl
	size() int
	each(f func(e *_mapEntry) bool)
	mapVals(f func(v any) any) _mapImpl
	// empty is an empty map of the same kind.
	empty() _mapImpl
}

func _mapOf[K, V any](keys []K, vals []V) _Map[K, V] {
	b := &_mapCore{}
	for i, k := range keys {
		b = b.put(k, vals[i], true)
	}
	return _Map[K, V]{b}
}

func (m _Map[K, V]) impl() _mapImpl {
	if m.core == nil {
		return (*_mapCore)(nil)
	}
	return m.core
}

// _mapSortedBy is m kept sorted by less.
func _mapSortedBy[K, V any](m _Map[K, V], less func(a, b K) bool) _Map[K, V] {
	var out _mapImpl = &_sortedCore{less: func(a, b any) bool {
		x, _ := a.(K)
		y, _ := b.(K)
		return less(x, y)
	}}
	m.impl().each(func(e *_mapEntry) bool {
		out = out.with(e.key, e.val)
		return true
	})
	return _Map[K, V]{out}
}

// _mapInOrder is m kept in the order its keys are added, starting
// with its current order.
func _mapInOrder[K, V any](m _Map[K, V]) _Map[K, V] {
	if _, ok := m.impl().(*_mapCore); ok {
		return m
	}
	b := &_mapCore{}
	m.impl().each(func(e *_mapEntry) bool {
		b = b.put(e.key, e.val, true)
		return true
	})
	return _Map[K, V]{b}
}

func (m _Map[K, V]) get(k K) (V, bool) {
	e := m.impl().find(k)
	if e == nil {
		var zero V
		return zero, false
	}
	v, _ := e.val.(V)
	return v, true
}

func (m _Map[K, V]) put(k K, v V) _Map[K, V] { return _Map[K, V]{m.impl().with(k, v)} }

func (m _Map[K, V]) remove(k K) _Map[K, V] { return _Map[K, V]{m.impl().without(k)} }

func (m _Map[K, V]) len() int { return m.impl().size() }

// each calls f with the entries in order, until it returns false.
func (m _Map[K, V]) each(f func(k K, v V) bool) {
	m.impl().each(func(e *_mapEntry) bool {
		k, _ := e.key.(K)
		v, _ := e.val.(V)
		return f(k, v)
	})
}

func (m _Map[K, V]) keys() []K {
	out := make([]K, 0, m.len())
	m.each(func(k K, _ V) bool { out = append(out, k); return true })
	return out
}

func (m _Map[K, V]) vals() []V {
	out := make([]V, 0, m.len())
	m.each(func(_ K, v V) bool { out = append(out, v); return true })
	return out
}

// _mapValues keeps the keys (and what finds them).
func _mapValues[K, V, W any](m _Map[K, V], f func(V) W) _Map[K, W] {
	return _Map[K, W]{m.impl().mapVals(func(v any) any {
		x, _ := v.(V)
		return f(x)
	})}
}

func (m _Map[K, V]) _borkEqual(other any) bool {
	o, ok := other.(_Map[K, V])
	return ok && m._equals(o)
}

func (m _Map[K, V]) _equals(o _Map[K, V]) bool {
	if m.len() != o.len() { return false }
	equal := true
	m.impl().each(func(e *_mapEntry) bool {
		v := o.impl().find(e.key)
		equal = v != nil && _equal(e.val, v.val)
		return equal
	})
	return equal
}

func (m _Map[K, V]) _borkHash() uint64 {
	var h uint64
	m.impl().each(func(e *_mapEntry) bool {
		h += _hashMix(_hash(e.key), _hash(e.val))
		return true
	})
	return _hashMix(uint64(m.len()), h)
}

func (m _Map[K, V]) String() string {
	if m.len() == 0 {
		return "{:}"
	}
	type shownEntry struct {
		key any
		keyText, text string
	}
	entries := make([]shownEntry, 0, m.len())
	m.impl().each(func(e *_mapEntry) bool {
		keyText := _show(e.key)
		entries = append(entries, shownEntry{e.key, keyText, keyText+": "+_show(e.val)})
		return true
	})
	if _, ok := m.impl().(*_hashCore); ok {
		slices.SortFunc(entries, func(a, b shownEntry) int {
			if c := _mapPrintCompare(a.key, b.key, a.keyText, b.keyText); c != 0 {
				return c
			}
			return strings.Compare(a.text, b.text)
		})
	}
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.text
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// _mapPrintCompare orders numbers and strings by value, and other keys
// by their text. Mixed kinds are grouped so the order stays transitive.
func _mapPrintCompare(a, b any, aText, bText string) int {
	x, y := reflect.ValueOf(a), reflect.ValueOf(b)
	if x.Kind() != y.Kind() {
		return cmp.Compare(x.Kind(), y.Kind())
	}
	switch x.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cmp.Compare(x.Int(), y.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return cmp.Compare(x.Uint(), y.Uint())
	case reflect.Float32, reflect.Float64:
		return cmp.Compare(x.Float(), y.Float())
	case reflect.String:
		return strings.Compare(x.String(), y.String())
	}
	return strings.Compare(aText, bText)
}

// _mapEntry is a key with its value. hkey is the key as Go hashes and
// compares it (see _mapKey).
type _mapEntry struct {
	key, val, hkey any
	hash           uint64
	pos            int // in an insertion-ordered map, the position in its vector
}

type _mapCore struct {
	trie  *_hnode // the keys, each with its position in the vector (its value may be stale)
	vec   *_vnode
	shift uint // of the vector's root: 0 for a single leaf
	total int  // positions used in the vector, holes included
	n     int  // entries
}

// _mapHash is a variable so tests can force collisions.
var _mapHash = _hash

// _mapKey keeps the structural key; trie comparisons use _equal.
func _mapKey(k any) any { return k }

func (c *_mapCore) with(k, v any) _mapImpl { return c.put(k, v, false) }

func (c *_mapCore) without(k any) _mapImpl { return c.remove(k) }

func (c *_mapCore) empty() _mapImpl { return (*_mapCore)(nil) }

func (c *_mapCore) size() int {
	if c == nil {
		return 0
	}
	return c.n
}

func (c *_mapCore) mapVals(f func(v any) any) _mapImpl {
	if c == nil {
		return c
	}
	out := &_mapCore{trie: c.trie, n: c.n}
	for i := 0; i < c.total; i++ {
		var ne *_mapEntry
		if e := c.at(i); e != nil {
			ne = &_mapEntry{key: e.key, hkey: e.hkey, hash: e.hash, val: f(e.val), pos: e.pos}
		}
		out.appendSlot(ne)
	}
	return out
}

func (c *_mapCore) find(k any) *_mapEntry {
	if c == nil || c.n == 0 {
		return nil
	}
	hk := _mapKey(k)
	r := c.trie.get(hk, _mapHash(hk))
	if r == nil {
		return nil
	}
	return c.at(r.pos)
}

// put gives a map with k set to v. With inPlace (only while building
// a new map) it changes c instead.
func (c *_mapCore) put(k, v any, inPlace bool) *_mapCore {
	hk := _mapKey(k)
	h := _mapHash(hk)
	out := &_mapCore{}
	if c != nil {
		if inPlace {
			out = c
		} else {
			*out = *c
		}
		if r := c.trie.get(hk, h); r != nil {
			out.setSlot(r.pos, &_mapEntry{key: r.key, hkey: hk, hash: h, val: v, pos: r.pos})
			return out
		}
	}
	e := &_mapEntry{key: k, hkey: hk, hash: h, val: v, pos: out.total}
	out.trie, _ = out.trie.set(e, 0)
	out.appendSlot(e)
	out.n++
	return out
}

func (c *_mapCore) remove(k any) *_mapCore {
	if c == nil || c.n == 0 {
		return c
	}
	hk := _mapKey(k)
	h := _mapHash(hk)
	r := c.trie.get(hk, h)
	if r == nil {
		return c
	}
	out := *c
	out.trie, _ = c.trie.remove(hk, h, 0)
	out.setSlot(r.pos, nil)
	out.n--
	if out.n == 0 {
		return nil
	}
	if holes := out.total - out.n; holes > 32 && holes > out.n {
		return out.compact()
	}
	return &out
}

// compact rebuilds the map without holes.
func (c *_mapCore) compact() *_mapCore {
	b := &_mapCore{}
	c.each(func(e *_mapEntry) bool {
		ne := &_mapEntry{key: e.key, hkey: e.hkey, hash: e.hash, val: e.val, pos: b.total}
		b.trie, _ = b.trie.set(ne, 0)
		b.appendSlot(ne)
		b.n++
		return true
	})
	return b
}

func (c *_mapCore) each(f func(e *_mapEntry) bool) {
	if c != nil && c.vec != nil {
		c.vec.each(c.shift, f)
	}
}

// The vector: a trie of 32-way nodes, leaves holding the entries.

type _vnode struct {
	kids  []*_vnode
	items []*_mapEntry
}

func (c *_mapCore) at(i int) *_mapEntry {
	n := c.vec
	for s := c.shift; s > 0; s -= 5 {
		n = n.kids[(i>>s)&31]
	}
	return n.items[i&31]
}

func (c *_mapCore) setSlot(i int, e *_mapEntry) { c.vec = c.vec.set(c.shift, i, e) }

func (c *_mapCore) appendSlot(e *_mapEntry) {
	if c.vec == nil {
		c.vec = &_vnode{}
	} else if c.total == 32<<c.shift {
		c.vec = &_vnode{kids: []*_vnode{c.vec}}
		c.shift += 5
	}
	c.setSlot(c.total, e)
	c.total++
}

func (n *_vnode) set(shift uint, i int, e *_mapEntry) *_vnode {
	out := &_vnode{}
	if n != nil {
		out.kids, out.items = append([]*_vnode(nil), n.kids...), append([]*_mapEntry(nil), n.items...)
	}
	if shift == 0 {
		for len(out.items) <= i&31 {
			out.items = append(out.items, nil)
		}
		out.items[i&31] = e
		return out
	}
	j := (i >> shift) & 31
	for len(out.kids) <= j {
		out.kids = append(out.kids, nil)
	}
	out.kids[j] = out.kids[j].set(shift-5, i, e)
	return out
}

func (n *_vnode) each(shift uint, f func(e *_mapEntry) bool) bool {
	if shift == 0 {
		for _, e := range n.items {
			if e != nil && !f(e) {
				return false
			}
		}
		return true
	}
	for _, k := range n.kids {
		if !k.each(shift-5, f) {
			return false
		}
	}
	return true
}

// The trie (a CHAMP: compressed hash-array mapped prefix tree, as
// Scala's HashMap): nodes indexed by 5 bits of the hash at a time.
// datamap marks the slots that hold an entry, nodemap those that hold
// a node deeper down; entries and subs hold them, in slot order. Keys
// whose whole hashes are equal share a collision node (coll).

type _hnode struct {
	datamap, nodemap uint32
	entries          []*_mapEntry
	subs             []*_hnode
	coll             []*_mapEntry
}

func (n *_hnode) get(hk any, h uint64) *_mapEntry {
	for shift := uint(0); n != nil; shift += 5 {
		if n.coll != nil {
			for _, e := range n.coll {
				if _equal(e.hkey, hk) {
					return e
				}
			}
			return nil
		}
		bit := uint32(1) << ((h >> shift) & 31)
		if n.datamap&bit != 0 {
			e := n.entries[_popcount(n.datamap&(bit-1))]
			if e.hash == h && _equal(e.hkey, hk) {
				return e
			}
			return nil
		}
		if n.nodemap&bit == 0 {
			return nil
		}
		n = n.subs[_popcount(n.nodemap&(bit-1))]
	}
	return nil
}

func (n *_hnode) clone() *_hnode {
	return &_hnode{datamap: n.datamap, nodemap: n.nodemap, entries: n.entries, subs: n.subs}
}

func _insertAt[T any](xs []T, i int, x T) []T {
	out := make([]T, len(xs)+1)
	copy(out, xs[:i])
	out[i] = x
	copy(out[i+1:], xs[i:])
	return out
}

func _removeAt[T any](xs []T, i int) []T {
	out := make([]T, len(xs)-1)
	copy(out, xs[:i])
	copy(out[i:], xs[i+1:])
	return out
}

func _replaceAt[T any](xs []T, i int, x T) []T {
	out := make([]T, len(xs))
	copy(out, xs)
	out[i] = x
	return out
}

// set gives n with e for its key, added or replacing the entry there,
// and whether it was added.
func (n *_hnode) set(e *_mapEntry, shift uint) (*_hnode, bool) {
	if n == nil {
		bit := uint32(1) << ((e.hash >> shift) & 31)
		return &_hnode{datamap: bit, entries: []*_mapEntry{e}}, true
	}
	if n.coll != nil {
		for i, x := range n.coll {
			if _equal(x.hkey, e.hkey) {
				return &_hnode{coll: _replaceAt(n.coll, i, e)}, false
			}
		}
		return &_hnode{coll: append(append([]*_mapEntry(nil), n.coll...), e)}, true
	}
	bit := uint32(1) << ((e.hash >> shift) & 31)
	switch {
	case n.datamap&bit != 0:
		i := _popcount(n.datamap & (bit - 1))
		old := n.entries[i]
		out := n.clone()
		if old.hash == e.hash && _equal(old.hkey, e.hkey) {
			out.entries = _replaceAt(n.entries, i, e)
			return out, false
		}
		// Both go into a node deeper down.
		out.datamap &^= bit
		out.nodemap |= bit
		out.entries = _removeAt(n.entries, i)
		out.subs = _insertAt(n.subs, _popcount(out.nodemap&(bit-1)), _hpair(old, e, shift+5))
		return out, true
	case n.nodemap&bit != 0:
		j := _popcount(n.nodemap & (bit - 1))
		sub, added := n.subs[j].set(e, shift+5)
		out := n.clone()
		out.subs = _replaceAt(n.subs, j, sub)
		return out, added
	}
	out := n.clone()
	out.datamap |= bit
	out.entries = _insertAt(n.entries, _popcount(out.datamap&(bit-1)), e)
	return out, true
}

// _hpair is a node holding two entries with different keys.
func _hpair(a, b *_mapEntry, shift uint) *_hnode {
	if a.hash == b.hash {
		return &_hnode{coll: []*_mapEntry{a, b}}
	}
	ba, bb := (a.hash>>shift)&31, (b.hash>>shift)&31
	if ba == bb {
		return &_hnode{nodemap: 1 << ba, subs: []*_hnode{_hpair(a, b, shift+5)}}
	}
	if ba > bb {
		a, b = b, a
	}
	return &_hnode{datamap: 1<<ba | 1<<bb, entries: []*_mapEntry{a, b}}
}

// single is the one entry of a node that holds nothing else, or nil.
func (n *_hnode) single() *_mapEntry {
	switch {
	case n.coll != nil && len(n.coll) == 1:
		return n.coll[0]
	case n.coll == nil && len(n.entries) == 1 && len(n.subs) == 0:
		return n.entries[0]
	}
	return nil
}

// remove gives n without a key (nil if nothing is left), and whether
// it had the key.
func (n *_hnode) remove(hk any, h uint64, shift uint) (*_hnode, bool) {
	if n == nil {
		return nil, false
	}
	if n.coll != nil {
		for i, x := range n.coll {
			if _equal(x.hkey, hk) {
				if len(n.coll) == 1 {
					return nil, true
				}
				return &_hnode{coll: _removeAt(n.coll, i)}, true
			}
		}
		return n, false
	}
	bit := uint32(1) << ((h >> shift) & 31)
	switch {
	case n.datamap&bit != 0:
		i := _popcount(n.datamap & (bit - 1))
		if e := n.entries[i]; e.hash != h || !_equal(e.hkey, hk) {
			return n, false
		}
		if len(n.entries) == 1 && len(n.subs) == 0 {
			return nil, true
		}
		out := n.clone()
		out.datamap &^= bit
		out.entries = _removeAt(n.entries, i)
		return out, true
	case n.nodemap&bit != 0:
		j := _popcount(n.nodemap & (bit - 1))
		sub, removed := n.subs[j].remove(hk, h, shift+5)
		if !removed {
			return n, false
		}
		out := n.clone()
		switch {
		case sub == nil:
			out.nodemap &^= bit
			out.subs = _removeAt(n.subs, j)
			if len(out.entries) == 0 && len(out.subs) == 0 {
				return nil, true
			}
		case sub.single() != nil:
			// A node left with one entry moves it up here.
			out.nodemap &^= bit
			out.subs = _removeAt(n.subs, j)
			out.datamap |= bit
			out.entries = _insertAt(n.entries, _popcount(out.datamap&(bit-1)), sub.single())
		default:
			out.subs = _replaceAt(n.subs, j, sub)
		}
		return out, true
	}
	return n, false
}

// each calls f with the entries, until it returns false.
func (n *_hnode) each(f func(e *_mapEntry) bool) bool {
	if n == nil {
		return true
	}
	for _, e := range n.coll {
		if !f(e) {
			return false
		}
	}
	for _, e := range n.entries {
		if !f(e) {
			return false
		}
	}
	for _, sub := range n.subs {
		if !sub.each(f) {
			return false
		}
	}
	return true
}

// mapEntries gives n with f applied to every entry, keeping its shape.
func (n *_hnode) mapEntries(f func(e *_mapEntry) *_mapEntry) *_hnode {
	if n == nil {
		return nil
	}
	out := &_hnode{datamap: n.datamap, nodemap: n.nodemap}
	for _, e := range n.coll {
		out.coll = append(out.coll, f(e))
	}
	if len(n.entries) > 0 {
		out.entries = make([]*_mapEntry, len(n.entries))
		for i, e := range n.entries {
			out.entries[i] = f(e)
		}
	}
	if len(n.subs) > 0 {
		out.subs = make([]*_hnode, len(n.subs))
		for i, sub := range n.subs {
			out.subs[i] = sub.mapEntries(f)
		}
	}
	return out
}

func _popcount(x uint32) int { return bits.OnesCount32(x) }

// The sorted map: a persistent AVL tree ordered by less. Two keys are
// the same key when neither is less than the other.

type _sortedCore struct {
	root *_tnode
	less func(a, b any) bool
	n    int
}

type _tnode struct {
	e           *_mapEntry
	left, right *_tnode
	height      int
}

func (c *_sortedCore) find(k any) *_mapEntry {
	n := c.root
	for n != nil {
		switch {
		case c.less(k, n.e.key):
			n = n.left
		case c.less(n.e.key, k):
			n = n.right
		default:
			return n.e
		}
	}
	return nil
}

func (c *_sortedCore) with(k, v any) _mapImpl {
	root, added := c.insert(c.root, k, v)
	out := &_sortedCore{root: root, less: c.less, n: c.n}
	if added {
		out.n++
	}
	return out
}

func (c *_sortedCore) without(k any) _mapImpl {
	if c.find(k) == nil {
		return c
	}
	return &_sortedCore{root: c.delete(c.root, k), less: c.less, n: c.n - 1}
}

func (c *_sortedCore) size() int { return c.n }

func (c *_sortedCore) empty() _mapImpl { return &_sortedCore{less: c.less} }

func (c *_sortedCore) each(f func(e *_mapEntry) bool) { c.root.each(f) }

func (c *_sortedCore) mapVals(f func(v any) any) _mapImpl {
	return &_sortedCore{root: c.root.mapVals(f), less: c.less, n: c.n}
}

func (n *_tnode) each(f func(e *_mapEntry) bool) bool {
	return n == nil || (n.left.each(f) && f(n.e) && n.right.each(f))
}

func (n *_tnode) mapVals(f func(v any) any) *_tnode {
	if n == nil {
		return nil
	}
	return &_tnode{e: &_mapEntry{key: n.e.key, val: f(n.e.val)}, left: n.left.mapVals(f), right: n.right.mapVals(f), height: n.height}
}

func (n *_tnode) h() int {
	if n == nil {
		return 0
	}
	return n.height
}

func _tmake(e *_mapEntry, l, r *_tnode) *_tnode {
	return &_tnode{e: e, left: l, right: r, height: 1 + max(l.h(), r.h())}
}

// _tbalance makes a node of e, l, and r, rotating if their heights
// differ by more than one.
func _tbalance(e *_mapEntry, l, r *_tnode) *_tnode {
	switch {
	case l.h() > r.h()+1:
		if l.left.h() >= l.right.h() {
			return _tmake(l.e, l.left, _tmake(e, l.right, r))
		}
		return _tmake(l.right.e, _tmake(l.e, l.left, l.right.left), _tmake(e, l.right.right, r))
	case r.h() > l.h()+1:
		if r.right.h() >= r.left.h() {
			return _tmake(r.e, _tmake(e, l, r.left), r.right)
		}
		return _tmake(r.left.e, _tmake(e, l, r.left.left), _tmake(r.e, r.left.right, r.right))
	}
	return _tmake(e, l, r)
}

func (c *_sortedCore) insert(n *_tnode, k, v any) (*_tnode, bool) {
	if n == nil {
		return &_tnode{e: &_mapEntry{key: k, val: v}, height: 1}, true
	}
	switch {
	case c.less(k, n.e.key):
		l, added := c.insert(n.left, k, v)
		return _tbalance(n.e, l, n.right), added
	case c.less(n.e.key, k):
		r, added := c.insert(n.right, k, v)
		return _tbalance(n.e, n.left, r), added
	}
	return _tmake(&_mapEntry{key: n.e.key, val: v}, n.left, n.right), false
}

// delete removes a key that n has.
func (c *_sortedCore) delete(n *_tnode, k any) *_tnode {
	switch {
	case c.less(k, n.e.key):
		return _tbalance(n.e, c.delete(n.left, k), n.right)
	case c.less(n.e.key, k):
		return _tbalance(n.e, n.left, c.delete(n.right, k))
	case n.left == nil:
		return n.right
	case n.right == nil:
		return n.left
	}
	// Replace n by the smallest entry on its right.
	m := n.right
	for m.left != nil {
		m = m.left
	}
	return _tbalance(m.e, n.left, c.delete(n.right, m.e.key))
}

// The unordered map: the trie alone, holding the entries. It lists
// them in the order of their hashes, which differs from run to run.

type _hashCore struct {
	trie *_hnode
	n    int
}

func (c *_hashCore) find(k any) *_mapEntry {
	hk := _mapKey(k)
	return c.trie.get(hk, _mapHash(hk))
}

func (c *_hashCore) with(k, v any) _mapImpl {
	hk := _mapKey(k)
	h := _mapHash(hk)
	key := k
	if old := c.trie.get(hk, h); old != nil {
		key = old.key
	}
	trie, added := c.trie.set(&_mapEntry{key: key, hkey: hk, hash: h, val: v}, 0)
	out := &_hashCore{trie: trie, n: c.n}
	if added {
		out.n++
	}
	return out
}

func (c *_hashCore) without(k any) _mapImpl {
	hk := _mapKey(k)
	trie, removed := c.trie.remove(hk, _mapHash(hk), 0)
	if !removed {
		return c
	}
	return &_hashCore{trie: trie, n: c.n - 1}
}

func (c *_hashCore) size() int { return c.n }

func (c *_hashCore) empty() _mapImpl { return &_hashCore{} }

func (c *_hashCore) each(f func(e *_mapEntry) bool) { c.trie.each(f) }

func (c *_hashCore) mapVals(f func(v any) any) _mapImpl {
	return &_hashCore{trie: c.trie.mapEntries(func(e *_mapEntry) *_mapEntry {
		return &_mapEntry{key: e.key, hkey: e.hkey, hash: e.hash, val: f(e.val)}
	}), n: c.n}
}

// _mapUnordered is m as an unordered map.
func _mapUnordered[K, V any](m _Map[K, V]) _Map[K, V] {
	if _, ok := m.impl().(*_hashCore); ok {
		return m
	}
	var out _mapImpl = &_hashCore{}
	m.impl().each(func(e *_mapEntry) bool {
		out = out.with(e.key, e.val)
		return true
	})
	return _Map[K, V]{out}
}

// _implOf is the core of a _Map value reached by reflection.
func _implOf(x reflect.Value) _mapImpl {
	f := x.Field(0)
	if f.IsNil() {
		return (*_mapCore)(nil)
	}
	e := f.Elem()
	switch e.Type() {
	case reflect.TypeFor[*_mapCore]():
		return (*_mapCore)(e.UnsafePointer())
	case reflect.TypeFor[*_sortedCore]():
		return (*_sortedCore)(e.UnsafePointer())
	case reflect.TypeFor[*_hashCore]():
		return (*_hashCore)(e.UnsafePointer())
	}
	panic("bork: unknown map kind " + e.Type().String())
}

// _equalMaps compares two maps: the same keys, with equal values, in
// any order. (x and y are _Map values, reached by reflection.)
func _equalMaps(x, y reflect.Value) bool {
	cx, cy := _implOf(x), _implOf(y)
	if cx.size() != cy.size() {
		return false
	}
	equal := true
	cx.each(func(e *_mapEntry) bool {
		o := cy.find(e.key)
		equal = o != nil && _equal(e.val, o.val)
		return equal
	})
	return equal
}

func init() { _equalMapHook = _equalMaps }
`

// unitRuntime is the Go value of Unit, in a union.
const unitRuntime = `package main

type _Unit struct{}

func (_Unit) String() string { return "Unit" }
`
