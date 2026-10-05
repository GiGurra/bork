package check

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// Unsafe Go can name exported Bork declarations through their import alias.
// Count those references while respecting native imports and local shadowing.
func (c *checker) markUnsafeGoImports(files []*syntax.File) {
	for _, file := range files {
		pkg := c.pkgs[file.Package]
		if pkg == nil {
			continue
		}
		candidates := map[string]*Package{}
		for _, imp := range file.Imports {
			if target := pkg.imports[imp.Name]; target != nil && !pkg.used[imp.Name] {
				candidates[imp.Name] = target
			}
		}
		if len(candidates) == 0 {
			continue
		}
		for _, fd := range file.Funcs {
			if fd.GoBody == nil {
				continue
			}
			body := fd.GoBody.Body
			relevant := false
			for alias := range candidates {
				relevant = relevant || strings.Contains(body, alias)
			}
			if !relevant {
				continue
			}
			native, err := parser.ParseFile(token.NewFileSet(), "", "package p\nfunc _(){"+body+"}", 0)
			if err != nil {
				continue
			}
			params := map[string]bool{}
			for _, param := range fd.Params {
				params[param.Name] = true
			}
			if fn := c.info.FuncOf[fd]; fn != nil {
				for _, need := range fn.NeedVars {
					params[need.Name] = true
				}
			}
			ast.Inspect(native, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				alias, ok := selector.X.(*ast.Ident)
				if !ok || alias.Obj != nil || params[alias.Name] || c.info.GoImportNames[fd.GoBody][alias.Name] != "" {
					return true
				}
				target := candidates[alias.Name]
				if target == nil || !Exported(selector.Sel.Name) {
					return true
				}
				member := selector.Sel.Name
				valid := target.TypeNamed(member) != nil || target.ClassNamed(member) != nil || target.Funcs[member] != nil
				if base, variant, ok := strings.Cut(member, "_"); !valid && ok {
					if sealed, ok := target.TypeNamed(base).(*Sealed); ok {
						valid = sealed.Variant(variant) != nil
					}
				}
				if valid {
					pkg.used[alias.Name] = true
				}
				return true
			})
		}
	}
}
