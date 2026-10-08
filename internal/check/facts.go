package check

import (
	"cmp"
	"fmt"
	"go/constant"
	"maps"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/std"
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
	// Values retain each closed argument's type for instantiated predicate calls.
	Values     []Expr
	ValueTexts []string
	Args       []constant.Value // the constrained value first
	// Subject, when set, is the constrained value as an expression of
	// constants (a list or record literal), shown as SubjectText; Args
	// then holds only the other arguments.
	Subject     Expr
	SubjectText string
	// TypeArgs are a generic predicate's type arguments, and Params its
	// parameter types with them filled in.
	TypeArgs []Type
	Params   []Type
	Dicts    []*Dict
	ArgFacts [][]*Constraint
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
	if q.Values != nil {
		args = append(args, q.ValueTexts...)
	}
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
	f.validators = validationContexts(info)
	f.validatorRequirements()
	for _, fn := range info.FuncOf {
		if fn.Requires != nil && !fn.Witness {
			f.withFunction(fn, func() {
				f.walk(fn.Requires, env{})
				f.constantRequirements(fn.Requires)
			})
		}
	}
	f.fn = nil
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
	for _, fn := range info.ExpandedFunctions {
		f.function(fn)
	}
	for _, binding := range info.PackageBindings {
		f.function(binding.Boundary)
	}
	for _, fn := range info.Tests {
		f.function(fn)
	}
	f.witnesses(info.DefinitionWitnesses)
	checkedDefaults := map[*syntax.FieldDecl]bool{}
	// A map walk would reorder otherwise identical evaluator batches between
	// checks, defeating generated-program proof reuse and Go's object cache.
	defaults := slices.SortedFunc(maps.Keys(info.fieldDefaults), func(a, b *Field) int {
		return cmp.Or(
			cmp.Compare(a.Decl.Pos.File, b.Decl.Pos.File),
			cmp.Compare(a.Decl.Pos.Line, b.Decl.Pos.Line),
			cmp.Compare(a.Decl.Pos.Col, b.Decl.Pos.Col),
			cmp.Compare(TypeText(a.Type, nil), TypeText(b.Type, nil)),
			cmp.Compare(a.defaultUse.File, b.defaultUse.File),
			cmp.Compare(a.defaultUse.Line, b.defaultUse.Line),
			cmp.Compare(a.defaultUse.Col, b.defaultUse.Col),
		)
	})
	for _, field := range defaults {
		if field.Computed {
			// Calls must be valid for every independent value admitted by the
			// declaration, including values constructed by Decode/Go boundaries.
			f.fn = &Func{Pkg: field.Pkg, Decl: &syntax.FuncDecl{Name: "computed " + field.Name}}
			f.fieldBoundary(field.Default, field.Type, env{}, func(Expr, env) {})
			continue // Result constraints need the completed construction's values.
		}
		if field.Prelude || field.Default == nil || hasTypeParam(field.Type) {
			continue
		}
		// Embedded standard declarations are validated by compiler tests. Keep
		// ordinary checks independent of Go solely for these shipped defaults.
		if importedDefault(field) && strings.HasPrefix(field.Pkg.Path, std.Prefix) {
			continue
		}
		if !field.defaultGeneric {
			if checkedDefaults[field.Decl] {
				continue
			}
			checkedDefaults[field.Decl] = true
		}
		f.fn = &Func{Pkg: field.Pkg}
		f.defaultUse = field.defaultUse
		f.defaultDecl = field.Decl.Pos
		f.walk(field.Default, env{})
		for _, con := range field.Constraints {
			if !con.HasSiblingArgs() {
				f.oblige(field.Default, con, noParams, env{}, "default of "+field.Name+" must be "+con.String())
			}
		}
	}
	f.defaultUse = diag.Pos{}
	f.defaultDecl = diag.Pos{}
	f.evaluate(eval)
}

// Closed non-generic defaults are proven once per declaration for every
// user package in the build graph; shipped standard defaults are tested with
// their package as root. Imported uses rely on that proof. Generic
// defaults still need proof after specialization, and sibling-dependent
// constraints remain use-site checks.
func importedDefault(field *Field) bool {
	return field != nil && field.Pkg != nil && !field.Pkg.Root && !field.defaultGeneric
}

type factChecker struct {
	// Preflight cannot rely on the later whole-program field-default proof.
	preflightDefaults bool
	proofArguments    bool
	// Deferred initializers check every exit, including implicit ? failures.
	expandedRecipes     map[Expr]bool
	initializerResultFn *Func
	initializerResult   func(Expr, env)
	validatorInvalid    bool
	validators          map[*Func]map[Type]bool
	candidate           Expr
	info                *Info
	diags               *diag.List
	fn                  *Func
	deriveRequest       diag.Pos
	defaultUse          diag.Pos
	defaultDecl         diag.Pos
	pending             []pendingQuery
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
	lintPatternProven map[diag.Pos]bool
	lintProven        map[diag.Pos]bool
	lintChecking      bool
	observe           func(diag.Pos, env)
	producer          *Generate
	yieldCheck        func(*Yield, env)
	// Carried names (see loop): the loop values that the values passed
	// on must give the facts their names declare (what), the facts
	// proven for each value passed on to a join where it was passed
	// on, and the facts each join has.
	carryMust  map[*Var]string
	carryFacts map[[2]*Var][]*Constraint
	joinKnown  map[*Var][]*Constraint
	// witness is set while checking a definition witness, whose failures
	// are reported to witnessDiags only when no target can avoid them.
	witness      *deriveWitnessTaint
	witnessDiags *diag.List
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
	pred       *Func
	inst       *Instance
	subject    string
	args       []argVal
	or         [][]fact
	comparison *comparison
	value      argVal
	// wild marks the facts of a witness condition that depends on the
	// target; it has no alternatives, so it proves nothing by itself.
	wild bool
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
	if c := ft.comparison; c != nil {
		return fmt.Sprintf("%s %v %s %t", c.left.key, c.op, c.right.key, c.positive)
	}
	out := ft.pred.Decl.Name
	if ft.inst != nil {
		out += "[" + argsKey(ft.inst.TypeArgs) + "]"
	}
	out += "(" + ft.subject
	for _, a := range ft.args {
		out += ", " + a.key
	}
	return out + ")"
}

// env holds the facts known at a point in a function. Facts are only
// ever added, and only for the code they dominate.
//
// In a definition witness, a wild env follows a condition or staged exit
// that differs between expansions: its facts are unknown.
type env struct {
	facts []fact
	wild  bool
}

func (e env) with(fs ...fact) env {
	out := make([]fact, 0, len(e.facts)+len(fs))
	wild := e.wild
	for _, ft := range fs {
		wild = wild || ft.wild
	}
	return env{facts: append(append(out, e.facts...), fs...), wild: wild}
}

// argVal is a value in a fact or obligation: identified by key (empty
// if it cannot be identified), with its value if it is a constant, its
// expression if there is one, and how to show it.
type argVal struct {
	key   string
	value constant.Value
	text  string
	expr  Expr
	typ   Type // used to infer predicates whose other arguments carry type parameters
}

func sameArgs(a, b []argVal) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].key == "" || a[i].key != b[i].key {
			return false
		}
		if a[i].value != nil && b[i].value != nil {
			at, bt := a[i].typ, b[i].typ
			if a[i].expr != nil {
				at = a[i].expr.Type()
			}
			if b[i].expr != nil {
				bt = b[i].expr.Type()
			}
			if at != nil && bt != nil && !identical(at, bt) {
				return false
			}
		}
	}
	return true
}

// known is a fact about some given value: pred(value, args...).
// A known fact with alternatives has a nil pred and them in or. A fact
// about part of the value (its elements) has the path to it.
type known struct {
	pkg  *Package
	inst *Instance
	pred *Func
	args []argVal
	or   []known
	path string
}

func (k known) proves(ob obligation) bool {
	if ob.inst != nil && len(ob.pred.TypeParams) > 0 && (k.inst == nil || requirementInstanceKey(k.inst) != requirementInstanceKey(ob.inst)) {
		return false
	}
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
	inst *Instance
	pred *Func
	args []argVal
	or   []obligation
	path string
	pkg  *Package // dictionary scope of the written constraint
	// requirement says who requires what, for messages:
	// "transfer requires amount to be positive".
	requirement string
	// con is the constraint as written by the requirer.
	con string
}

type pendingQuery struct {
	query   Query
	pos     diag.Pos
	ob      obligation
	from    *Package // the package whose code needs it
	request diag.Pos // derive request for target-dependent template obligations
}

func (f *factChecker) function(fn *Func) {
	// Generated bodies only assemble their inputs. Their complete construction
	// contract is checked on each call, including defaulted arguments.
	if fn.Decl.Constructor != nil {
		return
	}
	f.withFunction(fn, func() { f.tail(fn.Body, f.entryFacts(fn), f.checkResult) })
}

// Requirement prewalks and body walks share the same request provenance,
// including obligations queued for evaluation after either walk finishes.
func (f *factChecker) withFunction(fn *Func, walk func()) {
	outerFn, outerRequest := f.fn, f.deriveRequest
	start := f.diags.Len()
	f.fn = fn
	if fn.TemplateScope != nil {
		f.deriveRequest = fn.TemplateScope.Decl.Pos
	} else {
		f.deriveRequest = diag.Pos{}
	}
	defer func() {
		if f.deriveRequest.File != "" {
			f.diags.DeriveContext(start, f.deriveRequest)
		}
		f.fn, f.deriveRequest = outerFn, outerRequest
	}()
	walk()
}

// --- Walking function bodies ---

// tail walks an expression whose value is used by result, passing each
// branch's value (with the facts known in that branch) to result.
func (f *factChecker) tail(x Expr, e env, result func(Expr, env)) {
	if f.observe != nil && f.collect == nil {
		f.observe(x.Pos(), e)
	}
	x = debugValue(x)
	switch x := x.(type) {
	case *Block:
		e = f.stmts(x.Stmts, e)
		if !f.statementsComplete(x.Stmts) {
			return
		}
		if x.Tail != nil {
			f.tail(x.Tail, e, result)
		} else if f.completes(x) {
			result(&Block{expr: expr{pos: x.Pos(), typ: Ok}}, e)
		}
	case *ScopeBlock:
		for _, p := range x.Policies {
			f.walk(p, e)
		}
		f.tail(x.Body, e, result)
	case *If:
		f.walk(x.Cond, e)
		if !f.completes(x.Cond) {
			return
		}
		value, known := f.knownCondition(x.Cond)
		if !known || value {
			f.tail(x.Then, e.with(f.conditionFacts(x.Cond, true)...), result)
		}
		if known && value {
			return
		}
		if x.Else == nil {
			result(&Block{expr: expr{pos: x.Pos(), typ: Ok}}, e.with(f.conditionFacts(x.Cond, false)...))
			return
		}
		f.tail(x.Else, e.with(f.conditionFacts(x.Cond, false)...), result)
	case *Match:
		validation := e
		for _, guard := range x.ValidationGuards {
			f.walk(guard, validation)
			validation = validation.with(f.conditionFacts(guard, true)...)
		}
		f.patternTestCertainty(x, e)
		f.walk(x.X, e)
		for _, arm := range x.Arms {
			f.tail(arm.Body, f.walkPatternGuards(arm.Pat, f.patternInvariants(arm.Pat, x.X, e)), result)
		}
	default:
		f.walk(x, e)
		if x.Type() != Never {
			result(x, e)
		}
	}
}

