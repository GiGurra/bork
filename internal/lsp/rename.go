package lsp

import (
	"fmt"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/driver"
	"github.com/GiGurra/bork/internal/modcache"
)

// Rename owns protocol conversion only. Inventory, identities, edits and
// proposed-source validation all belong to compiler packages.
func (s *server) renameFeature(method, path string, p documentParams, pkg *packageState, pos diag.Pos) (any, error) {
	if pkg.stale && method != "textDocument/references" {
		return nil, fmt.Errorf("rename requires a successful check of current buffers")
	}
	ref := pkg.analysis.ReferenceAt(pos)
	if ref == nil {
		return nil, nil
	}
	if method != "textDocument/references" && (modcache.Contains(modcache.Root(), ref.Definition.File) || pkg.analysis.Sources()[ref.Definition.File] == "") {
		return nil, fmt.Errorf("dependency and standard library sources are read-only")
	}
	overlays := map[string]string{}
	for path, doc := range s.docs {
		overlays[path] = doc.text
	}
	if s.renameSessions == nil {
		s.renameSessions = map[string]*driver.Session{}
	}
	// Seed the open package's current session; Analyze still validates inputs
	// before reuse. Verification always uses the separate proposed-source cache.
	requestPath := analysisPath(path, s.source(path))
	if s.renameSessions[requestPath] == nil && !pkg.stale {
		s.renameSessions[requestPath] = pkg.session
	}
	if s.renameVerificationSessions == nil {
		s.renameVerificationSessions = map[string]*driver.Session{}
	}
	workspace, err := driver.AnalyzeWorkspaceWithSessions(path, s.workspaceRoots, overlays, s.renameSessions)
	if err != nil {
		return nil, err
	}
	sources := workspace.Sources()
	if method == "textDocument/references" {
		out := []location{}
		seen := map[location]bool{}
		for _, ref := range workspace.References(ref.Definition) {
			if !p.Context.IncludeDeclaration && ref.Declaration {
				continue
			}
			source, ok := sources[ref.Start.File]
			if !ok {
				continue
			}
			loc := location{fileURI(ref.Start.File), sourceRange{lspPosition(source, ref.Start), lspPosition(source, ref.End)}}
			if !seen[loc] {
				out = append(out, loc)
				seen[loc] = true
			}
		}
		return out, nil
	}
	if method == "textDocument/prepareRename" {
		// A no-op still checks writable identity and opaque Go restrictions.
		if _, err := workspace.RenameWithSessions(ref.Definition, ref.Name, s.renameVerificationSessions); err != nil {
			return nil, err
		}
		source := sources[path]
		return map[string]any{"range": sourceRange{lspPosition(source, ref.Start), lspPosition(source, ref.End)}, "placeholder": ref.Name}, nil
	}
	edits, err := workspace.RenameWithSessions(ref.Definition, p.NewName, s.renameVerificationSessions)
	if err != nil {
		return nil, err
	}
	changes := map[string][]textEdit{}
	for _, edit := range edits {
		source := sources[edit.Start.File]
		uri := fileURI(edit.Start.File)
		changes[uri] = append(changes[uri], textEdit{sourceRange{lspPosition(source, edit.Start), lspPosition(source, edit.End)}, edit.Replacement})
	}
	return map[string]any{"changes": changes}, nil
}
