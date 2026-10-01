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
type Query struct {
	Pred *Func
	Args []constant.Value // the constrained value first
}

func (q Query) String() string {
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
	f := &factChecker{info: info, diags: diags}
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
}

// A fact is a predicate known to hold: pred(subject, args...), with
// values identified by key (see key).
type fact struct {
	pred    *Func
	subject string
	args    []string
}

// env holds the facts known at a point in a function. Facts are only
// ever added, and only for the code they dominate.
type env struct{ facts []fact }

func (e env) with(fs ...fact) env {
	out := make([]fact, 0, len(e.facts)+len(fs))
	return env{facts: append(append(out, e.facts...), fs...)}
}

func (e env) has(pred *Func, subject string, args []argVal) bool {
	for _, f := range e.facts {
		if f.pred == pred && f.subject == subject && len(f.args) == len(args) {
			same := true
			for i, a := range args {
				if a.key == "" || a.key != f.args[i] {
					same = false
				}
			}
			if same {
				return true
			}
		}
	}
	return false
}

// argVal is a predicate argument in an obligation: a value identified
// by key (empty if it cannot be identified), with its value if it is a
// constant, and how to show it.
type argVal struct {
	key   string
	value constant.Value
	text  string
}

// obligation is a requirement pred(subject, args...) at some point.
type obligation struct {
	pred *Func
	args []argVal
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
	var e env
	f.tail(fn.Decl.Body, e, f.checkResult)
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
			if len(facts) == 0 {
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

// --- Where obligations arise ---

func (f *factChecker) callObligations(call *syntax.Call, fn *Func, e env) {
	subst := func(param string) argVal {
		for i, p := range fn.Decl.Params {
			if p.Name == param && i < len(call.Args) {
				return f.argOf(call.Args[i])
			}
		}
		return argVal{text: param}
	}
	for i, cons := range fn.ParamConstraints {
		if i >= len(call.Args) {
			break
		}
		for _, con := range cons {
			req := fmt.Sprintf("%s requires %s to be %s", fn.Decl.Name, fn.Decl.Params[i].Name, con)
			f.oblige(call.Args[i], con, subst, e, req)
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
			args := f.substitute(con, f.ownParams())
			ob := obligation{pred: con.Pred, args: args, requirement: req, con: con.String()}
			var ok bool
			var pending []Query
			if member != nil {
				ok = f.proveMember(x, member, ob, e, 0)
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
	ob := obligation{pred: con.Pred, args: f.substitute(con, subst), requirement: requirement, con: con.String()}
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

func (f *factChecker) substitute(con *Constraint, subst func(string) argVal) []argVal {
	args := make([]argVal, len(con.Args))
	for i, a := range con.Args {
		if a.Const != nil {
			args[i] = argVal{key: constKey(a.Const), value: a.Const, text: a.String()}
		} else {
			args[i] = subst(a.Param)
		}
	}
	return args
}

// ownParams substitutes the current function's parameters.
func (f *factChecker) ownParams() func(string) argVal {
	return func(param string) argVal { return argVal{key: "p:" + param, text: param} }
}

func noParams(param string) argVal { return argVal{text: param} }

func (f *factChecker) argOf(x syntax.Expr) argVal {
	return argVal{key: f.key(x), value: f.info.constantOf(x), text: f.describe(x)}
}

// --- Proving ---

// prove tries to prove ob for the value x. It returns false if that is
// not possible; otherwise the proof may still depend on predicates of
// constants, returned as queries to evaluate at compile time.
func (f *factChecker) prove(x syntax.Expr, ob obligation, e env, depth int) (bool, []Query) {
	if depth > 64 {
		return false, nil
	}
	// A constant: run the predicate at compile time.
	if v := f.info.constantOf(x); v != nil {
		q := Query{Pred: ob.pred, Args: []constant.Value{v}}
		allConst := true
		for _, a := range ob.args {
			if a.value == nil {
				allConst = false
			}
			q.Args = append(q.Args, a.value)
		}
		if allConst {
			return true, []Query{q}
		}
	}
	// A fact known here, from a guard or trust.
	if k := f.key(x); k != "" && e.has(ob.pred, k, ob.args) {
		return true, nil
	}
	switch x := x.(type) {
	case *syntax.Ident:
		switch d := f.info.Defs[x].(type) {
		case *syntax.Param:
			for i, p := range f.fn.Decl.Params {
				if p == d && f.declares(f.fn.ParamConstraints[i], ob, f.ownParams()) {
					return true, nil
				}
			}
		case *syntax.Binding:
			if f.declares(f.info.BindingConstraints[d], ob, f.ownParams()) {
				return true, nil
			}
			return f.prove(d.Value, ob, e, depth+1)
		case nil:
		default:
			if src := f.info.PatSources[d]; src != nil {
				if src.Member == nil {
					return f.prove(src.Subject, ob, e, depth+1)
				}
				return f.proveMember(src.Subject, src.Member, ob, e, depth+1), nil
			}
		}
	case *syntax.Call:
		if fn := f.info.CallFuncs[x]; fn != nil {
			for _, mc := range fn.ResultConstraints {
				if identical(mc.Type, fn.Result) && f.declares(mc.Constraints, ob, f.callArgs(x, fn)) {
					return true, nil
				}
			}
		}
	case *syntax.Try:
		if info := f.info.Tries[x]; info != nil && info.Option == nil {
			return f.proveMember(x.X, info.Kept, ob, e, depth+1), nil
		}
	case *syntax.Selector:
		if rec, ok := f.info.Types[x.X].(*Record); ok {
			if fd := rec.Field(x.Name); fd != nil && f.declares(fd.Constraints, ob, noParams) {
				return true, nil
			}
		}
	case *syntax.Block:
		if x.Tail != nil {
			return f.prove(x.Tail, ob, f.stmts(x.Stmts, e), depth+1)
		}
	case *syntax.If:
		if x.Else != nil {
			return f.all(ob, depth,
				branch{x.Then, e.with(f.conditionFacts(x.Cond, true)...)},
				branch{x.Else, e.with(f.conditionFacts(x.Cond, false)...)})
		}
	case *syntax.Match:
		var bs []branch
		for _, arm := range x.Arms {
			bs = append(bs, branch{arm.Body, e})
		}
		return f.all(ob, depth, bs...)
	}
	return false, nil
}

type branch struct {
	x syntax.Expr
	e env
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
// union type) can produce: from a function's promise for that member.
func (f *factChecker) proveMember(x syntax.Expr, m Type, ob obligation, e env, depth int) bool {
	if depth > 64 {
		return false
	}
	switch x := x.(type) {
	case *syntax.Call:
		if fn := f.info.CallFuncs[x]; fn != nil {
			for _, mc := range fn.ResultConstraints {
				if identical(mc.Type, m) && f.declares(mc.Constraints, ob, f.callArgs(x, fn)) {
					return true
				}
			}
		}
	case *syntax.Ident:
		if d, ok := f.info.Defs[x].(*syntax.Binding); ok {
			return f.proveMember(d.Value, m, ob, e, depth+1)
		}
	}
	return false
}

// declares reports whether one of cons is the obligation, with the
// constraints' parameter arguments substituted by subst.
func (f *factChecker) declares(cons []*Constraint, ob obligation, subst func(string) argVal) bool {
	for _, con := range cons {
		if con.Pred != ob.pred || len(con.Args) != len(ob.args) {
			continue
		}
		args := f.substitute(con, subst)
		same := true
		for i, a := range args {
			if a.key == "" || a.key != ob.args[i].key {
				same = false
			}
		}
		if same {
			return true
		}
	}
	return false
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
			ft.args = append(ft.args, f.key(a))
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
	args := []string{name}
	for _, a := range ob.args {
		args = append(args, a.text)
	}
	check := fmt.Sprintf("if (%s(%s)) { ... }", ob.pred.Decl.Name, strings.Join(args, ", "))
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
			f.diags.Add(p.pos, "%s, but %s is false", p.ob.requirement, p.query)
		}
	}
}
