package lsp

import (
	"strings"
	"testing"
)

func TestRebindingHoverAndSemanticToken(t *testing.T) {
	source := "fn F(input: Int): String {\n  input = s\"$input\"\n  input\n}\nfn main() {}\n"
	s, path := newTestServer(t, source)
	hover := query(t, s, path, "hover", 2, 2)
	value := hover.(map[string]any)["contents"].(map[string]string)["value"]
	if !strings.Contains(value, "Rebinds `input` (line 1).") {
		t.Fatalf("hover: %s", value)
	}
	result, err := s.semanticTokens(path, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	tokens := decodeSemantic(t, source, result)
	assertSemantic(t, tokens, "input", "variable", "declaration", "rebinding", "readonly")
	for _, token := range tokens {
		if token.start.Line != 1 {
			for _, modifier := range token.modifiers {
				if modifier == "rebinding" {
					t.Fatalf("rebinding modifier leaked: %+v", token)
				}
			}
		}
	}
}

func TestRebindingMockSemanticToken(t *testing.T) {
	source := "fn Fetch(url: String) uses io: String { url }\nfn main() {}\ntest \"mock\" {\n  handle = 1\n  println(handle)\n  handle = mock Fetch(url) { url }\n  handle.expect(times: 0)\n}\n"
	s, path := newTestServer(t, source)
	result, err := s.semanticTokens(path, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	tokens := decodeSemantic(t, source, result)
	assertSemantic(t, tokens, "handle", "variable", "declaration", "rebinding", "readonly")
	hover := query(t, s, path, "hover", 6, 2)
	value := hover.(map[string]any)["contents"].(map[string]string)["value"]
	if !strings.Contains(value, "Rebinds `handle` (line 4).") {
		t.Fatalf("hover: %s", value)
	}
}
