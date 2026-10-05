package check

import (
	"go/constant"
	"go/token"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Bound constant shifts before allocating arbitrary-precision integers. A modest
// precision budget allows exact intermediate values without unbounded allocation.
func shiftConstant(value, count constant.Value, op syntax.Kind) constant.Value {
	if constant.Sign(count) < 0 {
		return nil
	}
	n, ok := constant.Uint64Val(count)
	if !ok || n > 1024 {
		if op == syntax.Shl {
			if constant.Sign(value) == 0 {
				return constant.MakeInt64(0)
			}
			return nil
		}
		if constant.Sign(value) < 0 {
			return constant.MakeInt64(-1)
		}
		return constant.MakeInt64(0)
	}
	goOp := token.SHL
	if op == syntax.Shr {
		goOp = token.SHR
	}
	return constant.Shift(value, goOp, uint(n))
}

func conditionArithmeticShift(value, count constant.Value, op syntax.Kind, typ Type) constant.Value {
	if value.Kind() != constant.Int || count.Kind() != constant.Int {
		return nil
	}
	v := shiftConstant(value, count, op)
	if v == nil {
		return nil
	}
	return conditionArithmetic(v, typ)
}

func (f *factChecker) shiftCount(x *Binary, e env) {
	if v := f.branchConstant(x.Y, 0); v != nil && v.Kind() == constant.Int && constant.Sign(v) >= 0 {
		return
	}
	zero := &Const{expr: expr{typ: x.Y.Type()}, Value: constant.MakeInt64(0)}
	condition := &Binary{expr: expr{typ: Bool}, Op: syntax.GtEq, X: x.Y, Y: zero}
	ok, _ := f.proveCondition(condition, true, e, 0)
	if !ok {
		if want := f.comparison(condition, true); want != nil {
			ok = f.integerComparisonKnown(want, e.facts, 0, false)
		}
	}
	if !ok {
		// A strictly positive count also meets the requirement.
		condition.Op = syntax.Gt
		ok, _ = f.proveCondition(condition, true, e, 0)
	}
	if ok {
		return
	}
	pos := x.Y.Pos()
	name := f.describe(x.Y)
	if name == "" {
		name = "count"
	}
	f.diags.AddCode(pos, "type.shift-count", "shift count must be proven nonnegative (check it first with if (%s >= 0) { ... })", name)
	f.diags.Suggest(pos, "type.shift-count", pos, diag.Fix{Message: "check the count with if (" + name + " >= 0) { ... }", RequiresInput: true})
}
