package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/GiGurra/bork/internal/format"
)

// Exercise binary HTTP streaming and producer shutdown under the race detector
// using the actual generated implementation.
func TestHTTPClientStreamRace(t *testing.T) {
	testHTTPStreamRace(t, "http_client_stream")
}

func TestHTTPServerStreamRace(t *testing.T) {
	testHTTPStreamRace(t, "http_server_stream")
}

func testHTTPStreamRace(t *testing.T, fixture string) {
	if !testRaceEnabled {
		t.Skip("generated race executable is covered by go test -race")
	}
	t.Parallel()
	if testing.Short() {
		t.Skip("builds and runs a Go race executable")
	}
	path := filepath.Join("..", "..", "testdata", "cases", fixture)
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
		t.Fatalf("generated HTTP streaming: %v\n%s\n%s", err, out, stderr.String())
	}
	compare(t, filepath.Join(path, "expected_output.txt"), string(out))
}

func TestHTTPDocumentation(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "std", "http.md")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	page, err := parseDocPage(string(contents), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range page.blocks {
		if block.lang != "bork" {
			continue
		}
		file := filepath.Join(t.TempDir(), "main.bork")
		if err := os.WriteFile(file, []byte(block.source), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Check(file); err != nil {
			t.Fatalf("line %d: %v", block.line, err)
		}
		formatted, err := format.Source(file, []byte(block.source))
		if err != nil {
			t.Fatal(err)
		}
		if string(formatted) != block.source {
			t.Fatalf("line %d needs formatting:\n%s", block.line, formatted)
		}
	}
}
