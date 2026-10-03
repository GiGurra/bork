package check

import (
	"fmt"
	"go/constant"
	"testing"

	"github.com/GiGurra/bork/internal/syntax"
)

// Compare accepted proofs against exhaustive runtime values, independently of
// the proof's range calculations. Int8 includes every overflow/underflow edge.
func TestArithmeticInt8Soundness(t *testing.T) {
	f := &factChecker{fn: &Func{}}
	x := &VarRef{expr: expr{typ: Int8}, Var: &Var{Name: "x", Type: Int8, Kind: VarParam}}
	literal := func(n int) Expr {
		return &Const{expr: expr{typ: Int8}, Value: constant.MakeInt64(int64(n))}
	}
	binary := func(op syntax.Kind, a, b Expr) *Binary {
		typ := Int8
		if _, comparison := compareOps[op]; comparison {
			typ = Bool
		}
		return &Binary{expr: expr{typ: typ}, Op: op, X: a, Y: b}
	}
	intervals := [][2]int{{-128, 127}, {-128, -128}, {127, 127}, {-100, 100}, {0, 127}, {-128, 0}, {126, 127}}
	shifts := []int{-128, -127, -10, -1, 0, 1, 10, 126, 127}
	limits := []int{-128, -127, -1, 0, 1, 126, 127}
	ops := []syntax.Kind{syntax.Lt, syntax.LtEq, syntax.Gt, syntax.GtEq, syntax.Eq, syntax.NotEq}
	for _, interval := range intervals {
		facts := []fact{
			{comparison: f.comparison(binary(syntax.GtEq, x, literal(interval[0])), true)},
			{comparison: f.comparison(binary(syntax.LtEq, x, literal(interval[1])), true)},
		}
		for _, shift := range shifts {
			for _, arithmetic := range []syntax.Kind{syntax.Plus, syntax.Minus} {
				result := binary(arithmetic, x, literal(shift))
				for _, limit := range limits {
					for _, op := range ops {
						goal := f.comparison(binary(op, result, literal(limit)), true)
						if !f.arithmeticKnown(goal, facts, 0) {
							continue
						}
						for n := interval[0]; n <= interval[1]; n++ {
							value := int8(n) + int8(shift)
							if arithmetic == syntax.Minus {
								value = int8(n) - int8(shift)
							}
							bound := int8(limit)
							var holds bool
							switch op {
							case syntax.Lt:
								holds = value < bound
							case syntax.LtEq:
								holds = value <= bound
							case syntax.Gt:
								holds = value > bound
							case syntax.GtEq:
								holds = value >= bound
							case syntax.Eq:
								holds = value == bound
							case syntax.NotEq:
								holds = value != bound
							}
							if !holds {
								t.Fatalf("unsound proof in interval %v: %d %v %d %v %d (runtime result %d)", interval, n, arithmetic, shift, op, limit, value)
							}
						}
					}
				}
			}
		}
	}
}

func TestArithmeticProofBudgets(t *testing.T) {
	f := &factChecker{fn: &Func{}}
	x := &VarRef{expr: expr{typ: Int8}, Var: &Var{Name: "x", Type: Int8, Kind: VarParam}}
	zero := &Const{expr: expr{typ: Int8}, Value: constant.MakeInt64(0)}
	one := &Const{expr: expr{typ: Int8}, Value: constant.MakeInt64(1)}
	limit := &Const{expr: expr{typ: Int8}, Value: constant.MakeInt64(126)}
	lower := f.comparison(&Binary{Op: syntax.GtEq, X: x, Y: zero}, true)
	upper := f.comparison(&Binary{Op: syntax.LtEq, X: x, Y: limit}, true)
	result := &Binary{expr: expr{typ: Int8}, Op: syntax.Plus, X: x, Y: one}
	goal := f.comparison(&Binary{Op: syntax.Gt, X: result, Y: zero}, true)
	for _, count := range []int{10, 48} {
		facts := []fact{{comparison: lower}, {comparison: upper}}
		for i := 0; i < count; i++ {
			v := &VarRef{expr: expr{typ: Int8}, Var: &Var{Name: fmt.Sprintf("extra%d", i), Type: Int8, Kind: VarParam}}
			facts = append(facts, fact{comparison: f.comparison(&Binary{Op: syntax.GtEq, X: v, Y: zero}, true)})
		}
		if got, want := f.arithmeticKnown(goal, facts, 0), count == 10; got != want {
			t.Errorf("%d unrelated values: proven = %t, want %t", count, got, want)
		}
	}
	for _, count := range []int{32, 33} {
		alternatives := make([][]fact, count)
		for i := range alternatives {
			alternatives[i] = []fact{{comparison: upper}}
		}
		facts := []fact{{comparison: lower}, {or: alternatives}}
		if got, want := f.arithmeticKnown(goal, facts, 0), count == 32; got != want {
			t.Errorf("%d alternatives: proven = %t, want %t", count, got, want)
		}
	}
}
