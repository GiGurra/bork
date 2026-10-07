package gen

import (
	"bytes"
	"fmt"
	"go/constant"
	"go/printer"
	"go/token"
	"math"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/syntax"
)

// DebugScalarResult is a compiler-produced value with no target storage or children.
type DebugScalarResult struct{ Value string }

type debugScalar struct {
	width   int
	number  float64
	boolean bool
	op      syntax.Kind
	x, y    *debugScalar
	read    string
	ready   bool
}

func debugFloatArithmetic(expr check.Expr) bool {
	switch e := expr.(type) {
	case *check.Binary:
		return debugFloatOperand(e.X) || debugFloatOperand(e.Y) || debugFloatArithmetic(e.X) || debugFloatArithmetic(e.Y)
	case *check.Unary:
		return check.IsFloat(e.Type()) || debugFloatArithmetic(e.X)
	}
	return false
}

func debugFloatOperand(expr check.Expr) bool {
	_, literal := expr.(*check.Const)
	return check.IsFloat(expr.Type()) && !literal
}

func (g *gen) debugFloatPlan(expr check.Expr) (*DebugEvaluation, error) {
	plan := &DebugEvaluation{Type: basicGoNames[expr.Type()]}
	var stage func(check.Expr) (*debugScalar, error)
	stage = func(expr check.Expr) (*debugScalar, error) {
		n := &debugScalar{}
		if check.IsFloat(expr.Type()) {
			n.width = 64
			if expr.Type() == check.Float32 {
				n.width = 32
			}
		} else if expr.Type() != check.Bool {
			return nil, fmt.Errorf("debug expression: unsupported scalar result")
		}
		switch e := expr.(type) {
		case *check.Const:
			switch n.width {
			case 32:
				f, _ := constant.Float32Val(e.Value)
				n.number = float64(f)
			case 64:
				n.number, _ = constant.Float64Val(e.Value)
			default:
				n.boolean = constant.BoolVal(e.Value)
			}
			n.ready = true
			return n, nil
		case *check.FloatBits:
			n.number = math.Float64frombits(e.Bits)
			if n.width == 32 {
				n.number = float64(math.Float32frombits(uint32(e.Bits)))
			}
			n.ready = true
			return n, nil
		}
		if debugFloatArithmetic(expr) {
			var err error
			switch e := expr.(type) {
			case *check.Binary:
				n.op = e.Op
				n.x, err = stage(e.X)
				if err == nil {
					n.y, err = stage(e.Y)
				}
			case *check.Unary:
				n.op = e.Op
				n.x, err = stage(e.X)
			}
			return n, err
		}
		statements, out := g.value(expr)
		if len(statements) != 0 || out == nil || !g.debugReadSafe(out) {
			return nil, fmt.Errorf("debug expression: this expression requires execution")
		}
		var text bytes.Buffer
		if err := printer.Fprint(&text, token.NewFileSet(), out); err != nil {
			return nil, err
		}
		read := text.String()
		if n.width != 0 {
			read = fmt.Sprintf("*(*uint%d)(uint64(&(%s)))", n.width, read)
		}
		n.read = read
		return n, nil
	}
	var err error
	plan.scalar, err = stage(expr)
	if err != nil {
		return nil, err
	}
	return plan, plan.nextScalar()
}

func (e *DebugEvaluation) advanceFloat(result string) error {
	n := e.pending
	if n.width == 0 {
		switch result {
		case "true":
			n.boolean = true
		case "false":
			n.boolean = false
		default:
			return fmt.Errorf("debug expression: invalid boolean response from debugger")
		}
	} else {
		decimal, hexadecimal, decorated := strings.Cut(result, " = ")
		bits, err := strconv.ParseUint(decimal, 10, n.width)
		if decorated && err == nil {
			var hex uint64
			hex, err = strconv.ParseUint(strings.TrimPrefix(hexadecimal, "0x"), 16, n.width)
			if hex != bits {
				err = fmt.Errorf("inconsistent operand bits")
			}
		}
		if err != nil {
			return fmt.Errorf("debug expression: invalid floating-point response from debugger")
		}
		n.number = math.Float64frombits(bits)
		if n.width == 32 {
			n.number = float64(math.Float32frombits(uint32(bits)))
		}
	}
	n.ready = true
	return e.nextScalar()
}

