package check

import (
	"fmt"
	"go/constant"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// The facts pass checks where clauses, after ordinary type checking has
// succeeded. Facts are never computed forwards. Each requirement (an
// obligation) is resolved backwards from where it arises, through
// guards, bindings, declarations, and promised results, until it is
// proven or found unprovable:
//
//   - calling a function whose parameter has a where clause
//   - building a record whose field has one
//   - a typed binding (`port: Port = ...`)
//   - a function's promised result (`): Int where positive`)
//
// Requirements on constants (`connect("db", 5432)`) are decided by
// running the predicate at compile time.

// Query is a predicate call on constants, for evaluation at compile time.
//
// A query may also combine others: with Or set it holds if any of them
// does, with And if all of them do; Pred is then nil.
type Query struct {
	Pred *Func
	Args []constant.Value // the constrained value first
	Or   []Query
	And  []Query
	// Via names the function whose result the constant is, when the
	// query comes from a derived result (see derive).
	Via string
}

func (q Query) String() string {
	join := func(parts []Query, sep string) string {
		out := make([]string, len(parts))
		for i, p := range parts {
			out[i] = p.String()
			if p.Pred == nil && len(parts) > 1 {
				out[i] = "(" + out[i] + ")"
			}
		}
		return strings.Join(out, sep)
	}
	switch {
	case q.Or != nil:
		return join(q.Or, " or ")
	case q.And != nil:
		return join(q.And, " and ")
	}
	args := make([]string, len(q.Args))
	for i, a := range q.Args {
		args[i] = CArg{Const: a}.String()
	}
	return q.Pred.Decl.Name + "(" + strings.Join(args, ", ") + ")"
}

// Evaluator runs predicates on constants at compile time, returning one
// result per query.
type Evaluator func(queries []Query) ([]bool, error)

// Facts checks the where clauses of a type-checked package.
func Facts(files []*syntax.File, info *Info, diags *diag.List, eval Evaluator) {
	f := &factChecker{info: info, diags: diags, paths: map[*Func][]branch{}, active: map[string]bool{}, params: map[*syntax.Param]*syntax.Ident{}}
	for _, file := range files {
		if file.Prelude {
			continue
		}
		for _, fd := range file.Funcs {
			if fn := info.Funcs[fd.Name]; fn != nil && fn.Decl == fd && fd.Body != nil {
				f.function(fn)
			}
		}
	}
	f.evaluate(eval)
}

type factChecker struct {
	info    *Info
	diags   *diag.List
	fn      *Func
	pending []pendingQuery
	// collect, when set, receives result values instead of checking
	// them, and obligations are not checked (see resultPaths).
	collect *[]branch
	paths   map[*Func][]branch
	// active guards against cycles: goals being proven by a rule, and
	// functions whose results are being derived.
	active map[string]bool
	params map[*syntax.Param]*syntax.Ident
}

// A fact is a predicate known to hold: pred(subject, args...), with
// values identified by key (see key). A fact with alternatives (from
// `a || b`) has a nil pred and the alternatives in or, each a list of
// facts that hold together; at least one alternative holds.
type fact struct {
	pred    *Func
	subject string
	args    []argVal
	or      [][]fact
}

// factKey identifies a fact, to tell when it is already in use.
func factKey(ft fact) string {
	if ft.or != nil {
		var alts []string
		for _, alt := range ft.or {
			var all []string
			for _, a := range alt {
				all = append(all, factKey(a))
			}
			alts = append(alts, strings.Join(all, " && "))
		}
		return "(" + strings.Join(alts, " || ") + ")"
	}
	out := ft.pred.Decl.Name + "(" + ft.subject
	for _, a := range ft.args {
		out += ", " + a.key
	}
	return out + ")"
}

// env holds the facts known at a point in a function. Facts are only
// ever added, and only for the code they dominate.
type env struct{ facts []fact }

func (e env) with(fs ...fact) env {
	out := make([]fact, 0, len(e.facts)+len(fs))
	return env{facts: append(append(out, e.facts...), fs...)}
}

// argVal is a value in a fact or obligation: identified by key (empty
// if it cannot be identified), with its value if it is a constant, its
// expression if there is one, and how to show it.
type argVal struct {
	key   string
	value constant.Value
	text  string
	expr  syntax.Expr
}

func sameArgs(a, b []argVal) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].key == "" || a[i].key != b[i].key {
			return false
		}
	}
	return true
}

// known is a fact about some given value: pred(value, args...).
// A known fact with alternatives has a nil pred and them in or.
type known struct {
	pred *Func
	args []argVal
	or   []known
}

func (k known) proves(ob obligation) bool {
	return k.pred != nil && k.pred == ob.pred && sameArgs(k.args, ob.args)
}

// obligation is a requirement pred(subject, args...) at some point.
//
// An obligation with alternatives (from `where p or q`) has a nil pred
// and them in or; proving any of them proves it.
type obligation struct {
	pred *Func
	args []argVal
	or   []obligation
	// requirement says who requires what, for messages:
	// "transfer requires amount to be positive".
	requirement string
	// con is the constraint as written by the requirer.
	con string
}

