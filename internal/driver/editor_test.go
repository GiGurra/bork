package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func BenchmarkEditorPhases(b *testing.B) {
	for _, name := range []string{"hello", "http_server"} {
		b.Run(name, func(b *testing.B) {
			dir, err := filepath.Abs(filepath.Join("..", "..", "examples", name))
			if err != nil {
				b.Fatal(err)
			}
			path := filepath.Join(dir, "main.bork")
			src, err := os.ReadFile(path)
			if err != nil {
				b.Fatal(err)
			}
			session := NewSession()
			if _, err := session.Analyze(dir, map[string]string{path: string(src)}); err != nil {
				b.Fatal(err)
			}
			times := map[string]time.Duration{}
			previous := ""
			since := time.Now()
			session.observe = func(next string) {
				now := time.Now()
				if previous != "" {
					times[previous] += now.Sub(since)
				}
				previous, since = next, now
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				text := string(src) + strings.Repeat("\n", (i+1)%2)
				if _, err := session.Analyze(dir, map[string]string{path: text}); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			for phase, duration := range times {
				b.ReportMetric(float64(duration.Microseconds())/float64(b.N), phase+"-us/op")
			}
		})
	}
}

func TestEditorMethodDeclarationIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	source := "type Item = { n: Int }\nfn (value: Item) value(): Int { value.n }\nfn main() uses io { println(Item { n: 1 }.value()) }\n"
	a, err := NewSession().Analyze(dir, map[string]string{path: source})
	if err != nil {
		t.Fatal(err)
	}
	def, err := a.Definition(diag.Pos{File: path, Line: 3, Col: 43})
	if err != nil || def == nil || def.Line != 2 || def.Col != 18 {
		t.Fatalf("method identity selected receiver: %+v %v", def, err)
	}
}
