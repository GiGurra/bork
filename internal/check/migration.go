package check

import "github.com/GiGurra/bork/internal/diag"

// MigrationWarnings reports deprecated type spellings with precise edits.
func MigrationWarnings(info *Info) *diag.List {
	warnings := &diag.List{}
	for written, typ := range info.writtenTypes {
		if written.Name != "Unit" || typ != Ok {
			continue
		}
		start := written.Pos
		end := start
		end.Col += len("Unit")
		const code = "migration.unit"
		warnings.Warn(start, code, "Unit is deprecated; use Ok")
		warnings.Suggest(start, code, end, diag.Fix{
			Message: "replace Unit with Ok",
			Edits:   []diag.TextEdit{{Start: start, End: end, Replacement: "Ok"}},
		})
	}
	return warnings
}
