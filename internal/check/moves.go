package check

import (
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
)

// Moves: `move(r, s)` hands the registration of resource r with the scope
// it belongs to here over to scope s (see docs/design/move.md).
//
// A handle is a scope of the lifetimes like the others, standing for the
// registration of one resource with one scope, acquired in this frame:
// the result of an acquisition (an unsafe go function, or a bork function
// verified to acquire, given exactly one simple scope, whose declared
// result is a resource), of attach or of move. For every outlives check
// it is its scope. Every value made from the resource carries it, so a
// move, which ends it, makes them all unusable.
//
// Which handles a value is (its origin) is tracked apart from its
// lifetime, which only says what it was made from: a variable, ?, a block,
// an if or a match, and a pattern binding the whole value keep it; every
// other expression has an unknown origin, and is borrowed. A move needs a
// known origin, from one scope, acquired in this frame outside the loops
// around the move, and no pin: a task, a channel, an atom, Go code given
// a scope, or a parameter declared in another that may still use it.

// A handle is a registration of a resource with a scope.
type handle struct {
	res   *Resource
	scope any  // *ScopeBlock, *Var (a Scope parameter) or *child
	from  Expr // names scope at run time, for MoveFrom
	frame any
	loop  *For // the innermost loop around it in its frame
	pos   diag.Pos
}

// movedAt is where a handle was moved, and to which scope.
type movedAt struct {
	pos      diag.Pos
	to       string
	possibly bool
}

// pinAt is a use that may keep a value with a handle: until every scope
// of keeper has ended, or for good (forever).
type pinAt struct {
	pos     diag.Pos
	what    string
	keeper  lifetime
	forever bool
}

// origin is the handles a value may be; known is false when it may be a
// resource the checker cannot see (a borrowed value).
type resOrigin struct {
	hs    []*handle
	known bool
}

func (o resOrigin) union(p resOrigin) resOrigin {
	if !o.known || !p.known {
		return resOrigin{}
	}
	out := o
	for _, h := range p.hs {
		if !containsHandle(out.hs, h) {
			out.hs = append(out.hs[:len(out.hs):len(out.hs)], h)
		}
	}
	return out
}

func containsHandle(hs []*handle, h *handle) bool {
	for _, x := range hs {
		if x == h {
			return true
		}
	}
	return false
}

// handles lists the handles in a lifetime.
func handles(life lifetime) []*handle {
	var out []*handle
	for _, x := range life {
		if h, ok := x.(*handle); ok {
			out = append(out, h)
		}
	}
	return out
}

// unhandle is the scope a lifetime element stands for.
func unhandle(x any) any {
	if h, ok := x.(*handle); ok {
		return h.scope
	}
	return x
}

// originOf gives the handles x may be.
func (l *lifeChecker) originOf(x Expr) resOrigin {
	if x == nil {
		return resOrigin{}
	}
	// A value that cannot hold a resource is none.
	if x.Type() == Never || !l.carriesLife(x.Type()) {
		return resOrigin{known: true}
	}
	switch x := x.(type) {
	case *VarRef:
		return l.origins[x.Var]
	case *Call:
		if h := l.acquired[x]; h != nil {
			return resOrigin{hs: []*handle{h}, known: true}
		}
		return l.passedOrigin(x)
	case *Try:
		return l.originOf(x.X)
	case *Block:
		if x.Tail != nil {
			return l.originOf(x.Tail)
		}
	case *ScopeBlock:
		return l.originOf(x.Body)
	case *If:
		if x.Else != nil {
			return l.originOf(x.Then).union(l.originOf(x.Else))
		}
	case *Match:
		o := resOrigin{known: true}
		for _, arm := range x.Arms {
			o = o.union(l.originOf(arm.Body))
		}
		return o
	}
	return resOrigin{}
}

