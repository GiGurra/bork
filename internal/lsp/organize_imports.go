package lsp

import (
	"path/filepath"
	"strings"

	"github.com/GiGurra/bork/internal/driver"
)

func (s *server) organizeImports(path string, p documentParams) any {
	if len(p.Context.Only) > 0 {
		allowed := false
		for _, kind := range p.Context.Only {
			if kind == "source.organizeImports" || strings.HasPrefix("source.organizeImports", kind+".") {
				allowed = true
			}
		}
		if !allowed {
			return nil
		}
	}
	src := s.source(path)
	// Sorting keeps paths and aliases unchanged. Removing only diagnosed
	// unused imports preserves the compiler's result; other errors cannot
	// be repaired by this action, so do not offer it while they remain.
	pkg := s.packages[analysisPath(path, src)]
	if pkg == nil {
		pkg = s.state(path)
	}
	if pkg == nil {
		return nil
	}
	for _, d := range pkg.diagnostics {
		file, err := filepath.Abs(d.Pos.File)
		if err != nil || d.Severity != "warning" && (file != path || d.Code != "import.unused") {
			return nil
		}
	}
	organized, err := driver.EditorOrganizeImports(path, src, s.diagnostics[path])
	if err != nil || organized == src {
		return nil
	}
	changes := map[string][]textEdit{fileURI(path): {{sourceRange{position{}, endPosition(src)}, organized}}}
	return map[string]any{"title": "Organize imports", "kind": "source.organizeImports", "edit": map[string]any{"changes": changes}}
}
