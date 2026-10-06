package check

import (
	"fmt"
	"slices"

	"github.com/GiGurra/bork/internal/diag"
)

// Owned child scopes: `b = openScope(app)` opens a child scope of app
// that b owns, `closeScope(b)` ends it, and `b.scope` borrows its Scope.
// Their lifetimes may overlap without nesting: b can be opened while a
// is open, and stay open after a is closed.
//
// An owner is affine: it is consumed exactly once on every path that
// finishes normally, by closeScope, by passing it to a parameter of type
// OwnedScope, or by returning it, and it cannot be copied into anything
// (a record, a list, a lambda) that would make a second closer. A path
// that ends early (`?`, return, panic) closes the owners still open in
// the function, as a scope block's end does. These rules, and that a
// value of the child is not used once it is closed, are checked with the
// lifetimes (see lifetimes.go): the child of an owner is a scope of the
// values made with b.scope, open until the owner is consumed.
//
// A parameter can be declared to belong to another (`conn: Conn in
// prev`), so that a value of a child can be passed on with its owner.

// ownerSignature checks where a function's signature may hold an
// OwnedScope (only as a whole parameter or result), and resolves the
// `in` clauses of its parameters.
func (c *checker) ownerSignature(fn *Func) {
	fd := fn.Decl
	fn.ParamIn = make([]int, len(fd.Params))
	for i, p := range fd.Params {
		fn.ParamIn[i] = -1
		if i < len(fn.Params) && fn.Params[i] != OwnedScope && containsOwned(fn.Params[i]) {
			c.errorf(p.Type.Pos, "parameter %s cannot hold an OwnedScope inside another type; take the OwnedScope itself", p.Name)
		}
		if p.In == "" {
			continue
		}
		j := -1
		for k, q := range fd.Params {
			if q.Name == p.In {
				j = k
			}
		}
		switch {
		case j < 0:
			c.errorf(p.InPos, "%s is not a parameter of %s", p.In, fd.Name)
		case j == i:
			c.errorf(p.InPos, "parameter %s cannot belong to itself", p.Name)
		case j < len(fn.Params) && !holdsScope(fn.Params[j]):
			c.errorf(p.InPos, "parameter %s cannot belong to %s, of type %s, which holds no scope", p.Name, p.In, fn.Params[j])
		default:
			fn.ParamIn[i] = j
		}
	}
	// Parameters cannot belong to each other in a cycle.
	for i := range fn.ParamIn {
		j := fn.ParamIn[i]
		for steps := 0; j >= 0 && steps < len(fn.ParamIn); steps++ {
			if j == i {
				c.errorf(fd.Params[i].InPos, "parameter %s belongs to itself through other parameters' in clauses", fd.Params[i].Name)
				fn.ParamIn[i] = -1
				break
			}
			j = fn.ParamIn[j]
		}
	}
	if fn.Result != OwnedScope && containsOwned(fn.Result) {
		c.errorf(fd.Result.Pos, "%s cannot return an OwnedScope inside another type; return the OwnedScope itself", fd.Name)
	}
	if fd.IsGo() && !fn.Prelude {
		for i, t := range fn.Params {
			if containsOwned(t) {
				c.errorf(fd.Params[i].Type.Pos, "an unsafe go function cannot take an OwnedScope: Go code could keep or copy it")
			}
		}
		if containsOwned(fn.Result) {
			c.errorf(fd.Result.Pos, "an unsafe go function cannot return an OwnedScope")
		}
	}
}

// holdsScope reports whether a value of type t may belong to a scope,
// so that a parameter can be declared in it.
func holdsScope(t Type) bool {
	return t == Invalid || (&lifeChecker{carries: map[Type]bool{}}).carriesLife(t)
}

// keepsValues reports whether a value of type t can keep other values
// for later, or give a way to: a scope (its finalizers and tasks), a
// function (which may be or capture one, a channel, or an atom: records
// of functions), a Go value, or a type parameter (which may be any of
// them), or anything that holds one. Resources only belong to scopes.
func keepsValues(t Type) bool {
	return keepsValuesSeen(t, map[Type]bool{}, true)
}