// passShape gives, for a generic bork function whose result is a type
// parameter T (or a union of T with values that hold no resource) that
// its bounds cannot make, T and its parameters that mention T, all built
// from T alone (T, T | Failure, Option[T]). Such a function can only give
// back a T it was given, unless it gets one elsewhere (from Go code that
// keeps values, say): passes holds those verified not to (summarize).
func (l *lifeChecker) passShape(fn *Func) (*TypeParam, []int) {
	if len(fn.TypeParams) == 0 || fn.Body == nil || fn.Decl == nil || fn.Decl.IsGo() || fn.Class != nil || fn.Of != nil || fn.Synthetic {
		return nil, nil
	}
	var tp *TypeParam
	members := []Type{fn.Result}
	if u, ok := fn.Result.(*Union); ok {
		members = u.Members
	}
	for _, m := range members {
		if t, ok := m.(*TypeParam); ok {
			if tp != nil {
				return nil, nil
			}
			tp = t
		} else if l.carriesLife(m) {
			return nil, nil
		}
	}
	if tp == nil || !l.cannotMake(tp) {
		return nil, nil
	}
	can := map[*TypeParam]bool{tp: true}
	var params []int
	for i, p := range fn.Params {
		if !mentionsParam(p, tp) {
			continue
		}
		if !builtFrom(p, can, map[Type]bool{}) {
			return nil, nil
		}
		params = append(params, i)
	}
	return tp, params
}

// passedOrigin gives the origin of a call of a generic function verified
// to give back only what it was given (see passShape): that of those
// arguments. fs.Open's result(open(path, s)) is the value open acquired.
func (l *lifeChecker) passedOrigin(x *Call) resOrigin {
	if !l.passes[x.Func] {
		return resOrigin{}
	}
	_, params := l.passShape(x.Func)
	o := resOrigin{known: true}
	for _, i := range params {
		if i >= len(x.Args) {
			return resOrigin{}
		}
		o = o.union(l.originOf(x.Args[i]))
	}
	return o
}

// resourceOf is the resource type a call of fn acquires: its declared
// result, or the one resource member of it.
func resourceOf(t Type) *Resource {
	switch t := t.(type) {
	case *Resource:
		return t
	case *Union:
		var found *Resource
		for _, m := range t.Members {
			if r, ok := m.(*Resource); ok {
				if found != nil && found != r {
					return nil
				}
				found = r
			}
		}
		return found
	}
	return nil
}

// simpleScope reports whether x names one scope at run time, by a name
// in Go scope wherever a value of that scope is usable: a scope block's
// variable, a parameter of a function or lambda, or b.scope (not a local
// binding, which a block may hide).
func simpleScope(x Expr) bool {
	switch x := x.(type) {
	case *VarRef:
		k := x.Var.Kind
		return x.Var.Type == Scope && (k == VarScope || k == VarParam || k == VarLambdaParam)
	case *Call:
		if x.Func.Prelude && x.Func.Decl.Name == "scopeOf" && len(x.Args) == 1 {
			_, ok := x.Args[0].(*VarRef)
			return ok
		}
	}
	return false
}

// scopeVar is the variable a simple scope names: the scope's, or the
// owner's for b.scope.
func scopeVar(x Expr) *Var {
	switch x := x.(type) {
	case *VarRef:
		return x.Var
	case *Call:
		if v, ok := x.Args[0].(*VarRef); ok {
			return v.Var
		}
	}
	return nil
}

// newHandle makes a handle of resource res in the scope from names, if
// from is a simple scope of one lifetime scope; nil otherwise.
func (l *lifeChecker) newHandle(res *Resource, from Expr, pos diag.Pos) *handle {
	if res == nil || !simpleScope(from) {
		return nil
	}
	var life lifetime
	switch x := from.(type) {
	case *VarRef:
		life = l.env[x.Var]
	case *Call:
		life = lifetime{l.childOf(x.Args[0].(*VarRef).Var)}
	}
	if len(life) != 1 {
		return nil
	}
	return &handle{res: res, scope: unhandle(life[0]), from: from, frame: l.cur, loop: l.loop, pos: pos}
}

// acquisition gives the handle of the resource a call of fn acquires, if
// it does: fn is an unsafe go function whose declared result is a
// resource, or a bork function verified to acquire into its Scope
// parameter, given exactly one simple scope.
func (l *lifeChecker) acquisition(x *Call) *handle {
	fn := x.Func
	if fn.Prelude && (fn.Decl.Name == "move" || fn.Decl.Name == "attach") {
		return nil
	}
	res := resourceOf(fn.Result)
	if res == nil {
		return nil
	}
	scopeArg := -1
	for i, a := range x.Args {
		if a.Type() == Scope {
			if scopeArg >= 0 {
				return nil
			}
			scopeArg = i
		}
	}
	if scopeArg < 0 {
		return nil
	}
	if !fn.Decl.IsGo() {
		if i, ok := l.acquires[fn]; !ok || i != scopeArg {
			return nil
		}
	}
	return l.newHandle(res, x.Args[scopeArg], x.Pos())
}

