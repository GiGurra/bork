package driver

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/modcache"
	"github.com/GiGurra/bork/internal/syntax"
)

// EditorWorkspace owns current checked snapshots of local workspace packages.
// Dependencies remain readable for navigation, but never editable.
type EditorWorkspace struct {
	analyses []*EditorAnalysis
	paths    []string
	overlays map[string]string
}

func (w *EditorWorkspace) Analyses() []*EditorAnalysis { return slices.Clone(w.analyses) }
func (w *EditorWorkspace) Sources() map[string]string {
	out := map[string]string{}
	for _, a := range w.analyses {
		maps.Copy(out, a.Sources())
	}
	return out
}
func (w *EditorWorkspace) References(def diag.Pos) []check.SourceReference {
	seen := map[check.SourceReference]bool{}
	for _, a := range w.analyses {
		for _, ref := range a.References(def) {
			seen[ref] = true
		}
	}
	out := make([]check.SourceReference, 0, len(seen))
	for ref := range seen {
		out = append(out, ref)
	}
	slices.SortFunc(out, func(a, b check.SourceReference) int {
		if n := strings.Compare(a.Start.File, b.Start.File); n != 0 {
			return n
		}
		if a.Start.Line != b.Start.Line {
			return a.Start.Line - b.Start.Line
		}
		return a.Start.Col - b.Start.Col
	})
	return out
}

// WorkspacePackages inventories local packages through the compiler source
// reader, including new buffers. Hidden/vendor directories and nested modules
// are excluded unless supplied as explicit workspace roots.
func WorkspacePackages(path string, roots []string, overlays map[string]string) ([]string, error) {
	reader := overlaySources{files: overlays}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dir := absolute
	if yes, err := reader.isDirectory(dir); err != nil {
		return nil, err
	} else if !yes {
		dir = filepath.Dir(dir)
	}
	owner, err := findModuleFrom(dir, reader)
	if err != nil {
		return nil, err
	}
	if owner.path == "" {
		if text, err := reader.readFile(absolute); err == nil && strings.HasPrefix(string(text), "#!") {
			return []string{absolute}, nil
		}
		return []string{dir}, nil
	}
	roots = append(slices.Clone(roots), owner.root)
	packages := map[string]bool{}
	visited := map[string]bool{}
	var walk func(string, bool) error
	walk = func(dir string, root bool) error {
		if visited[dir] || modcache.Contains(modcache.Root(), dir) {
			return nil
		}
		visited[dir] = true
		if !root {
			if _, err := reader.readFile(filepath.Join(dir, ModFile)); err == nil {
				return nil
			}
		}
		entries, err := reader.directory(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.directory {
				if strings.HasPrefix(entry.name, ".") || entry.name == "vendor" || entry.name == "node_modules" {
					continue
				}
				if err := walk(filepath.Join(dir, entry.name), false); err != nil {
					return err
				}
			} else if filepath.Ext(entry.name) == ".bork" {
				file := filepath.Join(dir, entry.name)
				text, err := reader.readFile(file)
				if err != nil {
					return err
				}
				if strings.HasPrefix(string(text), "#!") {
					packages[file] = true
				} else {
					packages[dir] = true
				}
			}
		}
		return nil
	}
	for _, root := range roots {
		root, err = filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		if err := walk(root, true); err != nil {
			return nil, err
		}
	}
	out := slices.Sorted(maps.Keys(packages))
	return out, nil
}

// AnalyzeWorkspace checks closed importers as well as open buffers. An invalid
// local package prevents edits: returning a partial reference set is unsafe.
func AnalyzeWorkspace(path string, roots []string, overlays map[string]string) (*EditorWorkspace, error) {
	paths, err := WorkspacePackages(path, roots, overlays)
	if err != nil {
		return nil, err
	}
	w := &EditorWorkspace{paths: paths, overlays: maps.Clone(overlays)}
	for _, path := range paths {
		analysis, err := NewSession().Analyze(path, overlays)
		if err != nil {
			return nil, fmt.Errorf("workspace package %s does not check: %w", path, err)
		}
		w.analyses = append(w.analyses, analysis)
	}
	return w, nil
}

