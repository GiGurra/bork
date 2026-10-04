// Package doccomment gives compiler documentation and editor hover one comment model.
package doccomment

import (
	"html"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// At returns the adjacent whole-line comment group preceding a declaration.
func At(file *syntax.File, pos diag.Pos) string {
	// Lazy declaration positions point at the name after the marker.
	sourceLines := strings.Split(file.Source, "\n")
	if pos.Line > 0 && pos.Line <= len(sourceLines) && pos.Col > 0 && pos.Col-1 <= len(sourceLines[pos.Line-1]) {
		prefix := sourceLines[pos.Line-1][:pos.Col-1]
		if strings.TrimSpace(prefix) == "lazy" {
			pos.Col = len(prefix) - len(strings.TrimLeft(prefix, " \t")) + 1
		}
	}
	if !wholeLine(file.Source, pos) {
		return ""
	}
	var lines []string
	line := pos.Line - 1
	for i := len(file.Comments) - 1; i >= 0; i-- {
		c := file.Comments[i]
		if c.Pos.Line > line {
			continue
		}
		if c.Pos.Line != line || !strings.HasPrefix(c.Text, "//") || !wholeLine(file.Source, c.Pos) || strings.HasPrefix(c.Text, "// bork:") {
			break
		}
		lines = append([]string{strings.TrimPrefix(strings.TrimPrefix(c.Text, "//"), " ")}, lines...)
		line = c.Pos.Line - 1
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func wholeLine(source string, pos diag.Pos) bool {
	lines := strings.Split(source, "\n")
	return pos.Line > 0 && pos.Line <= len(lines) && pos.Col > 0 && pos.Col-1 <= len(lines[pos.Line-1]) && strings.TrimSpace(lines[pos.Line-1][:pos.Col-1]) == ""
}

// Package returns leading prose that is not attached to the first declaration.
func Package(file *syntax.File) string {
	tokens, _ := syntax.Lex(file.Path, []byte(file.Source), &diag.List{})
	if len(tokens) == 0 {
		return ""
	}
	first := tokens[0]
	var parts []string
	var group []string
	end := 0
	flush := func() {
		if len(group) > 0 && (first.Text == "import" || end+1 < first.Pos.Line || first.Kind == syntax.EOF) {
			parts = append(parts, strings.Join(group, "\n"))
		}
		group = nil
	}
	for _, c := range file.Comments {
		if c.Pos.Line >= first.Pos.Line {
			break
		}
		if !strings.HasPrefix(c.Text, "//") || !wholeLine(file.Source, c.Pos) || strings.HasPrefix(c.Text, "// bork:") {
			flush()
			continue
		}
		if end != 0 && c.Pos.Line != end+1 {
			flush()
		}
		group = append(group, strings.TrimPrefix(strings.TrimPrefix(c.Text, "//"), " "))
		end = c.Pos.Line
	}
	flush()
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

type block struct {
	text, language string
	code           bool
}

func blocks(text string) []block {
	var out []block
	var lines []string
	code, language := false, ""
	flush := func() {
		if len(lines) > 0 {
			out = append(out, block{strings.Join(lines, "\n"), language, code})
			lines = nil
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			flush()
			if !code {
				language = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "```"))
			}
			code = !code
			continue
		}
		if !code && strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		lines = append(lines, line)
	}
	flush()
	return out
}

// Markdown preserves prose and fenced examples, without allowing raw HTML.
func Markdown(text string) string {
	var parts []string
	for _, b := range blocks(text) {
		if b.code {
			parts = append(parts, Fence(b.language, b.text))
			continue
		}
		parts = append(parts, inlineMarkdown(b.text))
	}
	return strings.Join(parts, "\n\n")
}

// inlineMarkdown recognizes matched backtick runs; unmatched runs are prose.
func inlineMarkdown(text string) string {
	var out strings.Builder
	for len(text) > 0 {
		start := strings.IndexByte(text, '`')
		if start < 0 {
			out.WriteString(html.EscapeString(text))
			break
		}
		out.WriteString(html.EscapeString(text[:start]))
		text = text[start:]
		width := 0
		for width < len(text) && text[width] == '`' {
			width++
		}
		end := -1
		for offset := width; offset < len(text); {
			next := strings.IndexByte(text[offset:], '`')
			if next < 0 {
				break
			}
			begin := offset + next
			offset = begin
			for offset < len(text) && text[offset] == '`' {
				offset++
			}
			if offset-begin == width {
				end = offset
				break
			}
		}
		if end < 0 {
			out.WriteString(html.EscapeString(text[:width]))
			text = text[width:]
			continue
		}
		out.WriteString(text[:end])
		text = text[end:]
	}
	return out.String()
}

// HTML uses the same prose/example blocks with escaped source text.
func HTML(text string) string {
	var out strings.Builder
	for _, b := range blocks(text) {
		if b.code {
			out.WriteString("<pre><code>" + html.EscapeString(b.text) + "</code></pre>\n")
		} else {
			out.WriteString("<p>" + strings.ReplaceAll(html.EscapeString(b.text), "\n", "<br>\n") + "</p>\n")
		}
	}
	return out.String()
}

// Fence handles examples containing backticks without ending the code block.
func Fence(language, text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	ticks := strings.Repeat("`", max(3, longest+1))
	return ticks + language + "\n" + text + "\n" + ticks
}
