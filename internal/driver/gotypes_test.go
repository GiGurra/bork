package driver

import (
	"os"
	"path/filepath"
	"strings"

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
