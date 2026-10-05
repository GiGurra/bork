package gen

import (
	"go/ast"
	"go/token"
	"strconv"

	"github.com/GiGurra/bork/internal/check"
)

// Self tail calls (see docs/design/loops.md). A function whose body
// calls itself in tail position gets a loop around its body; a tail
// call assigns the next arguments to the parameters' slots and jumps
// to the top:
//
//	func gcd(_in_a int64, _in_b int64) int64 {
//	_tail:
//		for {
//			a, b := _in_a, _in_b
//			_, _ = a, b
//			if b == 0 {
//				return a
//			}
//			_in_a, _in_b = b, a%b
//			continue _tail
//		}
//	}
//
// Each call gets its own copies of the parameters, which closures
// capture. The loop is a loop frame like a for's, so scopes and owners
// bound in the body close through its cleanup root (declared once,
// outside the loop) rather than per-call Go defers, and a return or a
// tail call leaving an inner loop goes through that loop's exit.

// tailFrame is the function being generated, if it jumps.
type tailFrame struct {
	fn    *check.Func
	slots []*ast.Ident
}

// tailJump reports whether call is a self call compiled as a jump.
func (g *gen) tailJump(call *check.Call) bool {
	tc := g.info.TailCalls[call]
	return tc != nil && tc.Jump && g.tail != nil && g.tail.fn == call.Func
}

// tailFuncDecl gives fn's declaration (decl, its signature) a body
// that loops.
func (g *gen) tailFuncDecl(fn *check.Func, decl *ast.FuncDecl) *ast.FuncDecl {
	saved := g.tail
	defer func() { g.tail = saved }()
	frame := &tailFrame{fn: fn}
	g.tail = frame
	// The parameters follow the instances of the type parameters'
	// bounds.
	fields := decl.Type.Params.List[len(g.dictParams(fn.TypeParams)):]
	var copies []ast.Stmt
	var names []ast.Expr
	for i := range fn.Decl.Params {
		param := fields[i].Names[0]
		slot := ast.NewIdent("_in_" + param.Name)
		fields[i].Names[0] = slot
		frame.slots = append(frame.slots, slot)
		copies = append(copies, define(param, slot))
		names = append(names, param)
	}
	if len(names) > 0 {
		blanks := make([]ast.Expr, len(names))
		for i := range blanks {
			blanks[i] = ast.NewIdent("_")
		}
		copies = append(copies, &ast.AssignStmt{Lhs: blanks, Tok: token.ASSIGN, Rhs: names})
	}
	root := &loopCleanup{name: g.newTmp()}
	exit := &loopExit{flag: g.newTmp(), result: g.newTmp(), jump: g.newTmp()}
	label := ast.NewIdent("_tail")
	k := sink{ret: fn.Result != check.Ok}
	body := g.guardLabels(func() []ast.Stmt {
		g.loops = append(g.loops, loopFrame{len(g.openScopes), len(g.blockOwners), len(g.openMocks), root, exit, label, true})
		inner := append(copies, g.blockInto(fn.Body, k)...)
		g.loops = g.loops[:len(g.loops)-1]
		if fn.Result == check.Ok {
			// Ending the body ends the function.
			inner = append(inner, &ast.ReturnStmt{})
		}
		var stmts []ast.Stmt
		if root.used {
			g.usesLoopCleanup = true
			g.usesScopes = true
			stmts = append(stmts, typedVar(root.name, ast.NewIdent("_loopCleanup"), &ast.CompositeLit{Type: ast.NewIdent("_loopCleanup")}), &ast.DeferStmt{Call: &ast.CallExpr{Fun: &ast.SelectorExpr{X: root.name, Sel: ast.NewIdent("close")}}})
		}
		stmts = append(stmts, g.exitVars(root, exit)...)
		stmts = append(stmts, &ast.LabeledStmt{Label: label, Stmt: &ast.ForStmt{Body: &ast.BlockStmt{List: inner}}})
		if exit.used {
			// Only a return leaves the loop.
			if fn.Result == check.OwnedScope {
				stmts = append(stmts, &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: root.name, Sel: ast.NewIdent("forget")}, Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: exit.result}}}})
			}
			stmts = append(stmts, &ast.ReturnStmt{Results: g.exitResult(exit)})
		}
		return stmts
	})
	decl.Body = &ast.BlockStmt{List: append(g.debugLine(fn.Decl.Pos), body...)}
	return decl
}

// jump lowers a self tail call: the arguments, in the call's order,
// become the next call's parameters.
func (g *gen) jump(call *check.Call) []ast.Stmt {
	stmts, xs := g.values(call.EvaluationArgs())
	if xs == nil {
		return stmts
	}
	if call.ArgOrder != nil {
		ordered := make([]ast.Expr, len(xs))
		for i, param := range call.ArgOrder {
			ordered[param] = xs[i]
		}
		xs = ordered
	}
	for i := range xs {
		xs[i] = g.convert(xs[i], call.Args[i].Type(), call.Inst.Params[i])
	}
	if id := g.mockIDs[call.Func]; id != 0 {
		// A mock in force answers the call, as it would an ordinary one.
		for i, x := range xs {
			if !stable(x) {
				var save []ast.Stmt
				save, xs[i] = g.save(x, call.Inst.Params[i])
				stmts = append(stmts, save...)
			}
		}
		g.usesMocks, g.usesMockIn = true, true
		var dicts []ast.Expr
		for _, d := range call.Inst.Dicts {
			dicts = append(dicts, g.dict(d))
		}
		needStmts, needs := g.values(call.Needs)
		stmts = append(stmts, needStmts...)
		ordinary := g.instanceResult(call.Inst, &ast.CallExpr{Fun: g.instance(call.Inst), Args: append(append(dicts, xs...), needs...)})
		var answer []ast.Stmt
		if g.fnResult == check.Ok || g.fnResult == check.Never {
			answer = append([]ast.Stmt{&ast.ExprStmt{X: ordinary}}, g.returning()...)
		} else {
			answer = g.returning(ordinary)
		}
		mocked := &ast.CallExpr{Fun: ast.NewIdent("_mockIn"), Args: []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(id)}}}
		stmts = append(stmts, &ast.IfStmt{Cond: mocked, Body: &ast.BlockStmt{List: answer}})
	}
	if len(xs) > 0 {
		lhs := make([]ast.Expr, len(xs))
		for i := range xs {
			lhs[i] = g.tail.slots[i]
		}
		stmts = append(stmts, &ast.AssignStmt{Lhs: lhs, Tok: token.ASSIGN, Rhs: xs})
	}
	return append(stmts, g.tailContinue()...)
}

// tailContinue jumps to the top of the function, leaving the loops
// inside it through their exits.
func (g *gen) tailContinue() []ast.Stmt {
	frame := g.loops[len(g.loops)-1]
	if frame.tail {
		frame.exit.labelUsed = true
		return []ast.Stmt{&ast.BranchStmt{Tok: token.CONTINUE, Label: frame.label}}
	}
	frame.exit.jumpUsed = true
	return append([]ast.Stmt{assign(frame.exit.jump, ast.NewIdent("true"))}, g.loopControl(&check.LoopControl{})...)
}

const tailMockRuntime = `package main
// _mockIn reports whether a mock of function id is in force on this
// goroutine: a self tail call then goes through it.
func _mockIn(id int) bool {
	for f := _mockCurrent(); f != nil; f = f.parent {
		if f.id == id && !f.ended.Load() {
			return true
		}
	}
	return false
}
`
