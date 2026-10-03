package check

import (
	"go/constant"
	"go/token"

	"github.com/GiGurra/bork/internal/syntax"
)

// Rule is a checked inference rule: when facts matching Premises hold
// (and the Conditions are established), the Conclusions hold too.
type Rule struct {
	Decl     *syntax.RuleDecl
	Pkg      *Package
	VarTypes []Type // the types of Decl.Params
	Premises []*RuleAtom
	// Conditions are the premises that compute with the variables'
	// values, as typed expressions. They are evaluated on constants or
	// matched against branch comparisons; conditions holds
	// them as written.
	Conditions  []Expr
	conditions  []syntax.Expr
	Conclusions []*RuleAtom
	// Vars are the rule's variables. PremiseExprs and ConclusionExprs
	// are its premises and conclusions as typed expressions, which test
	// mode runs, and ConclusionTexts the conclusions as written.
	Vars            []*Var
	PremiseExprs    []Expr
	ConclusionExprs []Expr
	ConclusionTexts []string
}

// RuleAtom is a predicate applied to rule variables and constants.
type RuleAtom struct {
	Pred *Func
	Args []RuleArg // the subject first
}

// RuleArg is a rule variable, or a constant.
type RuleArg struct {
	Var   string
	Const constant.Value
}

func (c *checker) checkRules(files []*syntax.File) {
	names := map[string]bool{}
	for _, f := range files {
		c.inFile(f)
		for _, rd := range f.Rules {
			if names[f.Package+"."+rd.Name] {
				c.errorf(rd.Pos, "rule %s is declared twice", rd.Name)
				continue
			}
			names[f.Package+"."+rd.Name] = true
			if r := c.checkRule(rd); r != nil {
				c.info.Rules = append(c.info.Rules, r)
			}
		}
	}
}

func (c *checker) checkRule(rd *syntax.RuleDecl) *Rule {
	c.fn = nil
	c.scopes = []map[string]*local{{}}
	vars := map[string]bool{}
	var types []Type
	for _, p := range rd.Params {
		t := c.resolveType(p.Type)
		types = append(types, t)
		if vars[p.Name] {
			c.errorf(p.Pos, "variable %s is declared twice", p.Name)
		}
		vars[p.Name] = true
		c.scopes[0][p.Name] = &local{typ: t, decl: p, used: true}
	}
	r := &Rule{Decl: rd, Pkg: c.pkg, VarTypes: types}
	ok := true
	bound := map[string]bool{}
	atom := func(x syntax.Expr, what string) *RuleAtom {
		call, isCall := x.(*syntax.Call)
		fn := c.info.callFuncs[call]
		if !isCall || fn == nil || !fn.Decl.IsPred {
			c.errorf(x.Position(), "a rule's %s must be predicate calls, as in positive(x)", what)
			ok = false
			return nil
		}
		a := &RuleAtom{Pred: fn}
		for i, arg := range c.info.args(call) {
			if id, isID := arg.(*syntax.Ident); isID && vars[id.Name] {
				a.Args = append(a.Args, RuleArg{Var: id.Name})
				bound[id.Name] = true
				continue
			}
			if v := c.info.constantOf(arg); v != nil && i > 0 {
				a.Args = append(a.Args, RuleArg{Const: v})
				continue
			}
			if i == 0 {
				c.errorf(arg.Position(), "the first argument to %s must be one of the rule's variables (rules are about any value, not a particular one)", fn.Decl.Name)
			} else {
				c.errorf(arg.Position(), "arguments to %s in a rule must be the rule's variables or constants", fn.Decl.Name)
			}
			ok = false
			return nil
		}
		return a
	}
	for _, p := range rd.Premises {
		if t := c.expr(p); t != Bool && t != Invalid {
			c.errorf(p.Position(), "a premise must be a Bool, found %s", t)
			ok = false
			continue
		}
		if call, isCall := p.(*syntax.Call); isCall && c.info.callFuncs[call] != nil && c.info.callFuncs[call].Decl.IsPred {
			if a := atom(p, "premises"); a != nil {
				r.Premises = append(r.Premises, a)
			}
			continue
		}
		if !c.conditionOnly(p, vars) {
			ok = false
			continue
		}
		r.conditions = append(r.conditions, p)
	}
	for _, x := range rd.Conclusions {
		if t := c.expr(x); t == Invalid {
			ok = false
			continue
		}
		if a := atom(x, "conclusions"); a != nil {
			r.Conclusions = append(r.Conclusions, a)
		}
	}
	for _, p := range rd.Params {
		if !bound[p.Name] {
			c.errorf(p.Pos, "variable %s is not used in any premise or conclusion", p.Name)
			ok = false
		}
	}
	c.scopes = nil
	if !ok {
		return nil
	}
	return r
}

