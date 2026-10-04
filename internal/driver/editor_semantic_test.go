package driver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GiGurra/bork/internal/check"
)

func BenchmarkEditorSemanticBuildHTTPServer(b *testing.B) {
	dir, err := filepath.Abs("../../examples/http_server")
	if err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(dir, "main.bork")
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	analysis, err := NewSession().Analyze(dir, map[string]string{path: string(data)})
	if err != nil {
		b.Fatal(err)
	}
	file, _ := analysis.editorFile(path)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		check.SemanticTokens(file, analysis.program.info)
	}
}
