package check

import (
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
		env:     map[any]lifetime{},
		frame:   map[any]any{},
		parent:  map[any]any{},
		carries: map[Type]bool{},
	}
	for _, f := range files {
		for _, fd := range f.Funcs {
			if fd.Body != nil && info.FuncOf[fd] != nil {
				l.function(fd, fd.Params, fd.Body)
			}
		}
	}
	for _, fn := range info.Tests {
		l.function(fn.Decl, nil, fn.Decl.Body)
	}
}

// A lifetime is a set of scopes: a value is usable while all of them
// are open. Each scope is a *syntax.ScopeExpr, or a *syntax.Param (the
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
	// env holds the lifetime of each binding, parameter, and pattern
	// variable; and of each scope (a scope lives as long as itself).
	env map[any]lifetime
	// open lists the scope blocks around the current point.
	open []*syntax.ScopeExpr
	// enclosing holds, for each scope block, the scopes open when it
	// opened; they outlive it.
	enclosing map[*syntax.ScopeExpr][]*syntax.ScopeExpr
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
	l.diags.Add(pos, format, args...)
}

func (l *lifeChecker) function(fd *syntax.FuncDecl, params []*syntax.Param, body *syntax.Block) {
	l.cur = fd
	l.open = nil
	l.enclosing = map[*syntax.ScopeExpr][]*syntax.ScopeExpr{}
	for _, p := range params {
		l.frame[p] = fd
		l.env[p] = lifetime{p}
	}
	l.result(body, l.expr(body), l.what())
}

