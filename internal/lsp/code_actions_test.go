package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/driver"
	borkformat "github.com/GiGurra/bork/internal/format"
)

func TestDiagnosticActionsCheckAndFormat(t *testing.T) {
	for _, tc := range []struct{ name, source, title, want string }{
		{"boolean", "fn choose(x: Bool): Int { match (x) { true => 1 } }\nfn main() {}\n", "Add missing match arms", "false => todo()"},
		{"empty", "fn choose(x: Bool): Int { match (x) {} }\nfn main() {}\n", "Add missing match arms", "false => todo()"},
		{"union", "fn choose(x: Int | String): Int { match (x) { n: Int => n } }\nfn main() {}\n", "Add missing match arms", "value: String => todo()"},
		{"payload", "type Choice = sealed { Yes { value: Bool }, No }\nfn choose(x: Choice): Int { match (x) { Choice.No => 1 } }\nfn main() {}\n", "Add missing match arms", "Choice.Yes { value: _ } => todo()"},
		{"partial payload", "type Choice = sealed { Yes { value: Bool }, No }\nfn choose(x: Choice): Int { match (x) { Choice.Yes { value: true } => 1, Choice.No => 2 } }\nfn main() {}\n", "Add missing match arms", "value: _"},
		{"infinite", "fn choose(x: Int): Int { match (x) { 1 => 1 } }\nfn main() {}\n", "Add missing match arms", "_ => todo()"},
		{"list", "fn choose(x: List[Int]): Int { match (x) { [] => 0 } }\nfn main() {}\n", "Add missing match arms", "[_, ...] => todo()"},
		{"effect", "fn print() { println(1) }\nfn main() {}\n", "declare uses io", "fn print() uses io"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "main.bork")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			input := &bytes.Buffer{}
			messages := []any{
				map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}},
				map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path), "version": 1, "text": tc.source}}},
				map[string]any{"jsonrpc": "2.0", "id": 2, "method": "textDocument/codeAction", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path)}, "range": sourceRange{position{}, endPosition(tc.source)}, "context": map[string]any{"diagnostics": []any{}, "only": []string{"quickfix"}}}},
				map[string]any{"jsonrpc": "2.0", "id": 3, "method": "shutdown"},
				map[string]any{"jsonrpc": "2.0", "method": "exit"},
			}
			for _, msg := range messages {
				b, _ := json.Marshal(msg)
				fmt.Fprintf(input, "Content-Length: %d\r\n\r\n%s", len(b), b)
			}
			out := &bytes.Buffer{}
			if err := Serve(input, out); err != nil {
				t.Fatal(err)
			}
			var changed string
			for _, part := range strings.Split(out.String(), "Content-Length: ")[1:] {
				_, body, _ := strings.Cut(part, "\r\n\r\n")
				var response struct {
					ID     int
					Result []struct {
						Title string
						Edit  struct{ Changes map[string][]textEdit }
					}
				}
				if json.Unmarshal([]byte(body), &response) != nil || response.ID != 2 {
					continue
				}
				for _, action := range response.Result {
					if action.Title == tc.title {
						changed = action.Edit.Changes[fileURI(path)][0].NewText
					}
				}
			}
			if !strings.Contains(changed, tc.want) {
				t.Fatalf("missing action %q: %s", tc.title, out)
			}
			formatted, err := borkformat.Source(path, []byte(changed))
			if err != nil || string(formatted) != changed {
				t.Fatalf("action is not formatted: %s (%v)", changed, err)
			}
			if _, err := driver.NewSession().Analyze(dir, map[string]string{path: changed}); err != nil {
				t.Fatalf("action does not check: %v\n%s", err, changed)
			}
		})
	}
}

func BenchmarkDiagnosticActionHTTPServer(b *testing.B) {
	path, err := filepath.Abs("../../examples/http_server/main.bork")
	if err != nil {
		b.Fatal(err)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	src := string(source) + "\nfn diagnosticPrint() { println(1) }\n"
	s := &server{out: &bytes.Buffer{}, initialized: true, docs: map[string]document{path: {src, 1}}, packages: map[string]*packageState{}, diagnostics: map[string][]diag.Diagnostic{}}
	if err := s.check(); err != nil {
		b.Fatal(err)
	}
	p := documentParams{Range: sourceRange{position{}, endPosition(src)}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(s.codeActions(path, p)) == 0 {
			b.Fatal("missing effect action")
		}
	}
}
