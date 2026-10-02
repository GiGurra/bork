package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDiagnosticJSON(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "bork")
	build := exec.Command("go", "build", "-o", exe, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte("fn main() { println(missing) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"check", "build", "test"} {
		t.Run(command, func(t *testing.T) {
			cmd := exec.Command(exe, command, "--json", path)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err == nil {
				t.Fatal("expected nonzero exit")
			}
			diagnostics, other := &stderr, &stdout
			if command == "check" {
				diagnostics, other = &stdout, &stderr
			}
			if other.Len() != 0 {
				t.Fatalf("unexpected output on other stream: %s", other)
			}
			var d struct {
				SchemaVersion int    `json:"schema_version"`
				File          string `json:"file"`
				Code          string `json:"code"`
				Message       string `json:"message"`
			}
			if err := json.Unmarshal(diagnostics.Bytes(), &d); err != nil {
				t.Fatalf("invalid JSON: %v\n%s", err, diagnostics)
			}
			if d.SchemaVersion != 1 || d.File != path || d.Code != "type.error" || d.Message != "undefined: missing" {
				t.Fatalf("unexpected diagnostic: %+v", d)
			}
		})
	}
	t.Run("tool error", func(t *testing.T) {
		cmd := exec.Command(exe, "check", "--json", filepath.Join(dir, "missing.bork"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err == nil {
			t.Fatal("expected nonzero exit")
		}
		var d struct {
			Code string `json:"code"`
			Line int    `json:"line"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &d); err != nil || d.Code != "tool.error" || d.Line != 0 || stderr.Len() != 0 {
			t.Fatalf("unexpected tool error: %s / %s (%v)", &stdout, &stderr, err)
		}
	})
	t.Run("successful check", func(t *testing.T) {
		if err := os.WriteFile(path, []byte("fn main() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(exe, "check", "--json", path).CombinedOutput()
		if err != nil || len(out) != 0 {
			t.Fatalf("success should emit nothing: %v\n%s", err, out)
		}
	})
}