type pendingQuery struct {
	query Query
	pos   diag.Pos
	ob    obligation
}

func (f *factChecker) function(fn *Func) {
	f.fn = fn
	f.tail(fn.Decl.Body, env{}, f.checkResult)
	f.fn = nil
}

// --- Walking function bodies ---

// tail walks an expression whose value is used by result, passing each
// branch's value (with the facts known in that branch) to result.
func (f *factChecker) tail(x syntax.Expr, e env, result func(syntax.Expr, env)) {
	switch x := x.(type) {
	case *syntax.Block:
		e = f.stmts(x.Stmts, e)
		if x.Tail != nil {
			f.tail(x.Tail, e, result)
		}
	case *syntax.If:
		if x.Else == nil {
			f.walk(x, e)
			return
		}
		f.walk(x.Cond, e)
		f.tail(x.Then, e.with(f.conditionFacts(x.Cond, true)...), result)
		f.tail(x.Else, e.with(f.conditionFacts(x.Cond, false)...), result)
	case *syntax.Match:
		f.walk(x.X, e)
		for _, arm := range x.Arms {
			f.tail(arm.Body, e, result)
		}
	default:
		f.walk(x, e)
		if f.info.Types[x] != Never {
			result(x, e)
		}
	}
}

// stmts walks a block's statements and returns the facts known after
// them.
func (f *factChecker) stmts(list []syntax.Stmt, e env) env {
	for _, s := range list {
		switch s := s.(type) {
		case *syntax.Binding:
			f.walk(s.Value, e)
			for _, con := range f.info.BindingConstraints[s] {
				f.oblige(s.Value, con, f.ownParams(), e, fmt.Sprintf("%s must be %s", s.Name, con))
			}
		case *syntax.ExprStmt:
			f.walk(s.X, e)
			// A guard: `if (!p(x)) { return ... }` leaves p(x) known.
			if ifx, ok := s.X.(*syntax.If); ok {
				if f.info.Types[ifx.Then] == Never {
					e = e.with(f.conditionFacts(ifx.Cond, false)...)
				}
				if ifx.Else != nil && f.info.Types[ifx.Else] == Never {
					e = e.with(f.conditionFacts(ifx.Cond, true)...)
				}
			}
		case *syntax.TrustStmt:
			f.walk(s.Call, e)
			facts := f.conditionFacts(s.Call, true)
			if len(facts) == 0 && f.collect == nil {
				f.diags.Add(s.Call.Args[0].Position(), "trust needs a value with a name (bind it first: x = ...), or the fact could not be used")
			}
			e = e.with(facts...)
		}
	}
	return e
}

// walk visits an expression, checking the obligations inside it.
func (f *factChecker) walk(x syntax.Expr, e env) {
	switch x := x.(type) {
	case *syntax.Call:
		for _, a := range x.Args {
			f.walk(a, e)
		}
		if fn := f.info.CallFuncs[x]; fn != nil {
			f.callObligations(x, fn, e)
		}
	case *syntax.Unary:
		f.walk(x.X, e)
	case *syntax.Binary:
		f.walk(x.X, e)
		switch x.Op {
		case syntax.AndAnd:
			f.walk(x.Y, e.with(f.conditionFacts(x.X, true)...))
		case syntax.OrOr:
			f.walk(x.Y, e.with(f.conditionFacts(x.X, false)...))
		default:
			f.walk(x.Y, e)
		}
	case *syntax.If:
		f.walk(x.Cond, e)
		f.walk(x.Then, e.with(f.conditionFacts(x.Cond, true)...))
		if x.Else != nil {
			f.walk(x.Else, e.with(f.conditionFacts(x.Cond, false)...))
		}
	case *syntax.Block:
		e = f.stmts(x.Stmts, e)
		if x.Tail != nil {
			f.walk(x.Tail, e)
		}
	case *syntax.Return:
		if x.Value != nil {
			f.tail(x.Value, e, f.checkResult)
		}
	case *syntax.Selector:
		f.walk(x.X, e)
	case *syntax.RecordLit:
		for _, fi := range x.Fields {
			f.walk(fi.Value, e)
		}
		f.recordObligations(x, e)
	case *syntax.Copy:
		f.walk(x.X, e)
		for _, u := range x.Updates {
			f.walk(u.Value, e)
		}
		f.copyObligations(x, e)
	case *syntax.Match:
		f.walk(x.X, e)
		for _, arm := range x.Arms {
			f.walk(arm.Body, e)
		}
	case *syntax.Try:
		f.walk(x.X, e)
	case *syntax.Interp:
		for _, ix := range x.Exprs {
			f.walk(ix, e)
		}
	}
}

// resultPaths lists the values a function can return, each with the
// facts known where it is returned.
func (f *factChecker) resultPaths(fn *Func) []branch {
	if paths, ok := f.paths[fn]; ok {
		return paths
	}
	var paths []branch
	saveFn, saveCollect := f.fn, f.collect
	f.fn, f.collect = fn, &paths
	f.tail(fn.Decl.Body, env{}, f.checkResult)
	f.fn, f.collect = saveFn, saveCollect
	f.paths[fn] = paths
	return paths
}

