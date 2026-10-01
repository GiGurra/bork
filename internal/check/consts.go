package check

import (
	"go/constant"
	"go/token"
	"math"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Number literals, and arithmetic on them, are constants: they are
// computed exactly at compile time, and take their type from where they
// are used (`x: Int8 = 100 + 27`). Without a context, an integer
// constant is an Int and any other constant is a Float. A constant that
// does not fit its type is a compile error.

var constOps = map[syntax.Kind]token.Token{
	syntax.Plus: token.ADD, syntax.Minus: token.SUB, syntax.Star: token.MUL,
	syntax.Slash: token.QUO, syntax.Pct: token.REM,
}

// constValue computes e if it is a constant expression. It reports
// nothing: an invalid literal or a division by zero makes e
// non-constant, and the error is reported when e is checked normally.
func constValue(e syntax.Expr) constant.Value {
	var v constant.Value
	switch e := e.(type) {
	case *syntax.IntLit:
		v = constant.MakeFromLiteral(e.Text, token.INT, 0)
	case *syntax.FloatLit:
		v = constant.MakeFromLiteral(e.Text, token.FLOAT, 0)
	case *syntax.Unary:
		if e.Op != syntax.Minus {
			return nil
		}
		if x := constValue(e.X); x != nil {
			v = constant.UnaryOp(token.SUB, x, 0)
		}
	case *syntax.Binary:
		op, ok := constOps[e.Op]
		if !ok {
			return nil
		}
		x, y := constValue(e.X), constValue(e.Y)
		if x == nil || y == nil {
			return nil
		}
		ints := x.Kind() == constant.Int && y.Kind() == constant.Int
		switch {
		case (op == token.QUO || op == token.REM) && constant.Sign(y) == 0:
			return nil
		case op == token.REM && !ints:
			return nil
		case op == token.QUO && ints:
			op = token.QUO_ASSIGN // integer division, as in Go
		}
		v = constant.BinaryOp(x, op, y)
	}
	if v == nil || v.Kind() == constant.Unknown {
		return nil
	}
	return v
}

// constant types the constant expression e, of value v, for the context
// want, and checks that it fits.
func (c *checker) constant(e syntax.Expr, v constant.Value, want Type) Type {
	t := Int
	if v.Kind() == constant.Float {
		t = Float
	}
	if w := numericWant(want, t); w != nil {
		t = w
	}
	v, ok := c.fits(e.Position(), v, t)
	if !ok {
		return c.record(e, Invalid)
	}
	c.info.Consts[e] = v
	return c.record(e, t)
}

// numericWant picks the numeric type a constant takes from the expected
// type: that type if it is numeric, or the only numeric member of an
// expected union (preferring def, the constant's default type).
func numericWant(want, def Type) Type {
	if IsNumeric(want) {
		return want
	}
	u, ok := want.(*Union)
	if !ok {
		return nil
	}
	var found Type
	for _, m := range u.Members {
		if m == def {
			return def
		}
		if IsNumeric(m) {
			if found != nil {
				return nil
			}
			found = m
		}
	}
	return found
}

// fits checks that the constant v can be a value of the numeric type t,
// and returns it converted to t's kind of number.
func (c *checker) fits(pos diag.Pos, v constant.Value, t Type) (constant.Value, bool) {
	if IsFloat(t) {
		v = constant.ToFloat(v)
		var f float64
		if t == Float32 {
			f32, _ := constant.Float32Val(v)
			f = float64(f32)
		} else {
			f, _ = constant.Float64Val(v)
		}
		if math.IsInf(f, 0) {
			c.errorf(pos, "%s does not fit in %s", v, t)
			return nil, false
		}
		return v, true
	}
	iv := constant.ToInt(v)
	if iv.Kind() != constant.Int {
		c.errorf(pos, "%s is not a whole number, so it cannot be %s", v, t)
		return nil, false
	}
	lo, hi := intRange(t)
	if constant.Compare(iv, token.LSS, lo) || constant.Compare(iv, token.GTR, hi) {
		c.errorf(pos, "%s does not fit in %s (%s to %s)", iv.ExactString(), t, lo.ExactString(), hi.ExactString())
		return nil, false
	}
	return iv, true
}

func intRange(t Type) (lo, hi constant.Value) {
	bits := uint(bitsOf(t))
	one := constant.MakeInt64(1)
	if isUnsigned(t) {
		return constant.MakeInt64(0), constant.BinaryOp(constant.Shift(one, token.SHL, bits), token.SUB, one)
	}
	half := constant.Shift(one, token.SHL, bits-1)
	return constant.UnaryOp(token.SUB, half, 0), constant.BinaryOp(half, token.SUB, one)
}
