// Package std holds bork's standard packages, imported as "bork/name"
// (import "bork/http"). They are written in bork, with unsafe go where
// they reach Go's libraries, and embedded in the compiler.
package std

import (
	"embed"
	"io/fs"
	"path"
	"strings"
)

//go:embed */*.bork */go-deps.mod */go-deps.sum
var files embed.FS

// Prefix starts the import paths of standard packages.
const Prefix = "bork/"

// Sources returns the files of the standard package with the given
// import path: their paths (as positions show them) and contents.
func Sources(importPath string) (paths []string, srcs [][]byte, ok bool) {
	dir, found := strings.CutPrefix(importPath, Prefix)
	if !found {
		return nil, nil, false
	}
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return nil, nil, false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".bork") {
			continue
		}
		src, err := files.ReadFile(path.Join(dir, e.Name()))
		if err != nil {
			return nil, nil, false
		}
		paths = append(paths, path.Join(importPath, e.Name()))
		srcs = append(srcs, src)
	}
	return paths, srcs, len(paths) > 0
}
