package check

import (
	"go/constant"
	"go/token"
	"math"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Number literals, and arithmetic on them, are constants: they
// are computed exactly at compile time, and take their type from where
// they are used (`x: Int8 = 100 + 27`). Without a context, a constant
// with a float literal in it is a Float, and any other is an Int. A
// constant is computed as its type
// computes: `/` divides integers as integers (`7 / 2` is 3 as an Int)
// and floats exactly (`1 / 3` is 0.333... as a Float). A constant that
// does not fit its type is a compile error.

var constOps = map[syntax.Kind]token.Token{
	syntax.Plus: token.ADD, syntax.Minus: token.SUB, syntax.Star: token.MUL,
	syntax.Slash: token.QUO, syntax.Pct: token.REM,
}

// constValue computes e if it is a constant expression of an integer
// type, or (asFloat) of a float type. It reports nothing: an invalid
// literal or a division by zero makes e non-constant, and the error is
// reported when e is checked normally.
func constValue(e syntax.Expr) constant.Value { return constValueAs(e, false) }

func constValueAs(e syntax.Expr, asFloat bool) constant.Value {
	var v constant.Value
	switch e := e.(type) {
	case *syntax.IntLit:
		v = constant.MakeFromLiteral(e.Text, token.INT, 0)
	case *syntax.FloatLit:
		v = constant.MakeFromLiteral(e.Text, token.FLOAT, 0)
	case *syntax.RuneLit:
		return nil
	case *syntax.Unary:
		if e.Op != syntax.Minus {
			return nil
		}
		if x := constValueAs(e.X, asFloat); x != nil {
			v = constant.UnaryOp(token.SUB, x, 0)
		}
	case *syntax.Binary:
		op, ok := constOps[e.Op]
		if !ok {
			return nil
		}
		x, y := constValueAs(e.X, asFloat), constValueAs(e.Y, asFloat)
		if x == nil || y == nil {
			return nil
		}
		ints := x.Kind() == constant.Int && y.Kind() == constant.Int
		switch {
		case (op == token.QUO || op == token.REM) && constant.Sign(y) == 0:
			return nil
		case op == token.REM && (!ints || asFloat):
			return nil
		case op == token.QUO && ints && !asFloat:
			op = token.QUO_ASSIGN // integer division
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
	t := defaultConstType(e)
	if w := numericWant(want, t); w != nil {
		t = w
	}
	if IsFloat(t) {
		v = constValueAs(e, true)
		if v == nil {
			// The only operation integers allow and floats do not.
			c.errorf(e.Position(), "operator %% needs integers, but this constant is a %s", t)
			return c.record(e, Invalid)
		}
	}
	v, ok := c.fits(e.Position(), v, t)
	if !ok {
		return c.record(e, Invalid)
	}
	c.info.consts[e] = v
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

// IntRange is the smallest and the largest value of the integer type t.
func IntRange(t Type) (lo, hi constant.Value) { return intRange(t) }

func intRange(t Type) (lo, hi constant.Value) {
	bits := uint(bitsOf(t))
	one := constant.MakeInt64(1)
	if isUnsigned(t) {
		return constant.MakeInt64(0), constant.BinaryOp(constant.Shift(one, token.SHL, bits), token.SUB, one)
	}
	half := constant.Shift(one, token.SHL, bits-1)
	return constant.UnaryOp(token.SUB, half, 0), constant.BinaryOp(half, token.SUB, one)
}

// defaultConstType is the type of a constant expression used without a
// context: Float if it has a float literal, and Int otherwise.
func defaultConstType(e syntax.Expr) Type {
	hasFloat := false
	var walk func(e syntax.Expr)
	walk = func(e syntax.Expr) {
		switch e := e.(type) {
		case *syntax.FloatLit:
			hasFloat = true
		case *syntax.Unary:
			walk(e.X)
		case *syntax.Binary:
			walk(e.X)
			walk(e.Y)
		}
	}
	walk(e)
	switch {
	case hasFloat:
		return Float
	}
	return Int
}
