package check

import (
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// removedTaskCall explains that spawn and launch became fork. Like
// removedBytesCall, it only handles unresolved names. The fix renames the
// call; the caller then checks it as a call to fork, so its task keeps a
// type and no further errors follow.
func (c *checker) removedTaskCall(id *syntax.Ident) bool {
	if id.Name != "spawn" && id.Name != "launch" {
		return false
	}
	code := "migration." + id.Name
	c.diags.AddCode(id.Pos, code, "%s was removed; use fork(s, () => ...), which starts a task of s and gives a Task[T] (Task[Ok] for work with no value)", id.Name)
	end := id.Pos
	end.Col += len(id.Name)
	c.diags.Suggest(id.Pos, code, end, diag.Fix{Message: "replace " + id.Name + " with fork", Edits: []diag.TextEdit{{Start: id.Pos, End: end, Replacement: "fork"}}})
	return true
}
