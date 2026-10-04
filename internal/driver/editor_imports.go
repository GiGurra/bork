package driver

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/std"
	"github.com/GiGurra/bork/internal/syntax"
)

var standardEditorSymbols = sync.OnceValue(func() []EditorCompletion {
	var out []EditorCompletion
	for _, pkg := range std.Packages() {
		paths, srcs, _ := std.Sources(pkg)
		out = append(out, editorImportSymbols(pkg, paths, srcs)...)
	}
	return out
})

func editorImportSymbols(pkg string, paths []string, srcs [][]byte) []EditorCompletion {
	var out []EditorCompletion
	for _, file := range syntax.ParseFiles(paths, srcs, strings.HasPrefix(pkg, std.Prefix), &diag.List{}) {
		add := func(name, kind string) {
			if check.Exported(name) {
				out = append(out, EditorCompletion{Name: name, Detail: "from " + pkg, Kind: kind, Rank: 4, ImportPath: pkg})
			}
		}
		for _, fn := range file.Funcs {
			if !fn.IsMethod {
				add(fn.Name, "function")
			}
		}
		for _, typ := range file.Types {
			add(typ.Name, "type")
		}
		for _, b := range file.Bindings {
			add(b.Name, "variable")
		}
	}
	return out
}

// EditorImportSymbols inventories exported declarations in embedded standard
// packages, the current module and the already resolved library graph. It parses source without running code.
func (a *EditorAnalysis) EditorImportSymbols(path string) []EditorCompletion {
	out := append([]EditorCompletion{}, standardEditorSymbols()...)
	mod, err := findModule(filepath.Dir(path))
	if err != nil || mod.path == "" {
		return out
	}
	roots := []module{mod}
	if a.program.module != nil && a.program.module.libraries != nil {
		for _, library := range a.program.module.libraries.libraries {
			roots = append(roots, library.module)
		}
	}
	for _, mod := range roots {
		_ = filepath.WalkDir(mod.root, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				if name != mod.root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor" || entry.Name() == "node_modules" || entry.Name() == "testdata") {
					return filepath.SkipDir
				}
				// Nested modules have a separate import namespace.
				if name != mod.root {
					if _, err := os.Stat(filepath.Join(name, ModFile)); err == nil {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if !strings.HasSuffix(name, ".bork") || filepath.Dir(name) == filepath.Dir(path) {
				return nil
			}
			text, err := os.ReadFile(name)
			if err != nil {
				return nil
			}
			abs, _ := filepath.Abs(name)
			if overlay, ok := a.overlays[abs]; ok {
				text = []byte(overlay)
			}
			rel, err := filepath.Rel(mod.root, filepath.Dir(name))
			if err != nil {
				return nil
			}
			pkg := mod.path
			if rel != "." {
				pkg += "/" + filepath.ToSlash(rel)
			}
			out = append(out, editorImportSymbols(pkg, []string{name}, [][]byte{text})...)
			return nil
		})
	}
	return out
}
