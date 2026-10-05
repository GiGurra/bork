package check

import (
	"go/constant"
	"go/token"

	"github.com/GiGurra/bork/internal/syntax"
)

// Integer arithmetic is normalized only after bounds prove every intermediate
// fits its runtime type. Other computed values keep their ordinary identities.
func arithmeticExpr(x Expr) Expr {
	for depth := 0; x != nil && depth < maxDepth; depth++ {
		x = debugValue(x)
		if v, ok := x.(*VarRef); ok && v.Var.Kind == VarLet {
			x = v.Var.Let.Value
			continue
		}
		return x
	}
	return nil
}

func hasIntegerArithmetic(x Expr) bool {
	b, ok := arithmeticExpr(x).(*Binary)
	return ok && IsInteger(b.Type()) && (b.Op == syntax.Plus || b.Op == syntax.Minus)
}

func (f *factChecker) arithmeticKnown(want *comparison, facts []fact, depth int) bool {
	return f.integerComparisonKnown(want, facts, depth, true)
}

func (f *factChecker) integerComparisonKnown(want *comparison, facts []fact, depth int, arithmeticOnly bool) bool {
	if !want.positive || want.left.expr == nil || want.right.expr == nil {
		return false
	}
	typ := want.left.expr.Type()
	if !IsInteger(typ) || !identical(typ, want.right.expr.Type()) ||
		(arithmeticOnly && !hasIntegerArithmetic(want.left.expr) && !hasIntegerArithmetic(want.right.expr)) {
		return false
	}
	facts = append([]fact{}, facts...)
	seen := map[string]bool{}
	var declared func(Expr, int)
	declared = func(x Expr, depth int) {
		if x == nil || depth > maxDepth {
			return
		}
		v := f.argOf(x)
		if v.key == "" || seen[v.key] {
			return
		}
		seen[v.key] = true
		for _, k := range f.declared(x, env{facts: facts}, depth) {
			if k.path == "" && k.pred != nil {
				facts = append(facts, fact{pred: k.pred, inst: k.inst, subject: v.key, args: k.args, value: v})
			}
		}
		if b, ok := arithmeticExpr(x).(*Binary); ok && (b.Op == syntax.Plus || b.Op == syntax.Minus) {
			declared(b.X, depth+1)
			declared(b.Y, depth+1)
		}
	}
	declared(want.left.expr, depth+1)
	declared(want.right.expr, depth+1)
	cases, ok := f.arithmeticCases(facts, depth)
	if !ok || len(cases) == 0 {
		return false
	}
	for _, comparisons := range cases {
		g := &integerBounds{f: f, typ: typ, index: map[string]int{}, values: []Expr{nil}}
		left, right := g.term(want.left), g.term(want.right)
		for _, c := range comparisons {
			if c.positive && c.left.expr != nil && c.right.expr != nil && identical(c.left.expr.Type(), typ) && identical(c.right.expr.Type(), typ) {
				g.term(c.left)
				g.term(c.right)
			}
		}
		if g.failed {
			return false
		}
		g.init()
		for _, c := range comparisons {
			if c.positive && c.left.expr != nil && c.right.expr != nil && identical(c.left.expr.Type(), typ) && identical(c.right.expr.Type(), typ) {
				g.assume(c.op, g.term(c.left), g.term(c.right))
			}
		}
		g.close()
		if g.impossible {
			continue
		}
		for round := 0; round < len(g.values); round++ {
			changed := false
			for i, x := range g.values[1:] {
				changed = g.arithmetic(i+1, x) || changed
			}
			if !changed {
				break
			}
			g.close()
			if g.impossible {
				break
			}
		}
		if !g.proves(want.op, left, right) {
			return false
		}
	}
	return true
}

