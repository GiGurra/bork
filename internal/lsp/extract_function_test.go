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
	borkformat "github.com/GiGurra/bork/internal/format"
)

type protocolExtractionAction struct {
	Title string
	Kind  string
	Edit  struct{ Changes map[string][]textEdit }
}

func BenchmarkExtractFunctionHTTPServer(b *testing.B) {
	path, err := filepath.Abs("../../examples/http_server/main.bork")
	if err != nil {
		b.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	src := string(raw)
	s := &server{out: &bytes.Buffer{}, initialized: true, docs: map[string]document{path: {src, 1}}, packages: map[string]*packageState{}, diagnostics: map[string][]diag.Diagnostic{}}
	if err := s.check(); err != nil {
		b.Fatal(err)
	}
	selected := `s"no todo $id"`
	offset := strings.Index(src, selected)
	if offset < 0 {
		b.Fatal("missing benchmark selection")
	}
	pos := func(offset int) position {
		prefix := src[:offset]
		return lspPosition(src, diag.Pos{File: path, Line: strings.Count(prefix, "\n") + 1, Col: len(prefix) - strings.LastIndexByte(prefix, '\n')})
	}
	p := documentParams{Range: sourceRange{pos(offset), pos(offset + len(selected))}}
	p.Context.Only = []string{"refactor.extract"}
	if s.extractFunction(path, p) == nil {
		b.Fatal("missing initial extraction action")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if s.extractFunction(path, p) == nil {
			b.Fatal("missing extraction action")
		}
	}
}

func protocolExtractionActions(t *testing.T, path, source string, selection sourceRange, only []string, requireValid bool) []protocolExtractionAction {
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
	originalChecked := false
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
			Method string
			Params struct {
				URI         string
				Diagnostics []struct {
					Severity int
					Message  string
				}
			}
		}
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		if requireValid && response.Method == "textDocument/publishDiagnostics" {
			if response.Params.URI == fileURI(path) {
				originalChecked = true
			}
			for _, diagnostic := range response.Params.Diagnostics {
				if diagnostic.Severity == 1 {
					t.Fatalf("invalid original extraction fixture: %s", diagnostic.Message)
				}
			}
		}
		if response.ID == 2 {
			if requireValid && !originalChecked {
				t.Fatal("original source was not checked before extraction")
			}
			if response.Error != nil {
				t.Fatalf("code action error: %+v", response.Error)
			}
			var actions []protocolExtractionAction
			if err := json.Unmarshal(response.Result, &actions); err != nil {
				t.Fatal(err)
			}
			return actions
		}
	}
	t.Fatal("missing code action response")
	return nil
}

