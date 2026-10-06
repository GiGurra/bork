package check

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// TailCall says how a call of a function by itself is compiled (see
// docs/design/loops.md): as a jump to the top of the function, reusing
// its frame, or as an ordinary call, for Reason.
type TailCall struct {
	Jump   bool   `json:"jump"`
	Reason string `json:"reason,omitempty"`
}

// TailJumps reports whether fn has a self call compiled as a jump, so
// its generated body loops.
func (info *Info) TailJumps(fn *Func) bool {
	return info.tailJumps[fn]
}

// CheckTailCalls finds the self calls of every function with a bork
// body, records how each is compiled in info.TailCalls, and checks the
// promise of functions that declare tailrec. It reads the typed tree,
// so it runs once the program type-checks.
func CheckTailCalls(files []*syntax.File, info *Info, diags *diag.List) {
	if info.TailCalls == nil {
		info.TailCalls = map[*Call]*TailCall{}
	}
	if info.tailJumps == nil {
		info.tailJumps = map[*Func]bool{}
	}
	var funcs []*Func
	for _, f := range files {
		for _, fd := range f.Funcs {
			if fn := info.FuncOf[fd]; fn != nil && fn.Body != nil && fn.Of == nil && fn.Class == nil && fn.Derived == nil {
				funcs = append(funcs, fn)
			}
		}
	}
	for _, fn := range funcs {
		s := &tailScan{fn: fn, info: info, eligible: tailEligible(fn)}
		s.walk(fn.Body, tailCtx{tail: true})
		if !fn.TailRec {
			continue
		}
		cycle := mutualCycle(fn)
		if !s.recursive && cycle == "" {
			diags.AddCode(fn.TailRecPos, "tailrec.not-recursive", "%s declares tailrec, but never calls itself; remove the marker", fn.Decl.Name)
		}
		for _, call := range s.calls {
			if tc := info.TailCalls[call]; !tc.Jump {
				diags.AddCode(call.Pos(), "tailrec.not-tail", "%s declares tailrec, but this call of itself is not a tail call: %s", fn.Decl.Name, tc.Reason)
			}
		}
		if cycle != "" {
			diags.AddCode(fn.TailRecPos, "tailrec.mutual", "%s declares tailrec, but it is mutually recursive: %s; tailrec covers only calls of itself, so write the states as a loop over a state value", fn.Decl.Name, cycle)
		}
	}
}

// tailEligible is why no self call of fn can be a jump, or "".
func tailEligible(fn *Func) string {
	if fn.TrackCaller {
		return fn.Decl.Name + " takes its caller's location, which a self call would change"
	}
	for i, p := range fn.Params {
		if p == OwnedScope {
			return fmt.Sprintf("%s takes owned scope %s, which each call closes when it returns if it was not passed on", fn.Decl.Name, fn.Decl.Params[i].Name)
		}
	}
	return ""
}

type tailScan struct {
	fn        *Func
	info      *Info
	eligible  string
	recursive bool
	calls     []*Call
}

// tailCtx is where an expression is: in tail position (its value is
// the function's result, with nothing left to do), and if not, why;
// blocked is set inside a construct that makes nothing in it a tail
// call, such as a scope block, even after return.
type tailCtx struct {
	tail    bool
	why     string
	blocked string
}

func (c tailCtx) inner(why string) tailCtx {
	return tailCtx{why: why, blocked: c.blocked}
}

func (c tailCtx) block(why string) tailCtx {
	if c.blocked != "" {
		return tailCtx{blocked: c.blocked}
	}
	return tailCtx{blocked: why}
}

const tailUsed = "its result is used after it returns"

func (s *tailScan) walk(x Expr, c tailCtx) {
	if x == nil || reflect.ValueOf(x).IsNil() {
		return
	}
	switch x := x.(type) {
	case *Call:
		for _, arg := range x.Needs {
			s.walk(arg, c.inner(tailUsed))
		}
		for _, arg := range x.EvaluationArgs() {
			s.walk(arg, c.inner(tailUsed))
		}
		if x.Func == s.fn {
			s.selfCall(x, c)
		}
		return
	case *If:
		s.walk(x.Cond, c.inner(tailUsed))
		if x.Else == nil && s.fn.Result != Ok {
			// Without else, the branch's value is dropped.
			s.walk(x.Then, c.inner("an if without else drops its value"))
			return
		}
		s.walk(x.Then, c)
		s.walk(x.Else, c)
		return
	case *Match:
		s.walk(x.X, c.inner(tailUsed))
		for _, arm := range x.Arms {
			for _, guard := range arm.Pat.Guards() {
				s.walk(guard, c.inner(tailUsed))
			}
			s.walk(arm.Body, c)
		}
		return
	case *Block:
		s.blockStmts(x, c)
		return
	case *ScopeBlock:
		for _, p := range x.Policies {
			s.walk(p, c.inner(tailUsed))
		}
		s.walk(x.Body, c.block(fmt.Sprintf("it is inside the scope block at line %d, which closes after the call returns", x.Pos().Line)))
		return
	case *Return:
		s.walk(x.Value, tailCtx{tail: true, blocked: c.blocked})
		return
	case *Try:
		s.walk(x.X, c.inner("its result is checked by ?"))
		return
	case *Lambda:
		s.walk(x.Body, c.block("it is inside a lambda, which is a function of its own"))
		return
	case *Generate:
		s.walk(x.Body, c.block("it is inside a generator, which runs as a function of its own"))
		return
	case *Comptime:
		s.walk(x.Body, c.block("it is inside a comptime block"))
		return
	case *For:
		s.walk(x.Items, c.inner(tailUsed))
		s.walk(x.Body, c.inner("the loop continues after it returns"))
		return
	case *RecordLit:
		for _, field := range x.Fields {
			s.walk(field.Value, s.fieldCtx(c, field.Thunk != nil || field.Lazy != nil))
		}
		return
	case *Copy:
		s.walk(x.X, c.inner(tailUsed))
		for _, update := range x.Updates {
			s.walk(update.Value, s.fieldCtx(c, update.Thunk != nil || update.Lazy != nil))
		}
		return
	}
	// Anything else uses its parts' values.
	WalkComptime(x, func(y Expr) bool {
		if y == x {
			return true
		}
		s.walk(y, c.inner(tailUsed))
		return false
	})
}

