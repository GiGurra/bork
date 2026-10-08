package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTMLInterpolatorExampleEscapesQuotes(t *testing.T) {
	t.Parallel()
	text, err := os.ReadFile(filepath.Join("..", "..", "docs", "language", "interpolators.md"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := parseDocPage(string(text), true)
	if err != nil {
		t.Fatal(err)
	}
	for index, block := range page.blocks {
		if block.lang != "bork" || !strings.Contains(block.source, "fn escape(") {
			continue
		}
		if index+1 >= len(page.blocks) || page.blocks[index+1].lang != "text" {
			t.Fatal("HTML example needs expected output")
		}
		path := filepath.Join(t.TempDir(), "main.bork")
		if err := os.WriteFile(path, []byte(block.source), 0600); err != nil {
			t.Fatal(err)
		}
		executable := filepath.Join(t.TempDir(), "html")
		if err := Build(path, executable); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command(executable).CombinedOutput()
		if err != nil {
			t.Fatalf("HTML example: %v\n%s", err, output)
		}
		if string(output) != page.blocks[index+1].source {
			t.Fatalf("HTML example output:\n%s\nwant:\n%s", output, page.blocks[index+1].source)
		}
		if !strings.Contains(string(output), "&quot;") || !strings.Contains(string(output), "&#39;") {
			t.Fatal("example must exercise both quote kinds")
		}
		return
	}
	t.Fatal("HTML example not found")
}
