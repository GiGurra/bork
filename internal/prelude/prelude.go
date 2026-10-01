// Package prelude holds prelude.bork: the built-in types and functions
// that every bork package can use.
package prelude

import (
	_ "embed"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

//go:embed prelude.bork
var src []byte

// Path is the file name prelude positions are reported with.
const Path = "prelude.bork"

// Parse parses the prelude.
func Parse(diags *diag.List) *syntax.File {
	f := syntax.Parse(Path, src, diags)
	f.Prelude = true
	return f
}
