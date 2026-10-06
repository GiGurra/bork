// Package prelude holds the built-in types and functions
// that every bork package can use.
package prelude

import (
	"embed"
	"io/fs"
	"path"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/std"
	"github.com/GiGurra/bork/internal/syntax"
)

//go:embed *.bork
var sources embed.FS

// Parse parses the prelude, followed by the standard packages it imports.
// Those packages are ordinary packages: they are not part of the prelude, and
// a user import of the same path shares them.
func Parse(diags *diag.List) []*syntax.File {
	names, err := fs.Glob(sources, "*.bork")
	if err != nil {
		panic(err)
	}
	var paths []string
	var srcs [][]byte
	for _, name := range names {
		src, err := sources.ReadFile(name)
		if err != nil {
			panic(err)
		}
		paths = append(paths, path.Join("prelude", name))
		srcs = append(srcs, src)
	}
	files := syntax.ParseFiles(paths, srcs, true, diags)
	for _, f := range files {
		f.Prelude = true
	}
	loaded := map[string]bool{}
	var load func(*syntax.File)
	load = func(f *syntax.File) {
		for _, imp := range f.Imports {
			if loaded[imp.Path] {
				continue
			}
			loaded[imp.Path] = true
			paths, srcs, ok := std.Sources(imp.Path)
			if !ok {
				panic("prelude imports unknown standard package " + imp.Path)
			}
			imported := syntax.ParseFiles(paths, srcs, true, diags)
			for _, file := range imported {
				file.Package = imp.Path
			}
			files = append(files, imported...)
			for _, file := range imported {
				load(file)
			}
		}
	}
	for _, f := range files[:len(names)] {
		load(f)
	}
	return files
}
