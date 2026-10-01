package check

import (
	"strings"

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
}

// funcType is the type of fn as a value.
func (fn *Func) funcType() *FuncType {
	return &FuncType{Params: fn.Params, Result: fn.Result}
}

// declareTypeParams makes a generic function's type parameters
// visible while its signature, constraints, and body are resolved.
func (c *checker) declareTypeParams(fd *syntax.FuncDecl, prelude bool) []*TypeParam {
	var out []*TypeParam
	c.typeParams = map[string]*TypeParam{}
	for _, d := range fd.TypeParams {
		switch {
		case c.typeParams[d.Name] != nil:
			c.errorf(d.Pos, "type parameter %s is declared twice", d.Name)
			continue
		case !prelude && c.isTypeName(d.Name):
			c.errorf(d.Pos, "type parameter %s has the name of a type", d.Name)
			continue
		}
		tp := &TypeParam{Name: d.Name, Decl: d}
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
		}
	case *List:
		if a, ok := a.(*List); ok {
			in.unify(p.Elem, a.Elem)
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

// callFunc checks a call of a declared function. Arguments are checked
// left to right, except that those that need a type from the context
// (lambdas, `[]`, `Option.None`) come last, once the other arguments
// have decided what they can.
func (c *checker) callFunc(e *syntax.Call, id *syntax.Ident, fn *Func, want Type) Type {
	c.info.CallFuncs[e] = fn
	if c.fn != nil {
		c.fn.Calls = append(c.fn.Calls, fn)
	}
	if len(e.Args) != len(fn.Params) {
		c.errorf(e.Pos, "%s takes %d argument(s), but %d were given", id.Name, len(fn.Params), len(e.Args))
	}
	var in *inference
	if len(fn.TypeParams) > 0 {
		in = newInference(fn)
	}
	types := make([]Type, len(e.Args))
	check := func(i int, a syntax.Expr) {
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
			if in.open(pw) {
				pw = nil
			}
			types[i] = c.exprWant(a, pw)
		}
		if in != nil {
			in.unify(fn.Params[i], types[i])
		}
	}
	for i, a := range e.Args {
		if !c.needsContext(a) {
			check(i, a)
		}
	}
	if in != nil && want != nil {
		// The context decides what the arguments leave open
		// (`xs: List[Int] = empty()`), also for the lambdas' results.
		in.unify(fn.Result, want)
	}
	for i, a := range e.Args {
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
					c.errorf(e.Args[i].Position(), "argument %d to %s must be %s, found %s", i+1, id.Name, in.subst(fn.Params[i]), t)
					reported = true
				}
			}
			if !reported {
				c.errorf(e.Pos, "cannot tell what %s is in this call to %s; give the arguments (or the result) a known type", strings.Join(missing, " and "), id.Name)
			}
			return Invalid
		}
		inst = in.instance()
	}
	c.info.Instances[e] = inst
	for i, a := range e.Args {
		if i < len(inst.Params) && types[i] != Invalid && !assignable(types[i], inst.Params[i]) {
			c.errorf(a.Position(), "argument %d to %s must be %s, found %s", i+1, id.Name, inst.Params[i], types[i])
		}
	}
	return inst.Result
}

// needsContext reports whether x can only be typed with an expected
// type: a lambda (for its parameters), an empty list, or a variant
// without fields of a generic type (Option.None).
func (c *checker) needsContext(x syntax.Expr) bool {
	switch x := x.(type) {
	case *syntax.Lambda:
		return true
	case *syntax.ListLit:
		return len(x.Elems) == 0
	case *syntax.Selector:
		if owner, ok := c.isTypeRef(x.X); ok {
			s, ok := c.typeNamed(owner).(*Sealed)
			return ok && len(s.TypeParams) > 0
		}
	}
	return false
}

// callValue checks a call of a function value: `f(x)`, `make(1)(2)`.
func (c *checker) callValue(e *syntax.Call) Type {
	t := c.expr(e.Fun)
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
		if want != nil {
			in.unify(fn.funcType(), want)
		}
		if missing := in.unsolved(); len(missing) > 0 {
			c.errorf(e.Pos, "cannot tell what %s is for %s here; use it where a function type is expected, or call it in a lambda", strings.Join(missing, " and "), e.Name)
			return Invalid
		}
		inst = in.instance()
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
	ts := make([]Type, len(e.Elems))
	for i, x := range e.Elems {
		ts[i] = c.exprWant(x, ew)
	}
	if ew != nil {
		ok := true
		for i, t := range ts {
			if t != Invalid && !assignable(t, ew) {
				c.errorf(e.Elems[i].Position(), "list element must be %s, found %s", ew, t)
				ok = false
			}
		}
		if !ok {
			return Invalid
		}
		return &List{Elem: ew}
	}
	t := c.unify(e.Pos, "list elements have", ts, nil)
	switch {
	case t == Invalid:
		return Invalid
	case !isValue(t):
		c.errorf(e.Pos, "a list cannot hold %s", t)
		return Invalid
	}
	return &List{Elem: t}
}
