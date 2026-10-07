package gen

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
)

func floatFixture(width int) (*DebugMap, []check.DebugLocal) {
	metadata, locals := expressionFixture()
	key := fmt.Sprintf("float%d", width)
	label := "Float"
	if width == 32 {
		label = "Float32"
	}
	metadata.Expressions[key] = check.DebugShape{Name: label, Kind: "scalar"}
	metadata.Bindings[0].Names["f"] = "f"
	locals = append(locals, check.DebugLocal{Name: "f", GoName: "f", Type: key})
	return metadata, locals
}

func TestDebugFloatStages(t *testing.T) {
	for _, width := range []int{32, 64} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			metadata, locals := floatFixture(width)
			plan, err := DebugExpressionPlan("(f + 1.0) + 1.0 == f", metadata, diag.Pos{File: "main.bork", Line: 5}, locals)
			if err != nil {
				t.Fatal(err)
			}
			value := float64(9007199254740992)
			bits := math.Float64bits(value)
			if width == 32 {
				value = 16777216
				bits = uint64(math.Float32bits(float32(value)))
			}
			if !strings.Contains(plan.Read, fmt.Sprintf("uint%d", width)) {
				t.Fatalf("raw operand read: %s", plan.Read)
			}
			decorated := fmt.Sprintf("%d = 0x%x", bits, bits)
			for _, response := range []string{decorated, fmt.Sprint(value), fmt.Sprint(value), fmt.Sprint(bits)} {
				if err := plan.Advance(response); err != nil {
					t.Fatal(err)
				}
			}
			if plan.Read != "" || strings.Contains(plan.Expression, "_debug_pending") || !strings.Contains(plan.Expression, "==") || plan.Type != "bool" {
				t.Fatalf("completed plan: %+v", plan)
			}
		})
	}
}

func TestDebugFloatSpecialValues(t *testing.T) {
	for _, width := range []int{32, 64} {
		for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), math.Copysign(0, -1)} {
			metadata, locals := floatFixture(width)
			plan, err := DebugExpressionPlan("f + 1.0", metadata, diag.Pos{File: "main.bork", Line: 5}, locals)
			if err != nil {
				t.Fatal(err)
			}
			bits := math.Float64bits(value)
			if width == 32 {
				bits = uint64(math.Float32bits(float32(value)))
			}
			if err := plan.Advance(fmt.Sprint(bits)); err == nil {
				t.Fatalf("accepted special operand width %d: %v", width, value)
			}
		}
		for _, response := range []string{"0", "-0", "NaN", "+Inf", "-Inf", "invalid"} {
			metadata, locals := floatFixture(width)
			plan, err := DebugExpressionPlan("f + 1.0", metadata, diag.Pos{File: "main.bork", Line: 5}, locals)
			if err != nil {
				t.Fatal(err)
			}
			bits := math.Float64bits(1)
			if width == 32 {
				bits = uint64(math.Float32bits(1))
			}
			if err := plan.Advance(fmt.Sprint(bits)); err != nil {
				t.Fatal(err)
			}
			if err := plan.Advance(response); err == nil {
				t.Fatalf("accepted special result width %d: %s", width, response)
			}
		}
	}
	metadata, locals := floatFixture(64)
	for _, source := range []string{"b && f + 1.0 > f", "f + 1.0 > f || b"} {
		if _, err := DebugExpressionPlan(source, metadata, diag.Pos{File: "main.bork", Line: 5}, locals); err == nil || !strings.Contains(err.Error(), "short-circuit") {
			t.Fatalf("%s: %v", source, err)
		}
	}
}

func TestDebugFloatMalformedOperands(t *testing.T) {
	for _, width := range []int{32, 64} {
		for _, response := range []string{"-1", "18446744073709551616", "garbage", "1 = 0x2", "1 = nothex", "1; helper()", "1 + 2"} {
			metadata, locals := floatFixture(width)
			plan, err := DebugExpressionPlan("f + 1.0", metadata, diag.Pos{File: "main.bork", Line: 5}, locals)
			if err != nil {
				t.Fatal(err)
			}
			if err := plan.Advance(response); err == nil || !strings.Contains(err.Error(), "invalid floating-point response") {
				t.Fatalf("width %d, operand %q: %v", width, response, err)
			}
		}
		metadata, locals := floatFixture(width)
		plan, err := DebugExpressionPlan("f + 1.0", metadata, diag.Pos{File: "main.bork", Line: 5}, locals)
		if err != nil {
			t.Fatal(err)
		}
		if err := plan.Advance("0 = 0x0"); err != nil {
			t.Fatalf("positive zero operand: %v", err)
		}
		if err := plan.Advance("1.0"); err != nil || plan.Read != "" {
			t.Fatalf("positive zero plus one: %+v, %v", plan, err)
		}
	}
	metadata, locals := floatFixture(64)
	metadata.Expressions["float32"] = check.DebugShape{Name: "Float32", Kind: "scalar"}
	metadata.Bindings[0].Names["g"] = "g"
	locals = append(locals, check.DebugLocal{Name: "g", GoName: "g", Type: "float32"})
	if _, err := DebugExpressionPlan("f + g", metadata, diag.Pos{File: "main.bork", Line: 5}, locals); err == nil {
		t.Fatal("accepted mixed-width arithmetic")
	}
}
