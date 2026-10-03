package check

import (
	"fmt"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Instance is a use of a function: a call, or a reference to it as a
// value. For a generic function it records the type arguments, and the
// parameter and result types with them substituted.
type Instance struct {
	Func     *Func
	TypeArgs []Type
	Params   []Type
	Result   Type
	// Dicts holds the class instances the use needs: for a class method,
	// the class's; for a function with bounded type parameters, one per
	// bound, in order.
	Dicts []*Dict
	// ArgFacts holds, per type parameter, the constraints of an explicit
	// type argument (decodeJson[Port]): arguments of that type must
	// satisfy them, and results of it do (see callFunc).
	ArgFacts [][]*Constraint
}

// funcType is the type of fn as a value.
func (fn *Func) funcType() *FuncType {
	return &FuncType{Params: fn.Params, Result: fn.Result, Effects: fn.Effects}
}

// declareTypeParams makes a generic function's type parameters
// visible while its signature, constraints, and body are resolved.
func (c *checker) declareTypeParams(fd *syntax.FuncDecl, prelude bool) []*TypeParam {
	return c.declareTypeParamList(fd.TypeParams, prelude)
}

// declareTypeParamList declares type parameters (of a function or an
// instance), with their bounds, and makes them visible.
func (c *checker) declareTypeParamList(decls []*syntax.TypeParam, prelude bool) []*TypeParam {
	var out []*TypeParam
	c.typeParams = map[string]*TypeParam{}
	for _, d := range decls {
		switch {
		case c.typeParams[d.Name] != nil:
			c.errorf(d.Pos, "type parameter %s is declared twice", d.Name)
			continue
		case !prelude && c.isTypeName(d.Name):
			c.errorf(d.Pos, "type parameter %s has the name of a type", d.Name)
			continue
		}
		tp := &TypeParam{Name: d.Name, Decl: d, Bounds: c.bounds(d)}
		c.typeParams[d.Name] = tp
		out = append(out, tp)
	}
	return out
}

// useTypeParams makes fn's type parameters visible (nil hides them).
func (c *checker) useTypeParams(fn *Func) {
	c.typeParams = nil
	if fn == nil {
		return
	}
	c.typeParams = map[string]*TypeParam{}
	for _, tp := range fn.TypeParams {
		c.typeParams[tp.Name] = tp
	}
}

// inference matches types against type parameters in one pass: those
// of a generic type in a literal of it, of a predicate for its subject,
// or of a class instance. (Calls are inferred with unknowns; see
// infer.go.)
type inference struct {
	fn     *Func // nil when solving a generic type's parameters
	params []*TypeParam
	bound  map[*TypeParam]Type
}

func newInference(fn *Func) *inference {
	return &inference{fn: fn, params: fn.TypeParams, bound: map[*TypeParam]Type{}}
}

func typeInference(params []*TypeParam) *inference {
	return &inference{params: params, bound: map[*TypeParam]Type{}}
}

// args lists the solved type arguments, in order.
func (in *inference) args() []Type {
	out := make([]Type, len(in.params))
	for i, p := range in.params {
		out[i] = in.bound[p]
	}
	return out
}

func (in *inference) owns(tp *TypeParam) bool {
	for _, p := range in.params {
		if p == tp {
			return true
		}
	}
	return false
}

// unify matches a parameter type p against the type a of the value
// given for it, binding the type parameters p mentions.
func (in *inference) unify(p, a Type) {
	if a == nil || a == Invalid || a == Never {
		return
	}
	switch p := p.(type) {
	case *TypeParam:
		if in.owns(p) && in.bound[p] == nil {
			in.bound[p] = a
		} else if previous, ok := in.bound[p].(*Seq); ok && in.owns(p) {
			if next, ok := a.(*Seq); ok && identical(previous.Elem, next.Elem) {
				in.bound[p] = &Seq{Elem: previous.Elem, Effects: previous.Effects | next.Effects}
			}
		} else if bf, ok := in.bound[p].(*FuncType); ok && in.owns(p) {
			// Functions that differ only in their effects: T is one
			// that may use what either uses.
			if af, ok := a.(*FuncType); ok && sameSignature(bf, af) {
				in.bound[p] = &FuncType{Params: bf.Params, Result: bf.Result, Effects: bf.Effects | af.Effects}
			}
		}
	case *Seq:
		if a, ok := a.(*Seq); ok {
			in.unify(p.Elem, a.Elem)
		}
	case *List:
		if a, ok := a.(*List); ok {
			in.unify(p.Elem, a.Elem)
		}
	case *Map:
		if a, ok := a.(*Map); ok {
			in.unify(p.Key, a.Key)
			in.unify(p.Value, a.Value)
		}
	case *Record, *Sealed:
		if base := genericBase(p); base != nil && base == genericBase(a) {
			pa, aa := TypeArgs(p), TypeArgs(a)
			for i := range pa {
				in.unify(pa[i], aa[i])
			}
		}
	case *FuncType:
		if a, ok := a.(*FuncType); ok && len(a.Params) == len(p.Params) {
			for i := range p.Params {
				in.unify(p.Params[i], a.Params[i])
			}
			in.unify(p.Result, a.Result)
		}
	case *Union:
		// `T | NotFound` given `Int | NotFound`: T is what remains.
		var open []*TypeParam
		var fixed []Type
		for _, m := range p.Members {
			if tp, ok := m.(*TypeParam); ok && in.owns(tp) && in.bound[tp] == nil {
				open = append(open, tp)
			} else {
				fixed = append(fixed, in.subst(m))
			}
		}
		if len(open) == 0 {
			// `List[T] | DecodeError` given `List[Int] | DecodeError`: the
			// one member with open parameters takes what remains.
			var pOpen []Type
			for _, m := range p.Members {
				if in.open(m) {
					pOpen = append(pOpen, m)
				}
			}
			members := []Type{a}
			if u, ok := a.(*Union); ok {
				members = u.Members
			}
			var rest []Type
			for _, m := range members {
				if !containsMember(&Union{Members: fixed}, m) {
					rest = append(rest, m)
				}
			}
			if len(pOpen) == 1 && len(rest) == 1 {
				in.unify(pOpen[0], rest[0])
			}
			return
		}
		if len(open) != 1 {
			return
		}
		members := []Type{a}
		if u, ok := a.(*Union); ok {
			members = u.Members
		}
		var rest []Type
		for _, m := range members {
			if !containsMember(&Union{Members: fixed}, m) {
				rest = append(rest, m)
			}
		}
		if len(rest) > 0 {
			in.unify(open[0], newUnion(rest))
		}
	}
}

// subst replaces the bound type parameters in t.
func (in *inference) subst(t Type) Type {
	return subst(t, in.bound)
}

func subst(t Type, bound map[*TypeParam]Type) Type {
	switch t := t.(type) {
	case *TypeParam:
		if b := bound[t]; b != nil {
			return b
		}
	case *Seq:
		return &Seq{Elem: subst(t.Elem, bound), Effects: t.Effects}
	case *List:
		return &List{Elem: subst(t.Elem, bound)}
	case *Map:
		return &Map{Key: subst(t.Key, bound), Value: subst(t.Value, bound)}
	case *FuncType:
		ft := &FuncType{Result: subst(t.Result, bound), Effects: t.Effects}
		for _, p := range t.Params {
			ft.Params = append(ft.Params, subst(p, bound))
		}
		return ft
	case *Record, *Sealed:
		if base := genericBase(t); base != nil {
			args := TypeArgs(t)
			out := make([]Type, len(args))
			for i, a := range args {
				out[i] = subst(a, bound)
			}
			return instantiate(base, out)
		}
	case *Union:
		members := make([]Type, len(t.Members))
		for i, m := range t.Members {
			members[i] = subst(m, bound)
		}
		return newUnion(members)
	}
	return t
}

// open reports whether t mentions a type parameter still being solved.
func (in *inference) open(t Type) bool {
	if in == nil {
		return false
	}
	switch t := t.(type) {
	case *TypeParam:
		return in.owns(t) && in.bound[t] == nil
	case *Seq:
		return in.open(t.Elem)
	case *List:
		return in.open(t.Elem)
	case *Map:
		return in.open(t.Key) || in.open(t.Value)
	case *FuncType:
		for _, p := range t.Params {
			if in.open(p) {
				return true
			}
		}
		return in.open(t.Result)
	case *Record, *Sealed:
		for _, a := range TypeArgs(t) {
			if in.open(a) {
				return true
			}
		}
	case *Union:
		for _, m := range t.Members {
			if in.open(m) {
				return true
			}
		}
	}
	return false
}

// unsolved lists the type parameters left unbound.
func (in *inference) unsolved() []string {
	var out []string
	for _, tp := range in.params {
		if in.bound[tp] == nil {
			out = append(out, tp.Name)
		}
	}
	return out
}

func (in *inference) instance() *Instance {
	inst := &Instance{Func: in.fn, Result: in.subst(in.fn.Result)}
	for _, tp := range in.fn.TypeParams {
		inst.TypeArgs = append(inst.TypeArgs, in.bound[tp])
	}
	for _, p := range in.fn.Params {
		inst.Params = append(inst.Params, in.subst(p))
	}
	return inst
}

// InstanceFor instantiates fn (a predicate) for a value of type
// subject as its first argument. It returns nil if that does not
// decide fn's type parameters.
func (fn *Func) InstanceFor(subject Type) *Instance {
	if len(fn.TypeParams) == 0 {
		return &Instance{Func: fn, Params: fn.Params, Result: fn.Result}
	}
	in := newInference(fn)
	in.unify(fn.Params[0], subject)
	if len(in.unsolved()) > 0 {
		return nil
	}
	return in.instance()
}

// callFunc checks a call of a declared function, named name, with the
// given arguments and type arguments: for a method call, the receiver
// comes first, already checked as being of type recv (nil otherwise).
// See infer.go for how the type arguments are inferred.
func (c *checker) callFunc(e *syntax.Call, name string, fn *Func, args []syntax.Expr, recv Type, typeArgs []*syntax.TypeExpr, want Type) Type {
	outer := c.session == nil
	if outer {
		c.session = &session{}
	}
	cs := &callState{parent: c.session.cur}
	c.session.cur = cs
	t := c.inferCall(e, name, fn, args, recv, typeArgs, want, cs)
	c.session.cur = cs.parent
	if !outer {
		return t
	}
	c.closeSession()
	if cs.failed || c.open(t) {
		return Invalid
	}
	return c.zonk(t)
}

// inferCall checks a call's arguments (see callFunc), and adds what
// completes the call once the session closes. It gives the call's
// type, which may have unknowns.
func (c *checker) inferCall(e *syntax.Call, name string, fn *Func, args []syntax.Expr, recv Type, typeArgs []*syntax.TypeExpr, want Type, cs *callState) Type {
	errorsBefore := c.diags.Len()
	fail := func() Type {
		cs.failed = true
		cs.parent.blame()
		return Invalid
	}
	c.info.callFuncs[e] = fn
	if c.fn != nil {
		c.fn.Calls = append(c.fn.Calls, fn)
	}
	var valid bool
	args, valid = c.namedArgs(e, name, fn, args)
	if !valid {
		for _, a := range e.Args {
			c.expr(a)
		}
		return fail()
	}
	c.info.callArgs[e] = args
	// Errors about the call itself are reported at the method's name in
	// a method call, x.m(a).
	at := e.Pos
	if sel, ok := e.Fun.(*syntax.Selector); ok && recv != nil {
		at = sel.Pos
	}
	if len(args) != len(fn.Params) {
		// A method's receiver is not counted.
		what, skip := name, 0
		if recv != nil {
			what, skip = "method "+name, 1
		}
		if req := requiredParams(fn); req < len(fn.Params) {
			c.errorf(at, "%s takes %d to %d argument(s), but %d were given", what, req-skip, len(fn.Params)-skip, len(args)-skip)
		} else {
			c.errorf(at, "%s takes %d argument(s), but %d were given", what, len(fn.Params)-skip, len(args)-skip)
		}
	}
	// The call's own unknowns, one for each type parameter, and fn's
	// signature in terms of them.
	unknowns := make([]*TypeParam, len(fn.TypeParams))
	fresh := map[*TypeParam]Type{}
	for i, tp := range fn.TypeParams {
		unknowns[i] = newUnknown(tp)
		fresh[tp] = unknowns[i]
	}
	params := make([]Type, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = subst(p, fresh)
	}
	result := subst(fn.Result, fresh)
	var argFacts [][]*Constraint
	if len(typeArgs) > 0 {
		reported := func() {
			for _, ta := range typeArgs {
				c.whereReported(ta)
			}
		}
		if len(typeArgs) != len(fn.TypeParams) {
			c.errorf(at, "%s takes %d type argument(s), but %d were given", name, len(fn.TypeParams), len(typeArgs))
			reported()
			return fail()
		}
		for i, ta := range typeArgs {
			if ta == nil {
				continue // a method's, decided by its receiver
			}
			t := c.resolveType(ta)
			if t == Invalid {
				reported()
				return fail()
			}
			c.bindUnknown(unknowns[i], t)
			var cons []*Constraint
			for _, con := range c.constraintsOf(ta, t, c.paramScope()) {
				if con.Path != "" {
					c.errorf(ta.Pos, "facts inside a type argument (on the parts of %s) are not supported yet, so they would not be checked; only facts on the whole type argument are", t)
					break
				}
				cons = append(cons, con)
			}
			if len(cons) > 0 {
				if argFacts == nil {
					argFacts = make([][]*Constraint, len(fn.TypeParams))
				}
				argFacts[i] = cons
			}
		}
	}
	if recv == nil && fn.Decl != nil && fn.Decl.IsMethod {
		if owner := c.methodReferenceOwner(e.Fun); owner != nil {
			c.solve(params[0], owner)
			if !c.couldFit(params[0], owner) {
				c.errorf(at, "%s requires receiver %s, found owner %s", name, c.zonk(params[0]), owner)
				return fail()
			}
		}
	}
	types := make([]Type, len(args))
	check := func(i int, a syntax.Expr) {
		switch {
		case i == 0 && recv != nil:
			types[i] = recv
			c.solve(params[i], recv)
			return
		case c.sharedDefaults[a]:
			types[i] = c.info.types[a]
			return
		}
		if i >= len(params) {
			types[i] = c.expr(a)
			return
		}
		pw := c.zonk(params[i])
		if l, ok := a.(*syntax.Lambda); ok {
			types[i] = c.record(l, c.lambda(l, pw))
		} else {
			types[i] = c.exprWant(a, pw)
		}
		c.solve(params[i], types[i])
	}
	later := func(a syntax.Expr) bool {
		a = debugSyntaxValue(a)
		_, isLambda := a.(*syntax.Lambda)
		return isLambda || c.genericFuncRef(a)
	}
	var pending []int
	for i, a := range args {
		if !later(a) {
			if i < len(params) && c.contextNeedsType(a, params[i]) {
				pending = append(pending, i)
				continue
			}
			check(i, a)
		}
	}
	if want != nil {
		// The context decides what the arguments leave open
		// (`xs: List[Int] = empty()`), also for the lambdas.
		c.solve(result, want)
	}
	for i, a := range args {
		if later(a) {
			if i < len(params) && c.contextNeedsType(a, params[i]) {
				pending = append(pending, i)
				continue
			}
			check(i, a)
		}
	}
	for len(pending) > 0 {
		var remaining []int
		for _, i := range pending {
			if c.contextNeedsType(args[i], params[i]) {
				remaining = append(remaining, i)
			} else {
				check(i, args[i])
			}
		}
		if len(remaining) == len(pending) {
			// No argument can supply more nominal context. Checking now
			// gives the literal's specific missing-context diagnostic.
			for _, i := range remaining {
				check(i, args[i])
			}
			break
		}
		pending = remaining
	}
	errs := c.diags.Len() - errorsBefore
	// What is known of the values a type parameter stands for selects
	// constrained instances: an explicit type argument's constraints, or
	// those declared for the argument a class method is called on.
	have := argFacts
	if have == nil {
		// Only what every argument of the type is known to be counts:
		// the instance may be used on any of them.
		for j, tp := range fn.TypeParams {
			var common []*Constraint
			first := true
			for i, p := range fn.Params {
				if i >= len(args) || !mentionsParam(p, tp) {
					continue
				}
				if p != Type(tp) {
					common = nil // List[T] and the like: not tracked
					break
				}
				cons := c.declaredFacts(args[i])
				if first {
					common, first = cons, false
					continue
				}
				var kept []*Constraint
				for _, con := range common {
					if len(missingConstraints(cons, []*Constraint{con})) == 0 {
						kept = append(kept, con)
					}
				}
				common = kept
			}
			if len(common) > 0 {
				if have == nil {
					have = make([][]*Constraint, len(fn.TypeParams))
				}
				have[j] = common
			}
		}
	}
	s := c.session
	finish := func() {
		reported := errs > 0 || cs.blamed
		for i := range types {
			types[i] = c.zonk(types[i])
		}
		// A type argument still unknown is reported here, unless what
		// it came from is (an empty literal, or a call in the arguments).
		var missing []string
		for i, u := range unknowns {
			if c.unexplained(s, u) {
				missing = append(missing, fn.TypeParams[i].Name)
			}
		}
		if len(missing) > 0 {
			// A wrong argument (or number of them) is the better
			// explanation.
			for i, t := range types {
				switch {
				case t == Invalid:
					reported = true
				case i < len(params) && !c.couldFit(params[i], t):
					c.errorf(args[i].Position(), "%s to %s must be %s, found %s", argLabel(fn, i), name, c.zonk(params[i]), t)
					reported = true
				}
			}
			if !reported {
				c.errorf(e.Pos, "cannot tell what %s is in this call to %s; give the arguments (or the result) a known type", strings.Join(missing, " and "), name)
			}
			for _, u := range unknowns {
				c.explain(s, u)
			}
			fail()
			return
		}
		inst := &Instance{Func: fn, Params: fn.Params, Result: fn.Result}
		if len(fn.TypeParams) > 0 {
			inst = &Instance{Func: fn, Result: c.zonk(result)}
			for _, u := range unknowns {
				inst.TypeArgs = append(inst.TypeArgs, c.zonk(u))
			}
			for _, p := range params {
				inst.Params = append(inst.Params, c.zonk(p))
			}
			for _, ta := range inst.TypeArgs {
				if c.open(ta) {
					// What stayed unknown is reported where it came from.
					fail()
					return
				}
			}
		}
		if !c.checkParallelInstance(inst, e.Pos) || !c.checkOpaqueInstance(inst, e.Pos, args) {
			fail()
			return
		}
		// An abstract input can consume or forward an open value, but it
		// must not escape through a generic result with untracked effects.
		for i, ta := range inst.TypeArgs {
			if mentionsOpen(ta) && mentionsParam(fn.Result, fn.TypeParams[i]) && !reported {
				c.diags.AddCode(e.Pos, "effect.open-type-argument", "%s of %s cannot be %s: it uses what an open parameter uses, which its caller chooses, so it can only be passed to an open parameter or returned as an open result", fn.TypeParams[i].Name, name, innerText(ta, c.pkg))
				fail()
				return
			}
			if ta == Unit && !reported {
				hint := ""
				if fn.Prelude && fn.Decl.Name == "spawn" {
					hint = " (to run work that gives no value, use launch)"
				} else if fn.Prelude && fn.Decl.Name == "withTimeout" {
					hint = " (for a Unit callback, use withTimeoutDo)"
				}
				c.errorf(e.Pos, "%s of %s cannot be %s: a type argument must be a type of values%s", fn.TypeParams[i].Name, name, ta, hint)
				fail()
				return
			}
		}
		if !c.resolveDictsWith(inst, e.Pos, have) {
			fail()
			return
		}
		if argFacts != nil && !c.promisesArgFacts(inst, argFacts, name, e.Pos) {
			fail()
			return
		}
		inst.ArgFacts = argFacts
		if len(typeArgs) > 0 {
			c.info.callTypeArgs[e] = typeArgs
		}
		c.info.instances[e] = inst
		for i, a := range args {
			if i < len(inst.Params) && types[i] != Invalid && !c.open(types[i]) && !fitsParam(types[i], inst.Params[i]) {
				c.errorf(a.Position(), "%s to %s must be %s, found %s", argLabel(fn, i), name, inst.Params[i], types[i])
			}
		}
	}
	c.session.finish = append(c.session.finish, finish)
	// What the call does is charged where it is (to the lambda it is in).
	zonked := make([]Type, len(types))
	for i, t := range types {
		zonked[i] = c.zonk(t)
	}
	return c.chargeCall(fn, c.zonk(result), zonked)
}

// needsContext reports whether x can only be typed with an expected
// type: a lambda (for its parameters), an empty list, or a variant
// without fields of a generic type (Option.None).
func (c *checker) needsContext(x syntax.Expr) bool {
	x = debugSyntaxValue(x)
	if hasContextLiteral(x) {
		return true
	}
	if c.genericFuncRef(x) {
		return true
	}
	switch x := x.(type) {
	case *syntax.Lambda:
		return true
	case *syntax.ListLit:
		return len(x.Elems) == 0
	case *syntax.MapLit:
		return len(x.Keys) == 0
	case *syntax.Call:
		// A generic call that is given `[]` or `{:}` may need the
		// context too: makeMap({:}).
		id, ok := x.Fun.(*syntax.Ident)
		if !ok || c.lookup(id.Name) != nil {
			return false
		}
		if fn, ok := c.funcNamed(id.Name); !ok || len(fn.TypeParams) == 0 || len(x.TypeArgs) > 0 {
			return false
		}
		return c.hasEmptyLiteralArg(x.Args)
	case *syntax.Selector:
		if owner, ok := c.isTypeRef(x.X); ok {
			s, ok := c.typeNamed(owner).(*Sealed)
			return ok && len(s.TypeParams) > 0
		}
	}
	return false
}

// genericFuncRef reports whether x names a generic function (used as a
// value), whose type arguments come from the context.
func (c *checker) genericFuncRef(x syntax.Expr) bool {
	if sel, ok := x.(*syntax.Selector); ok {
		fn, _, _ := c.methodReference(sel)
		return fn != nil && len(fn.TypeParams) > 0
	}
	id, ok := x.(*syntax.Ident)
	if !ok || c.lookup(id.Name) != nil {
		return false
	}
	fn, ok := c.funcNamed(id.Name)
	return ok && len(fn.TypeParams) > 0
}

// callValue checks a call of a function value: `f(x)`, `make(1)(2)`.
func (c *checker) callValue(e *syntax.Call) Type {
	return c.callFuncValue(e, c.expr(e.Fun))
}

// callFuncValue checks a call of a function value, e.Fun, of type t.
func (c *checker) callFuncValue(e *syntax.Call, t Type) Type {
	c.rejectNamedArgs(e, "function types do not carry parameter names; call the declaration directly")
	ft, ok := t.(*FuncType)
	if !ok {
		if t != Invalid {
			c.errorf(e.Fun.Position(), "cannot call a value of type %s", t)
		}
		for _, a := range e.Args {
			c.expr(a)
		}
		return Invalid
	}
	if len(e.Args) != len(ft.Params) {
		c.errorf(e.Pos, "this function takes %d argument(s), but %d were given", len(ft.Params), len(e.Args))
	}
	c.used |= ft.Effects
	for i, a := range e.Args {
		if i >= len(ft.Params) {
			c.expr(a)
			continue
		}
		at := c.exprWant(a, ft.Params[i])
		if at, pt := c.settle(at, ft.Params[i]); !assignable(at, pt) {
			c.errorf(a.Position(), "argument %d must be %s, found %s", i+1, pt, at)
		}
	}
	return ft.Result
}

// funcValue checks a function used as a value: `xs.map(double)`. A
// generic function takes its type arguments from the expected type.
func (c *checker) funcValue(e syntax.Expr, name string, fn *Func, want Type) Type {
	if c.fn != nil {
		c.fn.Calls = append(c.fn.Calls, fn)
	}
	inst := &Instance{Func: fn, Params: fn.Params, Result: fn.Result}
	if len(fn.TypeParams) > 0 {
		fresh := map[*TypeParam]Type{}
		inst = &Instance{Func: fn}
		for _, tp := range fn.TypeParams {
			u := newUnknown(tp)
			fresh[tp] = u
			inst.TypeArgs = append(inst.TypeArgs, u)
		}
		if owner := c.methodReferenceOwner(e); owner != nil {
			c.solve(subst(fn.Params[0], fresh), owner)
		}
		if want != nil {
			c.solve(subst(fn.funcType(), fresh), want)
		}
		var missing []string
		for i, ta := range inst.TypeArgs {
			if c.open(ta) {
				missing = append(missing, fn.TypeParams[i].Name)
			}
			inst.TypeArgs[i] = c.zonk(ta)
		}
		if len(missing) > 0 {
			c.errorf(e.Position(), "cannot tell what %s is for %s here; use it where a function type is expected, or call it in a lambda", strings.Join(missing, " and "), name)
			return Invalid
		}
		bound := bindParams(fn.TypeParams, inst.TypeArgs)
		inst.Result = subst(fn.Result, bound)
		for _, p := range fn.Params {
			inst.Params = append(inst.Params, subst(p, bound))
		}
	}
	if owner := c.methodReferenceOwner(e); owner != nil && !c.couldFit(inst.Params[0], owner) {
		c.errorf(e.Position(), "%s requires receiver %s, found owner %s", name, inst.Params[0], owner)
		return Invalid
	}
	if !c.checkParallelInstance(inst, e.Position()) || !c.checkOpaqueInstance(inst, e.Position(), nil) {
		return Invalid
	}
	if !c.resolveDicts(inst, e.Position()) {
		return Invalid
	}
	c.info.funcRefs[e] = inst
	return closeOpen(&FuncType{Params: inst.Params, Result: inst.Result, Effects: inst.Func.Effects})
}

// lambda checks a lambda. want is the expected function type, if any,
// which may have unknowns (see infer.go).
func (c *checker) lambda(e *syntax.Lambda, want Type) Type {
	wf, _ := want.(*FuncType)
	quiet := false // the lambda is already reported
	if wf != nil && len(wf.Params) != len(e.Params) {
		c.errorf(e.Pos, "expected a function taking %d argument(s), but this lambda takes %d", len(wf.Params), len(e.Params))
		wf, quiet = nil, true
	}
	ft := &FuncType{}
	c.pushScope()
	defer c.popScope()
	for i, p := range e.Params {
		var t Type
		switch {
		case p.Type != nil:
			t = c.resolveType(p.Type)
			if wf != nil && t != Invalid && !c.open(wf.Params[i]) && !identical(t, wf.Params[i]) {
				c.errorf(p.Type.Pos, "parameter %s must be %s here, found %s", p.Name, wf.Params[i], t)
			}
		case wf != nil && !c.unbound(wf.Params[i]):
			// Unknowns in it are decided later, or else reported
			// (see closeSession).
			t = wf.Params[i]
			if c.open(t) {
				c.session.params = append(c.session.params, &openParam{lambda: e, param: p, t: t, call: c.session.cur})
			}
		default:
			if !quiet {
				c.cannotTellParam(e, p)
			}
			t = Invalid
		}
		if t == Unit {
			c.errorf(p.Pos, "parameter %s cannot have type Unit", p.Name)
			t = Invalid
		}
		ft.Params = append(ft.Params, t)
		c.bind(p.Name, p.Pos, t, p)
		c.scopes[len(c.scopes)-1][p.Name].node = nil // unused parameters are fine
	}
	var rw Type
	if wf != nil && wf.Result != Unit {
		rw = wf.Result
	}
	c.lambdaDepth++
	outer := c.used
	c.used = 0
	bt := c.exprWant(e.Body, rw)
	ft.Effects = c.used
	c.used = outer
	c.lambdaDepth--
	switch {
	case bt == Invalid:
		ft.Result = Invalid
	case wf != nil && wf.Result == Unit:
		ft.Result = Unit // the body's value, if any, is dropped
	case rw != nil && assignable(c.settle(bt, rw)):
		ft.Result = rw
	case bt == Never:
		ft.Result = Unit
		if rw != nil {
			ft.Result = rw
		}
	default:
		ft.Result = bt
	}
	for _, p := range ft.Params {
		if p == Invalid {
			return Invalid
		}
	}
	if ft.Result == Invalid {
		return Invalid
	}
	return ft
}

// hasEmptyLiteralArg reports whether an argument takes its type from
// the context and is not a lambda (`[]`, `{:}`, `makeMap({:})`).
func (c *checker) hasEmptyLiteralArg(args []syntax.Expr) bool {
	for _, a := range args {
		if _, isLambda := a.(*syntax.Lambda); !isLambda && c.needsContext(a) {
			return true
		}
	}
	return false
}

// argLabel names a call's i-th argument (from 0) in messages; a
// method's receiver is not counted.
func argLabel(fn *Func, i int) string {
	if fn.Decl != nil && fn.Decl.IsMethod {
		if i == 0 {
			return "the receiver"
		}
		return fmt.Sprintf("argument %d", i)
	}
	return fmt.Sprintf("argument %d", i+1)
}

// listLit checks a list literal. Its element type comes from the
// context, or else from the elements, which must then agree.
func (c *checker) listLit(e *syntax.ListLit, want Type) Type {
	var ew Type
	if wl, ok := want.(*List); ok {
		ew = wl.Elem
	}
	if len(e.Elems) == 0 {
		report := func() {
			c.diags.AddCode(e.Pos, "type.empty-list", "cannot tell the type of an empty list; give it one, as in xs: List[Int] = []")
		}
		switch {
		case c.open(want):
			// What it holds is decided later in the call it is given to.
			t := c.addOrigin(&List{Elem: newUnknown(listElem)}, report)
			c.solve(want, t)
			return t
		case ew == nil:
			report()
			return Invalid
		}
		// An empty callback list contributes no effects.
		return &List{Elem: closeOne(ew)}
	}
	t := c.elems(e.Pos, e.Elems, ew, "list element", "list elements have")
	switch {
	case t == Invalid:
		return Invalid
	case !isValue(t):
		c.errorf(e.Pos, "a list cannot hold %s", t)
		return Invalid
	}
	return &List{Elem: t}
}

// mapLit checks a map literal. Its key and value types come from the
// context, or else from the entries, which must then agree.
func (c *checker) mapLit(e *syntax.MapLit, want Type) Type {
	var kw, vw Type
	if wm, ok := want.(*Map); ok {
		kw, vw = wm.Key, wm.Value
	}
	if len(e.Keys) == 0 {
		report := func() {
			c.diags.AddCode(e.Pos, "type.empty-map", "cannot tell the type of an empty map; give it one, as in m: Map[String, Int] = {:}")
		}
		switch {
		case c.open(want):
			// What it holds is decided later in the call it is given to.
			t := c.addOrigin(&Map{Key: newUnknown(mapKey), Value: newUnknown(mapValue)}, report)
			c.solve(want, t)
			return t
		case kw == nil:
			report()
			return Invalid
		}
		return &Map{Key: kw, Value: vw}
	}
	k := c.elems(e.Pos, e.Keys, kw, "map key", "map keys have")
	v := c.elems(e.Pos, e.Values, vw, "map value", "map values have")
	if k == Invalid || v == Invalid {
		return Invalid
	}
	for _, t := range []Type{k, v} {
		if !isValue(t) {
			c.errorf(e.Pos, "a map cannot hold %s", t)
			return Invalid
		}
	}
	if !comparable(k) {
		c.errorf(e.Keys[0].Position(), "a Map's keys must be comparable with ==, and %s is not", k)
		return Invalid
	}
	return &Map{Key: k, Value: v}
}

// elems checks the elements of a list literal (or the keys or values
// of a map literal), and gives their type: ew if the context gives
// one, or else the type they agree on.
func (c *checker) elems(pos diag.Pos, elems []syntax.Expr, ew Type, what, agree string) Type {
	ts := make([]Type, len(elems))
	// Elements whose type comes from the context (`Option.None`) are
	// checked last, against the others' type if there is no context.
	var later []int
	for i, x := range elems {
		if (ew == nil || c.unbound(ew)) && c.branchNeedsContext(x) {
			later = append(later, i)
			continue
		}
		ts[i] = c.exprWant(x, ew)
	}
	for _, i := range later {
		w := ew
		for j, t := range ts {
			if t != nil && t != Invalid && (len(later) == 0 || j != i) {
				w = t
				break
			}
		}
		ts[i] = c.exprWant(elems[i], w)
	}
	if isOpen(ew) {
		var effects Effects
		for i, t := range ts {
			t, want := c.settle(t, ew)
			if t == Invalid || !fitsParam(t, want) {
				c.errorf(elems[i].Position(), "%s must be %s, found %s", what, want, t)
				return Invalid
			}
			if f, ok := t.(*FuncType); ok {
				effects |= f.Effects
			}
		}
		f := c.zonk(ew).(*FuncType)
		return &FuncType{Params: f.Params, Result: f.Result, Effects: effects}
	}
	if ew != nil {
		ok := true
		for i, t := range ts {
			if t, ew := c.settle(t, ew); t != Invalid && !assignable(t, ew) {
				c.errorf(elems[i].Position(), "%s must be %s, found %s", what, ew, t)
				ok = false
			}
		}
		if !ok {
			return Invalid
		}
		return ew
	}
	return c.unify(pos, agree, ts, nil)
}

// mentionsParam reports whether t mentions the type parameter tp.
func mentionsParam(t Type, tp *TypeParam) bool {
	return mentionsWhere(t, func(p *TypeParam) bool { return p == tp })
}

// mentionsWhere reports whether t mentions a type parameter for which
// pred holds.
func mentionsWhere(t Type, pred func(*TypeParam) bool) bool {
	switch t := t.(type) {
	case *TypeParam:
		return pred(t)
	case *Seq:
		return mentionsWhere(t.Elem, pred)
	case *List:
		return mentionsWhere(t.Elem, pred)
	case *Map:
		return mentionsWhere(t.Key, pred) || mentionsWhere(t.Value, pred)
	case *FuncType:
		for _, p := range t.Params {
			if mentionsWhere(p, pred) {
				return true
			}
		}
		return mentionsWhere(t.Result, pred)
	case *Union:
		for _, m := range t.Members {
			if mentionsWhere(m, pred) {
				return true
			}
		}
	case *Record:
		for _, a := range t.Args {
			if mentionsWhere(a, pred) {
				return true
			}
		}
	case *Sealed:
		for _, a := range t.Args {
			if mentionsWhere(a, pred) {
				return true
			}
		}
	}
	return false
}
