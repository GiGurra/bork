package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	t.Run("successful build", func(t *testing.T) {
		out, err := exec.Command(exe, "build", "--json", "-o", filepath.Join(dir, "program"), path).CombinedOutput()
		if err != nil || len(out) != 0 {
			t.Fatalf("build should emit no diagnostics: %v\n%s", err, out)
		}
	})
	for _, passing := range []bool{true, false} {
		t.Run(fmt.Sprintf("runtime test %t", passing), func(t *testing.T) {
			source := fmt.Sprintf("test \"example\" { assert(%t) }\n", passing)
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, "test", "--json", path)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if passing && err != nil || !passing && cmd.ProcessState.ExitCode() != 1 {
				t.Fatalf("unexpected test exit: %v", err)
			}
			if stderr.Len() != 0 || !strings.Contains(stdout.String(), "example") || !strings.Contains(stdout.String(), "passed,") {
				t.Fatalf("test report must remain on stdout: %s / %s", &stdout, &stderr)
			}
		})
	}
}
