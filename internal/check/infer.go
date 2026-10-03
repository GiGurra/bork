package check

import (
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Local type inference.
//
// A call of a generic function gives each of the function's type
// parameters a fresh unknown: a type not known yet, which unification
// with the arguments' types (and with the type the call is expected to
// have) decides. Arguments are checked left to right, except lambdas
// and generic functions used as values, which come last: they take
// their parameters' types from what the others decided.
//
// An empty `[]` or `{:}`, or `Option.None`, whose expected type is not
// known yet gets unknowns of its own, which later arguments or a
// lambda's body decide: in words.fold({:}, (m, w) => m.put(w, 1)),
// `{:}` is a Map of two unknowns, which put decides.
//
// The calls nested in a call's arguments (also in its lambdas' bodies)
// share one session with it, which closes when the outermost call has
// been checked. Then what is still unknown is reported where it comes
// from, and each call is completed: its instance, its dictionaries, and
// the checks of its arguments against the parameters' final types.
type session struct {
	cur     *callState
	origins []*origin
	params  []*openParam
	finish  []func()
	// explained holds the unknowns that stayed unknown and are
	// reported: those of an origin or a lambda parameter, or a call's.
	explained map[*TypeParam]bool
}

// callState is what a session knows of one of its calls.
type callState struct {
	parent *callState
	// blamed is set when something in the call's arguments is
	// reported, which then explains what the call cannot tell.
	blamed bool
	// failed is set when the call itself is wrong; it then has no type.
	failed bool
}

func (cs *callState) blame() {
	for ; cs != nil; cs = cs.parent {
		cs.blamed = true
	}
}

// origin is an empty literal or a variant that got unknowns of its own;
// report tells that they stayed unknown.
type origin struct {
	t      Type
	call   *callState
	report func()
}

// openParam is a lambda parameter whose type has unknowns.
type openParam struct {
	lambda *syntax.Lambda
	param  *syntax.Param
	t      Type
	call   *callState
}

// newUnknown makes an unknown for the type parameter tp (with its name,
// for messages, and its bounds).
func newUnknown(tp *TypeParam) *TypeParam {
	return &TypeParam{Name: tp.Name, Decl: tp.Decl, Bounds: tp.Bounds, unknown: true}
}

// newOrigin gives the generic type base unknowns as its arguments, for
// a value that the session must decide; report tells that it could not.
func (c *checker) newOrigin(base Type, report func()) Type {
	params := typeParamsOf(base)
	args := make([]Type, len(params))
	for i, tp := range params {
		args[i] = newUnknown(tp)
	}
	return c.addOrigin(instantiate(base, args), report)
}

// addOrigin records t, which has unknowns of its own, as the type of a
// value that the session must decide.
func (c *checker) addOrigin(t Type, report func()) Type {
	c.session.origins = append(c.session.origins, &origin{t: t, call: c.session.cur, report: report})
	return t
}

// The type parameters that unknowns of empty lists and maps are named
// after.
var (
	listElem = &TypeParam{Name: "T"}
	mapKey   = &TypeParam{Name: "K"}
	mapValue = &TypeParam{Name: "V"}
)

// resolve follows t's solution while t is a solved unknown.
func (c *checker) resolve(t Type) Type {
	for {
		u, ok := t.(*TypeParam)
		if !ok || !u.unknown {
			return t
		}
		s := c.solved[u]
		if s == nil {
			return t
		}
		t = s
	}
}

// unbound reports whether t is an unknown not solved yet.
func (c *checker) unbound(t Type) bool {
	u, ok := c.resolve(t).(*TypeParam)
	return ok && u.unknown
}

func isUnknown(tp *TypeParam) bool { return tp.unknown }

// zonk replaces the solved unknowns in t by their solutions.
func (c *checker) zonk(t Type) Type {
	if len(c.solved) == 0 || !mentionsWhere(t, isUnknown) {
		return t
	}
	switch t := t.(type) {
	case *TypeParam:
		if r := c.resolve(t); r != Type(t) {
			return c.zonk(r)
		}
	case *Seq:
		return &Seq{Elem: c.zonk(t.Elem), Effects: t.Effects}
	case *List:
		return &List{Elem: c.zonk(t.Elem)}
	case *Map:
		return &Map{Key: c.zonk(t.Key), Value: c.zonk(t.Value)}
	case *FuncType:
		ft := &FuncType{Result: c.zonk(t.Result), Effects: t.Effects}
		for _, p := range t.Params {
			ft.Params = append(ft.Params, c.zonk(p))
		}
		return ft
	case *Record, *Sealed:
		args := TypeArgs(t)
		out := make([]Type, len(args))
		for i, a := range args {
			out[i] = c.zonk(a)
		}
		return instantiate(genericBase(t), out)
	case *Union:
		members := make([]Type, len(t.Members))
		for i, m := range t.Members {
			members[i] = c.zonk(m)
		}
		return newUnion(members)
	}
	return t
}

// open reports whether t mentions an unknown not solved yet.
func (c *checker) open(t Type) bool {
	return t != nil && mentionsWhere(c.zonk(t), isUnknown)
}

// bindUnknown solves the unknown u as t, unless t mentions u.
func (c *checker) bindUnknown(u *TypeParam, t Type) {
	if t == Type(u) || mentionsWhere(c.zonk(t), func(tp *TypeParam) bool { return tp == u }) {
		return
	}
	if c.solved == nil {
		c.solved = map[*TypeParam]Type{}
	}
	c.solved[u] = t
}

// solve matches the type p, of a parameter (or another place a value is
// expected), against the type a of the value given for it, solving the
// unknowns of either. A mismatch solves nothing; it is reported by the
// assignability checks that come after.
func (c *checker) solve(p, a Type) {
	if p == nil || a == nil || p == Invalid || a == Invalid || p == Never || a == Never {
		return
	}
	if u, ok := p.(*TypeParam); ok && u.unknown {
		// Functions that differ only in their effects: the unknown is
		// one that may use what either uses.
		if previous, ok := c.solved[u].(*Seq); ok {
			if next, ok := c.resolve(a).(*Seq); ok && identical(previous.Elem, next.Elem) {
				c.solved[u] = &Seq{Elem: previous.Elem, Effects: previous.Effects | next.Effects}
				return
			}
		}
		bf, ok1 := c.solved[u].(*FuncType)
		af, ok2 := c.resolve(a).(*FuncType)
		if ok1 && ok2 && sameSignature(bf, af) {
			c.solved[u] = &FuncType{Params: bf.Params, Result: bf.Result, Effects: bf.Effects | af.Effects}
			return
		}
	}
	p, a = c.resolve(p), c.resolve(a)
	if p == a {
		return
	}
	if u, ok := p.(*TypeParam); ok && u.unknown {
		c.bindUnknown(u, a)
		return
	}
	if u, ok := a.(*TypeParam); ok && u.unknown {
		c.bindUnknown(u, p)
		return
	}
	switch p := p.(type) {
	case *Seq:
		if a, ok := a.(*Seq); ok {
			c.solve(p.Elem, a.Elem)
		}
	case *List:
		if a, ok := a.(*List); ok {
			c.solve(p.Elem, a.Elem)
		}
	case *Map:
		if a, ok := a.(*Map); ok {
			c.solve(p.Key, a.Key)
			c.solve(p.Value, a.Value)
		}
	case *Record, *Sealed:
		if base := genericBase(p); base != nil && base == genericBase(a) {
			pa, aa := TypeArgs(p), TypeArgs(a)
			for i := range pa {
				c.solve(pa[i], aa[i])
			}
		}
	case *FuncType:
		if a, ok := a.(*FuncType); ok && len(a.Params) == len(p.Params) {
			for i := range p.Params {
				c.solve(p.Params[i], a.Params[i])
			}
			c.solve(p.Result, a.Result)
		}
	case *Union:
		c.unifyUnion(p, a)
	}
}

// unifyUnion matches a union parameter type against a: in `T | NotFound`
// given `Int | NotFound`, T is what remains.
func (c *checker) unifyUnion(p *Union, a Type) {
	var unknown []Type
	var fixed []Type
	for _, m := range p.Members {
		if c.unbound(m) {
			unknown = append(unknown, m)
		} else {
			fixed = append(fixed, c.zonk(m))
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
	switch len(unknown) {
	case 0:
		// `List[T] | DecodeError` given `List[Int] | DecodeError`: the
		// one member with unknowns takes what remains.
		var open []Type
		for _, m := range p.Members {
			if c.open(m) {
				open = append(open, m)
			}
		}
		if len(open) == 1 && len(rest) == 1 {
			c.solve(open[0], rest[0])
		}
	case 1:
		if len(rest) > 0 {
			c.solve(unknown[0], newUnion(rest))
		}
	}
}

// couldFit reports whether a value of type a could be given for a parameter
// of type p, whatever p's unknowns turn out to be.
func (c *checker) couldFit(p, a Type) bool {
	p = c.zonk(p)
	if !c.open(p) {
		return fitsParam(a, p)
	}
	switch p := p.(type) {
	case *TypeParam, *Union:
		return true
	case *Seq:
		a, ok := a.(*Seq)
		return ok && a.Effects&^p.Effects == 0 && c.couldFit(p.Elem, a.Elem)
	case *List:
		a, ok := a.(*List)
		return ok && c.couldFit(p.Elem, a.Elem)
	case *Map:
		a, ok := a.(*Map)
		return ok && c.couldFit(p.Key, a.Key) && c.couldFit(p.Value, a.Value)
	case *FuncType:
		a, ok := a.(*FuncType)
		if !ok || len(a.Params) != len(p.Params) {
			return false
		}
		for i := range p.Params {
			if !c.couldFit(p.Params[i], a.Params[i]) {
				return false
			}
		}
		return c.couldFit(p.Result, a.Result)
	case *Record, *Sealed:
		base := genericBase(p)
		if base == nil || base != genericBase(a) {
			return false
		}
		pa, aa := TypeArgs(p), TypeArgs(a)
		for i := range pa {
			if !c.couldFit(pa[i], aa[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// settle solves the unknowns of a value's type t and of the type want
// it is expected to have (in a session; see infer.go), and gives both
// with what is solved, for an assignability check.
func (c *checker) settle(t, want Type) (Type, Type) {
	if c.session == nil || t == Invalid || want == Invalid {
		return t, want
	}
	c.solve(want, t)
	return c.zonk(t), c.zonk(want)
}

// closeSession reports what the session could not decide, and completes
// its calls, innermost first.
func (c *checker) closeSession() {
	s := c.session
	c.session = nil
	for _, o := range s.origins {
		if c.open(o.t) {
			o.report()
			o.call.blame()
			c.explain(s, o.t)
		}
	}
	for _, p := range s.params {
		if c.open(p.t) {
			c.cannotTellParam(p.lambda, p.param)
			p.call.blame()
			c.explain(s, p.t)
		}
	}
	for _, f := range s.finish {
		f()
	}
}

// explain records that the unknowns left in t are reported.
func (c *checker) explain(s *session, t Type) {
	if s.explained == nil {
		s.explained = map[*TypeParam]bool{}
	}
	mentionsWhere(c.zonk(t), func(tp *TypeParam) bool {
		if tp.unknown {
			s.explained[tp] = true
		}
		return false
	})
}

// unexplained reports whether t has unknowns that are not reported.
func (c *checker) unexplained(s *session, t Type) bool {
	return mentionsWhere(c.zonk(t), func(tp *TypeParam) bool { return tp.unknown && !s.explained[tp] })
}

// cannotTellParam reports that a lambda parameter, written without a
// type, has none that can be told.
func (c *checker) cannotTellParam(e *syntax.Lambda, p *syntax.Param) {
	c.diags.AddCode(p.Pos, "type.lambda-parameter", "cannot tell the type of parameter %s; write it: (%s: Type) => ...", p.Name, p.Name)
	end := p.Pos
	end.Col += len(p.Name)
	edit := diag.TextEdit{Start: end, End: end, Replacement: ": Type"}
	if e.Pos == p.Pos {
		edit = diag.TextEdit{Start: p.Pos, End: end, Replacement: "(" + p.Name + ": Type)"}
	}
	c.diags.Suggest(p.Pos, "type.lambda-parameter", end, diag.Fix{
		Message:       "annotate the parameter (replace Type with its intended type)",
		RequiresInput: true, Edits: []diag.TextEdit{edit},
	})
}

// zonkInfo replaces the solved unknowns in what the checker recorded
// while calls were being inferred, before the typed tree is built.
func (c *checker) zonkInfo() {
	if len(c.solved) == 0 {
		return
	}
	info := c.info
	for x, t := range info.types {
		info.types[x] = c.zonk(t)
	}
	for b, t := range info.bindings {
		info.bindings[b] = c.zonk(t)
	}
	for _, inst := range info.funcRefs {
		c.zonkInstance(inst)
	}
	for _, inst := range info.instances {
		c.zonkInstance(inst)
	}
	for x, v := range info.selectorVariants {
		info.selectorVariants[x] = c.zonkVariant(v)
	}
	for x, v := range info.contextVariants {
		info.contextVariants[x] = c.zonkVariant(v)
	}
	for x, target := range info.recordTargets {
		switch t := target.(type) {
		case *Variant:
			info.recordTargets[x] = c.zonkVariant(t)
		case Type:
			info.recordTargets[x] = c.zonk(t)
		}
	}
	for _, p := range info.armPats {
		c.zonkPat(p)
	}
	for _, ti := range info.tries {
		ti.Kept = c.zonk(ti.Kept)
		for i, t := range ti.Rest {
			ti.Rest[i] = c.zonk(t)
		}
		if ti.Option != nil {
			ti.Option = c.zonk(ti.Option).(*Sealed)
		}
		if ti.NoneOf != nil {
			ti.NoneOf = c.zonk(ti.NoneOf).(*Sealed)
		}
	}
	for _, src := range info.patSources {
		src.Member = c.zonk(src.Member)
	}
}

func (c *checker) zonkInstance(inst *Instance) {
	for i, t := range inst.TypeArgs {
		inst.TypeArgs[i] = c.zonk(t)
	}
	for i, t := range inst.Params {
		inst.Params[i] = c.zonk(t)
	}
	inst.Result = c.zonk(inst.Result)
}

// zonkVariant is v, of an instance with solved unknowns, in the
// instance with their solutions.
func (c *checker) zonkVariant(v *Variant) *Variant {
	if !mentionsWhere(v.Parent, isUnknown) {
		return v
	}
	if s, ok := c.zonk(v.Parent).(*Sealed); ok {
		return s.Variant(v.Name)
	}
	return v
}

func (c *checker) zonkPat(p *Pat) {
	if p == nil {
		return
	}
	p.Type = c.zonk(p.Type)
	p.BindType = c.zonk(p.BindType)
	if p.Variant != nil {
		p.Variant = c.zonkVariant(p.Variant)
	}
	for i, m := range p.Members {
		p.Members[i] = c.zonk(m)
	}
	for _, f := range p.Fields {
		c.zonkPat(f.Pat)
	}
	c.zonkPat(p.Sub)
	for _, e := range p.Elems {
		c.zonkPat(e)
	}
	c.zonkPat(p.Rest)
}