// --- Where obligations arise ---

func (f *factChecker) callObligations(call *syntax.Call, fn *Func, e env) {
	for i, cons := range fn.ParamConstraints {
		if i >= len(call.Args) {
			break
		}
		for _, con := range cons {
			req := fmt.Sprintf("%s requires %s to be %s", fn.Decl.Name, fn.Decl.Params[i].Name, con)
			f.oblige(call.Args[i], con, f.callArgs(call, fn), e, req)
		}
	}
}

func (f *factChecker) recordObligations(lit *syntax.RecordLit, e env) {
	var fields []*Field
	var label string
	switch t := f.info.RecordTargets[lit].(type) {
	case *Record:
		fields, label = t.Fields, t.Name
	case *Variant:
		fields, label = t.Fields, t.Parent.Name+"."+t.Name
	}
	for _, fi := range lit.Fields {
		if fd := findField(fields, fi.Name); fd != nil {
			for _, con := range fd.Constraints {
				f.oblige(fi.Value, con, noParams, e, fmt.Sprintf("%s requires %s to be %s", label, fi.Name, con))
			}
		}
	}
}

func (f *factChecker) copyObligations(cp *syntax.Copy, e env) {
	rec, _ := f.info.Types[cp.X].(*Record)
	for _, u := range cp.Updates {
		cur := rec
		var fd *Field
		for _, name := range u.Path {
			if cur == nil {
				fd = nil
				break
			}
			fd = cur.Field(name)
			if fd == nil {
				break
			}
			cur, _ = fd.Type.(*Record)
		}
		if fd == nil {
			continue
		}
		path := strings.Join(u.Path, ".")
		for _, con := range fd.Constraints {
			f.oblige(u.Value, con, noParams, e, fmt.Sprintf("%s requires %s to be %s", rec.Name, path, con))
		}
	}
}

// checkResult checks a value the current function returns against the
// facts its signature promises.
func (f *factChecker) checkResult(x syntax.Expr, e env) {
	if f.collect != nil {
		*f.collect = append(*f.collect, branch{x: x, e: e})
		return
	}
	t := f.info.Types[x]
	for _, mc := range f.fn.ResultConstraints {
		var member Type
		switch {
		case identical(t, mc.Type):
		case isMemberOf(mc.Type, t):
			member = mc.Type
		default:
			continue // a different member of the result's union
		}
		for _, con := range mc.Constraints {
			req := fmt.Sprintf("%s promises a result that is %s", f.fn.Decl.Name, con)
			ob := f.obligationOf(con, f.ownParams(), req)
			var ok bool
			var pending []Query
			if member != nil {
				ok, pending = f.proveMember(x, member, ob, e, 0)
			} else {
				ok, pending = f.prove(x, ob, e, 0)
			}
			f.settle(x, ob, ok, pending)
		}
	}
}

func isMemberOf(m, t Type) bool {
	u, ok := t.(*Union)
	return ok && containsMember(u, m)
}

// oblige requires con of the value x, with the constraint's parameter
// arguments given by subst.
func (f *factChecker) oblige(x syntax.Expr, con *Constraint, subst func(string) argVal, e env, requirement string) {
	if f.collect != nil {
		return
	}
	ob := f.obligationOf(con, subst, requirement)
	ok, pending := f.prove(x, ob, e, 0)
	f.settle(x, ob, ok, pending)
}

func (f *factChecker) settle(x syntax.Expr, ob obligation, ok bool, pending []Query) {
	if !ok {
		f.diags.Add(x.Position(), "%s, but that is not proven for %s%s", ob.requirement, f.describe(x), f.hint(x, ob))
		return
	}
	for _, q := range pending {
		f.pending = append(f.pending, pendingQuery{query: q, pos: x.Position(), ob: ob})
	}
}

// obligationOf is the obligation to prove con, with the constraint's
// parameter arguments given by subst.
func (f *factChecker) obligationOf(con *Constraint, subst func(string) argVal, requirement string) obligation {
	ob := obligation{requirement: requirement, con: con.String()}
	if con.Or != nil {
		for _, alt := range con.Or {
			ob.or = append(ob.or, f.obligationOf(alt, subst, requirement))
		}
		return ob
	}
	ob.pred, ob.args = con.Pred, f.substitute(con, subst)
	return ob
}

// knownOf is what con says is known, with the constraint's parameter
// arguments given by subst.
func (f *factChecker) knownOf(con *Constraint, subst func(string) argVal) known {
	if con.Or != nil {
		k := known{}
		for _, alt := range con.Or {
			k.or = append(k.or, f.knownOf(alt, subst))
		}
		return k
	}
	return known{pred: con.Pred, args: f.substitute(con, subst)}
}

func (f *factChecker) substitute(con *Constraint, subst func(string) argVal) []argVal {
	args := make([]argVal, len(con.Args))
	for i, a := range con.Args {
		if a.Const != nil {
			args[i] = constArg(a.Const)
		} else {
			args[i] = subst(a.Param)
		}
	}
	return args
}

