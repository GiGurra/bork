package gen

import (
	"go/ast"
	"go/token"

	"github.com/GiGurra/bork/internal/check"
)

type loopCleanup struct {
	name *ast.Ident
	used bool
}
type loopExit struct {
	flag, result *ast.Ident
	used         bool
	labelUsed    bool
}
type loopFrame struct {
	scopes, owners, mocks int
	cleanup               *loopCleanup
	exit                  *loopExit
	label                 *ast.Ident
}

func (g *gen) generateSeq(e *check.Generate) ast.Expr {
	t := e.Type().(*check.Seq)
	seqType := g.goType(t)
	// The producer is a function of its own: a return in it ends
	// only what it opened.
	savedResult, savedScopes, savedOwners, savedMocks := g.fnResult, g.openScopes, g.blockOwners, g.openMocks
	savedYield, savedLoops := g.yieldName, g.loops
	g.fnResult, g.openScopes, g.blockOwners, g.openMocks = check.Unit, nil, nil, nil
	g.yieldName, g.loops = g.newTmp(), nil
	yield := g.yieldName
	body := g.guardLabels(func() []ast.Stmt { return g.effect(e.Body) })
	g.fnResult, g.openScopes, g.blockOwners, g.openMocks = savedResult, savedScopes, savedOwners, savedMocks
	g.yieldName, g.loops = savedYield, savedLoops
	cb := &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{Type: g.goType(t.Elem)}}}, Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("bool")}}}}
	producer := &ast.FuncLit{Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{yield}, Type: cb}}}}, Body: &ast.BlockStmt{List: body}}
	return &ast.CompositeLit{Type: seqType, Elts: []ast.Expr{&ast.KeyValueExpr{Key: ast.NewIdent("run"), Value: producer}}}
}

func (g *gen) yieldSeq(e *check.Yield) []ast.Stmt {
	stmts, value := g.value(e.Value)
	if value == nil {
		return stmts
	}
	call := &ast.CallExpr{Fun: g.yieldName, Args: []ast.Expr{g.convert(value, e.Value.Type(), e.Elem)}}
	return append(stmts, &ast.IfStmt{Cond: &ast.UnaryExpr{Op: token.NOT, X: call}, Body: &ast.BlockStmt{List: g.returning()}})
}

func (g *gen) forSeq(e *check.For) []ast.Stmt {
	stmts, source := g.value(e.Items)
	if source == nil {
		return stmts
	}
	root := &loopCleanup{name: g.newTmp()}
	top := len(g.loops) == 0
	if !top {
		root = g.loops[len(g.loops)-1].cleanup
	}
	exit := &loopExit{flag: g.newTmp(), result: g.newTmp()}
	label := g.newTmp()
	g.loops = append(g.loops, loopFrame{len(g.openScopes), len(g.blockOwners), len(g.openMocks), root, exit, label})
	body := g.effect(e.Body)
	g.loops = g.loops[:len(g.loops)-1]
	if top && root.used {
		g.usesLoopCleanup = true
		g.usesScopes = true
		stmts = append(stmts, typedVar(root.name, ast.NewIdent("_loopCleanup"), &ast.CompositeLit{Type: ast.NewIdent("_loopCleanup")}), &ast.DeferStmt{Call: &ast.CallExpr{Fun: &ast.SelectorExpr{X: root.name, Sel: ast.NewIdent("close")}}})
	}
	if exit.used {
		stmts = append(stmts, define(exit.flag, ast.NewIdent("false")))
		if g.fnResult != check.Unit {
			stmts = append(stmts, &ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{&ast.ValueSpec{Names: []*ast.Ident{exit.result}, Type: g.goType(g.fnResult)}}}})
			if g.fnResult == check.OwnedScope {
				stmts = append(stmts, &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: root.name, Sel: ast.NewIdent("owner")}, Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: exit.result}}}})
			}
		}
	}
	// Iteration names are always legal when unused.
	body = append([]ast.Stmt{assign(ast.NewIdent("_"), name(e.Var.Name))}, body...)
	loop := &ast.RangeStmt{Tok: token.DEFINE, Body: &ast.BlockStmt{List: body}}
	if _, ok := e.Items.Type().(*check.Seq); ok {
		loop.Key = name(e.Var.Name)
		if root.used {
			source = &ast.CallExpr{Fun: ast.NewIdent("_seqLoop"), Args: []ast.Expr{source, &ast.UnaryExpr{Op: token.AND, X: root.name}}}
		}
		loop.X = &ast.SelectorExpr{X: source, Sel: ast.NewIdent("run")}
	} else {
		loop.Key = ast.NewIdent("_")
		loop.Value = name(e.Var.Name)
		loop.X = source
	}
	if exit.labelUsed {
		stmts = append(stmts, &ast.LabeledStmt{Label: label, Stmt: loop})
	} else {
		stmts = append(stmts, loop)
	}
	if exit.used {
		if g.fnResult == check.OwnedScope {
			stmts = append(stmts, &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: root.name, Sel: ast.NewIdent("forget")}, Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: exit.result}}}})
		}
		var result []ast.Expr
		if g.fnResult != check.Unit {
			var value ast.Expr = exit.result
			if g.fnResult == check.OwnedScope {
				value = &ast.CallExpr{Fun: ast.NewIdent("_takeScope"), Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: exit.result}}}
			}
			result = []ast.Expr{value}
		}
		stmts = append(stmts, &ast.IfStmt{Cond: exit.flag, Body: &ast.BlockStmt{List: g.returning(result...)}})
	}
	return stmts
}

