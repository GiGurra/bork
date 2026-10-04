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

func completeAt(t *testing.T, s *server, path, src string) []any {
	t.Helper()
	offset := strings.Index(src, "|")
	if offset < 0 {
		t.Fatal("missing cursor")
	}
	src = strings.Replace(src, "|", "", 1)
	s.docs[path] = document{src, 2}
	return s.completion(s.state(path), path, src, lspPosition(src, offsetPos(src, offset)))
}
func completionItem(items []any, name string) map[string]any {
	for _, item := range items {
		m := item.(map[string]any)
		if m["label"] == name {
			return m
		}
	}
	return nil
}
func TestCompletionScopesFieldsAndArguments(t *testing.T) {
	src := "type User = { name: String, age: Int }\nfn greet(user: User, suffix: String): String { user.name + suffix }\nfn main() uses io {\n  user = User { name: \"Ada\", age: 36 }\n  println(greet(user, \"!\"))\n}\n"
	s, path := newTestServer(t, src)
	items := completeAt(t, s, path, strings.Replace(src, "println(greet(user, \"!\"))", "us|", 1))
	if c := completionItem(items, "user"); c == nil || c["sortText"] != "0-user" {
		t.Fatalf("local: %+v", items)
	}
	items = completeAt(t, s, path, strings.Replace(src, "println(greet(user, \"!\"))", "user.na|me", 1))
	c := completionItem(items, "name")
	if c == nil || c["kind"] != 5 {
		t.Fatalf("field: %+v", items)
	}
	edit := c["textEdit"].(textEdit)
	if edit.NewText != "name" || edit.Range.End.Character-edit.Range.Start.Character != 4 {
		t.Fatalf("replacement: %+v", edit)
	}
	items = completeAt(t, s, path, strings.Replace(src, "println(greet(user, \"!\"))", "greet(su|)", 1))
	if c := completionItem(items, "suffix"); c == nil || c["textEdit"].(textEdit).NewText != "suffix: " {
		t.Fatalf("named arg: %+v", items)
	}
	items = completeAt(t, s, path, strings.Replace(src, "name: \"Ada\", age: 36", "name: \"Ada\", ag|", 1))
	if c := completionItem(items, "age"); c == nil || c["textEdit"].(textEdit).NewText != "age: " {
		t.Fatalf("literal: %+v", items)
	}
	items = completeAt(t, s, path, strings.Replace(src, "age: 36", "age: 36, na|", 1))
	if completionItem(items, "name") != nil {
		t.Fatal("offered duplicate field")
	}
}
func TestCompletionGenericFieldsAndMatchArms(t *testing.T) {
	src := "type Box[T] = { value: T }\ntype Choice = sealed { First { value: Int }, Second }\nfn read(choice: Choice): Int { match (choice) { Choice.First { value } => value, Choice.Second => 0 } }\nfn main() uses io { box = Box[Int] { value: 1 }; println(read(Choice.Second)); println(box.value) }\n"
	s, path := newTestServer(t, src)
	items := completeAt(t, s, path, strings.Replace(src, "value: 1", "va|", 1))
	if c := completionItem(items, "value"); c == nil || c["detail"] != "Int" {
		t.Fatalf("generic field: %+v", items)
	}
	items = completeAt(t, s, path, strings.Replace(src, "Choice.First { value } => value, Choice.Second => 0", "Ch|", 1))
	if c := completionItem(items, "Choice.First"); c == nil || !strings.Contains(c["textEdit"].(textEdit).NewText, "{ value } =>") {
		t.Fatalf("match: %+v", items)
	}
}
func TestCompletionAutoImportEditChecks(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib")
	if err := os.Mkdir(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.bork")
	src := "fn main() uses io { println(42) }\n"
	for name, text := range map[string]string{filepath.Join(dir, "bork.mod"): "module example.com/app\n", filepath.Join(lib, "lib.bork"): "fn Answer(): Int { 42 }\n", main: src} {
		if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &server{docs: map[string]document{main: {src, 1}}, packages: map[string]*packageState{}, out: &bytes.Buffer{}}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	current := strings.Replace(src, "42", "Ans|", 1)
	c := completionItem(completeAt(t, s, main, current), "Answer")
	if c == nil {
		t.Fatal("missing auto-import")
	}
	text := strings.Replace(current, "|", "", 1)
	edits := append([]textEdit{}, c["additionalTextEdits"].([]textEdit)...)
	edits = append(edits, c["textEdit"].(textEdit))
	// Import insertion is before the completion; apply edits in reverse order.
	for i := len(edits) - 1; i >= 0; i-- {
		edit := edits[i]
		lo, _ := byteOffset(text, edit.Range.Start)
		hi, _ := byteOffset(text, edit.Range.End)
		text = text[:lo] + edit.NewText + text[hi:]
	}
	if _, err := driver.NewSession().Analyze(dir, map[string]string{main: text}); err != nil {
		t.Fatalf("completion does not check: %v\n%s", err, text)
	}
}
func TestCompletionWithoutSuccessfulAnalysisAndSnippetNegotiation(t *testing.T) {
	s := &server{docs: map[string]document{}, packages: map[string]*packageState{}}
	items := s.completion(nil, "main.bork", "fn", position{0, 2})
	if completionItem(items, "fn") == nil || completionItem(items, "fn snippet") != nil {
		t.Fatalf("plain: %+v", items)
	}
	var params = json.RawMessage(`{"capabilities":{"textDocument":{"completion":{"completionItem":{"snippetSupport":true}}}}}`)
	if _, err, _ := s.handle(message{Method: "initialize", Params: params}); err != nil {
		t.Fatal(err)
	}
	items = s.completion(nil, "main.bork", "fn", position{0, 2})
	if c := completionItem(items, "fn snippet"); c == nil || c["insertTextFormat"] != 2 {
		t.Fatalf("snippet: %+v", items)
	}
	if items := s.completion(nil, "main.bork", "// fn", position{0, 4}); len(items) != 0 {
		t.Fatalf("comment: %+v", items)
	}
	if items := s.completion(nil, "main.bork", "\"fn\"", position{0, 3}); len(items) != 0 {
		t.Fatalf("string: %+v", items)
	}
}

func TestCompletionPatternAndValueContexts(t *testing.T) {
	src := "type User = { name: String }\nfn read(user: User): String { match (user) { User { name } => name } }\nfn main() uses io { user = User { name: \"Ada\" }; println(read(user)) }\n"
	s, path := newTestServer(t, src)
	items := completeAt(t, s, path, strings.Replace(src, "User { name } => name", "User { na| } => name", 1))
	if c := completionItem(items, "name"); c == nil || c["textEdit"].(textEdit).NewText != "name" {
		t.Fatalf("pattern: %+v", items)
	}
	items = completeAt(t, s, path, strings.Replace(src, "println(read(user))", "println(read(user: us|))", 1))
	if completionItem(items, "user") == nil {
		t.Fatal("missing value candidate")
	}
	for _, item := range items {
		c := item.(map[string]any)
		if c["textEdit"].(textEdit).NewText == "user: " {
			t.Fatal("offered argument label inside a value")
		}
	}
}

func TestCompletionImportedPackageAndScope(t *testing.T) {
	src := "import \"bork/json\"\nfn main() uses io {\n  { hidden = 1; println(hidden) }\n  println(json.Encode(42))\n}\n"
	s, path := newTestServer(t, src)
	items := completeAt(t, s, path, strings.Replace(src, "json.Encode(42)", "json.En|", 1))
	if completionItem(items, "Encode") == nil {
		t.Fatalf("package completion: %+v", items)
	}
	items = completeAt(t, s, path, strings.Replace(src, "json.Encode(42)", "hid|", 1))
	if completionItem(items, "hidden") != nil {
		t.Fatal("leaked a closed block binding")
	}
}

func TestCompletionStdioProtocol(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.bork")
	src := "type User = { name: String }\nfn main() uses io {\n  user = User { name: \"Ada\" }\n  println(user.name)\n}\n"
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	current := strings.Replace(src, "println(user.name)", "user.na", 1)
	var in, out bytes.Buffer
	for _, m := range []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}},
		{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(file), "version": 1, "text": src}}},
		{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(file), "version": 2}, "contentChanges": []any{map[string]any{"text": current}}}},
		{"jsonrpc": "2.0", "id": 2, "method": "textDocument/completion", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(file)}, "position": position{3, 9}}},
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
	reader := bufio.NewReader(&out)
	for {
		header, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
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
			ID     int `json:"id"`
			Result []struct {
				Label    string   `json:"label"`
				Detail   string   `json:"detail"`
				TextEdit textEdit `json:"textEdit"`
			} `json:"result"`
		}
		var envelope struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.ID != 2 {
			continue
		}
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		for _, item := range response.Result {
			if item.Label == "name" {
				if !strings.Contains(item.Detail, "Stale") || item.TextEdit.NewText != "name" {
					t.Fatalf("completion: %+v", item)
				}
				return
			}
		}
		t.Fatalf("missing field completion: %s", body)
	}
	t.Fatal("missing completion response")
}

func BenchmarkCompletionHTTPServer(b *testing.B) {
	dir, err := filepath.Abs("../../examples/http_server")
	if err != nil {
		b.Fatal(err)
	}
	file := filepath.Join(dir, "main.bork")
	data, err := os.ReadFile(file)
	if err != nil {
		b.Fatal(err)
	}
	src := string(data)
	a, err := driver.NewSession().Analyze(dir, map[string]string{file: src})
	if err != nil {
		b.Fatal(err)
	}
	pkg := &packageState{analysis: a, stale: true}
	s := &server{packages: map[string]*packageState{dir: pkg}}
	current := src + "\nfn completion() { En"
	p := endPosition(current)
	s.completion(pkg, file, current, p)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.completion(pkg, file, current, p)
	}
}
