package driver

import (
	"fmt"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	borkformat "github.com/GiGurra/bork/internal/format"
	"github.com/GiGurra/bork/internal/syntax"
)

// EditorOrganizeImports sorts parsed imports and removes only imports identified
// as unused by the current compiler diagnostics. It never infers usage from text.
func EditorOrganizeImports(path, source string, diagnostics []diag.Diagnostic) (string, error) {
	d := &diag.List{}
	files := syntax.ParseFiles([]string{path}, [][]byte{[]byte(source)}, false, d)
	if len(files) != 1 || d.Len() != 0 {
		return "", fmt.Errorf("organize imports requires valid syntax")
	}
	imports := files[0].Imports
	if len(imports) == 0 {
		return source, nil
	}
	lines := strings.SplitAfter(source, "\n")
	offsets := make([]int, len(lines)+1)
	for i, line := range lines {
		offsets[i+1] = offsets[i] + len(line)
	}
	unused := map[diag.Pos]bool{}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "import.unused" {
			unused[diagnostic.Pos] = true
		}
	}
	type entry struct {
		path, name, text  string
		leading, trailing []string
		unused            bool
	}
	offset := func(pos diag.Pos) int { return offsets[pos.Line-1] + pos.Col - 1 }
	start, end := offset(imports[0].Pos), offset(imports[len(imports)-1].End)
	tokens, _ := syntax.Lex(path, []byte(source), &diag.List{})
	onlySeparators := func(lo, hi int) bool {
		for _, token := range tokens {
			if token.Kind != syntax.EOF && token.Kind != syntax.Semi && offset(token.Pos) >= lo && offset(token.Pos) < hi {
				return false
			}
		}
		return true
	}
	lastLine := imports[len(imports)-1].End.Line
	for _, comment := range files[0].Comments {
		if comment.Pos.Line == lastLine && offset(comment.Pos) >= end && onlySeparators(end, offset(comment.Pos)) {
			end = offset(comment.End)
			lastLine = comment.End.Line
		}
	}
	// Consume the separators belonging to the final import, so replacing
	// an inline import cannot leave a standalone semicolon or add a blank
	// line on every subsequent organization pass.
	for _, token := range tokens {
		if token.Kind == syntax.EOF || offset(token.Pos) < end {
			continue
		}
		if token.Kind != syntax.Semi {
			break
		}
		end = min(len(source), max(offset(token.End), offset(token.Pos)+len(token.Text)))
	}
	end = len(source) - len(strings.TrimLeft(source[end:], " \t\r\n"))
	entries := make([]entry, len(imports))
	for i, imp := range imports {
		entries[i] = entry{path: imp.Path, name: imp.Name, text: source[offset(imp.Pos):offset(imp.End)], unused: unused[imp.Pos]}
	}
	for _, comment := range files[0].Comments {
		if offset(comment.Pos) < start || offset(comment.End) > end {
			continue
		}
		previous, following := -1, -1
		inside := -1
		for i, imp := range imports {
			if offset(imp.Pos) <= offset(comment.Pos) && offset(comment.End) <= offset(imp.End) {
				inside = i
			}
			if offset(imp.End) <= offset(comment.Pos) {
				previous = i
			}
			if following < 0 && offset(imp.Pos) >= offset(comment.End) {
				following = i
			}
		}
		if inside >= 0 {
			if entries[inside].unused {
				entries[inside].leading = append(entries[inside].leading, comment.Text)
			}
			continue
		}
		if previous >= 0 && imports[previous].End.Line == comment.Pos.Line {
			entries[previous].trailing = append(entries[previous].trailing, comment.Text)
		} else if following >= 0 {
			entries[following].leading = append(entries[following].leading, comment.Text)
		} else if previous >= 0 {
			entries[previous].trailing = append(entries[previous].trailing, comment.Text)
		}
	}
	var orphanComments []string
	for _, entry := range entries {
		if entry.unused {
			orphanComments = append(orphanComments, entry.leading...)
			orphanComments = append(orphanComments, entry.trailing...)
		}
	}
	entries = slices.DeleteFunc(entries, func(e entry) bool { return e.unused })
	slices.SortFunc(entries, func(a, b entry) int {
		if n := strings.Compare(a.path, b.path); n != 0 {
			return n
		}
		return strings.Compare(a.name, b.name)
	})
	parts := append([]string{}, orphanComments...)
	for _, entry := range entries {
		parts = append(parts, entry.leading...)
		text := entry.text
		if len(entry.trailing) > 0 {
			text += " " + strings.Join(entry.trailing, " ")
		}
		parts = append(parts, text)
	}
	replacement := strings.Join(parts, "\n")
	if replacement != "" {
		replacement += "\n"
	}
	formatted, err := borkformat.Source(path, []byte(source[:start]+replacement+source[end:]))
	return string(formatted), err
}
