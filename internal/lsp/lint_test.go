package lsp

import (
	"bytes"
	"strings"
	"testing"
)

func TestUnusedBindingErrorAndQuickFix(t *testing.T) {
	s, path := newTestServer(t, "fn main() {\n  _ = 42\n}\n")
	s.docs[path] = document{"fn main() {\n  unused = 42\n}\n", 2}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if len(s.diagnostics[path]) != 1 || s.diagnostics[path][0].Code != "binding.unused" {
		t.Fatalf("warnings: %+v", s.diagnostics[path])
	}
	messages := s.out.(*bytes.Buffer).String()
	if !strings.Contains(messages, `"severity":1`) {
		t.Fatalf("not an LSP error: %s", messages)
	}
	p := documentParams{Range: sourceRange{position{1, 0}, position{1, 20}}}
	actions, err := s.feature("textDocument/codeAction", path, p)
	if err != nil {
		t.Fatal(err)
	}
	items := actions.([]any)
	if len(items) != 2 {
		t.Fatalf("quick fixes: %+v", items)
	}
	changes := items[0].(map[string]any)["edit"].(map[string]any)["changes"].(map[string][]textEdit)
	if edits := changes[fileURI(path)]; len(edits) != 1 || edits[0].NewText != "fn main() {\n  _ = 42\n}\n" {
		t.Fatalf("quick fix: %+v", changes)
	}
	s.docs[path] = document{"fn main() {\n // lint:ignore lint.unused-binding intentional\n unused = 42\n}\n", 2}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if len(s.diagnostics[path]) != 1 || s.diagnostics[path][0].Code != "binding.unused" {
		t.Fatalf("compiler error must resist lint suppression: %+v", s.diagnostics[path])
	}
}
