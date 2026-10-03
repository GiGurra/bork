package check

import (
	"fmt"
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
	l := &lifeChecker{
		info:    info,
		diags:   diags,
		env:     map[*Var]lifetime{},
		frame:   map[any]any{},
		parent:  map[any]any{},
		carries: map[Type]bool{},
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
// are open. Each scope is a *ScopeBlock, or a *Var of a parameter (the
// scope of a function's or lambda's caller). The empty lifetime is
// forever.
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
	}
	l.result(fn.Body, l.expr(fn.Body), l.what())
}

// result checks that a function's or lambda's result does not belong to
// a scope opened inside it.
func (l *lifeChecker) result(x Expr, life lifetime, what string) {
	for _, s := range life {
		if s, ok := s.(*ScopeBlock); ok && l.within(s, l.cur) {
			l.errorf(valuePos(x), "%s cannot return this value: it belongs to scope %s, which ends on line %d", what, s.Var.Name, s.Body.End.Line)
			return
		}
	}
}

// within reports whether the scope or parameter x belongs to the frame
// f, or to a lambda inside it.
func (l *lifeChecker) within(x, f any) bool {
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
	if s := l.closed(life); s != nil {
		if _, ok := x.(*ScopeBlock); ok {
			l.errorf(x.Pos(), "the value of this scope block belongs to scope %s, which has ended; use it inside the block", s.Var.Name)
		} else {
			l.errorf(x.Pos(), "%s may be released: it belongs to scope %s, which ended on line %d", describe(x), s.Var.Name, s.Body.End.Line)
		}
		return nil
	}
	return life
}

// closed is a scope of life that has ended, or nil.
func (l *lifeChecker) closed(life lifetime) *ScopeBlock {
	for _, s := range life {
		if s, ok := s.(*ScopeBlock); ok && !l.isOpen(s) {
			return s
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
		return x.Var.Name
	case *FuncRef:
		return x.Name
	case *Lambda:
		return "the lambda"
	case *ScopeBlock:
		return "the value of scope " + x.Var.Name
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
	}
	return "?"
}

func (l *lifeChecker) scopeOutlives(x, y any) bool {
	if x == y {
		return true
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
	if v, ok := l.carries[t]; ok {
		return v
	}
	l.carries[t] = false // for recursive types
	v := false
	switch t := t.(type) {
	case *Resource, *Opaque, *FuncType, *TypeParam:
		v = true
	case *Basic:
		v = t == Scope
	case *List:
		v = l.carriesLife(t.Elem)
	case *Map:
		v = l.carriesLife(t.Key) || l.carriesLife(t.Value)
	case *Union:
		for _, m := range t.Members {
			v = v || l.carriesLife(m)
		}
	case *Record:
		for _, f := range t.Fields {
			v = v || l.carriesLife(f.Type)
		}
	case *Sealed:
		for _, vt := range t.Variants {
			for _, f := range vt.Fields {
				v = v || l.carriesLife(f.Type)
			}
		}
	}
	l.carries[t] = v
	return v
}

// expr checks an expression and returns its lifetime.
func (l *lifeChecker) expr(x Expr) lifetime {
	life := l.exprLife(x)
	if t := x.Type(); t == nil || !l.carriesLife(t) {
		return nil
	}
	return life
}

func (l *lifeChecker) exprLife(x Expr) lifetime {
	switch x := x.(type) {
	case *VarRef:
		if !l.carriesLife(x.Type()) {
			return nil
		}
		life := l.env[x.Var]
		for _, c := range l.captures {
			*c = c.union(life)
		}
		return l.use(x, life)
	case *Block:
		for _, s := range x.Stmts {
			l.stmt(s)
		}
		if x.Tail == nil {
			return nil
		}
		return l.expr(x.Tail)
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
		life := l.expr(x.Then)
		if x.Else != nil {
			life = life.union(l.expr(x.Else))
		}
		return life
	case *Match:
		subject := l.use(x.X, l.expr(x.X))
		var life lifetime
		for _, arm := range x.Arms {
			l.bindPattern(arm.Pat, subject)
			life = life.union(l.expr(arm.Body))
		}
		return life
	case *Return:
		if x.Value != nil {
			l.result(x.Value, l.use(x.Value, l.expr(x.Value)), l.what())
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
	case *Lambda:
		return l.lambda(x)
	case *Call:
		return l.call(x.Func, true, x.Args)
	case *CallBuiltin:
		return l.call(nil, true, x.Args)
	case *CallValue:
		life := l.use(x.Fun, l.expr(x.Fun))
		return life.union(l.call(nil, false, x.Args))
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
		l.use(x.Y, l.expr(x.Y))
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
		return "function " + fn.Decl.Name
	}
	return "the lambda"
}

func (l *lifeChecker) stmt(s Stmt) {
	switch s := s.(type) {
	case *Let:
		// A possibly released value can be bound; using it is the error.
		l.env[s.Var] = l.expr(s.Value)
	case *ExprStmt:
		l.use(s.X, l.expr(s.X))
	case *Trust:
		l.use(s.Call, l.expr(s.Call))
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

// call checks the arguments of a call, and returns the lifetime of its
// result. The callee is fn, a builtin (direct, with fn nil), or a
// function value (neither).
func (l *lifeChecker) call(fn *Func, direct bool, xargs []Expr) lifetime {
	var life lifetime
	args := make([]lifetime, len(xargs))
	for i, a := range xargs {
		args[i] = l.use(a, l.expr(a))
		life = life.union(args[i])
	}
	// attach(r, s) gives r as a value of s: it stays open until s closes.
	if fn != nil && fn.Prelude && fn.Decl.Name == "attach" && len(args) == 2 {
		return args[1]
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
	if v, ok := x.(*VarRef); ok {
		return v.Var.Name
	}
	return "(a scope)"
}
