package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/driver"
)

type decodedSemantic struct {
	text, kind string
	modifiers  []string
	start, end position
}

func decodeSemantic(t *testing.T, src string, result semanticTokensResult) []decodedSemantic {
	t.Helper()
	if len(result.Data)%5 != 0 {
		t.Fatal("incomplete token")
	}
	var out []decodedSemantic
	pos := position{}
	previousEnd := position{}
	for i := 0; i < len(result.Data); i += 5 {
		d := result.Data[i : i+5]
		if d[0] == 0 {
			pos.Character += int(d[1])
		} else {
			pos.Line += int(d[0])
			pos.Character = int(d[1])
		}
		end := position{pos.Line, pos.Character + int(d[2])}
		if d[2] == 0 || d[3] >= uint32(len(semanticTokenTypes)) || pos.Line < previousEnd.Line || pos.Line == previousEnd.Line && pos.Character < previousEnd.Character {
			t.Fatalf("invalid or overlapping token: %v", d)
		}
		from, err := byteOffset(src, pos)
		if err != nil {
			t.Fatal(err)
		}
		to, err := byteOffset(src, end)
		if err != nil {
			t.Fatal(err)
		}
		token := decodedSemantic{text: src[from:to], kind: semanticTokenTypes[d[3]], start: pos, end: end}
		for j, modifier := range semanticTokenModifiers {
			if d[4]&(1<<j) != 0 {
				token.modifiers = append(token.modifiers, modifier)
			}
		}
		out = append(out, token)
		previousEnd = end
	}
	return out
}
func assertSemantic(t *testing.T, tokens []decodedSemantic, text, kind string, modifiers ...string) {
	t.Helper()
	for _, token := range tokens {
		if token.text == text && token.kind == kind {
			match := true
			for _, m := range modifiers {
				match = match && slices.Contains(token.modifiers, m)
			}
			if match {
				return
			}
		}
	}
	t.Fatalf("missing %q %s %v in %+v", text, kind, modifiers, tokens)
}
func TestSemanticTokensResolvedMeaning(t *testing.T) {
	src := `pred positive(value: Int) { value > 0 }
type Box[T] = { value: T }
type Choice = sealed { First, Second }
Answer = 42
fn id[T](item: T): T { item }
fn (box: Box[Int]) read(): Int { box.value }
fn run(choice: Choice) uses io {
  local = id(Answer)
  box = Box[Int] { value: local }
  println(box.read())
  match (choice) { Choice.First => println(1); Choice.Second => println(2) }
}
fn main() uses io { run(Choice.First) }
`
	s, path := newTestServer(t, src)
	result, err := s.semanticTokens(path, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	tokens := decodeSemantic(t, src, result)
	for _, tc := range []struct {
		text, kind string
		modifiers  []string
	}{
		{"positive", "function", []string{"predicate", "declaration"}}, {"Int", "type", []string{"defaultLibrary"}},
		{"T", "typeParameter", []string{"declaration"}}, {"Box", "type", []string{"declaration"}},
		{"First", "enumMember", []string{"declaration", "readonly"}}, {"Answer", "variable", []string{"static", "readonly"}},
		{"item", "parameter", []string{"readonly"}}, {"local", "variable", []string{"declaration", "readonly"}},
		{"read", "method", nil}, {"io", "keyword", []string{"effect"}}, {"id", "function", nil},
		{"value", "property", []string{"readonly"}},
	} {
		assertSemantic(t, tokens, tc.text, tc.kind, tc.modifiers...)
	}
	// Cache ownership must not allow callers to corrupt later responses.
	cached := s.state(path).analysis.SemanticTokens(path)
	cached[0].Kind = "corrupted"
	again, err := s.semanticTokens(path, src, nil)
	if err != nil || !slices.Equal(result.Data, again.Data) {
		t.Fatalf("cache changed: %v", err)
	}
}
func TestSemanticTokensBindingsPatternsAndInterpolation(t *testing.T) {
	src := `pred small(x: Int) { x < 10 }
type Small = Int where small
type Whole = { n: Int } where valid
pred valid(record: Whole) { true }
fn constrained(p: (Int) => Bool, n: Int where p): Int { n }
type GoTime = go "time.Time"
fn ignore(value: GoTime) {}
type Choice = sealed { First { number: Int }, Second }
fn Abs(number: Float): Float unsafe go "math.Abs"
fn echo(number: Int): Int unsafe go { return number }
fn main() uses io {
  id = (value: Int) => value
  chosen = Choice.First { number: id(2) }
  match (chosen) {
    Choice.First { number: bound } => println(s"😀 $bound")
    Choice.Second => println(0)
  }
  println(Abs(number: -1.0))
  println(echo(1))
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	for name, text := range map[string]string{"main.bork": src, "bork.mod": "module example.com/semantic\nunsafe \"example.com/semantic\"\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := &server{out: &bytes.Buffer{}, initialized: true, docs: map[string]document{path: {src, 1}}, packages: map[string]*packageState{}, diagnostics: map[string][]diag.Diagnostic{}}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if s.state(path) == nil {
		t.Fatalf("check: %s", s.out)
	}
	result, err := s.semanticTokens(path, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	tokens := decodeSemantic(t, src, result)
	assertSemantic(t, tokens, "GoTime", "type", "declaration", "goBinding")
	assertSemantic(t, tokens, "time.Time", "type", "goBinding")
	assertSemantic(t, tokens, "valid", "function", "predicate")
	validCount := 0
	goReferences := 0
	for _, token := range tokens {
		if token.text == "valid" && slices.Contains(token.modifiers, "predicate") {
			validCount++
		}
		if token.text == "GoTime" && slices.Contains(token.modifiers, "goBinding") {
			goReferences++
		}
	}
	if validCount != 2 || goReferences != 2 {
		t.Fatalf("whole-type predicates/Go references: %d/%d", validCount, goReferences)
	}
	paramCount := 0
	for _, token := range tokens {
		if token.text == "p" && token.kind == "parameter" {
			paramCount++
		}
	}
	if paramCount != 2 {
		t.Fatalf("predicate parameter identity: %d", paramCount)
	}
	assertSemantic(t, tokens, "math.Abs", "function", "goBinding")
	assertSemantic(t, tokens, "Abs", "function", "goBinding")
	assertSemantic(t, tokens, "echo", "function", "goBinding")
	assertSemantic(t, tokens, "id", "variable", "readonly")
	assertSemantic(t, tokens, "number", "parameter", "readonly")
	assertSemantic(t, tokens, "small", "function", "predicate")
	assertSemantic(t, tokens, "bound", "variable", "declaration", "readonly")
	for _, token := range tokens {
		if strings.Contains(token.text, "return") {
			t.Fatalf("raw Go received bork semantic token: %+v", token)
		}
	}
	occurrences := 0
	for _, token := range tokens {
		if token.text == "First" && token.kind == "enumMember" {
			occurrences++
		}
	}
	if occurrences != 3 {
		t.Fatalf("variant declaration, literal and pattern: %d", occurrences)
	}
	// The resolved variable inside an interpolated string uses the parser position.
	for _, token := range tokens {
		if token.text == "bound" && token.start == lspPosition(src, offsetPos(src, strings.Index(src, "$bound")+1)) && token.kind == "variable" {
			return
		}
	}
	t.Fatal("interpolation variable missing")
}

func TestSemanticTokensPreserveTypeAndNestedPropertyMeaning(t *testing.T) {
	src := `type Child = { n: Int }
type Parent = { child: Child }
fn narrow(x: Int | String): Int { match (x) { n: Int => n; s: String => { _ = s; 0 } } }
fn main() { parent = Parent { child: Child { n: 1 } }; updated = parent.copy(child.n: 2); _ = updated }
`
	s, path := newTestServer(t, src)
	result, err := s.semanticTokens(path, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	tokens := decodeSemantic(t, src, result)
	for _, token := range tokens {
		if (token.text == "Int" || token.text == "String") && !slices.Contains(token.modifiers, "defaultLibrary") {
			t.Fatalf("builtin modifier lost: %+v", token)
		}
		if token.text == "child" && (token.kind != "property" || !slices.Contains(token.modifiers, "readonly")) {
			t.Fatalf("copy path treated as namespace: %+v", token)
		}
	}
}

func TestSemanticTokensImportedPackageValuesAndShadowing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	src := `import "example.com/semantic/lib"
fn main() uses io { Read = (value: Int) => value; println(Read(lib.Value)); println(lib.Read()) }
`
	if err := os.Mkdir(filepath.Join(dir, "lib"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"main.bork": src, "bork.mod": "module example.com/semantic\n", "lib/lib.bork": "Value = 42\nfn Read(): Int { Value }\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := &server{out: &bytes.Buffer{}, docs: map[string]document{path: {src, 1}}, packages: map[string]*packageState{}, diagnostics: map[string][]diag.Diagnostic{}}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if s.state(path) == nil {
		t.Fatalf("check: %s", s.out)
	}
	result, err := s.semanticTokens(path, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	tokens := decodeSemantic(t, src, result)
	assertSemantic(t, tokens, "lib", "namespace")
	assertSemantic(t, tokens, "Value", "variable", "static", "readonly")
	assertSemantic(t, tokens, "Read", "variable", "readonly")
	assertSemantic(t, tokens, "Read", "function")
}

func TestSemanticTokensCurrentSourceFallbackAndUTF16(t *testing.T) {
	src := "fn main() uses io { println(42) }\n"
	s, path := newTestServer(t, src)
	current := "// 😀 changed\r\nfn main() { missing(\"😀x\") }\r\n"
	s.docs[path] = document{current, 2}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if !s.state(path).stale {
		t.Fatal("expected stale analysis")
	}
	result, err := s.semanticTokens(path, current, nil)
	if err != nil {
		t.Fatal(err)
	}
	tokens := decodeSemantic(t, current, result)
	assertSemantic(t, tokens, "// 😀 changed", "comment")
	assertSemantic(t, tokens, "\"😀x\"", "string")
	for _, token := range tokens {
		if token.kind == "function" || token.kind == "variable" {
			t.Fatalf("old meanings leaked: %+v", token)
		}
	}
	requested := sourceRange{Start: position{1, 21}, End: position{1, 24}}
	result, err = s.semanticTokens(path, current, &requested)
	if err != nil {
		t.Fatal(err)
	}
	clipped := decodeSemantic(t, current, result)
	if len(clipped) != 1 || clipped[0].text != "😀x" {
		t.Fatalf("UTF16 range: %+v", clipped)
	}
	for _, r := range []sourceRange{{Start: position{1, 22}, End: position{1, 24}}, {Start: position{2, 0}, End: position{1, 0}}} {
		if _, err := s.semanticTokens(path, current, &r); err == nil {
			t.Fatal("invalid range accepted")
		}
	}
	// An unopened broken document has no successful snapshot at all.
	empty := &server{}
	result, err = empty.semanticTokens(path, "// comment\nfn broken(", nil)
	if err != nil {
		t.Fatal(err)
	}
	decodeSemantic(t, "// comment\nfn broken(", result)
	result, err = empty.semanticTokens(path, "", nil)
	if err != nil || result.Data == nil || len(result.Data) != 0 {
		t.Fatalf("empty result: %+v %v", result, err)
	}
}
func TestSemanticTokensStdioProtocol(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.bork")
	src := "fn main() uses io { println(42) }\n"
	if err := os.WriteFile(file, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	var in, out bytes.Buffer
	messages := []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize"},
		{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(file), "version": 1, "text": src}}},
		{"jsonrpc": "2.0", "id": 2, "method": "textDocument/semanticTokens/full", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(file)}}},
		{"jsonrpc": "2.0", "id": 3, "method": "textDocument/semanticTokens/range", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(file)}, "range": sourceRange{Start: position{0, 28}, End: position{0, 30}}}},
		{"jsonrpc": "2.0", "id": 4, "method": "shutdown"}, {"jsonrpc": "2.0", "method": "exit"},
	}
	for _, m := range messages {
		if err := writeMessage(&in, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(&out)
	seen := map[int]bool{}
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
		var envelope struct {
			ID     int
			Result json.RawMessage
			Error  json.RawMessage
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.ID == 0 {
			continue
		}
		seen[envelope.ID] = true
		if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
			t.Fatalf("protocol error: %s", body)
		}
		if envelope.ID == 1 {
			if !bytes.Contains(envelope.Result, []byte("semanticTokensProvider")) || !bytes.Contains(envelope.Result, []byte("goBinding")) {
				t.Fatalf("legend missing: %s", body)
			}
		}
		if envelope.ID == 2 || envelope.ID == 3 {
			var result semanticTokensResult
			if err := json.Unmarshal(envelope.Result, &result); err != nil {
				t.Fatal(err)
			}
			tokens := decodeSemantic(t, src, result)
			if envelope.ID == 2 {
				assertSemantic(t, tokens, "println", "function", "defaultLibrary")
			} else {
				assertSemantic(t, tokens, "42", "number")
			}
		}
	}
	if !seen[1] || !seen[2] || !seen[3] {
		t.Fatalf("missing responses: %v", seen)
	}
}
func BenchmarkSemanticTokensHTTPServer(b *testing.B) {
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
	session := driver.NewSession()
	analysis, err := session.Analyze(dir, map[string]string{file: src})
	if err != nil {
		b.Fatal(err)
	}
	s := &server{packages: map[string]*packageState{dir: {analysis: analysis}}}
	b.Run("cached", func(b *testing.B) {
		if _, err := s.semanticTokens(file, src, nil); err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := s.semanticTokens(file, src, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("fallback", func(b *testing.B) {
		current := src + "\nfn unfinished("
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := s.semanticTokens(file, current, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("recheck", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			suffix := strings.Repeat(" ", i%2)
			if _, err := session.Analyze(dir, map[string]string{file: src + suffix}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