func (e *DebugEvaluation) nextScalar() error {
	next, err := e.scalar.next()
	if err != nil {
		return err
	}
	e.pending = next
	e.Read = ""
	if next != nil {
		e.Read = next.read
		return nil
	}
	text := strconv.FormatBool(e.scalar.boolean)
	if e.scalar.width != 0 {
		text = strconv.FormatFloat(e.scalar.number, 'g', -1, e.scalar.width)
		if !strings.ContainsAny(text, ".eE") && !math.IsNaN(e.scalar.number) && !math.IsInf(e.scalar.number, 0) {
			text += ".0"
		}
	}
	e.Result = &DebugScalarResult{Value: text}
	return nil
}

// next selects only the next operand runtime would evaluate. Completed nodes
// retain their rounded result, and short-circuit nodes never visit a skipped RHS.
func (n *debugScalar) next() (*debugScalar, error) {
	if n.ready {
		return nil, nil
	}
	if n.read != "" {
		return n, nil
	}
	if next, err := n.x.next(); next != nil || err != nil {
		return next, err
	}
	if n.op == syntax.AndAnd && !n.x.boolean || n.op == syntax.OrOr && n.x.boolean {
		n.boolean, n.ready = n.x.boolean, true
		return nil, nil
	}
	if n.y != nil {
		if next, err := n.y.next(); next != nil || err != nil {
			return next, err
		}
	}
	if err := n.compute(); err != nil {
		return nil, err
	}
	n.ready = true
	return nil, nil
}

func (n *debugScalar) compute() error {
	if n.y == nil {
		switch n.op {
		case syntax.Minus:
			n.number = -n.x.number
		case syntax.Not:
			n.boolean = !n.x.boolean
		default:
			return fmt.Errorf("debug expression: unsupported scalar unary operator")
		}
		return nil
	}
	if n.width != 0 {
		var err error
		if n.width == 32 {
			var f float32
			f, err = debugFloatOp(n.op, float32(n.x.number), float32(n.y.number))
			n.number = float64(f)
		} else {
			n.number, err = debugFloatOp(n.op, n.x.number, n.y.number)
		}
		return err
	}
	if n.x.width == 0 {
		switch n.op {
		case syntax.Eq:
			n.boolean = n.x.boolean == n.y.boolean
		case syntax.NotEq:
			n.boolean = n.x.boolean != n.y.boolean
		case syntax.AndAnd, syntax.OrOr:
			n.boolean = n.y.boolean
		default:
			return fmt.Errorf("debug expression: unsupported scalar boolean operator")
		}
		return nil
	}
	x, y := n.x.number, n.y.number
	switch n.op {
	case syntax.Eq:
		n.boolean = x == y
	case syntax.NotEq:
		n.boolean = x != y
	case syntax.Lt:
		n.boolean = x < y
	case syntax.LtEq:
		n.boolean = x <= y
	case syntax.Gt:
		n.boolean = x > y
	case syntax.GtEq:
		n.boolean = x >= y
	default:
		return fmt.Errorf("debug expression: unsupported scalar comparison")
	}
	return nil
}

// Typed Go operators are the same operations emitted by ordinary lowering.
// Assigning each result to T forces the checked width before its parent runs.
func debugFloatOp[T ~float32 | ~float64](op syntax.Kind, x, y T) (T, error) {
	switch op {
	case syntax.Plus:
		return T(x + y), nil
	case syntax.Minus:
		return T(x - y), nil
	case syntax.Star:
		return T(x * y), nil
	case syntax.Slash:
		return T(x / y), nil
	}
	return 0, fmt.Errorf("debug expression: unsupported scalar arithmetic")
}
