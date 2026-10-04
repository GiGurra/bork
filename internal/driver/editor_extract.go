package driver

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	borkformat "github.com/GiGurra/bork/internal/format"
	"github.com/GiGurra/bork/internal/syntax"
)

// EditorExtractFunction creates a private helper from a complete expression.
// Callers must recheck the proposed source to verify contracts, lifetimes,
// contextual types, and return boundaries after moving the expression.
func (a *EditorAnalysis) EditorExtractFunction(start, end diag.Pos) (string, error) {
	return a.editorExtractFunction(start, end, true)
}

func (a *EditorAnalysis) editorExtractFunction(start, end diag.Pos, allowStatements bool) (string, error) {
	file, from := a.editorFile(start.File)
	if file == nil || start.File != end.File {
		return "", fmt.Errorf("selection is outside the checked source")
	}
	source := file.Source
	lo, _ := editorByteOffset(source, start)
	hi, _ := editorByteOffset(source, end)
	if lo >= hi || hi > len(source) || editorPositionAt(start.File, source, lo) != start || editorPositionAt(end.File, source, hi) != end {
		return "", fmt.Errorf("invalid selection")
	}
	selected := source[lo:hi]
	lo += len(selected) - len(strings.TrimLeftFunc(selected, unicode.IsSpace))
	hi -= len(selected) - len(strings.TrimRightFunc(selected, unicode.IsSpace))
	if lo >= hi {
		return "", fmt.Errorf("select a complete expression")
	}
	start, end = editorPositionAt(file.Path, source, lo), editorPositionAt(file.Path, source, hi)
	extraction, err := check.EditorExtractExpression(a.program.info, file, start, end)
	if err != nil {
		if allowStatements {
			// Let the real parser and checker decide whether a selection of
			// statements forms a self-contained block. Escaping bindings and
			// partial syntax fail the temporary check rather than being guessed.
			wrapped := "{\n" + source[lo:hi] + "\n}"
			changed := source[:lo] + wrapped + source[hi:]
			overlays := a.Sources()
			overlays[file.Path] = changed
			path := filepath.Dir(file.Path)
			if file.Script {
				path = file.Path
			}
			if analysis, checkErr := NewSession().Analyze(path, overlays); checkErr == nil {
				return analysis.editorExtractFunction(editorPositionAt(file.Path, changed, lo), editorPositionAt(file.Path, changed, lo+len(wrapped)), false)
			}
		}
		return "", err
	}
	used := map[string]bool{}
	for _, sibling := range a.program.files {
		if sibling.Package != file.Package || sibling.Prelude {
			continue
		}
		tokens, _ := syntax.Lex(sibling.Path, []byte(sibling.Source), &diag.List{})
		for _, token := range tokens {
			if token.Kind == syntax.TIdent {
				used[token.Text] = true
			}
		}
	}
	name := "extracted"
	for i := 2; used[name]; i++ {
		name = "extracted" + strconv.Itoa(i)
	}
	var parameters, arguments []string
	for _, parameter := range extraction.Parameters {
		typeText := parameter.WrittenType
		if typeText == "" {
			typeText = check.TypeText(parameter.Type, from)
		}
		parameters = append(parameters, parameter.Name+": "+typeText)
		arguments = append(arguments, parameter.Name)
	}
	effects := ""
	if extraction.Effects != 0 {
		effects = " uses " + extraction.Effects.String()
	}
	result := extraction.WrittenResult
	if result == "" {
		result = check.TypeText(extraction.Result, from)
	}
	var generics, typeArguments []string
	for _, parameter := range extraction.Function.Decl.TypeParams {
		text := parameter.Name
		if len(parameter.Bounds) > 0 {
			text += ": " + strings.Join(parameter.Bounds, " + ")
		}
		generics = append(generics, text)
		typeArguments = append(typeArguments, parameter.Name)
	}
	genericHeader, genericCall := "", ""
	if len(generics) > 0 {
		genericHeader = "[" + strings.Join(generics, ", ") + "]"
		genericCall = "[" + strings.Join(typeArguments, ", ") + "]"
	}
	helper := "\n\nfn " + name + genericHeader + "(" + strings.Join(parameters, ", ") + ")" + effects + ": " + result + " {\n" + source[lo:hi] + "\n}\n"
	call := name + genericCall + "(" + strings.Join(arguments, ", ") + ")"
	formatted, err := borkformat.Source(file.Path, []byte(source[:lo]+call+source[hi:]+helper))
	return string(formatted), err
}

func editorPositionAt(path, source string, offset int) diag.Pos {
	before := source[:offset]
	return diag.Pos{File: path, Line: strings.Count(before, "\n") + 1, Col: offset - strings.LastIndexByte(before, '\n')}
}
