package driver

import (
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Each package contributes one constant recipe. evaluateComptimes provides the
// native target policy, bounded transport, proof checks and execution deadline.
// Results are shared only within this build; no independent evaluator cache is
// safe without a certified execution receipt.
func evaluateInterpolationValidators(files []*syntax.File, info *check.Info, diags *diag.List, module *goModuleInputs, context *goContext, usage *goUsage) {
	results := map[*check.Call]check.Expr{}
	for _, batch := range info.InterpolationBatches {
		original := batch.Recipe.Body.Tail.(*check.ListLit)
		values := *original
		values.Elems = nil
		for _, value := range original.Elems {
			if results[value.(*check.Call)] == nil {
				values.Elems = append(values.Elems, value)
			}
		}
		if len(values.Elems) > 0 {
			body := *batch.Recipe.Body
			body.Tail = &values
			recipe := *batch.Recipe
			recipe.Body = &body
			batchInfo := *info
			batchInfo.Comptimes = []*check.Comptime{&recipe}
			failures := &diag.List{}
			evaluateComptimes(files, &batchInfo, failures, module, context, usage)
			if failures.Len() > 0 {
				var names []string
				seen := map[string]bool{}
				for _, site := range batch.Sites {
					if !seen[site.Validator] {
						names = append(names, site.Validator)
						seen[site.Validator] = true
					}
				}
				for _, failure := range failures.Sorted() {
					diags.AddCode(batch.Recipe.Pos(), "interpolation.validator", "%s (validator %s)", failure.Msg, strings.Join(names, ", "))
				}
				return
			}
			decoded := recipe.Value.(*check.ListLit)
			for i, call := range values.Elems {
				results[call.(*check.Call)] = decoded.Elems[i]
			}
		}
		decoded := *original
		decoded.Elems = nil
		for _, call := range original.Elems {
			decoded.Elems = append(decoded.Elems, results[call.(*check.Call)])
		}
		batch.Recipe.Value = &decoded
	}
	for _, batch := range info.InterpolationBatches {
		check.InterpolationDiagnostics(batch, diags)
	}
}