// completes reports whether control may reach an expression's normal end.
// Some conditionals retain type Ok even when their condition or chosen arm
// cannot finish, so Type alone cannot identify an implicit Ok result.
func (f *factChecker) completes(x Expr) bool {
	x = debugValue(x)
	if x.Type() == Never {
		return false
	}
	switch x := x.(type) {
	case *Block:
		return f.statementsComplete(x.Stmts) && (x.Tail == nil || f.completes(x.Tail))
	case *ScopeBlock:
		for _, p := range x.Policies {
			if !f.completes(p) {
				return false
			}
		}
		return f.completes(x.Body)
	case *If:
		if !f.completes(x.Cond) {
			return false
		}
		if value, known := f.knownCondition(x.Cond); known {
			if value {
				return f.completes(x.Then)
			}
			return x.Else == nil || f.completes(x.Else)
		}
		return f.completes(x.Then) || x.Else == nil || f.completes(x.Else)
	case *Binary:
		if !f.completes(x.X) {
			return false
		}
		if x.Op == syntax.AndAnd || x.Op == syntax.OrOr {
			if value, known := f.knownCondition(x.X); known {
				if value == (x.Op == syntax.OrOr) {
					return true
				}
				return f.completes(x.Y)
			}
			return true
		}
		return f.completes(x.Y)
	case *Match:
		if !f.completes(x.X) {
			return false
		}
		for _, arm := range x.Arms {
			if f.completes(arm.Body) {
				return true
			}
		}
		return false
	}
	return true
}

