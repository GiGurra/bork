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

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/driver"
)

type protocolInlay struct {
	Position     position
	Label        string
	Kind         int
	PaddingRight bool
}

func inlayProtocol(t *testing.T, source string, options map[string]bool, selection sourceRange, changed string) []protocolInlay {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.NewSession().Analyze(filepath.Dir(path), map[string]string{path: source}); err != nil {
		t.Fatalf("invalid fixture: %v", err)
	}
	messages := []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"initializationOptions": map[string]any{"inlayHints": options}}},
		{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path), "version": 1, "text": source}}},
	}
	if changed != "" {
		// Ensure a successful snapshot exists before the broken edit.
		messages = append(messages, map[string]any{"jsonrpc": "2.0", "id": 9, "method": "textDocument/inlayHint", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path)}, "range": selection}})
		messages = append(messages, map[string]any{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path), "version": 2}, "contentChanges": []any{map[string]any{"text": changed}}}})
	}
	messages = append(messages,
		map[string]any{"jsonrpc": "2.0", "id": 2, "method": "textDocument/inlayHint", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path)}, "range": selection}},
		map[string]any{"jsonrpc": "2.0", "id": 3, "method": "shutdown"}, map[string]any{"jsonrpc": "2.0", "method": "exit"},
	)
	var in, out bytes.Buffer
	for _, m := range messages {
		if err := writeMessage(&in, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	if changed != "" && bytes.Contains(out.Bytes(), []byte(`"version":2`)) {
		t.Fatal("inlay request checked the pending changed buffer")
	}
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
		}
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		if response.ID == 1 && !bytes.Contains(response.Result, []byte(`"inlayHintProvider":true`)) {
			t.Fatal("missing inlay capability")
		}
		if response.ID == 2 {
			if response.Error != nil {
				t.Fatalf("inlay error: %+v", response.Error)
			}
			var hints []protocolInlay
			if err := json.Unmarshal(response.Result, &hints); err != nil {
				t.Fatal(err)
			}
			return hints
		}
	}
	t.Fatal("missing response")
	return nil
}

func TestInlayHintsProtocol(t *testing.T) {
	source := `fn sum(first: Int, second: Int = 2): Int { first + second }
fn main() uses io {
 greeting = "😀"; _ = greeting; inferred = sum(1, second: 3)
 explicit: Int = 4
 println(inferred + explicit)
}`
	hints := inlayProtocol(t, source, map[string]bool{"types": true, "parameters": true}, sourceRange{position{}, endPosition(source)}, "")
	want := []string{": String", ": Int", "first:"}
	for _, label := range want {
		if !containsInlay(hints, label) {
			t.Fatalf("missing %s: %+v", label, hints)
		}
	}
	for _, hint := range hints {
		if hint.Label == "first:" && (hint.Kind != 2 || !hint.PaddingRight) {
			t.Fatalf("parameter kind: %+v", hint)
		}
		if hint.Label == "second:" {
			t.Fatalf("already labeled argument: %+v", hint)
		}
		if hint.Label == ": Int" { // position is after inferred, following emoji UTF-16 text.
			byteAt := strings.Index(source, "inferred =") + len("inferred")
			prefix := source[:byteAt]
			wantPos := lspPosition(source, diag.Pos{Line: strings.Count(prefix, "\n") + 1, Col: byteAt - strings.LastIndexByte(prefix, '\n')})
			if hint.Position != wantPos {
				t.Fatalf("wrong binding location: %+v want %+v", hint, wantPos)
			}
		}
	}
	if got := inlayProtocol(t, source, map[string]bool{"types": false, "parameters": false, "facts": false}, sourceRange{position{}, endPosition(source)}, ""); len(got) != 0 {
		t.Fatalf("disabled: %+v", got)
	}
	if got := inlayProtocol(t, source, nil, sourceRange{position{3, 0}, position{4, 0}}, ""); len(got) != 0 {
		t.Fatalf("range contains explicitly typed binding: %+v", got)
	}
	if got := inlayProtocol(t, source, nil, sourceRange{position{}, endPosition(source)}, "fn main() { broken = missing }\n"); len(got) != 0 {
		t.Fatalf("obsolete positions retained: %+v", got)
	}
}

func containsInlay(hints []protocolInlay, label string) bool {
	for _, hint := range hints {
		if hint.Label == label {
			return true
		}
	}
	return false
}

