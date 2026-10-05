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
