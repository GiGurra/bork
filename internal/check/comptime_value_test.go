package check

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestComptimeTransportValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typ   Type
		value comptimeValue
		want  string
	}{
		{"signed width", Int8, comptimeValue{Kind: "Int8", Text: "128"}, "value out of range"},
		{"unsigned sign", Uint64, comptimeValue{Kind: "Uint64", Text: "-1"}, "invalid syntax"},
		{"wrong type", Int, comptimeValue{Kind: "String", Text: "1"}, "expected result type"},
		{"scalar children", Bool, comptimeValue{Kind: "Bool", Text: "true", Items: []comptimeValue{{}}}, "malformed scalar"},
		{"float width", Float32, comptimeValue{Kind: "Float32", Text: "100000000"}, "out of range"},
		{"invalid string bytes", String, comptimeValue{Kind: "String", Text: "!"}, "illegal base64"},
		{"nil with children", &List{Elem: Int}, comptimeValue{Kind: "List[Int]", Nil: true, Items: []comptimeValue{{Kind: "Int", Text: "1"}}}, "malformed list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(struct {
				Version int
				Value   comptimeValue
			}{1, tc.value})
			if err != nil {
				t.Fatal(err)
			}
			_, err = DecodeComptime(&Comptime{expr: expr{typ: tc.typ}}, data)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	for _, data := range []string{
		`{"Version":2,"Value":{"Kind":"Int","Text":"1"}}`,
		`{"Version":1,"Value":{"Kind":"Int","Text":"1","Unknown":true}}`,
		`{"Version":1,"Value":{"Kind":"Int","Text":"1"}} {}`,
	} {
		if _, err := DecodeComptime(&Comptime{expr: expr{typ: Int}}, []byte(data)); err == nil {
			t.Fatalf("accepted malformed transport %s", data)
		}
	}
}

func TestComptimeTransportPreservesValues(t *testing.T) {
	float, err := DecodeComptime(&Comptime{expr: expr{typ: Float}}, []byte(`{"Version":1,"Value":{"Kind":"Float","Text":"8000000000000000"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if float.(*FloatBits).Bits != 0x8000000000000000 {
		t.Fatalf("lost negative zero: %+v", float)
	}
	list, err := DecodeComptime(&Comptime{expr: expr{typ: &List{Elem: Int}}}, []byte(`{"Version":1,"Value":{"Kind":"List[Int]","Nil":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !list.(*ListLit).Nil {
		t.Fatal("lost nil list")
	}
}
