package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
)

func TestEditorRebindingIdentities(t *testing.T) {
	source := "fn F(x: Int) uses io: String {\n  old = () => x\n  x = x + 1\n  x = s\"$x\"\n  println(old())\n  x\n}\nfn main() {}\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	a, err := NewSession().Analyze(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	param := diag.Pos{File: path, Line: 1, Col: 6}
	first := diag.Pos{File: path, Line: 3, Col: 3}
	last := diag.Pos{File: path, Line: 4, Col: 3}
	for _, tc := range []struct {
		pos, def diag.Pos
		typ      string
		previous *diag.Pos
	}{
		{diag.Pos{File: path, Line: 2, Col: 15}, param, "Int", nil},
		{diag.Pos{File: path, Line: 3, Col: 7}, param, "Int", nil},
		{diag.Pos{File: path, Line: 4, Col: 10}, first, "Int", &param},
		{diag.Pos{File: path, Line: 6, Col: 3}, last, "String", &first},
	} {
		def, err := a.Definition(tc.pos)
		if err != nil || def == nil || *def != tc.def {
			t.Fatalf("definition at %+v: %+v, %v", tc.pos, def, err)
		}
		description, err := a.Describe(tc.pos)
		if err != nil || description.Type != tc.typ {
			t.Fatalf("describe at %+v: %+v, %v", tc.pos, description, err)
		}
		if tc.previous == nil {
			if description.Rebinds != nil {
				t.Fatalf("unexpected rebinding: %+v", description)
			}
		} else if description.Rebinds == nil || *description.Rebinds != *tc.previous {
			t.Fatalf("wrong previous binding: %+v", description)
		}
	}
	for _, tc := range []struct {
		pos diag.Pos
		typ string
	}{
		{diag.Pos{File: path, Line: 3, Col: 7}, "Int"},
		{diag.Pos{File: path, Line: 4, Col: 10}, "Int"},
		{diag.Pos{File: path, Line: 6, Col: 3}, "String"},
	} {
		found := false
		for _, symbol := range a.EditorSymbols(tc.pos) {
			if symbol.Name == "x" {
				found = true
				if symbol.Detail != tc.typ {
					t.Fatalf("completion at %+v: %+v", tc.pos, symbol)
				}
			}
		}
		if !found {
			t.Fatalf("no x completion at %+v", tc.pos)
		}
	}
	inlays, err := a.EditorInlays(path, check.EditorInlayOptions{Types: true})
	if err != nil {
		t.Fatal(err)
	}
	labels := map[int]string{}
	for _, hint := range inlays {
		labels[hint.Pos.Line] = hint.Label
	}
	if labels[3] != ": Int" || labels[4] != ": String" {
		t.Fatalf("rebinding inlays: %+v", inlays)
	}
	tokens := a.SemanticTokens(path)
	count := 0
	for _, token := range tokens {
		if token.Rebinding {
			count++
			if !token.Declaration || token.Kind != "variable" {
				t.Fatalf("rebinding token: %+v", token)
			}
		}
	}
	if count != 2 {
		t.Fatalf("rebinding tokens: %+v", tokens)
	}
	// An extracted expression captures the most recent declaration of x.
	text, err := a.EditorExtractFunction(diag.Pos{File: path, Line: 4, Col: 7}, diag.Pos{File: path, Line: 4, Col: 12})
	if err != nil || !strings.Contains(text, "x: Int") {
		t.Fatalf("extraction: %v\n%s", err, text)
	}
}

func TestRenameOneRebinding(t *testing.T) {
	source := "fn F(x: Int) uses io: String {\n  old = () => x\n  x = x + 1\n  x = s\"$x\"\n  println(old())\n  x\n}\nfn main() {}\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	w, err := AnalyzeWorkspace(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		line, col, count int
		name             string
	}{
		{1, 6, 3, "input"}, {3, 3, 2, "incremented"}, {4, 3, 2, "formatted"},
	} {
		edits, err := w.Rename(diag.Pos{File: path, Line: tc.line, Col: tc.col}, tc.name)
		if err != nil || len(edits) != tc.count {
			t.Fatalf("rename %s: %+v, %v", tc.name, edits, err)
		}
		for _, edit := range edits {
			if edit.Replacement != tc.name {
				t.Fatalf("unexpected rename: %+v", edit)
			}
		}
	}
}

func TestUnusedBindingFixes(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		fixes        int
	}{
		{"literal", `fn main() { unused = (1) }`, 2},
		{"typed discard", `fn main() { value: List[Int] = [] }`, 1},
		{"field", `type R = { value: Int }; fn main() { _ = match (R { value: 1 }) { R { value } => 0 } }`, 1},
		{"rest with space", `fn main() { _ = match ([1]) { [_, ... rest] => 0; _ => 1 } }`, 1},
		{"type", `fn f(x: Int | String): Int { match (x) { n: Int => 1; String => 2 } }`, 1},
		{"guarded type", `pred positive(n: Int) { n > 0 }; fn f(x: Int): Int { match (x) { n: Int where positive => 1; _ => 2 } }`, 1},
		{"loop", `fn main() { for (value in [1]) {} }`, 1},
		{"effectful value", `fn work() uses io: Int { println("side effect"); 1 }; fn main() { unused = work() }`, 1},
		{"deferred value", `fn main() { lazy unused: Int = { println("deferred"); 1 } }`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "main.bork")
			if err := os.WriteFile(path, []byte(tc.source), 0644); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(path)
			de, ok := err.(*DiagError)
			if !ok || de.Diags.Len() != 1 {
				t.Fatalf("expected one unused error: %v", err)
			}
			d := de.Diags.Sorted()[0]
			if d.Code != "binding.unused" || len(d.Fixes) != tc.fixes {
				t.Fatalf("diagnostic: %+v", d)
			}
			for _, fix := range d.Fixes {
				fixed := applyLintEdits(t, tc.source, fix.Edits)
				if err := os.WriteFile(path, []byte(fixed), 0644); err != nil {
					t.Fatal(err)
				}
				if _, _, err := Check(path); err != nil {
					t.Fatalf("fix %s fails: %v\n%s", fix.Message, err, fixed)
				}
			}
		})
	}
}
