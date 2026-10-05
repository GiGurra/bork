package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
)

func TestEditorTuples(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	source := "fn main() {\n  pair = (3, \"count\")\n  (number, label) = pair\n  println(number, label, pair.0)\n}\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	analysis, err := NewSession().Analyze(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	position := func(line int, name string) diag.Pos {
		return diag.Pos{File: path, Line: line, Col: strings.Index(strings.Split(source, "\n")[line-1], name) + 1}
	}
	result, err := analysis.Describe(position(4, "pair.0"))
	if err != nil || result.Type != "(Int, String)" {
		t.Fatalf("tuple hover: %+v, %v", result, err)
	}
	result, err = analysis.Describe(position(4, "0"))
	if err != nil || result.Type != "Int" {
		t.Fatalf("projection hover: %+v, %v", result, err)
	}
	fields := analysis.EditorMembers(position(4, "pair.0"))
	found := map[string]string{}
	for _, field := range fields {
		found[field.Name] = field.Detail
	}
	if found["0"] != "Int" || found["1"] != "String" {
		t.Fatalf("tuple completions: %+v", fields)
	}
	ref := analysis.ReferenceAt(position(4, "number"))
	if ref == nil || ref.Definition != position(3, "number") {
		t.Fatalf("destructured definition: %+v", ref)
	}
	if refs := analysis.References(ref.Definition); len(refs) != 2 {
		t.Fatalf("destructured rename references: %+v", refs)
	}
	hints, err := analysis.EditorInlays(path, check.EditorInlayOptions{Types: true})
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]bool{}
	for _, hint := range hints {
		labels[hint.Label] = true
	}
	if !labels[": (Int, String)"] || !labels[": Int"] || !labels[": String"] {
		t.Fatalf("tuple inlay hints: %+v", hints)
	}
	tokens := analysis.SemanticTokens(path)
	declaration := false
	for _, token := range tokens {
		if token.Start == position(3, "number") && token.Declaration && token.Kind == "variable" {
			declaration = true
		}
	}
	if !declaration {
		t.Fatal("destructured name has no declaration semantic token")
	}
}
