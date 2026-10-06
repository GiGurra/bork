package check

import (
	"strings"
	"testing"
)

func TestCompilerBuiltinDeclarations(t *testing.T) {
	seen := map[string]bool{}
	last := ""
	for _, declaration := range CompilerBuiltinDeclarations() {
		if declaration.Name <= last || declaration.Signature == "" || declaration.Documentation == "" {
			t.Fatalf("invalid or unordered declaration: %+v", declaration)
		}
		last = declaration.Name
		seen[declaration.Name] = true
		if !strings.Contains(declaration.Signature, declaration.Name+"(") && !strings.Contains(declaration.Signature, declaration.Name+"[") {
			t.Errorf("signature omits function name: %+v", declaration)
		}
	}
	for name := range builtins {
		if PreludeVisible(name) != seen[name] {
			t.Errorf("builtin visibility mismatch: %s", name)
		}
	}
}
