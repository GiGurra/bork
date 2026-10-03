package gen

import (
	"fmt"
	"go/ast"

	"github.com/GiGurra/bork/internal/diag"
)

// callerLocation forwards an internal helper's caller, otherwise embeds the
// source position. Function values capture this expression where named.
func (g *gen) callerLocation(pos diag.Pos) ast.Expr {
	if g.callerAt != nil {
		return g.callerAt
	}
	if pos.Col == 0 {
		return strLit(fmt.Sprintf("%s:%d", pos.File, pos.Line))
	}
	return at(pos)
}
