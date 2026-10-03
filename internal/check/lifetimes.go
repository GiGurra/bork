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
//     arguments until that scope closes, so they must live as long as it;
//   - a value stored in a channel or an atom must live as long as it.
//
// bork values are immutable. Package lazy initializers cannot retain scoped
// values; channels and atoms keep only values that live as long as they do
// (see call). These checks cover the ways a value can escape its scope.
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
		inTarget: map[*Var]lifetime{},

		lambdaParams:     map[*Var]lifetime{},
		deferredCaptures: map[*Var]lifetime{},
	}
	for _, f := range files {
		for _, fd := range f.Funcs {
			if fn := info.FuncOf[fd]; fn != nil && fn.Body != nil {
				l.function(fn)
			}
		}
	}
	for _, binding := range info.PackageBindings {
		l.function(binding.Boundary)
	}
	for _, fn := range info.Tests {
		l.function(fn)
	}
}

// A lifetime is a set of scopes: a value is usable while all of them
// are open. Each scope is a *ScopeBlock, a *Var of a parameter (the
// scope of a function's or lambda's caller), a *child: the scope an
// owner variable owns (see owners.go), or forever. The empty lifetime is
// forever too, but a value that keeps others (an atom started with a
// value of no scope) says so with forever, which a union with shorter
// scopes keeps: what is stored in it must still live forever.
type lifetime []any

// foreverScope is the scope of the whole program, which outlives every
// other.
type foreverScope struct{}