// fieldCtx is where a field's value is: a lazy field's runs later, in
// a function of its own.
func (s *tailScan) fieldCtx(c tailCtx, lazy bool) tailCtx {
	if lazy {
		return c.block("it is inside a lazy field's initializer, which runs as a function of its own")
	}
	return c.inner(tailUsed)
}

// blockStmts walks a block: its statements are never in tail position,
// and its tail is where the block is, unless the block binds ambient
// values (a with) or puts a mock in force, which end after it.
func (s *tailScan) blockStmts(b *Block, c tailCtx) {
	inner := c
	for _, stmt := range b.Stmts {
		switch stmt := stmt.(type) {
		case *Let:
			if stmt.Var != nil && stmt.Var.Ambient != nil {
				inner = c.block(fmt.Sprintf("it is inside the with block at line %d, whose ambient values end after the call returns", b.Pos().Line))
			}
		case *Mock:
			inner = c.block(fmt.Sprintf("the mock at line %d is in force until its block ends, after the call returns", stmt.Pos.Line))
		}
	}
	for _, stmt := range b.Stmts {
		switch stmt := stmt.(type) {
		case *Let:
			s.walk(stmt.AsyncScope, inner.inner(tailUsed))
			if stmt.Initializer != nil {
				s.walk(stmt.Initializer, inner.block("it is inside a lazy or async initializer, which runs as a function of its own"))
			} else {
				s.walk(stmt.Value, inner.inner(tailUsed))
			}
		case *ExprStmt:
			s.walk(stmt.X, inner.inner("the function continues after it returns"))
		case *Trust:
			s.walk(stmt.Call, inner.inner(tailUsed))
		case *Mock:
			// A mock's body is a function of its own.
		}
	}
	s.walk(b.Tail, inner)
}

func (s *tailScan) selfCall(call *Call, c tailCtx) {
	s.recursive = true
	s.calls = append(s.calls, call)
	tc := &TailCall{}
	switch {
	case c.blocked != "":
		tc.Reason = c.blocked
	case !c.tail:
		tc.Reason = c.why
	case s.eligible != "":
		tc.Reason = s.eligible
	case !s.sameInstance(call):
		tc.Reason = "it calls " + s.fn.Decl.Name + " with other type arguments"
	case !s.sameNeeds(call):
		tc.Reason = "it does not pass its own ambient values on unchanged"
	default:
		tc.Jump = true
		s.info.tailJumps[s.fn] = true
	}
	s.info.TailCalls[call] = tc
}

func (s *tailScan) sameInstance(call *Call) bool {
	if call.Inst == nil || len(call.Inst.TypeArgs) != len(s.fn.TypeParams) {
		return call.Inst == nil && len(s.fn.TypeParams) == 0
	}
	for i, t := range call.Inst.TypeArgs {
		if t != Type(s.fn.TypeParams[i]) {
			return false
		}
	}
	return true
}

func (s *tailScan) sameNeeds(call *Call) bool {
	if len(call.Needs) != len(s.fn.NeedVars) {
		return false
	}
	for i, n := range call.Needs {
		ref, ok := n.(*VarRef)
		if !ok || ref.Var != s.fn.NeedVars[i] {
			return false
		}
	}
	return true
}

// mutualCycle describes a cycle of direct calls from fn back to fn
// through another function, or gives "".
func mutualCycle(fn *Func) string {
	type step struct {
		fn   *Func
		prev int
	}
	steps := []step{{fn, -1}}
	seen := map[*Func]bool{fn: true}
	for i := 0; i < len(steps); i++ {
		for _, callee := range steps[i].fn.Calls {
			if callee == fn && i > 0 {
				var path []string
				for j := i; j >= 0; j = steps[j].prev {
					path = append([]string{steps[j].fn.Decl.Name}, path...)
				}
				return path[0] + " calls " + strings.Join(append(path[1:], fn.Decl.Name), ", which calls ")
			}
			if seen[callee] || callee.Body == nil || callee.Pkg != fn.Pkg {
				continue
			}
			seen[callee] = true
			steps = append(steps, step{callee, i})
		}
	}
	return ""
}
