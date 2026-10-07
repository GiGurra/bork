package check

import "github.com/GiGurra/bork/internal/diag"

// MigrationWarnings reports deprecated spellings with precise edits.
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
	for _, list := range info.legacyStaged {
		const code = "migration.comptime-comprehension"
		warnings.Warn(list.Pos, code, "[comptime for (x in xs) comptime if (c) v] is the older form; write comptime for { x in xs; if c } yield v")
		warnings.Suggest(list.Pos, code, list.End, diag.Fix{
			Message: "rewrite as comptime for { ... } yield",
			Edits:   list.Edits,
		})
	}
	return warnings
}
