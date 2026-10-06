package syntax

import (
	"sort"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestProviderMigrationFix(t *testing.T) {
	for _, source := range []string{
		"providers Wiring = { one: first, two: second }\n",
		"providers Wiring = {; one: first; two: second }\n",
		"providers Wiring = {\n // first comment\n one: first\n // second comment\n two: second\n}\n",
		"providers Wiring = { one /* label */: /* value */ first; two: second; }\n",
		"providers Wiring = { one: first }\n",
		"providers Wiring = { one: first, }\n",
	} {
		t.Run(source, func(t *testing.T) {
			var diagnostics diag.List
			file := Parse("main.bork", []byte(source), &diagnostics)
			if len(file.Bindings) != 0 {
				t.Fatal("legacy declaration became usable")
			}
			items := diagnostics.Sorted()
			if len(items) != 1 || items[0].Code != "migration.providers" || len(items[0].Fixes) != 1 {
				t.Fatalf("missing migration fix: %+v", items)
			}
			offset := func(pos diag.Pos) int {
				lines := strings.Split(source, "\n")
				n := pos.Col - 1
				for _, line := range lines[:pos.Line-1] {
					n += len(line) + 1
				}
				return n
			}
			edits := items[0].Fixes[0].Edits
			sort.SliceStable(edits, func(i, j int) bool { return offset(edits[i].Start) > offset(edits[j].Start) })
			fixed := source
			for _, edit := range edits {
				fixed = fixed[:offset(edit.Start)] + edit.Replacement + fixed[offset(edit.End):]
			}
			var after diag.List
			migrated := Parse("main.bork", []byte(fixed), &after)
			if after.Len() != 0 || len(migrated.Bindings) != 1 {
				t.Fatalf("invalid fix %s: %+v", fixed, after.Sorted())
			}
			if _, ok := migrated.Bindings[0].Value.(*TupleLit); !ok {
				t.Fatalf("fix did not make a tuple: %s", fixed)
			}
			for _, comment := range file.Comments {
				if !strings.Contains(fixed, comment.Text) {
					t.Fatalf("lost comment %s", comment.Text)
				}
			}
		})
	}
}

func TestProviderMigrationUnsafeFix(t *testing.T) {
	for _, source := range []string{"providers Empty = {}", "providers Bad = { a: first, a: second }", "providers Bad = { a: () => 1 }"} {
		var diagnostics diag.List
		Parse("main.bork", []byte(source), &diagnostics)
		for _, item := range diagnostics.Sorted() {
			if len(item.Fixes) != 0 {
				t.Fatalf("unsafe fix for %s: %+v", source, item)
			}
		}
	}
}
