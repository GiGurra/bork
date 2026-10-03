// Package prelude holds the built-in types and functions
// that every bork package can use.
package prelude

import (
	"embed"
	"io/fs"
	"path"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

//go:embed *.bork
var sources embed.FS

// Parse parses the prelude.
func Parse(diags *diag.List) []*syntax.File {
	names, err := fs.Glob(sources, "*.bork")
	if err != nil {
		panic(err)
	}
	var files []*syntax.File
	for _, name := range names {
		src, err := sources.ReadFile(name)
		if err != nil {
			panic(err)
		}
		f := syntax.ParseEmbedded(path.Join("prelude", name), src, diags)
		f.Prelude = true
		files = append(files, f)
	}
	return files
}
