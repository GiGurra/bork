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

// Each fixture owns its files and server. Bound parallel compiler work to two
// fixtures, retaining full protocol, formatting and semantic checks per case.
var protocolFixtureSessions = func() chan *driver.Session {
	sessions := make(chan *driver.Session, 2)
	sessions <- driver.NewSession()
	sessions <- driver.NewSession()
	return sessions
}()

func parallelProtocolFixture(t *testing.T) *driver.Session {
	t.Helper()
	t.Parallel()
	session := <-protocolFixtureSessions
	t.Cleanup(func() { protocolFixtureSessions <- session })
	return session
}

func TestDiagnosticActionsCheckAndFormat(t *testing.T) {
	for _, tc := range []struct{ name, source, title, want string }{
		{"boolean", "fn choose(x: Bool): Int { match (x) { true => 1 } }\nfn main() {}\n", "Add missing match arms", "false => todo()"},
		{"empty", "fn choose(x: Bool): Int { match (x) {} }\nfn main() {}\n", "Add missing match arms", "false => todo()"},
		{"union", "fn choose(x: Int | String): Int { match (x) { n: Int => n } }\nfn main() {}\n", "Add missing match arms", "missingValue: String => todo()"},
		{"binding collision", "fn choose(missingValue: Int | String): Int { match (missingValue) { n: Int => n } }\nfn main() {}\n", "Add missing match arms", "missingValue2: String => todo()"},
		{"function collision", "fn missingValue() {}\nfn choose(x: Int | String): Int { match (x) { n: Int => n } }\nfn main() {}\n", "Add missing match arms", "missingValue2: String => todo()"},
		{"interpolation", "fn choose(x: Bool): String { s\"${match (x) { true => 1 }}\" }\nfn main() {}\n", "Add missing match arms", "false => todo()"},
		{"payload", "type Choice = sealed { Yes { value: Bool }, No }\nfn choose(x: Choice): Int { match (x) { Choice.No => 1 } }\nfn main() {}\n", "Add missing match arms", "Choice.Yes { value: _ } => todo()"},
		{"partial payload", "type Choice = sealed { Yes { value: Bool }, No }\nfn choose(x: Choice): Int { match (x) { Choice.Yes { value: true } => 1, Choice.No => 2 } }\nfn main() {}\n", "Add missing match arms", "value: _"},
		{"infinite", "fn choose(x: Int): Int { match (x) { 1 => 1 } }\nfn main() {}\n", "Add missing match arms", "_ => todo()"},
		{"list", "fn choose(x: List[Int]): Int { match (x) { [] => 0 } }\nfn main() {}\n", "Add missing match arms", "[_, ...] => todo()"},
		{"effect", "fn print() { println(1) }\nfn main() {}\n", "declare uses io", "fn print() uses io"},
		{"transitive alias", "import \"example.com/review/api\"\nfn choose(x: api.Choice): Int { match (x) { api.Choice.No => 1 } }\nfn main() {}\n", "Add missing match arms", "_ => todo()"},
		{"transitive sequence alias", "import \"example.com/review/api\"\nfn choose(x: Int | api.Items): Int { match (x) { n: Int => n } }\nfn main() {}\n", "Add missing match arms", "_ => todo()"},
		{"private variant", "import \"example.com/review/model\"\nfn choose(x: model.Hidden): Int { match (x) { model.Hidden.Public => 1 } }\nfn main() {}\n", "Add missing match arms", "_ => todo()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := parallelProtocolFixture(t)
			dir := t.TempDir()
			if strings.HasPrefix(tc.name, "transitive") || tc.name == "private variant" {
				files := map[string]string{
					"bork.mod":         "module example.com/review\n",
					"model/model.bork": "type Choice[T] = sealed { Yes { Value: T }, No }\ntype Hidden = sealed { Public, hidden }\ntype Item = { Value: Int }\n",
					"api/api.bork":     "import \"example.com/review/model\"\ntype Choice = model.Choice[Int]\ntype Items = Seq[model.Item]\n",
				}
				for name, source := range files {
					file := filepath.Join(dir, name)
					if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(file, []byte(source), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
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
			if _, err := session.Analyze(dir, map[string]string{path: changed}); err != nil {
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
