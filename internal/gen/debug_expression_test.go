package gen

import (
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
)

func expressionFixture() (*DebugMap, []check.DebugLocal) {
	metadata := &DebugMap{Names: map[string]string{"range_": "range", "_rebind_2_3_n": "n"}, Expressions: map[string]check.DebugShape{
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
		{"^range & 3", "(^range_) & 3"}, {`"hé" + "llo"`, `"hé" + "llo"`},
		{"(1 + 2) * 3", "9"}, {"'å'", "229"}, {"1.5", "1.5"}, {"r.items", "r.items"},
	} {
		t.Run(test.source, func(t *testing.T) {
			metadata, locals := expressionFixture()
			got, err := DebugExpression(test.source, metadata, locals)
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
			_, err := DebugExpression(test.source, metadata, locals)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("got %v; want %q", err, test.message)
			}
		})
	}
	metadata, locals := expressionFixture()
	locals = append(locals, locals[0])
	if _, err := DebugExpression("range", metadata, locals); err == nil || !strings.Contains(err.Error(), "ambiguous local") {
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
