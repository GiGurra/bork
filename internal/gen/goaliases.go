package gen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/syntax"
)

// Rewrite file-local package aliases to unique generated import names. Parser
// objects distinguish local shadowing from package references. Text edits leave the rest of
// the original body intact.
func (g *gen) goBodyAliases(fd *syntax.FuncDecl) string {
	body := fd.GoBody.Body
	imports := g.info.GoImportNames[fd.GoBody]
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
		offset, length int
		replacement    string
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
			edits = append(edits, edit{fset.Position(name.Pos()).Offset - len(prefix), len(name.Name), g.goImport(path)})
			return true
		}
		pkg := g.info.FuncOf[fd].Pkg.Imported(name.Name)
		if pkg == nil || !check.Exported(selector.Sel.Name) {
			return true
		}
		member := selector.Sel.Name
		var generated string
		if typ := pkg.TypeNamed(member); typ != nil {
			g.goType(typ)
			generated = typeName(member, pkg).Name
		} else if class := pkg.ClassNamed(member); class != nil {
			generated = className(class).Name
		} else if fn := pkg.Funcs[member]; fn != nil {
			generated = g.funcName(fn).Name
		} else if base, variant, ok := strings.Cut(member, "_"); ok {
			if sealed, ok := pkg.TypeNamed(base).(*check.Sealed); ok {
				for _, v := range sealed.Variants {
					if v.Name == variant {
						g.goType(sealed)
						generated = variantName(v).Name
						break
					}
				}
			}
		}
		if generated != "" {
			start := fset.Position(selector.Pos()).Offset - len(prefix)
			end := fset.Position(selector.End()).Offset - len(prefix)
			edits = append(edits, edit{start, end - start, generated})
		}
		return true
	})
	slices.SortFunc(edits, func(a, b edit) int { return b.offset - a.offset })
	for _, change := range edits {
		body = body[:change.offset] + change.replacement + body[change.offset+change.length:]
	}
	return body
}