// stmts walks a block's statements and returns the facts known after
// them.
func (f *factChecker) stmts(list []Stmt, e env) env {
	for _, s := range list {
		switch s := s.(type) {
		case *Let:
			if s.Initializer != nil {
				if s.AsyncScope != nil {
					f.walk(s.AsyncScope, e)
				}
				f.deferredBinding(s, e)
				continue
			}
			f.walk(s.Value, e)
			for _, con := range s.Constraints {
				f.oblige(s.Value, con, f.ownParams(), e, fmt.Sprintf("%s must be %s", pathPhrase(con.Path, s.Var.displayName(), s.Var.Type), con))
			}
		case *ExprStmt:
			f.walk(s.X, e)
			// A loop left only by its condition leaves its negation known
			// (of the values it carried: see joinedVar).
			if loop, ok := s.X.(*For); ok && loop.Cond != nil && !loop.Broken {
				e = e.with(f.conditionFacts(loop.Cond, false)...)
			}
			// A guard: `if (!p(x)) { return ... }` leaves p(x) known.
			if ifx, ok := s.X.(*If); ok {
				if ifx.Then.Type() == Never {
					e = e.with(f.conditionFacts(ifx.Cond, false)...)
				}
				if ifx.Else != nil && ifx.Else.Type() == Never {
					e = e.with(f.conditionFacts(ifx.Cond, true)...)
				}
			}
		case *Mock:
			// The mock's body keeps its target's promises, knowing what
			// the test knows of the values it uses.
			outer := f.fn
			f.fn = s.Func
			f.tail(s.Func.Body, e.with(f.entryFacts(s.Func).facts...), f.checkResult)
			f.fn = outer
		case *Trust:
			f.walk(s.Call, e)
			facts := f.conditionFacts(s.Call, true)
			if len(facts) == 0 && f.collect == nil {
				f.diags.AddCode(trustSubjectPos(s), "facts.error", "trust needs a value with a name (bind it first: x = ...), or the fact could not be used")
			}
			e = e.with(facts...)
		}
		if f.witness != nil && f.witness.stagedExit(s) {
			e.wild = true
		}
		if !f.statementCompletes(s) {
			break
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
	case *Comptime:
		if x.Value == nil {
			f.diags.AddCode(x.Pos(), "comptime.cycle", "comptime value is required before it is evaluated")
		} else {
			f.walk(x.Value, e)
		}
	case *Call:
		if len(f.validators[f.fn]) > 0 && x.Func.Class != nil {
			f.diags.AddCode(x.Pos(), "facts.invariant_call", "an invariant validator cannot call a class method with an unknown implementation; use a declared helper")
			f.validatorInvalid = true
		}
		for i, a := range x.Args {
			if l, ok := debugValue(a).(*Lambda); ok {
				for k, p := range l.Params {
					f.lambdaArgs[p] = lambdaArg{call: x, arg: i, param: k}
				}
			}
		}
		for _, a := range x.EvaluationArgs() {
			f.walk(a, e)
		}
		f.callObligations(x, e)
		if f.lintProven != nil && !f.lintChecking && f.collect == nil && f.fn != nil && f.fn.Pkg != nil && f.fn.Pkg.Root && !f.fn.Decl.IsPred && x.Func.Decl.IsPred {
			f.lintChecking = true
			proved, queries := f.proveCondition(x, true, e, 0)
			f.lintChecking = false
			if proved && len(queries) == 0 {
				f.lintProven[x.Pos()] = true
			}
		}
	case *CallBuiltin:
		for _, a := range x.Args {
			f.walk(a, e)
		}
	case *CallValue:
		if len(f.validators[f.fn]) > 0 {
			f.diags.AddCode(x.Pos(), "facts.invariant_call", "an invariant validator cannot call an unknown function value; use a declared helper")
			f.validatorInvalid = true
		}
		f.walk(x.Fun, e)
		for _, a := range x.Args {
			f.walk(a, e)
		}
	case *FuncRef:
		// A function with requirements cannot be a value: calls through
		// the value could not be checked.
		if f.collect == nil {
			if x.Inst.Func.Requires != nil || constructorInvariant(x.Inst.Func) {
				f.diags.AddCode(x.Pos(), "facts.error", "%s has function-level where requirements, so it cannot be used as a value; use a lambda that checks them", x.Name)
			}
			for i, cons := range x.Inst.Func.ParamConstraints {
				if len(cons) > 0 {
					f.diags.AddCode(x.Pos(), "facts.error", "%s requires %s to be %s, so it cannot be used as a value; use a lambda that checks it: x => if (...) { %s(x) } else { ... }", x.Name, x.Inst.Func.Decl.Params[i].Name, cons[0], x.Name)
					break
				}
			}
		}
	case *SeqCall:
		for _, a := range x.Args {
			f.walk(a, e)
		}
	case *Generate:
		saved, check := f.producer, f.yieldCheck
		f.producer, f.yieldCheck = x, nil
		f.walk(x.Body, e)
		f.producer, f.yieldCheck = saved, check
	case *Yield:
		f.walk(x.Value, e)
		if f.producer != nil {
			for _, con := range f.producer.Constraints {
				f.oblige(x.Value, con, f.ownParams(), e, "yielded value must be "+con.String())
			}
		}
		if f.yieldCheck != nil {
			f.yieldCheck(x, e)
		}
	case *For:
		if x.Items != nil {
			f.walk(x.Items, e)
		}
		f.loop(x, e)
	case *LoopControl:
		f.carryEdges(x.Carry, e)
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
		if !f.completes(x.X) {
			return
		}
		if value, known := f.knownCondition(x.X); known && (x.Op == syntax.AndAnd && !value || x.Op == syntax.OrOr && value) {
			return
		}
		switch x.Op {
		case syntax.AndAnd:
			f.walk(x.Y, e.with(f.conditionFacts(x.X, true)...))
		case syntax.OrOr:
			f.walk(x.Y, e.with(f.conditionFacts(x.X, false)...))
		default:
			f.walk(x.Y, e)
			if (x.Op == syntax.Shl || x.Op == syntax.Shr) && !isUnsigned(x.Y.Type()) {
				f.shiftCount(x, e)
			}
		}
	case *If:
		f.walk(x.Cond, e)
		if !f.completes(x.Cond) {
			return
		}
		value, known := f.knownCondition(x.Cond)
		if !known || value {
			f.walk(x.Then, e.with(f.conditionFacts(x.Cond, true)...))
		}
		if x.Else != nil && (!known || !value) {
			f.walk(x.Else, e.with(f.conditionFacts(x.Cond, false)...))
		}
		f.joins(x.Joins, e)
	case *Block:
		before := e
		e = f.stmts(x.Stmts, e)
		if !f.statementsComplete(x.Stmts) {
			return
		}
		if x.Tail != nil {
			f.walk(x.Tail, e)
		}
		f.carryEdges(x.Carry, e)
		f.joins(x.Joins, before)
	case *ScopeBlock:
		for _, p := range x.Policies {
			f.walk(p, e)
		}
		f.walk(x.Body, e)
	case *Return:
		if x.Value != nil {
			f.tail(x.Value, e, f.checkResult)
		} else {
			f.checkResult(&Block{expr: expr{pos: x.Pos(), typ: Ok}}, e)
		}
	case *Select:
		f.walk(x.X, e)
	case *RecordLit:
		for _, fi := range x.Fields {
			if !fi.IsDefault || !importedDefault(fi.Field) || f.preflightDefaults {
				if fi.Thunk != nil {
					f.fieldBoundary(fi.Value, fi.Field.Type, e, func(x Expr, e env) {})
				} else {
					f.walk(fi.Value, e)
				}
			}
		}
		f.recordObligations(x, e)
		for _, con := range x.Constraints {
			f.oblige(x, con, f.ownParams(), e, "constructor requires "+con.Text(f.from()))
		}
		f.nominalObligations(x, x.Variant, e)
	case *VariantValue:
		for _, con := range x.Constraints {
			f.oblige(x, con, f.ownParams(), e, "constructor requires "+con.Text(f.from()))
		}
		f.nominalObligations(x, x.Variant, e)
	case *Copy:
		f.walk(x.X, e)
		for _, u := range x.Updates {
			if u.Thunk != nil {
				f.fieldBoundary(u.Value, u.Field.Type, e, func(x Expr, e env) {})
			} else {
				f.walk(u.Value, e)
			}
		}
		f.copyObligations(x, e)
	case *Match:
		validation := e
		for _, guard := range x.ValidationGuards {
			f.walk(guard, validation)
			validation = validation.with(f.conditionFacts(guard, true)...)
		}
		f.patternTestCertainty(x, e)
		f.walk(x.X, e)
		for _, arm := range x.Arms {
			f.walk(arm.Body, f.walkPatternGuards(arm.Pat, f.patternInvariants(arm.Pat, x.X, e)))
		}
		f.joins(x.Joins, e)
	case *Try:
		f.walk(x.X, e)
		if f.fn != nil {
			f.tryResults(x, e)
		}
	case *Interp:
		for _, ix := range x.Exprs {
			f.walk(ix, e)
		}
	}
}

// tryResults checks each implicit failure return without changing its boundary.
func (f *factChecker) tryResults(x *Try, e env) {
	if x.Option != nil {
		none := x.NoneOf.Variant("None")
		failure := &VariantValue{expr: expr{pos: x.Pos(), typ: x.NoneOf}, Variant: none, Text: "Option.None"}
		if identical(x.Option, x.NoneOf) {
			failure.ProofSource = x.X
		}
		f.checkResult(failure, e)
	} else {
		for _, member := range x.Rest {
			failure := &Try{expr: expr{pos: x.Pos(), typ: member}, X: x.X, TryInfo: TryInfo{Kept: member}}
			f.checkResult(failure, e)
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
	f.tail(fn.Body, f.entryFacts(fn), f.checkResult)
	f.fn, f.collect = saveFn, saveCollect
	f.paths[fn] = paths
	return paths
}

// initializerPaths includes the local early returns of a deferred binding.
func (f *factChecker) initializerPaths(value Expr, typ Type, facts env) []branch {
	var paths []branch
	f.fieldBoundary(value, typ, facts, func(x Expr, e env) { paths = append(paths, branch{x: x, e: e}) })
	return paths
}

// --- Where obligations arise ---

// pathPhrase describes the part of the value called name that path
// leads to: "every element of xs", "the value in note".
func pathPhrase(path, name string, typ Type) string {
	if path == "" {
		return name
	}
	for _, step := range strings.Split(path[1:], ".") {
		switch step {
		case "[]":
			name = "every element of " + name
		case "value":
			name = "the value in " + name
		case "0":
			if IsOption(typ) {
				name = "the value in " + name
			} else {
				name = "the 0 of " + name
			}
		default:
			name = "the " + step + " of " + name
		}
		switch t := typ.(type) {
		case *List:
			typ = t.Elem
		case *Seq:
			typ = t.Elem
		case *Record:
			if field := t.Field(step); field != nil {
				typ = field.Type
			}
		case *Sealed:
			for _, variant := range t.Variants {
				if field := variant.Field(step); field != nil {
					typ = field.Type
					break
				}
			}
		}
	}
	return name
}

func (f *factChecker) callObligations(call *Call, e env) {
	fn, args := call.Func, call.Args
	f.callRequirements(call, e)
	f.constructorObligations(call, e)
	for i, cons := range fn.ParamConstraints {
		if i >= len(args) {
			break
		}
		for _, con := range cons {
			req := fmt.Sprintf("%s requires %s to be %s", fn.QualifiedName(f.from()), pathPhrase(con.Path, fn.Decl.Params[i].Name, fn.Params[i]), con.Text(f.from()))
			saveUse, saveDecl := f.defaultUse, f.defaultDecl
			if fn.Decl.Constructor != nil {
				if defaultValue := fn.Decl.Params[i].Default; defaultValue != nil && args[i].Pos() == defaultValue.Position() {
					f.defaultUse, f.defaultDecl = call.Pos(), fn.Result.(*Record).Fields[i].Decl.Pos
				}
			}
			f.oblige(args[i], con, f.callArgs(call), e, req)
			f.defaultUse, f.defaultDecl = saveUse, saveDecl
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
				if !fi.IsDefault || fd.Computed || con.HasSiblingArgs() || f.preflightDefaults {
					saveUse, saveDecl := f.defaultUse, f.defaultDecl
					if fi.IsDefault {
						f.defaultUse, f.defaultDecl = lit.Pos(), fd.Decl.Pos
					}
					oblige := func(value Expr, facts env) {
						f.oblige(value, con, f.recordArgs(lit), facts, fmt.Sprintf("%s requires %s to be %s", label, pathPhrase(con.Path, fi.Name, fd.Type), con.Text(f.from())))
					}
					if fi.Thunk != nil {
						f.fieldBoundary(fi.Value, fi.Field.Type, e, oblige)
					} else {
						oblige(fi.Value, e)
					}
					f.defaultUse, f.defaultDecl = saveUse, saveDecl
				}
			}
		}
	}
}

// project selects a field, preserving the identity of a value supplied by a
// literal or copy instead of inventing an identity for the whole expression.
func (f *factChecker) project(x Expr, name string) Expr {
	x = debugValue(x)
	switch v := x.(type) {
	case *Select:
		value := f.project(v.X, v.Name)
		if selected, ok := value.(*Select); !ok || selected.X != v.X || selected.Name != v.Name {
			return f.project(value, name)
		}
	case *VarRef:
		if v.Var.Kind == VarLet {
			value := f.project(v.Var.Let.Value, name)
			if _, selected := value.(*Select); !selected || f.aliasKey(v.Var.Let.Value) != "" {
				return value
			}
		}
	case *RecordLit:
		for _, field := range v.Fields {
			if field.Name == name {
				return field.Value
			}
		}
	case *Copy:
		for _, u := range v.Updates {
			if u.Path[0] != name {
				continue
			}
			if len(u.Path) == 1 {
				return u.Value
			}
		}
		var updates []*FieldUpdate
		for _, u := range v.Updates {
			if u.Path[0] == name {
				cp := *u
				cp.Path = cp.Path[1:]
				updates = append(updates, &cp)
			}
		}
		original := f.project(v.X, name)
		if len(updates) > 0 {
			return &Copy{expr: expr{pos: x.Pos(), typ: original.Type()}, X: original, Updates: updates}
		}
		return original
	}
	var field *Field
	switch t := x.Type().(type) {
	case *Record:
		field = t.Field(name)
	case *Sealed:
		for _, v := range t.Variants {
			if field = v.Field(name); field != nil {
				break
			}
		}
	}
	if field == nil {
		return nil
	}
	return &Select{expr: expr{pos: x.Pos(), typ: field.Type}, X: x, Name: name, Field: field}
}

func (f *factChecker) recordArgs(x Expr) func(string) argVal {
	return func(name string) argVal {
		if v := f.project(x, name); v != nil {
			return f.argOf(v)
		}
		return noParams(name)
	}
}

// A destructured sealed value needs the fields of its matched variant: other
// variants may use the same field names with different types.
func (f *factChecker) recordFieldArgs(x Expr, field *Field) func(string) argVal {
	if sealed, ok := x.Type().(*Sealed); ok {
		for _, variant := range sealed.Variants {
			for _, candidate := range variant.Fields {
				if candidate != field {
					continue
				}
				return func(name string) argVal {
					if sibling := variant.Field(name); sibling != nil {
						return f.argOf(&Select{expr: expr{pos: x.Pos(), typ: sibling.Type}, X: x, Name: name, Field: sibling})
					}
					return noParams(name)
				}
			}
		}
	}
	return f.recordArgs(x)
}

func constraintChanged(con *Constraint, subject string, updates []*FieldUpdate) bool {
	for _, u := range updates {
		if u.Path[0] == subject {
			return true
		}
		for _, a := range con.Args {
			if a.Sibling && a.Param == u.Path[0] {
				return true
			}
		}
	}
	for _, alt := range con.Or {
		if constraintChanged(alt, subject, updates) {
			return true
		}
	}
	return false
}

func (f *factChecker) copyObligations(cp *Copy, e env) {
	rec, _ := cp.X.Type().(*Record)
	if rec == nil {
		return
	}
	for _, fd := range rec.Fields {
		value := f.project(cp, fd.Name)
		for _, con := range fd.Constraints {
			if constraintChanged(con, fd.Name, cp.Updates) {
				oblige := func(value Expr, facts env) {
					f.oblige(value, con, f.recordArgs(cp), facts, fmt.Sprintf("%s requires %s to be %s", qualify(rec.Name, rec.Pkg, f.from()), pathPhrase(con.Path, fd.Name, fd.Type), con.Text(f.from())))
				}
				deferred := false
				for _, update := range cp.Updates {
					if update.Thunk != nil && len(update.Path) == 1 && update.Path[0] == fd.Name {
						f.fieldBoundary(update.Value, fd.Type, e, oblige)
						deferred = true
						break
					}
				}
				if !deferred {
					oblige(value, e)
				}
			}
		}
		for _, update := range cp.Updates {
			if update.Path[0] == fd.Name && len(update.Path) > 1 {
				if inner, ok := value.(*Copy); ok {
					f.copyObligations(inner, e)
				}
				break
			}
		}
	}
	f.nominalObligations(cp, nil, e)
}

// checkResult checks a value the current function returns against the
// facts its signature promises.
func (f *factChecker) checkResult(x Expr, e env) {
	if f.fn == f.initializerResultFn && f.initializerResult != nil {
		f.initializerResult(x, e)
		return
	}
	if f.collect != nil {
		*f.collect = append(*f.collect, branch{x: x, e: e})
		return
	}
	t := x.Type()
	for _, mc := range f.fn.ResultConstraints {
		var member Type
		switch {
		case assignable(t, mc.Type):
		case typesOverlap(mc.Type, t):
			member = mc.Type
		default:
			continue // a different member of the result's union
		}
		for _, con := range mc.Constraints {
			req := fmt.Sprintf("%s promises a result that is %s", f.fn.Decl.Name, con)
			if con.Path != "" {
				req = fmt.Sprintf("%s promises that %s is %s", f.fn.Decl.Name, pathPhrase(con.Path, "its result", mc.Type), con)
			}
			if f.fn.MockOf != nil {
				req += ", so its mock must keep that promise"
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

// oblige requires con of the value x, with the constraint's parameter
// arguments given by subst.
func (f *factChecker) oblige(x Expr, con *Constraint, subst func(string) argVal, e env, requirement string) {
	if f.collect != nil {
		return
	}
	ob := f.obligationOf(con, subst, requirement)
	if f.witness != nil {
		f.witnessOblige(x, ob, e)
		return
	}
	ok, pending := f.prove(x, ob, e, 0)
	f.settle(x, ob, ok, pending)
}

func (f *factChecker) settle(x Expr, ob obligation, ok bool, pending []Query) {
	pos := x.Pos()
	if f.defaultUse.File != "" {
		pos = f.defaultUse
		ob.requirement += fmt.Sprintf(" (default declared at %s)", f.defaultDecl)
	}
	if !ok {
		f.diags.AddCode(pos, "facts.error", "%s, but that is not proven for %s%s", ob.requirement, f.describe(x), f.hint(x, ob))
		return
	}
	for _, q := range pending {
		f.pending = append(f.pending, pendingQuery{query: q, pos: pos, ob: ob, from: f.from(), request: f.deriveRequest})
	}
}

// obligationOf is the obligation to prove con, with the constraint's
// parameter arguments given by subst.
func (f *factChecker) obligationOf(con *Constraint, subst func(string) argVal, requirement string) obligation {
	ob := obligation{requirement: requirement, con: con.Text(f.from()), path: con.Path, pkg: con.Pkg}
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
	return []known{{pkg: con.Pkg, pred: con.Pred, args: f.substitute(con, subst), path: con.Path}}
}

// predsOf is what a predicate parameter's argument says about a value
// it returns true for: a predicate (`xs.filter(positive)`), the
// caller's own predicate parameter, or the facts a lambda's body
// establishes about its parameter (`x => positive(x) && small(x)`).
func (f *factChecker) predsOf(arg argVal) []known {
	switch x := debugValue(arg.expr).(type) {
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
				out = append(out, known{inst: ft.inst, pred: ft.pred, args: ft.args})
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
		args[i].typ = a.Type
	}
	return args
}

func constArg(v constant.Value) argVal {
	return argVal{key: constKey(v), value: v, text: CArg{Const: v}.String()}
}

// ownParams substitutes the current function's parameters.
func (f *factChecker) ownParams() func(string) argVal { return f.paramsOf(f.fn) }

// paramsOf substitutes fn's parameters by references to them.
func (f *factChecker) paramsOf(fn *Func) func(string) argVal {
	return func(param string) argVal {
		for _, p := range fn.ParamVars {
			if p.Name == param {
				return f.argOf(f.paramRef(p))
			}
		}
		// In a mock, a name can also be one of its test's parameters
		// (names are never shadowed, so it is not ambiguous).
		if in := fn.MockIn; in != nil {
			for _, p := range in.ParamVars {
				if p.Name == param {
					return f.argOf(f.paramRef(p))
				}
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
	if sel, ok := x.(*Select); ok {
		if value := f.project(sel.X, sel.Name); value != nil {
			if projected, ok := value.(*Select); !ok || projected.X != sel.X {
				return f.argOf(value)
			}
		}
	}
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
	x = debugValue(x)
	if depth > maxDepth || (ob.pred == nil && ob.or == nil) {
		return false, nil
	}
	// A deferred recipe's value includes every local return, not only its tail.
	// Constructor type-argument facts can reach recipes through projection, too.
	if f.info.fieldRecipes[x] != nil && !f.expandedRecipes[x] {
		if f.expandedRecipes == nil {
			f.expandedRecipes = map[Expr]bool{}
		}
		f.expandedRecipes[x] = true
		defer delete(f.expandedRecipes, x)
		var paths []branch
		f.fieldBoundary(x, x.Type(), e, func(value Expr, facts env) { paths = append(paths, branch{x: value, e: facts}) })
		return f.all(ob, depth, paths...)
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
		if ob.inst != nil && k.inst == nil && k.pred == ob.pred {
			k.inst = inferredPredicate(k.pred, x, k.args)
			if k.inst != nil {
				scope := k.pkg
				if scope == nil {
					scope = f.from()
				}
				if !f.info.PredicateDicts(scope, k.inst) {
					k.inst = nil
				}
			}
		}
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
	case *Generate:
		if rest, ok := cutStep(ob.path, ".[]"); ok {
			inner := ob
			inner.path = rest
			savedP, savedCheck := f.producer, f.yieldCheck
			f.producer = x
			valid := true
			var pending []Query
			f.yieldCheck = func(y *Yield, e env) {
				ok, qs := f.prove(y.Value, inner, e, depth+1)
				valid = valid && ok
				pending = append(pending, qs...)
			}
			f.walk(x.Body, e)
			f.producer, f.yieldCheck = savedP, savedCheck
			if valid {
				return true, pending
			}
		}
	case *SeqCall:
		if len(x.Args) > 0 {
			switch x.Op {
			case "indexed":
				if rest, ok := cutStep(ob.path, ".[]"); ok {
					if rest, ok := cutStep(rest, ".1"); ok {
						inner := ob
						inner.path = ".[]" + rest
						if cs.take(f.prove(x.Args[0], inner, e, depth+1)) {
							return true, nil
						}
					}
				}
			case "map":
				if rest, ok := cutStep(ob.path, ".[]"); ok {
					if callback, ok := x.Args[1].(*Lambda); ok {
						inner := ob
						inner.path = rest
						if cs.take(f.prove(callback.Body, inner, e, depth+1)) {
							return true, nil
						}
					}
				}
			case "fromList", "take", "drop", "filter", "toList":
				if cs.take(f.prove(x.Args[0], ob, e, depth+1)) {
					return true, nil
				}
			}
		}
	case *VarRef:
		switch d := x.Var; d.Kind {
		case VarLet:
			if d.Let.Initializer != nil {
				if cs.take(f.all(ob, depth, f.initializerPaths(d.Let.Value, d.Type, e)...)) {
					return true, nil
				}
				return cs.result()
			}
			if cs.take(f.prove(d.Let.Value, ob, e, depth+1)) {
				return true, nil
			}
		case VarJoin:
			// A value where paths meet has what the value of every path has.
			var paths []branch
			for _, in := range d.Joins {
				paths = append(paths, branch{x: &VarRef{expr: expr{pos: x.Pos(), typ: in.Type}, Var: in}, e: e})
			}
			if len(paths) > 0 && cs.take(f.all(ob, depth, paths...)) {
				return true, nil
			}
		case VarLambdaParam:
			if d.Source != nil {
				inner := ob
				inner.path = d.Source.Path + ob.path
				if cs.take(f.prove(d.Source.Subject, inner, e, depth+1)) {
					return true, nil
				}
			}
			if la, ok := f.lambdaArgs[d]; ok {
				if cs.take(f.lambdaParam(la, ob, e, depth)) {
					return true, nil
				}
			}
		case VarPattern, VarLoop:
			if src := d.Source; src != nil {
				inner := ob
				inner.path = src.Path + ob.path
				var ok bool
				var pending []Query
				if src.Member == nil {
					// A part of the matched value.
					ok, pending = f.prove(src.Subject, inner, e, depth+1)
				} else {
					ok, pending = f.proveMember(src.Subject, src.Member, inner, e, depth+1)
					if !ok {
						ok, pending = f.prove(src.Subject, inner, e, depth+1)
					}
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
		} else {
			inner := ob
			inner.path = "." + x.Option.Variant("Some").Fields[0].Name + ob.path
			if cs.take(f.prove(x.X, inner, e, depth+1)) {
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
		if value, known := f.knownCondition(x.Cond); known {
			chosen := Expr(x.Then)
			if !value {
				chosen = x.Else
			}
			if chosen != nil {
				return f.prove(chosen, ob, e.with(f.conditionFacts(x.Cond, value)...), depth+1)
			}
		}
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
		if cs.take(f.unfold(f.argOf(x), ob, e, depth)) {
			return true, nil
		}
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
	case *Seq:
		return typeParamPaths(t.Elem, tp, path+".[]")
	case *List:
		return typeParamPaths(t.Elem, tp, path+".[]")
	case *Map:
		if mentions(t, tp) {
			return nil, false // not followed into maps yet
		}
	case *Sealed:
		if IsOption(t) {
			return typeParamPaths(t.Args[0], tp, path+"."+t.Variant("Some").Fields[0].Name)
		}
		if mentions(t, tp) {
			return nil, false // not followed into other generic types yet
		}
	case *Record:
		if t.Tuple {
			var paths []string
			for _, field := range t.Fields {
				inner, ok := typeParamPaths(field.Type, tp, path+"."+field.Name)
				if !ok {
					return nil, false
				}
				paths = append(paths, inner...)
			}
			return paths, true
		}
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
	case *Seq:
		return mentions(t.Elem, tp)
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
		return fact{inst: k.inst, pred: k.pred, subject: subject, args: k.args}
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
	if ob.pred != nil && ob.pred.RuntimePackageReads || packageInstanceRuntimeReads(f.info, ob.inst) {
		return Query{}, false
	}
	if len(f.info.PackageBindings) != 0 && packageRuntimeReads(f.info, x) {
		return Query{}, false
	}
	// A completed candidate is fresh data, not an existing runtime cell.
	// Reify only roots whose independent inputs are wholly known. The query
	// still checks this exact return path, rather than the recipe's tail.
	x = transformComputedDefault(nil, x, func(ref *VarRef) Expr {
		if ref.Var.Unvalidated {
			if literal, ok := ref.Var.Let.Value.(*RecordLit); ok && f.closed(literal) {
				return literal
			}
		}
		return nil
	})
	if ob.pred == nil || ob.pred.Synthetic || !f.closed(x) {
		return Query{}, false
	}
	if ob.inst != nil {
		for _, t := range ob.inst.TypeArgs {
			if hasTypeParam(t) {
				return Query{}, false
			}
		}
		values := []Expr{x}
		texts := []string{f.literalText(x)}
		if constOf(x) != nil {
			texts[0] = requirementText(x, f.from())
		}
		for _, a := range ob.args {
			if a.expr == nil || !f.closed(a.expr) {
				return Query{}, false
			}
			values = append(values, a.expr)
			text := f.literalText(a.expr)
			if constOf(a.expr) != nil {
				text = requirementText(a.expr, f.from())
			}
			texts = append(texts, text)
		}
		return Query{Pred: ob.pred, Values: values, ValueTexts: texts, Params: ob.inst.Params, TypeArgs: ob.inst.TypeArgs, Dicts: ob.inst.Dicts, ArgFacts: ob.inst.ArgFacts}, true
	}

	q := Query{Pred: ob.pred, Params: ob.pred.Params}
	// Widening to a union must preserve the literal's concrete runtime type.
	if v := constOf(x); v != nil && identical(x.Type(), ob.pred.Params[0]) {
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
		for i, arg := range ob.args {
			typ := arg.typ
			if arg.expr != nil {
				typ = arg.expr.Type()
			}
			if typ != nil {
				in.unify(ob.pred.Params[i+1], typ)
			}
		}
		if len(in.unsolved()) > 0 {
			return Query{}, false
		}
		if !assignable(x.Type(), in.subst(ob.pred.Params[0])) {
			return Query{}, false
		}
		for i, a := range ob.args {
			if a.expr != nil && !assignable(a.expr.Type(), in.subst(ob.pred.Params[i+1])) {
				return Query{}, false
			}
		}
		inst := in.instance()
		scope := ob.pkg
		if scope == nil {
			scope = f.from()
		}
		if !f.info.PredicateDicts(scope, inst) || packageInstanceRuntimeReads(f.info, inst) {
			return Query{}, false
		}
		q.TypeArgs, q.Params, q.Dicts, q.ArgFacts = inst.TypeArgs, inst.Params, inst.Dicts, inst.ArgFacts
	}
	return q, true
}

// closed reports whether x is made of constants only: a constant, or a
// list, record, or variant literal of them.
func (f *factChecker) closed(x Expr) bool {
	x = debugValue(x)
	if constOf(x) != nil {
		return true
	}
	switch x := x.(type) {
	case *FloatBits:
		return true
	case *Block:
		return x.Type() == Ok && len(x.Stmts) == 0 && x.Tail == nil
	case *Call:
		return x.BuildRead != nil && x.BuildRead.Captured
	case *MapLit:
		for i, key := range x.Keys {
			if !f.closed(key) || !f.closed(x.Values[i]) {
				return false
			}
		}
		return true
	case *ListLit:
		for _, el := range x.Elems {
			if !f.closed(el) {
				return false
			}
		}
		return true
	case *RecordLit:
		for _, fi := range x.Fields {
			if fi.Field.Computed && fi.Field.RuntimePackageReads {
				return false
			}
			if !fi.Field.Computed && !f.closed(fi.Value) {
				return false
			}
		}
		return true
	case *Select:
		return f.closed(x.X)
	case *Unary:
		return f.closed(x.X)
	case *Binary:
		return f.closed(x.X) && f.closed(x.Y)
	case *Interp:
		for _, part := range x.Exprs {
			if !f.closed(part) {
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
	x = debugValue(x)
	if v := constOf(x); v != nil {
		return CArg{Const: v}.String()
	}
	switch x := x.(type) {
	case *Call:
		if read := x.BuildRead; read != nil && read.Captured {
			return read.literalText()
		}
	case *FloatBits:
		return fmt.Sprintf("%s(bits=0x%x)", x.Type(), x.Bits)
	case *Block:
		if x.Type() == Ok && len(x.Stmts) == 0 && x.Tail == nil {
			return "Ok"
		}
	case *MapLit:
		parts := make([]string, len(x.Keys))
		for i, key := range x.Keys {
			parts[i] = f.literalText(key) + ": " + f.literalText(x.Values[i])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *ListLit:
		if x.Nil {
			return "nil"
		}
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
	if ob.pred == nil || ob.pred.Synthetic || ob.pred.RuntimePackageReads || len(ob.pred.TypeParams) > 0 {
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

// allMembers proves ob for every result path returning member m.
func (f *factChecker) allMembers(m Type, ob obligation, depth int, paths ...branch) (bool, []Query) {
	var pending []Query
	for _, path := range paths {
		ok, queries := f.proveMember(path.x, m, ob, path.e, depth+1)
		if !ok {
			return false, nil
		}
		pending = append(pending, queries...)
	}
	return true, pending
}

// typesOverlap conservatively asks whether two types can share a union member,
// including after their type parameters have been instantiated.
func typesOverlap(a, b Type) bool {
	if a == Never || b == Never {
		return false
	}
	if _, ok := a.(*TypeParam); ok {
		return true
	}
	if _, ok := b.(*TypeParam); ok {
		return true
	}
	if u, ok := a.(*Union); ok {
		for _, m := range u.Members {
			if typesOverlap(m, b) {
				return true
			}
		}
		return false
	}
	if _, ok := b.(*Union); ok {
		return typesOverlap(b, a)
	}
	if assignable(a, b) || assignable(b, a) {
		return true
	}
	switch a := a.(type) {
	case *Record, *Sealed:
		if genericBaseOrSelf(a) != genericBaseOrSelf(b) {
			return false
		}
		args, other := TypeArgs(a), TypeArgs(b)
		if len(args) != len(other) {
			return false
		}
		for i, arg := range args {
			if !typesOverlap(arg, other[i]) {
				return false
			}
		}
		return true
	case *List:
		other, ok := b.(*List)
		return ok && typesOverlap(a.Elem, other.Elem)
	case *Seq:
		other, ok := b.(*Seq)
		return ok && typesOverlap(a.Elem, other.Elem)
	case *Map:
		other, ok := b.(*Map)
		return ok && typesOverlap(a.Key, other.Key) && typesOverlap(a.Value, other.Value)
	case *FuncType:
		other, ok := b.(*FuncType)
		if !ok || len(a.Params) != len(other.Params) || !typesOverlap(a.Result, other.Result) {
			return false
		}
		for i, param := range a.Params {
			if !typesOverlap(param, other.Params[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// proveMember proves ob for the values of member type m that x (of a
// union type) can produce.
func (f *factChecker) proveMember(x Expr, m Type, ob obligation, e env, depth int) (bool, []Query) {
	x = debugValue(x)
	if depth > maxDepth {
		return false, nil
	}
	if f.info.fieldRecipes[x] != nil && !f.expandedRecipes[x] {
		if f.expandedRecipes == nil {
			f.expandedRecipes = map[Expr]bool{}
		}
		f.expandedRecipes[x] = true
		defer delete(f.expandedRecipes, x)
		return f.allMembers(m, ob, depth, f.initializerPaths(x, x.Type(), e)...)
	}
	if x.Type() == Never {
		return true, nil
	}
	if !typesOverlap(x.Type(), m) {
		return true, nil // no instantiation can produce this member
	}
	if assignable(x.Type(), m) {
		return f.prove(x, ob, e, depth+1)
	}
	if ob.or != nil {
		if ok, pending := anyOf(ob, func(alt obligation) (bool, []Query) { return f.proveMember(x, m, alt, e, depth+1) }); ok {
			return true, pending
		}
	}
	// Narrowing a union preserves facts about the same whole value.
	for _, k := range f.declared(x, e, depth) {
		if k.proves(ob) {
			return true, nil
		}
	}
	for _, k := range f.declaredMember(x, m) {
		if k.proves(ob) {
			return true, nil
		}
	}
	switch x := x.(type) {
	case *Try:
		if x.Option == nil {
			return f.proveMember(x.X, m, ob, e, depth+1)
		}
	case *Call:
		return f.derive(x, m, ob, e, depth)
	case *VarRef:
		if x.Var.Kind == VarLet {
			if x.Var.Let.Initializer != nil {
				return f.allMembers(m, ob, depth, f.initializerPaths(x.Var.Let.Value, x.Var.Type, e)...)
			}
			return f.proveMember(x.Var.Let.Value, m, ob, e, depth+1)
		}
	case *Select:
		value := f.project(x.X, x.Name)
		if selected, ok := value.(*Select); !ok || selected.X != x.X || selected.Name != x.Name {
			return f.proveMember(value, m, ob, e, depth+1)
		}
	case *Block:
		if x.Tail != nil {
			return f.proveMember(x.Tail, m, ob, f.stmts(x.Stmts, e), depth+1)
		}
	case *ScopeBlock:
		return f.proveMember(x.Body, m, ob, e, depth+1)
	case *If:
		if value, known := f.knownCondition(x.Cond); known {
			chosen := Expr(x.Then)
			if !value {
				chosen = x.Else
			}
			if chosen != nil {
				return f.proveMember(chosen, m, ob, e.with(f.conditionFacts(x.Cond, value)...), depth+1)
			}
		}
		if x.Else != nil {
			ok, pending := f.proveMember(x.Then, m, ob, e.with(f.conditionFacts(x.Cond, true)...), depth+1)
			if !ok {
				return false, nil
			}
			other, queries := f.proveMember(x.Else, m, ob, e.with(f.conditionFacts(x.Cond, false)...), depth+1)
			return other, append(pending, queries...)
		}
	case *Match:
		var pending []Query
		for _, arm := range x.Arms {
			facts := f.patternInvariants(arm.Pat, x.X, e)
			for _, guard := range arm.Pat.Guards() {
				facts = facts.with(f.conditionFacts(guard, true)...)
			}
			ok, queries := f.proveMember(arm.Body, m, ob, facts, depth+1)
			if !ok {
				return false, nil
			}
			pending = append(pending, queries...)
		}
		return true, pending
	}
	return false, nil
}

// declared lists what is known about the value x without looking into
// how it was computed: facts from guards and trust, and the facts that
// declarations and promises give it.
func (f *factChecker) declared(x Expr, e env, depth int) []known {
	if ref, ok := x.(*VarRef); ok && ref.Var.Kind == VarDefaultField && ref.Var.Sibling.Computed {
		return nil
	}
	if ref, ok := x.(*VarRef); ok {
		if v := joinedVar(ref.Var); v != ref.Var {
			return f.declared(&VarRef{expr: ref.expr, Var: v}, e, depth)
		}
	}
	if ref, ok := x.(*VarRef); ok && ref.Var.Unvalidated {
		return nil
	}
	if selected, ok := x.(*Select); ok && unvalidatedDefaultRoot(selected.X) {
		if depth < maxDepth {
			return f.declared(f.project(selected.X, selected.Name), e, depth+1)
		}
		return nil
	}
	x = debugValue(x)
	var out []known
	if k := f.key(x); k != "" {
		for _, ft := range e.facts {
			if ft.pred == nil {
				continue
			}
			if rest, ok := strings.CutPrefix(ft.subject, k); ok && (rest == "" || rest[0] == '.') {
				if constOf(x) != nil && ft.value.expr != nil && constOf(ft.value.expr) != nil && !identical(x.Type(), ft.value.expr.Type()) {
					continue
				}
				out = append(out, known{inst: ft.inst, pred: ft.pred, args: ft.args, path: rest})
			}
		}
	}
	add := func(cons []*Constraint, subst func(string) argVal) {
		for _, con := range cons {
			out = append(out, f.knownOf(con, subst)...)
		}
	}
	if x != f.candidate && !f.validates(x.Type()) {
		add(TypeConstraints(x.Type()), noParams)
	}
	switch x := x.(type) {
	case *VariantValue:
		if x.ProofSource != nil && depth < maxDepth {
			out = append(out, f.declared(x.ProofSource, e, depth+1)...)
		}
	case *SeqCall:
		if x.Op == "filter" {
			if ft, ok := x.Args[1].Type().(*FuncType); ok && ft.Effects == 0 {
				for _, k := range f.predsOf(f.argOf(x.Args[1])) {
					k.path = ".[]" + k.path
					out = append(out, k)
				}
			}
		}
		if x.Op == "map" {
			if ref, ok := debugValue(x.Args[1]).(*FuncRef); ok {
				fn := ref.Inst.Func
				for _, mc := range fn.ResultConstraints {
					if identical(mc.Type, fn.Result) {
						for _, con := range mc.Constraints {
							for _, k := range f.knownOf(con, func(param string) argVal { return argVal{text: fmt.Sprintf("seq-element:%p:%s", x, param)} }) {
								k.path = ".[]" + k.path
								out = append(out, k)
							}
						}
					}
				}
			}
		}
	case *VarRef:
		switch d := x.Var; d.Kind {
		case VarDefaultField:
			if !d.Sibling.Computed {
				for _, con := range d.Sibling.Constraints {
					if !con.HasSiblingArgs() {
						add([]*Constraint{con}, noParams)
					}
				}
			}
		case VarParam:
			if f.ownsParam(d) {
				add(f.fn.ParamConstraints[d.Index], f.ownParams())
			} else if in := f.fn.MockIn; in != nil && isParamOf(d, in) {
				// A property test's parameter, used in a mock.
				add(in.ParamConstraints[d.Index], f.ownParams())
			}
		case VarAmbient:
			// A needed value has its ambient's facts (an optional
			// need is an Option, and is not known to have them).
			if !d.Need.Optional {
				add(d.Need.Ambient.Constraints, f.ownParams())
			}
		case VarLet:
			add(d.Let.Constraints, f.ownParams())
			if d.Let.Initializer == nil && depth < maxDepth {
				out = append(out, f.declared(d.Let.Value, e, depth+1)...)
			}
		case VarJoin:
			add(f.joinKnown[d], f.ownParams())
		case VarPattern, VarLoop:
			// A loop's header name has the facts it declares.
			add(d.Invariant, f.ownParams())
			if src := d.Source; src != nil && depth < maxDepth {
				if src.Field != nil {
					owner := src.Subject
					steps := strings.Split(strings.TrimPrefix(src.Path, "."), ".")
					for _, step := range steps[:len(steps)-1] {
						owner = f.project(owner, step)
					}
					if owner != nil {
						add(src.Field.Constraints, f.recordFieldArgs(owner, src.Field))
					}
				}
				out = append(out, within(f.declared(src.Subject, e, depth+1), src.Path)...)
				if src.Member != nil {
					out = append(out, within(f.declaredMember(src.Subject, src.Member), src.Path)...)
				}
			}
		}
	case *CallValue:
		if x.Provider != nil {
			call := &Call{expr: x.expr, Func: x.Provider.Func, Inst: x.Provider, Args: x.Args}
			out = append(out, f.declared(call, e, depth+1)...)
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
	case *Match:
		if x.Assertion != nil && len(x.Arms) > 1 && depth < maxDepth {
			arm := x.Arms[0]
			facts := e
			for _, guard := range arm.Pat.Guards() {
				facts = facts.with(f.conditionFacts(guard, true)...)
			}
			out = append(out, f.declared(arm.Body, facts, depth+1)...)
		}
	case *Try:
		if x.Option == nil {
			out = append(out, f.declaredMember(x.X, x.Kept)...)
		} else if depth < maxDepth {
			path := "." + x.Option.Variant("Some").Fields[0].Name
			out = append(out, within(f.declared(x.X, e, depth+1), path)...)
		}
	case *Select:
		if rec, ok := x.X.Type().(*Record); ok {
			if fd := rec.Field(x.Name); fd != nil {
				add(fd.Constraints, f.recordArgs(x.X))
			}
			if depth < maxDepth {
				out = append(out, within(f.declared(x.X, e, depth+1), "."+x.Name)...)
			}
		}
	}
	if rec, ok := x.Type().(*Record); ok {
		for _, fd := range rec.Fields {
			for _, con := range fd.Constraints {
				for _, known := range f.knownOf(con, f.recordArgs(x)) {
					known.path = "." + fd.Name + known.path
					out = append(out, known)
				}
			}
		}
	}
	return out
}

// declaredMember lists what a function promises about the member m of
// the union x produces.
func (f *factChecker) declaredMember(x Expr, m Type) []known {
	if ref, ok := x.(*VarRef); ok && ref.Var.Kind == VarDefaultField && ref.Var.Sibling.Computed {
		return nil
	}
	if unvalidatedDefaultRoot(x) {
		if selected, ok := x.(*Select); ok {
			return f.declaredMember(f.project(selected.X, selected.Name), m)
		}
		return nil
	}
	x = debugValue(x)
	var out []known
	if !f.validates(m) {
		for _, con := range TypeConstraints(m) {
			out = append(out, f.knownOf(con, noParams)...)
		}
	}
	switch x := x.(type) {
	case *CallValue:
		if x.Provider != nil {
			call := &Call{expr: x.expr, Func: x.Provider.Func, Inst: x.Provider, Args: x.Args}
			out = append(out, f.declaredMember(call, m)...)
		}
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
	case *CallBuiltin:
		if x.Builtin == BuiltinShapeValidate && x.Validation != nil && x.Validation.ReturnValue && identical(x.Validation.Field.Type, m) {
			for _, con := range x.Validation.Constraints {
				out = append(out, f.knownOf(con, noParams)...)
			}
		}
		if x.Builtin == BuiltinShapeFinish && x.Construction != nil && identical(x.Construction.Owner, m) {
			for _, con := range x.Construction.Constraints {
				out = append(out, f.knownOf(con, noParams)...)
			}
			for _, con := range variantConstraints(x.Construction.Variant) {
				out = append(out, f.knownOf(con, noParams)...)
			}
		}
	case *VarRef:
		if x.Var.Kind == VarLet && x.Var.Let.Initializer == nil {
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
	// Nor does a function a test mocks: the mock keeps only the
	// signature's promises.
	if f.info.Mocked(fn) {
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
		param := returnedParam(path.x, fn)
		parameter := param != nil && param.Index < len(call.Args)
		if parameter {
			t = call.Args[param.Index].Type()
		}
		if member != nil && !typesOverlap(t, member) {
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
			if parameter {
				value := call.Args[param.Index]
				if member != nil {
					ok, p = f.proveMember(value, member, ob, e, depth+1)
				} else {
					ok, p = f.prove(value, ob, e, depth+1)
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

// returnedParam follows eager aliases of a returned parameter. Deferred
// initializers can return other values before their tail and must keep their
// own result boundary.
func returnedParam(x Expr, fn *Func) *Var {
	for depth := 0; depth < maxDepth; depth++ {
		ref, ok := debugValue(x).(*VarRef)
		if !ok {
			return nil
		}
		if isParamOf(ref.Var, fn) {
			return ref.Var
		}
		if ref.Var.Kind != VarLet || ref.Var.Let.Initializer != nil {
			return nil
		}
		x = ref.Var.Let.Value
	}
	return nil
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
	var cs candidates
	for _, r := range f.info.Rules {
		for _, c := range r.Conclusions {
			if c.Pred != ob.pred || len(c.Args) != len(ob.args)+1 || ob.inst != nil && len(ob.pred.TypeParams) > 0 && requirementInstanceKey(c.Inst) != requirementInstanceKey(ob.inst) {
				continue
			}
			bound := map[string]argVal{c.Args[0].Var: v}
			ok := ob.inst == nil || ruleValueFits(r, c.Args[0], v, ob.path)
			for i, a := range c.Args[1:] {
				ok = ok && (ob.inst == nil || ruleValueFits(r, a, ob.args[i], "")) && bindArg(bound, a, ob.args[i])
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
			proven, pending := f.rulePremises(r, r.Premises, bound, ob.path, e, depth+1)
			delete(f.active, goal)
			if cs.take(proven, pending) {
				return true, nil
			}
		}
	}
	return cs.result()
}

// bindArg binds a rule argument to a value, or checks that it matches.
func bindArg(bound map[string]argVal, a RuleArg, v argVal) bool {
	if a.Const != nil {
		return v.key == constKey(a.Const)
	}
	if prev, ok := bound[a.Var]; ok {
		return sameArgs([]argVal{prev}, []argVal{v})
	}
	bound[a.Var] = v
	return true
}

// rulePremises binds values mentioned only in premises from matching
// facts, then proves every premise and condition. With a path, the rule
// applies to that part of each value (for example, every list element).
func (f *factChecker) rulePremises(r *Rule, remaining []*RuleAtom, bound map[string]argVal, path string, e env, depth int) (bool, []Query) {
	if depth > maxDepth {
		return false, nil
	}
	if len(remaining) == 0 {
		vars := map[string]constant.Value{}
		subst := map[*Var]argVal{}
		for _, v := range r.Vars {
			subst[v] = bound[v.Name]
			vars[v.Name] = bound[v.Name].value
		}
		var pending []Query
		for _, cond := range r.Conditions {
			if v := evalCondition(cond, vars); v != nil && v.Kind() == constant.Bool {
				if !constant.BoolVal(v) {
					return false, nil
				}
				continue
			}
			ok, queries := f.proveCondition(substituteExpr(cond, subst), true, e, depth+1)
			if !ok {
				return false, nil
			}
			pending = append(pending, queries...)
		}
		return true, pending
	}
	var cs candidates
	for i, p := range remaining {
		args := make([]argVal, len(p.Args))
		complete := true
		for j, a := range p.Args {
			if a.Const != nil {
				args[j] = ruleConstant(a)
			} else {
				v, ok := bound[a.Var]
				args[j] = v
				complete = complete && ok
			}
		}
		rest := append(append([]*RuleAtom{}, remaining[:i]...), remaining[i+1:]...)
		if complete {
			ok, pending := f.proveArg(args[0], obligation{inst: p.Inst, pred: p.Pred, args: args[1:], path: path}, e, depth+1)
			if !ok {
				return cs.result()
			}
			done, more := f.rulePremises(r, rest, bound, path, e, depth+1)
			if cs.take(done, append(pending, more...)) {
				return true, nil
			}
			return cs.result()
		}
		// An unbound variable may be the subject as well as an argument.
		// Try each matching fact; another premise can provide the binding first.
		var candidates []fact
		if path == "" {
			candidates = append(candidates, e.facts...)
		}
		subjects := map[string]argVal{}
		for _, v := range bound {
			if v.key != "" {
				subjects[v.key] = v
			}
		}
		if f.fn != nil {
			for _, p := range f.fn.ParamVars {
				v := f.argOf(f.paramRef(p))
				subjects[v.key] = v
			}
		}
		for _, ft := range e.facts {
			if cmp := ft.comparison; cmp != nil {
				subjects[cmp.left.key], subjects[cmp.right.key] = cmp.left, cmp.right
			}
			if ft.value.key != "" {
				subjects[ft.value.key] = ft.value
			}
			for _, v := range ft.args {
				if v.key != "" {
					subjects[v.key] = v
				}
			}
		}
		for _, v := range subjects {
			if v.expr == nil {
				continue
			}
			for _, k := range f.declared(v.expr, e, depth+1) {
				if k.path == path && k.pred != nil {
					candidates = append(candidates, fact{inst: k.inst, pred: k.pred, subject: v.key, args: k.args, value: v})
				}
			}
		}
		for _, ft := range candidates {
			if ft.pred != p.Pred || len(ft.args)+1 != len(p.Args) || len(p.Pred.TypeParams) > 0 && requirementInstanceKey(ft.inst) != requirementInstanceKey(p.Inst) {
				continue
			}
			next := map[string]argVal{}
			for name, v := range bound {
				next[name] = v
			}
			subject := ft.value
			if subject.key == "" {
				subject = subjects[ft.subject]
				subject.key = ft.subject
			}
			matches := ruleValueFits(r, p.Args[0], subject, path) && bindArg(next, p.Args[0], subject)
			for j, a := range p.Args[1:] {
				matches = matches && ruleValueFits(r, a, ft.args[j], "") && bindArg(next, a, ft.args[j])
			}
			if matches {
				if cs.take(f.rulePremises(r, rest, next, path, e, depth+1)) {
					return true, nil
				}
			}
		}
		// A comparison guard can supply a missing value without naming a
		// predicate. Try its stable values, then prove the instantiated atom.
		if path == "" {
			if ok, pending := f.bindRuleValues(r, p, 0, bound, subjects, depth+1, func(next map[string]argVal) (bool, []Query) {
				args := make([]argVal, len(p.Args))
				for j, a := range p.Args {
					if a.Const != nil {
						args[j] = ruleConstant(a)
					} else {
						args[j] = next[a.Var]
					}
				}
				ok, pending := f.proveArg(args[0], obligation{inst: p.Inst, pred: p.Pred, args: args[1:]}, e, depth+1)
				if !ok {
					return false, nil
				}
				done, more := f.rulePremises(r, rest, next, path, e, depth+1)
				return done, append(pending, more...)
			}); cs.take(ok, pending) {
				return true, nil
			}
		}
	}
	return cs.result()
}

func (f *factChecker) bindRuleValues(r *Rule, p *RuleAtom, i int, bound, values map[string]argVal, depth int, prove func(map[string]argVal) (bool, []Query)) (bool, []Query) {
	if depth > maxDepth {
		return false, nil
	}
	if i == len(p.Args) {
		return prove(bound)
	}
	a := p.Args[i]
	if _, ok := bound[a.Var]; ok || a.Const != nil {
		return f.bindRuleValues(r, p, i+1, bound, values, depth+1, prove)
	}
	var typ Type
	for j, param := range r.Decl.Params {
		if param.Name == a.Var {
			typ = r.VarTypes[j]
			break
		}
	}
	var cs candidates
	for _, v := range values {
		if v.expr == nil || !identical(v.expr.Type(), typ) {
			continue
		}
		next := map[string]argVal{}
		for name, v := range bound {
			next[name] = v
		}
		next[a.Var] = v
		if cs.take(f.bindRuleValues(r, p, i+1, next, values, depth+1, prove)) {
			return true, nil
		}
	}
	return cs.result()
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
			if ft.subject == v.key+ob.path && (known{inst: ft.inst, pred: ft.pred, args: ft.args}).proves(obligation{inst: ob.inst, pred: ob.pred, args: ob.args}) {
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
	cond = debugValue(cond)
	if f.witness != nil {
		if witnessStaged(cond) {
			return nil // An expansion removes the branch instead of testing it.
		}
		if f.witness.tainted(cond) {
			return []fact{{wild: true, or: [][]fact{}}}
		}
	}
	switch c := cond.(type) {
	case *Unary:
		if c.Op == syntax.Not {
			return f.conditionFacts(c.X, !positive)
		}
	case *Binary:
		switch {
		case c.Op == syntax.AndAnd && positive, c.Op == syntax.OrOr && !positive:
			return append(f.conditionFacts(c.X, positive), f.conditionFacts(c.Y, positive)...)
		case (c.Op == syntax.OrOr && positive) || (c.Op == syntax.AndAnd && !positive):
			// One side holds: only useful if both sides say something.
			l, r := f.conditionFacts(c.X, positive), f.conditionFacts(c.Y, positive)
			if len(l) == 0 || len(r) == 0 {
				return nil
			}
			return []fact{{or: append(alternatives(l), alternatives(r)...)}}
		}
		if cmp := f.comparison(c, positive); cmp != nil {
			return []fact{{comparison: cmp}}
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
		ft := fact{inst: c.Inst, pred: fn, subject: subject, value: f.argOf(args[0])}
		for _, a := range args[1:] {
			ft.args = append(ft.args, f.argOf(a))
		}
		return []fact{ft}
	case *CallValue:
		if p, ok := debugValue(c.Fun).(*VarRef); ok && positive && p.Var.Kind == VarParam && len(c.Args) == 1 {
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
// a parameter, binding, field, constant, or pure computed expression.
// Structural keys describe evaluation, not algebraic equivalence.
func (f *factChecker) key(x Expr) string {
	x = debugValue(x)
	if v := constOf(x); v != nil {
		return constKey(v)
	}
	switch x := x.(type) {
	case *VarRef:
		if v := joinedVar(x.Var); v != x.Var {
			return f.key(&VarRef{expr: x.expr, Var: v})
		}
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
			// A universal element path projects list facts, but does not
			// identify the individual value bound by a loop.
			if src := d.Source; src != nil && !strings.Contains(src.Path, ".[]") && (src.Member == nil || src.Path != "") {
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
	case *Call:
		if !stableCall(x) {
			return ""
		}
		parts := []string{fmt.Sprintf("%p", x.Func), requirementInstanceKey(x.Inst)}
		for _, a := range append(append([]Expr{}, x.Args...), x.Needs...) {
			k := f.computedArgKey(a)
			if stableLengthCall(x) {
				k = fmt.Sprintf("%q:%q", typeKey(a.Type()), f.key(a))
				if f.key(a) == "" {
					k = ""
				}
			}
			if k == "" {
				return ""
			}
			parts = append(parts, k)
		}
		return fmt.Sprintf("call:%q", parts)
	case *Binary:
		left, right := f.computedArgKey(x.X), f.computedArgKey(x.Y)
		if left != "" && right != "" {
			return fmt.Sprintf("binary:%v:%q:%q:%q", x.Op, typeKey(x.Type()), left, right)
		}
	case *Unary:
		if k := f.computedArgKey(x.X); k != "" {
			return fmt.Sprintf("unary:%v:%q:%q", x.Op, typeKey(x.Type()), k)
		}
	}
	return ""
}

// Mutable Go values and open callbacks cannot promise repeatable evaluation.
func stableCall(call *Call) bool {
	if call.Func.Effects&^EffOpen != 0 || !stableProjectionType(call.Type(), map[Type]bool{}) {
		return false
	}
	if stableLengthCall(call) {
		return true
	}
	for i, a := range append(append([]Expr{}, call.Args...), call.Needs...) {
		if !stableProjectionType(a.Type(), map[Type]bool{}) || i < len(call.Func.Params) && openArgEffects(call.Func.Params[i], a.Type()) != 0 {
			return false
		}
	}
	return true
}

// List length only observes the immutable container, never its elements.
func stableLengthCall(call *Call) bool {
	if !call.Func.Prelude || call.Func.Decl.Name != "length" || len(call.Args) != 1 || len(call.Needs) != 0 {
		return false
	}
	_, ok := call.Args[0].Type().(*List)
	return ok
}

// Unknown generic values may be opaque at instantiation. Function values and
// lazy sequences can hide mutable captures even when their effects are empty.
func stableProjectionType(t Type, seen map[Type]bool) bool {
	if seen[t] {
		return true
	}
	seen[t] = true
	if GoTypeOf(t) != nil || t == Scope || t == OwnedScope {
		return false
	}
	switch t := t.(type) {
	case *TypeParam, *FuncType, *Seq, *Resource:
		return false
	case *List:
		return stableProjectionType(t.Elem, seen)
	case *Map:
		return stableProjectionType(t.Key, seen) && stableProjectionType(t.Value, seen)
	case *Record:
		for _, field := range t.Fields {
			if !stableProjectionType(field.Type, seen) {
				return false
			}
		}
	case *Sealed:
		for _, variant := range t.Variants {
			for _, field := range variant.Fields {
				if !stableProjectionType(field.Type, seen) {
					return false
				}
			}
		}
	case *Union:
		for _, member := range t.Members {
			if !stableProjectionType(member, seen) {
				return false
			}
		}
	}
	return true
}

func (f *factChecker) computedArgKey(x Expr) string {
	if !stableProjectionType(x.Type(), map[Type]bool{}) {
		return ""
	}
	if k := f.argOf(x).key; k != "" {
		return fmt.Sprintf("%q:%q", typeKey(x.Type()), k)
	}
	return ""
}

// aliasKey is the key of x if binding x just gives a value another name.
func (f *factChecker) aliasKey(x Expr) string {
	x = debugValue(x)
	switch x.(type) {
	case *VarRef, *Select, *Call, *Binary, *Unary:
		return f.key(x)
	}
	return ""
}

func constKey(v constant.Value) string { return "c:" + v.ExactString() }

// constOf is the value of x if it is a constant, or nil.
func constOf(x Expr) constant.Value {
	x = debugValue(x)
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
		return x.Var.displayName()
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
	if x == f.candidate {
		return " (check the field values before construction, or use a checked constructor)"
	}
	if f.validates(x.Type()) {
		return " (prove this from the candidate's fields; its type invariant is unavailable during validation)"
	}
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
		if fn := call.Func; f.info.Mocked(fn) {
			return fmt.Sprintf(" (%s does not promise it in its signature, and a test mocks it, so only what it promises is known; promise it in the signature, or give the result a name and check it first)", fn.QualifiedName(f.from()))
		}
	}
	if ob.path != "" || name == "this value" {
		return " (give it a name and check it first)"
	}
	check := fmt.Sprintf("if (%s) { ... }", checkText(name, ob, f.from()))
	if ref, ok := x.(*VarRef); ok && f.ownsParam(ref.Var) && f.fn.MockOf == nil {
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
	if len(f.pending) == 0 || f.validatorInvalid {
		return
	}
	var queries []Query
	index := map[string]int{}
	for _, p := range f.pending {
		k := queryKey(p.query)
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
		start := f.diags.Len()
		f.diags.AddCode(f.pending[0].pos, "facts.error", "cannot run predicates at compile time: %v", err)
		if request := f.pending[0].request; request.File != "" {
			f.diags.DeriveContext(start, request)
		}
		return
	}
	for _, p := range f.pending {
		if !results[index[queryKey(p.query)]] {
			f.failedQuery(p)
		}
	}
}

func (f *factChecker) failedQuery(p pendingQuery) {
	// A query is evaluated after its function's walk has ended; retain
	// the same derive provenance for this delayed diagnostic.
	start := f.diags.Len()
	if p.request.File != "" {
		defer func() { f.diags.DeriveContext(start, p.request) }()
	}
	var returned constant.Value
	if len(p.query.Args) > 0 {
		returned = p.query.Args[0]
	} else if len(p.query.Values) > 0 {
		if v, ok := p.query.Values[0].(*Const); ok {
			returned = v.Value
		}
	}
	if p.query.Via != "" && p.query.Pred != nil && p.query.Subject == nil && returned != nil {
		f.diags.AddCode(p.pos, "facts.error", "%s, but %s can return %s, and %s is false", p.ob.requirement, p.query.Via, CArg{Const: returned}, p.query.Text(p.from))
		return
	}
	if p.query.Via != "" {
		f.diags.AddCode(p.pos, "facts.error", "%s, but for a value %s can return, %s is false", p.ob.requirement, p.query.Via, p.query.Text(p.from))
		return
	}
	f.diags.AddCode(p.pos, "facts.error", "%s, but %s is false", p.ob.requirement, p.query.Text(p.from))
}

func queryKey(q Query) string {
	if q.And != nil || q.Or != nil {
		parts := q.And
		prefix := "and"
		if q.Or != nil {
			parts = q.Or
			prefix = "or"
		}
		var keys []string
		for _, p := range parts {
			keys = append(keys, queryKey(p))
		}
		return prefix + strings.Join(keys, ";")
	}
	key := q.String() + "[" + argsKey(q.TypeArgs) + "]"
	for i, cons := range q.ArgFacts {
		for _, con := range cons {
			key += fmt.Sprintf("|argfact:%d:%p:%s:%s", i, con.Pred, con.Path, con.Text(nil))
		}
	}
	// Expression-valued arguments can contain distinct union members with the
	// same printed value. Keep their typed tree identities rather than merging
	// them by display text (including member types nested inside records/lists).
	for _, d := range q.Dicts {
		key += "|dict:" + requirementDictionaryKey(d, nil)
	}
	for _, v := range q.Values {
		key += fmt.Sprintf("|%p:%s", v, typeKey(v.Type()))
	}
	return key
}

func ruleConstant(a RuleArg) argVal {
	v := constArg(a.Const)
	if a.ConstType != nil {
		v.expr = &Const{expr: expr{typ: a.ConstType}, Value: a.Const}
		v.typ = a.ConstType
	}
	return v
}

func ruleValueFits(r *Rule, a RuleArg, v argVal, path string) bool {
	if path != "" {
		return true
	} // Legacy element paths retain their existing checks.
	typ := v.typ
	if v.expr != nil {
		typ = v.expr.Type()
	}
	if typ == nil {
		return true
	}
	if a.Const != nil {
		return a.ConstType == nil || identical(a.ConstType, typ)
	}
	for i, p := range r.Decl.Params {
		if p.Name == a.Var {
			return assignable(typ, r.VarTypes[i])
		}
	}
	return false
}

// Certainty reads the existing proof context, before learning any facts from
// the test itself. It does not eliminate operand or predicate evaluation.
func (f *factChecker) patternTestCertainty(x *Match, e env) {
	if !x.PatternTest || f.lintPatternProven == nil || len(x.Arms) == 0 {
		return
	}
	p := x.Arms[0].Pat
	if p.Kind == PatNever || len(missingCases([]*Pat{withoutPatternGuards(p)}, p.Type)) != 0 {
		return
	}
	saved := f.lintChecking
	f.lintChecking = true
	defer func() { f.lintChecking = saved }()
	for _, guard := range p.Guards() {
		proven, queries := f.proveCondition(guard, true, e, 0)
		if !proven || len(queries) > 0 {
			return
		}
	}
	f.lintPatternProven[x.TokenPos()] = true
}
func withoutPatternGuards(p *Pat) *Pat {
	if p == nil {
		return nil
	}
	copy := *p
	copy.Guard, copy.guard = nil, nil
	copy.Fields = nil
	for _, field := range p.Fields {
		copy.Fields = append(copy.Fields, &PatField{Name: field.Name, Pat: withoutPatternGuards(field.Pat)})
	}
	copy.Elems = nil
	for _, elem := range p.Elems {
		copy.Elems = append(copy.Elems, withoutPatternGuards(elem))
	}
	copy.Sub, copy.Rest = withoutPatternGuards(p.Sub), withoutPatternGuards(p.Rest)
	return &copy
}

// loop checks a loop without a source: every first and next value of a
// header name must have the facts the name declares (its invariant),
// and the condition is known in the body and the post clause.
func (f *factChecker) loop(x *For, e env) {
	invariant := func(c *Carry, value Expr, e env, which string) {
		for _, con := range c.Head.Invariant {
			f.oblige(value, con, f.ownParams(), e, fmt.Sprintf(which+" must be %s", f.carrySubject(c, x, con), con))
		}
	}
	for _, c := range x.Carries {
		if c.Init != nil {
			f.walk(c.Init, e)
			invariant(c, c.Init, e, "the first value of %s")
		} else {
			invariant(c, &VarRef{expr: expr{pos: x.Pos(), typ: c.Outer.Type}, Var: c.Outer}, e, "the value before the loop of %s")
		}
		// The value each iteration passes on, and each break's, has the
		// facts the name declares; the values in between need not.
		if f.carryMust == nil {
			f.carryMust = map[*Var]string{}
		}
		if c.Post == nil {
			f.carryMust[c.Latch] = "the next value of " + f.carrySubject(c, x, nil)
		}
		if c.After != nil {
			f.carryMust[c.After] = "at a break, the value of " + f.carrySubject(c, x, nil)
		}
	}
	inside := e
	if x.Cond != nil {
		f.walk(x.Cond, e)
		inside = e.with(f.conditionFacts(x.Cond, true)...)
	}
	f.walk(x.Body, inside)
	for _, c := range x.Carries {
		f.joinFacts(c.Latch, c.Head, inside)
		if c.Post != nil {
			f.walk(c.Post, inside)
			invariant(c, c.Post, inside, "the next value of %s")
		}
		if c.After != nil {
			f.joinFacts(c.After, c.Head, inside)
		}
	}
}

// carrySubject names a carried name in messages.
func (f *factChecker) carrySubject(c *Carry, x *For, con *Constraint) string {
	name := c.Head.displayName()
	if con != nil {
		name = pathPhrase(con.Path, name, c.Head.Type)
	}
	if c.Header() {
		return "loop variable " + name
	}
	return fmt.Sprintf("%s, which the loop at line %d carries,", name, x.Pos().Line)
}

// carryEdges checks the values a path passes on, in its facts e: one
// passed on to the next iteration or out of the loop must have the
// facts its name declares, and one passed on to a join is noted for
// the join if it has them.
func (f *factChecker) carryEdges(edges []*CarryEdge, e env) {
	for _, edge := range edges {
		from := &VarRef{expr: expr{pos: edge.From.Pos, typ: edge.From.Type}, Var: edge.From}
		if what, ok := f.carryMust[edge.To]; ok {
			for _, con := range edge.To.Invariant {
				f.oblige(from, con, f.ownParams(), e, fmt.Sprintf("%s must be %s", what, con))
			}
		}
		f.noteCarried(edge.To, edge.From, e)
	}
}

// noteCarried notes which facts the name of join to declares the value
// from, passed on to it, has in facts e.
func (f *factChecker) noteCarried(to, from *Var, e env) {
	if len(to.Invariant) == 0 {
		return
	}
	if f.carryFacts == nil {
		f.carryFacts = map[[2]*Var][]*Constraint{}
	}
	key := [2]*Var{to, from}
	// Several paths may pass the same value on: it has what it has on
	// every one.
	earlier, again := f.carryFacts[key]
	f.carryFacts[key] = nil
	saved := f.diags
	f.diags = &diag.List{}
	defer func() { f.diags = saved }()
	for _, con := range to.Invariant {
		if again && !slices.Contains(earlier, con) {
			continue
		}
		ob := f.obligationOf(con, f.ownParams(), "")
		if ok, pending := f.prove(&VarRef{expr: expr{pos: from.Pos, typ: from.Type}, Var: from}, ob, e, 0); ok && len(pending) == 0 {
			f.carryFacts[key] = append(f.carryFacts[key], con)
		}
	}
}

// joinFacts gives join v the facts its name declares that every value
// passed on to it has. prior is the value no edge passes on, which is
// checked in facts e.
func (f *factChecker) joinFacts(v, prior *Var, e env) {
	if len(v.Invariant) == 0 {
		return
	}
	var known []*Constraint
	for i, con := range v.Invariant {
		all := true
		for _, in := range v.Joins {
			if in == prior {
				if _, ok := f.carryFacts[[2]*Var{v, in}]; !ok {
					f.noteCarried(v, in, e)
				}
			}
			found := false
			for _, c := range f.carryFacts[[2]*Var{v, in}] {
				found = found || c == con
			}
			all = all && found
		}
		if all {
			known = append(known, v.Invariant[i])
		}
	}
	if f.joinKnown == nil {
		f.joinKnown = map[*Var][]*Constraint{}
	}
	f.joinKnown[v] = known
}

// joins gives the joins after a statement, in facts e, their facts.
func (f *factChecker) joins(js []*Join, e env) {
	for _, j := range js {
		f.joinFacts(j.Var, j.Prior, e)
	}
}

// joinedVar is the value a carried name's join always has, when only
// one path reaches it (a loop's latch when the body never rebinds the
// name), or v.
func joinedVar(v *Var) *Var {
	for v.Kind == VarJoin && len(v.Joins) == 1 {
		v = v.Joins[0]
	}
	return v
}

// witnesses checks definition witnesses (see derive_witness.go). Their
// other diagnostics and predicate evaluations are discarded: an unused
// definition never starts the evaluator.
func (f *factChecker) witnesses(fns []*Func) {
	diags, pending := f.diags, f.pending
	defer func() { f.diags, f.pending, f.witness, f.witnessDiags = diags, pending, nil, nil }()
	for _, fn := range fns {
		f.witnessFunction(fn, diags)
	}
	// An expansion repeats a definition's failure at its request; the
	// definition's own diagnostic is the one to fix. An expansion names
	// a specialized helper differently, so the callee is not compared.
	requirement := func(message string) string {
		_, rest, _ := strings.Cut(message, " ")
		return rest
	}
	reported := map[string]bool{}
	for _, d := range diags.Sorted() {
		if d.Code == "facts.error" {
			reported[requirement(d.Msg)+" (derive template at "+d.Pos.String()+")"] = true
		}
	}
	diags.Rewrite(0, func(d *diag.Diagnostic) bool { return d.Code != "facts.error" || !reported[requirement(d.Msg)] })
}

// A witness's Invalid types can reach code written for checked programs. A
// witness this pass cannot walk reports nothing, like one that fails to check.
func (f *factChecker) witnessFunction(fn *Func, diags *diag.List) {
	start := diags.Len()
	defer func() {
		if recover() != nil {
			diags.Truncate(start)
		}
	}()
	f.witness, f.witnessDiags, f.diags = newDeriveWitnessTaint(fn), diags, &diag.List{}
	f.fn, f.collect, f.active = nil, nil, map[string]bool{}
	if fn.Requires != nil {
		f.withFunction(fn, func() { f.walk(fn.Requires, env{}) })
	}
	f.function(fn)
}

func (f *factChecker) witnessOblige(x Expr, ob obligation, e env) {
	if e.wild || f.witness.tainted(x) || f.witness.obligation(ob) {
		return
	}
	if ok, _ := f.prove(x, ob, e, 0); !ok {
		f.witnessDiags.AddCode(x.Pos(), "facts.error", "%s, but that is not proven for %s%s", ob.requirement, f.describe(x), f.hint(x, ob))
	}
}