// Keep common conjuncts in every OR alternative, without mixing alternatives.
func (f *factChecker) arithmeticCases(facts []fact, depth int) ([][]*comparison, bool) {
	var cases [][]*comparison
	seen := map[string]bool{}
	var visit func([]fact, []*comparison, int) bool
	visit = func(todo []fact, known []*comparison, depth int) bool {
		if depth > maxDepth || len(cases) >= 32 {
			return false
		}
		if len(todo) == 0 {
			cases = append(cases, known)
			return true
		}
		ft, rest := todo[0], todo[1:]
		if ft.comparison != nil {
			return visit(rest, append(known, ft.comparison), depth)
		}
		if ft.or != nil {
			for _, alt := range ft.or {
				if !visit(append(append([]fact{}, alt...), rest...), append([]*comparison{}, known...), depth+1) {
					return false
				}
			}
			return true
		}
		if ft.pred != nil && ft.pred.Body != nil && ft.value.expr != nil && len(ft.pred.ParamVars) == len(ft.args)+1 {
			key := factKey(ft)
			if !seen[key] {
				seen[key] = true
				bound := map[*Var]argVal{ft.pred.ParamVars[0]: ft.value}
				for i, a := range ft.args {
					bound[ft.pred.ParamVars[i+1]] = a
				}
				body := substituteExpr(ft.pred.Body, bound)
				inst := ft.inst
				if inst == nil {
					inst = f.projectionPredicateInstance(ft.pred, ft.value.expr, ft.args, nil)
				}
				if inst != nil {
					body = substituteRequirementTypes(body, bindParams(ft.pred.TypeParams, inst.TypeArgs), inst.Dicts)
				}
				if body != nil {
					ok := visit(append(f.conditionFacts(body, true), rest...), known, depth+1)
					delete(seen, key)
					return ok
				}
				delete(seen, key)
			}
		}
		return visit(rest, known, depth)
	}
	ok := visit(facts, nil, depth)
	return cases, ok
}

type integerTerm struct {
	node   int
	offset constant.Value
}

// bounds[i][j] is an upper bound on value i minus value j. Node zero is
// mathematical zero; constants are offsets from it. Proof calculations are exact.
type integerBounds struct {
	f          *factChecker
	typ        Type
	index      map[string]int
	values     []Expr
	bounds     [][]constant.Value
	failed     bool
	impossible bool
}

func (g *integerBounds) term(a argVal) integerTerm {
	if a.value != nil {
		return integerTerm{offset: a.value}
	}
	x := arithmeticExpr(a.expr)
	if v := constOf(x); v != nil {
		return integerTerm{offset: v}
	}
	if i, ok := g.index[a.key]; ok {
		return integerTerm{node: i, offset: constant.MakeInt64(0)}
	}
	if a.key == "" || a.expr == nil || len(g.values) >= 48 {
		g.failed = true
		return integerTerm{offset: constant.MakeInt64(0)}
	}
	i := len(g.values)
	g.index[a.key] = i
	g.values = append(g.values, x)
	if b, ok := x.(*Binary); ok && (b.Op == syntax.Plus || b.Op == syntax.Minus) {
		g.term(g.f.comparisonArg(b.X))
		g.term(g.f.comparisonArg(b.Y))
	}
	return integerTerm{node: i, offset: constant.MakeInt64(0)}
}

func (g *integerBounds) init() {
	g.bounds = make([][]constant.Value, len(g.values))
	zero := constant.MakeInt64(0)
	lo, hi := intRange(g.typ)
	for i := range g.bounds {
		g.bounds[i] = make([]constant.Value, len(g.values))
		g.bounds[i][i] = zero
		if i > 0 {
			g.bounds[i][0] = hi
			g.bounds[0][i] = constant.UnaryOp(token.SUB, lo, 0)
		}
	}
}

func (g *integerBounds) add(i, j int, upper constant.Value) bool {
	if old := g.bounds[i][j]; old == nil || constant.Compare(upper, token.LSS, old) {
		g.bounds[i][j] = upper
		return true
	}
	return false
}