// result checks that a function's or lambda's result does not belong to
// a scope opened inside it.
func (l *lifeChecker) result(x syntax.Expr, life lifetime, what string) {
	for _, s := range life {
		if s, ok := s.(*syntax.ScopeExpr); ok && l.within(s, l.cur) {
			l.errorf(valuePos(x), "%s cannot return this value: it belongs to scope %s, which ends on line %d", what, s.Name, s.Body.End.Line)
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
func (l *lifeChecker) use(x syntax.Expr, life lifetime) lifetime {
	if s := l.closed(life); s != nil {
		if _, ok := x.(*syntax.ScopeExpr); ok {
			l.errorf(x.Position(), "the value of this scope block belongs to scope %s, which has ended; use it inside the block", s.Name)
		} else {
			l.errorf(x.Position(), "%s may be released: it belongs to scope %s, which ended on line %d", describe(x), s.Name, s.Body.End.Line)
		}
		return nil
	}
	return life
}

// closed is a scope of life that has ended, or nil.
func (l *lifeChecker) closed(life lifetime) *syntax.ScopeExpr {
	for _, s := range life {
		if s, ok := s.(*syntax.ScopeExpr); ok && !l.isOpen(s) {
			return s
		}
	}
	return nil
}

func (l *lifeChecker) isOpen(s *syntax.ScopeExpr) bool {
	for _, o := range l.open {
		if o == s {
			return true
		}
	}
	return false
}

func describe(x syntax.Expr) string {
	switch x := x.(type) {
	case *syntax.Ident:
		return x.Name
	case *syntax.Lambda:
		return "the lambda"
	case *syntax.ScopeExpr:
		return "the value of scope " + x.Name
	}
	return "this value"
}

// valuePos is where the value of x comes from: the tail of a block.
func valuePos(x syntax.Expr) diag.Pos {
	if b, ok := x.(*syntax.Block); ok && b.Tail != nil {
		return valuePos(b.Tail)
	}
	return x.Position()
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
	case *syntax.ScopeExpr:
		return "scope " + x.Name
	case *syntax.Param:
		return "parameter " + x.Name
	}
	return "?"
}

func (l *lifeChecker) scopeOutlives(x, y any) bool {
	if x == y {
		return true
	}
	switch x := x.(type) {
	case *syntax.Param:
		// A caller's scope outlives the scopes its callee opens.
		s, ok := y.(*syntax.ScopeExpr)
		return ok && l.within(s, l.frame[x])
	case *syntax.ScopeExpr:
		s, ok := y.(*syntax.ScopeExpr)
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
	case *Resource, *FuncType, *TypeParam:
		v = true
	case *Basic:
		v = t == Scope
	case *List:
		v = l.carriesLife(t.Elem)
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
func (l *lifeChecker) expr(x syntax.Expr) lifetime {
	life := l.exprLife(x)
	if t := l.info.Types[x]; t == nil || !l.carriesLife(t) {
		return nil
	}
	return life
}

func (l *lifeChecker) exprLife(x syntax.Expr) lifetime {
	switch x := x.(type) {
	case *syntax.Ident:
		decl := l.info.Defs[x]
		if decl == nil {
			return nil // a function
		}
		if t := l.info.Types[x]; t == nil || !l.carriesLife(t) {
			return nil
		}
		life := l.env[decl]
		for _, c := range l.captures {
			*c = c.union(life)
		}
		return l.use(x, life)
	case *syntax.Block:
		for _, s := range x.Stmts {
			l.stmt(s)
		}
		if x.Tail == nil {
			return nil
		}
		return l.expr(x.Tail)
	case *syntax.ScopeExpr:
		l.enclosing[x] = append([]*syntax.ScopeExpr(nil), l.open...)
		l.frame[x] = l.cur
		l.env[x] = lifetime{x}
		l.open = append(l.open, x)
		life := l.expr(x.Body)
		l.open = l.open[:len(l.open)-1]
		// The value may belong to x, which has now ended: it is possibly
		// released, which is reported where it is used.
		return life
	case *syntax.If:
		l.use(x.Cond, l.expr(x.Cond))
		life := l.expr(x.Then)
		if x.Else != nil {
			life = life.union(l.expr(x.Else))
		}
		return life
	case *syntax.Match:
		subject := l.use(x.X, l.expr(x.X))
		var life lifetime
		for _, arm := range x.Arms {
			l.bindPattern(arm.Pattern, subject)
			life = life.union(l.expr(arm.Body))
		}
		return life
	case *syntax.Return:
		if x.Value != nil {
			l.result(x.Value, l.use(x.Value, l.expr(x.Value)), l.what())
		}
		return nil
	case *syntax.Try:
		life := l.expr(x.X)
		if info := l.info.Tries[x]; info != nil {
			for _, m := range info.Rest {
				if l.carriesLife(m) {
					// Other members are returned from the function.
					l.result(x.X, l.use(x.X, life), l.what())
					break
				}
			}
		}
		return life
	case *syntax.Lambda:
		return l.lambda(x)
	case *syntax.Call:
		return l.call(x)
	case *syntax.Selector:
		if l.info.SelectorVariants[x] != nil {
			return nil
		}
		return l.use(x.X, l.expr(x.X))
	case *syntax.RecordLit:
		var life lifetime
		for _, f := range x.Fields {
			life = life.union(l.expr(f.Value))
		}
		return life
	case *syntax.Copy:
		life := l.expr(x.X)
		for _, u := range x.Updates {
			life = life.union(l.expr(u.Value))
		}
		return life
	case *syntax.ListLit:
		var life lifetime
		for _, e := range x.Elems {
			life = life.union(l.expr(e))
		}
		return life
	case *syntax.Unary:
		return l.expr(x.X)
	case *syntax.Binary:
		l.use(x.X, l.expr(x.X))
		l.use(x.Y, l.expr(x.Y))
		return nil
	case *syntax.Interp:
		for _, e := range x.Exprs {
			l.use(e, l.expr(e))
		}
	}
	return nil
}

// what names the current frame, for messages.
func (l *lifeChecker) what() string {
	switch f := l.cur.(type) {
	case *syntax.FuncDecl:
		if f.Name == "test" {
			return "the test"
		}
		return "function " + f.Name
	}
	return "the lambda"
}

func (l *lifeChecker) stmt(s syntax.Stmt) {
	switch s := s.(type) {
	case *syntax.Binding:
		// A possibly released value can be bound; using it is the error.
		l.env[s] = l.expr(s.Value)
	case *syntax.ExprStmt:
		l.use(s.X, l.expr(s.X))
	case *syntax.TrustStmt:
		l.use(s.Call, l.expr(s.Call))
	}
}

// bindPattern gives the names a pattern binds the lifetime of the value
// it matches.
func (l *lifeChecker) bindPattern(p syntax.Pattern, life lifetime) {
	switch p := p.(type) {
	case *syntax.TypePat:
		l.env[p] = life
	case *syntax.VariantPat:
		l.env[p] = life
		for _, f := range p.Fields {
			l.env[f] = life
			if f.Pattern != nil {
				l.bindPattern(f.Pattern, life)
			}
		}
	}
}

func (l *lifeChecker) lambda(x *syntax.Lambda) lifetime {
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

func (l *lifeChecker) call(x *syntax.Call) lifetime {
	var life lifetime
	fn := l.info.CallFuncs[x]
	direct := fn != nil || l.info.CallBuiltins[x] != BuiltinNone
	if !direct {
		life = l.use(x.Fun, l.expr(x.Fun))
	}
	args := make([]lifetime, len(x.Args))
	for i, a := range x.Args {
		args[i] = l.use(a, l.expr(a))
		life = life.union(args[i])
	}
	// Go code given a scope may keep its other arguments until the scope
	// closes (as a finalizer, say). So may a function value, which could
	// be such Go code.
	if fn == nil && direct || fn != nil && fn.Decl.GoBody == nil {
		return life
	}
	for i, a := range x.Args {
		if l.info.Types[a] != Scope {
			continue
		}
		for j, b := range x.Args {
			if j == i {
				continue
			}
			if short := l.shorter(args[j], args[i]); short != nil {
				callee := "the function"
				if fn != nil {
					callee = fn.Decl.Name
				}
				l.errorf(b.Position(), "%s may not live as long as scope %s (it depends on %s), but %s may keep it until %s closes", describe(b), scopeName(a), l.scopeText(short), callee, scopeName(a))
			}
		}
	}
	return life
}

func scopeName(x syntax.Expr) string {
	if id, ok := x.(*syntax.Ident); ok {
		return id.Name
	}
	return "(a scope)"
}
