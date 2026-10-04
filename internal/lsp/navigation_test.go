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
)

func navigationFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	main := `import "example.com/nav/api"
fn helper(value: api.Box): api.Box { value }
fn main() uses io { greeting = "😀"; first = api.Make(); second = helper(first); println(second.Value); _ = api.Choose(api.Choice.Yes) }
`
	api := `type Box = { Value: Int }
type Choice = sealed { Yes, No }
class Render[T] { fn render(value: T): String }
instance IntRender: Render[Int] { fn render(value: Int): String { s"$value" } }
fn Make(): Box { Box { Value: 1 } }
fn Choose(value: Choice): Int { match (value) { Choice.Yes => 1, Choice.No => 2 } }
`
	files := map[string]string{"bork.mod": "module example.com/nav\n", "main.bork": main, "api/api.bork": api, "worker/worker.bork": "import \"example.com/nav/api\"\nfn Worker(): api.Box { api.Make() }\n"}
	for name, source := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, filepath.Join(dir, "main.bork"), main, api
}
func navigationPosition(t *testing.T, source, name string) position {
	t.Helper()
	offset := strings.Index(source, name)
	if offset < 0 {
		t.Fatalf("missing cursor %q", name)
	}
	prefix := source[:offset]
	return lspPosition(source, diag.Pos{Line: strings.Count(prefix, "\n") + 1, Col: offset - strings.LastIndexByte(prefix, '\n')})
}
func navigationMessage(id int, method, path string, pos position) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path)}, "position": pos}}
}
func navigationResponses(t *testing.T, messages []map[string]any) map[int]json.RawMessage {
	t.Helper()
	var in, out bytes.Buffer
	messages = append(messages, map[string]any{"jsonrpc": "2.0", "id": 99, "method": "shutdown"}, map[string]any{"jsonrpc": "2.0", "method": "exit"})
	for _, m := range messages {
		if err := writeMessage(&in, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	results := map[int]json.RawMessage{}
	r := bufio.NewReader(&out)
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
				Diagnostics []struct {
					Severity int
					Message  string
				}
			}
		}
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		if response.Error != nil {
			t.Fatalf("request %d: %+v", response.ID, response.Error)
		}
		if response.Method == "textDocument/publishDiagnostics" {
			for _, d := range response.Params.Diagnostics {
				if d.Severity == 1 {
					t.Fatalf("invalid navigation fixture: %s", d.Message)
				}
			}
		}
		if response.ID != 0 {
			results[response.ID] = response.Result
		}
	}
	return results
}
func TestNavigationTypeImplementationsSymbolsAndHighlightsProtocol(t *testing.T) {
	dir, path, main, api := navigationFixture(t)
	apiPath := filepath.Join(dir, "api/api.bork")
	messages := []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"rootUri": fileURI(dir)}},
		{"jsonrpc": "2.0", "id": 8, "method": "workspace/symbol", "params": map[string]any{"query": "Worker"}},
		{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path), "version": 1, "text": main}}},
		{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(apiPath), "version": 1, "text": api}}},
		navigationMessage(2, "textDocument/typeDefinition", path, navigationPosition(t, main, "first =")),
		navigationMessage(3, "textDocument/implementation", apiPath, navigationPosition(t, api, "Choice =")),
		navigationMessage(4, "textDocument/implementation", apiPath, navigationPosition(t, api, "Render[T]")),
		navigationMessage(5, "textDocument/documentHighlight", path, navigationPosition(t, main, "first =")),
		navigationMessage(6, "textDocument/typeDefinition", path, navigationPosition(t, main, "api.Box")),
		navigationMessage(7, "textDocument/prepareCallHierarchy", path, navigationPosition(t, main, "helper(first)")),
	}
	results := navigationResponses(t, messages)
	for _, id := range []int{2, 6} {
		var locations []location
		if err := json.Unmarshal(results[id], &locations); err != nil {
			t.Fatal(err)
		}
		if len(locations) != 1 || locations[0].URI != fileURI(apiPath) || locations[0].Range.Start != navigationPosition(t, api, "Box =") {
			t.Fatalf("type definition %d: %s", id, results[id])
		}
	}
	var variants []location
	if err := json.Unmarshal(results[3], &variants); err != nil {
		t.Fatal(err)
	}
	if len(variants) != 2 {
		t.Fatalf("variants: %s", results[3])
	}
	var instances []location
	if err := json.Unmarshal(results[4], &instances); err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Range.Start != navigationPosition(t, api, "IntRender:") {
		t.Fatalf("class instances: %s", results[4])
	}
	var highlights []struct {
		Range sourceRange
		Kind  int
	}
	if err := json.Unmarshal(results[5], &highlights); err != nil {
		t.Fatal(err)
	}
	if len(highlights) != 2 || highlights[0].Kind != 3 || highlights[1].Kind != 2 {
		t.Fatalf("highlights: %s", results[5])
	}
	if highlights[0].Range.Start != navigationPosition(t, main, "first =") {
		t.Fatalf("UTF16 highlight: %+v", highlights)
	}
	if !bytes.Contains(results[8], []byte(`"Worker"`)) {
		t.Fatalf("unopened symbol: %s", results[8])
	}
	var items []hierarchyItem
	if err := json.Unmarshal(results[7], &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "helper" {
		t.Fatalf("hierarchy prepare: %s", results[7])
	}
}

