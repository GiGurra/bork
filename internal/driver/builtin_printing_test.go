package driver

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestEprintlnChecks(t *testing.T) {
	for _, tc := range []struct{ name, source, diagnostic string }{
		{"values", "fn main() uses io { eprintln(); eprintln(1, true, [1, 2], {\"a\": 1}) }", ""},
		{"nonvalue", "fn main() uses io { eprintln(println(1)) }", "eprintln cannot print"},
		{"effects", "fn quiet() uses nothing { eprintln(1) }", "io"},
		{"generic effects", "fn quiet[T](value: T) uses nothing { eprintln(value) }", "io"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixtureFile(t, root, "main.bork", tc.source+"\n")
			_, _, err := Check(root)
			if tc.diagnostic == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.diagnostic) {
				t.Fatalf("wanted %q, got %v", tc.diagnostic, err)
			}
		})
	}
}

func TestEditorHidesCompilerPrelude(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.bork")
	writeFixtureFile(t, root, "main.bork", "fn compilerSelectLocal(): Int { 1 }\nfn main() uses io { println(compilerSelectLocal()) }\n")
	analysis, err := NewSession().Analyze(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	symbols := analysis.EditorSymbols(diag.Pos{File: path, Line: 2, Col: 30})
	seen := map[string]bool{}
	for _, symbol := range symbols {
		seen[symbol.Name] = true
		if symbol.Name == "SelectHandle" || strings.HasPrefix(symbol.Name, "compiler") && symbol.Name != "compilerSelectLocal" {
			t.Errorf("compiler helper completion: %s", symbol.Name)
		}
	}
	for _, name := range []string{"compilerSelectLocal", "println", "eprintln", "fork", "String"} {
		if !seen[name] {
			t.Errorf("missing visible completion: %s", name)
		}
	}
}