func constArg(v constant.Value) argVal {
	return argVal{key: constKey(v), value: v, text: CArg{Const: v}.String()}
}

// ownParams substitutes the current function's parameters.
func (f *factChecker) ownParams() func(string) argVal {
	fn := f.fn
	return func(param string) argVal {
		for _, p := range fn.Decl.Params {
			if p.Name == param {
				return f.argOf(f.paramIdent(p))
			}
		}
		return argVal{text: param}
	}
}

// paramIdent is an identifier referring to parameter p, for proving
// facts about a parameter that no expression mentions.
func (f *factChecker) paramIdent(p *syntax.Param) *syntax.Ident {
	if id, ok := f.params[p]; ok {
		return id
	}
	id := &syntax.Ident{Pos: p.Pos, Name: p.Name}
	f.info.Defs[id] = p
	f.params[p] = id
	return id
}

func noParams(param string) argVal { return argVal{text: param} }

func (f *factChecker) argOf(x syntax.Expr) argVal {
	return argVal{key: f.key(x), value: f.info.constantOf(x), text: f.describe(x), expr: x}
}

// callArgs substitutes a callee's parameters by the call's arguments.
func (f *factChecker) callArgs(call *syntax.Call, fn *Func) func(string) argVal {
	return func(param string) argVal {
		for i, p := range fn.Decl.Params {
			if p.Name == param && i < len(call.Args) {
				return f.argOf(call.Args[i])
			}
		}
		return argVal{text: param}
	}
}

// --- Proving ---

const maxDepth = 48

type branch struct {
	x syntax.Expr
	e env
}

// prove tries to prove ob for the value x. It returns false if that is
// not possible; otherwise the proof may still depend on predicates of
// constants, returned as queries to evaluate at compile time.
func (f *factChecker) prove(x syntax.Expr, ob obligation, e env, depth int) (bool, []Query) {
	if depth > maxDepth {
		return false, nil
	}
	if ob.or != nil {
		// One alternative for the whole value, or different ones for
		// different branches (see proveCases).
		ok1, p1 := anyOf(ob, func(alt obligation) (bool, []Query) { return f.prove(x, alt, e, depth+1) })
		if ok1 && len(p1) == 0 {
			return true, nil
		}
		ok2, p2 := f.proveCases(x, ob, e, depth)
		switch {
		case ok1 && ok2 && len(p2) > 0:
			return true, []Query{{Or: []Query{allOf(p1), allOf(p2)}}}
		case ok2:
			return true, p2
		}
		return ok1, p1
	}
	// A constant: run the predicate at compile time.
	if v := f.info.constantOf(x); v != nil {
		if q, ok := constQuery(ob, v); ok {
			return true, []Query{q}
		}
	}
	// Known from a guard, a declaration, or a promise.
	for _, k := range f.declared(x, e, depth) {
		if k.proves(ob) {
			return true, nil
		}
	}
	return f.proveCases(x, ob, e, depth)
}

// proveCases proves ob for x from how x is computed: what it is bound
// to, its branches, the callee's body, or by rules or case splits.
func (f *factChecker) proveCases(x syntax.Expr, ob obligation, e env, depth int) (bool, []Query) {
	switch x := x.(type) {
	case *syntax.Ident:
		switch d := f.info.Defs[x].(type) {
		case *syntax.Binding:
			if ok, pending := f.prove(d.Value, ob, e, depth+1); ok {
				return true, pending
			}
		case *syntax.Param, nil:
		default:
			if src := f.info.PatSources[d]; src != nil {
				var ok bool
				var pending []Query
				if src.Member == nil {
					ok, pending = f.prove(src.Subject, ob, e, depth+1)
				} else {
					ok, pending = f.proveMember(src.Subject, src.Member, ob, e, depth+1)
				}
				if ok {
					return true, pending
				}
			}
		}
	case *syntax.Call:
		if fn := f.info.CallFuncs[x]; fn != nil {
			if ok, pending := f.derive(x, fn, nil, ob, e, depth); ok {
				return true, pending
			}
		}
	case *syntax.Try:
		if info := f.info.Tries[x]; info != nil && info.Option == nil {
			if ok, pending := f.proveMember(x.X, info.Kept, ob, e, depth+1); ok {
				return true, pending
			}
		}
	case *syntax.Block:
		if x.Tail != nil {
			if ok, pending := f.prove(x.Tail, ob, f.stmts(x.Stmts, e), depth+1); ok {
				return true, pending
			}
		}
	case *syntax.If:
		if x.Else != nil {
			if ok, pending := f.all(ob, depth,
				branch{x.Then, e.with(f.conditionFacts(x.Cond, true)...)},
				branch{x.Else, e.with(f.conditionFacts(x.Cond, false)...)}); ok {
				return true, pending
			}
		}
	case *syntax.Match:
		var bs []branch
		for _, arm := range x.Arms {
			bs = append(bs, branch{arm.Body, e})
		}
		if ok, pending := f.all(ob, depth, bs...); ok {
			return true, pending
		}
	}
	if ob.or == nil {
		if ok, pending := f.byRules(f.argOf(x), ob, e, depth); ok {
			return true, pending
		}
	}
	return f.split(x, ob, e, depth)
}