func (g *integerBounds) close() {
	for k := range g.bounds {
		for i := range g.bounds {
			for j := range g.bounds {
				if a, b := g.bounds[i][k], g.bounds[k][j]; a != nil && b != nil {
					g.add(i, j, constant.BinaryOp(a, token.ADD, b))
					if i == j && constant.Sign(g.bounds[i][i]) < 0 {
						g.impossible = true
						return
					}
				}
			}
		}
	}
}

func (g *integerBounds) assume(op syntax.Kind, a, b integerTerm) {
	upper := constant.BinaryOp(b.offset, token.SUB, a.offset)
	switch op {
	case syntax.Lt:
		g.add(a.node, b.node, constant.BinaryOp(upper, token.SUB, constant.MakeInt64(1)))
	case syntax.LtEq:
		g.add(a.node, b.node, upper)
	case syntax.Eq:
		g.add(a.node, b.node, upper)
		g.add(b.node, a.node, constant.UnaryOp(token.SUB, upper, 0))
	}
}

func (g *integerBounds) upper(a, b integerTerm) constant.Value {
	return constant.BinaryOp(g.bounds[a.node][b.node], token.ADD, constant.BinaryOp(a.offset, token.SUB, b.offset))
}

func (g *integerBounds) proves(op syntax.Kind, a, b integerTerm) bool {
	// Contradictory branch facts describe no runtime values.
	if g.impossible {
		return true
	}
	difference := g.upper(a, b)
	switch op {
	case syntax.Lt:
		return constant.Sign(difference) < 0
	case syntax.LtEq:
		return constant.Sign(difference) <= 0
	case syntax.Eq:
		return constant.Sign(difference) <= 0 && constant.Sign(g.upper(b, a)) <= 0
	case syntax.NotEq:
		return constant.Sign(difference) < 0 || constant.Sign(g.upper(b, a)) < 0
	}
	return false
}

func (g *integerBounds) arithmetic(i int, x Expr) bool {
	b, ok := x.(*Binary)
	if !ok || (b.Op != syntax.Plus && b.Op != syntax.Minus) {
		return false
	}
	a, c := g.term(g.f.comparisonArg(b.X)), g.term(g.f.comparisonArg(b.Y))
	zero := integerTerm{offset: constant.MakeInt64(0)}
	var lo, hi constant.Value
	if b.Op == syntax.Minus {
		hi = g.upper(a, c)
		lo = constant.UnaryOp(token.SUB, g.upper(c, a), 0)
	} else {
		hi = constant.BinaryOp(g.upper(a, zero), token.ADD, g.upper(c, zero))
		lo = constant.UnaryOp(token.SUB, constant.BinaryOp(g.upper(zero, a), token.ADD, g.upper(zero, c)), 0)
	}
	min, max := intRange(g.typ)
	if constant.Compare(lo, token.LSS, min) || constant.Compare(hi, token.GTR, max) {
		return false
	}
	changed := g.add(i, 0, hi)
	changed = g.add(0, i, constant.UnaryOp(token.SUB, lo, 0)) || changed
	// A safe operation also bounds its result relative to either operand.
	// For subtraction, result - a is -c; for addition it is c.
	upper, lower := g.upper(c, zero), constant.UnaryOp(token.SUB, g.upper(zero, c), 0)
	if b.Op == syntax.Minus {
		upper, lower = constant.UnaryOp(token.SUB, lower, 0), constant.UnaryOp(token.SUB, upper, 0)
	}
	changed = g.relative(i, a, lower, upper) || changed
	if b.Op == syntax.Plus {
		changed = g.relative(i, c, constant.UnaryOp(token.SUB, g.upper(zero, a), 0), g.upper(a, zero)) || changed
	}
	return changed
}

func (g *integerBounds) relative(result int, base integerTerm, lo, hi constant.Value) bool {
	changed := g.add(result, base.node, constant.BinaryOp(base.offset, token.ADD, hi))
	return g.add(base.node, result, constant.UnaryOp(token.SUB, constant.BinaryOp(base.offset, token.ADD, lo), 0)) || changed
}
