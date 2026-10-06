package driver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
)

func TestBuiltinReferenceCoverage(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "main.bork", "fn main() {}\n")
	files, info, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	api := check.BuiltinAPI(info, files)
	seen := map[string]bool{}
	for _, declaration := range api.Declarations {
		if declaration.Signature == "" || strings.TrimSpace(declaration.Documentation) == "" {
			t.Errorf("missing builtin signature or comment: %s.%s", declaration.Receiver, declaration.Name)
		}
		if !check.PreludeVisible(declaration.Name) {
			t.Errorf("internal builtin reference entry: %s", declaration.Name)
		}
		receiver := strings.Split(declaration.Receiver, "[")[0]
		seen[receiver+"."+declaration.Name] = true
	}
	namePattern := regexp.MustCompile("`([a-z][A-Za-z0-9]*)`")
	for _, group := range []struct{ file, pattern, receiver string }{
		{"basics.md", `(?m)^String methods include (.*)$`, "String"},
		{"types.md", `(?m)^.*Option methods are (.*)$`, "Option"},
		{"collections.md", `(?m)^The list methods:\n\n((?:\|.*\n)+)`, "List"},
		{"collections.md", `(?m)^.*Methods are (.*?)\.`, "Bytes"},
		{"collections.md", `(?m)^The map methods: (.*)$`, "Map"},
	} {
		source, err := os.ReadFile(filepath.Join("..", "..", "docs", "language", group.file))
		if err != nil {
			t.Fatal(err)
		}
		match := regexp.MustCompile(group.pattern).FindStringSubmatch(string(source))
		if len(match) != 2 {
			t.Fatalf("method list missing in %s", group.file)
		}
		for _, name := range namePattern.FindAllStringSubmatch(match[1], -1) {
			if !seen[group.receiver+"."+name[1]] && !seen["."+name[1]] {
				t.Errorf("%s method list entry %s is absent from reference", group.file, name[1])
			}
		}
	}
}
