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
	// TypeArgExprs holds a call's explicit type arguments as written,
	// one per type parameter (nil for those a method's receiver
	// decides), or nil if there are none.
	TypeArgExprs []*syntax.TypeExpr
}

// funcType is the type of fn as a value.
func (fn *Func) funcType() *FuncType {
	return &FuncType{Params: fn.Params, Result: fn.Result}
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

// inference solves the type parameters of one use of a generic function.
type inference struct {
	fn     *Func // nil when solving a generic type's parameters
	params []*TypeParam
	bound  map[*TypeParam]Type
	// foreign, if set, tells type parameters that must not be bound to:
	// unknowns of another inference.
	foreign func(*TypeParam) bool
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
		if ta, ok := a.(*TypeParam); ok && in.foreign != nil && in.foreign(ta) {
			return
		}
		if in.owns(p) && in.bound[p] == nil {
			in.bound[p] = a
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
	case *List:
		return &List{Elem: subst(t.Elem, bound)}
	case *Map:
		return &Map{Key: subst(t.Key, bound), Value: subst(t.Value, bound)}
	case *FuncType:
		ft := &FuncType{Result: subst(t.Result, bound)}
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

// fits reports whether a value of type a could be given for a parameter
// of type p, whatever the open type parameters in p turn out to be.
func (in *inference) fits(p, a Type) bool {
	if !in.open(p) {
		return assignable(a, p)
	}
	switch p := p.(type) {
	case *TypeParam, *Union:
		return true
	case *List:
		a, ok := a.(*List)
		return ok && in.fits(p.Elem, a.Elem)
	case *Map:
		a, ok := a.(*Map)
		return ok && in.fits(p.Key, a.Key) && in.fits(p.Value, a.Value)
	case *FuncType:
		a, ok := a.(*FuncType)
		if !ok || len(a.Params) != len(p.Params) {
			return false
		}
		for i := range p.Params {
			if !in.fits(p.Params[i], a.Params[i]) {
				return false
			}
		}
		return in.fits(p.Result, a.Result)
	case *Record, *Sealed:
		base := genericBase(p)
		if base == nil || base != genericBase(a) {
			return false
		}
		pa, aa := TypeArgs(p), TypeArgs(a)
		for i := range pa {
			if !in.fits(pa[i], aa[i]) {
				return false
			}
		}
		return true
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
// Arguments are checked left to right, except that those that need a
// type from the context (lambdas, `[]`, `Option.None`) come last, once
// the other arguments have decided what they can.
func (c *checker) callFunc(e *syntax.Call, name string, fn *Func, args []syntax.Expr, recv Type, typeArgs []*syntax.TypeExpr, want Type) Type {
	errorsBefore := c.diags.Len()
	c.info.CallFuncs[e] = fn
	if c.fn != nil {
		c.fn.Calls = append(c.fn.Calls, fn)
	}
	args = c.withDefaults(args, fn)
	c.info.CallArgs[e] = args
	if len(args) != len(fn.Params) {
		if req := requiredParams(fn); req < len(fn.Params) {
			c.errorf(e.Pos, "%s takes %d to %d argument(s), but %d were given", name, req, len(fn.Params), len(args))
		} else {
			c.errorf(e.Pos, "%s takes %d argument(s), but %d were given", name, len(fn.Params), len(args))
		}
	}
	var in *inference
	if len(fn.TypeParams) > 0 {
		in = newInference(fn)
	}
	var argFacts [][]*Constraint
	if len(typeArgs) > 0 {
		if len(typeArgs) != len(fn.TypeParams) {
			c.errorf(e.Pos, "%s takes %d type argument(s), but %d were given", name, len(fn.TypeParams), len(typeArgs))
			return Invalid
		}
		for i, ta := range typeArgs {
			if ta == nil {
				continue // a method's, decided by its receiver
			}
			t := c.resolveType(ta)
			if t == Invalid {
				return Invalid
			}
			in.bound[fn.TypeParams[i]] = t
			var cons []*Constraint
			for _, con := range c.constraintsOf(ta, t, c.paramScope()) {
				if con.Path == "" {
					cons = append(cons, con)
				}
			}
			if len(cons) > 0 {
				if argFacts == nil {
					argFacts = make([][]*Constraint, len(fn.TypeParams))
				}
				argFacts[i] = cons
			}
		}
	}
	types := make([]Type, len(args))
	check := func(i int, a syntax.Expr) {
		switch {
		case i == 0 && recv != nil:
			types[i] = recv
			if in != nil {
				in.unify(fn.Params[i], recv)
			}
			return
		case c.sharedDefaults[a]:
			types[i] = c.info.Types[a]
			return
		}
		if i >= len(fn.Params) {
			types[i] = c.expr(a)
			return
		}
		pw := fn.Params[i]
		if in != nil {
			pw = in.subst(pw)
		}
		if l, ok := a.(*syntax.Lambda); ok {
			types[i] = c.record(l, c.lambda(l, pw, in))
		} else {
			if in.open(pw) && !c.genericFuncRef(a) {
				pw = nil
			}
			types[i] = c.exprWant(a, pw)
		}
		if in != nil {
			in.unify(fn.Params[i], types[i])
		}
	}
	for i, a := range args {
		if !c.needsContext(a) {
			check(i, a)
		}
	}
	if in != nil && want != nil {
		// The context decides what the arguments leave open
		// (`xs: List[Int] = empty()`), also for the lambdas' results.
		in.unify(fn.Result, want)
	}
	// A lambda's body can decide what `[]` or `{:}` leaves open:
	// fold(xs, [], (acc, x) => append(acc, x)).
	if in != nil && c.hasEmptyLiteralArg(args) {
		for i, a := range args {
			if l, ok := a.(*syntax.Lambda); ok && i < len(fn.Params) && lambdaParamsOpen(l, in.subst(fn.Params[i]), in) {
				c.inferFromBody(l, fn.Params[i], in)
			}
		}
	}
	for i, a := range args {
		if c.needsContext(a) {
			check(i, a)
		}
	}
	inst := &Instance{Func: fn, Params: fn.Params, Result: fn.Result}
	if in != nil {
		if missing := in.unsolved(); len(missing) > 0 {
			// A wrong argument is the better explanation.
			reported := false
			for i, t := range types {
				switch {
				case t == Invalid:
					reported = true
				case i < len(fn.Params) && !in.fits(in.subst(fn.Params[i]), t):
					c.errorf(args[i].Position(), "%s to %s must be %s, found %s", argLabel(fn, i), name, in.subst(fn.Params[i]), t)
					reported = true
				}
			}
			if !reported {
				c.errorf(e.Pos, "cannot tell what %s is in this call to %s; give the arguments (or the result) a known type", strings.Join(missing, " and "), name)
			}
			return Invalid
		}
		inst = in.instance()
	}
	for i, ta := range inst.TypeArgs {
		if ta == Unit && c.diags.Len() == errorsBefore {
			hint := ""
			if fn.Prelude && fn.Decl.Name == "spawn" {
				hint = " (to run work that gives no value, use launch)"
			}
			c.errorf(e.Pos, "%s of %s cannot be %s: a type argument must be a type of values%s", fn.TypeParams[i].Name, name, ta, hint)
			return Invalid
		}
	}
	if fn.Prelude && fn.Decl.Name == "attach" && c.diags.Len() == errorsBefore {
		if _, ok := inst.TypeArgs[0].(*Resource); !ok {
			c.errorf(e.Pos, "attach takes a resource (a value of a resource type, such as File), found %s", inst.TypeArgs[0])
			return Invalid
		}
	}
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
	if !c.resolveDictsWith(inst, e.Pos, have) {
		return Invalid
	}
	if argFacts != nil && !c.promisesArgFacts(inst, argFacts, name, e.Pos) {
		return Invalid
	}
	inst.ArgFacts = argFacts
	inst.TypeArgExprs = typeArgs
	c.info.Instances[e] = inst
	for i, a := range args {
		if i < len(inst.Params) && types[i] != Invalid && !assignable(types[i], inst.Params[i]) {
			c.errorf(a.Position(), "%s to %s must be %s, found %s", argLabel(fn, i), name, inst.Params[i], types[i])
		}
	}
	return inst.Result
}

// needsContext reports whether x can only be typed with an expected
// type: a lambda (for its parameters), an empty list, or a variant
// without fields of a generic type (Option.None).
func (c *checker) needsContext(x syntax.Expr) bool {
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
		// context too: maps.Sorted({:}).
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
	for i, a := range e.Args {
		if i >= len(ft.Params) {
			c.expr(a)
			continue
		}
		at := c.exprWant(a, ft.Params[i])
		if !assignable(at, ft.Params[i]) {
			c.errorf(a.Position(), "argument %d must be %s, found %s", i+1, ft.Params[i], at)
		}
	}
	return ft.Result
}

// funcValue checks a function used as a value: `map(xs, double)`. A
// generic function takes its type arguments from the expected type.
func (c *checker) funcValue(e *syntax.Ident, fn *Func, want Type) Type {
	if c.fn != nil {
		c.fn.Calls = append(c.fn.Calls, fn)
	}
	inst := &Instance{Func: fn, Params: fn.Params, Result: fn.Result}
	if len(fn.TypeParams) > 0 {
		in := newInference(fn)
		// The expected type may still mention type parameters of a call
		// being inferred (map's B in map(xs, show)); those say nothing.
		in.foreign = func(tp *TypeParam) bool { return !c.inScopeParam(tp) }
		if want != nil {
			in.unify(fn.funcType(), want)
		}
		if missing := in.unsolved(); len(missing) > 0 {
			c.errorf(e.Pos, "cannot tell what %s is for %s here; use it where a function type is expected, or call it in a lambda", strings.Join(missing, " and "), e.Name)
			return Invalid
		}
		inst = in.instance()
	}
	if !c.resolveDicts(inst, e.Pos) {
		return Invalid
	}
	c.info.FuncRefs[e] = inst
	return &FuncType{Params: inst.Params, Result: inst.Result}
}

// lambda checks a lambda. want is the expected function type, if any;
// in the middle of inferring a generic call, in tells which of its
// type parameters are not known yet.
func (c *checker) lambda(e *syntax.Lambda, want Type, in *inference) Type {
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
			if wf != nil && t != Invalid && !in.open(wf.Params[i]) && !identical(t, wf.Params[i]) {
				c.errorf(p.Type.Pos, "parameter %s must be %s here, found %s", p.Name, wf.Params[i], t)
			}
		case wf != nil && !in.open(wf.Params[i]):
			t = wf.Params[i]
		default:
			if !quiet {
				c.errorf(p.Pos, "cannot tell the type of parameter %s; write it: (%s: Type) => ...", p.Name, p.Name)
			}
			t = Invalid
		}
		if t == Unit {
			c.errorf(p.Pos, "parameter %s cannot have type Unit", p.Name)
			t = Invalid
		}
		ft.Params = append(ft.Params, t)
		c.info.LambdaParams[p] = true
		c.bind(p.Name, p.Pos, t, p)
		c.scopes[len(c.scopes)-1][p.Name].node = nil // unused parameters are fine
	}
	var rw Type
	if wf != nil && !in.open(wf.Result) && wf.Result != Unit {
		rw = wf.Result
	}
	c.lambdaDepth++
	bt := c.exprWant(e.Body, rw)
	c.lambdaDepth--
	switch {
	case bt == Invalid:
		ft.Result = Invalid
	case wf != nil && wf.Result == Unit:
		ft.Result = Unit // the body's value, if any, is dropped
	case rw != nil && assignable(bt, rw):
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
// the context and is not a lambda (`[]`, `{:}`, `maps.Sorted({:})`).
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

// lambdaParamsOpen reports whether some parameter of lambda l, written
// without a type, would get its type from a type parameter that in has
// not decided yet.
func lambdaParamsOpen(l *syntax.Lambda, want Type, in *inference) bool {
	ft, ok := want.(*FuncType)
	if !ok || len(ft.Params) != len(l.Params) {
		return false
	}
	for i, p := range l.Params {
		if p.Type == nil && in.open(ft.Params[i]) {
			return true
		}
	}
	return false
}

// inferFromBody decides type parameters from what a lambda's body
// gives, when only an argument that takes its type from the context
// would otherwise decide them: in fold(words, {:}, (m, w) =>
// maps.Put(m, w, 1)), the body gives a Map[String, Int], which is the
// accumulator's type. The body is checked quietly, with the parameters
// of undecided types as holes (a type that nothing is known of); if
// its result is then a type without holes, the lambda's result type
// is unified with it. The real checks come after.
func (c *checker) inferFromBody(l *syntax.Lambda, p Type, in *inference) {
	pt, ok := in.subst(p).(*FuncType)
	orig, ok2 := p.(*FuncType)
	if !ok || !ok2 || len(pt.Params) != len(l.Params) {
		return
	}
	hole := &TypeParam{Name: "?", Hole: true}
	spec := &FuncType{}
	for _, q := range pt.Params {
		if in.open(q) {
			q = hole
		}
		spec.Params = append(spec.Params, q)
	}
	before := c.diags.Len()
	t := c.lambda(l, spec, nil)
	c.diags.Truncate(before)
	ft, ok := t.(*FuncType)
	if !ok || ft.Result == nil || !isValue(ft.Result) || mentionsParam(ft.Result, hole) {
		return
	}
	in.unify(orig.Result, ft.Result)
}

// listLit checks a list literal. Its element type comes from the
// context, or else from the elements, which must then agree.
func (c *checker) listLit(e *syntax.ListLit, want Type) Type {
	var ew Type
	if wl, ok := want.(*List); ok {
		ew = wl.Elem
	}
	if len(e.Elems) == 0 {
		if ew == nil {
			c.errorf(e.Pos, "cannot tell the type of an empty list; give it one, as in xs: List[Int] = []")
			return Invalid
		}
		return &List{Elem: ew}
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
		if kw == nil {
			c.errorf(e.Pos, "cannot tell the type of an empty map; give it one, as in m: Map[String, Int] = {:}")
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
		if ew == nil && c.branchNeedsContext(x) {
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
	if ew != nil {
		ok := true
		for i, t := range ts {
			if t != Invalid && !assignable(t, ew) {
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
	switch t := t.(type) {
	case *TypeParam:
		return t == tp
	case *List:
		return mentionsParam(t.Elem, tp)
	case *Map:
		return mentionsParam(t.Key, tp) || mentionsParam(t.Value, tp)
	case *FuncType:
		for _, p := range t.Params {
			if mentionsParam(p, tp) {
				return true
			}
		}
		return mentionsParam(t.Result, tp)
	case *Union:
		for _, m := range t.Members {
			if mentionsParam(m, tp) {
				return true
			}
		}
	case *Record:
		for _, a := range t.Args {
			if mentionsParam(a, tp) {
				return true
			}
		}
	case *Sealed:
		for _, a := range t.Args {
			if mentionsParam(a, tp) {
				return true
			}
		}
	}
	return false
}
