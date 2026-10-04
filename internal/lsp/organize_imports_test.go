package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/driver"
	borkformat "github.com/GiGurra/bork/internal/format"
)

type protocolAction struct {
	Title string
	Kind  string
	Edit  struct{ Changes map[string][]textEdit }
}

func BenchmarkOrganizeImportsHTTPServer(b *testing.B) {
	path, err := filepath.Abs("../../examples/http_server/main.bork")
	if err != nil {
		b.Fatal(err)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	s := &server{out: &bytes.Buffer{}, initialized: true, docs: map[string]document{path: {string(src), 1}}, packages: map[string]*packageState{}, diagnostics: map[string][]diag.Diagnostic{}}
	if err := s.check(); err != nil {
		b.Fatal(err)
	}
	p := documentParams{}
	p.Context.Only = []string{"source.organizeImports"}
	if s.organizeImports(path, p) == nil {
		b.Fatal("missing initial organize imports action")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if s.organizeImports(path, p) == nil {
			b.Fatal("missing organize imports action")
		}
	}
}

func protocolActions(t *testing.T, path, source string, selection sourceRange, only []string) []protocolAction {
	t.Helper()
	var in, out bytes.Buffer
	for _, m := range []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}},
		{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path), "version": 1, "text": source}}},
		{"jsonrpc": "2.0", "id": 2, "method": "textDocument/codeAction", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path)}, "range": selection, "context": map[string]any{"diagnostics": []any{}, "only": only}}},
		{"jsonrpc": "2.0", "id": 3, "method": "shutdown"},
		{"jsonrpc": "2.0", "method": "exit"},
	} {
		if err := writeMessage(&in, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(bytes.NewReader(out.Bytes()))
	for {
		line, err := r.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if _, err := fmt.Sscanf(line, "Content-Length: %d", &n); err != nil {
			t.Fatal(err)
		}
		if _, err := r.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			t.Fatal(err)
		}
		var response struct {
			ID     int
			Result json.RawMessage
			Error  any
		}
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		if response.ID == 2 {
			if response.Error != nil {
				t.Fatalf("code action error: %+v", response.Error)
			}
			var actions []protocolAction
			if err := json.Unmarshal(response.Result, &actions); err != nil {
				t.Fatal(err)
			}
			return actions
		}
	}
	t.Fatal("missing code action response")
	return nil
}

func TestOrganizeImportsProtocol(t *testing.T) {
	dir := t.TempDir()
	for name, src := range map[string]string{
		"bork.mod": "module example.com/organize\n",
		"a/a.bork": "fn A(): Int { 1 }\n",
		"b/b.bork": "fn B(): Int { 2 }\n",
		"c/c.bork": "fn C(): Int { 3 }\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "main.bork")
	src := "// Header 🐶\nimport beta \"example.com/organize/b\" /* B comment\n   spanning lines */\n// A comment\nimport \"example.com/organize/a\"\nimport \"example.com/organize/c\" // unused comment\nfn main() uses io { println(a.A() + beta.B()) }\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, only := range [][]string{{"source.organizeImports"}, {"source"}} {
		actions := protocolActions(t, path, src, sourceRange{}, only)
		if len(actions) != 1 || actions[0].Kind != "source.organizeImports" {
			t.Fatalf("actions: %+v", actions)
		}
		edits := actions[0].Edit.Changes[fileURI(path)]
		if len(edits) != 1 {
			t.Fatalf("edits: %+v", edits)
		}
		result := edits[0].NewText
		if strings.Contains(result, "import \"example.com/organize/c\"") || strings.Index(result, "/a\"") > strings.Index(result, "/b\"") {
			t.Fatalf("imports not organized: %s", result)
		}
		for _, comment := range []string{"// Header 🐶", "// A comment", "/* B comment\n   spanning lines */", "// unused comment"} {
			if !strings.Contains(result, comment) {
				t.Fatalf("lost comment: %s", result)
			}
		}
		formatted, err := borkformat.Source(path, []byte(result))
		if err != nil || string(formatted) != result {
			t.Fatalf("not formatted: %v\n%s", err, result)
		}
		if _, err := driver.NewSession().Analyze(dir, map[string]string{path: result}); err != nil {
			t.Fatalf("does not check: %v\n%s", err, result)
		}
		if next := protocolActions(t, path, result, sourceRange{}, only); len(next) != 0 {
			t.Fatalf("not idempotent: %+v", next)
		}
	}
	if actions := protocolActions(t, path, src, sourceRange{}, []string{"refactor.extract"}); len(actions) != 0 {
		t.Fatalf("kind filter ignored: %+v", actions)
	}
	inline := strings.Replace(src, "/a\"\nimport", "/a\"; import", 1)
	actions := protocolActions(t, path, inline, sourceRange{}, []string{"source.organizeImports"})
	if len(actions) != 1 {
		t.Fatalf("semicolon imports: %+v", actions)
	}
	result := actions[0].Edit.Changes[fileURI(path)][0].NewText
	if _, err := driver.NewSession().Analyze(dir, map[string]string{path: result}); err != nil {
		t.Fatalf("semicolon import action does not check: %v\n%s", err, result)
	}
	broken := src + "fn broken() { missing }\n"
	if actions := protocolActions(t, path, broken, sourceRange{}, []string{"source.organizeImports"}); len(actions) != 0 {
		t.Fatalf("offered action while other errors remain: %+v", actions)
	}
}
