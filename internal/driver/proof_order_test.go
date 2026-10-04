package driver

import (
	"fmt"
	"go/constant"
	"slices"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
)

func TestFieldDefaultProofOrder(t *testing.T) {
	t.Parallel()
	var source strings.Builder
	source.WriteString("fn identity(n:Int):Int{n}\npred Positive(n:Int){identity(n)>0}\ntype Defaults={\n")
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&source, " f%d:Int where Positive=%d\n", i, i)
	}
	source.WriteString("}\n")
	program := predicateMemoProgram(t, source.String())
	for range 16 {
		ds := &diag.List{}
		var arguments []int64
		check.Facts(program.files, program.info, ds, func(queries []check.Query) ([]bool, error) {
			results := make([]bool, len(queries))
			for i, query := range queries {
				if query.Pred == nil || query.Pred.Decl.Name != "Positive" || len(query.Args) != 1 {
					t.Fatalf("unexpected default query: %+v", query)
				}
				value, ok := constant.Int64Val(query.Args[0])
				if !ok {
					t.Fatal("noninteger default")
				}
				arguments = append(arguments, value)
				results[i] = true
			}
			return results, nil
		})
		if ds.Len() != 0 || !slices.Equal(arguments, []int64{1, 2, 3, 4, 5, 6, 7, 8}) {
			t.Fatalf("default proof order %v, diagnostics %s", arguments, ds.Error())
		}
	}
}
