package driver

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestSuggestedEdits(t *testing.T) {
	cases := []struct {
		name, source string
	}{
		{"operator", "fn main() { println(true & false) }\n"},
		{"annotations", "fn main() {\n xs = []\n m = {:}\n f = x => x\n g = (y) => y\n h = (a, b) => a\n}\n"},
		{"multiline", "fn main() {\n xs = [\n ]\n m = {\n :}\n f = (\n x,\n y\n ) => x\n}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.bork")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(path)
			var de *DiagError
			if !errors.As(err, &de) {
				t.Fatalf("expected diagnostics, got %v", err)
			}
			var edits []diag.TextEdit
			for _, d := range de.Diags.Sorted() {
				for _, fix := range d.Fixes {
					for _, edit := range fix.Edits {
						if fix.RequiresInput {
							edit.Replacement = strings.NewReplacer("Element", "Int", "Key", "String", "Value", "Int", "Type", "Int").Replace(edit.Replacement)
						}
						if edit.Start.File != path || edit.End.File != path {
							t.Fatalf("edit points to another file: %+v", edit)
						}
						edits = append(edits, edit)
					}
				}
			}
			if len(edits) == 0 {
				t.Fatal("no suggested edits")
			}
			offset := func(pos diag.Pos) int {
				lines := strings.SplitAfter(tc.source, "\n")
				n := pos.Col - 1
				for _, line := range lines[:pos.Line-1] {
					n += len(line)
				}
				return n
			}
			sort.Slice(edits, func(i, j int) bool { return offset(edits[i].Start) > offset(edits[j].Start) })
			fixed := tc.source
			for _, edit := range edits {
				fixed = fixed[:offset(edit.Start)] + edit.Replacement + fixed[offset(edit.End):]
			}
			if err := os.WriteFile(path, []byte(fixed), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Check(path); err != nil {
				t.Fatalf("suggested edits did not fix the program:\n%s\n%v", fixed, err)
			}
		})
	}
}
