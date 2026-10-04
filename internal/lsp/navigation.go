package lsp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/driver"
)

type hierarchyItem struct {
	Name           string      `json:"name"`
	Kind           int         `json:"kind"`
	URI            string      `json:"uri"`
	Range          sourceRange `json:"range"`
	SelectionRange sourceRange `json:"selectionRange"`
	Detail         string      `json:"detail,omitempty"`
}

func navigationKind(kind string) int {
	switch kind {
	case "type", "typeParameter":
		return 5
	case "class":
		return 11
	case "field":
		return 8
	case "variant":
		return 22
	case "method":
		return 6
	case "value":
		return 14
	case "function", "predicate", "test":
		return 12
	case "instance":
		return 23
	default:
		return 13
	}
}

func (s *server) navigationWorkspace(path string) (*driver.EditorWorkspace, error) {
	if s.navigationSessions == nil {
		s.navigationSessions = map[string]*driver.Session{}
	}
	overlays := map[string]string{}
	for path, doc := range s.docs {
		overlays[path] = doc.text
	}
	if path == "" {
		if len(s.workspaceRoots) > 0 {
			path = s.workspaceRoots[0]
		} else if paths := sortedKeys(s.docs); len(paths) > 0 {
			path = paths[0]
		} else {
			return nil, nil
		}
	}
	return driver.AnalyzeNavigationWorkspace(path, s.workspaceRoots, overlays, s.navigationSessions)
}

func (s *server) navigationLocation(item driver.EditorNavigationItem) (location, bool) {
	if !filepath.IsAbs(item.SelectionStart.File) {
		return location{}, false
	}
	src := s.source(item.SelectionStart.File)
	return location{fileURI(item.SelectionStart.File), sourceRange{lspPosition(src, item.SelectionStart), lspPosition(src, item.SelectionEnd)}}, true
}
func (s *server) hierarchyItem(item driver.EditorNavigationItem) (hierarchyItem, bool) {
	loc, ok := s.navigationLocation(item)
	if !ok {
		return hierarchyItem{}, false
	}
	src := s.source(item.SelectionStart.File)
	return hierarchyItem{Name: item.Name, Kind: navigationKind(item.Kind), URI: loc.URI, Range: sourceRange{lspPosition(src, item.Start), lspPosition(src, item.End)}, SelectionRange: loc.Range, Detail: item.Detail}, true
}

func (s *server) navigationFeature(method, path string, p documentParams) (any, error) {
	out := []any{}
	pkg := s.state(path)
	if pkg == nil || pkg.analysis == nil || pkg.stale {
		return out, nil
	}
	src := s.source(path)
	if pkg.analysis.Sources()[path] != src {
		return out, nil
	}
	pos, err := compilerPosition(path, src, p.Position)
	if err != nil {
		return out, nil
	}
	switch method {
	case "textDocument/typeDefinition", "textDocument/implementation":
		var items []driver.EditorNavigationItem
		if method == "textDocument/typeDefinition" {
			items = pkg.analysis.EditorTypeDefinitions(pos)
		} else {
			items = pkg.analysis.EditorImplementations(pos)
		}
		for _, item := range items {
			if loc, ok := s.navigationLocation(item); ok {
				out = append(out, loc)
			}
		}
	case "textDocument/prepareCallHierarchy":
		if item, err := pkg.analysis.EditorCallHierarchy(pos); err == nil {
			if item, ok := s.hierarchyItem(*item); ok {
				out = append(out, item)
			}
		}
	case "textDocument/documentHighlight":
		ref := pkg.analysis.ReferenceAt(pos)
		if ref == nil {
			return out, nil
		}
		for _, use := range pkg.analysis.References(ref.Definition) {
			if use.Start.File != path {
				continue
			}
			kind := 2
			if use.Declaration {
				kind = 3
			}
			out = append(out, map[string]any{"range": sourceRange{lspPosition(src, use.Start), lspPosition(src, use.End)}, "kind": kind})
		}
	}
	return out, nil
}