// pinned gives a pin of h that still holds, if any.
func (l *lifeChecker) pinned(h *handle) (pinAt, bool) {
	for _, p := range l.pins[h] {
		if p.forever || !l.ended(p.keeper) {
			return p, true
		}
	}
	return pinAt{}, false
}

// ended reports whether every scope of life has ended, so that nothing
// it kept is still used: a scope block that ended, or an owner's child
// that was closed (not passed on, which leaves its tasks running), opened
// here (an owner parameter's child may have any policies). Either must
// have only policies known not to leave tasks running (taskTimeout
// orphans them); setScopePolicy cannot be called directly, so the
// policies are all at the scope's start.
func (l *lifeChecker) ended(life lifetime) bool {
	if len(life) == 0 {
		return false
	}
	for _, x := range life {
		switch x := unhandle(x).(type) {
		case *ScopeBlock:
			if l.isOpen(x) || !safePolicies(x.Policies) {
				return false
			}
		case *child:
			if g, ok := l.gone[x.owner]; !ok || g.how != "closed" || !l.safeChild[x.owner] {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// safePolicies reports whether policies are all prelude policies that
// leave nothing running after the scope closes (logFailures). Anything
// else may orphan tasks (taskTimeout) or finalizers (cleanupTimeout), or
// be either (a policy computed elsewhere).
func safePolicies(policies []Expr) bool {
	for _, p := range policies {
		c, ok := p.(*Call)
		if !ok || !c.Func.Prelude || c.Func.Decl.Name != "logFailures" {
			return false
		}
	}
	return true
}

// safeOpen reports whether x opens an owned scope with safe policies
// only: openScope(parent), or with a list of them.
func safeOpen(x Expr) bool {
	c, ok := x.(*Call)
	if !ok || !c.Func.Prelude || c.Func.Decl.Name != "openScope" {
		return false
	}
	if len(c.Args) < 2 || c.Args[1] == nil {
		return true
	}
	list, ok := c.Args[1].(*ListLit)
	return ok && safePolicies(list.Elems)
}

// pin records that what (at pos) may keep the values of life with the
// scopes of keeper (or for good).
func (l *lifeChecker) pin(life lifetime, pos diag.Pos, what string, keeper lifetime, forever bool) {
	for _, h := range handles(life) {
		// Branches share the slices (copyPins): never append in place.
		l.pins[h] = append(slices.Clip(l.pins[h]), pinAt{pos: pos, what: what, keeper: keeper, forever: forever})
	}
}

// moveCall checks move(r, s), and gives its lifetime: that of s, with a
// new handle.
func (l *lifeChecker) moveCall(x *Call) lifetime {
	order := []int{0, 1}
	if x.ArgOrder != nil {
		order = x.ArgOrder
	}
	args := make([]lifetime, 2)
	for _, i := range order {
		args[i] = l.use(x.Args[i], l.expr(x.Args[i]))
	}
	r, target := x.Args[0], x.Args[1]
	if _, ok := r.Type().(*Resource); !ok || args[0] == nil {
		return args[1] // not a resource, or moved or released already: reported
	}
	// The target is evaluated after r (in order): it may have closed or
	// moved what r is.
	if order[0] == 0 && l.use(r, args[0]) == nil {
		return args[1]
	}
	name := describe(r)
	o := l.originOf(r)
	if !o.known || len(o.hs) == 0 || o.hs[0].res == nil {
		l.errorf(r.Pos(), "%s cannot be moved: it is borrowed (a parameter, a value from a channel, a task or a call that may give a resource held elsewhere), not acquired here; attach it instead to keep it open until %s closes too", name, scopeName(target))
		return args[1]
	}
	src := o.hs[0]
	for _, h := range o.hs {
		if unhandle(h.scope) != unhandle(src.scope) {
			l.errorf(r.Pos(), "%s cannot be moved: it may belong to %s or %s", name, l.scopeText(src.scope), l.scopeText(h.scope))
			return args[1]
		}
		if scopeVar(h.from) != scopeVar(src.from) {
			l.errorf(r.Pos(), "%s cannot be moved: it may belong to scope %s or scope %s", name, scopeName(src.from), scopeName(h.from))
			return args[1]
		}
		if h.res == nil {
			l.errorf(r.Pos(), "%s cannot be moved: it is borrowed (a parameter)", name)
			return args[1]
		}
		if h.frame != l.cur {
			l.errorf(r.Pos(), "%s cannot be moved here: it was acquired outside this %s, which may run later or more than once; move it before", name, l.frameKind())
			return args[1]
		}
		if h.loop != l.loop {
			l.errorf(r.Pos(), "a loop cannot move %s, which was acquired outside its body", name)
			return args[1]
		}
		if p, ok := l.pinned(h); ok {
			l.errorf(r.Pos(), "%s cannot be moved: %s at line %d may still use it; attach it instead", name, p.what, p.pos.Line)
			return args[1]
		}
	}
	if len(args[1]) == 1 && unhandle(args[1][0]) == unhandle(src.scope) {
		l.errorf(target.Pos(), "%s already belongs to %s", name, l.scopeText(src.scope))
		return args[1]
	}
	to := scopeName(target)
	if len(args[1]) == 1 {
		to = strings.TrimPrefix(l.scopeText(unhandle(args[1][0])), "parameter ")
	}
	for _, h := range o.hs {
		l.moved[h] = movedAt{pos: x.Pos(), to: to}
	}
	x.MoveFrom = src.from
	life := args[1]
	if h := l.newHandle(src.res, target, x.Pos()); h != nil {
		l.acquired[x] = h
		life = life.union(lifetime{h})
	}
	return life
}

// movedUse reports the use of x, of a value that holds the moved handle
// h.
func (l *lifeChecker) movedUse(x Expr, h *handle) {
	m := l.moved[h]
	how := "was moved"
	if m.possibly {
		how = "may have been moved"
	}
	o := l.originOf(x)
	if o.known && containsHandle(o.hs, h) {
		l.errorf(x.Pos(), "%s %s to %s at line %d; to keep using it here, attach it instead of moving it", describe(x), how, m.to, m.pos.Line)
	} else {
		l.errorf(x.Pos(), "%s may be released: it holds a resource that %s to %s at line %d", describe(x), how, m.to, m.pos.Line)
	}
	if m.pos.Line > 0 {
		end := m.pos
		end.Col += len("move")
		l.diags.Suggest(x.Pos(), "lifetime.error", x.Pos(), diag.Fix{
			Message: "attach instead of moving, so that the source keeps it too",
			Edits:   []diag.TextEdit{{Start: m.pos, End: end, Replacement: "attach"}},
		})
	}
}

// movesIn gives the handles moved since before (a copy of moved).
func (l *lifeChecker) movesSince(before map[*handle]movedAt) []*handle {
	var out []*handle
	for h := range l.moved {
		if _, ok := before[h]; !ok {
			out = append(out, h)
		}
	}
	return out
}

// usesMoved reports the arguments of a call that hold a handle another
// argument moved: they were evaluated before it, and the callee would
// use them after.
func (l *lifeChecker) usesMoved(xargs []Expr, args []lifetime, moves []*handle) {
	if len(moves) == 0 {
		return
	}
	for j, life := range args {
		for _, h := range handles(life) {
			if containsHandle(moves, h) {
				m := l.moved[h]
				l.errorf(xargs[j].Pos(), "%s is used by this call after another of its arguments moves it to %s (line %d)", describe(xargs[j]), m.to, m.pos.Line)
				break
			}
		}
	}
}

// frameKind names the current frame, for messages.
func (l *lifeChecker) frameKind() string {
	switch l.cur.(type) {
	case *Func:
		return "function"
	case *Generate:
		return "generator"
	}
	return "lambda (or lazy, async or comptime body)"
}

// inFrame reports whether frame f is g or nested in it.
func (l *lifeChecker) inFrame(f, g any) bool {
	for ; f != nil; f = l.parent[f] {
		if f == g {
			return true
		}
	}
	return false
}

func copyMoved(m map[*handle]movedAt) map[*handle]movedAt {
	out := make(map[*handle]movedAt, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyPins(m map[*handle][]pinAt) map[*handle][]pinAt {
	out := make(map[*handle][]pinAt, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// joinMoves joins the moves and pins of the branches that go on: moved on
// some is possibly moved, pinned on any is pinned.
func joinMoves(start map[*handle]movedAt, ends []map[*handle]movedAt, startPins map[*handle][]pinAt, pinEnds []map[*handle][]pinAt) (map[*handle]movedAt, map[*handle][]pinAt) {
	moved := copyMoved(start)
	for _, end := range ends {
		for h, m := range end {
			if _, ok := start[h]; ok {
				continue
			}
			for _, other := range ends {
				if o, ok := other[h]; !ok || o.possibly {
					m.possibly = true
				}
			}
			if prev, ok := moved[h]; ok && !prev.possibly {
				continue
			}
			moved[h] = m
		}
	}
	pins := copyPins(startPins)
	for _, end := range pinEnds {
		for h, ps := range end {
			pins[h] = append(slices.Clip(pins[h]), ps[len(startPins[h]):]...)
		}
	}
	return moved, pins
}

// verifyResult checks, for a function being summarized, that the value
// it gives back, x, is what its summary says: only its parameters' values
// (passes), or acquired in its Scope parameter here, and neither moved
// nor pinned (acquires).
func (l *lifeChecker) verifyResult(x Expr) {
	fn, ok := l.cur.(*Func)
	if !ok || x == nil || x.Type() == Never {
		return
	}
	if len(l.passParams) > 0 {
		o := l.originOf(x)
		if !o.known {
			l.passOK = false
		}
		for _, h := range o.hs {
			if v, ok := h.scope.(*Var); !ok || h.res != nil || l.passParams[v] != h {
				l.passOK = false
			}
		}
	}
	if l.acquireParam == nil {
		return
	}
	o := l.originOf(x)
	if !o.known || len(o.hs) == 0 {
		l.acquireOK = false
		return
	}
	for _, h := range o.hs {
		_, moved := l.moved[h]
		_, pinned := l.pinned(h)
		if h.frame != fn || unhandle(h.scope) != l.acquireParam || moved || pinned {
			l.acquireOK = false
		}
	}
}

// mayAcquire gives the index of fn's one Scope parameter, if fn is a bork
// function with a resource result that may acquire into it.
func mayAcquire(fn *Func) int {
	if fn.Body == nil || fn.Decl == nil || fn.Decl.IsGo() || resourceOf(fn.Result) == nil {
		return -1
	}
	found := -1
	for i, p := range fn.ParamVars {
		if p.Type == Scope {
			if found >= 0 {
				return -1
			}
			found = i
		}
	}
	return found
}

// summarize finds the bork functions that acquire (whose result is a
// resource they acquired into their one Scope parameter), and the
// generic ones that give back only what they were given (see passShape).
// It checks them until no more are found, with diagnostics discarded.
func summarize(fns []*Func, info *Info) (map[*Func]int, map[*Func]bool) {
	acquires, passes := map[*Func]int{}, map[*Func]bool{}
	// A function is checked again only once a function it calls has a
	// new summary.
	fresh := map[*Func]bool{}
	for round := 0; round == 0 || len(fresh) > 0; round++ {
		added := map[*Func]bool{}
		for _, fn := range fns {
			if round > 0 && !slices.ContainsFunc(fn.Calls, func(g *Func) bool { return fresh[g] }) {
				continue
			}
			if i := mayAcquire(fn); i >= 0 {
				if _, ok := acquires[fn]; !ok {
					l := newLifeChecker(info, &diag.List{})
					l.acquires, l.passes = acquires, passes
					l.acquireParam = fn.ParamVars[i]
					l.acquireOK = true
					l.function(fn)
					if l.acquireOK {
						acquires[fn] = i
						added[fn] = true
					}
				}
			}
			if passes[fn] {
				continue
			}
			l := newLifeChecker(info, &diag.List{})
			if _, params := l.passShape(fn); len(params) > 0 {
				l.acquires, l.passes = acquires, passes
				for _, i := range params {
					p := fn.ParamVars[i]
					marker := &handle{scope: p, frame: fn}
					l.passParams[p] = marker
					l.origins[p] = resOrigin{hs: []*handle{marker}, known: true}
				}
				l.passOK = true
				l.function(fn)
				if l.passOK {
					passes[fn] = true
					added[fn] = true
				}
			}
		}
		fresh = added
	}
	return acquires, passes
}
