package doccomment

import (
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

func TestCommentOwnership(t *testing.T) {
	source := "// Package summary.\n\n// Value documentation.\nfn Value(): Int { 1 }\n\n// Record documentation.\ntype Record = { inline: Int }\n// Field record.\ntype Fields = {\n    // Indented field documentation.\n    value: Int,\n}\n"
	diags := &diag.List{}
	file := syntax.Parse("api.bork", []byte(source), diags)
	if diags.Len() != 0 {
		t.Fatal(diags)
	}
	if got := Package(file); got != "Package summary." {
		t.Fatalf("package: %q", got)
	}
	if got := At(file, file.Funcs[0].Pos); got != "Value documentation." {
		t.Fatalf("function: %q", got)
	}
	if got := At(file, file.Types[0].Fields[0].Pos); got != "" {
		t.Fatalf("inline field inherited comments: %q", got)
	}
	if got := At(file, file.Types[1].Fields[0].Pos); got != "Indented field documentation." {
		t.Fatalf("field: %q", got)
	}
}

func TestRenderExamplesAndHTML(t *testing.T) {
	text := "Uses <script>escaped</script> and `code`.\n\n```bork\nprintln(\"<value>\")\n```"
	md, html := Markdown(text), HTML(text)
	if strings.Contains(md, "<script>") || strings.Contains(html, "<script>") || !strings.Contains(md, "```bork\nprintln(\"<value>\")") || !strings.Contains(html, "&lt;value&gt;") {
		t.Fatalf("rendering: %s / %s", md, html)
	}
	if got := Fence("bork", "a ``` b"); got != "````bork\na ``` b\n````" {
		t.Fatalf("fence: %q", got)
	}
}

func TestMarkdownUnmatchedAndMultipleBackticks(t *testing.T) {
	for _, text := range []string{"unmatched ` <script>unsafe</script>", "double `` <script>unsafe</script> `"} {
		if got := Markdown(text); strings.Contains(got, "<script>") {
			t.Fatalf("raw HTML after unmatched delimiter: %q", got)
		}
	}
	if got := Markdown("Use ``<tag> ` value`` and <script>text</script>"); got != "Use ``<tag> ` value`` and &lt;script&gt;text&lt;/script&gt;" {
		t.Fatalf("matched span: %q", got)
	}
}
