package driver

import (
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestOrganizeImportsDuplicates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, source, want string
		diagnostics        []diag.Diagnostic
	}{
		{name: "identical", source: "import \"bork/encoding\"\nimport \"bork/encoding\"\nfn main() {}\n", want: "import \"bork/encoding\"\nfn main() {}\n"},
		{name: "effective alias", source: "import \"bork/encoding\"\nimport encoding \"bork/encoding\"\nfn main() {}\n", want: "import \"bork/encoding\"\nfn main() {}\n"},
		{name: "distinct aliases", source: "import codec \"bork/encoding\"\nimport \"bork/encoding\"\nfn main() {}\n", want: "import codec \"bork/encoding\"\nimport \"bork/encoding\"\nfn main() {}\n"},
		{name: "distinct paths", source: "import codec \"bork/encoding\"\nimport codec \"bork/json\"\nfn main() {}\n", want: "import codec \"bork/encoding\"\nimport codec \"bork/json\"\nfn main() {}\n"},
		{name: "inline separators", source: "import \"bork/encoding\"; import \"bork/encoding\"; fn main() {}\n", want: "import \"bork/encoding\"\nfn main() {}\n"},
		{name: "unused first", source: "import \"bork/encoding\"\nimport \"bork/encoding\"\nfn main() {}\n", want: "import \"bork/encoding\"\nfn main() {}\n", diagnostics: []diag.Diagnostic{{Pos: diag.Pos{File: "main.bork", Line: 1, Col: 1}, Code: "import.unused"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EditorOrganizeImports("main.bork", tc.source, tc.diagnostics)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			again, err := EditorOrganizeImports("main.bork", got, nil)
			if err != nil || again != got {
				t.Fatalf("not idempotent: %q, %v", again, err)
			}
		})
	}
}

func TestOrganizeImportsDuplicateComments(t *testing.T) {
	source := "import \"bork/encoding\" // retained\n// before duplicate\nimport /* inside duplicate */ \"bork/encoding\" // after duplicate\nfn main() {}\n"
	got, err := EditorOrganizeImports("main.bork", source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "import ") != 1 {
		t.Fatalf("duplicate import remains: %s", got)
	}
	for _, comment := range []string{"// retained", "// before duplicate", "/* inside duplicate */", "// after duplicate"} {
		if strings.Count(got, comment) != 1 {
			t.Fatalf("comment %q was lost or repeated: %s", comment, got)
		}
	}
	again, err := EditorOrganizeImports("main.bork", got, nil)
	if err != nil || again != got {
		t.Fatalf("not idempotent: %q, %v", again, err)
	}
}