// anyOf proves one of ob's alternatives with try. An alternative proven
// without queries settles it; otherwise the proof needs any of the
// alternatives' queries to hold.
func anyOf(ob obligation, try func(obligation) (bool, []Query)) (bool, []Query) {
	var alts []Query
	for _, alt := range ob.or {
		ok, pending := try(alt)
		if ok && len(pending) == 0 {
			return true, nil
		}
		if ok {
			alts = append(alts, allOf(pending))
		}
	}
	if alts == nil {
		return false, nil
	}
	if len(alts) == 1 {
		return true, alts
	}
	return true, []Query{{Or: alts}}
}

func allOf(qs []Query) Query {
	if len(qs) == 1 {
		return qs[0]
	}
	return Query{And: qs}
}

// split proves ob for x by cases: if one of several facts is known to
// hold (from `a || b`, or `where p or q`), proving ob assuming each of
// them in turn proves it.
func (f *factChecker) split(x syntax.Expr, ob obligation, e env, depth int) (bool, []Query) {
	var cases []fact
	for _, ft := range e.facts {
		if ft.or != nil {
			cases = append(cases, ft)
		}
	}
	if k := f.key(x); k != "" {
		for _, kn := range f.declared(x, e, depth) {
			if kn.or != nil {
				cases = append(cases, f.factOf(k, kn))
			}
		}
	}
	for _, c := range cases {
		id := "split " + factKey(c)
		if f.active[id] {
			continue
		}
		f.active[id] = true
		var pending []Query
		ok := true
		for _, alt := range c.or {
			proven, p := f.prove(x, ob, e.with(alt...), depth+1)
			if !proven {
				ok = false
				break
			}
			pending = append(pending, p...)
		}
		delete(f.active, id)
		if ok {
			return true, pending
		}
	}
	return false, nil
}

// factOf is the fact that k is known about the value with key subject.
func (f *factChecker) factOf(subject string, k known) fact {
	if k.or == nil {
		return fact{pred: k.pred, subject: subject, args: k.args}
	}
	ft := fact{}
	for _, alt := range k.or {
		ft.or = append(ft.or, []fact{f.factOf(subject, alt)})
	}
	return ft
}

// constQuery is ob on the constant v, if all its arguments are constants.
func constQuery(ob obligation, v constant.Value) (Query, bool) {
	q := Query{Pred: ob.pred, Args: []constant.Value{v}}
	for _, a := range ob.args {
		if a.value == nil {
			return Query{}, false
		}
		q.Args = append(q.Args, a.value)
	}
	return q, true
}

// all proves ob for every branch that produces a value.
func (f *factChecker) all(ob obligation, depth int, bs ...branch) (bool, []Query) {
	var pending []Query
	for _, b := range bs {
		if f.info.Types[b.x] == Never {
			continue
		}
		ok, p := f.prove(b.x, ob, b.e, depth+1)
		if !ok {
			return false, nil
		}
		pending = append(pending, p...)
	}
	return true, pending
}

// proveMember proves ob for the values of member type m that x (of a
// union type) can produce.
func (f *factChecker) proveMember(x syntax.Expr, m Type, ob obligation, e env, depth int) (bool, []Query) {
	if depth > maxDepth {
		return false, nil
	}
	if ob.or != nil {
		if ok, pending := anyOf(ob, func(alt obligation) (bool, []Query) { return f.proveMember(x, m, alt, e, depth+1) }); ok {
			return true, pending
		}
	}
	for _, k := range f.declaredMember(x, m) {
		if k.proves(ob) {
			return true, nil
		}
	}
	switch x := x.(type) {
	case *syntax.Call:
		if fn := f.info.CallFuncs[x]; fn != nil {
			return f.derive(x, fn, m, ob, e, depth)
		}
	case *syntax.Ident:
		if d, ok := f.info.Defs[x].(*syntax.Binding); ok {
			return f.proveMember(d.Value, m, ob, e, depth+1)
		}
	}
	return false, nil
}

// declared lists what is known about the value x without looking into
// how it was computed: facts from guards and trust, and the facts that
// declarations and promises give it.
func (f *factChecker) declared(x syntax.Expr, e env, depth int) []known {
	var out []known
	if k := f.key(x); k != "" {
		for _, ft := range e.facts {
			if ft.pred != nil && ft.subject == k {
				out = append(out, known{pred: ft.pred, args: ft.args})
			}
		}
	}
	add := func(cons []*Constraint, subst func(string) argVal) {
		for _, con := range cons {
			out = append(out, f.knownOf(con, subst))
		}
	}
	switch x := x.(type) {
	case *syntax.Ident:
		switch d := f.info.Defs[x].(type) {
		case *syntax.Param:
			for i, p := range f.fn.Decl.Params {
				if p == d {
					add(f.fn.ParamConstraints[i], f.ownParams())
				}
			}
		case *syntax.Binding:
			add(f.info.BindingConstraints[d], f.ownParams())
			if depth < maxDepth {
				out = append(out, f.declared(d.Value, e, depth+1)...)
			}
		case nil:
		default:
			if src := f.info.PatSources[d]; src != nil && depth < maxDepth {
				if src.Member == nil {
					out = append(out, f.declared(src.Subject, e, depth+1)...)
				} else {
					out = append(out, f.declaredMember(src.Subject, src.Member)...)
				}
			}
		}
	case *syntax.Call:
		if fn := f.info.CallFuncs[x]; fn != nil {
			for _, mc := range fn.ResultConstraints {
				if identical(mc.Type, fn.Result) {
					add(mc.Constraints, f.callArgs(x, fn))
				}
			}
		}
	case *syntax.Try:
		if info := f.info.Tries[x]; info != nil && info.Option == nil {
			out = append(out, f.declaredMember(x.X, info.Kept)...)
		}
	case *syntax.Selector:
		if rec, ok := f.info.Types[x.X].(*Record); ok {
			if fd := rec.Field(x.Name); fd != nil {
				add(fd.Constraints, noParams)
			}
		}
	}
	return out
}

