package gen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"

	"github.com/GiGurra/bork/internal/syntax"
)

// Rewrite file-local package aliases to unique generated import names. Parser
// objects distinguish local shadowing from package references. Text edits retain
// the original body layout and line directives.
func (g *gen) goBodyAliases(fd *syntax.FuncDecl) string {
	body := fd.GoBody.Body
	imports := g.info.GoImportNames[fd.GoBody]
	if len(imports) == 0 {
		return body
	}
	const prefix = "package p\nfunc _() {"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", prefix+body+"}\n", 0)
	if err != nil {
		return body
	} // syntax checking has already reported errors
	params := map[string]bool{}
	for _, p := range fd.Params {
		params[p.Name] = true
	}
	for _, v := range g.info.FuncOf[fd].NeedVars {
		params[varIdent(v).Name] = true
	}
	type edit struct {
		offset            int
		name, replacement string
	}
	var edits []edit
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		name, ok := selector.X.(*ast.Ident)
		if !ok || name.Obj != nil || params[name.Name] {
			return true
		}
		if path, ok := imports[name.Name]; ok {
			edits = append(edits, edit{fset.Position(name.Pos()).Offset - len(prefix), name.Name, g.goImport(path)})
		}
		return true
	})
	slices.SortFunc(edits, func(a, b edit) int { return b.offset - a.offset })
	for _, change := range edits {
		body = body[:change.offset] + change.replacement + body[change.offset+len(change.name):]
	}
	return body
}
