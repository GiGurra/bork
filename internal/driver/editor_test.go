package driver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestEditorOverlays(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	disk := "fn main() uses io { println(missing) }\n"
	if err := os.WriteFile(path, []byte(disk), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "fn main() uses io { println(answer()) }\n"
	sibling := filepath.Join(dir, "new.bork")
	overlays := map[string]string{path: src, sibling: "fn answer(): Int { 42 }\n"}
	session := NewSession()
	a, err := session.Analyze(dir, overlays)
	if err != nil {
		t.Fatal(err)
	}
	b, err := session.Analyze(dir, overlays)
	if err != nil || a != b {
		t.Fatalf("unchanged editor snapshot not reused: %v", err)
	}
	def, err := a.Definition(diag.Pos{File: path, Line: 1, Col: 29})
	if err != nil || def == nil || def.File != sibling || def.Col != 4 {
		t.Fatalf("definition: %+v, %v", def, err)
	}
	overlays[sibling] = "fn answer(): Int { false }\n"
	if _, err := session.Analyze(dir, overlays); err == nil {
		t.Fatal("invalid overlay accepted")
	}
	result, err := a.Describe(diag.Pos{File: path, Line: 1, Col: 29})
	if err != nil || result.Type != "() => Int" {
		t.Fatalf("previous snapshot changed: %+v, %v", result, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != disk {
		t.Fatal("overlay changed disk source")
	}
	if _, err := os.Stat(sibling); !os.IsNotExist(err) {
		t.Fatal("new overlay written to disk")
	}
}

func TestEditorImportedOverlay(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.bork")
	lib := filepath.Join(dir, "lib", "lib.bork")
	for path, text := range map[string]string{
		filepath.Join(dir, "bork.mod"): "module example.com/editor\n",
		main:                           "import \"example.com/editor/lib\"\nfn main() uses io { println(lib.Answer()) }\n",
		lib:                            "fn Answer(): Int { 42 }\n",
	} {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	session := NewSession()
	a, err := session.Analyze(dir, map[string]string{lib: "fn Answer(): Int { 43 }\n"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Sources()[lib] != "fn Answer(): Int { 43 }\n" {
		t.Fatal("import ignored overlay")
	}
	result, err := a.Describe(diag.Pos{File: lib, Line: 1, Col: 20})
	if err != nil || result.Type != "Int" {
		t.Fatalf("import query: %+v %v", result, err)
	}
	if _, err := session.Analyze(dir, map[string]string{lib: "fn Answer(): Int { false }\n"}); err == nil {
		t.Fatal("broken import ignored")
	}
}
