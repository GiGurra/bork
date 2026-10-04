package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/driver"
)

func TestWorkspaceRenameClosedImporters(t *testing.T) {
	root := t.TempDir()
	model := `type Item = { value: Int }
type Alias = Item
type Choice = sealed { Some { value: Int }, Empty }
fn New = Item.new
pred Positive(value: Int) { value > 0 }
fn Take(value: Int where Positive): Int where Positive { value }
Version = 3
`
	consumer := `import "example.com/rename/model"
fn Use(input: model.Alias): Int {
 other = model.New(value: input.value)
 match (other) { model.Alias { value } => value + model.Version }
}
fn Choice(input: model.Choice): Int {
 match (input) { model.Choice.Some { value: result } => result, model.Choice.Empty => 0 }
}
`
	files := map[string]string{"bork.mod": "module example.com/rename\n", "model/lib.bork": model, "consumer/use.bork": consumer}
	for name, text := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "model/lib.bork")
	s := &server{out: &bytes.Buffer{}, initialized: true, docs: map[string]document{path: {model, 1}}, packages: map[string]*packageState{}, diagnostics: map[string][]diag.Diagnostic{}}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if s.state(path).analysis == nil {
		t.Fatalf("initial check: %s", s.out)
	}
	for _, tc := range []struct {
		fragment, name string
		consumerEdits  int
	}{
		{"Item =", "Record", 0}, {"Alias =", "OtherAlias", 2}, {"value: Int }", "count", 3}, {"Some {", "Present", 1}, {"Empty }", "Absent", 1}, {"New =", "Create", 1}, {"Positive(value", "AboveZero", 0}, {"Version =", "Release", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offset := strings.Index(model, tc.fragment)
			pos := position{strings.Count(model[:offset], "\n"), offset - strings.LastIndex(model[:offset], "\n") - 1}
			params, _ := json.Marshal(map[string]any{"textDocument": map[string]string{"uri": fileURI(path)}, "position": pos, "newName": tc.name})
			result, rpcErr, _ := s.handle(message{Method: "textDocument/rename", Params: params})
			if rpcErr != nil {
				t.Fatal(rpcErr)
			}
			if result == nil {
				t.Fatal("missing rename")
			}
			changes := result.(map[string]any)["changes"].(map[string][]textEdit)
			edits := changes[fileURI(filepath.Join(root, "consumer/use.bork"))]
			if len(edits) != tc.consumerEdits {
				t.Fatalf("closed importer: got %d edits, want %d: %+v", len(edits), tc.consumerEdits, changes)
			}
			if tc.name == "count" {
				found := false
				for _, edit := range edits {
					found = found || edit.NewText == "count: value"
				}
				if !found {
					t.Fatal("shorthand binding was not preserved")
				}
			}
		})
	}
	// A closed importer with broken source makes the reference inventory unsafe.
	if err := os.WriteFile(filepath.Join(root, "consumer/use.bork"), []byte("fn Broken() { missing }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.feature("textDocument/rename", path, documentParams{Position: position{6, 0}, NewName: "Other"}); err == nil {
		t.Fatal("broken closed importer ignored")
	}
}

func TestRenameInterpolationAndSameSpelledField(t *testing.T) {
	s, path := newTestServer(t, "fn main() {}\n")
	for _, tc := range []struct {
		src   string
		pos   position
		name  string
		edits int
	}{
		{"fn Use(): String { s\"${{ local = 1; local }}\" }\nfn Other(local: Int): Int { local }\n", position{0, 25}, "changed", 2},
		{"type Item = { item: Int }\nfn Use(item: Item): Int { item.item }\n", position{1, 28}, "input", 2},
		{"type Item = { item: Int }\nfn Use(item: Item): Int { item.item }\n", position{0, 15}, "value", 2},
	} {
		replaceRenameSource(t, s, path, tc.src)
		result, err := s.feature("textDocument/rename", path, documentParams{Position: tc.pos, NewName: tc.name})
		if err != nil {
			t.Fatal(err)
		}
		if result == nil {
			t.Fatal("no indexed identity")
		}
		edits := result.(map[string]any)["changes"].(map[string][]textEdit)[fileURI(path)]
		if len(edits) != tc.edits {
			t.Fatalf("wrong edits: %+v", edits)
		}
	}
}

func TestRenameSpacedVariantPatterns(t *testing.T) {
	s, path := newTestServer(t, "fn main() {}\n")
	for _, qualification := range []string{"Choice . Some", "Choice.\n Some"} {
		src := "type Choice = sealed { Some { value: Int }, Empty }\nfn Use(x: Choice): Int { match (x) { " + qualification + " { value } => value, Choice.Empty => 0 } }\n"
		replaceRenameSource(t, s, path, src)
		result, err := s.feature("textDocument/rename", path, documentParams{Position: position{0, 24}, NewName: "Present"})
		if err != nil {
			t.Fatal(err)
		}
		if result == nil {
			t.Fatal("no variant identity")
		}
		if edits := result.(map[string]any)["changes"].(map[string][]textEdit)[fileURI(path)]; len(edits) != 2 {
			t.Fatalf("spaced variant omitted: %+v", edits)
		}
	}
}

