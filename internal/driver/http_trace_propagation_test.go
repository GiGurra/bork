package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Exercise propagated labels and concurrent request isolation using generated HTTP code.
func TestHTTPTracePropagationRace(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a Go race executable")
	}
	path := filepath.Join("..", "..", "testdata", "cases", "http_trace_propagation")
	files, _, source, err := emit(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeGoModule(dir, files); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", "-race", "-mod=readonly", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("generated HTTP traces: %v\n%s\n%s", err, out, stderr.String())
	}
	compare(t, filepath.Join(path, "expected_output.txt"), string(out))
	compare(t, filepath.Join(path, "expected_stderr.txt"), stderr.String())
}
