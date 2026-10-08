package check

import (
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

func (c *checker) removedProcessExit(id *syntax.Ident) bool {
	pkg, member, ok := c.qualified(id.Name)
	if !ok || pkg.Path != "bork/process" || member != "Exit" {
		return false
	}
	const code = "migration.process-exit"
	c.diags.AddCode(id.Pos, code, "process.Exit was renamed to process.ExitNow: it skips scope cleanup; prefer returning a failure from main")
	end := id.Pos
	end.Col += len(id.Name)
	c.diags.Suggest(id.Pos, code, end, diag.Fix{Message: "rename to ExitNow", Edits: []diag.TextEdit{{Start: end, End: end, Replacement: "Now"}}})
	return true
}
