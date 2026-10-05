package check

import "github.com/GiGurra/bork/internal/syntax"

// removedChannelCall explains the channel functions that became methods.
// Like removedBytesCall, it only handles unresolved names. There is no
// automatic fix: the methods that wait need a scope to wait in, which only
// the author can choose.
func (c *checker) removedChannelCall(call *syntax.Call, id *syntax.Ident) bool {
	replacement := ""
	switch id.Name {
	case "send":
		replacement = "ch.send(s, x), giving the scope to wait in"
	case "receive":
		replacement = "ch.receive(s), giving the scope to wait in"
	case "closeChannel":
		replacement = "ch.close()"
	case "received":
		replacement = "ch.toList(s), or for (x in ch.values(s)) { ... }"
	default:
		return false
	}
	c.diags.AddCode(id.Pos, "migration."+id.Name, "%s was removed; use %s", id.Name, replacement)
	return true
}