func TestExtractFunctionProtocol(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"parameters", "fn sum(x: Int, y: Int): Int { [|x + y|] }\nfn main() {}\n", "fn extracted(x: Int, y: Int): Int"},
		{"effects", "fn greet(name: String) uses io { [|println(name)|] }\nfn main() {}\n", "fn extracted(name: String) uses io: Ok"},
		{"folded capture", "fn answer(): Int { x = 1; [|x + 2|] }\nfn main() {}\n", "fn extracted(x: Int): Int"},
		{"grouping", "fn sum(x: Int, y: Int): Int { [|(x + y)|] }\nfn main() {}\n", "extracted(x, y)"},
		{"subexpression", "fn sum(x: Int, y: Int): Int { [|x + 1|] + y }\nfn main() {}\n", "extracted(x) + y"},
		{"block", "fn answer(x: Int): Int { [|{ local = x + 1; local * 2 }|] }\nfn main() {}\n", "fn extracted(x: Int): Int"},
		{"statements", "fn print(x: Int) uses io { [|local = x + 1; println(local)|]; println(x) }\nfn main() {}\n", "fn extracted(x: Int) uses io: Ok"},
		{"pattern capture", "fn choose(x: Int | String): Int { match (x) { value: Int => [|value + 1|], text: String => text.byteLength() } }\nfn main() {}\n", "fn extracted(value: Int): Int"},
		{"name collision", "fn extracted(): Int { 1 }\nfn sum(x: Int): Int { [|x + 1|] }\nfn main() {}\n", "fn extracted2(x: Int): Int"},
		{"unicode", "fn sum(x: Int): Int { text = \"🐶\"; [|x + text.byteLength()|] }\nfn main() {}\n", "fn extracted(x: Int, text: String): Int"},
		{"lambda", "fn add(x: Int): (Int) => Int { [|v => v + x|] }\nfn main() {}\n", "fn extracted(x: Int): (Int) => Int"},
		{"constraint", "pred positive(x: Int) { x > 0 }\nfn divide(value: Int, divisor: Int where positive): Int { [|value / divisor|] }\nfn main() {}\n", "divisor: Int where positive"},
		{"generic", "fn identity[T](value: T): T { [|value|] }\nfn main() {}\n", "fn extracted[T](value: T): T"},
		{"generic bound", "fn equal[T: Eq](a: T, b: T): Bool { [|a == b|] }\nfn main() {}\n", "fn extracted[T: Eq](a: T, b: T): Bool"},
		{"interpolation", "fn say(x: Int): String { s\"🐶 ${[|x + 1|]}\" }\nfn main() {}\n", "fn extracted(x: Int): Int"},
		{"alias", "import \"example.com/extract/api\"\nfn echo(value: api.Item): api.Item { [|value|] }\nfn main() {}\n", "fn extracted(value: api.Item): api.Item"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := parallelProtocolFixture(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "main.bork")
			if tc.name == "alias" {
				for name, content := range map[string]string{"bork.mod": "module example.com/extract\n", "model/model.bork": "type Item = { Value: Int }\n", "api/api.bork": "import \"example.com/extract/model\"\ntype Item = model.Item\n"} {
					file := filepath.Join(dir, name)
					if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			src, start, end := extractionSelection(t, path, tc.source)
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			actions := protocolExtractionActions(t, path, src, sourceRange{start, end}, []string{"refactor.extract"}, true)
			if len(actions) != 1 || actions[0].Kind != "refactor.extract" {
				t.Fatalf("actions: %+v", actions)
			}
			edits := actions[0].Edit.Changes[fileURI(path)]
			if len(edits) != 1 || !strings.Contains(edits[0].NewText, tc.want) {
				t.Fatalf("edit: %+v", edits)
			}
			result := edits[0].NewText
			formatted, err := borkformat.Source(path, []byte(result))
			if err != nil || string(formatted) != result {
				t.Fatalf("not formatted: %v\n%s", err, result)
			}
			if _, err := session.Analyze(dir, map[string]string{path: result}); err != nil {
				t.Fatalf("does not check: %v\n%s", err, result)
			}
		})
	}
}

func TestExtractFunctionRejectsUnsupportedSelections(t *testing.T) {
	for i, source := range []string{
		`fn value() uses io: Int { println("real"); 1 }
 test "example" { mock value() { [|value() + 1|] }; assertEqual(value(), 2) }
 fn main() {}`,
		`fn main() uses state + io { scope s { async(s) x: Int = panic("unused"); println([|if (false) { x } else { 1 }|]) } }`,
		`fn main() uses state + io { scope outer { cancel(outer); [|scope inner { println(cancelled(inner)) }|] } }`,
		`fn main() uses io { lazy x: Int = panic("unused"); println([|if (false) { x } else { 1 }|]) }`,
		"fn sum(x: Int): Int { [|x +|] 1 }\nfn main() {}\n",
		"fn sum(x: Int): Int { [|local = x + 1|]; local }\nfn main() {}\n",
		"fn sum(x: Int): Int { [|missing + x|] }\nfn main() {}\n",
		"fn choose(value: Int | String, flag: Bool) uses io: Int | String { result: Int | String = [|if (flag) { value? } else { \"fallback\" }|]; println(\"after\"); result }\nfn main() {}\n",
	} {
		t.Run(fmt.Sprintf("selection-%d", i), func(t *testing.T) {
			parallelProtocolFixture(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "main.bork")
			src, start, end := extractionSelection(t, path, source)
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			if actions := protocolExtractionActions(t, path, src, sourceRange{start, end}, []string{"refactor.extract"}, !strings.Contains(src, "missing")); len(actions) != 0 {
				t.Fatalf("unsupported selection: %+v", actions)
			}
		})
	}
}

func extractionSelection(t *testing.T, path, marked string) (string, position, position) {
	t.Helper()
	lo := strings.Index(marked, "[|")
	hi := strings.Index(marked, "|]")
	if lo < 0 || hi < lo {
		t.Fatal("invalid selection fixture")
	}
	src := marked[:lo] + marked[lo+2:hi] + marked[hi+2:]
	pos := func(offset int) position {
		prefix := src[:offset]
		line := strings.Count(prefix, "\n")
		col := len(prefix) - strings.LastIndexByte(prefix, '\n')
		return lspPosition(src, diag.Pos{File: path, Line: line + 1, Col: col})
	}
	return src, pos(lo), pos(hi - 2)
}
