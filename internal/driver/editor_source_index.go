package driver

import (
	"path/filepath"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/std"
)

func (a *EditorAnalysis) sourceIndex() *check.SymbolIndex {
	if a.symbols == nil {
		a.symbols = check.BuildSourceIndex(a.program.files, a.program.info)
	}
	return a.symbols
}
func (a *EditorAnalysis) sourcePosition(pos diag.Pos) diag.Pos {
	for _, file := range a.program.files {
		absolute, err := filepath.Abs(file.Path)
		if err == nil && absolute == pos.File {
			pos.File = file.Path
			break
		}
	}
	return pos
}
func (a *EditorAnalysis) absoluteSourcePosition(pos diag.Pos) diag.Pos {
	for _, file := range a.program.files {
		if file.Path == pos.File {
			if !file.Prelude && !strings.HasPrefix(file.Package, std.Prefix) {
				if absolute, err := filepath.Abs(pos.File); err == nil {
					pos.File = absolute
				}
			}
			break
		}
	}
	return pos
}

// ReferenceAt resolves a source token through the checker-owned identity index.
func (a *EditorAnalysis) ReferenceAt(pos diag.Pos) *check.SourceReference {
	ref := a.sourceIndex().At(a.sourcePosition(pos))
	if ref == nil {
		return nil
	}
	ref.Start = a.absoluteSourcePosition(ref.Start)
	ref.End = a.absoluteSourcePosition(ref.End)
	ref.Definition = a.absoluteSourcePosition(ref.Definition)
	return ref
}
func (a *EditorAnalysis) References(def diag.Pos) []check.SourceReference {
	refs := a.sourceIndex().References(a.sourcePosition(def))
	for i := range refs {
		refs[i].Start = a.absoluteSourcePosition(refs[i].Start)
		refs[i].End = a.absoluteSourcePosition(refs[i].End)
		refs[i].Definition = a.absoluteSourcePosition(refs[i].Definition)
	}
	return refs
}
func (a *EditorAnalysis) Symbols() []check.Symbol {
	symbols := a.sourceIndex().Symbols()
	for i := range symbols {
		symbols[i].Declaration = a.absoluteSourcePosition(symbols[i].Declaration)
		symbols[i].Definition = a.absoluteSourcePosition(symbols[i].Definition)
		symbols[i].End = a.absoluteSourcePosition(symbols[i].End)
	}
	return symbols
}

func (a *EditorAnalysis) AllReferences() []check.SourceReference {
	refs := a.sourceIndex().AllReferences()
	for i := range refs {
		refs[i].Start = a.absoluteSourcePosition(refs[i].Start)
		refs[i].End = a.absoluteSourcePosition(refs[i].End)
		refs[i].Definition = a.absoluteSourcePosition(refs[i].Definition)
	}
	return refs
}
