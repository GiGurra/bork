package driver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Public examples are the top-level, non-hidden directories with a main.bork
// entry point. Nested packages and support fixtures are not index entries.
func unlistedPublicExamples(root, markdown string) ([]string, error) {
	linked := map[string]bool{}
	for _, match := range regexp.MustCompile(`\]\(\.\./examples/([^/)#]+)(?:[/)#])`).FindAllStringSubmatch(markdown, -1) {
		linked[match[1]] = true
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		_, err := os.Stat(filepath.Join(root, entry.Name(), "main.bork"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !linked[entry.Name()] {
			missing = append(missing, entry.Name())
		}
	}
	return missing, nil
}

func TestExamplesIndexCoverage(t *testing.T) {
	root := filepath.Join("..", "..")
	markdown, err := os.ReadFile(filepath.Join(root, "docs", "examples.md"))
	if err != nil {
		t.Fatal(err)
	}
	missing, err := unlistedPublicExamples(filepath.Join(root, "examples"), string(markdown))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range missing {
		t.Errorf("public example %s has no link in docs/examples.md", name)
	}
}

func TestExamplesIndexIgnoresSupportFixtures(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"demo/main.bork", "demo/nested/main.bork", ".fixture/main.bork", "support/helper.bork"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	missing, err := unlistedPublicExamples(root, "No examples linked yet.")
	if err != nil || len(missing) != 1 || missing[0] != "demo" {
		t.Fatalf("missing = %v, err = %v; want [demo]", missing, err)
	}
	missing, err = unlistedPublicExamples(root, "[Demo](../examples/demo/README.md)")
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing = %v, err = %v; want none", missing, err)
	}
}
