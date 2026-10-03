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
	t.Run("snapshot golden", func(t *testing.T) {
		fixture := filepath.Join("..", "..", "testdata", "cases", "snapshot_json_ok")
		cmd := exec.Command(exe, "test", "--json", fixture)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("snapshot test: %v\n%s\n%s", err, &stdout, &stderr)
		}
		wantDiagnostics, err := os.ReadFile(filepath.Join(fixture, "expected_diagnostics.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		wantReport, err := os.ReadFile(filepath.Join(fixture, "expected_test_output.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if stderr.String() != string(wantDiagnostics) || stdout.String()+"exit code 0\n" != string(wantReport) {
			t.Fatalf("snapshot JSON golden mismatch:\nstdout: %s\nstderr: %s", &stdout, &stderr)
		}
	})
}

func TestFormatCLI(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "bork")
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "main.bork")
	src := []byte("fn main(){println(1)}")
	if err := os.WriteFile(good, src, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.bork", "z.bork"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("@"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, check := range []bool{true, false} {
		args := []string{"fmt"}
		if check {
			args = append(args, "--check")
		}
		cmd := exec.Command(exe, args...)
		cmd.Dir = dir
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err == nil || cmd.ProcessState.ExitCode() != 1 {
			t.Fatalf("expected exit 1: %v", err)
		}
		if !strings.Contains(stdout.String(), "main.bork") {
			t.Fatalf("missing changed file: %s", &stdout)
		}
		for _, name := range []string{"a.bork", "z.bork"} {
			if !strings.Contains(stderr.String(), name) {
				t.Fatalf("missing error for %s: %s", name, &stderr)
			}
			got, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || string(got) != "@" {
				t.Fatal("invalid file changed")
			}
		}
		got, err := os.ReadFile(good)
		if err != nil {
			t.Fatal(err)
		}
		if check && !bytes.Equal(got, src) {
			t.Fatal("check wrote source")
		}
		if !check && bytes.Equal(got, src) {
			t.Fatal("invalid files prevented valid file formatting")
		}
	}
	for _, name := range []string{"a.bork", "z.bork"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(exe, "fmt", "--check")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
		t.Fatalf("clean directory: %v\n%s", err, out)
	}
}

func TestDescribeCLI(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "bork")
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	path := filepath.Join(dir, "main.bork")
	source := "pred positive(n: Int) { n > 0 }\nfn example(n: Int) uses io { println(n) }\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	position := path + ":2:38"
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("JSON %t", asJSON), func(t *testing.T) {
			args := []string{"describe", position, "--where", "positive"}
			if asJSON {
				args = append(args, "--json")
			}
			cmd := exec.Command(exe, args...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil || stderr.Len() != 0 {
				t.Fatalf("an unproven fact is a successful query: %v\n%s", err, &stderr)
			}
			if asJSON {
				var result struct {
					SchemaVersion int    `json:"schema_version"`
					Type          string `json:"type"`
					Proof         struct {
						Proven bool   `json:"proven"`
						Reason string `json:"reason"`
					} `json:"proof"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.SchemaVersion != 1 || result.Type != "Int" || result.Proof.Proven || result.Proof.Reason == "" {
					t.Fatalf("unexpected JSON description: %s (%v)", &stdout, err)
				}
			} else if !strings.Contains(stdout.String(), "type: Int\n") || !strings.Contains(stdout.String(), "not proven: positive\n") || !strings.Contains(stdout.String(), "check it first") {
				t.Fatalf("unexpected text description: %s", &stdout)
			}
		})
	}
	t.Run("invalid query", func(t *testing.T) {
		cmd := exec.Command(exe, "describe", position, "--where", "missing", "--json")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err == nil || stdout.Len() != 0 || !json.Valid(stderr.Bytes()) {
			t.Fatalf("invalid query should fail with JSON on stderr: %v\n%s / %s", err, &stdout, &stderr)
		}
	})
}

func TestDepsCLI(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "bork")
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bork.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(wantOK bool, args ...string) {
		t.Helper()
		cmd := exec.Command(exe, append([]string{"deps"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if (err == nil) != wantOK {
			t.Fatalf("deps %v: %v\n%s", args, err, out)
		}
	}
	run(true, "init", "--path", dir)
	for _, name := range []string{"go-deps.mod", "go-deps.sum"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	run(false, "init")
	run(true, "download")
	run(false, "download", "unexpected")
	run(false, "get")
	run(false, "get", "toolchain@latest")
	run(false, "get", "--", "-u")
}
