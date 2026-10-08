package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/gen"
)

func TestEditorTryMapper(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	src := `type Failure = { code: Int }
type Wrapped = { cause: Failure }
fn source(): Int | Failure { Failure { code: 1 } }
fn wrapped(): Int | Wrapped { source()?{ error => Wrapped { cause: error } } }
fn main() {}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := NewSession().Analyze(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Split(src, "\n")[3]
	site := diag.Pos{File: path, Line: 4, Col: strings.LastIndex(line, "error") + 1}
	def, err := a.Definition(site)
	if err != nil || def == nil || def.Line != 4 || def.Col != strings.Index(line, "error")+1 {
		t.Fatalf("definition: %+v, %v", def, err)
	}
	description, err := a.Describe(site)
	if err != nil || description.Type != "Failure" {
		t.Fatalf("describe: %+v, %v", description, err)
	}
}

func TestDebugTryMapperSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.bork")
	src := `type Failure = {}
type Wrapped = { cause: Failure }
fn source(): Int | Failure { Failure {} }
fn wrapped(): Int | Wrapped {
  source()?{ error => {
    Wrapped { cause: error }
  } }
}
fn main() uses io { println(wrapped()) }
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	files, info, err := Check(path)
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := gen.DebugPackageMap(files, info, filepath.Join(t.TempDir(), "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{":5", ":6"} {
		if !strings.Contains(string(source), "//line "+path+line+"\n") {
			t.Fatalf("mapper source location %s is missing", line)
		}
	}
}
