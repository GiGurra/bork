package lsp

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/doccomment"
	"github.com/GiGurra/bork/internal/driver"
	borkformat "github.com/GiGurra/bork/internal/format"
	"github.com/GiGurra/bork/internal/modcache"
	"github.com/GiGurra/bork/internal/syntax"
)

func (s *server) feature(method, path string, p documentParams) (any, error) {
	src := s.source(path)
	if method == "textDocument/formatting" || method == "textDocument/codeAction" || method == "textDocument/rename" || method == "textDocument/prepareRename" {
		if modcache.Contains(modcache.Root(), path) {
			return nil, fmt.Errorf("dependency sources are read-only; edit the original repository and publish a new version")
		}
	}
	switch method {
	case "textDocument/codeLens":
		return codeLenses(path, src), nil
	case "bork/tests":
		return discoverTests(path, src), nil
	case "textDocument/formatting":
		formatted, err := borkformat.Source(path, []byte(src))
		if err != nil {
			return nil, err
		}
		edits := []textEdit{}
		if string(formatted) != src {
			edits = append(edits, textEdit{sourceRange{position{}, endPosition(src)}, string(formatted)})
		}
		return edits, nil
	case "textDocument/documentSymbol":
		return symbols(path, src), nil
	case "textDocument/codeAction":
		out := s.codeActions(path, p)
		if action := s.organizeImports(path, p); action != nil {
			out = append(out, action)
		}
		return out, nil
	}
	pkg := s.state(path)
	if method == "textDocument/completion" {
		return s.completion(pkg, path, src, p.Position), nil
	}
	if pkg == nil || pkg.analysis == nil {
		return nil, nil
	}
	// Navigation uses the last successful snapshot while an edit is broken.
	sources := pkg.analysis.Sources()
	snapshot, ok := sources[path]
	if !ok {
		return nil, nil
	}
	pos, err := compilerPosition(path, snapshot, p.Position)
	if err != nil {
		return nil, nil
	}
	switch method {
	case "textDocument/hover":
		result, err := pkg.analysis.Describe(pos)
		if err != nil {
			return nil, nil
		}
		text := "```bork\n" + result.Type + "\n```"
		if result.Documentation != "" {
			text += "\n\n" + doccomment.Markdown(result.Documentation)
		}
		if pkg.stale {
			text = "**Stale: last successful check.**\n\n" + text
		}
		if result.Callable != nil {
			if len(result.Callable.Needs) > 0 {
				text += "\n\nNeeds: " + strings.Join(result.Callable.Needs, ", ")
			}
			if len(result.Callable.Requires) > 0 {
				text += "\n\nRequires: " + strings.Join(result.Callable.Requires, "; ")
			}
		}
		for _, fact := range result.Facts {
			text += "\n\nKnown: `" + fact.Path + " " + fact.Constraint + "`"
		}
		return map[string]any{"contents": map[string]string{"kind": "markdown", "value": text}}, nil
	case "textDocument/definition":
		def, err := pkg.analysis.Definition(pos)
		if err != nil || def == nil {
			return nil, nil
		}
		return tokenLocation(*def, sources[def.File]), nil
	case "textDocument/references", "textDocument/rename", "textDocument/prepareRename":
		if pkg.stale && method != "textDocument/references" {
			return nil, fmt.Errorf("rename requires a successful check of current buffers")
		}
		def, err := pkg.analysis.Definition(pos)
		if err != nil || def == nil {
			return nil, nil
		}
		if method != "textDocument/references" {
			if modcache.Contains(modcache.Root(), def.File) {
				return nil, fmt.Errorf("dependency sources are read-only")
			}
			declaration, err := pkg.analysis.Definition(*def)
			tok := tokenAt(def.File, sources[def.File], *def)
			if err != nil || declaration == nil || *declaration != *def || tok == nil || tok.Kind != syntax.TIdent {
				return nil, fmt.Errorf("rename currently supports local variables and package-private functions")
			}
			for _, text := range sources {
				tokens, _ := syntax.Lex("", []byte(text), &diag.List{})
				for _, token := range tokens {
					if token.Kind == syntax.TGoCode {
						return nil, fmt.Errorf("rename cannot verify references inside unsafe Go bodies")
					}
				}
			}
			if len(tok.Text) > 0 && tok.Text[0] >= 'A' && tok.Text[0] <= 'Z' {
				return nil, fmt.Errorf("exported names need a workspace-wide reference index before rename")
			}
		}
		if method == "textDocument/prepareRename" {
			tok := tokenAt(path, snapshot, pos)
			if tok == nil || tok.Kind != syntax.TIdent {
				return nil, nil
			}
			return map[string]any{"range": sourceRange{lspPosition(snapshot, tok.Pos), lspPosition(snapshot, tok.End)}, "placeholder": tok.Text}, nil
		}
		refs := s.references(*def, p.Context.IncludeDeclaration || method == "textDocument/rename")
		if method == "textDocument/references" {
			return refs, nil
		}
		if !validIdentifier(p.NewName) {
			return nil, fmt.Errorf("new name must be an identifier, not a keyword")
		}
		if p.NewName == tokenAt(def.File, sources[def.File], *def).Text {
			return map[string]any{"changes": map[string][]textEdit{}}, nil
		}
		for file := range sources {
			if filepath.Dir(file) != filepath.Dir(def.File) {
				continue
			}
			tokens, _ := syntax.Lex(file, []byte(s.source(file)), &diag.List{})
			for _, token := range tokens {
				if token.Kind == syntax.TIdent && token.Text == p.NewName {
					return nil, fmt.Errorf("new name already occurs in an affected file")
				}
			}
		}
		changes := map[string][]textEdit{}
		for _, ref := range refs {
			changes[ref.URI] = append(changes[ref.URI], textEdit{ref.Range, p.NewName})
		}
		if err := s.validateRename(changes); err != nil {
			return nil, err
		}
		return map[string]any{"changes": changes}, nil
	}
	return nil, fmt.Errorf("unsupported method %s", method)
}
func tokenAt(path, src string, pos diag.Pos) *syntax.Token {
	tokens := editorTokens(path, src)
	for _, t := range tokens {
		if t.Pos.Line == pos.Line && t.Pos.Col <= pos.Col && (t.End.Line > pos.Line || t.End.Col > pos.Col) {
			return &t
		}
	}
	return nil
}
func tokenLocation(pos diag.Pos, src string) location {
	start := lspPosition(src, pos)
	end := start
	if token := tokenAt(pos.File, src, pos); token != nil {
		end = lspPosition(src, token.End)
	}
	return location{fileURI(pos.File), sourceRange{start, end}}
}
func validIdentifier(name string) bool {
	diags := &diag.List{}
	tokens, _ := syntax.Lex("", []byte(name), diags)
	if diags.Len() != 0 {
		return false
	}
	if len(tokens) < 2 || tokens[0].Kind != syntax.TIdent || tokens[0].Text != name {
		return false
	}
	for _, token := range tokens[1:] {
		if token.Kind != syntax.Semi && token.Kind != syntax.EOF {
			return false
		}
	}
	return true
}
func (s *server) references(def diag.Pos, includeDeclaration bool) []location {
	out := []location{}
	seen := map[location]bool{}
	for _, dir := range sortedKeys(s.packages) {
		pkg := s.packages[dir]
		// Workspace edits never combine a stale package with current source text.
		if pkg.analysis == nil || pkg.stale {
			continue
		}
		sources := pkg.analysis.Sources()
		for _, path := range sortedKeys(sources) {
			src := sources[path]
			tokens := editorTokens(path, src)
			name := ""
			if target := tokenAt(def.File, sources[def.File], def); target != nil {
				name = target.Text
			}
			for _, token := range tokens {
				if token.Kind != syntax.TIdent || token.Text != name {
					continue
				}
				identity, err := pkg.analysis.Definition(token.Pos)
				if err != nil || identity == nil || *identity != def || !includeDeclaration && token.Pos == def {
					continue
				}
				loc := location{fileURI(path), sourceRange{lspPosition(src, token.Pos), lspPosition(src, token.End)}}
				if !seen[loc] {
					out = append(out, loc)
					seen[loc] = true
				}
			}
		}
	}
	return out
}
func symbols(path, src string) []any {
	diags := &diag.List{}
	files := syntax.ParseFiles([]string{path}, [][]byte{[]byte(src)}, false, diags)
	out := []any{}
	if len(files) == 0 {
		return out
	}
	add := func(name string, kind int, pos diag.Pos) {
		loc := tokenLocation(pos, src)
		out = append(out, map[string]any{"name": name, "kind": kind, "location": loc})
	}
	file := files[0]
	for _, fn := range file.Funcs {
		if fn.ScriptMain {
			continue
		}
		add(fn.Name, 12, fn.Pos)
	}
	for _, typ := range file.Types {
		add(typ.Name, 23, typ.Pos)
	}
	for _, binding := range file.Bindings {
		add(binding.Name, 13, binding.Pos)
	}
	for _, bundle := range file.Providers {
		add(bundle.Name, 13, bundle.NamePos)
	}
	return out
}
func (s *server) codeActions(path string, p documentParams) []any {
	out := []any{}
	if len(p.Context.Only) > 0 && !slices.Contains(p.Context.Only, "quickfix") {
		return out
	}
	for _, d := range s.diagnostics[path] {
		start := lspPosition(s.source(path), d.Pos)
		end := lspPosition(s.source(path), d.End)
		if end.Line < p.Range.Start.Line || start.Line > p.Range.End.Line {
			continue
		}
		for _, fix := range d.Fixes {
			if fix.RequiresInput {
				continue
			}
			changes := map[string][]textEdit{}
			for _, edit := range fix.Edits {
				file, err := filepath.Abs(edit.Start.File)
				if err != nil {
					continue
				}
				src := s.source(file)
				changes[fileURI(file)] = append(changes[fileURI(file)], textEdit{sourceRange{lspPosition(src, edit.Start), lspPosition(src, edit.End)}, edit.Replacement})
			}
			if len(changes) > 0 && s.formatFix(changes) {
				out = append(out, map[string]any{"title": fix.Message, "kind": "quickfix", "edit": map[string]any{"changes": changes}})
			}
		}
	}
	return out
}

