package lsp

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/driver"
)

func TestHoverSharesPackageDocumentation(t *testing.T) {
	src := "// Value contains <script>text</script>.\n//\n// ```bork\n// Value()\n// ```\nfn Value(): Int { 1 }\nfn main() { _ = Value() }\n"
	s, path := newTestServer(t, src)
	hover := query(t, s, path, "hover", 6, 17)
	if hover == nil {
		t.Fatal("missing hover")
	}
	text := hover.(map[string]any)["contents"].(map[string]string)["value"]
	docs, err := driver.Doc(filepath.Dir(path), driver.DocOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"Value contains &lt;script&gt;text&lt;/script&gt;.", "```bork"} {
		if !bytes.Contains(docs, []byte(phrase)) || !strings.Contains(text, phrase) {
			t.Fatalf("docs and hover disagree about %q: %s / %s", phrase, docs, text)
		}
	}
}
