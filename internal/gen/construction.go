package gen

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/GiGurra/bork/internal/check"
)

// constructionChecks validates completed stored inputs before exposing a value
// with its promised facts. Computed cells are initialized between validation
// phases, then variant and whole-value invariants are checked.
func (g *gen) constructionChecks(fields []*check.Field, owner check.Type, invariants []constructionInvariant, errorType check.Type) string {
	var b strings.Builder
	fieldPath := func(field *check.Field) string {
		if len(invariants) > 0 && invariants[0].positional {
			return ".values[" + field.Name + "]"
		}
		return "." + field.Name
	}
	phases := 1
	if hasComputedFields(fields) {
		phases = 2
	}
	for phase := 0; phase < phases; phase++ {
		if phase == 1 {
			for _, stmt := range g.computedCells(ast.NewIdent("_out"), fields, owner) {
				b.WriteString(g.text(stmt) + "\n")
			}
		}
		for _, f := range fields {
			for _, con := range f.Constraints {
				early := !f.Computed && !con.HasSiblingArgs()
				if phases == 2 && (phase == 0) != early {
					continue
				}
				stmts := g.atFailurePath(g.fieldRead(ast.NewIdent("_out"), f), f.Type, splitPath(con.Path), stringLit(fieldPath(f)), func(x ast.Expr, t check.Type, path ast.Expr) []ast.Stmt {
					runtime := fieldConstraint(con, func(n string) string {
						for _, sibling := range fields {
							if sibling.Name == n {
								return g.fieldReadText("_out", sibling)
							}
						}
						return name(n).Name
					})
					cond := g.constraintCond(runtime, x, t)
					if cond == nil {
						return nil
					}
					setup, failurePath, message := g.constraintFailure(runtime, x, t, path, con)
					ret := &ast.ReturnStmt{Results: []ast.Expr{&ast.CompositeLit{Type: g.goType(errorType), Elts: []ast.Expr{
						&ast.KeyValueExpr{Key: ast.NewIdent("path"), Value: failurePath},
						&ast.KeyValueExpr{Key: ast.NewIdent("message"), Value: message},
					}}}}
					return []ast.Stmt{&ast.IfStmt{Cond: &ast.UnaryExpr{Op: token.NOT, X: paren(cond)}, Body: &ast.BlockStmt{List: append(setup, ret)}}}
				})
				for _, s := range stmts {
					b.WriteString(g.text(s) + "\n")
				}
			}
		}
	}

	for _, inv := range invariants {
		for _, con := range inv.constraints {
			if cond := g.constraintCond(con, ast.NewIdent("_out"), inv.typ); cond != nil {
				setup, path, message := g.constraintFailure(con, ast.NewIdent("_out"), inv.typ, stringLit(""))
				fmt.Fprintf(&b, "if !(%s) {\n", g.text(cond))
				for _, stmt := range setup {
					b.WriteString(g.text(stmt) + "\n")
				}
				b.WriteString(g.constructionError(errorType, g.text(path), g.text(message)) + "}\n")
			}
		}
	}
	return b.String()
}

func (g *gen) constructionError(errorType check.Type, path, message string) string {
	return fmt.Sprintf("return %s{path: %s, message: %s}\n", g.typeText(errorType), path, message)
}
