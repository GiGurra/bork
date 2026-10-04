package driver

import (
	"path/filepath"
	"testing"
)

func TestEditorLibraryImportCatalog(t *testing.T) {
	proxy := newLibraryProxy(t)
	proxy.publish("example.com/library", "v1.0.0", "module example.com/library\n", map[string]string{
		"api/main.bork":   "fn Value(): Int { 42 }\n",
		"extra/main.bork": "fn Extra(): Int { 43 }\nfn hidden(): Int { 44 }\n",
	})
	dir := t.TempDir()
	writeFixtureFile(t, dir, ModFile, "module example.com/app\n")
	writeFixtureFile(t, dir, "main.bork", "import \"example.com/library/api\"\nfn main() uses io { println(api.Value()) }\n")
	if err := Deps(dir, "get", []string{"example.com/library@v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	analysis, err := NewSession().Analyze(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Completion must use the graph retained by checking, not resolve again.
	t.Setenv("GOPROXY", "http://127.0.0.1:1")
	found := false
	for _, symbol := range analysis.EditorImportSymbols(filepath.Join(dir, "main.bork")) {
		if symbol.ImportPath != "example.com/library/extra" {
			continue
		}
		if symbol.Name == "hidden" {
			t.Fatal("private dependency symbol offered")
		}
		if symbol.Name == "Extra" {
			found = true
		}
	}
	if !found {
		t.Fatal("unimported dependency package missing")
	}
}