func (s *server) navigationRequest(method string, raw json.RawMessage) (any, error) {
	out := []any{}
	if method == "workspace/symbol" {
		var p struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		workspace, err := s.navigationWorkspace("")
		if err != nil {
			return nil, err
		}
		if workspace == nil {
			return out, nil
		}
		sources := workspace.Sources()
		symbols := map[diag.Pos]check.Symbol{}
		for _, analysis := range workspace.Analyses() {
			for _, symbol := range analysis.Symbols() {
				if _, ok := sources[symbol.Definition.File]; !ok || !workspace.OwnsSource(symbol.Definition.File) || !strings.Contains(strings.ToLower(symbol.Name), strings.ToLower(p.Query)) {
					continue
				}
				switch symbol.Kind {
				case "variable", "parameter", "typeParameter":
					continue
				}
				symbols[symbol.Definition] = symbol
			}
		}
		ordered := make([]check.Symbol, 0, len(symbols))
		for _, symbol := range symbols {
			ordered = append(ordered, symbol)
		}
		slices.SortFunc(ordered, func(a, b check.Symbol) int {
			if n := strings.Compare(a.Name, b.Name); n != 0 {
				return n
			}
			if n := strings.Compare(a.Definition.File, b.Definition.File); n != 0 {
				return n
			}
			if a.Definition.Line != b.Definition.Line {
				return a.Definition.Line - b.Definition.Line
			}
			return a.Definition.Col - b.Definition.Col
		})
		for _, symbol := range ordered {
			src := sources[symbol.Definition.File]
			out = append(out, map[string]any{"name": symbol.Name, "kind": navigationKind(symbol.Kind), "containerName": symbol.Container, "location": location{fileURI(symbol.Definition.File), sourceRange{lspPosition(src, symbol.Definition), lspPosition(src, symbol.End)}}})
		}
		return out, nil
	}
	var p struct {
		Item hierarchyItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	path, err := filePath(p.Item.URI)
	if err != nil {
		return nil, err
	}
	workspace, err := s.navigationWorkspace(path)
	if err != nil {
		return nil, err
	}
	if workspace == nil {
		return out, nil
	}
	sources := workspace.Sources()
	src, ok := sources[path]
	if !ok {
		return out, nil
	}
	pos, err := compilerPosition(path, src, p.Item.SelectionRange.Start)
	if err != nil {
		return out, nil
	}
	var target *diag.Pos
	for _, analysis := range workspace.Analyses() {
		if item, err := analysis.EditorCallHierarchy(pos); err == nil {
			target = &item.SelectionStart
			break
		}
	}
	if target == nil {
		return out, nil
	}
	type grouped struct {
		Item   hierarchyItem
		Ranges []sourceRange
	}
	groups := map[diag.Pos]*grouped{}
	seen := map[driver.EditorCallEdge]bool{}
	for _, analysis := range workspace.Analyses() {
		for _, edge := range analysis.EditorCalls() {
			if seen[edge] {
				continue
			}
			seen[edge] = true
			matches := edge.Callee.SelectionStart == *target
			related := edge.Caller
			if method == "callHierarchy/outgoingCalls" {
				matches = edge.Caller.SelectionStart == *target
				related = edge.Callee
			}
			if !matches {
				continue
			}
			item, ok := s.hierarchyItem(related)
			if !ok {
				continue
			}
			source, ok := sources[edge.Start.File]
			if !ok {
				continue
			}
			key := related.SelectionStart
			group := groups[key]
			if group == nil {
				group = &grouped{Item: item}
				groups[key] = group
			}
			group.Ranges = append(group.Ranges, sourceRange{lspPosition(source, edge.Start), lspPosition(source, edge.End)})
		}
	}
	keys := make([]diag.Pos, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b diag.Pos) int {
		if n := strings.Compare(a.File, b.File); n != 0 {
			return n
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return a.Col - b.Col
	})
	for _, key := range keys {
		group := groups[key]
		field := "from"
		if method == "callHierarchy/outgoingCalls" {
			field = "to"
		}
		out = append(out, map[string]any{field: group.Item, "fromRanges": group.Ranges})
	}
	if method != "callHierarchy/incomingCalls" && method != "callHierarchy/outgoingCalls" {
		return nil, fmt.Errorf("unsupported navigation method")
	}
	return out, nil
}