// keepsValuesHere is keepsValues for values used in code generic in
// their type parameters, which cannot keep anything in a value of a
// type parameter: they know nothing about it.
func keepsValuesHere(t Type) bool {
	return keepsValuesSeen(t, map[Type]bool{}, false)
}

func keepsValuesSeen(t Type, seen map[Type]bool, typeParams bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t := t.(type) {
	case *TypeParam:
		return typeParams
	case *Opaque, *FuncType:
		return true
	case *Basic:
		return t == Scope || t == OwnedScope
	case *Record:
		// A channel (or a handoff) keeps what is sent to it, in its
		// native handle.
		if b, ok := genericBaseOrSelf(t).(*Record); ok && b.Prelude && (b.Name == "Channel" || b.Name == "Handoff") {
			return true
		}
		for _, f := range t.Fields {
			if f.Lazy || keepsValuesSeen(f.Type, seen, typeParams) {
				return true
			}
		}
	case *Sealed:
		for _, v := range t.Variants {
			for _, f := range v.Fields {
				if f.Lazy || keepsValuesSeen(f.Type, seen, typeParams) {
					return true
				}
			}
		}
	case *List:
		return keepsValuesSeen(t.Elem, seen, typeParams)
	case *Map:
		return keepsValuesSeen(t.Key, seen, typeParams) || keepsValuesSeen(t.Value, seen, typeParams)
	case *Union:
		for _, m := range t.Members {
			if keepsValuesSeen(m, seen, typeParams) {
				return true
			}
		}
	}
	return false
}

// keepsKeepers reports whether a value of type t may keep values that
// keep others: a channel or an atom of them, or what keepsValues cannot
// see into (a function, a Go value, a type parameter), or anything that
// holds one.
func keepsKeepers(t Type) bool {
	return keepsKeepersSeen(t, map[Type]bool{})
}

