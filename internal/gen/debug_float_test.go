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
	key, label := fmt.Sprintf("float%d", width), "Float"
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
		boundary := float64(9007199254740992)
		tiny := math.SmallestNonzeroFloat64
		huge := math.MaxFloat64
		if width == 32 {
			boundary = 16777216
			tiny = math.SmallestNonzeroFloat32
			huge = math.MaxFloat32
		}
		for _, test := range []struct {
			source string
			input  float64
			want   string
		}{
			{"(f + 1.0) + 1.0 == f", boundary, "true"},
			{"f - f", boundary, "0.0"},
			{"f / 2.0", tiny, "0.0"},
			{"-f / 2.0", tiny, "-0.0"},
			{"f * 2.0", huge, "+Inf"},
			{"1.0 / (f - f)", 1, "+Inf"},
			{"-1.0 / (f - f)", -1, "-Inf"},
			{"(f - f) / (f - f)", 0, "NaN"},
			{"f + f", math.Copysign(0, -1), "-0.0"},
			{"1.0 / (f * 2.0)", math.Copysign(0, -1), "-Inf"},
			{"f - f", math.Inf(1), "NaN"},
			{"f * 0.0", math.Inf(-1), "NaN"},
			{"-f", math.Inf(-1), "+Inf"},
			{"f + 1.0", math.NaN(), "NaN"},
			{"f == f", math.NaN(), "false"},
			{"f != f", math.NaN(), "true"},
			{"f <= f", math.NaN(), "false"},
			{"!(f > 0.0)", math.NaN(), "true"},
			{"(f > 0.0) == true", 1, "true"},
		} {
			t.Run(fmt.Sprintf("%d/%s/%v", width, test.source, test.input), func(t *testing.T) {
				metadata, locals := floatFixture(width)
				plan, err := DebugExpressionPlan(test.source, metadata, diag.Pos{File: "main.bork", Line: 5}, locals)
				if err != nil {
					t.Fatal(err)
				}
				bits := math.Float64bits(test.input)
				if width == 32 {
					bits = uint64(math.Float32bits(float32(test.input)))
				}
				for plan.Read != "" {
					if !strings.Contains(plan.Read, fmt.Sprintf("uint%d", width)) {
						t.Fatalf("non-bit operand read: %s", plan.Read)
					}
					if err := plan.Advance(fmt.Sprintf("%d = 0x%x", bits, bits)); err != nil {
						t.Fatal(err)
					}
				}
				if plan.Result == nil || plan.Result.Value != test.want || plan.Expression != "" {
					t.Fatalf("result: %+v, want %s", plan, test.want)
				}
			})
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
	}
	metadata, locals := floatFixture(64)
	metadata.Expressions["float32"] = check.DebugShape{Name: "Float32", Kind: "scalar"}
	metadata.Bindings[0].Names["g"] = "g"
	locals = append(locals, check.DebugLocal{Name: "g", GoName: "g", Type: "float32"})
	if _, err := DebugExpressionPlan("f + g", metadata, diag.Pos{File: "main.bork", Line: 5}, locals); err == nil {
		t.Fatal("accepted mixed-width arithmetic")
	}
}

func TestDebugFloatBooleanOperand(t *testing.T) {
	for _, response := range []string{"true", "false", "invalid", "true; helper()"} {
		metadata, locals := floatFixture(64)
		plan, err := DebugExpressionPlan("(f > 0.0) == b", metadata, diag.Pos{File: "main.bork", Line: 5}, locals)
		if err != nil {
			t.Fatal(err)
		}
		if err := plan.Advance(fmt.Sprint(math.Float64bits(1))); err != nil {
			t.Fatal(err)
		}
		if plan.Read != "b" {
			t.Fatalf("boolean read: %s", plan.Read)
		}
		err = plan.Advance(response)
		if response == "true" || response == "false" {
			if err != nil || plan.Result == nil || plan.Result.Value != response {
				t.Fatalf("%s: %+v, %v", response, plan, err)
			}
		} else if err == nil {
			t.Fatalf("accepted boolean %q", response)
		}
	}
}

func TestDebugFloatLazyReads(t *testing.T) {
	for _, width := range []int{32, 64} {
		floatRead := fmt.Sprintf("*(*uint%d)(uint64(&(f)))", width)
		bits := math.Float64bits(1)
		if width == 32 {
			bits = uint64(math.Float32bits(1))
		}
		for _, test := range []struct {
			source           string
			reads, responses []string
			want             string
		}{
			{"false && f > 0.0", nil, nil, "false"},
			{"true || f > 0.0", nil, nil, "true"},
			{"b && f > 0.0", []string{"b"}, []string{"false"}, "false"},
			{"b || f > 0.0", []string{"b"}, []string{"true"}, "true"},
			{"b && f > 0.0", []string{"b", floatRead}, []string{"true", fmt.Sprint(bits)}, "true"},
			{"b || f > 0.0", []string{"b", floatRead}, []string{"false", fmt.Sprint(bits)}, "true"},
			{"f > 0.0 || (f / f > 0.0 && b)", []string{floatRead}, []string{fmt.Sprint(bits)}, "true"},
			{"f < 0.0 && b || f >= 0.0", []string{floatRead, floatRead}, []string{fmt.Sprint(bits), fmt.Sprint(bits)}, "true"},
			{"b && (true || f > 0.0)", []string{"b"}, []string{"true"}, "true"},
			{"b || (false && f > 0.0)", []string{"b"}, []string{"false"}, "false"},
			{"!(b && f > 0.0)", []string{"b"}, []string{"false"}, "true"},
			{"(false && f > 0.0) == (true || f > 0.0)", nil, nil, "false"},
		} {
			t.Run(fmt.Sprintf("%d/%s/%v", width, test.source, test.responses), func(t *testing.T) {
				metadata, locals := floatFixture(width)
				plan, err := DebugExpressionPlan(test.source, metadata, diag.Pos{File: "main.bork", Line: 5}, locals)
				if err != nil {
					t.Fatal(err)
				}
				for i, read := range test.reads {
					if plan.Read != read {
						t.Fatalf("read %d: %s, want %s", i, plan.Read, read)
					}
					if err := plan.Advance(test.responses[i]); err != nil {
						t.Fatal(err)
					}
				}
				if plan.Read != "" || plan.Result == nil || plan.Result.Value != test.want {
					t.Fatalf("result: %+v; want %s", plan, test.want)
				}
			})
		}
	}
	metadata, locals := floatFixture(64)
	for _, source := range []string{"false && missing > f", "true || println(f)", "false && f / 0.0 > f"} {
		if _, err := DebugExpressionPlan(source, metadata, diag.Pos{File: "main.bork", Line: 5}, locals); err == nil {
			t.Fatalf("skipped source escaped normal checking: %s", source)
		}
	}
}