// conditionOnly checks that a rule condition only computes with the
// rule's variables and constants, so it can be computed in the
// compiler once the variables are known.
func (c *checker) conditionOnly(x syntax.Expr, vars map[string]bool) bool {
	if c.info.constantOf(x) != nil {
		return true
	}
	switch x := x.(type) {
	case *syntax.Ident:
		if vars[x.Name] {
			return true
		}
	case *syntax.Unary:
		return c.conditionOnly(x.X, vars)
	case *syntax.Binary:
		return c.conditionOnly(x.X, vars) && c.conditionOnly(x.Y, vars)
	}
	c.errorf(x.Position(), "a rule condition may only use the rule's variables, constants, and operators")
	return false
}

// evalCondition computes a rule condition with the variables bound to
// constants. It returns nil if that is not possible.
func evalCondition(x Expr, vars map[string]constant.Value) constant.Value {
	switch x := x.(type) {
	case *Const:
		return conditionConstant(x.Value, x.Type())
	case *VarRef:
		return conditionConstant(vars[x.Var.Name], x.Type())
	case *Unary:
		v := evalCondition(x.X, vars)
		if v == nil {
			return nil
		}
		if x.Op == syntax.Not {
			return constant.UnaryOp(token.NOT, v, 0)
		}
		return constant.UnaryOp(token.SUB, v, 0)
	case *Binary:
		a, b := evalCondition(x.X, vars), evalCondition(x.Y, vars)
		if a == nil || b == nil || a.Kind() == constant.Unknown || b.Kind() == constant.Unknown {
			return nil
		}
		switch x.Op {
		case syntax.AndAnd:
			return constant.MakeBool(constant.BoolVal(a) && constant.BoolVal(b))
		case syntax.OrOr:
			return constant.MakeBool(constant.BoolVal(a) || constant.BoolVal(b))
		}
		if cmp, ok := compareOps[x.Op]; ok {
			return constant.MakeBool(constant.Compare(a, cmp, b))
		}
		if op, ok := constOps[x.Op]; ok {
			if (op == token.QUO || op == token.REM) && constant.Sign(b) == 0 {
				return nil
			}
			if op == token.QUO && a.Kind() == constant.Int && b.Kind() == constant.Int {
				op = token.QUO_ASSIGN
			}
			return constant.BinaryOp(a, op, b)
		}
	}
	return nil
}

// Floating constants retain their exact source value in the typed tree.
// Comparisons must use the precision of the runtime value instead.
func conditionConstant(v constant.Value, typ Type) constant.Value {
	if v == nil || v.Kind() == constant.Unknown {
		return nil
	}
	if !IsFloat(typ) {
		return v
	}
	var rounded constant.Value
	if typ == Float32 {
		n, _ := constant.Float32Val(v)
		rounded = constant.MakeFloat64(float64(n))
	} else {
		n, _ := constant.Float64Val(v)
		rounded = constant.MakeFloat64(n)
	}
	if rounded.Kind() == constant.Unknown {
		return nil
	}
	return rounded
}

var compareOps = map[syntax.Kind]token.Token{
	syntax.Eq: token.EQL, syntax.NotEq: token.NEQ,
	syntax.Lt: token.LSS, syntax.LtEq: token.LEQ, syntax.Gt: token.GTR, syntax.GtEq: token.GEQ,
}