// declaredMember lists what a function promises about the member m of
// the union x produces.
func (f *factChecker) declaredMember(x syntax.Expr, m Type) []known {
	var out []known
	switch x := x.(type) {
	case *syntax.Call:
		if fn := f.info.CallFuncs[x]; fn != nil {
			for _, mc := range fn.ResultConstraints {
				if identical(mc.Type, m) {
					for _, con := range mc.Constraints {
						out = append(out, f.knownOf(con, f.callArgs(x, fn)))
					}
				}
			}
		}
	case *syntax.Ident:
		if d, ok := f.info.Defs[x].(*syntax.Binding); ok {
			out = append(out, f.declaredMember(d.Value, m)...)
		}
	}
	return out
}

// derive proves ob for the result of a call (or, with member, for that
// member of its union result) from the callee's body: every value it
// returns must satisfy ob. This is how helpers pass on facts without
// declaring them. A value the callee returns that is one of its
// parameters is proven at the call site, for the argument.
func (f *factChecker) derive(call *syntax.Call, fn *Func, member Type, ob obligation, e env, depth int) (bool, []Query) {
	activeKey := fmt.Sprintf("derive %p", fn)
	if fn.Decl.Body == nil || f.active[activeKey] || depth > maxDepth {
		return false, nil
	}
	f.active[activeKey] = true
	defer delete(f.active, activeKey)
	// The obligation, in terms of the callee's parameters.
	inner, ok := f.calleeObligation(call, fn, ob)
	if !ok {
		return false, nil
	}
	var pending []Query
	for _, path := range f.resultPaths(fn) {
		t := f.info.Types[path.x]
		if member != nil && !identical(t, member) && !isMemberOf(member, t) {
			continue
		}
		saveFn := f.fn
		f.fn = fn
		var ok bool
		var p []Query
		if member != nil && !identical(t, member) {
			ok, p = f.proveMember(path.x, member, inner, path.e, depth+1)
		} else {
			ok, p = f.prove(path.x, inner, path.e, depth+1)
		}
		f.fn = saveFn
		if !ok {
			// Returning a parameter: prove it for the argument.
			if id, isID := path.x.(*syntax.Ident); isID {
				if param, isParam := f.info.Defs[id].(*syntax.Param); isParam {
					for j, pp := range fn.Decl.Params {
						if pp == param && j < len(call.Args) {
							ok, p = f.prove(call.Args[j], ob, e, depth+1)
						}
					}
				}
			}
		}
		if !ok {
			return false, nil
		}
		for _, q := range p {
			if q.Via == "" {
				q.Via = fn.Decl.Name
			}
			pending = append(pending, q)
		}
	}
	return true, pending
}

// calleeObligation translates ob's arguments from a call's arguments to
// the callee's parameters. It fails if an argument is neither a
// constant nor one of the call's arguments.
func (f *factChecker) calleeObligation(call *syntax.Call, fn *Func, ob obligation) (obligation, bool) {
	inner := ob
	if ob.or != nil {
		inner.or = make([]obligation, len(ob.or))
		for i, alt := range ob.or {
			var ok bool
			if inner.or[i], ok = f.calleeObligation(call, fn, alt); !ok {
				return inner, false
			}
		}
		return inner, true
	}
	inner.args = make([]argVal, len(ob.args))
	for i, a := range ob.args {
		if a.value != nil {
			inner.args[i] = a
			continue
		}
		found := false
		for j, arg := range call.Args {
			if a.key != "" && f.key(arg) == a.key {
				inner.args[i] = argVal{key: "p:" + fn.Decl.Params[j].Name, text: fn.Decl.Params[j].Name, expr: f.paramIdent(fn.Decl.Params[j])}
				found = true
				break
			}
		}
		if !found {
			return inner, false
		}
	}
	return inner, true
}

