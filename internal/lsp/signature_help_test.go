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

	"github.com/GiGurra/bork/internal/driver"
)

func signatureAt(t *testing.T, s *server, path, marked string) *signatureHelpResult {
	t.Helper()
	offset := -1
	for i := 0; i < len(marked); i++ {
		if marked[i] == '|' && (i+1 == len(marked) || marked[i+1] != '>') {
			offset = i
			break
		}
	}
	if offset < 0 {
		t.Fatal("missing cursor")
	}
	src := marked[:offset] + marked[offset+1:]
	s.docs[path] = document{src, 2}
	result, err := s.signatureHelp(path, src, lspPosition(src, offsetPos(src, offset)))
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestSignatureHelpCallsMethodsGenericsAndNamedArguments(t *testing.T) {
	src := `// Adds a value to the left side.
fn add(left: Int, right: Int = 1): Int { left + right }
fn identity[T](value: T): T { value }
fn (items: List[T]) choose[T](index: Int): List[T] { items }
fn main() uses io { println(add(1, 2)); println(identity[Int](42)); println([1].choose(0)) }
`
	s, path := newTestServer(t, src)
	for _, tc := range []struct {
		name, current, label string
		active               int
	}{
		{"first", "fn main() { add(|)", "add(left: Int, right: Int = 1): Int", 0},
		{"second", "fn main() { add(1, |)", "add(left: Int, right: Int = 1): Int", 1},
		{"named reverse", "fn main() { add(right: |)", "add(left: Int, right: Int = 1): Int", 1},
		{"remaining named", "fn main() { add(right: 1, |)", "add(left: Int, right: Int = 1): Int", 0},
		{"generic", "fn main() { identity[String](|)", "identity(value: String): String", 0},
		{"method", "fn main() uses io { items = [1]; items.choose(|)", "choose(index: Int): List[Int]", 0},
		{"nested", "fn main() { add(identity[Int](42), |)", "add(left: Int, right: Int = 1): Int", 1},
		{"innermost", "fn main() { add(1, identity[String](|))", "identity(value: String): String", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Keep checked declarations and scope; incomplete edits change only main.
			current := src[:strings.Index(src, "fn main")] + tc.current
			if tc.name == "method" {
				initial := src[:strings.Index(src, "fn main")] + "fn main() uses io { items = [1]; println(items.choose(0)) }\n"
				s, path = newTestServer(t, initial)
			}
			help := signatureAt(t, s, path, current)
			if help == nil || len(help.Signatures) != 1 {
				t.Fatalf("missing help: %+v", help)
			}
			sig := help.Signatures[0]
			if sig.Label != tc.label || help.ActiveParameter == nil || *help.ActiveParameter != tc.active {
				t.Fatalf("help: %+v active %v", sig, help.ActiveParameter)
			}
			if tc.name == "first" && (sig.Documentation == nil || !strings.Contains(sig.Documentation.Value, "Adds a value")) {
				t.Fatalf("docs: %+v", sig)
			}
		})
	}
}
func TestSignatureHelpCheckedInstantiationAndFunctionValues(t *testing.T) {
	src := `fn identity[T](value: T): T { value }
fn run(f: (String, Int) => String): String { f("x", 1) }
fn main() uses io { println(identity(42)) }
`
	s, path := newTestServer(t, src)
	help := signatureAt(t, s, path, strings.Replace(src, "identity(42)", "identity(|42)", 1))
	if help == nil || help.Signatures[0].Label != "identity(value: Int): Int" {
		t.Fatalf("checked generic: %+v", help)
	}
	help = signatureAt(t, s, path, strings.Replace(src, "f(\"x\", 1)", "f(\"x\", |1)", 1))
	if help == nil || help.Signatures[0].Label != "f(String, Int): String uses open" || help.ActiveParameter == nil || *help.ActiveParameter != 1 {
		t.Fatalf("function value: %+v", help)
	}
	for _, current := range []string{
		"fn main() { identity(42)| }\n",
		"fn identity[T](|value: T): T { value }\n",
		"fn main() { unknown(|) }\n",
	} {
		if help := signatureAt(t, s, path, current); help != nil {
			t.Fatalf("unexpected help for %s: %+v", current, help)
		}
	}
}
func TestSignatureHelpPipesAndExpressionReceivers(t *testing.T) {
	src := `type Box = { value: Int }
fn (box: Box) plus(amount: Int): Int { box.value + amount }
fn (items: List[T]) choose[T](index: Int): List[T] { items }
fn add(left: Int, right: Int): Int { left + right }
fn main() uses io { println(Box.plus(Box { value: 1 }, 2)); println([1].choose(0)); println(List.choose([1],0)); println(1 |> add(2)) }
`
	s, path := newTestServer(t, src)
	for _, tc := range []struct {
		before, after, label string
		active               int
	}{
		{"Box.plus(Box { value: 1 }, 2)", "Box.plus(Box { value: 1 }, |)", "plus(box: Box, amount: Int): Int", 1},
		{"[1].choose(0)", "[1].choose(|)", "choose(index: Int): List[Int]", 0},
		{"List.choose([1],0)", "List.choose([1],|)", "choose(items: List[Int], index: Int): List[Int]", 1},
		{"1 |> add(2)", "1 |> add(|2)", "add(left: Int, right: Int): Int", 1},
	} {
		help := signatureAt(t, s, path, strings.Replace(src, tc.before, tc.after, 1))
		if help == nil || help.Signatures[0].Label != tc.label || help.ActiveParameter == nil || *help.ActiveParameter != tc.active {
			t.Fatalf("%s: %+v", tc.after, help)
		}
	}
}

