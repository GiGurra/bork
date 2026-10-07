package gen

import (
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
)

func expressionFixture() (*DebugMap, []check.DebugLocal) {
	metadata := &DebugMap{Bindings: []DebugBindings{{Start: diag.Pos{File: "main.bork", Line: 1}, End: diag.Pos{File: "main.bork", Line: 10}, Names: map[string]string{"range_": "range", "r": "r", "_rebind_2_3_n": "n", "b": "b"}}}, ExpressionNames: map[string]string{"range_": "range", "_rebind_2_3_n": "n"}, Names: map[string]string{"range_": "range", "_rebind_2_3_n": "n"}, Expressions: map[string]check.DebugShape{
		"int64": {Name: "Int", Kind: "scalar"}, "int8": {Name: "Int8", Kind: "scalar"}, "float64": {Name: "Float", Kind: "scalar"}, "bool": {Name: "Bool", Kind: "scalar"}, "string": {Name: "String", Kind: "scalar"}, "main._Rune": {Name: "Rune", Kind: "scalar"},
		"main.Record": {Name: "Record", Kind: "record", Fields: map[string]check.DebugMember{"range": {Type: "int64"}, "child": {Type: "main.Record"}, "deferred": {Type: "int64", Deferred: true}, "items": {Type: "[]int64"}}},
		"[]int64":     {Name: "List[Int]", Kind: "opaque"},
	}}
	return metadata, []check.DebugLocal{{Name: "range_", GoName: "range_", Type: "int64"}, {Name: "r", GoName: "r", Type: "main.Record"}, {Name: "_rebind_2_3_n", GoName: "_rebind_2_3_n", Type: "int8"}, {Name: "b", GoName: "b", Type: "bool"}}
}

