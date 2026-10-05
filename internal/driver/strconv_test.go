package driver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GiGurra/bork/internal/format"
)

func TestStrconvDocumentation(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile(filepath.Join("..", "..", "docs", "std", "strconv.md"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := parseDocPage(string(contents), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range page.blocks {
		if block.lang != "bork" {
			continue
		}
		file := filepath.Join(t.TempDir(), "main.bork")
		if err := os.WriteFile(file, []byte(block.source), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Check(file); err != nil {
			t.Fatalf("line %d: %v", block.line, err)
		}
		formatted, err := format.Source(file, []byte(block.source))
		if err != nil {
			t.Fatal(err)
		}
		if string(formatted) != block.source {
			t.Fatalf("line %d needs formatting:\n%s", block.line, formatted)
		}
	}
}