// byRules proves ob for the value v with an inference rule whose
// conclusion matches it, by proving the rule's premises.
func (f *factChecker) byRules(v argVal, ob obligation, e env, depth int) (bool, []Query) {
	if depth > maxDepth {
		return false, nil
	}
	for _, r := range f.info.Rules {
		for _, c := range r.Conclusions {
			if c.Pred != ob.pred || len(c.Args) != len(ob.args)+1 {
				continue
			}
			bound := map[string]argVal{c.Args[0].Var: v}
			ok := true
			for i, a := range c.Args[1:] {
				ok = ok && bindArg(bound, a, ob.args[i])
			}
			if !ok {
				continue
			}
			goal := fmt.Sprintf("rule %s %s %s", r.Decl.Name, v.key, ob.pred.Decl.Name)
			for _, a := range ob.args {
				goal += " " + a.key
			}
			if v.key == "" || f.active[goal] {
				continue
			}
			f.active[goal] = true
			proven, pending := f.premises(r, 0, bound, e, depth+1)
			delete(f.active, goal)
			if proven {
				return true, pending
			}
		}
	}
	return false, nil
}

// bindArg binds a rule argument to a value, or checks that it matches.
func bindArg(bound map[string]argVal, a RuleArg, v argVal) bool {
	if a.Const != nil {
		return v.key == constKey(a.Const)
	}
	if prev, ok := bound[a.Var]; ok {
		return prev.key != "" && prev.key == v.key
	}
	bound[a.Var] = v
	return true
}

// premises proves the rule's premises from index i on, binding the
// variables that only premises mention to the facts that match them.
func (f *factChecker) premises(r *Rule, i int, bound map[string]argVal, e env, depth int) (bool, []Query) {
	if i == len(r.Premises) {
		vars := map[string]constant.Value{}
		for name, v := range bound {
			vars[name] = v.value
		}
		for _, cond := range r.Conditions {
			v := evalCondition(cond, f.info, vars)
			if v == nil || v.Kind() != constant.Bool || !constant.BoolVal(v) {
				return false, nil
			}
		}
		return true, nil
	}
	p := r.Premises[i]
	subject, ok := bound[p.Args[0].Var]
	if !ok {
		return false, nil
	}
	free := false
	args := make([]argVal, len(p.Args)-1)
	for j, a := range p.Args[1:] {
		switch {
		case a.Const != nil:
			args[j] = constArg(a.Const)
		default:
			v, isBound := bound[a.Var]
			if !isBound {
				free = true
			}
			args[j] = v
		}
	}
	if !free {
		ok, pending := f.proveArg(subject, obligation{pred: p.Pred, args: args}, e, depth)
		if !ok {
			return false, nil
		}
		rest, more := f.premises(r, i+1, bound, e, depth)
		return rest, append(pending, more...)
	}
	// Bind the free variables to a fact about the subject.
	var candidates []known
	if subject.expr != nil {
		candidates = f.declared(subject.expr, e, depth)
	}
	for _, k := range candidates {
		if k.pred != p.Pred {
			continue
		}
		next := map[string]argVal{}
		for name, v := range bound {
			next[name] = v
		}
		matches := true
		for j, a := range p.Args[1:] {
			matches = matches && bindArg(next, a, k.args[j])
		}
		if !matches {
			continue
		}
		if ok, pending := f.premises(r, i+1, next, e, depth); ok {
			return true, pending
		}
	}
	return false, nil
}

// proveArg proves ob for a value that may only be known by key.
func (f *factChecker) proveArg(v argVal, ob obligation, e env, depth int) (bool, []Query) {
	if v.expr != nil {
		return f.prove(v.expr, ob, e, depth)
	}
	if v.value != nil {
		if q, ok := constQuery(ob, v.value); ok {
			return true, []Query{q}
		}
	}
	if v.key != "" {
		for _, ft := range e.facts {
			if ft.subject == v.key && (known{pred: ft.pred, args: ft.args}).proves(ob) {
				return true, nil
			}
		}
	}
	return false, nil
}

// alternatives lists the alternatives of facts that hold together,
// flattening `(a || b) || c`.
func alternatives(fs []fact) [][]fact {
	if len(fs) == 1 && fs[0].or != nil {
		return fs[0].or
	}
	return [][]fact{fs}
}

// conditionFacts lists the facts a condition establishes when it is
// true (or, with positive false, when it is false).
func (f *factChecker) conditionFacts(cond syntax.Expr, positive bool) []fact {
	switch c := cond.(type) {
	case *syntax.Unary:
		if c.Op == syntax.Not {
			return f.conditionFacts(c.X, !positive)
		}
	case *syntax.Binary:
		switch {
		case c.Op == syntax.AndAnd && positive, c.Op == syntax.OrOr && !positive:
			return append(f.conditionFacts(c.X, positive), f.conditionFacts(c.Y, positive)...)
		case c.Op == syntax.OrOr && positive:
			// One side holds: only useful if both sides say something.
			l, r := f.conditionFacts(c.X, true), f.conditionFacts(c.Y, true)
			if len(l) == 0 || len(r) == 0 {
				return nil
			}
			return []fact{{or: append(alternatives(l), alternatives(r)...)}}
		}
	case *syntax.Call:
		fn := f.info.CallFuncs[c]
		if !positive || fn == nil || !fn.Decl.IsPred || len(c.Args) == 0 {
			return nil
		}
		subject := f.key(c.Args[0])
		if subject == "" {
			return nil
		}
		ft := fact{pred: fn, subject: subject}
		for _, a := range c.Args[1:] {
			ft.args = append(ft.args, f.argOf(a))
		}
		return []fact{ft}
	}
	return nil
}

