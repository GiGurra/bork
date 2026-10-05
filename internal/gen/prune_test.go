package gen

import (
	"bytes"
	"go/format"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestPruneFunctions(t *testing.T) {
	src := []byte(`package main
import "fmt"
import "math"
import "os"
var initialized = initialize()
func initialize() int { return 1 }
type widget struct {}
func (widget) String() string { return render() }
func render() string { return "widget" }
func main() { callback := keep; callback() }
func keep() { /*line example.bork:7:3*/ fmt.Print(initialized) }
func unused() { math.Abs(1) }
func cycleA() { cycleB() }
func cycleB() { cycleA() }
//go:linkname linked external/pkg.symbol
func linked() {}
`)
	got, err := pruneFunctions(src, map[string]bool{"main": true})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, keep := range []string{"func initialize", "func (widget) String", "func render", "func main", "func keep", "func linked", "/*line example.bork:7:3*/", `import _ "math"`, `import "os"`} {
		if !strings.Contains(text, keep) {
			t.Errorf("lost %q", keep)
		}
	}
	for _, removed := range []string{"func unused", "func cycleA", "func cycleB"} {
		if strings.Contains(text, removed) {
			t.Errorf("retained %q", removed)
		}
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "generated.go", got, parser.ParseComments); err != nil {
		t.Fatal(err)
	}
}

func TestPrunePreservedFunctions(t *testing.T) {
	// User functions stay roots even when never called, so unsafe Go still
	// receives Go validation. Helpers they mention remain reachable too.
	src := []byte("package main\nfunc main() {}\nfunc user() { helper(); undefined() }\nfunc helper() {}\n")
	got, err := pruneFunctions(src, map[string]bool{"main": true, "user": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "func user()") || !strings.Contains(string(got), "undefined()") || !strings.Contains(string(got), "func helper()") {
		t.Fatalf("preserved source changed:\n%s", got)
	}
}

func pruneFunctions(src []byte, preserved map[string]bool) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	live := liveFunctions(file.Decls, preserved, file.Comments)
	all, retained := packageUses(file.Decls, live)
	preserveImports(file.Decls, all, retained)
	file.Decls = liveDecls(file.Decls, live)
	var out bytes.Buffer
	if err := format.Node(&out, fset, file); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func TestRemoveFunctionsPhysicalOffsets(t *testing.T) {
	src := []byte("package main\n//line source.bork:70:4\nfunc unused() { /*line inner.bork:900:2*/ println(1) }\nfunc main() { /*line kept.bork:7:3*/ println(2) }\n")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "helpers.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	live := liveFunctions(file.Decls, map[string]bool{"main": true}, file.Comments)
	got := removeFunctions(src, fset, file.Decls, live)
	want := "package main\n//line source.bork:70:4\n\nfunc main() { /*line kept.bork:7:3*/ println(2) }\n"
	if string(got) != want {
		t.Fatalf("raw deletion changed preserved text:\ngot %q\nwant %q", got, want)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "helpers.go", got, parser.ParseComments); err != nil {
		t.Fatal(err)
	}
}

func TestImportName(t *testing.T) {
	for path, want := range map[string]string{"math/rand/v2": "rand", "fmt": "fmt", "net/http": "http", "example.com/v2": "example.com", "example.com/v": "v"} {
		if got := importName(path); got != want {
			t.Errorf("importName(%q) = %q, want %q", path, got, want)
		}
	}
}
