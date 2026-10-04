package driver

import (
	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
	"strings"
)

// Each package contributes one constant recipe. evaluateComptimes provides the
// native target policy, bounded transport, proof checks and execution deadline.
// Results are shared only within this build; no independent evaluator cache is
// safe without a certified execution receipt.
func evaluateInterpolationValidators(files []*syntax.File, info *check.Info, diags *diag.List, module *goModuleInputs, context *goContext, usage *goUsage) {
	batchInfo := *info
	batchInfo.Comptimes = nil
	for _, batch := range info.InterpolationBatches {
		batchInfo.Comptimes = append(batchInfo.Comptimes, batch.Recipe)
	}
	failures := &diag.List{}
	evaluateComptimes(files, &batchInfo, failures, module, context, usage)
	if failures.Len() > 0 {
		for _, failure := range failures.Sorted() {
			batch := info.InterpolationBatches[0]
			for _, candidate := range info.InterpolationBatches {
				if candidate.Recipe.Pos() == failure.Pos {
					batch = candidate
					break
				}
			}
			var names []string
			seen := map[string]bool{}
			for _, site := range batch.Sites {
				if !seen[site.Validator] {
					names = append(names, site.Validator)
					seen[site.Validator] = true
				}
			}
			diags.AddCode(batch.Recipe.Pos(), "interpolation.validator", "%s (validator %s)", failure.Msg, strings.Join(names, ", "))
		}
		return
	}
	for _, batch := range info.InterpolationBatches {
		check.InterpolationDiagnostics(batch, diags)
	}
}
