package lsp

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/driver"
	"github.com/GiGurra/bork/internal/syntax"
)

func (s *server) completion(pkg *packageState, file, src string, p position) []any {
	out := []any{}
	offset, err := byteOffset(src, p)
	if err != nil {
		return out
	}
	tokens, comments := syntax.Lex(file, []byte(src), &diag.List{})
	pos, _ := compilerPosition(file, src, p)
	before := func(a, b diag.Pos) bool { return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col }
	for _, comment := range comments {
		if !before(pos, comment.Pos) && before(pos, comment.End) {
			return out
		}
	}
	for _, t := range tokens {
		if !before(pos, t.Pos) && before(pos, t.End) && (t.Kind == syntax.TString || t.Kind == syntax.TInterp || t.Kind == syntax.TGoCode || t.Kind == syntax.TRune) {
			return out
		}
	}
	start := offset
	for start > 0 && (identifierStart(src[start-1]) || src[start-1] >= '0' && src[start-1] <= '9') {
		start--
	}
	prefix := src[start:offset]
	end := offset
	for end < len(src) && (identifierStart(src[end]) || src[end] >= '0' && src[end] <= '9') {
		end++
	}
	replace := sourceRange{lspPosition(src, offsetPos(src, start)), lspPosition(src, offsetPos(src, end))}
	seen := map[string]bool{}
	add := func(c driver.EditorCompletion) map[string]any {
		key := c.Name + "|" + c.ImportPath
		if seen[key] || !strings.HasPrefix(strings.ToLower(c.Name), strings.ToLower(prefix)) {
			return nil
		}
		seen[key] = true
		detail := c.Detail
		if pkg != nil && pkg.stale {
			detail = "Stale: last successful check. " + detail
		}
		text := c.Text
		if text == "" {
			text = c.Name
		}
		if strings.HasSuffix(text, ": ") {
			for _, token := range tokens {
				if before(token.Pos, offsetPos(src, end)) || token.Kind == syntax.Semi {
					continue
				}
				if token.Kind == syntax.Colon {
					text = c.Name
				}
				break
			}
		}
		itemRange := replace
		if prefix == "" && strings.HasSuffix(text, ": ") {
			itemRange.End = itemRange.Start
		}
		kinds := map[string]int{"method": 2, "function": 3, "field": 5, "variable": 6, "type": 7, "module": 9, "enumMember": 20, "keyword": 14}
		item := map[string]any{"label": c.Name, "detail": detail, "kind": kinds[c.Kind], "sortText": fmt.Sprintf("%d-%s", c.Rank, c.Name), "textEdit": textEdit{itemRange, text}}
		out = append(out, item)
		return item
	}
	if pkg != nil && pkg.analysis != nil {
		context, exclusive := pkg.analysis.EditorContextCompletions(file, src, pos, offsetPos(src, start))
		for _, c := range context {
			add(c)
		}
		if exclusive {
			return out
		}
		for _, c := range pkg.analysis.EditorSymbolsInBuffer(file, src, pos) {
			add(c)
		}
		files := syntax.ParseFiles([]string{file}, [][]byte{[]byte(src)}, false, &diag.List{})
		lines := strings.Split(src, "\n")
		var imports []driver.EditorCompletion
		if len(prefix) >= 2 && len(files) > 0 {
			imports = pkg.analysis.EditorImportSymbols(file)
		}
		for _, c := range imports {
			imported := slices.ContainsFunc(files[0].Imports, func(imp *syntax.Import) bool { return imp.Path == c.ImportPath })
			if imported {
				continue
			}
			alias := path.Base(c.ImportPath)
			used := func(name string) bool {
				return slices.ContainsFunc(tokens, func(t syntax.Token) bool { return t.Kind == syntax.TIdent && t.Text == name })
			}
			base := alias
			if !validIdentifier(alias) {
				alias = "pkg"
			}
			for n := 2; used(alias); n++ {
				alias = "pkg" + strconv.Itoa(n)
			}
			c.Text = alias + "." + c.Name
			item := add(c)
			if item == nil {
				continue
			}
			importText := "import "
			if alias != base {
				importText += alias + " "
			}
			importText += strconv.Quote(c.ImportPath) + "\n"
			line := 0
			if strings.HasPrefix(src, "#!") {
				line = 1
				for line < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[line]), "//") {
					line++
				}
			}
			for _, imp := range files[0].Imports {
				line = max(line, imp.Pos.Line)
			}
			at := position{Line: line}
			if line >= len(lines) {
				at = endPosition(src)
				importText = "\n" + importText
			}
			item["additionalTextEdits"] = []textEdit{{sourceRange{at, at}, importText}}
		}
	}
	for _, word := range syntax.Keywords() {
		add(driver.EditorCompletion{Name: word, Detail: "keyword", Kind: "keyword", Rank: 3})
	}
	if s.snippets {
		for _, snippet := range []struct{ label, text string }{
			{"fn", "fn ${1:name}(${2}): ${3:Ok} {\n  ${0}\n}"},
			{"match", "match ${1:value} {\n  ${2:_} => ${0}\n}"},
			{"select", "select {\n  ${1:value} = ${2:channel}.receive(${3:s}) => ${0}\n}"},
			{"type", "type ${1:Name} = { ${0} }"},
			{"test", "test \"${1:description}\" {\n  ${0}\n}"},
		} {
			if !strings.HasPrefix(snippet.label, prefix) {
				continue
			}
			out = append(out, map[string]any{"label": snippet.label + " snippet", "filterText": snippet.label, "kind": 15, "sortText": "2-" + snippet.label, "insertTextFormat": 2, "textEdit": textEdit{replace, snippet.text}})
		}
	}
	return out
}

func offsetPos(src string, offset int) diag.Pos {
	line := strings.Count(src[:offset], "\n") + 1
	start := strings.LastIndexByte(src[:offset], '\n') + 1
	return diag.Pos{Line: line, Col: offset - start + 1}
}