func TestRenameMultilineContextVariants(t *testing.T) {
	s, path := newTestServer(t, "fn main() {}\n")
	for _, tail := range []string{"Some", "Some { value: 1 }"} {
		fields := ""
		if strings.Contains(tail, "{") {
			fields = " { value: Int }"
		}
		src := "type Choice = sealed { Some" + fields + ", Empty }\nfn Use(): Choice { .\n " + tail + " }\n"
		replaceRenameSource(t, s, path, src)
		result, err := s.feature("textDocument/rename", path, documentParams{Position: position{0, 24}, NewName: "Present"})
		if err != nil {
			t.Fatal(err)
		}
		if edits := result.(map[string]any)["changes"].(map[string][]textEdit)[fileURI(path)]; len(edits) != 2 {
			t.Fatalf("multiline context variant omitted: %+v", edits)
		}
	}
}

func TestRenameUnusedProviderBundleReference(t *testing.T) {
	src := "fn logFailures(): ScopePolicy { ScopePolicy.TaskTimeout { ms: 1 } }\nproviders Policies = { policy: logFailures }\n"
	s, path := newTestServer(t, src)
	result, err := s.feature("textDocument/rename", path, documentParams{Position: position{0, 4}, NewName: "ownPolicy"})
	if err != nil {
		t.Fatal(err)
	}
	changes := result.(map[string]any)["changes"].(map[string][]textEdit)
	if edits := changes[fileURI(path)]; len(edits) != 2 {
		t.Fatalf("unused provider reference silently rebound: %+v", edits)
	}
}

func TestRenameMockTargetAndHandle(t *testing.T) {
	src := "fn answer() uses io: Int { println(42); 42 }\ntest \"mock\" { h = mock answer() { 1 }; assertEqual(h.count(), 0) }\n"
	s, path := newTestServer(t, src)
	for _, tc := range []struct {
		p    position
		name string
	}{{position{0, 4}, "response"}, {position{1, 14}, "handle"}} {
		result, err := s.feature("textDocument/rename", path, documentParams{Position: tc.p, NewName: tc.name})
		if err != nil {
			t.Fatal(err)
		}
		if result == nil {
			t.Fatal("missing mock identity")
		}
		if edits := result.(map[string]any)["changes"].(map[string][]textEdit)[fileURI(path)]; len(edits) != 2 {
			t.Fatalf("mock reference omitted: %+v", edits)
		}
	}
}

func TestRenameRejectsCapture(t *testing.T) {
	s, path := newTestServer(t, "fn one(x: Int): Int { x }\nfn two(y: Int): Int { one(y) }\n")
	if _, err := s.feature("textDocument/rename", path, documentParams{Position: position{0, 4}, NewName: "y"}); err == nil {
		t.Fatal("capturing rename accepted")
	}
}

func TestWorkspacePackagesOverlay(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bork.mod"), []byte("module example.com/overlay\n"), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "new.bork")
	packages, err := driver.WorkspacePackages(path, nil, map[string]string{path: "fn main() {}\n"})
	if err != nil || len(packages) != 1 || packages[0] != root {
		t.Fatalf("new buffer omitted: %v %v", packages, err)
	}
}

func TestWorkspacePackagesExplicitNestedRoot(t *testing.T) {
	parent := t.TempDir()
	nested := filepath.Join(parent, "nested")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	for path, text := range map[string]string{
		filepath.Join(parent, "bork.mod"): "module example.com/parent\n", filepath.Join(parent, "main.bork"): "fn main() {}\n",
		filepath.Join(nested, "bork.mod"): "module example.com/nested\n", filepath.Join(nested, "main.bork"): "fn main() {}\n",
	} {
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, roots := range [][]string{{parent}, {parent, nested}} {
		packages, err := driver.WorkspacePackages(filepath.Join(nested, "main.bork"), roots, nil)
		if err != nil || len(packages) != 2 || packages[0] != parent || packages[1] != nested {
			t.Fatalf("explicit nested module omitted: %v %v", packages, err)
		}
	}
}

func BenchmarkWorkspaceRenameHTTPServer(b *testing.B) {
	path, err := filepath.Abs(filepath.Join("..", "..", "examples", "http_server", "main.bork"))
	if err != nil {
		b.Fatal(err)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	offset := strings.Index(string(text), "emptyStore()")
	pos := diag.Pos{File: path, Line: strings.Count(string(text[:offset]), "\n") + 1, Col: offset - strings.LastIndex(string(text[:offset]), "\n")}
	for i := 0; i < b.N; i++ {
		workspace, err := driver.AnalyzeWorkspace(path, nil, nil)
		if err != nil {
			b.Fatal(err)
		}
		var def *diag.Pos
		for _, analysis := range workspace.Analyses() {
			if candidate, _ := analysis.Definition(pos); candidate != nil {
				def = candidate
				break
			}
		}
		if def == nil {
			b.Fatal("missing checked function")
		}
		if _, err := workspace.Rename(*def, "initialStore"); err != nil {
			b.Fatal(err)
		}
	}
}

func replaceRenameSource(t *testing.T, s *server, path, source string) {
	t.Helper()
	s.docs[path] = document{source, s.docs[path].version + 1}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if pkg := s.state(path); pkg == nil || pkg.analysis == nil || pkg.stale {
		t.Fatalf("changed fixture did not check: %s", s.out)
	}
}
