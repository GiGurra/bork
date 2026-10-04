package lsp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestLintWarningsAndQuickFix(t *testing.T) {
	s, path := newTestServer(t, "fn main() {\n  unused = 42\n}\n")
	if len(s.diagnostics[path]) != 1 || s.diagnostics[path][0].Code != "lint.unused-binding" {
		t.Fatalf("warnings: %+v", s.diagnostics[path])
	}
	messages := s.out.(*bytes.Buffer).String()
	if !strings.Contains(messages, `"severity":2`) {
		t.Fatalf("not an LSP warning: %s", messages)
	}
	p := documentParams{Range: sourceRange{position{1, 0}, position{1, 20}}}
	actions, err := s.feature("textDocument/codeAction", path, p)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(actions)
	if !strings.Contains(string(body), `"newText":"_"`) {
		t.Fatalf("quick fix: %s", body)
	}
	s.docs[path] = document{"fn main() {\n // lint:ignore lint.unused-binding intentional\n unused = 42\n}\n", 2}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if len(s.diagnostics[path]) != 0 {
		t.Fatalf("suppression: %+v", s.diagnostics[path])
	}
}
