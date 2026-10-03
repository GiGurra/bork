package check

import "github.com/GiGurra/bork/internal/diag"

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
		case j < len(fn.Params) && fn.Params[j] != Scope && fn.Params[j] != OwnedScope && fn.Params[j] != Invalid:
			c.errorf(p.InPos, "parameter %s can only belong to a parameter of type Scope or OwnedScope, and %s has type %s", p.Name, p.In, fn.Params[j])
		default:
			fn.ParamIn[i] = j
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
		return l.block(x, func(t Expr) lifetime { return l.ownerValue(t, how) })
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
	return l.call(x.Func, true, x.Args, x.ArgOrder)
}

// block checks a block, its tail with tail. The owners bound in it must
// be gone where it ends, if it does.
func (l *lifeChecker) block(b *Block, tail func(Expr) lifetime) lifetime {
	l.owners = append(l.owners, nil)
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
				// prev: OwnedScope in app: app outlives prev's child.
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
			if short := l.shorter(args[j], target); short != nil {
				l.errorf(xargs[j].Pos(), "%s may not live as long as %s (it depends on %s), but %s declares parameter %s in %s", describe(xargs[j]), describe(xargs[t]), l.scopeText(short), fn.Decl.Name, fn.Decl.Params[j].Name, fn.Decl.Params[t].Name)
				args[j] = target
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
}

func copyGone(m map[*Var]goneAt) map[*Var]goneAt {
	out := make(map[*Var]goneAt, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (l *lifeChecker) fork() *branches {
	return &branches{l: l, start: copyGone(l.gone), mark: len(l.bound)}
}

// done ends a branch x (nil for an if's missing else), and starts the
// next one where the first started.
func (b *branches) done(x Expr) {
	if x == nil || x.Type() != Never {
		b.ends = append(b.ends, b.l.gone)
	}
	b.l.gone = copyGone(b.start)
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
}

// conditional checks x, which may not run (a match guard, or the right
// side of && or ||), with check: it cannot close or pass on an owner.
func (l *lifeChecker) conditional(x Expr, check func()) {
	before := copyGone(l.gone)
	mark := len(l.bound)
	check()
	for v := range l.gone {
		if _, ok := before[v]; !ok && l.bound[v] < mark {
			l.errorf(x.Pos(), "owned scope %s cannot be closed or passed on in a condition that may not run", v.Name)
			return
		}
	}
}
