package check

import (
	"go/constant"

	"github.com/GiGurra/bork/internal/syntax"
)

// comparison is a guard over stable values. Its polarity is retained:
// negating an ordered float comparison cannot reverse it in the presence of NaN.
type comparison struct {
	op          syntax.Kind
	left, right argVal
	positive    bool
}

func (f *factChecker) comparison(x *Binary, positive bool) *comparison {
	if _, ok := compareOps[x.Op]; !ok {
		return nil
	}
	left, right := f.argOf(x.X), f.argOf(x.Y)
	if left.key == "" || right.key == "" {
		return nil
	}
	op := x.Op
	if !positive && (!IsFloat(x.X.Type()) || op == syntax.Eq || op == syntax.NotEq) {
		op = oppositeComparison(op)
		positive = true
	}
	switch op {
	case syntax.Gt:
		op, left, right = syntax.Lt, right, left
	case syntax.GtEq:
		op, left, right = syntax.LtEq, right, left
	}
	if (op == syntax.Eq || op == syntax.NotEq) && left.key > right.key {
		left, right = right, left
	}
	return &comparison{op: op, left: left, right: right, positive: positive}
}

func oppositeComparison(op syntax.Kind) syntax.Kind {
	switch op {
	case syntax.Eq:
		return syntax.NotEq
	case syntax.NotEq:
		return syntax.Eq
	case syntax.Lt:
		return syntax.GtEq
	case syntax.LtEq:
		return syntax.Gt
	case syntax.Gt:
		return syntax.LtEq
	case syntax.GtEq:
		return syntax.Lt
	}
	return op
}

// substituteExpr copies just the simple expressions supported by predicate
// unfolding. It never changes the shared typed tree.
func substituteExpr(x Expr, bound map[*Var]argVal) Expr {
	switch x := x.(type) {
	case *Const:
		return x
	case *VarRef:
		if v, ok := bound[x.Var]; ok {
			if v.expr != nil {
				return v.expr
			}
			if v.value != nil {
				return &Const{expr: x.expr, Value: v.value}
			}
			return nil
		}
		return x
	case *Unary:
		if x.Op != syntax.Not {
			return nil
		}
		y := *x
		y.X = substituteExpr(x.X, bound)
		if y.X == nil {
			return nil
		}
		return &y
	case *Binary:
		if _, comparison := compareOps[x.Op]; !comparison && x.Op != syntax.AndAnd && x.Op != syntax.OrOr {
			return nil
		}
		y := *x
		y.X, y.Y = substituteExpr(x.X, bound), substituteExpr(x.Y, bound)
		if y.X == nil || y.Y == nil {
			return nil
		}
		return &y
	case *Select:
		y := *x
		y.X = substituteExpr(x.X, bound)
		if y.X == nil {
			return nil
		}
		return &y
	case *Call:
		if !x.Func.Decl.IsPred {
			return nil
		}
		y := *x
		y.Args = make([]Expr, len(x.Args))
		for i, a := range x.Args {
			y.Args[i] = substituteExpr(a, bound)
			if y.Args[i] == nil {
				return nil
			}
		}
		return &y
	case *Block:
		if len(x.Stmts) == 0 && x.Tail != nil {
			return substituteExpr(x.Tail, bound)
		}
	}
	return nil
}

func (f *factChecker) unfold(v argVal, ob obligation, e env, depth int) (bool, []Query) {
	if depth > maxDepth || ob.path != "" || ob.pred.Body == nil || len(ob.pred.ParamVars) != len(ob.args)+1 {
		return false, nil
	}
	bound := map[*Var]argVal{ob.pred.ParamVars[0]: v}
	for i, a := range ob.args {
		bound[ob.pred.ParamVars[i+1]] = a
	}
	body := substituteExpr(ob.pred.Body, bound)
	if body == nil {
		return false, nil
	}
	goal := "unfold " + factKey(fact{pred: ob.pred, subject: v.key, args: ob.args})
	if f.active[goal] {
		return false, nil
	}
	f.active[goal] = true
	ok, pending := f.proveCondition(body, true, e, depth+1)
	delete(f.active, goal)
	return ok, pending
}