func (g *gen) loopControl(e *check.LoopControl) []ast.Stmt {
	frame := g.loops[len(g.loops)-1]
	frame.exit.labelUsed = true
	stmts := g.endMocks(frame.mocks)
	for j := len(g.blockOwners) - 1; j >= frame.owners; j-- {
		o := g.blockOwners[j]
		take := &ast.CallExpr{Fun: ast.NewIdent("_takeLoopOwner"), Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: frame.cleanup.name}, &ast.UnaryExpr{Op: token.AND, X: name(o.name)}}}
		stmts = append(stmts, &ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("_dropScope"), Args: []ast.Expr{take}}})
	}
	for i := len(g.openScopes) - 1; i >= frame.scopes; i-- {
		stmts = append(stmts, &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: frame.cleanup.name, Sel: ast.NewIdent("closeScope")}, Args: []ast.Expr{g.openScopes[i]}}})
	}
	tok := token.BREAK
	if e.Continue {
		tok = token.CONTINUE
	}
	return append(stmts, &ast.BranchStmt{Tok: tok, Label: frame.label})
}

const seqRuntime = `package main

import "iter"

type _Seq[T any] struct{ run iter.Seq[T] }

func (_Seq[T]) _borkShow() string { return "<Seq>" }
func _seqRun[T any](source _Seq[T], yield func(T) bool) {
	stopped, finished := false, false
	defer func() { finished = true }()
	if source.run == nil {
		return
	}
	source.run(func(x T) bool {
		if stopped || finished {
			panic("iterator yielded after stop")
		}
		stopped = true
		keep := yield(x)
		stopped = !keep
		return keep
	})
}
func _seqempty[T any]() _Seq[T] { return _Seq[T]{func(yield func(T) bool) {}} }
func _seqrange(start, end int64) _Seq[int64] {
	return _Seq[int64]{func(yield func(int64) bool) {
		for n := start; n < end; n++ {
			if !yield(n) {
				return
			}
		}
	}}
}
func _seqfromList[T any](xs []T) _Seq[T] {
	return _Seq[T]{func(yield func(T) bool) {
		for _, x := range xs {
			if !yield(x) {
				return
			}
		}
	}}
}
func _seqmap[A, B any](source _Seq[A], f func(A) B) _Seq[B] {
	return _Seq[B]{func(yield func(B) bool) { _seqRun(source, func(x A) bool { return yield(f(x)) }) }}
}
func _seqfilter[T any](source _Seq[T], f func(T) bool) _Seq[T] {
	return _Seq[T]{func(yield func(T) bool) {
		_seqRun(source, func(x T) bool {
			if !f(x) {
				return true
			}
			return yield(x)
		})
	}}
}
func _seqflatMap[A, B any](source _Seq[A], f func(A) _Seq[B]) _Seq[B] {
	return _Seq[B]{func(yield func(B) bool) {
		stopped := false
		_seqRun(source, func(x A) bool {
			_seqRun(f(x), func(v B) bool {
				if !yield(v) {
					stopped = true
					return false
				}
				return true
			})
			return !stopped
		})
	}}
}
func _seqtake[T any](source _Seq[T], n int64) _Seq[T] {
	return _Seq[T]{func(yield func(T) bool) {
		if n <= 0 {
			return
		}
		left := n
		_seqRun(source, func(x T) bool { left--; return yield(x) && left > 0 })
	}}
}
func _seqdrop[T any](source _Seq[T], n int64) _Seq[T] {
	return _Seq[T]{func(yield func(T) bool) {
		left := n
		_seqRun(source, func(x T) bool {
			if left > 0 {
				left--
				return true
			}
			return yield(x)
		})
	}}
}
func _seqforEach[T any](source _Seq[T], f func(T)) {
	_seqRun(source, func(x T) bool { f(x); return true })
}
func _seqfold[T, S any](source _Seq[T], seed S, f func(S, T) S) S {
	acc := seed
	_seqRun(source, func(x T) bool { acc = f(acc, x); return true })
	return acc
}
func _seqtoList[T any](source _Seq[T]) []T {
	out := []T{}
	_seqRun(source, func(x T) bool { out = append(out, x); return true })
	return out
}
`

