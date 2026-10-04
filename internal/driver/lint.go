package driver

import (
	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
)

// Lint checks a package and returns advisory compiler-backed diagnostics.
func Lint(path string) ([]diag.Diagnostic, error) {
	files, info, err := Check(path)
	if err != nil {
		return nil, err
	}
	warnings := check.LintWarnings(files, info)
	warnings.Append(check.DebugWarnings(info))
	warnings.Append(check.LazyWarnings(info))
	warnings.Append(check.MigrationWarnings(info))
	return warnings.Sorted(), nil
}
