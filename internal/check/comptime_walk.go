package check

import (
	"fmt"
	"reflect"

	"github.com/GiGurra/bork/internal/diag"
)

// WalkComptime visits typed recipe children, without following declaration
// references or expanding closed captures. Returning false skips a subtree.
func WalkComptime(x Expr, visit func(Expr) bool) {
	if x == nil || reflect.ValueOf(x).IsNil() || !visit(x) {
		return
	}
	walk := func(x Expr) { WalkComptime(x, visit) }
	switch x := x.(type) {
	case *Comptime:
		walk(x.Body)
	case *Call:
		for _, arg := range x.Needs {
			walk(arg)
		}
		for _, arg := range x.EvaluationArgs() {
			walk(arg)
		}
	case *CallBuiltin:
		for _, arg := range x.Args {
			walk(arg)
		}
	case *CallValue:
		walk(x.Fun)
		for _, arg := range x.Args {
			walk(arg)
		}
	case *FuncRef:
		for _, arg := range x.Needs {
			walk(arg)
		}
	case *Unary:
		walk(x.X)
	case *Binary:
		walk(x.X)
		walk(x.Y)
	case *Interp:
		for _, arg := range x.Exprs {
			walk(arg)
		}
	case *Lambda:
		walk(x.Body)
	case *ListLit:
		for _, arg := range x.Elems {
			walk(arg)
		}
	case *MapLit:
		for i, arg := range x.Keys {
			walk(arg)
			walk(x.Values[i])
		}
	case *If:
		walk(x.Cond)
		walk(x.Then)
		walk(x.Else)
	case *Block:
		for _, stmt := range x.Stmts {
			switch stmt := stmt.(type) {
			case *Let:
				walk(stmt.Value)
			case *ExprStmt:
				walk(stmt.X)
			case *Trust:
				walk(stmt.Call)
			case *Mock:
				walk(stmt.Func.Body)
			}
		}
		walk(x.Tail)
	case *ScopeBlock:
		for _, arg := range x.Policies {
			walk(arg)
		}
		walk(x.Body)
	case *Return:
		walk(x.Value)
	case *Select:
		walk(x.X)
	case *RecordLit:
		for _, field := range x.Fields {
			walk(field.Value)
		}
	case *Copy:
		walk(x.X)
		for _, update := range x.Updates {
			walk(update.Value)
		}
	case *Match:
		walk(x.X)
		for _, arm := range x.Arms {
			for _, guard := range arm.Pat.Guards() {
				walk(guard)
			}
			walk(arm.Body)
		}
	case *Try:
		walk(x.X)
	case *SeqCall:
		for _, arg := range x.Args {
			walk(arg)
		}
	case *Generate:
		walk(x.Body)
	case *Yield:
		walk(x.Value)
	case *For:
		walk(x.Items)
		walk(x.Body)
	}
}

// ComptimeRecipe checks internal obligations without inheriting runtime guards,
// parameter facts or contextual constraints on the eventual computed result.
func ComptimeRecipe(node *Comptime, info *Info, diags *diag.List, eval Evaluator, helpers []*Func) {
	// Captured declarations have not completed the whole-program facts pass.
	// Do not assume their annotated promises while verifying the recipe.
	saved := map[*Let][]*Constraint{}
	var clear func(Expr)
	clear = func(x Expr) {
		WalkComptime(x, func(x Expr) bool {
			if node, ok := x.(*Comptime); ok {
				if node.Value != nil {
					clear(node.Value)
				}
				return false
			}
			if ref, ok := x.(*VarRef); ok && ref.Var.Let != nil {
				let := ref.Var.Let
				if _, ok := saved[let]; !ok {
					saved[let] = let.Constraints
					let.Constraints = nil
					clear(let.Value)
				}
			}
			return true
		})
	}
	for _, capture := range node.Captures {
		if capture.Let != nil {
			let := capture.Let
			if _, ok := saved[let]; ok {
				continue
			}
			saved[let] = let.Constraints
			let.Constraints = nil
			clear(let.Value)
		}
	}
	defer func() {
		for let, constraints := range saved {
			let.Constraints = constraints
		}
	}()
	f := &factChecker{info: info, diags: diags, paths: map[*Func][]branch{}, active: map[string]bool{}, params: map[*Var]*VarRef{}, predParams: map[*Var]*Func{}, lambdaArgs: map[*Var]lambdaArg{}}
	f.validators = validationContexts(info)
	f.validatorRequirements()
	f.comptimeRequirements(helpers)
	for _, fn := range helpers {
		if fn.Body != nil {
			f.function(fn)
		}
	}
	owner := *node.Owner
	decl := *owner.Decl
	decl.Name = "comptime"
	owner.Decl = &decl
	owner.ParamVars = nil
	owner.ParamConstraints = nil
	owner.Requires = nil
	owner.Needs = nil
	owner.Result = node.Type()
	owner.ResultConstraints = nil
	owner.Body = node.Body
	for _, capture := range node.Captures {
		f.fn = &owner
		f.walk(capture.Let.Value, env{})
		f.fn = nil
	}
	f.function(&owner)
	f.evaluate(eval)
}

