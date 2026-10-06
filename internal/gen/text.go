package gen

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"

	"github.com/GiGurra/bork/internal/check"
)

func (g *gen) text(x ast.Node) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, token.NewFileSet(), x)
	return buf.String()
}

func (g *gen) typeText(t check.Type) string { return g.text(g.goType(t)) }
