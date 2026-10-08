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

func TestTryMapperHermetic(t *testing.T) {
	for _, test := range []struct {
		name, body string
		code       int
	}{
		{"immediate", `assertEqual(remote(), 42)`, 0},
		{"nested lambda", `call = () => remote(); assertEqual(call(), 42)`, 1},
		{"nested lazy", `lazy value = remote(); assertEqual(value, 42)`, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			source := `type Failure = {}
fn source(): Ok | Failure { Failure {} }
fn remote() uses net: Int unsafe go { return 1 }
fn main() {}
test "wrapped" {
  {
    mock remote() { 42 }
    source()?{ _ => { ` + test.body + ` } }
  }
}
`
			for name, text := range map[string]string{"main.bork": source, "bork.mod": "module wraphermetic\nunsafe \"wraphermetic\"\n"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var out strings.Builder
			code, err := Test(dir, &out, TestOptions{Hermetic: true})
			if err != nil || code != test.code {
				t.Fatalf("hermetic test returned %d, %v: %s", code, err, out.String())
			}
			if (test.code == 1) != strings.Contains(out.String(), "not hermetic") {
				t.Fatalf("unexpected hermetic result: %s", out.String())
			}
		})
	}
}