func TestDebugExpression(t *testing.T) {
	for _, test := range []struct{ source, want string }{
		{"range", "range_"}, {"range + 2", "range_ + 2"}, {"r.child.range * (range + 1)", "r.child.range_ * (range_ + 1)"},
		{"n + 127", "_rebind_2_3_n + 127"}, {"!b || range > 2", "(!b) || (range_ > 2)"},
		{"^range & 3", "((^range_) + int64(0)) & 3"}, {`"hé" + "llo"`, `"hé" + "llo"`},
		{"(1 + 2) * 3", "int64(9)"}, {"'å'", "int32(229)"}, {"1.5", "float64(0x1.8p+00)"}, {"r.items", "r.items"},
	} {
		t.Run(test.source, func(t *testing.T) {
			metadata, locals := expressionFixture()
			got, err := DebugExpression(test.source, metadata, diag.Pos{File: "main.bork", Line: 5}, locals)
			if err != nil || got != test.want {
				t.Fatalf("got %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestDebugExpressionErrors(t *testing.T) {
	for _, test := range []struct{ source, message string }{
		{"missing + 1", "undefined local missing"}, {"r.nope", "has no field nope"}, {"n + 128", "does not fit in Int8"},
		{"range + true", "operator +"}, {"r == r", "scalar operands"}, {"r.deferred", "deferred field"},
		{"r.items == r.items", "scalar operands"}, {"r.items.get(0)", "unsupported expression"}, {"println(range)", "unsupported expression"},
		{"if (true) { range } else { 2 }", "unsupported expression"}, {`s"value: ${range}"`, "unsupported expression"},
		{"range |> println", "unsupported expression"}, {"range?", "unsupported expression"}, {"r.items[0]", "expected identifier"},
		{"range = 1", "after the expression"}, {"1; println(2)", "after the expression"}, {"", "expected"}, {"(", "expected"},
	} {
		t.Run(test.source, func(t *testing.T) {
			metadata, locals := expressionFixture()
			_, err := DebugExpression(test.source, metadata, diag.Pos{File: "main.bork", Line: 5}, locals)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("got %v; want %q", err, test.message)
			}
		})
	}
	metadata, locals := expressionFixture()
	locals = append(locals, locals[0])
	if _, err := DebugExpression("range", metadata, diag.Pos{File: "main.bork", Line: 5}, locals); err == nil || !strings.Contains(err.Error(), "ambiguous local") {
		t.Fatalf("duplicate names: %v", err)
	}
}

func TestDebugEvaluateName(t *testing.T) {
	metadata, _ := expressionFixture()
	for _, test := range []struct{ source, want string }{
		{"r.child.range_", "r.child.range"}, {"_rebind_2_3_n", "n"},
	} {
		got, ok := DebugEvaluateName(test.source, metadata)
		if !ok || got != test.want {
			t.Fatalf("name %s: %q, %v", test.source, got, ok)
		}
	}
	for _, source := range []string{"r.items[0]", "r.(main.Record).range_", "helper()", "r + 1"} {
		if translated, ok := DebugEvaluateName(source, metadata); ok {
			t.Fatalf("translated unsupported path %s: %s", source, translated)
		}
	}
}

func TestDebugExpressionBindingIdentity(t *testing.T) {
	metadata, locals := expressionFixture()
	metadata.Bindings = append(metadata.Bindings, DebugBindings{Start: diag.Pos{File: "main.bork", Line: 20}, End: diag.Pos{File: "main.bork", Line: 30}, Names: map[string]string{"range_": "range_"}})
	metadata.ExpressionNames["range_"] = ""
	site := diag.Pos{File: "main.bork", Line: 25}
	if got, err := DebugExpression("range_", metadata, site, locals); err != nil || got != "range_" {
		t.Fatalf("source suffix identity: %q, %v", got, err)
	}
	if _, err := DebugExpression("range", metadata, site, locals); err == nil {
		t.Fatal("invented a source variable from a reserved-name alias")
	}
	if got, ok := DebugEvaluateName("r.range_", metadata); ok {
		t.Fatalf("ambiguous field name translated: %s", got)
	}
	metadata.Bindings = append(metadata.Bindings, metadata.Bindings[1])
	if _, err := DebugExpression("1", metadata, site, locals); err == nil || !strings.Contains(err.Error(), "ambiguous source frame") {
		t.Fatalf("same-line function ambiguity: %v", err)
	}

}

func TestDebugExpressionFloatLimits(t *testing.T) {
	metadata, locals := expressionFixture()
	for _, source := range []string{"1.0 + 2.0", "-(1.0 + 2.0)", "1.0 + 2.0 == 3.0"} {
		if _, err := DebugExpression(source, metadata, diag.Pos{File: "main.bork", Line: 5}, locals); err == nil || !strings.Contains(err.Error(), "Delve does not preserve runtime rounding") {
			t.Fatalf("float arithmetic %s: %v", source, err)
		}
	}
}

func TestDebugListGetPlan(t *testing.T) {
	metadata, locals := expressionFixture()
	metadata.Expressions["[]int64"] = check.DebugShape{Name: "List[Int]", Kind: "list", Element: "int64", Option: "main.Option[int64]", Some: "main.Option_Some[int64]", None: "main.Option_None[int64]"}
	site := diag.Pos{File: "main.bork", Line: 5}
	for _, result := range []string{"true", "false", "invalid"} {
		plan, err := DebugExpressionPlan("r.items.get(range - 1)", metadata, site, locals)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Predicate != "range_-1 >= 0 && range_-1 < len(r.items)" || plan.Type != "main.Option[int64]" {
			t.Fatalf("plan: %+v", plan)
		}
		err = plan.Select(result)
		if result == "invalid" {
			if err == nil {
				t.Fatal("accepted invalid predicate result")
			}
			continue
		}
		want := "main.Option_Some[int64]{E0: r.items[range_-1]}"
		if result == "false" {
			want = "main.Option_None[int64]{}"
		}
		if err != nil || plan.Expression != want || plan.Predicate != "" {
			t.Fatalf("selected %s: %+v, %v", result, plan, err)
		}
	}
	for _, test := range []struct{ source, message string }{
		{"r.items.get(true)", "must be Int"}, {"r.items.get(n)", "must be Int"},
		{"r.items.get()", "one Int index"}, {"r.items.get(0, 1)", "one Int index"},
		{"r.items.get(0).getOr(1)", "standalone List.get"},
		{"r.items.get(0) == r.items.get(1)", "standalone List.get"},
		{"r.items.get(r.deferred)", "deferred field"}, {"r.deferred.get(0)", "collection lookup"},
	} {
		_, err := DebugExpressionPlan(test.source, metadata, site, locals)
		if err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("%s: %v; want %s", test.source, err, test.message)
		}
	}
	metadata.Expressions["[]int64"] = check.DebugShape{Name: "List[Int]", Kind: "list", Element: "int64"}
	if _, err := DebugExpressionPlan("r.items.get(0)", metadata, site, locals); err == nil || !strings.Contains(err.Error(), "concrete Option") {
		t.Fatalf("unavailable Option types: %v", err)
	}
}
