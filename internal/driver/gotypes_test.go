package driver

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/std"
	"github.com/GiGurra/bork/internal/syntax"
	"testing"
)

func TestGoBindingsReject32BitTarget(t *testing.T) {
	t.Setenv("GOARCH", "386")
	pkgs, errs := (goPackages{}).Load([]string{"strings"})
	if len(pkgs) != 0 || errs["strings"] == nil || !strings.Contains(errs["strings"].Error(), "64-bit Go target") {
		t.Fatalf("32-bit target must not use 64-bit integer conversions: packages=%v, errors=%v", pkgs, errs)
	}
}

// Shipped bindings would require checked signatures in the compiler so checking
// programs without user bindings does not depend on an installed Go toolchain.
// There are none yet; adding one must first add that signature mechanism.
func TestShippedGoBindingsRequireSignatures(t *testing.T) {
	diags := &diag.List{}
	files := prelude.Parse(diags)
	entries, err := os.ReadDir("../std")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		paths, sources, ok := std.Sources(std.Prefix + entry.Name())
		if !ok {
			continue
		}
		for i, path := range paths {
			files = append(files, syntax.Parse(path, sources[i], diags))
		}
	}
	if diags.Len() > 0 {
		t.Fatal(diags.Error())
	}
	for _, f := range files {
		for _, fn := range f.Funcs {
			if fn.GoBind != nil {
				t.Errorf("%s: shipped Go binding needs a checked signature snapshot before it can be added", fn.GoBind.Pos)
			}
		}
		for _, td := range f.Types {
			if td.GoName != nil {
				t.Errorf("%s: shipped Go type declaration needs a checked signature snapshot before it can be added", td.GoName.Pos)
			}
		}
	}
}

func TestHTTPTypeCheckWithoutGo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte("import \"bork/http\"\nfn Noop(request: http.Request) {}\nfn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, _, err := Check(root); err != nil {
		t.Fatalf("HTTP checking without Go: %v", err)
	}
}

// Importing a declaration with checked defaults does not evaluate predicates
// again; checking the declaring package itself still proves those defaults.
func TestImportedDefaultFactsWithoutGo(t *testing.T) {
	root := t.TempDir()
	api := filepath.Join(root, "api")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{
		filepath.Join(root, "bork.mod"):  "module example.com/defaultfacts\n",
		filepath.Join(root, "main.bork"): "import \"example.com/defaultfacts/api\"\nfn main() { _ = api.Outer {} }\n",
		filepath.Join(api, "api.bork"):   "pred positive(x: Int) { x > 0 }\ntype Inner = { value: Int where positive = 1 }\ntype Outer = { inner: Inner = Inner { value: 1 } }\n",
	}
	for path, source := range sources {
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := Check(api); err != nil {
		t.Fatalf("declaring package: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, _, err := Check(root); err != nil {
		t.Fatalf("imported defaults without Go: %v", err)
	}
	if _, _, err := Check(api); err == nil {
		t.Fatal("declaring package skipped default proof")
	}
}

func TestImportedDefaultsRetainUseSiteFacts(t *testing.T) {
	for name, main := range map[string]string{
		"explicit": "_ = api.Inner { value: 0 }",
		"sibling":  "_ = api.Pair { upper: 0 }",
		"generic":  "box: api.Box[Int] = api.Box {}",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			api := filepath.Join(root, "api")
			if err := os.MkdirAll(api, 0o755); err != nil {
				t.Fatal(err)
			}
			sources := map[string]string{
				filepath.Join(root, "bork.mod"):  "module example.com/defaultfacts\n",
				filepath.Join(root, "main.bork"): "import \"example.com/defaultfacts/api\"\nfn main() { " + main + " }\n",
				filepath.Join(api, "api.bork"):   "pred positive(x: Int) { x > 0 }\npred below(x: Int, upper: Int) { x < upper }\npred nonempty[T](xs: List[T]) { xs.length() > 0 }\ntype Inner = { value: Int where positive = 1 }\ntype Pair = { upper: Int, lower: Int where below(upper) = 0 }\ntype Box[T] = { xs: List[T] where nonempty = [] }\n",
			}
			for path, source := range sources {
				if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := Check(root); err == nil || !strings.Contains(err.Error(), "is false") {
				t.Fatalf("use-site facts unchecked: %v", err)
			}
		})
	}
}

// The HTTP package is normally imported by fixtures. Check its default facts
// with HTTP as the declaring root too, rather than trusting them in every test.
func TestHTTPDeclaredDefaultFacts(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte("import \"bork/http\"\nfn Noop(request: http.Request) {}\nfn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, _, diags, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	info := check.Program(files, "bork/http", diags, goPackages{files: files})
	if diags.Len() > 0 {
		t.Fatal(diags.Error())
	}
	check.Facts(files, info, diags, evaluator(root, files, info))
	if diags.Len() > 0 {
		t.Fatal(diags.Error())
	}
}
