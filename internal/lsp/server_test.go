package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/driver"
)

func newTestServer(t *testing.T, src string) (*server, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &server{out: &bytes.Buffer{}, initialized: true, docs: map[string]document{path: {src, 1}}, packages: map[string]*packageState{}, diagnostics: map[string][]diag.Diagnostic{}}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if pkg := s.state(path); pkg == nil || pkg.analysis == nil {
		t.Fatalf("failed initial check: %s", s.out)
	}
	return s, path
}
func query(t *testing.T, s *server, path, method string, line, col int) any {
	t.Helper()
	p := documentParams{Position: position{line, col}}
	p.TextDocument.URI = fileURI(path)
	result, err := s.feature("textDocument/"+method, path, p)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestFeaturesAndStaleAnalysis(t *testing.T) {
	src := "fn main() uses io {\n  value = 42\n  println(value)\n}\n"
	s, path := newTestServer(t, src)
	hover := query(t, s, path, "hover", 2, 10)
	b, _ := json.Marshal(hover)
	if !bytes.Contains(b, []byte("Int")) {
		t.Fatalf("hover: %s", b)
	}
	def := query(t, s, path, "definition", 2, 10).(location)
	if def.Range.Start != (position{1, 2}) {
		t.Fatalf("definition: %+v", def)
	}
	p := documentParams{Position: position{2, 10}, NewName: "renamed"}
	p.Context.IncludeDeclaration = true
	refs, err := s.feature("textDocument/references", path, p)
	if err != nil || len(refs.([]location)) != 2 {
		t.Fatalf("references: %+v %v", refs, err)
	}
	rename, err := s.feature("textDocument/rename", path, p)
	if err != nil {
		t.Fatal(err)
	}
	changes := rename.(map[string]any)["changes"].(map[string][]textEdit)
	if len(changes[fileURI(path)]) != 2 {
		t.Fatalf("rename: %+v", changes)
	}
	s.docs[path] = document{strings.Replace(src, "42", "missing", 1), 2}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if len(s.diagnostics[path]) == 0 || !s.state(path).stale {
		t.Fatal("broken edit did not publish errors")
	}
	b, _ = json.Marshal(query(t, s, path, "hover", 2, 10))
	if !bytes.Contains(b, []byte("Stale")) {
		t.Fatalf("stale hover: %s", b)
	}
	if _, err := s.feature("textDocument/rename", path, p); err == nil {
		t.Fatal("stale rename accepted")
	}
	s.docs[path] = document{src, 3}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if len(s.diagnostics[path]) != 0 || s.state(path).stale {
		t.Fatal("repair did not clear errors")
	}
	delete(s.docs, path)
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if len(s.packages) != 0 {
		t.Fatal("closed package retained")
	}
}
func TestReferencesUseIdentity(t *testing.T) {
	src := "fn one(value: Int): Int { value }\nfn two(value: Int): Int { value }\nfn main() {}\n"
	s, path := newTestServer(t, src)
	p := documentParams{Position: position{0, 26}}
	p.Context.IncludeDeclaration = true
	result, err := s.feature("textDocument/references", path, p)
	if err != nil || len(result.([]location)) != 2 {
		t.Fatalf("references: %+v %v", result, err)
	}
	for _, ref := range result.([]location) {
		if ref.Range.Start.Line != 0 {
			t.Fatal("same-spelled unrelated variable included")
		}
	}
}
func TestFunctionRenameIncludesDeclaration(t *testing.T) {
	s, path := newTestServer(t, "fn answer(): Int { 42 }\nfn main() uses io { println(answer()) }\n")
	p := documentParams{Position: position{1, 28}, NewName: "result"}
	p.Context.IncludeDeclaration = true
	refs, err := s.feature("textDocument/references", path, p)
	if err != nil || len(refs.([]location)) != 2 {
		t.Fatalf("function references: %+v %v", refs, err)
	}
}
func TestFormattingAndSymbols(t *testing.T) {
	s, path := newTestServer(t, "fn main() uses io {\nprintln(42)\n}\n")
	edits := query(t, s, path, "formatting", 0, 0).([]textEdit)
	if len(edits) != 1 || !strings.Contains(edits[0].NewText, "  println") {
		t.Fatalf("format edits: %+v", edits)
	}
	if len(query(t, s, path, "documentSymbol", 0, 0).([]any)) != 1 {
		t.Fatal("missing symbol")
	}
}
func TestUTF16Positions(t *testing.T) {
	src := "å😀x\r\ny\n"
	p := position{0, 3}
	pos, err := compilerPosition("x", src, p)
	if err != nil || pos.Col != 7 || lspPosition(src, pos) != p {
		t.Fatalf("conversion: %+v %v", pos, err)
	}
	if _, err := byteOffset(src, position{0, 2}); err == nil {
		t.Fatal("surrogate split accepted")
	}
	if _, err := byteOffset(src, position{3, 0}); err == nil {
		t.Fatal("out of range line accepted")
	}
	if endPosition(src) != (position{2, 0}) {
		t.Fatal("wrong end position")
	}
	path := filepath.Join(t.TempDir(), "a # å.bork")
	decoded, err := filePath(fileURI(path))
	if err != nil || decoded != path {
		t.Fatalf("URI roundtrip %s %v", decoded, err)
	}
}
func TestStdioLifecycle(t *testing.T) {
	var in, out bytes.Buffer
	for _, m := range []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}},
		{"jsonrpc": "2.0", "id": 2, "method": "shutdown"},
		{"jsonrpc": "2.0", "method": "exit"},
	} {
		if err := writeMessage(&in, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(&out)
	first, err := readMessage(reader)
	if err != nil {
		t.Fatal(err)
	}
	// readMessage accepts response envelopes; ensure framing contained only JSON.
	if string(first.ID) != "1" {
		t.Fatalf("initialize response: %+v", first)
	}
	second, err := readMessage(reader)
	if err != nil || string(second.ID) != "2" {
		t.Fatalf("shutdown response: %+v %v", second, err)
	}
	if _, err := readMessage(reader); err != io.EOF {
		t.Fatalf("unexpected stdout: %v", err)
	}
}
func TestFramingRejectsInvalidLength(t *testing.T) {
	for _, input := range []string{"Content-Length: -1\r\n\r\n", "Content-Length: 16777217\r\n\r\n", "\r\n", "bad\r\n\r\n"} {
		if _, err := readMessage(bufio.NewReader(strings.NewReader(input))); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}

func BenchmarkEditorDiagnostics(b *testing.B) {
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
			session := driver.NewSession()
			for i := 0; i < b.N; i++ {
				text := string(src) + strings.Repeat("\n", i%2)
				if _, err := session.Analyze(dir, map[string]string{path: text}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestInterpolationReferences(t *testing.T) {
	src := "fn main() uses io {\n  value = 42\n  println(s\"literal value: $value ${value + 1}\")\n}\n"
	s, path := newTestServer(t, src)
	p := documentParams{Position: position{1, 3}, NewName: "renamed"}
	p.Context.IncludeDeclaration = true
	result, err := s.feature("textDocument/references", path, p)
	if err != nil || len(result.([]location)) != 3 {
		t.Fatalf("interpolation references: %+v %v", result, err)
	}
}

func TestRenameRejectsIncompleteAndCollidingEdits(t *testing.T) {
	for _, tc := range []struct {
		src  string
		p    position
		name string
	}{
		{"fn Answer(): Int { 42 }\nfn main() uses io { println(Answer()) }\n", position{1, 28}, "Other"},
		{"fn main() uses io { x = 1; y = 2; println(x + y) }\n", position{0, 42}, "y"},
		{"type Item = { value: Int }\nfn use(item: Item): Int { item.value }\nfn main() {}\n", position{1, 31}, "other"},
	} {
		s, path := newTestServer(t, tc.src)
		p := documentParams{Position: tc.p, NewName: tc.name}
		if _, err := s.feature("textDocument/rename", path, p); err == nil {
			t.Fatalf("unsafe rename accepted for %s", tc.src)
		}
	}
}

func TestRenameRejectsSiblingCollision(t *testing.T) {
	s, path := newTestServer(t, "fn answer(): Int { 42 }\nfn main() uses io { println(answer()) }\n")
	sibling := filepath.Join(filepath.Dir(path), "other.bork")
	if err := os.WriteFile(sibling, []byte("fn result(): Int { 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	p := documentParams{Position: position{1, 28}, NewName: "result"}
	if _, err := s.feature("textDocument/rename", path, p); err == nil {
		t.Fatal("rename colliding with sibling declaration accepted")
	}
}

func TestRenameIncludesSignatureRequirements(t *testing.T) {
	src := "fn slice(from: Int, to: Int) where from <= to: Int { to - from }\nfn main() {}\n"
	s, path := newTestServer(t, src)
	p := documentParams{Position: position{0, 58}, NewName: "start"}
	result, err := s.feature("textDocument/rename", path, p)
	if err != nil {
		t.Fatal(err)
	}
	changes := result.(map[string]any)["changes"].(map[string][]textEdit)
	if len(changes[fileURI(path)]) != 3 {
		t.Fatalf("signature reference omitted: %+v", changes)
	}
	p.NewName = "_renamed"
	if _, err := s.feature("textDocument/rename", path, p); err == nil {
		t.Fatal("reserved identifier accepted")
	}
}

func TestRenameRejectsUnindexedWherePredicate(t *testing.T) {
	src := "pred positive(n: Int) { n > 0 }\nfn identity(n: Int where positive): Int where positive { n }\nfn main() {}\n"
	s, path := newTestServer(t, src)
	p := documentParams{Position: position{0, 6}, NewName: "aboveZero"}
	if _, err := s.feature("textDocument/rename", path, p); err == nil {
		t.Fatal("partial predicate rename accepted")
	}
}
