package check

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestComptimeTaggedTransportValidation(t *testing.T) {
	sealed := &Sealed{Name: "Value"}
	sealed.Variants = []*Variant{{Name: "N", Parent: sealed, Fields: []*Field{{Name: "n", Type: Int}}}}
	union := &Union{Members: []Type{Int, String}}
	mapType := &Map{Key: String, Value: Int}
	record := &Record{Name: "R", Fields: []*Field{{Name: "n", Type: Int}}}
	for _, tc := range []struct {
		name  string
		typ   Type
		value comptimeValue
		want  string
	}{
		{"unknown variant", sealed, comptimeValue{Tag: "Wrong"}, "malformed sealed"},
		{"missing variant field", sealed, comptimeValue{Tag: "N"}, "malformed sealed"},
		{"union tag range", union, comptimeValue{Tag: "2", Items: []comptimeValue{{Kind: "Int", Text: "1"}}}, "malformed union"},
		{"union member type", union, comptimeValue{Tag: "0", Items: []comptimeValue{{Kind: "String", Text: "eA=="}}}, "expected result type"},
		{"odd map entries", mapType, comptimeValue{Items: []comptimeValue{{Kind: "String", Text: "eA=="}}}, "malformed map"},
		{"unexpected record tag", record, comptimeValue{Tag: "N", Items: []comptimeValue{{Kind: "Int", Text: "1"}}}, "malformed record"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.value.Kind = TypeText(tc.typ, nil)
			data, err := json.Marshal(struct {
				Version int
				Value   comptimeValue
			}{ComptimeSchemaVersion, tc.value})
			if err != nil {
				t.Fatal(err)
			}
			_, err = DecodeComptime(&Comptime{expr: expr{typ: tc.typ}}, data)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}