// proveCondition recognizes simple predicate bodies and instantiated rule
// conditions using the same branch facts that prove ordinary obligations.
func (f *factChecker) proveCondition(x Expr, positive bool, e env, depth int) (bool, []Query) {
	if depth > maxDepth || x == nil {
		return false, nil
	}
	if v := evalCondition(x, nil); v != nil && v.Kind() == constant.Bool {
		return constant.BoolVal(v) == positive, nil
	}
	switch x := x.(type) {
	case *Unary:
		if x.Op == syntax.Not {
			return f.proveCondition(x.X, !positive, e, depth+1)
		}
	case *Binary:
		if x.Op == syntax.AndAnd || x.Op == syntax.OrOr {
			all := (x.Op == syntax.AndAnd) == positive
			a, qa := f.proveCondition(x.X, positive, e, depth+1)
			b, qb := f.proveCondition(x.Y, positive, e, depth+1)
			if all {
				return a && b, append(qa, qb...)
			}
			if a && len(qa) == 0 || b && len(qb) == 0 {
				return true, nil
			}
			if a && b {
				return true, []Query{{Or: []Query{allOf(qa), allOf(qb)}}}
			}
			if a {
				return true, qa
			}
			return b, qb
		}
		want := f.comparison(x, positive)
		if want == nil {
			return false, nil
		}
		facts := append([]fact{}, e.facts...)
		values := []argVal{want.left, want.right}
		if f.fn != nil {
			for _, p := range f.fn.ParamVars {
				values = append(values, f.argOf(f.paramRef(p)))
			}
		}
		for _, v := range values {
			if v.expr == nil {
				continue
			}
			for _, k := range f.declared(v.expr, e, depth+1) {
				if k.path == "" && k.pred != nil {
					facts = append(facts, fact{pred: k.pred, subject: v.key, args: k.args, value: v})
				}
			}
		}
		return f.comparisonKnown(want, facts, depth+1), nil

	case *Call:
		if positive && x.Func.Decl.IsPred && len(x.Args) > 0 {
			ob := obligation{pred: x.Func}
			for _, a := range x.Args[1:] {
				ob.args = append(ob.args, f.argOf(a))
			}
			return f.prove(x.Args[0], ob, e, depth+1)
		}
	}
	return false, nil
}

// Predicate guards and declared requirements may expose comparisons too.
// Expand only simple bodies and keep OR alternatives separate.
func (f *factChecker) comparisonKnown(want *comparison, facts []fact, depth int) bool {
	if depth > maxDepth {
		return false
	}
	for _, ft := range facts {
		if got := ft.comparison; got != nil && want.op == got.op && want.positive == got.positive && want.left.key == got.left.key && want.right.key == got.right.key {
			return true
		}
		if ft.or != nil {
			all := len(ft.or) > 0
			for _, alt := range ft.or {
				all = all && f.comparisonKnown(want, alt, depth+1)
			}
			if all {
				return true
			}
			continue
		}
		if ft.pred == nil || ft.pred.Body == nil || len(ft.pred.ParamVars) != len(ft.args)+1 || ft.value.expr == nil {
			continue
		}
		bound := map[*Var]argVal{ft.pred.ParamVars[0]: ft.value}
		for i, a := range ft.args {
			bound[ft.pred.ParamVars[i+1]] = a
		}
		goal := "comparison " + factKey(ft) + " => " + factKey(fact{comparison: want})
		if f.active[goal] {
			continue
		}
		f.active[goal] = true
		body := substituteExpr(ft.pred.Body, bound)
		proven := body != nil && f.comparisonKnown(want, f.conditionFacts(body, true), depth+1)
		delete(f.active, goal)
		if proven {
			return true
		}
	}
	return false
}