func TestSignatureHelpRecoveryOfNewUnboundAndGenericCalls(t *testing.T) {
	src := `type Box = { value: Int }
fn (box: Box) plus(amount: Int): Int { box.value + amount }
fn identity[T](value: T): T { value }
fn generic[T](value: T): T { identity[T](value) }
fn main() {}
`
	s, path := newTestServer(t, src)
	help := signatureAt(t, s, path, src+"fn other() { Box.plus(Box { value: 1 }, |) }")
	if help == nil || help.Signatures[0].Label != "plus(box: Box, amount: Int): Int" || help.ActiveParameter == nil || *help.ActiveParameter != 1 {
		t.Fatalf("new unbound method: %+v", help)
	}
	help = signatureAt(t, s, path, strings.Replace(src, "identity[T](value)", "identity[T](|)", 1))
	if help == nil || help.Signatures[0].Label != "identity(value: T): T" {
		t.Fatalf("generic caller context: %+v", help)
	}
}

func TestSignatureHelpRecoveryDoesNotBorrowUnrelatedLocalIdentities(t *testing.T) {
	src := `type Box = { value: Int }
type Holder = { box: Box }
fn (box: Box) plus(amount: Int): Int { box.value + amount }
fn (items: List[T]) plus[T](other: String): List[T] { items }
fn (items: List[T]) choose[T](index: Int): List[T] { items }
fn run(choose: (String) => String) uses io { println([1].choose(0)) }
fn main() uses io { box = [1]; holder = Holder { box: Box { value: 1 } }; println(holder.box.plus(2)) }
`
	s, path := newTestServer(t, src)
	for _, tc := range []struct{ before, after string }{
		{"println(holder.box.plus(2))", "println( holder.box.plus(|))"},
		{"holder.box.plus(2)", "(holder).box.plus(|)"},
		{"[1].choose(0)", "[2].choose(|)"},
	} {
		if help := signatureAt(t, s, path, strings.Replace(src, tc.before, tc.after, 1)); help != nil {
			t.Fatalf("borrowed unrelated identity for %s: %+v", tc.after, help)
		}
	}
}

func TestSignatureHelpQualifiedPackageRecovery(t *testing.T) {
	src := "import \"bork/json\"\nfn main() { encoded = json.Encode(42) }\n"
	s, path := newTestServer(t, src)
	help := signatureAt(t, s, path, src+"fn other() { json.Encode[Int](|) }")
	if help == nil || help.Signatures[0].Label != "Encode(x: Int): String" || help.ActiveParameter == nil || *help.ActiveParameter != 0 {
		t.Fatalf("qualified generic recovery: %+v", help)
	}
}

