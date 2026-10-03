package check

import (
	"fmt"
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// Requirements are checked before bodies, including bodyless class signatures.
func (c *checker) checkFunctionRequirements() {
	for _, fn := range c.info.FuncOf {
		if fn.Decl.Requires == nil {
			continue
		}
		c.fn, c.pkg, c.inPrelude = fn, fn.Pkg, fn.Prelude
		c.useTypeParams(fn)
		if fn.ParamConstraints == nil {
			fn.ParamConstraints = make([][]*Constraint, len(fn.Params))
		}
		c.scopes = []map[string]*local{{}}
		for i, p := range fn.Decl.Params {
			c.scopes[0][p.Name] = &local{typ: fn.Params[i], decl: p, used: true}
		}
		x := fn.Decl.Requires
		if c.expr(x) != Bool {
			c.errorf(x.Position(), "function-level where must be Bool")
		}
		c.requirementSyntax(x)
	}
	c.fn, c.inPrelude = nil, false
	c.useTypeParams(nil)
}

func (c *checker) requirementSyntax(x syntax.Expr) {
	switch x := x.(type) {
	case *syntax.Binary:
		if x.Op == syntax.AndAnd || x.Op == syntax.OrOr {
			c.requirementSyntax(x.X)
			c.requirementSyntax(x.Y)
			return
		}
		if _, ok := compareOps[x.Op]; ok {
			c.requirementArg(x.X)
			c.requirementArg(x.Y)
			return
		}
	case *syntax.Call:
		fn := c.info.callFuncs[x]
		if fn == nil || !fn.Decl.IsPred || fn.Effects != 0 {
			c.errorf(x.Pos, "function-level where requires a pure predicate call")
		}
		for _, a := range x.Args {
			c.requirementArg(a)
		}
		return
	}
	c.errorf(x.Position(), "function-level where requires a predicate call or comparison")
}

func (c *checker) requirementArg(x syntax.Expr) {
	if constValue(x) != nil {
		return
	}
	switch x := x.(type) {
	case *syntax.Ident:
		for i, p := range c.fn.Decl.Params {
			if x.Name == p.Name {
				if containsOpaque(c.fn.Params[i], map[Type]bool{}) {
					c.errorf(x.Pos, "function requirements cannot apply to mutable Go values")
				}
				return
			}
		}
	case *syntax.Selector:
		c.requirementArg(x.X)
		return
	}
	c.errorf(x.Position(), "requirement arguments must be parameters, their fields, or constants")
}

// Equality is intentionally structural after parameter renaming. Signature
// type checking already substitutes the class's type parameter in the instance.
func requirementKey(x Expr, types map[*TypeParam]Type) string {
	switch x := debugValue(x).(type) {
	case nil:
		return ""
	case *Const:
		return typeKey(subst(x.Type(), types)) + ":" + x.Value.ExactString()
	case *VarRef:
		return fmt.Sprintf("p%d", x.Var.Index)
	case *Select:
		return requirementKey(x.X, types) + "." + x.Name
	case *Binary:
		return fmt.Sprintf("(%s %s %s)", requirementKey(x.X, types), x.Op, requirementKey(x.Y, types))
	case *Call:
		args := make([]string, len(x.Args))
		for i, a := range x.Args {
			args[i] = requirementKey(a, types)
		}
		var typeArgs []Type
		if x.Inst != nil {
			for _, t := range x.Inst.TypeArgs {
				typeArgs = append(typeArgs, subst(t, types))
			}
		}
		key := fmt.Sprintf("%p[%s](%s)", x.Func, argsKey(typeArgs), strings.Join(args, ","))
		if x.Inst != nil {
			for _, d := range x.Inst.Dicts {
				key += "|" + requirementDictionaryKey(d, types)
			}
		}
		return key
	}
	return "unsupported"
}

func (c *checker) checkRequirementContracts() {
	for _, ci := range c.info.ClassInstances {
		for i, fn := range ci.Methods {
			if i >= len(ci.Class.Methods) {
				continue
			}
			signature := ci.Class.Methods[i]
			if requirementKey(fn.Requires, nil) != requirementKey(signature.Requires, map[*TypeParam]Type{ci.Class.Param: ci.Type}) {
				c.errorf(fn.Decl.Pos, "method %s must declare the same function-level where requirements as class %s", fn.Decl.Name, ci.Class.Name)
			}
		}
	}
}

func (f *factChecker) entryFacts(fn *Func) env {
	if fn.Requires == nil {
		return env{}
	}
	return env{}.with(f.conditionFacts(fn.Requires, true)...)
}

func (f *factChecker) callRequirements(call *Call, e env) {
	if call.Func.Requires == nil {
		return
	}
	bound := map[*Var]argVal{}
	for i, p := range call.Func.ParamVars {
		if i < len(call.Args) {
			bound[p] = f.argOf(call.Args[i])
		}
	}
	goal := substituteExpr(call.Func.Requires, bound)
	if call.Inst != nil {
		goal = substituteRequirementTypes(goal, bindParams(call.Func.TypeParams, call.Inst.TypeArgs), call.Inst.Dicts)
	}
	f.walk(goal, e)
	text := requirementText(goal, f.from())
	requirement := call.Func.QualifiedName(f.from()) + " requires " + text
	ok, pending := f.proveCondition(goal, true, e, 0)
	if !ok {
		f.diags.AddCode(call.Pos(), "facts.error", "%s, but that is not proven (check it first with if (%s) { ... }, or declare a function-level where clause)", requirement, text)
		return
	}
	for _, q := range pending {
		f.pending = append(f.pending, pendingQuery{query: q, pos: call.Pos(), ob: obligation{requirement: requirement, con: text}, from: f.from()})
	}
}

func requirementText(x Expr, from *Package) string {
	switch x := debugValue(x).(type) {
	case nil:
		return "unknown requirement"
	case *Const:
		text := CArg{Const: x.Value}.String()
		if IsNumeric(x.Type()) && x.Type() != Int && x.Type() != Float {
			return "to" + TypeText(x.Type(), from) + "(" + text + ")"
		}
		return text
	case *VarRef:
		return x.Var.displayName()
	case *Select:
		return requirementText(x.X, from) + "." + x.Name
	case *Binary:
		op := strings.Trim(x.Op.String(), "'")
		if x.Op == syntax.AndAnd {
			op = "and"
		}
		if x.Op == syntax.OrOr {
			op = "or"
		}
		return "(" + requirementText(x.X, from) + " " + op + " " + requirementText(x.Y, from) + ")"
	case *Call:
		args := make([]string, len(x.Args))
		for i, a := range x.Args {
			args[i] = requirementText(a, from)
		}
		return x.Func.QualifiedName(from) + "(" + strings.Join(args, ", ") + ")"
	}
	return "this value"
}

// Only closed clauses are decided here. For a disjunction the entire group
// must be closed, so a false alternative does not reject a satisfiable contract.
func (f *factChecker) constantRequirements(x Expr) {
	if b, ok := x.(*Binary); ok && b.Op == syntax.AndAnd {
		f.constantRequirements(b.X)
		f.constantRequirements(b.Y)
		return
	}
	var closed func(Expr) bool
	closed = func(x Expr) bool {
		switch x := debugValue(x).(type) {
		case *Const:
			return true
		case *Binary:
			return closed(x.X) && closed(x.Y)
		case *Call:
			if x.Inst != nil {
				for _, t := range x.Inst.TypeArgs {
					if hasTypeParam(t) {
						return false
					}
				}
			}
			for _, a := range x.Args {
				if !f.closed(a) {
					return false
				}
			}
			return true
		}
		return false
	}
	if !closed(x) {
		return
	}
	text := requirementText(x, f.from())
	ok, pending := f.proveCondition(x, true, env{}, 0)
	if !ok {
		f.diags.AddCode(x.Pos(), "facts.error", "%s has an impossible function-level requirement: %s", f.fn.QualifiedName(f.from()), text)
		return
	}
	for _, q := range pending {
		f.pending = append(f.pending, pendingQuery{query: q, pos: x.Pos(), ob: obligation{requirement: f.fn.QualifiedName(f.from()) + " requires " + text}, from: f.from()})
	}
}

// Keep predicate instantiations when replacing inputs in a requirement.
func substituteRequirementTypes(x Expr, bound map[*TypeParam]Type, available []*Dict) Expr {
	switch x := debugValue(x).(type) {
	case *Call:
		y := *x
		y.Args = make([]Expr, len(x.Args))
		for i, a := range x.Args {
			y.Args[i] = substituteRequirementTypes(a, bound, available)
		}
		if x.Inst != nil {
			inst := *x.Inst
			inst.TypeArgs = make([]Type, len(x.Inst.TypeArgs))
			for i, t := range x.Inst.TypeArgs {
				inst.TypeArgs[i] = subst(t, bound)
			}
			inst.Params = make([]Type, len(x.Inst.Params))
			for i, t := range x.Inst.Params {
				inst.Params[i] = subst(t, bound)
			}
			inst.Result = subst(x.Inst.Result, bound)
			inst.Dicts = make([]*Dict, len(x.Inst.Dicts))
			for i, d := range x.Inst.Dicts {
				inst.Dicts[i] = substituteRequirementDict(d, bound, available)
			}
			y.Inst = &inst
		}
		return &y
	case *Binary:
		y := *x
		y.X = substituteRequirementTypes(x.X, bound, available)
		y.Y = substituteRequirementTypes(x.Y, bound, available)
		return &y
	case *Select:
		y := *x
		y.X = substituteRequirementTypes(x.X, bound, available)
		y.typ = subst(x.Type(), bound)
		return &y
	case *Block:
		y := *x
		y.Tail = substituteRequirementTypes(x.Tail, bound, available)
		return &y
	}
	return x
}

func inferredPredicate(pred *Func, subject Expr, args []argVal) *Instance {
	in := newInference(pred)
	in.unify(pred.Params[0], subject.Type())
	for i, a := range args {
		if a.expr == nil {
			return nil
		}
		in.unify(pred.Params[i+1], a.expr.Type())
	}
	if len(in.unsolved()) > 0 {
		return nil
	}
	if !assignable(subject.Type(), in.subst(pred.Params[0])) {
		return nil
	}
	for i, a := range args {
		if !assignable(a.expr.Type(), in.subst(pred.Params[i+1])) {
			return nil
		}
	}
	return in.instance()
}

func substituteRequirementDict(d *Dict, bound map[*TypeParam]Type, available []*Dict) *Dict {
	if d.Param != nil {
		typ := subst(d.Param, bound)
		for _, actual := range available {
			if actual.Class == d.Class && identical(actual.Type, typ) {
				return actual
			}
		}
		if param, ok := typ.(*TypeParam); ok {
			copy := *d
			copy.Param = param
			copy.Type = param
			return &copy
		}
		// A missing concrete bound dictionary cannot be silently selected in a
		// different scope; type checking normally guarantees it is supplied.
		return d
	}
	copy := *d
	copy.Type = subst(d.Type, bound)
	copy.TypeArgs = make([]Type, len(d.TypeArgs))
	for i, t := range d.TypeArgs {
		copy.TypeArgs[i] = subst(t, bound)
	}
	copy.Args = make([]*Dict, len(d.Args))
	for i, a := range d.Args {
		copy.Args[i] = substituteRequirementDict(a, bound, available)
	}
	return &copy
}

func requirementDictionaryKey(d *Dict, bound map[*TypeParam]Type) string {
	if d == nil {
		return "nil"
	}
	key := fmt.Sprintf("%p:%p:%t:%s", d.Class, d.Inst, d.Builtin, typeKey(subst(d.Type, bound)))
	if d.Param != nil {
		key += "param:" + typeKey(subst(d.Param, bound))
	}
	for _, t := range d.TypeArgs {
		key += "[" + typeKey(subst(t, bound)) + "]"
	}
	for _, a := range d.Args {
		key += "{" + requirementDictionaryKey(a, bound) + "}"
	}
	return key
}

func requirementInstanceKey(inst *Instance) string {
	if inst == nil {
		return ""
	}
	key := argsKey(inst.TypeArgs)
	for _, d := range inst.Dicts {
		key += "|" + requirementDictionaryKey(d, nil)
	}
	return key
}

func requirementMentionsParam(x syntax.Expr, param string) bool {
	switch x := x.(type) {
	case *syntax.Ident:
		return x.Name == param
	case *syntax.Selector:
		return requirementMentionsParam(x.X, param)
	case *syntax.Binary:
		return requirementMentionsParam(x.X, param) || requirementMentionsParam(x.Y, param)
	case *syntax.Unary:
		return requirementMentionsParam(x.X, param)
	case *syntax.Call:
		for _, a := range x.Args {
			if requirementMentionsParam(a, param) {
				return true
			}
		}
	}
	return false
}
