package lsp

import "strings"

func (s *server) extractFunction(path string, p documentParams) any {
	if len(p.Context.Only) > 0 {
		allowed := false
		for _, kind := range p.Context.Only {
			if kind == "refactor.extract" || strings.HasPrefix("refactor.extract", kind+".") {
				allowed = true
			}
		}
		if !allowed {
			return nil
		}
	}
	pkg := s.state(path)
	if pkg == nil || pkg.stale || pkg.analysis == nil {
		return nil
	}
	src := s.source(path)
	if pkg.analysis.Sources()[path] != src {
		return nil
	}
	start, err := compilerPosition(path, src, p.Range.Start)
	if err != nil {
		return nil
	}
	end, err := compilerPosition(path, src, p.Range.End)
	if err != nil {
		return nil
	}
	changed, err := pkg.analysis.EditorExtractFunction(start, end)
	if err != nil {
		return nil
	}
	changes := map[string][]textEdit{fileURI(path): {{sourceRange{position{}, endPosition(src)}, changed}}}
	if err := s.validateWorkspaceEdit(changes); err != nil {
		return nil
	}
	return map[string]any{"title": "Extract function", "kind": "refactor.extract", "edit": map[string]any{"changes": changes}}
}