func TestInlayHintsFactsAndMethodArguments(t *testing.T) {
	source := `pred positive(n: Int) { n > 0 }
type Box = { value: Int }
fn (box: Box) add(amount: Int): Int { box.value + amount }
fn constrained(value: Int where positive): Int { copied = value; copied }
fn main() uses io { box = Box { value: 1 }; println(box.add(2)); println(constrained(3)) }`
	hints := inlayProtocol(t, source, map[string]bool{"types": false, "parameters": true, "facts": true}, sourceRange{position{}, endPosition(source)}, "")
	for _, label := range []string{"amount:", "value:", " where positive"} {
		if !containsInlay(hints, label) {
			t.Fatalf("missing %s: %+v", label, hints)
		}
	}
	for _, hint := range hints {
		if hint.Label == "box:" {
			t.Fatalf("implicit receiver: %+v", hint)
		}
	}
}

func TestInlayHintsConfiguration(t *testing.T) {
	s := &server{}
	if err := s.configureInlays(json.RawMessage(`{"settings":{"bork":{"inlayHints":{"types":false,"parameters":true,"facts":true}}}}`)); err != nil {
		t.Fatal(err)
	}
	if *s.inlaySettings.Types || !*s.inlaySettings.Parameters || !*s.inlaySettings.Facts {
		t.Fatalf("settings: %+v", s.inlaySettings)
	}
	if err := s.configureInlays(json.RawMessage(`{"settings":{"bork":{"inlayHints":{"facts":false}}}}`)); err != nil {
		t.Fatal(err)
	}
	if *s.inlaySettings.Types || !*s.inlaySettings.Parameters || *s.inlaySettings.Facts {
		t.Fatal("partial update lost settings")
	}
}

func BenchmarkInlayHintsHTTPServer(b *testing.B) {
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
	for _, facts := range []bool{false, true} {
		b.Run(fmt.Sprintf("facts=%t", facts), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				hints, err := s.state(path).analysis.EditorInlays(path, check.EditorInlayOptions{Types: true, Parameters: true, Facts: facts})
				if err != nil || len(hints) == 0 {
					b.Fatalf("hints %d: %v", len(hints), err)
				}
			}
		})
	}
}

func TestInlayHintsConfigurationRefreshProtocol(t *testing.T) {
	source := "fn main() { x = 1; _ = x }\n"
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	var in, out bytes.Buffer
	for _, m := range []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"capabilities": map[string]any{"workspace": map[string]any{"inlayHint": map[string]any{"refreshSupport": true}}}}},
		{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path), "version": 1, "text": source}}},
		{"jsonrpc": "2.0", "id": 2, "method": "textDocument/inlayHint", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path)}, "range": sourceRange{position{}, endPosition(source)}}},
		{"jsonrpc": "2.0", "method": "workspace/didChangeConfiguration", "params": map[string]any{"settings": map[string]any{"bork": map[string]any{"inlayHints": map[string]bool{"types": false}}}}},
		{"jsonrpc": "2.0", "id": "bork/inlayHint/refresh/1", "result": nil},
		{"jsonrpc": "2.0", "id": 3, "method": "textDocument/inlayHint", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path)}, "range": sourceRange{position{}, endPosition(source)}}},
		{"jsonrpc": "2.0", "id": 4, "method": "shutdown"}, {"jsonrpc": "2.0", "method": "exit"},
	} {
		if err := writeMessage(&in, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	refresh, disabled := false, false
	r := bufio.NewReader(&out)
	for {
		m, err := readMessage(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if m.Method == "workspace/inlayHint/refresh" {
			refresh = true
			continue
		}
		if string(m.ID) == `"bork/inlayHint/refresh/1"` {
			t.Fatal("server responded to client response")
		}
		if string(m.ID) == "2" && !bytes.Contains(m.Result, []byte(`": Int"`)) {
			t.Fatalf("initial hints missing: %s", m.Result)
		}
		if string(m.ID) == "3" {
			disabled = true
			if string(m.Result) != "[]" {
				t.Fatalf("disabled hints retained: %s", m.Result)
			}
		}
	}
	if !refresh || !disabled {
		t.Fatalf("refresh=%t disabled response=%t", refresh, disabled)
	}
}
