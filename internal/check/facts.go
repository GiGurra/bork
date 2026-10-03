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
	// Subject, when set, is the constrained value as an expression of
	// constants (a list or record literal), shown as SubjectText; Args
	// then holds only the other arguments.
	Subject     Expr
	SubjectText string
	// TypeArgs are a generic predicate's type arguments, and Params its
	// parameter types with them filled in.
	TypeArgs []Type
	Params   []Type
	Or       []Query
	And      []Query
	// Via names the function whose result the constant is, when the
	// query comes from a derived result (see derive).
	Via string
}

func (q Query) String() string { return q.Text(nil) }

// Text renders the query as code in package from would write it.
func (q Query) Text(from *Package) string {
	join := func(parts []Query, sep string) string {
		out := make([]string, len(parts))
		for i, p := range parts {
			out[i] = p.Text(from)
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
	var args []string
	if q.Subject != nil {
		args = append(args, q.SubjectText)
	}
	for _, a := range q.Args {
		args = append(args, CArg{Const: a}.String())
	}
	return q.Pred.QualifiedName(from) + "(" + strings.Join(args, ", ") + ")"
}

// Evaluator runs predicates on constants at compile time, returning one
// result per query.
type Evaluator func(queries []Query) ([]bool, error)

// Facts checks the where clauses of a type-checked package.
func Facts(files []*syntax.File, info *Info, diags *diag.List, eval Evaluator) {
	f := &factChecker{info: info, diags: diags, paths: map[*Func][]branch{}, active: map[string]bool{}, params: map[*Var]*VarRef{}, predParams: map[*Var]*Func{}, lambdaArgs: map[*Var]lambdaArg{}}
	for _, file := range files {
		if file.Prelude {
			continue
		}
		for _, fd := range file.Funcs {
			if fn := info.FuncOf[fd]; fn != nil && fn.Body != nil {
				f.function(fn)
			}
		}
	}
	for _, fn := range info.Tests {
		f.function(fn)
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
	params map[*Var]*VarRef
	// predParams holds the predicates standing for function parameters.
	predParams map[*Var]*Func
	// lambdaArgs records, for the parameters of lambdas passed to
	// declared functions, which call and parameter they belong to.
	lambdaArgs map[*Var]lambdaArg
	// observe captures the proof context for queries keyed by source position.
	observe func(diag.Pos, env)
}

// lambdaArg places a lambda's parameter: the lambda is argument arg of
// call, and the parameter is its param'th.
type lambdaArg struct {
	call       *Call
	arg, param int
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
	expr  Expr
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
// A known fact with alternatives has a nil pred and them in or. A fact
// about part of the value (its elements) has the path to it.
type known struct {
	pred *Func
	args []argVal
	or   []known
	path string
}

func (k known) proves(ob obligation) bool {
	return k.pred != nil && k.pred == ob.pred && k.path == ob.path && sameArgs(k.args, ob.args)
}

// within keeps the facts about the part of the value at path, relative
// to that part.
func within(ks []known, path string) []known {
	if path == "" {
		return ks
	}
	var out []known
	for _, k := range ks {
		if rest, ok := strings.CutPrefix(k.path, path); ok && (rest == "" || rest[0] == '.') {
			k.path = rest
			out = append(out, k)
		}
	}
	return out
}

// obligation is a requirement pred(subject, args...) at some point.
//
// An obligation with alternatives (from `where p or q`) has a nil pred
// and them in or; proving any of them proves it.
//
// An obligation about part of the value (every element of a list) has
// the path to it. One whose predicate could not be determined (a
// predicate parameter given a complicated lambda) has a nil pred and no
// alternatives, and cannot be proven.
type obligation struct {
	pred *Func
	args []argVal
	or   []obligation
	path string
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
	from  *Package // the package whose code needs it
}

func (f *factChecker) function(fn *Func) {
	f.fn = fn
	f.tail(fn.Body, env{}, f.checkResult)
	f.fn = nil
}

// --- Walking function bodies ---

// tail walks an expression whose value is used by result, passing each
// branch's value (with the facts known in that branch) to result.
func (f *factChecker) tail(x Expr, e env, result func(Expr, env)) {
	if f.observe != nil && f.collect == nil {
		f.observe(x.Pos(), e)
	}
	switch x := x.(type) {
	case *Block:
		e = f.stmts(x.Stmts, e)
		if x.Tail != nil {
			f.tail(x.Tail, e, result)
		}
	case *ScopeBlock:
		for _, p := range x.Policies {
			f.walk(p, e)
		}
		f.tail(x.Body, e, result)
	case *If:
		if x.Else == nil {
			f.walk(x, e)
			return
		}
		f.walk(x.Cond, e)
		f.tail(x.Then, e.with(f.conditionFacts(x.Cond, true)...), result)
		f.tail(x.Else, e.with(f.conditionFacts(x.Cond, false)...), result)
	case *Match:
		f.walk(x.X, e)
		for _, arm := range x.Arms {
			f.tail(arm.Body, f.walkPatternGuards(arm.Pat, e), result)
		}
	default:
		f.walk(x, e)
		if x.Type() != Never {
			result(x, e)
		}
	}
}

// stmts walks a block's statements and returns the facts known after
// them.
func (f *factChecker) stmts(list []Stmt, e env) env {
	for _, s := range list {
		switch s := s.(type) {
		case *Let:
			f.walk(s.Value, e)
			for _, con := range s.Constraints {
				f.oblige(s.Value, con, f.ownParams(), e, fmt.Sprintf("%s must be %s", pathPhrase(con.Path, s.Var.Name), con))
			}
		case *ExprStmt:
			f.walk(s.X, e)
			// A guard: `if (!p(x)) { return ... }` leaves p(x) known.
			if ifx, ok := s.X.(*If); ok {
				if ifx.Then.Type() == Never {
					e = e.with(f.conditionFacts(ifx.Cond, false)...)
				}
				if ifx.Else != nil && ifx.Else.Type() == Never {
					e = e.with(f.conditionFacts(ifx.Cond, true)...)
				}
			}
		case *Trust:
			f.walk(s.Call, e)
			facts := f.conditionFacts(s.Call, true)
			if len(facts) == 0 && f.collect == nil {
				f.diags.AddCode(trustSubjectPos(s), "facts.error", "trust needs a value with a name (bind it first: x = ...), or the fact could not be used")
			}
			e = e.with(facts...)
		}
	}
	return e
}

// trustSubjectPos is where the value a trust statement is about starts.
func trustSubjectPos(s *Trust) diag.Pos {
	switch c := s.Call.(type) {
	case *Call:
		if len(c.Args) > 0 {
			return c.Args[0].Pos()
		}
	case *CallValue:
		if len(c.Args) > 0 {
			return c.Args[0].Pos()
		}
	}
	return s.Call.Pos()
}

// walk visits an expression, checking the obligations inside it.
func (f *factChecker) walk(x Expr, e env) {
	if f.observe != nil && f.collect == nil {
		f.observe(x.Pos(), e)
	}
	switch x := x.(type) {
	case *Call:
		for i, a := range x.Args {
			if l, ok := a.(*Lambda); ok {
				for k, p := range l.Params {
					f.lambdaArgs[p] = lambdaArg{call: x, arg: i, param: k}
				}
			}
		}
		for _, a := range x.Args {
			f.walk(a, e)
		}
		f.callObligations(x, e)
	case *CallBuiltin:
		for _, a := range x.Args {
			f.walk(a, e)
		}
	case *CallValue:
		f.walk(x.Fun, e)
		for _, a := range x.Args {
			f.walk(a, e)
		}
	case *FuncRef:
		// A function with requirements cannot be a value: calls through
		// the value could not be checked.
		if f.collect == nil {
			for i, cons := range x.Inst.Func.ParamConstraints {
				if len(cons) > 0 {
					f.diags.AddCode(x.Pos(), "facts.error", "%s requires %s to be %s, so it cannot be used as a value; use a lambda that checks it: x => if (...) { %s(x) } else { ... }", x.Name, x.Inst.Func.Decl.Params[i].Name, cons[0], x.Name)
					break
				}
			}
		}
	case *Lambda:
		// Facts known here still hold inside: values never change.
		f.walk(x.Body, e)
	case *ListLit:
		for _, el := range x.Elems {
			f.walk(el, e)
		}
	case *MapLit:
		for i := range x.Keys {
			f.walk(x.Keys[i], e)
			f.walk(x.Values[i], e)
		}
	case *Unary:
		f.walk(x.X, e)
	case *Binary:
		f.walk(x.X, e)
		switch x.Op {
		case syntax.AndAnd:
			f.walk(x.Y, e.with(f.conditionFacts(x.X, true)...))
		case syntax.OrOr:
			f.walk(x.Y, e.with(f.conditionFacts(x.X, false)...))
		default:
			f.walk(x.Y, e)
		}
	case *If:
		f.walk(x.Cond, e)
		f.walk(x.Then, e.with(f.conditionFacts(x.Cond, true)...))
		if x.Else != nil {
			f.walk(x.Else, e.with(f.conditionFacts(x.Cond, false)...))
		}
	case *Block:
		e = f.stmts(x.Stmts, e)
		if x.Tail != nil {
			f.walk(x.Tail, e)
		}
	case *ScopeBlock:
		for _, p := range x.Policies {
			f.walk(p, e)
		}
		f.walk(x.Body, e)
	case *Return:
		if x.Value != nil {
			f.tail(x.Value, e, f.checkResult)
		}
	case *Select:
		f.walk(x.X, e)
	case *RecordLit:
		for _, fi := range x.Fields {
			f.walk(fi.Value, e)
		}
		f.recordObligations(x, e)
	case *Copy:
		f.walk(x.X, e)
		for _, u := range x.Updates {
			f.walk(u.Value, e)
		}
		f.copyObligations(x, e)
	case *Match:
		f.walk(x.X, e)
		for _, arm := range x.Arms {
			f.walk(arm.Body, f.walkPatternGuards(arm.Pat, e))
		}
	case *Try:
		f.walk(x.X, e)
	case *Interp:
		for _, ix := range x.Exprs {
			f.walk(ix, e)
		}
	}
}

// walkPatternGuards checks each predicate before making its successful
// facts available to the next guard and the arm body.
func (f *factChecker) walkPatternGuards(p *Pat, e env) env {
	for _, guard := range p.Guards() {
		f.walk(guard, e)
		e = e.with(f.conditionFacts(guard, true)...)
	}
	return e
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
	f.tail(fn.Body, env{}, f.checkResult)
	f.fn, f.collect = saveFn, saveCollect
	f.paths[fn] = paths
	return paths
}

// --- Where obligations arise ---

// pathPhrase describes the part of the value called name that path
// leads to: "every element of xs", "the value in note".
func pathPhrase(path, name string) string {
	if path == "" {
		return name
	}
	for _, step := range strings.Split(path[1:], ".") {
		switch step {
		case "[]":
			name = "every element of " + name
		case "value":
			name = "the value in " + name
		default:
			name = "the " + step + " of " + name
		}
	}
	return name
}

func (f *factChecker) callObligations(call *Call, e env) {
	fn, args := call.Func, call.Args
	for i, cons := range fn.ParamConstraints {
		if i >= len(args) {
			break
		}
		for _, con := range cons {
			req := fmt.Sprintf("%s requires %s to be %s", fn.QualifiedName(f.from()), pathPhrase(con.Path, fn.Decl.Params[i].Name), con.Text(f.from()))
			f.oblige(args[i], con, f.callArgs(call), e, req)
		}
	}
	// With a constrained type argument (f[Port](x)), arguments of that
	// type must satisfy its constraints.
	inst := call.Inst
	if inst == nil || inst.ArgFacts == nil {
		return
	}
	for i, p := range fn.Params {
		for j, tp := range fn.TypeParams {
			if p != Type(tp) || i >= len(args) {
				continue
			}
			for _, con := range inst.ArgFacts[j] {
				arg := TypeText(inst.TypeArgs[j], f.from())
				if j < len(call.TypeArgNames) && call.TypeArgNames[j] != "" {
					arg = call.TypeArgNames[j] // as written: Port, not Int
				}
				req := fmt.Sprintf("%s[%s] requires %s to be %s", fn.QualifiedName(f.from()), arg, fn.Decl.Params[i].Name, con.Text(f.from()))
				f.oblige(args[i], con, noParams, e, req)
			}
		}
	}
}

// argFactsFor lists what a call with constrained type arguments
// promises of its result's member m (or its whole result): the
// constraints of the type argument that member stands for.
func (f *factChecker) argFactsFor(call *Call, m Type) []*Constraint {
	fn, inst := call.Func, call.Inst
	if inst == nil || inst.ArgFacts == nil {
		return nil
	}
	members := []Type{fn.Result}
	if u, ok := fn.Result.(*Union); ok {
		members = u.Members
	}
	var out []*Constraint
	for j, tp := range fn.TypeParams {
		for _, mt := range members {
			if mt == Type(tp) && (m == nil || identical(inst.TypeArgs[j], m)) {
				out = append(out, inst.ArgFacts[j]...)
			}
		}
	}
	return out
}

func (f *factChecker) recordObligations(lit *RecordLit, e env) {
	label := ""
	if t := lit.Record; t != nil {
		label = qualify(t.Name, t.Pkg, f.from())
	} else if t := lit.Variant; t != nil {
		label = qualify(t.Parent.Name, t.Parent.Pkg, f.from()) + "." + t.Name
	}
	for _, fi := range lit.Fields {
		if fd := fi.Field; fd != nil {
			for _, con := range fd.Constraints {
				f.oblige(fi.Value, con, noParams, e, fmt.Sprintf("%s requires %s to be %s", label, pathPhrase(con.Path, fi.Name), con.Text(f.from())))
			}
		}
	}
}

func (f *factChecker) copyObligations(cp *Copy, e env) {
	rec, _ := cp.X.Type().(*Record)
	for _, u := range cp.Updates {
		fd := u.Field
		if fd == nil {
			continue
		}
		path := strings.Join(u.Path, ".")
		for _, con := range fd.Constraints {
			f.oblige(u.Value, con, noParams, e, fmt.Sprintf("%s requires %s to be %s", qualify(rec.Name, rec.Pkg, f.from()), pathPhrase(con.Path, path), con.Text(f.from())))
		}
	}
}

// checkResult checks a value the current function returns against the
// facts its signature promises.
func (f *factChecker) checkResult(x Expr, e env) {
	if f.collect != nil {
		*f.collect = append(*f.collect, branch{x: x, e: e})
		return
	}
	t := x.Type()
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
			if con.Path != "" {
				req = fmt.Sprintf("%s promises that %s is %s", f.fn.Decl.Name, pathPhrase(con.Path, "its result"), con)
			}
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
func (f *factChecker) oblige(x Expr, con *Constraint, subst func(string) argVal, e env, requirement string) {
	if f.collect != nil {
		return
	}
	ob := f.obligationOf(con, subst, requirement)
	ok, pending := f.prove(x, ob, e, 0)
	f.settle(x, ob, ok, pending)
}

func (f *factChecker) settle(x Expr, ob obligation, ok bool, pending []Query) {
	if !ok {
		f.diags.AddCode(x.Pos(), "facts.error", "%s, but that is not proven for %s%s", ob.requirement, f.describe(x), f.hint(x, ob))
		return
	}
	for _, q := range pending {
		f.pending = append(f.pending, pendingQuery{query: q, pos: x.Pos(), ob: ob, from: f.from()})
	}
}

// obligationOf is the obligation to prove con, with the constraint's
// parameter arguments given by subst.
func (f *factChecker) obligationOf(con *Constraint, subst func(string) argVal, requirement string) obligation {
	ob := obligation{requirement: requirement, con: con.Text(f.from()), path: con.Path}
	if con.Or != nil {
		for _, alt := range con.Or {
			a := f.obligationOf(alt, subst, requirement)
			a.path = con.Path
			ob.or = append(ob.or, a)
		}
		return ob
	}
	if con.PredParam != "" {
		if ks := f.predsOf(subst(con.PredParam)); len(ks) == 1 {
			ob.pred, ob.args = ks[0].pred, ks[0].args
		}
		return ob
	}
	ob.pred, ob.args = con.Pred, f.substitute(con, subst)
	return ob
}

// knownOf is what con says is known, with the constraint's parameter
// arguments given by subst.
func (f *factChecker) knownOf(con *Constraint, subst func(string) argVal) []known {
	if con.Or != nil {
		k := known{path: con.Path}
		for _, alt := range con.Or {
			alts := f.knownOf(alt, subst)
			if len(alts) != 1 {
				return nil // an alternative that says nothing
			}
			k.or = append(k.or, alts[0])
		}
		return []known{k}
	}
	if con.PredParam != "" {
		ks := f.predsOf(subst(con.PredParam))
		for i := range ks {
			ks[i].path = con.Path
		}
		return ks
	}
	return []known{{pred: con.Pred, args: f.substitute(con, subst), path: con.Path}}
}

// predsOf is what a predicate parameter's argument says about a value
// it returns true for: a predicate (`xs.filter(positive)`), the
// caller's own predicate parameter, or the facts a lambda's body
// establishes about its parameter (`x => positive(x) && small(x)`).
func (f *factChecker) predsOf(arg argVal) []known {
	switch x := arg.expr.(type) {
	case *FuncRef:
		if x.Inst.Func.Decl.IsPred && len(x.Inst.Func.Params) == 1 {
			return []known{{pred: x.Inst.Func}}
		}
		return nil
	case *VarRef:
		if k := x.Var.Kind; k == VarParam || k == VarLambdaParam {
			// A function parameter (the checker made sure of that). A
			// fact named after it only means something if it gives the
			// same answer every time: an open one is pure at the calls
			// where facts are kept (see the Lambda case), and one that
			// declares effects is not.
			if ft, ok := x.Var.Type.(*FuncType); ok && ft.Effects&^EffOpen != 0 {
				return nil
			}
			return []known{{pred: f.paramPred(x.Var)}}
		}
	case *Lambda:
		if len(x.Params) != 1 {
			return nil
		}
		subject := fmt.Sprintf("l:%p", x.Params[0])
		var out []known
		for _, ft := range f.conditionFacts(x.Body, true) {
			if ft.pred != nil && ft.subject == subject {
				out = append(out, known{pred: ft.pred, args: ft.args})
			}
		}
		return out
	}
	return nil
}

// paramPred stands for a function parameter used as a predicate, as a
// predicate of its own: facts about it can be known and required, but
// it cannot be run.
func (f *factChecker) paramPred(p *Var) *Func {
	if fn, ok := f.predParams[p]; ok {
		return fn
	}
	fn := &Func{Decl: &syntax.FuncDecl{Pos: p.Pos, Name: p.Name, IsPred: true}, Synthetic: true}
	f.predParams[p] = fn
	return fn
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
		for _, p := range fn.ParamVars {
			if p.Name == param {
				return f.argOf(f.paramRef(p))
			}
		}
		return argVal{text: param}
	}
}

// paramRef is a use of parameter p, for proving facts about a parameter
// that no expression mentions.
func (f *factChecker) paramRef(p *Var) *VarRef {
	if ref, ok := f.params[p]; ok {
		return ref
	}
	ref := &VarRef{expr: expr{pos: p.Pos, typ: p.Type}, Var: p}
	f.params[p] = ref
	return ref
}

func noParams(param string) argVal { return argVal{text: param} }

func (f *factChecker) argOf(x Expr) argVal {
	return argVal{key: f.key(x), value: constOf(x), text: f.describe(x), expr: x}
}

// callArgs substitutes a callee's parameters by the call's arguments.
func (f *factChecker) callArgs(call *Call) func(string) argVal {
	return func(param string) argVal {
		for i, p := range call.Func.Decl.Params {
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
	x Expr
	e env
}

// prove tries to prove ob for the value x. It returns false if that is
// not possible; otherwise the proof may still depend on predicates of
// constants, returned as queries to evaluate at compile time.
func (f *factChecker) prove(x Expr, ob obligation, e env, depth int) (bool, []Query) {
	if depth > maxDepth || (ob.pred == nil && ob.or == nil) {
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
	// A constant, or a literal of constants: run the predicate at
	// compile time.
	if ob.path == "" {
		if q, ok := f.literalQuery(ob, x); ok {
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
func (f *factChecker) proveCases(x Expr, ob obligation, e env, depth int) (bool, []Query) {
	var cs candidates
	switch x := x.(type) {
	case *VarRef:
		switch d := x.Var; d.Kind {
		case VarLet:
			if cs.take(f.prove(d.Let.Value, ob, e, depth+1)) {
				return true, nil
			}
		case VarLambdaParam:
			if la, ok := f.lambdaArgs[d]; ok {
				if cs.take(f.lambdaParam(la, ob, e, depth)) {
					return true, nil
				}
			}
		case VarPattern:
			if src := d.Source; src != nil {
				var ok bool
				var pending []Query
				if src.Member == nil {
					// A part of the matched value.
					inner := ob
					inner.path = src.Path + ob.path
					ok, pending = f.prove(src.Subject, inner, e, depth+1)
				} else {
					ok, pending = f.proveMember(src.Subject, src.Member, ob, e, depth+1)
				}
				if cs.take(ok, pending) {
					return true, nil
				}
			}
		}
	case *Call:
		if cs.take(f.derive(x, nil, ob, e, depth)) {
			return true, nil
		}
		if cs.take(f.parametric(x, ob, e, depth)) {
			return true, nil
		}
	case *ListLit:
		// Every element, one by one.
		if rest, ok := cutStep(ob.path, ".[]"); ok {
			inner := ob
			inner.path = rest
			var bs []branch
			for _, el := range x.Elems {
				bs = append(bs, branch{el, e})
			}
			if cs.take(f.all(inner, depth, bs...)) {
				return true, nil
			}
		}
	case *RecordLit:
		// A field of a record or variant built here.
		for _, fi := range x.Fields {
			if rest, ok := cutStep(ob.path, "."+fi.Name); ok {
				inner := ob
				inner.path = rest
				if cs.take(f.prove(fi.Value, inner, e, depth+1)) {
					return true, nil
				}
			}
		}
		if v := x.Variant; v != nil && ob.path != "" && v.Field(firstStep(ob.path)) == nil {
			return true, nil // a variant without that part: nothing to prove
		}
	case *VariantValue:
		if ob.path != "" {
			return true, nil // Option.None has no value to constrain
		}
	case *Select:
		if _, isRec := x.X.Type().(*Record); isRec {
			inner := ob
			inner.path = "." + x.Name + ob.path
			if cs.take(f.prove(x.X, inner, e, depth+1)) {
				return true, nil
			}
		}
	case *Try:
		if x.Option == nil {
			if cs.take(f.proveMember(x.X, x.Kept, ob, e, depth+1)) {
				return true, nil
			}
		}
	case *Block:
		if x.Tail != nil {
			if cs.take(f.prove(x.Tail, ob, f.stmts(x.Stmts, e), depth+1)) {
				return true, nil
			}
		}
	case *ScopeBlock:
		if cs.take(f.prove(x.Body, ob, e, depth+1)) {
			return true, nil
		}
	case *If:
		if x.Else != nil {
			if cs.take(f.all(ob, depth,
				branch{x.Then, e.with(f.conditionFacts(x.Cond, true)...)},
				branch{x.Else, e.with(f.conditionFacts(x.Cond, false)...)})) {
				return true, nil
			}
		}
	case *Match:
		var bs []branch
		for _, arm := range x.Arms {
			armEnv := e
			for _, guard := range arm.Pat.Guards() {
				armEnv = armEnv.with(f.conditionFacts(guard, true)...)
			}
			bs = append(bs, branch{arm.Body, armEnv})
		}
		if cs.take(f.all(ob, depth, bs...)) {
			return true, nil
		}
	}
	if ob.or == nil {
		if cs.take(f.byRules(f.argOf(x), ob, e, depth)) {
			return true, nil
		}
	}
	if ob.path == "" {
		if cs.take(f.split(x, ob, e, depth)) {
			return true, nil
		}
	}
	return cs.result()
}

// candidates collects proofs that still depend on compile-time queries.
// Such a proof may turn out false while another way of proving holds,
// so every way is tried: one without queries settles it, and otherwise
// any of the candidates' queries holding is enough.
type candidates struct{ alts [][]Query }

// take adds a proof attempt, and reports whether it settles the goal.
func (cs *candidates) take(ok bool, pending []Query) bool {
	if !ok {
		return false
	}
	if len(pending) == 0 {
		return true
	}
	cs.alts = append(cs.alts, pending)
	return false
}

func (cs *candidates) result() (bool, []Query) {
	switch len(cs.alts) {
	case 0:
		return false, nil
	case 1:
		return true, cs.alts[0] // each query can fail on its own
	}
	or := Query{}
	for _, alt := range cs.alts {
		or.Or = append(or.Or, allOf(alt))
	}
	return true, []Query{or}
}

// cutStep removes the first step of path if it is step.
func cutStep(path, step string) (string, bool) {
	rest, ok := strings.CutPrefix(path, step)
	return rest, ok && (rest == "" || rest[0] == '.')
}

func firstStep(path string) string {
	step, _, _ := strings.Cut(path[1:], ".")
	return step
}

// parametric proves ob for the result of a call of a generic function
// from its arguments. The function cannot make values of its type
// parameters, so those in its result come from its arguments, and what
// holds for all of those holds for them: the head of a list of positive
// numbers is positive.
func (f *factChecker) parametric(call *Call, ob obligation, e env, depth int) (bool, []Query) {
	fn := call.Func
	type source struct {
		arg  int
		path string
	}
	for _, tp := range fn.TypeParams {
		results, ok := typeParamPaths(fn.Result, tp, "")
		if !ok {
			continue
		}
		var sources []source
		for i, pt := range fn.Params {
			paths, ok := typeParamPaths(pt, tp, "")
			if !ok {
				sources = nil
				break
			}
			for _, p := range paths {
				sources = append(sources, source{i, p})
			}
		}
		if len(sources) == 0 {
			continue
		}
		for _, r := range results {
			rest, ok := strings.CutPrefix(ob.path, r)
			if !ok || (rest != "" && rest[0] != '.') {
				continue
			}
			var pending []Query
			proven := true
			for _, s := range sources {
				inner := ob
				inner.path = s.path + rest
				ok, p := f.prove(call.Args[s.arg], inner, e, depth+1)
				if !ok {
					proven = false
					break
				}
				pending = append(pending, p...)
			}
			if proven {
				return true, pending
			}
		}
	}
	return false, nil
}

// lambdaParam proves ob for a parameter of a lambda passed to a generic
// function, where the parameter takes values of a type parameter: the
// function can only pass it values it was given, so it is proven for
// those (`positives.map(p => transfer(p))`).
func (f *factChecker) lambdaParam(la lambdaArg, ob obligation, e env, depth int) (bool, []Query) {
	fn := la.call.Func
	ft, ok := fn.Params[la.arg].(*FuncType)
	if !ok || la.param >= len(ft.Params) {
		return false, nil
	}
	tp, ok := ft.Params[la.param].(*TypeParam)
	if !ok {
		return false, nil
	}
	var pending []Query
	found := false
	for i, pt := range fn.Params {
		paths, ok := typeParamPaths(pt, tp, "")
		if !ok {
			return false, nil
		}
		for _, p := range paths {
			inner := ob
			inner.path = p + ob.path
			ok, more := f.prove(la.call.Args[i], inner, e, depth+1)
			if !ok {
				return false, nil
			}
			pending = append(pending, more...)
			found = true
		}
	}
	return found, pending
}

// typeParamPaths lists the paths at which values of the type parameter
// tp occur in t. It fails if they occur where facts cannot follow them:
// in a union, or as what a function value returns. (A function value's
// parameters only take values of tp.)
func typeParamPaths(t Type, tp *TypeParam, path string) ([]string, bool) {
	switch t := t.(type) {
	case *TypeParam:
		if t == tp {
			return []string{path}, true
		}
	case *List:
		return typeParamPaths(t.Elem, tp, path+".[]")
	case *Map:
		if mentions(t, tp) {
			return nil, false // not followed into maps yet
		}
	case *Sealed:
		if IsOption(t) {
			return typeParamPaths(t.Args[0], tp, path+".value")
		}
		if mentions(t, tp) {
			return nil, false // not followed into other generic types yet
		}
	case *Record:
		if mentions(t, tp) {
			return nil, false
		}
	case *Union:
		if mentions(t, tp) {
			return nil, false
		}
	case *FuncType:
		if mentions(t.Result, tp) {
			return nil, false
		}
		for _, p := range t.Params {
			if _, isTP := p.(*TypeParam); !isTP && mentions(p, tp) {
				return nil, false
			}
		}
	}
	return nil, true
}

// mentions reports whether t refers to the type parameter tp.
func mentions(t Type, tp *TypeParam) bool {
	switch t := t.(type) {
	case *TypeParam:
		return t == tp
	case *List:
		return mentions(t.Elem, tp)
	case *Map:
		return mentions(t.Key, tp) || mentions(t.Value, tp)
	case *Record, *Sealed:
		for _, a := range TypeArgs(t) {
			if mentions(a, tp) {
				return true
			}
		}
	case *Union:
		for _, m := range t.Members {
			if mentions(m, tp) {
				return true
			}
		}
	case *FuncType:
		for _, p := range t.Params {
			if mentions(p, tp) {
				return true
			}
		}
		return mentions(t.Result, tp)
	}
	return false
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
func (f *factChecker) split(x Expr, ob obligation, e env, depth int) (bool, []Query) {
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

// literalQuery is ob on x, if x and ob's arguments are made of constants
// only, so the predicate can be run at compile time.
func (f *factChecker) literalQuery(ob obligation, x Expr) (Query, bool) {
	if ob.pred == nil || ob.pred.Synthetic || !f.closed(x) {
		return Query{}, false
	}
	q := Query{Pred: ob.pred, Params: ob.pred.Params}
	if v := constOf(x); v != nil {
		q.Args = []constant.Value{v}
	} else {
		q.Subject, q.SubjectText = x, f.literalText(x)
	}
	for _, a := range ob.args {
		if a.value == nil {
			return Query{}, false
		}
		q.Args = append(q.Args, a.value)
	}
	if len(ob.pred.TypeParams) > 0 {
		in := newInference(ob.pred)
		in.unify(ob.pred.Params[0], x.Type())
		if len(in.unsolved()) > 0 {
			return Query{}, false
		}
		inst := in.instance()
		q.TypeArgs, q.Params = inst.TypeArgs, inst.Params
	}
	return q, true
}

// closed reports whether x is made of constants only: a constant, or a
// list, record, or variant literal of them.
func (f *factChecker) closed(x Expr) bool {
	if constOf(x) != nil {
		return true
	}
	switch x := x.(type) {
	case *ListLit:
		for _, el := range x.Elems {
			if !f.closed(el) {
				return false
			}
		}
		return true
	case *RecordLit:
		for _, fi := range x.Fields {
			if !f.closed(fi.Value) {
				return false
			}
		}
		return true
	case *VariantValue:
		return true
	}
	return false
}

// literalText shows a closed expression as written.
func (f *factChecker) literalText(x Expr) string {
	if v := constOf(x); v != nil {
		return CArg{Const: v}.String()
	}
	switch x := x.(type) {
	case *ListLit:
		parts := make([]string, len(x.Elems))
		for i, el := range x.Elems {
			parts[i] = f.literalText(el)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *RecordLit:
		name := ""
		if t := x.Record; t != nil {
			name = t.Name
		} else if t := x.Variant; t != nil {
			name = t.Parent.Name + "." + t.Name
		}
		parts := make([]string, len(x.Fields))
		for i, fi := range x.Fields {
			parts[i] = fi.Name + ": " + f.literalText(fi.Value)
		}
		return name + " { " + strings.Join(parts, ", ") + " }"
	case *VariantValue:
		return x.Variant.Parent.Name + "." + x.Variant.Name
	}
	return "?"
}

// constQuery is ob on the constant v, if all its arguments are constants.
func constQuery(ob obligation, v constant.Value) (Query, bool) {
	if ob.pred == nil || ob.pred.Synthetic || len(ob.pred.TypeParams) > 0 {
		return Query{}, false // cannot be run on its own
	}
	q := Query{Pred: ob.pred, Args: []constant.Value{v}, Params: ob.pred.Params}
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
		if b.x.Type() == Never {
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
func (f *factChecker) proveMember(x Expr, m Type, ob obligation, e env, depth int) (bool, []Query) {
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
	case *Call:
		return f.derive(x, m, ob, e, depth)
	case *VarRef:
		if x.Var.Kind == VarLet {
			return f.proveMember(x.Var.Let.Value, m, ob, e, depth+1)
		}
	}
	return false, nil
}

// declared lists what is known about the value x without looking into
// how it was computed: facts from guards and trust, and the facts that
// declarations and promises give it.
func (f *factChecker) declared(x Expr, e env, depth int) []known {
	var out []known
	if k := f.key(x); k != "" {
		for _, ft := range e.facts {
			if ft.pred == nil {
				continue
			}
			if rest, ok := strings.CutPrefix(ft.subject, k); ok && (rest == "" || rest[0] == '.') {
				out = append(out, known{pred: ft.pred, args: ft.args, path: rest})
			}
		}
	}
	add := func(cons []*Constraint, subst func(string) argVal) {
		for _, con := range cons {
			out = append(out, f.knownOf(con, subst)...)
		}
	}
	switch x := x.(type) {
	case *VarRef:
		switch d := x.Var; d.Kind {
		case VarParam:
			if f.ownsParam(d) {
				add(f.fn.ParamConstraints[d.Index], f.ownParams())
			}
		case VarLet:
			add(d.Let.Constraints, f.ownParams())
			if depth < maxDepth {
				out = append(out, f.declared(d.Let.Value, e, depth+1)...)
			}
		case VarPattern:
			if src := d.Source; src != nil && depth < maxDepth {
				if src.Field != nil {
					add(src.Field.Constraints, noParams)
				}
				if src.Member == nil {
					out = append(out, within(f.declared(src.Subject, e, depth+1), src.Path)...)
				} else {
					out = append(out, f.declaredMember(src.Subject, src.Member)...)
				}
			}
		}
	case *Call:
		fn := x.Func
		for _, mc := range fn.ResultConstraints {
			if identical(mc.Type, fn.Result) {
				add(mc.Constraints, f.callArgs(x))
			}
		}
		if _, isUnion := fn.Result.(*Union); !isUnion {
			add(f.argFactsFor(x, nil), noParams)
		}
	case *Try:
		if x.Option == nil {
			out = append(out, f.declaredMember(x.X, x.Kept)...)
		}
	case *Select:
		if rec, ok := x.X.Type().(*Record); ok {
			if fd := rec.Field(x.Name); fd != nil {
				add(fd.Constraints, noParams)
			}
			if depth < maxDepth {
				out = append(out, within(f.declared(x.X, e, depth+1), "."+x.Name)...)
			}
		}
	}
	return out
}

// declaredMember lists what a function promises about the member m of
// the union x produces.
func (f *factChecker) declaredMember(x Expr, m Type) []known {
	var out []known
	switch x := x.(type) {
	case *Call:
		for _, mc := range x.Func.ResultConstraints {
			if identical(mc.Type, m) {
				for _, con := range mc.Constraints {
					out = append(out, f.knownOf(con, f.callArgs(x))...)
				}
			}
		}
		for _, con := range f.argFactsFor(x, m) {
			out = append(out, f.knownOf(con, noParams)...)
		}
	case *VarRef:
		if x.Var.Kind == VarLet {
			out = append(out, f.declaredMember(x.Var.Let.Value, m)...)
		}
	}
	return out
}

// derive proves ob for the result of a call (or, with member, for that
// member of its union result) from the callee's body: every value it
// returns must satisfy ob. This is how helpers pass on facts without
// declaring them. A value the callee returns that is one of its
// parameters is proven at the call site, for the argument.
func (f *factChecker) derive(call *Call, member Type, ob obligation, e env, depth int) (bool, []Query) {
	fn := call.Func
	activeKey := fmt.Sprintf("derive %p", fn)
	if fn.Body == nil || f.active[activeKey] || depth > maxDepth {
		return false, nil
	}
	// Another package's function promises only what its signature says,
	// so that changing its body cannot break its importers.
	if !fn.Prelude && f.fn != nil && fn.Pkg != f.fn.Pkg {
		return false, nil
	}
	f.active[activeKey] = true
	defer delete(f.active, activeKey)
	// The obligation, in terms of the callee's parameters.
	inner, ok := f.calleeObligation(call, ob)
	if !ok {
		return false, nil
	}
	var pending []Query
	for _, path := range f.resultPaths(fn) {
		t := path.x.Type()
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
			if ref, isRef := path.x.(*VarRef); isRef && isParamOf(ref.Var, fn) && ref.Var.Index < len(call.Args) {
				ok, p = f.prove(call.Args[ref.Var.Index], ob, e, depth+1)
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

// isParamOf reports whether v is a parameter of fn.
func isParamOf(v *Var, fn *Func) bool {
	return v.Kind == VarParam && v.Index < len(fn.ParamVars) && fn.ParamVars[v.Index] == v
}

// ownsParam reports whether v is a parameter of the current function.
func (f *factChecker) ownsParam(v *Var) bool {
	return f.fn != nil && isParamOf(v, f.fn)
}

// calleeObligation translates ob's arguments from a call's arguments to
// the callee's parameters. It fails if an argument is neither a
// constant nor one of the call's arguments.
func (f *factChecker) calleeObligation(call *Call, ob obligation) (obligation, bool) {
	inner := ob
	if ob.or != nil {
		inner.or = make([]obligation, len(ob.or))
		for i, alt := range ob.or {
			var ok bool
			if inner.or[i], ok = f.calleeObligation(call, alt); !ok {
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
				p := call.Func.ParamVars[j]
				inner.args[i] = argVal{key: "p:" + p.Name, text: p.Name, expr: f.paramRef(p)}
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
			goal := fmt.Sprintf("rule %s %s%s %s", r.Decl.Name, v.key, ob.path, ob.pred.Decl.Name)
			for _, a := range ob.args {
				goal += " " + a.key
			}
			if v.key == "" || f.active[goal] {
				continue
			}
			f.active[goal] = true
			proven, pending := f.premises(r, 0, bound, ob.path, e, depth+1)
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
// With a path, the rule is applied to that part of the values (every
// element of a list).
func (f *factChecker) premises(r *Rule, i int, bound map[string]argVal, path string, e env, depth int) (bool, []Query) {
	if i == len(r.Premises) {
		vars := map[string]constant.Value{}
		for name, v := range bound {
			vars[name] = v.value
		}
		for _, cond := range r.Conditions {
			v := evalCondition(cond, vars)
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
		ok, pending := f.proveArg(subject, obligation{pred: p.Pred, args: args, path: path}, e, depth)
		if !ok {
			return false, nil
		}
		rest, more := f.premises(r, i+1, bound, path, e, depth)
		return rest, append(pending, more...)
	}
	// Bind the free variables to a fact about the subject.
	var candidates []known
	if subject.expr != nil {
		for _, k := range f.declared(subject.expr, e, depth) {
			if k.path == path {
				candidates = append(candidates, k)
			}
		}
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
		if ok, pending := f.premises(r, i+1, next, path, e, depth); ok {
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
	if v.value != nil && ob.path == "" {
		if q, ok := constQuery(ob, v.value); ok {
			return true, []Query{q}
		}
	}
	if v.key != "" {
		for _, ft := range e.facts {
			if ft.subject == v.key+ob.path && (known{pred: ft.pred, args: ft.args}).proves(obligation{pred: ob.pred, args: ob.args}) {
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
func (f *factChecker) conditionFacts(cond Expr, positive bool) []fact {
	switch c := cond.(type) {
	case *Unary:
		if c.Op == syntax.Not {
			return f.conditionFacts(c.X, !positive)
		}
	case *Binary:
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
	case *Call:
		fn, args := c.Func, c.Args
		if !positive || !fn.Decl.IsPred || len(args) == 0 {
			return nil
		}
		subject := f.key(args[0])
		if subject == "" {
			return nil
		}
		ft := fact{pred: fn, subject: subject}
		for _, a := range args[1:] {
			ft.args = append(ft.args, f.argOf(a))
		}
		return []fact{ft}
	case *CallValue:
		if p, ok := c.Fun.(*VarRef); ok && positive && p.Var.Kind == VarParam && len(c.Args) == 1 {
			if ft, ok := p.Var.Type.(*FuncType); ok && ft.Effects == 0 && ft.Result == Bool {
				if subject := f.key(c.Args[0]); subject != "" {
					return []fact{{pred: f.paramPred(p.Var), subject: subject}}
				}
			}
		}
	}
	return nil
}

// --- Identifying values ---

// key identifies the value of x, so facts about it can be found again:
// a parameter, a binding, a field path from one of those, or a constant.
// Bindings to another value share its key. Values that cannot be
// identified (calls, arithmetic) have no key.
func (f *factChecker) key(x Expr) string {
	if v := constOf(x); v != nil {
		return constKey(v)
	}
	switch x := x.(type) {
	case *VarRef:
		switch d := x.Var; d.Kind {
		case VarParam:
			return "p:" + d.Name
		case VarLambdaParam:
			return fmt.Sprintf("l:%p", d)
		case VarLet:
			if k := f.aliasKey(d.Let.Value); k != "" {
				return k
			}
			return fmt.Sprintf("b:%p", d)
		default:
			if src := d.Source; src != nil && src.Member == nil {
				if k := f.aliasKey(src.Subject); k != "" {
					return k + src.Path
				}
			}
			return fmt.Sprintf("n:%p", d)
		}
	case *Select:
		if k := f.key(x.X); k != "" {
			return k + "." + x.Name
		}
	}
	return ""
}

// aliasKey is the key of x if binding x just gives a value another name.
func (f *factChecker) aliasKey(x Expr) string {
	switch x.(type) {
	case *VarRef, *Select:
		return f.key(x)
	}
	return ""
}

func constKey(v constant.Value) string { return "c:" + v.ExactString() }

// constOf is the value of x if it is a constant, or nil.
func constOf(x Expr) constant.Value {
	if c, ok := x.(*Const); ok {
		return c.Value
	}
	return nil
}

// --- Messages ---

// describe shows a value in a message: its name, field path, or
// constant, or "this value".
func (f *factChecker) describe(x Expr) string {
	if v := constOf(x); v != nil {
		return CArg{Const: v}.String()
	}
	switch x := x.(type) {
	case *VarRef:
		return x.Var.Name
	case *FuncRef:
		return x.Name
	case *VariantValue:
		return x.Text
	case *Select:
		if inner := f.describe(x.X); inner != "this value" {
			return inner + "." + x.Name
		}
	}
	return "this value"
}

// hint suggests how to establish a missing fact.
func (f *factChecker) hint(x Expr, ob obligation) string {
	name := f.describe(x)
	if ob.pred == nil && ob.or == nil {
		return ""
	}
	if ob.path == ".[]" {
		keep := "x => " + checkText("x", ob, f.from())
		if ob.pred != nil && len(ob.args) == 0 {
			keep = ob.pred.QualifiedName(f.from())
		}
		if name == "this value" {
			name = "xs"
		}
		return fmt.Sprintf(" (keep only those that are: %s.filter(%s))", name, keep)
	}
	if sel, ok := f.fieldSelector(x, ob.path); ok && name != "this value" {
		// A field of a record can be checked directly.
		return fmt.Sprintf(" (check it first with if (%s) { ... })", checkText(name+sel, obligation{pred: ob.pred, args: ob.args, or: ob.or}, f.from()))
	}
	if call, ok := x.(*Call); ok && ob.path == "" {
		if fn := call.Func; !fn.Prelude && f.fn != nil && fn.Pkg != f.fn.Pkg {
			return fmt.Sprintf(" (%s does not promise it in its signature, and only what it promises is known outside its package; give the result a name and check it first)", fn.QualifiedName(f.from()))
		}
	}
	if ob.path != "" || name == "this value" {
		return " (give it a name and check it first)"
	}
	check := fmt.Sprintf("if (%s) { ... }", checkText(name, ob, f.from()))
	if ref, ok := x.(*VarRef); ok && f.ownsParam(ref.Var) {
		p := ref.Var
		return fmt.Sprintf(" (check it first with %s, or require it: %s: %s where %s)", check, p.Name, f.fn.Params[p.Index], ob.con)
	}
	return fmt.Sprintf(" (check it first with %s)", check)
}

// fieldSelector is path as field selectors (".address.zip"), if every
// step of it is a field of a record.
func (f *factChecker) fieldSelector(x Expr, path string) (string, bool) {
	if path == "" {
		return "", false
	}
	t := x.Type()
	for _, step := range strings.Split(path[1:], ".") {
		rec, ok := t.(*Record)
		if !ok || rec.Field(step) == nil {
			return "", false
		}
		t = rec.Field(step).Type
	}
	return path, true
}

// checkText is the condition that checks ob for the value called name,
// as code in package from would write it.
func checkText(name string, ob obligation, from *Package) string {
	if ob.or != nil {
		alts := make([]string, len(ob.or))
		for i, alt := range ob.or {
			alts[i] = checkText(name, alt, from)
		}
		return strings.Join(alts, " || ")
	}
	args := []string{name}
	for _, a := range ob.args {
		args = append(args, a.text)
	}
	return fmt.Sprintf("%s(%s)", ob.pred.QualifiedName(from), strings.Join(args, ", "))
}

// from is the package of the code being checked.
func (f *factChecker) from() *Package {
	if f.fn == nil {
		return nil
	}
	return f.fn.Pkg
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
		f.diags.AddCode(f.pending[0].pos, "facts.error", "cannot run predicates at compile time: %v", err)
		return
	}
	for _, p := range f.pending {
		if !results[index[p.query.String()]] {
			if p.query.Via != "" && p.query.Pred != nil && p.query.Subject == nil {
				f.diags.AddCode(p.pos, "facts.error", "%s, but %s can return %s, and %s is false", p.ob.requirement, p.query.Via, CArg{Const: p.query.Args[0]}, p.query.Text(p.from))
				continue
			}
			if p.query.Via != "" {
				f.diags.AddCode(p.pos, "facts.error", "%s, but for a value %s can return, %s is false", p.ob.requirement, p.query.Via, p.query.Text(p.from))
				continue
			}
			f.diags.AddCode(p.pos, "facts.error", "%s, but %s is false", p.ob.requirement, p.query.Text(p.from))
		}
	}
}