// ComptimeResult validates nominal and field constraints on decoded data before
// dependent recipes can assume them. Contextual result constraints are checked
// by the enclosing facts pass, without executing the recipe again.
func ComptimeResult(node *Comptime, info *Info, diags *diag.List, eval Evaluator) {
	result := *node
	result.Captures = nil
	result.Body = &Block{expr: expr{pos: node.Pos(), typ: node.Type()}, Tail: node.Value}
	ComptimeRecipe(&result, info, diags, eval, nil)
}

// ComptimeProof checks predicate implementations and their helpers before a
// compile-time proof executes them. Their own declared parameter contracts are
// available, independently of the calling recipe's runtime context.
func ComptimeProof(functions []*Func, info *Info, diags *diag.List, eval Evaluator) {
	f := &factChecker{info: info, diags: diags, paths: map[*Func][]branch{}, active: map[string]bool{}, params: map[*Var]*VarRef{}, predParams: map[*Var]*Func{}, lambdaArgs: map[*Var]lambdaArg{}}
	f.validators = validationContexts(info)
	f.validatorRequirements()
	f.comptimeRequirements(functions)
	for _, fn := range functions {
		if fn.Body != nil {
			f.function(fn)
		}
	}
	f.evaluate(eval)
}

// ComptimeQuery validates the actual arguments of a proof invocation before
// batching downstream predicates. A predicate's own contract is a prerequisite
// even when the query was produced by a function-level where clause.
func ComptimeQuery(q Query, info *Info, diags *diag.List, eval Evaluator) {
	params := q.Params
	if params == nil {
		params = q.Pred.Params
	}
	args := append([]Expr{}, q.Values...)
	if q.Subject != nil {
		args = append(args, q.Subject)
	}
	for _, value := range q.Args {
		args = append(args, &Const{expr: expr{pos: q.Pred.Decl.Pos, typ: params[len(args)]}, Value: value})
	}
	call := &Call{expr: expr{pos: q.Pred.Decl.Pos, typ: Bool}, Func: q.Pred, Args: args, Inst: &Instance{Func: q.Pred, Params: params, Result: Bool, TypeArgs: q.TypeArgs, Dicts: q.Dicts, ArgFacts: q.ArgFacts}}
	f := &factChecker{info: info, diags: diags, fn: q.Pred, paths: map[*Func][]branch{}, active: map[string]bool{}, params: map[*Var]*VarRef{}, predParams: map[*Var]*Func{}, lambdaArgs: map[*Var]lambdaArg{}}
	f.validators = validationContexts(info)
	f.callObligations(call, env{})
	f.evaluate(eval)
}

// ComptimeQueryKey identifies active proof invocations by semantic arguments,
// rather than request-local expression addresses, for recursive proof detection.
func ComptimeQueryKey(q Query) string {
	values := q.Values
	if values != nil {
		q.Values = []Expr{}
	}
	key := fmt.Sprintf("%p:%s", q.Pred, queryKey(q))
	for _, value := range values {
		key += ":" + typeKey(value.Type())
	}
	if q.Subject != nil {
		key += ":" + typeKey(q.Subject.Type())
	}
	return key
}

func (f *factChecker) comptimeRequirements(functions []*Func) {
	for _, fn := range functions {
		if fn.Requires != nil {
			f.fn = fn
			f.walk(fn.Requires, env{})
			f.constantRequirements(fn.Requires)
		}
	}
	f.fn = nil
}

// ComptimeLambda gives generation an independent return boundary.
func ComptimeLambda(node *Comptime) *Lambda {
	return &Lambda{expr: expr{pos: node.Pos(), typ: &FuncType{Result: node.Type()}}, Body: node.Body}
}

// ComptimeHelpers inventories implementations used by a recipe, including
// dictionary methods and captures. The driver separately evaluates dependencies.
func ComptimeHelpers(node *Comptime, dictionaries ...*Dict) []*Func {
	var out []*Func
	seen := map[*Func]bool{}
	var visit func(*Func)
	var dict func(*Dict)
	dict = func(d *Dict) {
		if d == nil {
			return
		}
		if d.Inst != nil {
			for _, fn := range d.Inst.Methods {
				visit(fn)
			}
		}
		for _, arg := range d.Args {
			dict(arg)
		}
	}
	inspect := func(x Expr) bool {
		switch x := x.(type) {
		case *Comptime:
			return false
		case *Call:
			visit(x.Func)
			for _, d := range x.Inst.Dicts {
				dict(d)
			}
		case *FuncRef:
			visit(x.Inst.Func)
			for _, d := range x.Inst.Dicts {
				dict(d)
			}
		}
		return true
	}
	visit = func(fn *Func) {
		if fn == nil || seen[fn] {
			return
		}
		seen[fn] = true
		out = append(out, fn)
		WalkComptime(fn.Requires, inspect)
		WalkComptime(fn.Body, inspect)
		// Unsafe Go's direct package helper references are resolved by generation;
		// their declarations still need checked contracts before execution.
		if fn.Decl.IsGo() {
			for _, callee := range fn.Calls {
				visit(callee)
			}
		}
	}
	for _, dictionary := range dictionaries {
		dict(dictionary)
	}
	WalkComptime(node.Body, inspect)
	for _, capture := range node.Captures {
		WalkComptime(capture.Let.Value, inspect)
	}
	return out
}
