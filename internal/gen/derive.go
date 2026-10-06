package gen

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"

	"github.com/GiGurra/bork/internal/check"
)

// Checked source templates implement structural codecs; these helpers remain
// for printing generated Go.

func (g *gen) text(x ast.Node) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, token.NewFileSet(), x)
	return buf.String()
}

func (g *gen) typeText(t check.Type) string { return g.text(g.goType(t)) }
