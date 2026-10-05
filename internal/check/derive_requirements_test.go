package check

import (
	"reflect"
	"testing"
)

func TestDeriveRequirementsGroundedClosure(t *testing.T) {
	a := deriveToken{"A", "C"}
	b := deriveToken{"B", "C"}
	c := deriveToken{"C", "D"}
	for _, tc := range []struct {
		name    string
		clauses []deriveClause
		want    deriveBounds
	}{
		{"unrooted cycle", []deriveClause{{[]deriveToken{a}, b}, {[]deriveToken{b}, a}}, deriveBounds{}},
		{"grounded cycle", []deriveClause{{[]deriveToken{a}, b}, {[]deriveToken{b}, a}, {nil, a}}, deriveBounds{"A": {"C": true}, "B": {"C": true}}},
		{"missing conjunct", []deriveClause{{nil, a}, {[]deriveToken{a, b}, c}}, deriveBounds{"A": {"C": true}}},
		{"conjunction", []deriveClause{{[]deriveToken{a, b}, c}, {nil, b}, {nil, a}}, deriveBounds{"A": {"C": true}, "B": {"C": true}, "C": {"D": true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := (&deriveDiscovery{clauses: tc.clauses}).solve()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("requirements: got %#v, want %#v", got, tc.want)
			}
		})
	}
}
