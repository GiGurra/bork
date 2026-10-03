package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComptimeEvaluationGate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	// Even a recipe which would panic must not execute in this static-only phase.
	if err := os.WriteFile(path, []byte("fn main(){n:Int=comptime{panic(\"must not run\")};println(n)}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Check(dir)
	if err == nil || !strings.Contains(err.Error(), "comptime evaluation is not implemented yet") {
		t.Fatalf("unexpected result: %v", err)
	}
}