func TestSignatureHelpStaleAndNoAnalysis(t *testing.T) {
	src := "// Adds two integers.\nfn add(left: Int, right: Int): Int { left + right }\nfn main() { value = add(1, 2) }\n"
	s, path := newTestServer(t, src)
	current := strings.Replace(src, "add(1, 2)", "add(right: missing)", 1)
	s.docs[path] = document{current, 2}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	help := signatureAt(t, s, path, strings.Replace(current, "missing", "|missing", 1))
	if help == nil || help.Signatures[0].Documentation == nil || !strings.Contains(help.Signatures[0].Documentation.Value, "Stale") || *help.ActiveParameter != 1 {
		t.Fatalf("stale help: %+v", help)
	}
	empty := &server{}
	help, err := empty.signatureHelp(path, "fn main() { add(", position{0, 16})
	if err != nil || help != nil {
		t.Fatalf("no analysis: %+v %v", help, err)
	}
}
func TestSignatureHelpProtocol(t *testing.T) {
	src := "fn add(left: Int, right: Int): Int { left + right }\nfn main() { value = add(1, 2) }\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	s := &server{out: &bytes.Buffer{}, docs: map[string]document{}, packages: map[string]*packageState{}}
	initialize, rpcErr, _ := s.handle(message{Method: "initialize", Params: json.RawMessage(`{}`)})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	data, _ := json.Marshal(initialize)
	if !bytes.Contains(data, []byte("signatureHelpProvider")) {
		t.Fatalf("capability: %s", data)
	}
	params, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": fileURI(path), "version": 1, "text": src}})
	if _, rpcErr, _ := s.handle(message{Method: "textDocument/didOpen", Params: params}); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	params, _ = json.Marshal(map[string]any{"textDocument": map[string]any{"uri": fileURI(path)}, "position": lspPosition(src, offsetPos(src, strings.LastIndex(src, "2)")))})
	result, rpcErr, _ := s.handle(message{Method: "textDocument/signatureHelp", Params: params})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	data, _ = json.Marshal(result)
	if !bytes.Contains(data, []byte(`"activeParameter":1`)) || !bytes.Contains(data, []byte(`"parameters"`)) {
		t.Fatalf("protocol result: %s", data)
	}
}
func TestSignatureHelpStdioDocumentChanges(t *testing.T) {
	src := "// Adds two integers.\nfn add(left: Int, right: Int): Int { left + right }\nfn main() { value = add(1, 2) }\n"
	current := strings.Replace(src, "add(1, 2)", "add(right: missing)", 1)
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	var in, out bytes.Buffer
	for _, m := range []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"capabilities": map[string]any{"textDocument": map[string]any{"signatureHelp": map[string]any{"contextSupport": true}}}}},
		{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path), "version": 1, "text": src}}},
		{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path), "version": 2}, "contentChanges": []any{map[string]any{"text": current}}}},
		{"jsonrpc": "2.0", "id": 2, "method": "textDocument/signatureHelp", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path)}, "position": lspPosition(current, offsetPos(current, strings.Index(current, "missing"))), "context": map[string]any{"triggerKind": 2, "triggerCharacter": ":"}}},
		{"jsonrpc": "2.0", "id": 3, "method": "shutdown"}, {"jsonrpc": "2.0", "method": "exit"},
	} {
		if err := writeMessage(&in, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(&out)
	seen := false
	for reader.Buffered() > 0 || out.Len() > 0 {
		header, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		var length int
		if _, err := fmt.Sscanf(header, "Content-Length: %d", &length); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(reader, body); err != nil {
			t.Fatal(err)
		}
		var response struct {
			ID     int
			Result *signatureHelpResult
			Error  json.RawMessage
		}
		var envelope struct{ ID int }
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.ID != 2 {
			continue
		}
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Error) > 0 {
			t.Fatalf("RPC error: %s", body)
		}
		if response.Result == nil || response.Result.ActiveParameter == nil || *response.Result.ActiveParameter != 1 || response.Result.Signatures[0].Documentation == nil || !strings.Contains(response.Result.Signatures[0].Documentation.Value, "Stale") {
			t.Fatalf("stdio help: %s", body)
		}
		seen = true
	}
	if !seen {
		t.Fatal("missing signature response")
	}
}

func BenchmarkSignatureHelpHTTPServer(b *testing.B) {
	dir, err := filepath.Abs("../../examples/http_server")
	if err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(dir, "main.bork")
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	src := string(data)
	session := driver.NewSession()
	analysis, err := session.Analyze(dir, map[string]string{path: src})
	if err != nil {
		b.Fatal(err)
	}
	s := &server{packages: map[string]*packageState{dir: {analysis: analysis}}}
	for _, tc := range []struct {
		name, source string
		at           position
	}{
		{"checked", src, lspPosition(src, offsetPos(src, strings.Index(src, "http.JsonReply(404,")+len("http.JsonReply(404,")))},
		{"unfinished", src + "\nfn signature() { list(", endPosition(src + "\nfn signature() { list(")},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				help, err := s.signatureHelp(path, tc.source, tc.at)
				if err != nil || help == nil {
					b.Fatalf("help: %+v %v", help, err)
				}
			}
		})
	}
}