func (g *gen) seqCall(e *check.SeqCall) ([]ast.Stmt, ast.Expr) {
	g.usesSeq = true
	stmts, args := g.values(e.Args)
	var types []ast.Expr
	switch e.Op {
	case "empty":
		types = []ast.Expr{g.goType(e.Type().(*check.Seq).Elem)}
	case "unfold":
		g.goType(e.Args[1].Type())
		g.usesSeqUnfold = true
	case "first":
		g.goType(e.Type())
		g.usesOptionHelpers = true
		g.usesSeqFirst = true
	}
	if e.Op == "map" || e.Op == "flatMap" {
		types = []ast.Expr{g.goType(e.Args[0].Type().(*check.Seq).Elem), g.goType(e.Type().(*check.Seq).Elem)}
	}
	var fun ast.Expr = ast.NewIdent("_seq" + e.Op)
	if len(types) > 0 {
		fun = &ast.IndexListExpr{X: fun, Indices: types}
	}
	return stmts, &ast.CallExpr{Fun: fun, Args: args}
}

// Loop cleanup retains only active entries, rather than one Go defer per item.
const loopCleanupRuntime = `package main

type _loopEntry struct {
	scope *_Scope
	owner **_Scope
}
type _loopCleanup struct{ entries []_loopEntry }

func (s *_loopCleanup) scope(scope *_Scope)  { s.entries = append(s.entries, _loopEntry{scope: scope}) }
func (s *_loopCleanup) owner(owner **_Scope) { s.entries = append(s.entries, _loopEntry{owner: owner}) }
func (s *_loopCleanup) forget(owner **_Scope) {
	for i := len(s.entries) - 1; i >= 0; i-- {
		if s.entries[i].owner == owner {
			copy(s.entries[i:], s.entries[i+1:])
			s.entries[len(s.entries)-1] = _loopEntry{}
			s.entries = s.entries[:len(s.entries)-1]
			return
		}
	}
}
func _takeLoopOwner(s *_loopCleanup, owner **_Scope) *_Scope {
	s.forget(owner)
	return _takeScope(owner)
}
func (s *_loopCleanup) pop() {
	n := len(s.entries) - 1
	entry := s.entries[n]
	s.entries[n] = _loopEntry{}
	s.entries = s.entries[:n]
	if entry.owner != nil {
		_dropScope(_takeScope(entry.owner))
	} else {
		entry.scope.close()
	}
}
func (s *_loopCleanup) closeScope(scope *_Scope) {
	for i := len(s.entries) - 1; i >= 0; i-- {
		if s.entries[i].scope == scope {
			for len(s.entries) > i {
				s.pop()
			}
			return
		}
	}
	scope.close()
}
func (s *_loopCleanup) closeFrom(mark int) {
	if len(s.entries) > mark {
		defer s.closeFrom(mark)
		s.pop()
	}
}
func (s *_loopCleanup) close() { s.closeFrom(0) }
`
const seqLoopRuntime = `package main
func _seqLoop[T any](source _Seq[T], cleanup *_loopCleanup) _Seq[T] {
	return _Seq[T]{func(yield func(T) bool) {
		_seqRun(source, func(value T) bool {
			mark := len(cleanup.entries)
			defer cleanup.closeFrom(mark)
			return yield(value)
		})
	}}
}
`