// Rename constructs and checks edits without writing files. Rechecking every
// package also verifies that references still bind to the renamed declaration.
func (w *EditorWorkspace) Rename(def diag.Pos, name string) ([]diag.TextEdit, error) {
	tokens, _ := syntax.Lex("", []byte(name), &diag.List{})
	if len(tokens) < 2 || tokens[0].Kind != syntax.TIdent || tokens[0].Text != name {
		return nil, fmt.Errorf("new name must be an identifier, not a keyword")
	}
	refs := w.References(def)
	if len(refs) == 0 {
		return nil, fmt.Errorf("no checked source references")
	}
	sources := w.Sources()
	if _, ok := sources[def.File]; !ok || modcache.Contains(modcache.Root(), def.File) {
		return nil, fmt.Errorf("dependency and standard library sources are read-only")
	}
	supported := false
	for _, analysis := range w.analyses {
		for _, symbol := range analysis.Symbols() {
			if symbol.Definition != def {
				continue
			}
			switch symbol.Kind {
			case "variable", "parameter", "typeParameter", "function", "predicate", "method", "type", "field", "variant", "value":
				supported = true
			}
		}
	}
	if !supported {
		return nil, fmt.Errorf("rename does not yet support this declaration kind")
	}
	edits := []diag.TextEdit{}
	changed := map[string]bool{}
	for _, ref := range refs {
		if modcache.Contains(modcache.Root(), ref.Start.File) {
			return nil, fmt.Errorf("dependency sources are read-only")
		}
		if _, ok := sources[ref.Start.File]; !ok {
			return nil, fmt.Errorf("source outside editable workspace")
		}
		edits = append(edits, diag.TextEdit{Start: ref.Start, End: ref.End, Replacement: ref.Prefix + name + ref.Suffix})
		changed[ref.Start.File] = true
	}
	// Raw Go is opaque to the checker. Reject affected graphs rather than claim
	// to verify bindings inside Go bodies.
	for _, a := range w.analyses {
		affected := false
		for path := range a.Sources() {
			affected = affected || changed[path]
		}
		if !affected {
			continue
		}
		for path, text := range a.Sources() {
			if modcache.Contains(modcache.Root(), path) {
				continue
			}
			tokens, _ := syntax.Lex(path, []byte(text), &diag.List{})
			for _, token := range tokens {
				if token.Kind == syntax.TGoCode {
					return nil, fmt.Errorf("rename cannot verify references inside unsafe Go bodies")
				}
			}
		}
	}
	if refs[0].Name == name {
		return []diag.TextEdit{}, nil
	}
	overlays := maps.Clone(w.overlays)
	if overlays == nil {
		overlays = map[string]string{}
	}
	for file := range changed {
		text := sources[file]
		for i := len(edits) - 1; i >= 0; i-- {
			edit := edits[i]
			if edit.Start.File != file {
				continue
			}
			start, end := sourceOffset(text, edit.Start), sourceOffset(text, edit.End)
			if start < 0 || end < start || end > len(text) {
				return nil, fmt.Errorf("invalid rename range")
			}
			text = text[:start] + edit.Replacement + text[end:]
		}
		overlays[file] = text
	}

	translate := func(pos, identity diag.Pos) diag.Pos {
		out := renamePosition(pos, edits)
		for _, ref := range refs {
			if ref.Start != pos {
				continue
			}
			if identity == def {
				out.Col += len(ref.Prefix)
			}
			if identity != def && identity == pos && ref.Suffix != "" {
				out.Col += len(name) + 2
			}
		}
		return out
	}
	beforeRefs := map[check.SourceReference]bool{}
	for _, before := range w.analyses {
		for _, ref := range before.AllReferences() {
			beforeRefs[ref] = true
		}
	}
	for _, path := range w.paths {
		after, err := NewSession().Analyze(path, overlays)
		if err != nil {
			return nil, fmt.Errorf("proposed rename does not check: %w", err)
		}
		afterSources := after.Sources()
		afterRefs := map[diag.Pos]map[diag.Pos]bool{}
		for _, ref := range after.AllReferences() {
			if afterRefs[ref.Start] == nil {
				afterRefs[ref.Start] = map[diag.Pos]bool{}
			}
			afterRefs[ref.Start][ref.Definition] = true
		}
		for ref := range beforeRefs {
			if _, ok := afterSources[ref.Start.File]; !ok {
				continue
			}
			expected := translate(ref.Definition, ref.Definition)
			if !afterRefs[translate(ref.Start, ref.Definition)][expected] {
				return nil, fmt.Errorf("proposed rename changes the binding of %s at %s", ref.Name, ref.Start)
			}
		}
	}

	return edits, nil
}
func sourceOffset(text string, pos diag.Pos) int {
	offset := 0
	for line := 1; line < pos.Line; line++ {
		i := strings.IndexByte(text[offset:], '\n')
		if i < 0 {
			return -1
		}
		offset += i + 1
	}
	if pos.Col < 1 || offset+pos.Col-1 > len(text) {
		return -1
	}
	return offset + pos.Col - 1
}
func renamePosition(pos diag.Pos, edits []diag.TextEdit) diag.Pos {
	out := pos
	for _, edit := range edits {
		if edit.Start.File != pos.File || edit.Start.Line != pos.Line {
			continue
		}
		if edit.End.Col <= pos.Col {
			out.Col += len(edit.Replacement) - (edit.End.Col - edit.Start.Col)
		}
	}
	return out
}