// The compiler owns the fix and formatter. Apply its edits to the current
// buffer before formatting so even a fix inserted into an inline expression
// yields a document that passes bork fmt --check.
func (s *server) formatFix(changes map[string][]textEdit) bool {
	for uri, edits := range changes {
		path, err := filePath(uri)
		if err != nil {
			return false
		}
		src := s.source(path)
		text := src
		edits = slices.Clone(edits)
		slices.SortFunc(edits, func(a, b textEdit) int {
			if a.Range.Start.Line != b.Range.Start.Line {
				return b.Range.Start.Line - a.Range.Start.Line
			}
			return b.Range.Start.Character - a.Range.Start.Character
		})
		for _, edit := range edits {
			start, err := byteOffset(text, edit.Range.Start)
			if err != nil {
				return false
			}
			end, err := byteOffset(text, edit.Range.End)
			if err != nil || end < start {
				return false
			}
			text = text[:start] + edit.NewText + text[end:]
		}
		formatted, err := borkformat.Source(path, []byte(text))
		if err != nil {
			return false
		}
		changes[uri] = []textEdit{{sourceRange{position{}, endPosition(src)}, string(formatted)}}
	}
	return true
}

// Interpolation holes sit inside one lexer token. Candidates inside that token
// still require a compiler definition identity; literal text has none.
func editorTokens(path, src string) []syntax.Token {
	tokens, _ := syntax.Lex(path, []byte(src), &diag.List{})
	out := make([]syntax.Token, 0, len(tokens))
	for _, token := range tokens {
		if token.Kind != syntax.TInterp {
			out = append(out, token)
			continue
		}
		start, end := token.Pos, token.End
		lines := strings.Split(src, "\n")
		for line := start.Line; line <= end.Line && line <= len(lines); line++ {
			text := lines[line-1]
			lo, hi := 0, len(text)
			if line == start.Line {
				lo = start.Col - 1
			}
			if line == end.Line {
				hi = min(hi, end.Col-1)
			}
			for i := lo; i < hi; {
				if !identifierStart(text[i]) {
					i++
					continue
				}
				j := i + 1
				for j < hi && (identifierStart(text[j]) || text[j] >= '0' && text[j] <= '9') {
					j++
				}
				out = append(out, syntax.Token{Kind: syntax.TIdent, Text: text[i:j], Pos: diag.Pos{File: path, Line: line, Col: i + 1}, End: diag.Pos{File: path, Line: line, Col: j + 1}})
				i = j
			}
		}
	}
	return out
}
func identifierStart(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_' }

// Recheck proposed edits in memory before returning them. This catches syntax
// references outside the query index (for example named where predicates).
func (s *server) validateRename(changes map[string][]textEdit) error {
	overlays := map[string]string{}
	for path, doc := range s.docs {
		overlays[path] = doc.text
	}
	dirs := map[string]bool{}
	for uri, edits := range changes {
		path, err := filePath(uri)
		if err != nil {
			return err
		}
		if modcache.Contains(modcache.Root(), path) {
			return fmt.Errorf("dependency sources are read-only")
		}
		text := s.source(path)
		edits = slices.Clone(edits)
		slices.SortFunc(edits, func(a, b textEdit) int {
			if a.Range.Start.Line != b.Range.Start.Line {
				return b.Range.Start.Line - a.Range.Start.Line
			}
			return b.Range.Start.Character - a.Range.Start.Character
		})
		for _, edit := range edits {
			start, err := byteOffset(text, edit.Range.Start)
			if err != nil {
				return err
			}
			end, err := byteOffset(text, edit.Range.End)
			if err != nil || end < start {
				return fmt.Errorf("invalid rename range")
			}
			text = text[:start] + edit.NewText + text[end:]
		}
		overlays[path] = text
		dirs[analysisPath(path, text)] = true
	}
	for dir := range dirs {
		if _, err := driver.NewSession().Analyze(dir, overlays); err != nil {
			return fmt.Errorf("proposed rename does not check; some references may be unsupported: %w", err)
		}
	}
	return nil
}