func keepsKeepersSeen(t Type, seen map[Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t := t.(type) {
	case *Opaque, *FuncType, *TypeParam:
		return true
	case *Record:
		if b, ok := genericBaseOrSelf(t).(*Record); ok && b.Prelude && (b.Name == "Channel" || b.Name == "Handoff" || b.Name == "Atom") {
			return keepsValues(storedType(t))
		}
		for _, f := range t.Fields {
			if keepsKeepersSeen(f.Type, seen) {
				return true
			}
		}
	case *Sealed:
		for _, v := range t.Variants {
			for _, f := range v.Fields {
				if keepsKeepersSeen(f.Type, seen) {
					return true
				}
			}
		}
	case *List:
		return keepsKeepersSeen(t.Elem, seen)
	case *Map:
		return keepsKeepersSeen(t.Key, seen) || keepsKeepersSeen(t.Value, seen)
	case *Union:
		for _, m := range t.Members {
			if keepsKeepersSeen(m, seen) {
				return true
			}
		}
	}
	return false
}

// storedType is the type of what a Channel[T] or an Atom[T] keeps.
func storedType(t Type) Type {
	if r, ok := t.(*Record); ok && len(r.Args) == 1 {
		return r.Args[0]
	}
	return Invalid
}

// containsOwned reports whether values of type t hold an OwnedScope.
func containsOwned(t Type) bool {
	return containsOwnedSeen(t, map[Type]bool{})
}

func containsOwnedSeen(t Type, seen map[Type]bool) bool {
	if t == OwnedScope {
		return true
	}
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch t := t.(type) {
	case *Seq:
		return containsOwnedSeen(t.Elem, seen)
	case *List:
		return containsOwnedSeen(t.Elem, seen)
	case *Map:
		return containsOwnedSeen(t.Key, seen) || containsOwnedSeen(t.Value, seen)
	case *Union:
		for _, m := range t.Members {
			if containsOwnedSeen(m, seen) {
				return true
			}
		}
	case *Record:
		for _, f := range t.Fields {
			if containsOwnedSeen(f.Type, seen) {
				return true
			}
		}
	case *Sealed:
		for _, v := range t.Variants {
			for _, f := range v.Fields {
				if containsOwnedSeen(f.Type, seen) {
					return true
				}
			}
		}
	case *FuncType:
		for _, p := range t.Params {
			if containsOwnedSeen(p, seen) {
				return true
			}
		}
		return containsOwnedSeen(t.Result, seen)
	}
	return false
}

// A child is the scope an owner variable owns: values made with its
// b.scope belong to it. It is open from where the owner is bound until
// the owner is closed or passed on.
type child struct{ owner *Var }

// goneAt is where an owner was closed or passed on, and how.
type goneAt struct {
	pos diag.Pos
	how string
}

func (l *lifeChecker) childOf(v *Var) *child {
	c := l.children[v]
	if c == nil {
		c = &child{owner: v}
		l.children[v] = c
	}
	return c
}

// bind records that the owner variable v is bound here.
func (l *lifeChecker) bind(v *Var) {
	l.bound[v] = len(l.bound)
	delete(l.gone, v)
}

// scopeOfVar is the lifetime of values of the scope variable v: its
// child if v is an owner, or v itself.
func (l *lifeChecker) scopeOfVar(v *Var) lifetime {
	if v.Type == OwnedScope {
		return lifetime{l.childOf(v)}
	}
	return lifetime{v}
}

// mustBeGone reports the owner v if it is still open where its block
// (or function) ends: its end must be explicit.
func (l *lifeChecker) mustBeGone(v *Var, end diag.Pos) {
	if _, ok := l.gone[v]; !ok {
		l.errorf(end, "owned scope %s is still open at the end of its block: close it with closeScope(%s), pass it on, or return it", v.Name, v.Name)
		l.gone[v] = goneAt{end, "closed"}
	}
}

// value checks an expression whose value is bound, passed or returned,
// how says which.
func (l *lifeChecker) value(x Expr, how string) lifetime {
	if x.Type() == OwnedScope {
		return l.ownerValue(x, how)
	}
	return l.expr(x)
}

// ownerValue checks an owner that is bound, passed or returned: a new
// one (from a call), or an owner variable, which is then gone.
func (l *lifeChecker) ownerValue(x Expr, how string) lifetime {
	switch x := x.(type) {
	case *VarRef:
		life, _ := l.ownerRef(x)
		if how == "bind" {
			l.errorf(x.Pos(), "owned scope %s cannot be bound again: there is one owner; use %s itself", x.Var.Name, x.Var.Name)
			return life
		}
		if _, ok := l.gone[x.Var]; !ok {
			l.gone[x.Var] = goneAt{x.Pos(), map[string]string{"return": "returned", "pass": "passed on"}[how]}
		}
		return life
	case *Block:
		return l.block(x, func(t Expr) lifetime {
			// The block's own owner is moved out of it: bound outside.
			if v, ok := t.(*VarRef); ok && how == "bind" && slices.Contains(l.owners[len(l.owners)-1], v.Var) {
				return l.ownerValue(t, "pass")
			}
			return l.ownerValue(t, how)
		})
	case *Call:
		return l.callLife(x)
	}
	l.errorf(x.Pos(), "an owned scope cannot be the value of an if, a match or a scope block: close, pass on or return it in each branch instead")
	return nil
}

// ownerRef checks a use of the owner variable x: it must be this
// function's, and not closed or passed on yet. It gives the owner's
// lifetime (that of its parent), and false if it may not be used.
func (l *lifeChecker) ownerRef(x *VarRef) (lifetime, bool) {
	if l.frame[x.Var] != l.cur {
		l.errorf(x.Pos(), "a lambda cannot close or pass on owned scope %s; it can borrow %s.scope", x.Var.Name, x.Var.Name)
		return nil, false
	}
	if g, ok := l.gone[x.Var]; ok {
		l.errorf(x.Pos(), "owned scope %s was already %s at line %d", x.Var.Name, g.how, g.pos.Line)
		return nil, false
	}
	return l.use(x, l.env[x.Var]), true
}

// callLife checks a call of a declared function: b.scope (scopeOf(b))
// borrows the child of b, and gives values of it.
func (l *lifeChecker) callLife(x *Call) lifetime {
	if x.Func.Prelude && x.Func.Decl.Name == "move" && len(x.Args) == 2 {
		return l.moveCall(x)
	}
	if handoffMethod(x.Func) && x.Func.Decl.Name == "handOver" && len(x.Args) == 3 {
		return l.handOverCall(x)
	}
	if x.Func.Prelude && x.Func.Decl.Name == "handoff" {
		if _, ok := storedType(x.Type()).(*Resource); !ok {
			l.errorf(x.Pos(), "a handoff passes resources only, not %s; use a channel", TypeText(storedType(x.Type()), nil))
		}
	}
	// Policies are given where a scope starts (with, or openScope's), so
	// that moves can tell whether its tasks may outlive it.
	if x.Func.Prelude && x.Func.Decl.Name == "setScopePolicy" {
		l.errorf(x.Pos(), "setScopePolicy cannot be called directly; give policies where the scope starts: scope s with taskTimeout(100) { ... }, or openScope(parent, [taskTimeout(100)])")
	}
	if x.Func.Prelude && x.Func.Decl.Name == "scopeOf" && len(x.Args) == 1 {
		v, ok := x.Args[0].(*VarRef)
		if !ok {
			l.errorf(x.Args[0].Pos(), "an owned scope must be bound before its scope is borrowed: b = ..., then b.scope")
			l.expr(x.Args[0])
			return nil
		}
		// A lambda may borrow it too: it then belongs to the child, so it
		// cannot run once the owner is gone.
		if g, ok := l.gone[v.Var]; ok {
			l.errorf(v.Pos(), "owned scope %s was already %s at line %d", v.Var.Name, g.how, g.pos.Line)
			return nil
		}
		life := lifetime{l.childOf(v.Var)}
		for _, c := range l.captures {
			*c = c.union(life)
		}
		return life
	}
	life := l.call(x.Func, true, x.Args, x.ArgOrder).union(l.captureLife(x.Captures))
	// An acquisition, or an attach to a simple scope, gives a new
	// registration, which may be moved.
	var h *handle
	if x.Func.Prelude && x.Func.Decl.Name == "attach" && len(x.Args) == 2 {
		if r, ok := x.Args[0].Type().(*Resource); ok {
			h = l.newHandle(r, x.Args[1], x.Pos())
		}
	} else if h = l.received(x); h == nil {
		h = l.acquisition(x)
	}
	if h != nil {
		l.acquired[x] = h
		life = life.union(lifetime{h})
	}
	return life
}

// block checks a block, its tail with tail. The owners bound in it must
// be gone where it ends, if it does.
func (l *lifeChecker) block(b *Block, tail func(Expr) lifetime) lifetime {
	l.owners = append(l.owners, nil)
	defined := len(l.defined)
	for _, s := range b.Stmts {
		l.stmt(s)
	}
	var life lifetime
	if b.Tail != nil {
		life = tail(b.Tail)
	}
	bound := l.owners[len(l.owners)-1]
	l.owners = l.owners[:len(l.owners)-1]
	if b.Type() != Never {
		for _, v := range bound {
			l.mustBeGone(v, b.End)
		}
	}
	l.settle(defined)
	return life
}

// ownerArgs checks a call whose arguments' evaluation consumed the
// owners in gone (closed or passed on: inside an argument, or as one,
// moved by argument), and the parameters fn (nil for a builtin or a
// function value) declares in another. No other argument may belong to
// the child of a consumed owner, as the callee would use it after the
// child was closed (or while the callee may close it), unless the
// parameter is declared in that owner's: the callee then checks it. It
// gives the moved owners, which the call consumes.
func (l *lifeChecker) ownerArgs(fn *Func, xargs []Expr, args []lifetime, moved map[int]*Var, gone []*Var) {
	declaredIn := func(j int, m *Var) bool {
		if fn == nil || j >= len(fn.ParamIn) || fn.ParamIn[j] < 0 {
			return false
		}
		return moved[fn.ParamIn[j]] == m
	}
	if fn != nil {
		for j, t := range fn.ParamIn {
			if t < 0 || j >= len(args) || t >= len(args) {
				continue
			}
			target := args[t]
			if m, ok := moved[t]; ok {
				target = lifetime{l.childOf(m)}
			}
			if fn.Params[j] == OwnedScope {
				// prev: OwnedScope in app: app outlives prev's child. A new
				// owner (not a variable) outlives no existing child.
				if fn.Params[t] == OwnedScope && moved[t] == nil {
					l.errorf(xargs[t].Pos(), "parameter %s of %s is declared in %s, so give %s as an owner variable, whose scope it belongs to", fn.Decl.Params[j].Name, fn.Decl.Name, fn.Decl.Params[t].Name, fn.Decl.Params[t].Name)
					continue
				}
				child := args[j]
				if m, ok := moved[j]; ok {
					child = lifetime{l.childOf(m)}
				}
				for _, x := range target {
					if !l.outlivesAll(x, child) {
						l.errorf(xargs[j].Pos(), "the scope of %s may not live as long as %s (%s may end first), but %s declares parameter %s in %s", describe(xargs[j]), describe(xargs[t]), l.scopeText(x), fn.Decl.Name, fn.Decl.Params[j].Name, fn.Decl.Params[t].Name)
						break
					}
				}
				continue
			}
			l.pin(args[j], xargs[j].Pos(), fmt.Sprintf("parameter %s of %s, declared in %s,", fn.Decl.Params[j].Name, fn.Decl.Name, fn.Decl.Params[t].Name), target, false)
			if short := l.storeShorter(args[j], target); short != nil {
				l.errorf(xargs[j].Pos(), "%s may not live as long as %s (it depends on %s), but %s declares parameter %s in %s", describe(xargs[j]), describe(xargs[t]), l.scopeText(short), fn.Decl.Name, fn.Decl.Params[j].Name, fn.Decl.Params[t].Name)
				args[j] = target
				continue
			}
			// The callee may keep a value that keeps others in what it is
			// declared in, which gives it back with its own lifetime (see
			// call): it must live exactly as long.
			if keepsValues(xargs[j].Type()) && keepsKeepers(xargs[t].Type()) && len(args[j]) > 0 {
				for _, y := range target {
					if !l.outlivesAll(y, args[j]) {
						l.errorf(xargs[j].Pos(), "%s may live longer than %s, which %s may keep it in: what comes out would be treated as ending with %s, and could be given its values; give one of the same lifetime", describe(xargs[j]), describe(xargs[t]), fn.Decl.Name, describe(xargs[t]))
						break
					}
				}
			}
		}
	}
	for _, m := range gone {
		c := l.childOf(m)
	args:
		for j := range args {
			if moved[j] == m || declaredIn(j, m) {
				continue
			}
			for _, e := range args[j] {
				if l.scopeOutlives(c, e) {
					callee := "the function"
					if fn != nil {
						callee = fn.Decl.Name
					}
					if i := movedIndex(moved, m); i >= 0 && fn != nil {
						l.errorf(xargs[j].Pos(), "%s belongs to owned scope %s, which %s takes and may close while it still uses this; declare its parameter in the owner's (%s: ... in %s)", describe(xargs[j]), m.Name, callee, paramName(fn, j), paramName(fn, i))
					} else {
						l.errorf(xargs[j].Pos(), "%s belongs to owned scope %s, which is closed or passed on before %s gets it", describe(xargs[j]), m.Name, callee)
					}
					break args
				}
			}
		}
	}
	for i, m := range moved {
		how := "passed on"
		if fn != nil && fn.Prelude && fn.Decl.Name == "closeScope" {
			how = "closed"
		}
		l.gone[m] = goneAt{xargs[i].Pos(), how}
	}
}

func movedIndex(moved map[int]*Var, m *Var) int {
	for i, v := range moved {
		if v == m {
			return i
		}
	}
	return -1
}

// outlivesAll reports whether the scope x outlives every scope of life
// (and life has some).
func (l *lifeChecker) outlivesAll(x any, life lifetime) bool {
	for _, y := range life {
		if !l.scopeOutlives(x, y) {
			return false
		}
	}
	return len(life) > 0
}

func paramName(fn *Func, i int) string {
	if i < len(fn.Decl.Params) {
		return fn.Decl.Params[i].Name
	}
	return "?"
}

// branches tracks the owners closed on the branches of an if or match:
// those open before it must be closed on all branches that finish, or
// on none.
type branches struct {
	l     *lifeChecker
	start map[*Var]goneAt
	mark  int
	ends  []map[*Var]goneAt
	// The moves and pins of handles, likewise (see joinMoves).
	startMoved map[*handle]movedAt
	movedEnds  []map[*handle]movedAt
	startPins  map[*handle][]pinAt
	pinEnds    []map[*handle][]pinAt
}

func copyGone(m map[*Var]goneAt) map[*Var]goneAt {
	out := make(map[*Var]goneAt, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (l *lifeChecker) fork() *branches {
	return &branches{l: l, start: copyGone(l.gone), mark: len(l.bound), startMoved: copyMoved(l.moved), startPins: copyPins(l.pins)}
}

// done ends a branch x (nil for an if's missing else), and starts the
// next one where the first started.
func (b *branches) done(x Expr) {
	if x == nil || x.Type() != Never {
		b.ends = append(b.ends, b.l.gone)
		b.movedEnds = append(b.movedEnds, b.l.moved)
	}
	// A branch that ends early may still go on after a loop (break,
	// continue): keep its pins.
	b.pinEnds = append(b.pinEnds, b.l.pins)
	b.l.gone = copyGone(b.start)
	b.l.moved = copyMoved(b.startMoved)
	b.l.pins = copyPins(b.startPins)
}

func (b *branches) join(pos diag.Pos, what string) {
	out := b.start
	for _, end := range b.ends {
		for v, g := range end {
			if _, ok := b.start[v]; ok || b.l.bound[v] >= b.mark {
				continue
			}
			if _, ok := out[v]; ok {
				continue
			}
			out[v] = g
			for _, other := range b.ends {
				if _, ok := other[v]; !ok {
					b.l.errorf(pos, "owned scope %s is %s on only some paths of this %s (line %d); close or pass it on in every branch that goes on", v.Name, g.how, what, g.pos.Line)
					break
				}
			}
		}
	}
	b.l.gone = out
	b.l.moved, b.l.pins = joinMoves(b.startMoved, b.movedEnds, b.startPins, b.pinEnds)
}

// conditional checks x, which may not run (a match guard, or the right
// side of && or ||), with check: it cannot close or pass on an owner.
func (l *lifeChecker) conditional(x Expr, check func()) {
	before := copyGone(l.gone)
	mark := len(l.bound)
	movedBefore := copyMoved(l.moved)
	check()
	// What it moves is possibly moved.
	for _, h := range l.movesSince(movedBefore) {
		m := l.moved[h]
		m.possibly = true
		l.moved[h] = m
	}
	for v := range l.gone {
		if _, ok := before[v]; !ok && l.bound[v] < mark {
			l.errorf(x.Pos(), "owned scope %s cannot be closed or passed on in a condition that may not run", v.Name)
			return
		}
	}
}

// storeFields reports (as an error) a use of the fields of a Channel or
// an Atom outside the prelude: they store values, and the lifetimes
// check what is stored through send, update and swap only.
func (c *checker) storeFields(pos diag.Pos, rec *Record) bool {
	if c.inPrelude {
		return false
	}
	base := genericBaseOrSelf(rec)
	for name, uses := range map[string]string{"Channel": "methods (send, receive, close, ...)", "Handoff": "methods (handOver, receive, close, ...)", "Atom": "functions (update, current, swap)"} {
		if base == genericBaseOrSelf(c.preludePkg.TypeNamed(name)) {
			c.errorf(pos, "the fields of %s are internal; use its %s", name, uses)
			return true
		}
	}
	return false
}