var forever = &foreverScope{}

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
	env              map[*Var]lifetime
	deferredCaptures map[*Var]lifetime
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
	// inTarget holds the scope each parameter declared `in` another
	// belongs to: the parameter (as a scope of the caller) outlives it.
	inTarget map[*Var]lifetime
	// lambdaParams holds the lifetimes inferred for the parameters of a
	// lambda passed to a generic function (see inferParams).
	lambdaParams map[*Var]lifetime
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
			target := l.scopeOfVar(fn.ParamVars[fn.ParamIn[i]])
			l.env[p] = target
			if p.Type == OwnedScope {
				continue
			}
			l.inTarget[p] = target
			// A value that keeps others (a channel, an atom, a scope, a
			// function) declared in a scope is only known to outlive it:
			// what is kept in it must outlive the parameter itself.
			if keepsValuesHere(p.Type) {
				l.env[p] = target.union(lifetime{p})
			}
		}
	}
	for _, p := range fn.ParamVars {
		if l.carriesLife(p.Type) {
			// A parameter declared in another belongs to that one's scope
			// (its own identity only bounds what may be kept in it).
			life := l.env[p]
			if _, ok := l.inTarget[p]; ok {
				life = l.inTarget[p]
			}
			l.info.VarLifetimes[p] = l.lifeText(life)
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

// storeShorter returns a scope of lifetime a that may end before a
// container of lifetime b does, or nil if a value of lifetime a can be
// kept in it (or used wherever it can be). Each scope of a must outlive
// every scope of b: a container whose lifetime was widened by mixing it
// with shorter values (an if, a record, a generic call) is the same
// container, and lives as long as before.
func (l *lifeChecker) storeShorter(a, b lifetime) any {
	for _, x := range a {
		if !l.outlivesAll(x, b) {
			return x
		}
	}
	return nil
}

// funcRef reports a function used as a value whose call would keep an
// argument (in a channel or an atom, or as a parameter declared in
// another): a function value's caller is not checked for that.
func (l *lifeChecker) funcRef(x *FuncRef) {
	fn := x.Inst.Func
	for i, t := range x.Inst.Params {
		if !l.carriesLife(t) || i >= len(fn.Decl.Params) {
			continue
		}
		keeps := i < len(fn.ParamIn) && fn.ParamIn[i] >= 0
		if fn.Prelude && i == 1 && (fn.Decl.Name == "send" || fn.Decl.Name == "update" || fn.Decl.Name == "swap") {
			// What they keep is a value of the element type.
			keeps = len(x.Inst.TypeArgs) == 1 && l.carriesLife(x.Inst.TypeArgs[0])
		}
		if keeps {
			l.errorf(x.Pos(), "%s cannot be used as a function value here: it keeps its argument %s, which a call through a function value does not check; call it in a lambda instead", x.Name, fn.Decl.Params[i].Name)
			return
		}
	}
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
	case *foreverScope:
		return "the whole program"
	}
	return "?"
}

func (l *lifeChecker) scopeOutlives(x, y any) bool {
	if x == y || x == forever {
		return true
	}
	if y == forever {
		return false
	}
	if v, ok := x.(*Var); ok {
		for _, t := range l.inTarget[v] {
			if l.scopeOutlives(t, y) {
				return true
			}
		}
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
			v = v || f.Lazy || l.carriesLifeSeen(f.Type, seen)
		}
	case *Sealed:
		for _, vt := range t.Variants {
			for _, f := range vt.Fields {
				v = v || f.Lazy || l.carriesLifeSeen(f.Type, seen)
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
		if x.Var.Let != nil && x.Var.Let.Initializer != nil {
			// Capture/validate the cell even when its eventual payload is scalar.
			life := l.deferredCaptures[x.Var]
			for _, c := range l.captures {
				*c = c.union(life)
			}
			l.use(x, life)
			if !l.carriesLife(x.Type()) {
				return nil
			}
			return l.env[x.Var]
		}
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
	case *Comptime:
		// Validate scopes created inside the independent build-time boundary.
		return l.lambda(ComptimeLambda(x))
	case *Lambda:
		return l.lambda(x)
	case *FuncRef:
		l.funcRef(x)
		return nil
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
		life := l.use(x.X, l.expr(x.X))
		if !l.carriesLife(x.Type()) {
			return nil
		}
		return life
	case *RecordLit:
		var life lifetime
		for _, f := range x.Fields {
			if f.Field.Computed {
				continue
			}
			if f.Thunk != nil {
				life = life.union(l.lambda(f.Thunk))
			} else {
				life = life.union(l.expr(f.Value))
			}
		}
		if x.Candidate != nil {
			l.env[x.Candidate], l.frame[x.Candidate] = life, l.cur
			for _, field := range x.Fields {
				if field.Field.Computed {
					life = life.union(l.lambda(field.Thunk))
				}
			}
		}
		return life
	case *Copy:
		life := l.expr(x.X)
		for _, u := range x.Updates {
			if u.Field.Computed {
				continue
			}
			if u.Thunk != nil {
				life = life.union(l.lambda(u.Thunk))
			} else {
				life = life.union(l.expr(u.Value))
			}
		}
		if x.Candidate != nil {
			l.env[x.Candidate], l.frame[x.Candidate] = life, l.cur
			for _, update := range x.Updates {
				if update.Field.Computed {
					life = life.union(l.lambda(update.Thunk))
				}
			}
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
		if s.Initializer != nil {
			var owner lifetime
			if s.Deferred == AsyncBinding {
				owner = l.use(s.AsyncScope, l.expr(s.AsyncScope))
			}
			life := l.lambda(s.Initializer)
			payload := life
			if s.Deferred == AsyncBinding {
				if short := l.storeShorter(life, owner); short != nil {
					l.errorf(s.Pos, "async initializer may not live as long as scope %s (it depends on %s); attach shorter resources to the task scope first", scopeName(s.AsyncScope), l.scopeText(short))
				}
				life = life.union(owner)
			}
			l.deferredCaptures[s.Var] = life
			l.info.VarLifetimes[s.Var] = l.lifeText(life)
			if l.carriesLife(s.Var.Type) {
				l.env[s.Var] = payload
			}
			return
		}
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
		if life, ok := l.lambdaParams[p]; ok {
			l.env[p] = life
			if len(life) > 0 && l.carriesLife(p.Type) {
				l.info.VarLifetimes[p] = l.lifeText(life)
			}
		}
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
	done := map[int]bool{}
	for _, i := range indices {
		a := xargs[i]
		done[i] = true
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
			if x, ok := a.(*Lambda); ok {
				l.inferParams(fn, x, i, args, done)
			}
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
	// An atom started with a value of no scope keeps only values of no
	// scope.
	if fn != nil && fn.Prelude && fn.Decl.Name == "atom" && len(life) == 0 && l.carriesLife(fn.Result) {
		return lifetime{forever}
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
	// A channel or an atom keeps what is stored in it (sent, or made by
	// update's or swap's function), and gives it as a value of its own
	// lifetime: it must live as long.
	if fn != nil && fn.Prelude && len(args) == 2 && (fn.Decl.Name == "send" || fn.Decl.Name == "update" || fn.Decl.Name == "swap") {
		what := "channel"
		if fn.Decl.Name != "send" {
			what = "atom"
		}
		if short := l.storeShorter(args[1], args[0]); short != nil {
			hint := "store only values that outlive the " + what + ", or attach a resource to its scope first"
			if v, ok := short.(*Var); ok && v.Kind == VarParam {
				if c, ok := xargs[0].(*VarRef); ok && c.Var.Kind == VarParam {
					hint = fmt.Sprintf("declare that it does: %s: ... in %s", v.Name, c.Var.Name)
				}
			}
			if d := describe(xargs[0]); d != "this value" {
				what += " " + d
			}
			l.errorf(xargs[1].Pos(), "%s may not live as long as the %s (it depends on %s), which keeps it; %s", describe(xargs[1]), what, l.scopeText(short), hint)
		} else if keepsValues(storedType(xargs[0].Type())) && len(args[1]) > 0 {
			// What comes out is a value of the container's lifetime: one
			// that keeps values (a channel, an atom, a scope, a function)
			// would then take values of the container's scopes, while it
			// lives longer. So it must live exactly as long.
			for _, y := range args[0] {
				if !l.outlivesAll(y, args[1]) {
					l.errorf(xargs[1].Pos(), "%s may live longer than the %s, which keeps it: what comes out would be treated as ending with the %s, and could be given its values; keep only ones of the same lifetime here", describe(xargs[1]), what, what)
					break
				}
			}
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
			if short := l.storeShorter(args[j], args[i]); short != nil {
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

// inferParams infers the lifetimes of the parameters of lambda x, passed
// as argument i of a call of fn, from fn's signature; done tells the
// arguments evaluated, of lifetimes args. A generic function cannot make
// a value of its type parameters' types: what it passes to x as one, or
// as a value built from them alone (T, List[T], Option[T], but not Conn
// or Channel[T], which it could make in a scope of its own), comes from
// its arguments whose types mention them. So the parameter lives as long
// as those arguments: `xs.forEach(x => send(ch, x))` sends a value of
// xs. That needs those arguments to be evaluated first, the lambda not to
// be one of them (fold's f gives back what it is given), and the type
// parameters' bounds to have no method that makes a value of the type.
// Otherwise the parameter keeps its own lifetime, which outlives nothing.
func (l *lifeChecker) inferParams(fn *Func, x *Lambda, i int, args []lifetime, done map[int]bool) {
	if fn == nil || len(fn.TypeParams) == 0 || fn.Class != nil || fn.Of != nil || fn.Synthetic || i >= len(fn.Params) {
		return
	}
	ft, ok := fn.Params[i].(*FuncType)
	if !ok || len(ft.Params) != len(x.Params) {
		return
	}
	// own holds fn's type parameters, and can those of them it cannot
	// make a value of.
	own, can := map[*TypeParam]bool{}, map[*TypeParam]bool{}
	for _, tp := range fn.TypeParams {
		own[tp] = true
		can[tp] = l.cannotMake(tp)
	}
	isOwn := func(tp *TypeParam) bool { return own[tp] }
	// The lambda gives values of the type parameters back to fn.
	if mentionsWhere(ft.Result, isOwn) {
		return
	}
	for _, p := range ft.Params {
		if mentionsWhere(p, isOwn) && !builtFrom(p, can, map[Type]bool{}) {
			return
		}
	}
params:
	for k, p := range ft.Params {
		if !mentionsWhere(p, isOwn) {
			continue
		}
		mentioned := func(tp *TypeParam) bool { return isOwn(tp) && mentionsParam(p, tp) }
		var life lifetime
		for j, q := range fn.Params {
			if j == i || j >= len(args) || !mentionsWhere(q, mentioned) {
				continue
			}
			// A function among them could make one from what fn gives
			// it (a scope of its own, say): only values held as data,
			// or in a channel or an atom (which keep only values that
			// outlive them), come out as they went in.
			if !done[j] || !builtFrom(q, can, map[Type]bool{}) && !storesData(q, can) {
				continue params
			}
			life = life.union(args[j])
		}
		l.lambdaParams[x.Params[k]] = life
	}
}

// storesData reports whether t is a channel or an atom of values built
// from the type parameters can holds (see builtFrom).
func storesData(t Type, can map[*TypeParam]bool) bool {
	r, ok := t.(*Record)
	return ok && r.Prelude && (r.Name == "Channel" || r.Name == "Atom") && len(r.Args) == 1 && builtFrom(r.Args[0], can, map[Type]bool{})
}

// builtFrom reports whether a value of type t can only carry a lifetime
// through values of the type parameters in can (those a function cannot
// make): t holds no scope, resource, function or opaque value of its
// own.
func builtFrom(t Type, can map[*TypeParam]bool, seen map[Type]bool) bool {
	if seen[t] {
		return true
	}
	seen[t] = true
	switch t := t.(type) {
	case *TypeParam:
		return can[t]
	case *Basic:
		return t != Scope && t != OwnedScope
	case *List:
		return builtFrom(t.Elem, can, seen)
	case *Map:
		return builtFrom(t.Key, can, seen) && builtFrom(t.Value, can, seen)
	case *Union:
		for _, m := range t.Members {
			if !builtFrom(m, can, seen) {
				return false
			}
		}
		return true
	case *Record:
		for _, f := range t.Fields {
			if !builtFrom(f.Type, can, seen) {
				return false
			}
		}
		return true
	case *Sealed:
		for _, v := range t.Variants {
			for _, f := range v.Fields {
				if !builtFrom(f.Type, can, seen) {
					return false
				}
			}
		}
		return true
	}
	return false
}

// cannotMake reports whether a function cannot make a value of type
// parameter tp through its bounds: no method of them gives one (Eq, Ord
// and Show only take them).
func (l *lifeChecker) cannotMake(tp *TypeParam) bool {
	for _, c := range tp.Bounds {
		self := map[*TypeParam]bool{c.Param: true}
		for _, m := range c.Methods {
			if mentionsParam(m.Result, c.Param) {
				return false
			}
			for _, p := range m.Params {
				if mentionsParam(p, c.Param) && !builtFrom(p, self, map[Type]bool{}) {
					return false
				}
			}
		}
	}
	return true
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