// --- Identifying values ---

// key identifies the value of x, so facts about it can be found again:
// a parameter, a binding, a field path from one of those, or a constant.
// Bindings to another value share its key. Values that cannot be
// identified (calls, arithmetic) have no key.
func (f *factChecker) key(x syntax.Expr) string {
	if v := f.info.constantOf(x); v != nil {
		return constKey(v)
	}
	switch x := x.(type) {
	case *syntax.Ident:
		switch d := f.info.Defs[x].(type) {
		case *syntax.Param:
			return "p:" + d.Name
		case *syntax.Binding:
			if k := f.aliasKey(d.Value); k != "" {
				return k
			}
			return fmt.Sprintf("b:%p", d)
		case nil:
			return ""
		default:
			if src := f.info.PatSources[d]; src != nil && src.Member == nil {
				if k := f.aliasKey(src.Subject); k != "" {
					return k
				}
			}
			return fmt.Sprintf("n:%p", d)
		}
	case *syntax.Selector:
		if f.info.SelectorVariants[x] != nil {
			return ""
		}
		if k := f.key(x.X); k != "" {
			return k + "." + x.Name
		}
	}
	return ""
}

// aliasKey is the key of x if binding x just gives a value another name.
func (f *factChecker) aliasKey(x syntax.Expr) string {
	switch x.(type) {
	case *syntax.Ident, *syntax.Selector:
		return f.key(x)
	}
	return ""
}

func constKey(v constant.Value) string { return "c:" + v.ExactString() }

// constantOf is the value of a constant expression, or nil.
func (info *Info) constantOf(x syntax.Expr) constant.Value {
	if v, ok := info.Consts[x]; ok {
		return v
	}
	switch x := x.(type) {
	case *syntax.StringLit:
		return constant.MakeString(x.Value)
	case *syntax.BoolLit:
		return constant.MakeBool(x.Value)
	}
	return nil
}

// --- Messages ---

// describe shows a value in a message: its name, field path, or
// constant, or "this value".
func (f *factChecker) describe(x syntax.Expr) string {
	if v := f.info.constantOf(x); v != nil {
		return CArg{Const: v}.String()
	}
	switch x := x.(type) {
	case *syntax.Ident:
		return x.Name
	case *syntax.Selector:
		if inner := f.describe(x.X); inner != "this value" {
			return inner + "." + x.Name
		}
	}
	return "this value"
}

// hint suggests how to establish a missing fact.
func (f *factChecker) hint(x syntax.Expr, ob obligation) string {
	name := f.describe(x)
	if name == "this value" {
		return " (give it a name and check it first)"
	}
	check := fmt.Sprintf("if (%s) { ... }", checkText(name, ob))
	if id, ok := x.(*syntax.Ident); ok {
		if p, isParam := f.info.Defs[id].(*syntax.Param); isParam {
			for i, pp := range f.fn.Decl.Params {
				if pp == p {
					return fmt.Sprintf(" (check it first with %s, or require it: %s: %s where %s)", check, p.Name, f.fn.Params[i], ob.con)
				}
			}
		}
	}
	return fmt.Sprintf(" (check it first with %s)", check)
}

// checkText is the condition that checks ob for the value called name.
func checkText(name string, ob obligation) string {
	if ob.or != nil {
		alts := make([]string, len(ob.or))
		for i, alt := range ob.or {
			alts[i] = checkText(name, alt)
		}
		return strings.Join(alts, " || ")
	}
	args := []string{name}
	for _, a := range ob.args {
		args = append(args, a.text)
	}
	return fmt.Sprintf("%s(%s)", ob.pred.Decl.Name, strings.Join(args, ", "))
}

// --- Compile-time evaluation ---

func (f *factChecker) evaluate(eval Evaluator) {
	if len(f.pending) == 0 {
		return
	}
	var queries []Query
	index := map[string]int{}
	for _, p := range f.pending {
		k := p.query.String()
		if _, ok := index[k]; !ok {
			index[k] = len(queries)
			queries = append(queries, p.query)
		}
	}
	if eval == nil {
		return
	}
	results, err := eval(queries)
	if err != nil {
		f.diags.Add(f.pending[0].pos, "cannot run predicates at compile time: %v", err)
		return
	}
	for _, p := range f.pending {
		if !results[index[p.query.String()]] {
			if p.query.Via != "" && p.query.Pred != nil {
				f.diags.Add(p.pos, "%s, but %s can return %s, and %s is false", p.ob.requirement, p.query.Via, CArg{Const: p.query.Args[0]}, p.query)
				continue
			}
			if p.query.Via != "" {
				f.diags.Add(p.pos, "%s, but for a value %s can return, %s is false", p.ob.requirement, p.query.Via, p.query)
				continue
			}
			f.diags.Add(p.pos, "%s, but %s is false", p.ob.requirement, p.query)
		}
	}
}
