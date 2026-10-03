package check

import (
	"fmt"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
	"reflect"
	"strings"
	"testing"
)

func BenchmarkTypeExprTraversal(b *testing.B) {
	var source strings.Builder
	for i := range 1000 {
		fmt.Fprintf(&source, "fn value%d(x: Int): Int { x + %d }\n", i, i)
	}
	diags := &diag.List{}
	file := syntax.Parse("main.bork", []byte(source.String()), diags)
	if diags.Len() != 0 {
		b.Fatal(diags)
	}
	c := &checker{info: &Info{}}
	input := reflect.ValueOf(file)
	b.ReportAllocs()
	for b.Loop() {
		count := 0
		c.forTypeExprs(input, func(*syntax.TypeExpr, string) { count++ })
		if count != 2000 {
			b.Fatalf("type expressions = %d, want 2000", count)
		}
	}
}