func TestNavigationCallHierarchyClosedIncomingProtocol(t *testing.T) {
	dir, path, main, api := navigationFixture(t)
	apiPath := filepath.Join(dir, "api/api.bork")
	makePos := navigationPosition(t, api, "Make():")
	mainPos := navigationPosition(t, main, "main()")
	results := navigationResponses(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"rootUri": fileURI(dir)}},
		{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path), "version": 1, "text": main}}},
		{"jsonrpc": "2.0", "id": 2, "method": "callHierarchy/incomingCalls", "params": map[string]any{"item": hierarchyItem{URI: fileURI(apiPath), SelectionRange: sourceRange{makePos, makePos}}}},
		{"jsonrpc": "2.0", "id": 3, "method": "callHierarchy/outgoingCalls", "params": map[string]any{"item": hierarchyItem{URI: fileURI(path), SelectionRange: sourceRange{mainPos, mainPos}}}},
	})
	var incoming []struct {
		From       hierarchyItem
		FromRanges []sourceRange
	}
	if err := json.Unmarshal(results[2], &incoming); err != nil {
		t.Fatal(err)
	}
	if len(incoming) != 2 {
		t.Fatalf("closed callers: %s", results[2])
	}
	for _, call := range incoming {
		if len(call.FromRanges) != 1 {
			t.Fatalf("call site: %+v", call)
		}
		if call.From.Name != "main" && call.From.Name != "Worker" {
			t.Fatalf("unexpected caller: %+v", call)
		}
	}
	if !bytes.Contains(results[3], []byte(`"Make"`)) || !bytes.Contains(results[3], []byte(`"helper"`)) || !bytes.Contains(results[3], []byte(`"Choose"`)) {
		t.Fatalf("outgoing calls: %s", results[3])
	}
}

func TestNavigationTests(t *testing.T) {
	dir, path, main, api := navigationFixture(t)
	apiPath := filepath.Join(dir, "api/api.bork")
	main += "test \"make works\" { assert(api.Make().Value == 1) }\n"
	if err := os.WriteFile(path, []byte(main), 0600); err != nil {
		t.Fatal(err)
	}
	makePos := navigationPosition(t, api, "Make():")
	testPos := navigationPosition(t, main, "test ")
	results := navigationResponses(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"rootUri": fileURI(dir)}},
		{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path), "version": 1, "text": main}}},
		{"jsonrpc": "2.0", "id": 2, "method": "callHierarchy/incomingCalls", "params": map[string]any{"item": hierarchyItem{URI: fileURI(apiPath), SelectionRange: sourceRange{makePos, makePos}}}},
		{"jsonrpc": "2.0", "id": 3, "method": "callHierarchy/outgoingCalls", "params": map[string]any{"item": hierarchyItem{URI: fileURI(path), SelectionRange: sourceRange{testPos, testPos}}}},
	})

	if !bytes.Contains(results[3], []byte(`"Make"`)) {
		t.Fatal("test caller cannot be expanded")
	}
}
func TestNavigationConcreteClass(t *testing.T) {
	dir, path, main, api := navigationFixture(t)
	apiPath := filepath.Join(dir, "api/api.bork")
	api += "fn RenderOne(): String { render(1) }\n"
	if err := os.WriteFile(apiPath, []byte(api), 0600); err != nil {
		t.Fatal(err)
	}
	decl := navigationPosition(t, api, "render(value: T)")
	concrete := navigationPosition(t, api, "render(value: Int)")
	fn := navigationPosition(t, api, "RenderOne():")
	results := navigationResponses(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"rootUri": fileURI(dir)}},
		{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path), "version": 1, "text": main}}},
		{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(apiPath), "version": 1, "text": api}}},
		navigationMessage(2, "textDocument/implementation", apiPath, decl),
		navigationMessage(5, "textDocument/prepareCallHierarchy", apiPath, navigationPosition(t, api, "render(1)")),
		{"jsonrpc": "2.0", "id": 3, "method": "callHierarchy/outgoingCalls", "params": map[string]any{"item": hierarchyItem{URI: fileURI(apiPath), SelectionRange: sourceRange{fn, fn}}}},
		{"jsonrpc": "2.0", "id": 4, "method": "callHierarchy/incomingCalls", "params": map[string]any{"item": hierarchyItem{URI: fileURI(apiPath), SelectionRange: sourceRange{concrete, concrete}}}},
	})

	var prepared []hierarchyItem
	if err := json.Unmarshal(results[5], &prepared); err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 1 || prepared[0].SelectionRange.Start != concrete {
		t.Errorf("concrete call prepare returned abstract method: %s", results[5])
	}
	var loc []location
	if err := json.Unmarshal(results[2], &loc); err != nil {
		t.Fatal(err)
	}
	if len(loc) != 1 || loc[0].Range.Start != concrete {
		t.Fatal("class method implementations failed")
	}
	if !bytes.Contains(results[4], []byte(`"RenderOne"`)) {
		t.Fatal("concrete incoming failed")
	}
}

func TestNavigationMultiRoot(t *testing.T) {
	dir, _, _, _ := navigationFixture(t)
	empty := t.TempDir()
	results := navigationResponses(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"workspaceFolders": []map[string]any{{"uri": fileURI(empty), "name": "tools"}, {"uri": fileURI(dir), "name": "bork"}}}},
		{"jsonrpc": "2.0", "id": 2, "method": "workspace/symbol", "params": map[string]any{"query": "Worker"}},
	})
	if !bytes.Contains(results[2], []byte(`"Worker"`)) {
		t.Fatalf("second workspace root lost: %s", results[2])
	}
}
