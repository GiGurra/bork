package driver

import (
	"fmt"
	"go/constant"
	"path/filepath"
	"testing"
	"time"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/gen"
)

// Collect selections from an already successful clean check. The second Facts
// walk only records them; the benchmark compares actual native executions below.
func closedProofQueries(b testing.TB, path string) (*compiledProgram, []check.Query) {
	b.Helper()
	program, err := checkProgramObserved(path, nil)
	if err != nil {
		b.Fatal(err)
	}
	ds := &diag.List{}
	var queries []check.Query
	check.Facts(program.files, program.info, ds, func(selected []check.Query) ([]bool, error) {
		queries = append(queries, selected...)
		results := make([]bool, len(selected))
		for i := range results {
			results[i] = true
		}
		return results, nil
	})
	if ds.Len() != 0 || len(queries) == 0 {
		b.Fatalf("proof selection: %s, %d queries", ds.Error(), len(queries))
	}
	return program, queries
}

// BenchmarkClosedProofBuild measures Go staging/build/execution after changing
// an argument to a previously uncompiled value. It excludes Bork checking and
// generation; separate editor/build profiles include those costs. Modes keep
// independent stable stage paths. The SDK's Go object cache stays warm.
func BenchmarkClosedProofBuild(b *testing.B) {
	for _, name := range []string{"config", "http_server"} {
		b.Run(name, func(b *testing.B) {
			program, queries := closedProofQueries(b, "../../examples/"+name)
			for _, mode := range []string{"full", "closed"} {
				b.Run(mode, func(b *testing.B) {
					generate := gen.EvalProgram
					if mode == "closed" {
						generate = gen.ClosedProofProgram
					}
					src, err := generate(program.files, program.info, queries)
					if err != nil {
						b.Fatal(err)
					}
					proofProgramOutput(b, program, src, "benchmark-proof-"+mode)
					exe := filepath.Join(b.TempDir(), "proof")
					// A new nonce for every sample prevents count/calibration runs from
					// accidentally measuring previously compiled source versions.
					nonce := time.Now().UnixNano()
					b.ResetTimer()
					for i := range b.N {
						b.StopTimer()
						selected := append([]check.Query(nil), queries...)
						selected[0].Args = append([]constant.Value(nil), queries[0].Args...)
						if name == "config" {
							selected[0].Args[0] = constant.MakeString(fmt.Sprintf("local-%d-%d", nonce, i))
						} else {
							selected[0].Args[0] = constant.MakeInt64(nonce + int64(i))
						}
						source, err := generate(program.files, program.info, selected)
						if err != nil {
							b.Fatal(err)
						}
						b.StartTimer()
						if err := buildGoWithMode(program.files, source, exe, program.module, program.context, "benchmark-proof-"+mode); err != nil {
							b.Fatal(err)
						}
						cmd := program.context.command()
						cmd.Path = exe
						cmd.Args = []string{exe}
						cmd.Env = program.context.processEnv
						if out, err := cmd.CombinedOutput(); err != nil {
							b.Fatalf("execution: %v\n%s", err, out)
						}
					}
					b.ReportMetric(float64(len(src)), "go-bytes")
				})
			}
		})
	}
}
