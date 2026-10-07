package gen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"math"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/syntax"
)

type debugFloatRead struct {
	expression ast.Expr
	result     *ast.ParenExpr
	width      int
	operand    bool
}

func debugFloatArithmetic(expr check.Expr) bool {
	switch e := expr.(type) {
	case *check.Binary:
		return check.IsFloat(e.Type()) || debugFloatArithmetic(e.X) || debugFloatArithmetic(e.Y)
	case *check.Unary:
		return check.IsFloat(e.Type()) || debugFloatArithmetic(e.X)
	}
	return false
}

func debugText(expr ast.Expr) (string, error) {
	var out bytes.Buffer
	err := printer.Fprint(&out, token.NewFileSet(), expr)
	return out.String(), err
}

// debugFloatPlan reuses ordinary lowering, then replaces each computed float
// with a staged, rounded value before lowering its parent. Exact hexadecimal
// inputs and one-operation reads are essential: a cast inside a larger Delve
// expression does not round. See docs/debugger-rounding.md for the argument.
func (g *gen) debugFloatPlan(expr check.Expr) (*DebugEvaluation, error) {
	plan := &DebugEvaluation{Type: basicGoNames[expr.Type()]}
	g.debugValues = map[check.Expr]ast.Expr{}
	var stage func(check.Expr) (ast.Expr, error)
	stage = func(expr check.Expr) (ast.Expr, error) {
		if cached := g.debugValues[expr]; cached != nil {
			return cached, nil
		}
		operand := false
		switch e := expr.(type) {
		case *check.Binary:
			if e.Op == syntax.AndAnd || e.Op == syntax.OrOr {
				return nil, fmt.Errorf("debug expression: staged floating-point arithmetic inside short-circuit expressions is unsupported")
			}
			if _, err := stage(e.X); err != nil {
				return nil, err
			}
			if _, err := stage(e.Y); err != nil {
				return nil, err
			}
		case *check.Unary:
			if _, err := stage(e.X); err != nil {
				return nil, err
			}
		case *check.VarRef, *check.Select:
			operand = true
		}
		statements, out := g.value(expr)
		if len(statements) != 0 || out == nil || !g.debugReadSafe(out) {
			return nil, fmt.Errorf("debug expression: this expression requires execution")
		}
		_, literal := expr.(*check.Const)
		if check.IsFloat(expr.Type()) && !literal {
			width := 64
			if expr.Type() == check.Float32 {
				width = 32
			}
			read := out
			if operand {
				// Read the actual IEEE bits. Delve's scalar previews already erase -0.
				text, err := debugText(out)
				if err != nil {
					return nil, err
				}
				read, err = parser.ParseExpr(fmt.Sprintf("*(*uint%d)(uint64(&(%s)))", width, text))
				if err != nil {
					return nil, err
				}
			}
			result := &ast.ParenExpr{X: ast.NewIdent("_debug_pending")}
			plan.floats = append(plan.floats, debugFloatRead{expression: read, result: result, width: width, operand: operand})
			out = result
		}
		g.debugValues[expr] = out
		return out, nil
	}
	out, err := stage(expr)
	if err != nil {
		return nil, err
	}
	plan.output = out
	plan.Read, err = debugText(plan.floats[0].expression)
	return plan, err
}

func (e *DebugEvaluation) advanceFloat(result string) error {
	read := e.floats[0]
	var value float64
	var err error
	if read.operand {
		var bits uint64
		decimal, hexadecimal, decorated := strings.Cut(result, " = ")
		bits, err = strconv.ParseUint(decimal, 10, read.width)
		if decorated && err == nil {
			var hex uint64
			hex, err = strconv.ParseUint(strings.TrimPrefix(hexadecimal, "0x"), 16, read.width)
			if hex != bits {
				err = fmt.Errorf("inconsistent operand bits")
			}
		}
		if read.width == 32 {
			value = float64(math.Float32frombits(uint32(bits)))
		} else {
			value = math.Float64frombits(bits)
		}
	} else {
		value, err = strconv.ParseFloat(result, read.width)
	}
	if err != nil {
		return fmt.Errorf("debug expression: invalid floating-point response from debugger")
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value == 0 && (math.Signbit(value) || !read.operand) {
		return fmt.Errorf("debug expression: floating-point arithmetic with non-finite values, signed zero or a computed zero is unsupported; inspect the values directly")
	}
	text := strconv.FormatFloat(value, 'x', -1, read.width)
	literal, err := parser.ParseExpr(fmt.Sprintf("float%d(%s)", read.width, text))
	if err != nil {
		return err
	}
	read.result.X = literal
	e.floats = e.floats[1:]
	if len(e.floats) != 0 {
		e.Read, err = debugText(e.floats[0].expression)
	} else {
		e.Read = ""
		e.Expression, err = debugText(e.output)
	}
	return err
}
