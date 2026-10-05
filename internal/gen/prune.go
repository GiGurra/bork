package gen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"
)

// pruneHelpers drops unreachable free helpers before printing them. User
// functions/instances, methods and global declarations remain roots, preserving
// unsafe Go validation, dynamic method dispatch and package initialization.
func (g *gen) pruneHelpers(decls, runtime []ast.Decl, goFuncs []string) ([]ast.Decl, []ast.Decl, []string, error) {
	preserved := map[string]bool{"main": true, "init": true}
	for _, fn := range g.info.FuncOf {
		if !fn.Prelude {
			preserved[g.funcName(fn).Name] = true
		}
	}
	for _, ci := range g.info.ClassInstances {
		if !ci.Prelude {
			preserved[instName(ci)] = true
		}
	}
	const prefix = "package main\n"
	raw := []byte(prefix + strings.Join(goFuncs, "\n"))
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "helpers.go", raw, parser.ParseComments)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parsing generated helpers: %w", err)
	}
	all := make([]ast.Decl, 0, len(decls)+len(runtime)+len(file.Decls))
	all = append(all, decls...)
	all = append(all, runtime...)
	all = append(all, file.Decls...)
	live := liveFunctions(all, preserved, file.Comments)
	allPackages, livePackages := packageUses(all, live)
	preserveImports(decls, allPackages, livePackages)
	filtered := removeFunctions(raw, fset, file.Decls, live)
	if !bytes.Equal(filtered, raw) {
		goFuncs = []string{string(filtered[len(prefix):])}
	}
	return liveDecls(decls, live), liveDecls(runtime, live), goFuncs, nil
}

func liveFunctions(decls []ast.Decl, preserved map[string]bool, comments []*ast.CommentGroup) map[*ast.FuncDecl]bool {
	functions := map[string]*ast.FuncDecl{}
	for _, decl := range decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
			functions[fn.Name.Name] = fn
		}
	}
	live := map[*ast.FuncDecl]bool{}
	var visit func(ast.Node)
	visit = func(node ast.Node) {
		ast.Inspect(node, func(node ast.Node) bool {
			if name, ok := node.(*ast.Ident); ok {
				if fn := functions[name.Name]; fn != nil && !live[fn] {
					live[fn] = true
					visit(fn)
				}
			}
			return true
		})
	}
	for _, decl := range decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			if fn.Recv != nil || preserved[fn.Name.Name] {
				live[fn] = true
				visit(fn)
			}
		} else if group, ok := decl.(*ast.GenDecl); !ok || group.Tok != token.IMPORT {
			visit(decl)
		}
	}
	// Linkname directives can reference a helper without an identifier in code.
	for _, group := range comments {
		for _, comment := range group.List {
			if words := strings.Fields(comment.Text); len(words) >= 2 && words[0] == "//go:linkname" {
				if fn := functions[words[1]]; fn != nil && !live[fn] {
					live[fn] = true
					visit(fn)
				}
			}
		}
	}
	return live
}

func liveDecls(decls []ast.Decl, live map[*ast.FuncDecl]bool) []ast.Decl {
	return slices.DeleteFunc(decls, func(decl ast.Decl) bool {
		fn, ok := decl.(*ast.FuncDecl)
		return ok && !live[fn]
	})
}

func packageUses(decls []ast.Decl, live map[*ast.FuncDecl]bool) (map[string]bool, map[string]bool) {
	all, retained := map[string]bool{}, map[string]bool{}
	for _, decl := range decls {
		fn, isFunc := decl.(*ast.FuncDecl)
		ast.Inspect(decl, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok {
				if name, ok := selector.X.(*ast.Ident); ok {
					all[name.Name] = true
					if !isFunc || live[fn] {
						retained[name.Name] = true
					}
				}
			}
			return true
		})
	}
	return all, retained
}

func preserveImports(decls []ast.Decl, all, live map[string]bool) {
	for _, decl := range decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.IMPORT {
			continue
		}
		for _, item := range group.Specs {
			spec := item.(*ast.ImportSpec)
			name := ""
			if spec.Name != nil {
				name = spec.Name.Name
			} else {
				imported, _ := strconv.Unquote(spec.Path.Value)
				name = importName(imported)
			}
			// Preserve init effects when only removed helpers used a package. Leave
			// originally unused imports unchanged so their Go errors remain visible.
			if name != "_" && name != "." && all[name] && !live[name] {
				spec.Name = ast.NewIdent("_")
			}
		}
	}
}

// importName is the name an unnamed import declares: its path's last
// element, or the one before a major version suffix (math/rand/v2).
func importName(imported string) string {
	name := path.Base(imported)
	if dir := path.Dir(imported); dir != "." && len(name) > 1 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" {
		return path.Base(dir)
	}
	return name
}

// Remove raw declarations by physical byte offset, retaining unsafe Go layout
// and line directives. Synthetic/runtime ASTs are filtered before printing.
func removeFunctions(src []byte, fset *token.FileSet, decls []ast.Decl, live map[*ast.FuncDecl]bool) []byte {
	type span struct{ start, end int }
	var spans []span
	for _, decl := range decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && !live[fn] {
			file := fset.File(fn.Pos())
			spans = append(spans, span{file.Offset(fn.Pos()), file.Offset(fn.End())})
		}
	}
	if len(spans) == 0 {
		return src
	}
	var out bytes.Buffer
	out.Grow(len(src))
	last := 0
	for _, s := range spans {
		out.Write(src[last:s.start])
		last = s.end
	}
	out.Write(src[last:])
	return out.Bytes()
}
