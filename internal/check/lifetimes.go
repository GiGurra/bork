package check

import (
	"fmt"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Lifetimes checks that values never outlive the scopes they belong to.
//
// A resource belongs to the scope it was opened in: a function that
// takes a scope returns values that live as long as it. So do values
// that hold such a value, such as a record with a resource field, or a
// lambda that uses one. A value's lifetime is the set of scopes that
// must all be open for it to be usable. The scopes are `scope s { ... }`
// blocks, and parameters: whatever a function is given lives as long as
// the call, at least.
//
// The checks:
//   - a value whose scope has ended is "possibly released", and may not
//     be used;
//   - a function (or lambda) may not return a value of a scope it opened;
//   - an `unsafe go` function that takes a scope may keep its other
//     arguments until that scope closes, so they must live as long as it.
//
// bork values are immutable and there is nothing global to store them
// in, so these are the only ways for a value to escape its scope.
func Lifetimes(files []*syntax.File, info *Info, diags *diag.List) {
	info.Lifetimes = map[Expr][]string{}
	info.VarLifetimes = map[*Var][]string{}
	l := &lifeChecker{
		info:     info,
		diags:    diags,
		env:      map[*Var]lifetime{},
		frame:    map[any]any{},
		parent:   map[any]any{},
		carries:  map[Type]bool{},
		children: map[*Var]*child{},
		gone:     map[*Var]goneAt{},
		bound:    map[*Var]int{},
	}
	for _, f := range files {
		for _, fd := range f.Funcs {
			if fn := info.FuncOf[fd]; fn != nil && fn.Body != nil {
				l.function(fn)
			}
		}
	}
	for _, fn := range info.Tests {
		l.function(fn)
	}
}

// A lifetime is a set of scopes: a value is usable while all of them
// are open. Each scope is a *ScopeBlock, a *Var of a parameter (the
// scope of a function's or lambda's caller), or a *child: the scope an
// owner variable owns (see owners.go). The empty lifetime is forever.
type lifetime []any

func (a lifetime) union(b lifetime) lifetime {
	out := a
	for _, x := range b {
		if !a.has(x) {
			out = append(out[:len(out):len(out)], x)
		}
	}
	return out
}

func (a lifetime) has(x any) bool {
	for _, y := range a {
		if y == x {
			return true
		}
	}
	return false
}

type lifeChecker struct {
	info  *Info
	diags *diag.List
	// env holds the lifetime of each variable (a scope's lives as long
	// as its scope block).
	env map[*Var]lifetime
	// open lists the scope blocks around the current point.
	open []*ScopeBlock
	// enclosing holds, for each scope block, the scopes open when it
	// opened; they outlive it.
	enclosing map[*ScopeBlock][]*ScopeBlock
	// frame is the function or lambda each scope and parameter belongs
	// to, and parent each lambda's enclosing frame.
	frame, parent map[any]any
	cur           any // the current frame
	// captures collects the lifetimes of the values used inside each
	// lambda being checked, which become the lambda's lifetime.
	captures []*lifetime
	carries  map[Type]bool
	// children holds the child scope of each owner variable, gone the
	// owners already closed or passed on, and bound the order in which
	// owner variables were bound (to tell, at the end of a branch, those
	// bound before it). owners lists the owner variables bound in each
	// block being checked.
	children map[*Var]*child
	gone     map[*Var]goneAt
	bound    map[*Var]int
	owners   [][]*Var
}

func (l *lifeChecker) errorf(pos diag.Pos, format string, args ...any) {
	l.diags.AddCode(pos, "lifetime.error", format, args...)
}

func (l *lifeChecker) function(fn *Func) {
	l.cur = fn
	l.open = nil
	l.enclosing = map[*ScopeBlock][]*ScopeBlock{}
	for _, p := range fn.ParamVars {
		l.frame[p] = fn
		l.env[p] = lifetime{p}
		if p.Type == OwnedScope {
			l.bind(p)
		}
	}
	// A parameter declared in another (`conn: Conn in prev`) belongs to
	// its scope: an owner's child, or a Scope parameter's.
	for i, p := range fn.ParamVars {
		if i < len(fn.ParamIn) && fn.ParamIn[i] >= 0 {
			l.env[p] = l.scopeOfVar(fn.ParamVars[fn.ParamIn[i]])
		}
	}
	for _, p := range fn.ParamVars {
		if l.carriesLife(p.Type) {
			l.info.VarLifetimes[p] = l.lifeText(l.env[p])
		}
	}
	l.result(fn.Body, l.value(fn.Body, "return"), l.what())
	if fn.Body.Type() != Never {
		for _, p := range fn.ParamVars {
			if p.Type == OwnedScope {
				l.mustBeGone(p, fn.Body.End)
			}
		}
	}
}

// result checks that a function's or lambda's result does not belong to
// a scope opened inside it.
func (l *lifeChecker) result(x Expr, life lifetime, what string) {
	for _, s := range life {
		if s, ok := s.(*ScopeBlock); ok && l.within(s, l.cur) {
			l.errorf(valuePos(x), "%s cannot return this value: it belongs to scope %s, which ends on line %d", what, s.Var.Name, s.Body.End.Line)
			return
		}
		// The child of an owner here is closed by the time it returns,
		// unless the owner is returned instead.
		if c, ok := s.(*child); ok && l.within(c.owner, l.cur) {
			l.errorf(valuePos(x), "%s cannot return this value: it belongs to owned scope %s, which is closed when %s returns", what, c.owner.Name, strings.TrimPrefix(what, "function "))
			return
		}
	}
}

// within reports whether the scope or parameter x belongs to the frame
// f, or to a lambda inside it.
func (l *lifeChecker) within(x, f any) bool {
	if c, ok := x.(*child); ok {
		x = c.owner
	}
	for g := l.frame[x]; g != nil; g = l.parent[g] {
		if g == f {
			return true
		}
	}
	return false
}

// use checks that a value about to be used has not been released, and
// returns its lifetime (nil if it was, to avoid follow-up errors).
func (l *lifeChecker) use(x Expr, life lifetime) lifetime {
	switch s := l.closed(life).(type) {
	case *ScopeBlock:
		if _, ok := x.(*ScopeBlock); ok {
			l.errorf(x.Pos(), "the value of this scope block belongs to scope %s, which has ended; use it inside the block", s.Var.Name)
		} else {
			l.errorf(x.Pos(), "%s may be released: it belongs to scope %s, which ended on line %d", describe(x), s.Var.Name, s.Body.End.Line)
		}
		return nil
	case *child:
		g := l.gone[s.owner]
		l.errorf(x.Pos(), "%s may be released: it belongs to owned scope %s, which was %s at line %d", describe(x), s.owner.Name, g.how, g.pos.Line)
		return nil
	}
	return life
}

// closed is a scope of life that has ended (a *ScopeBlock or a *child),
// or nil.
func (l *lifeChecker) closed(life lifetime) any {
	for _, s := range life {
		switch s := s.(type) {
		case *ScopeBlock:
			if !l.isOpen(s) {
				return s
			}
		case *child:
			if _, ok := l.gone[s.owner]; ok {
				return s
			}
			// Closing an owner closes its children too.
			if inner := l.closed(l.env[s.owner]); inner != nil {
				return inner
			}
		}
	}
	return nil
}

func (l *lifeChecker) isOpen(s *ScopeBlock) bool {
	for _, o := range l.open {
		if o == s {
			return true
		}
	}
	return false
}

func describe(x Expr) string {
	switch x := x.(type) {
	case *VarRef:
		return x.Var.displayName()
	case *FuncRef:
		return x.Name
	case *Lambda:
		return "the lambda"
	case *ScopeBlock:
		return "the value of scope " + x.Var.Name
	case *Call:
		if n := scopeName(x); n != "(a scope)" {
			return n
		}
	}
	return "this value"
}

// valuePos is where the value of x comes from: the tail of a block.
func valuePos(x Expr) diag.Pos {
	if b, ok := x.(*Block); ok && b.Tail != nil {
		return valuePos(b.Tail)
	}
	return x.Pos()
}

// shorter returns a scope of lifetime a that may end before lifetime b
// does, or nil if a value of lifetime a is usable wherever one of
// lifetime b is (each scope of a outlives some scope of b).
func (l *lifeChecker) shorter(a, b lifetime) any {
	for _, x := range a {
		ok := false
		for _, y := range b {
			if l.scopeOutlives(x, y) {
				ok = true
				break
			}
		}
		if !ok {
			return x
		}
	}
	return nil
}

// scopeText names a scope of a lifetime, for messages.
func (l *lifeChecker) scopeText(x any) string {
	switch x := x.(type) {
	case *ScopeBlock:
		return "scope " + x.Var.Name
	case *Var:
		return "parameter " + x.Name
	case *child:
		return "owned scope " + x.owner.Name
	}
	return "?"
}

func (l *lifeChecker) scopeOutlives(x, y any) bool {
	if x == y {
		return true
	}
	// A child scope is outlived by its parent, and what outlives that:
	// the scopes its owner belongs to.
	if c, ok := y.(*child); ok {
		return l.outlivesAll(x, l.env[c.owner])
	}
	switch x := x.(type) {
	case *Var:
		// A caller's scope outlives the scopes its callee opens.
		s, ok := y.(*ScopeBlock)
		return ok && l.within(s, l.frame[x])
	case *ScopeBlock:
		s, ok := y.(*ScopeBlock)
		if !ok {
			return false
		}
		for _, e := range l.enclosing[s] {
			if e == x {
				return true
			}
		}
	}
	return false
}

// carriesLife reports whether values of type t can belong to a scope:
// scopes, resources, functions (which may use them), type parameters
// (which may be them), and anything that can hold one of those.
func (l *lifeChecker) carriesLife(t Type) bool {
	return l.carriesLifeSeen(t, map[Type]bool{})
}
func (l *lifeChecker) carriesLifeSeen(t Type, seen map[Type]bool) bool {
	if v, ok := l.carries[t]; ok {
		return v
	}
	if seen[t] {
		return false
	}
	seen[t] = true
	defer delete(seen, t)
	v := false
	switch t := t.(type) {
	case *Resource, *Opaque, *FuncType, *Seq, *TypeParam:
		v = true
	case *Basic:
		v = t == Scope || t == OwnedScope
	case *List:
		v = l.carriesLifeSeen(t.Elem, seen)
	case *Map:
		v = l.carriesLifeSeen(t.Key, seen) || l.carriesLifeSeen(t.Value, seen)
	case *Union:
		for _, m := range t.Members {
			v = v || l.carriesLifeSeen(m, seen)
		}
	case *Record:
		for _, f := range t.Fields {
			v = v || l.carriesLifeSeen(f.Type, seen)
		}
	case *Sealed:
		for _, vt := range t.Variants {
			for _, f := range vt.Fields {
				v = v || l.carriesLifeSeen(f.Type, seen)
			}
		}
	}
	// A false answer while traversing a cycle is provisional. Only memoize
	// positive answers, so a later query can reach a resource or opaque field.
	if v {
		l.carries[t] = true
	}
	return v
}

// expr checks an expression and returns its lifetime.
func (l *lifeChecker) expr(x Expr) lifetime {
	if t := x.Type(); t != OwnedScope && containsOwned(t) {
		l.errorf(x.Pos(), "a value of type %s cannot hold an OwnedScope: an owner cannot be copied into a list, record, union or function", t)
		return nil
	}
	life := l.exprLife(x)
	if t := x.Type(); t == nil || !l.carriesLife(t) {
		return nil
	}
	if len(life) > 0 {
		l.info.Lifetimes[x] = l.lifeText(life)
	}
	return life
}

// lifeText names the scopes of a lifetime, for queries.
func (l *lifeChecker) lifeText(life lifetime) []string {
	var out []string
	for _, s := range life {
		out = append(out, l.scopeText(s))
	}
	return out
}

func (l *lifeChecker) exprLife(x Expr) lifetime {
	switch x := x.(type) {
	case *VarRef:
		if !l.carriesLife(x.Type()) {
			return nil
		}
		if x.Type() == OwnedScope {
			l.errorf(x.Pos(), "owned scope %s can only be closed (closeScope(%s)), passed to a parameter of type OwnedScope, or returned; %s.scope borrows its Scope", x.Var.Name, x.Var.Name, x.Var.Name)
			return nil
		}
		life := l.env[x.Var]
		for _, c := range l.captures {
			*c = c.union(life)
		}
		return l.use(x, life)
	case *Block:
		return l.block(x, l.expr)
	case *ScopeBlock:
		for _, p := range x.Policies {
			l.use(p, l.expr(p))
		}
		l.enclosing[x] = append([]*ScopeBlock(nil), l.open...)
		l.frame[x] = l.cur
		l.env[x.Var] = lifetime{x}
		l.open = append(l.open, x)
		life := l.expr(x.Body)
		l.open = l.open[:len(l.open)-1]
		// The value may belong to x, which has now ended: it is possibly
		// released, which is reported where it is used.
		return life
	case *If:
		l.use(x.Cond, l.expr(x.Cond))
		b := l.fork()
		life := l.expr(x.Then)
		b.done(x.Then)
		if x.Else != nil {
			life = life.union(l.expr(x.Else))
			b.done(x.Else)
		} else {
			b.done(nil)
		}
		b.join(x.Pos(), "if")
		return life
	case *Match:
		subject := l.use(x.X, l.expr(x.X))
		var life lifetime
		b := l.fork()
		for _, arm := range x.Arms {
			l.bindPattern(arm.Pat, subject)
			for _, guard := range arm.Pat.Guards() {
				l.conditional(guard, func() { l.use(guard, l.expr(guard)) })
			}
			life = life.union(l.expr(arm.Body))
			b.done(arm.Body)
		}
		b.join(x.Pos(), "match")
		return life
	case *Return:
		if x.Value != nil {
			l.result(x.Value, l.use(x.Value, l.value(x.Value, "return")), l.what())
		}
		return nil
	case *Try:
		life := l.expr(x.X)
		for _, m := range x.Rest {
			if l.carriesLife(m) {
				// Other members are returned from the function.
				l.result(x.X, l.use(x.X, life), l.what())
				break
			}
		}
		return life
	case *SeqCall:
		var life lifetime
		for _, a := range x.Args {
			life = life.union(l.use(a, l.expr(a)))
		}
		return life
	case *Generate:
		return l.generate(x)
	case *Yield:
		l.result(x.Value, l.use(x.Value, l.expr(x.Value)), "the generator")
		return nil
	case *For:
		l.env[x.Var] = l.use(x.Items, l.expr(x.Items))
		l.frame[x.Var] = l.cur
		before := copyGone(l.gone)
		mark := len(l.bound)
		l.expr(x.Body)
		for owner, gone := range l.gone {
			if _, ok := before[owner]; !ok && l.bound[owner] < mark {
				l.errorf(gone.pos, "a loop cannot consume owned scope %s from outside its body; borrow its scope instead", owner.Name)
			}
		}
		l.gone = before
		return nil
	case *LoopControl:
		return nil
	case *Lambda:
		return l.lambda(x)
	case *Call:
		life := l.callLife(x)
		if x.Type() == OwnedScope {
			l.errorf(x.Pos(), "this owned scope is dropped: bind it (b = ...) and close it with closeScope(b), or pass it on")
		}
		return life
	case *CallBuiltin:
		return l.call(nil, true, x.Args)
	case *CallValue:
		life := l.use(x.Fun, l.expr(x.Fun))
		args := l.call(nil, false, x.Args)
		// The function runs after its arguments, which may have closed an
		// owner it belongs to.
		if len(life) > 0 {
			life = l.use(x.Fun, life)
		}
		return life.union(args)
	case *Select:
		return l.use(x.X, l.expr(x.X))
	case *RecordLit:
		var life lifetime
		for _, f := range x.Fields {
			life = life.union(l.expr(f.Value))
		}
		return life
	case *Copy:
		life := l.expr(x.X)
		for _, u := range x.Updates {
			life = life.union(l.expr(u.Value))
		}
		return life
	case *ListLit:
		var life lifetime
		for _, e := range x.Elems {
			life = life.union(l.expr(e))
		}
		return life
	case *MapLit:
		var life lifetime
		for i := range x.Keys {
			life = life.union(l.expr(x.Keys[i])).union(l.expr(x.Values[i]))
		}
		return life
	case *Unary:
		return l.expr(x.X)
	case *Binary:
		l.use(x.X, l.expr(x.X))
		if x.Op == syntax.AndAnd || x.Op == syntax.OrOr {
			l.conditional(x.Y, func() { l.use(x.Y, l.expr(x.Y)) })
		} else {
			l.use(x.Y, l.expr(x.Y))
		}
		return nil
	case *Interp:
		for _, e := range x.Exprs {
			l.use(e, l.expr(e))
		}
	}
	return nil
}

// what names the current frame, for messages.
func (l *lifeChecker) what() string {
	if fn, ok := l.cur.(*Func); ok {
		if fn.Test != nil {
			return "the test"
		}
		if fn.MockOf != nil {
			return "the mock of " + fn.Decl.Name
		}
		return "function " + fn.Decl.Name
	}
	return "the lambda"
}

func (l *lifeChecker) stmt(s Stmt) {
	switch s := s.(type) {
	case *Let:
		if s.Var.Type == OwnedScope {
			if s.Var.Name == "_" {
				l.errorf(s.Pos, "an owned scope cannot be dropped: bind it and close it with closeScope, or pass it on")
				l.ownerValue(s.Value, "pass")
				return
			}
			l.env[s.Var] = l.ownerValue(s.Value, "bind")
			if _, ok := s.Value.(*VarRef); ok {
				return // rebinding is the error
			}
			l.info.VarLifetimes[s.Var] = l.lifeText(l.env[s.Var])
			l.frame[s.Var] = l.cur
			l.bind(s.Var)
			if n := len(l.owners); n > 0 {
				l.owners[n-1] = append(l.owners[n-1], s.Var)
			}
			return
		}
		// A possibly released value can be bound; using it is the error.
		l.env[s.Var] = l.expr(s.Value)
		if len(l.env[s.Var]) > 0 {
			l.info.VarLifetimes[s.Var] = l.lifeText(l.env[s.Var])
		}
	case *ExprStmt:
		l.use(s.X, l.expr(s.X))
	case *Trust:
		l.use(s.Call, l.expr(s.Call))
	case *Mock:
		l.mock(s)
	}
}

// bindPattern gives the names a pattern binds the lifetime of the value
// it matches.
func (l *lifeChecker) bindPattern(p *Pat, life lifetime) {
	if p == nil {
		return
	}
	if p.Var != nil {
		l.env[p.Var] = life
	}
	for _, f := range p.Fields {
		l.bindPattern(f.Pat, life)
	}
	for _, e := range p.Elems {
		l.bindPattern(e, life)
	}
	l.bindPattern(p.Rest, life)
	l.bindPattern(p.Sub, life)
}

func (l *lifeChecker) lambda(x *Lambda) lifetime {
	saved, savedOpen := l.cur, l.open
	l.parent[x] = l.cur
	l.cur = x
	for _, p := range x.Params {
		l.frame[p] = x
		l.env[p] = lifetime{p}
	}
	var used lifetime
	l.captures = append(l.captures, &used)
	l.result(x.Body, l.use(x.Body, l.expr(x.Body)), "the lambda")
	l.captures = l.captures[:len(l.captures)-1]
	l.cur, l.open = saved, savedOpen
	// The lambda lives as long as what it uses from outside.
	var life lifetime
	for _, s := range used {
		if !l.within(s, x) {
			life = life.union(lifetime{s})
		}
	}
	return life
}

// mock checks a mock's body as a lambda written at the mock statement:
// it may use what is alive there, since the mock ends with its block
// (after the calls of it still running have finished).
func (l *lifeChecker) mock(m *Mock) {
	saved := l.cur
	l.parent[m.Func] = l.cur
	l.cur = m.Func
	for _, p := range m.Func.ParamVars {
		l.frame[p] = m.Func
		l.env[p] = lifetime{p}
	}
	life := l.use(m.Func.Body, l.expr(m.Func.Body))
	l.result(m.Func.Body, life, l.what())
	// Callers give the result the lifetime the target's signature
	// implies: that of the arguments. So it may not hold what the mock
	// captured from the test.
	for _, x := range life {
		switch x := x.(type) {
		case *ScopeBlock:
			if !l.within(x, m.Func) {
				l.errorf(valuePos(m.Func.Body), "the mock of %s cannot return this value: it belongs to scope %s of the test, but callers of %s expect a result that lives as long as its arguments", m.Text, x.Var.Name, m.Text)
				l.cur = saved
				return
			}
		case *Var:
			if !isParamOf(x, m.Func) {
				l.errorf(valuePos(m.Func.Body), "the mock of %s cannot return this value: it belongs to the scope of %s, but callers of %s expect a result that lives as long as its arguments", m.Text, x.Name, m.Text)
				l.cur = saved
				return
			}
		}
	}
	l.cur = saved
}

// call checks the arguments of a call, and returns the lifetime of its
// result. The callee is fn, a builtin (direct, with fn nil), or a
// function value (neither).
func (l *lifeChecker) call(fn *Func, direct bool, xargs []Expr, order ...[]int) lifetime {
	var life lifetime
	args := make([]lifetime, len(xargs))
	indices := make([]int, len(xargs))
	for i := range indices {
		indices[i] = i
	}
	if len(order) > 0 && order[0] != nil {
		indices = order[0]
	}
	// moved holds the owner variables the call takes, by argument.
	moved := map[int]*Var{}
	goneBefore := copyGone(l.gone)
	for _, i := range indices {
		a := xargs[i]
		if fn != nil && a.Type() == OwnedScope && i < len(fn.Params) && fn.Params[i] == OwnedScope {
			if v, ok := a.(*VarRef); ok {
				var usable bool
				args[i], usable = l.ownerRef(v)
				for _, w := range moved {
					if w == v.Var {
						l.errorf(a.Pos(), "owned scope %s is passed twice", v.Var.Name)
						usable = false
					}
				}
				if usable {
					moved[i] = v.Var
				}
			} else {
				args[i] = l.ownerValue(a, "pass")
			}
		} else {
			args[i] = l.use(a, l.expr(a))
		}
		life = life.union(args[i])
	}
	// The owners its arguments consumed, and those it takes.
	var consumed []*Var
	for v := range l.gone {
		if _, ok := goneBefore[v]; !ok {
			consumed = append(consumed, v)
		}
	}
	for _, v := range moved {
		consumed = append(consumed, v)
	}
	l.ownerArgs(fn, xargs, args, moved, consumed)
	// An owner the call returns does not belong to the children it
	// consumed: its callee could not return one of those.
	if fn != nil && fn.Result == OwnedScope {
		var kept lifetime
	scopes:
		for _, s := range life {
			for _, m := range consumed {
				if l.scopeOutlives(l.childOf(m), s) {
					continue scopes
				}
			}
			kept = append(kept, s)
		}
		life = kept
	}
	// attach(r, s) gives r as a value of s: it stays open until s closes.
	if fn != nil && fn.Prelude && fn.Decl.Name == "attach" && len(args) == 2 {
		return args[1]
	}
	// Fan-in and receive selection only inspect the wait scope during this
	// call; they retain no tasks, arms or callbacks in it. The result still
	// carries every input lifetime, including resources received from an arm.
	if fn != nil && fn.Prelude && fn.Decl.IsMethod {
		switch fn.Decl.Name {
		case "awaitFirst", "awaitAllUntil", "select":
			return life
		}
	}
	// Go code given a scope may keep its other arguments until the scope
	// closes (as a finalizer, say). So may a function value, which could
	// be such Go code.
	if fn == nil && direct || fn != nil && !fn.Decl.IsGo() {
		return life
	}
	for i, a := range xargs {
		if a.Type() != Scope {
			continue
		}
		for j, b := range xargs {
			if j == i {
				continue
			}
			if short := l.shorter(args[j], args[i]); short != nil {
				callee := "the function"
				if fn != nil {
					callee = fn.Decl.Name
				}
				hint := ""
				if fn != nil && fn.Prelude && (callee == "launch" || callee == "spawn") {
					hint = fmt.Sprintf("; to give a task of %s a resource of a shorter scope, attach it first: r2 = attach(r, %s)", scopeName(a), scopeName(a))
				}
				l.errorf(b.Pos(), "%s may not live as long as scope %s (it depends on %s), but %s may keep it until %s closes%s", describe(b), scopeName(a), l.scopeText(short), callee, scopeName(a), hint)
			}
		}
	}
	return life
}

func scopeName(x Expr) string {
	switch x := x.(type) {
	case *VarRef:
		return x.Var.displayName()
	case *Call:
		// b.scope
		if len(x.Args) == 1 && x.Func.Prelude && x.Func.Decl.Name == "scopeOf" {
			if v, ok := x.Args[0].(*VarRef); ok {
				return v.Var.displayName() + ".scope"
			}
		}
	}
	return "(a scope)"
}

func (l *lifeChecker) generate(x *Generate) lifetime {
	saved, savedOpen := l.cur, l.open
	l.parent[x] = l.cur
	l.cur = x
	var used lifetime
	l.captures = append(l.captures, &used)
	l.expr(x.Body)
	l.captures = l.captures[:len(l.captures)-1]
	l.cur, l.open = saved, savedOpen
	var life lifetime
	for _, s := range used {
		if !l.within(s, x) {
			life = life.union(lifetime{s})
		}
	}
	return life
}
