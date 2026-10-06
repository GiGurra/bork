package driver

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite expected files with actual results")

// TestMain lets test cases bind example.com/bindtest, a local Go module
// with the shapes Go's standard library does not have.
func TestMain(m *testing.M) {
	configureTestStageCache()
	// Detached trim workers outlive the test that staged; only the trim tests
	// start them, directly and with their own barriers.
	scheduleCacheTrim = func(string) bool { return false }

	// Parallel cases each start Go tools; keep their GC/build workers bounded
	// without changing this process's runtime or an explicit caller setting.
	if os.Getenv("GOMAXPROCS") == "" {
		if err := os.Setenv("GOMAXPROCS", "2"); err != nil {
			panic(err)
		}
	}
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "gomod", "bindtest"))
	if err != nil {
		panic(err)
	}
	goModuleHook = func(goMod []byte) []byte {
		return fmt.Appendf(goMod, "\nrequire example.com/bindtest v0.0.0\n\nreplace example.com/bindtest => %s\n", dir)
	}
	os.Exit(m.Run())
}

// TestCases runs every case under testdata/cases. A case is a directory
// of .bork files plus either:
//
//   - expected_output.txt: the program must build, run, and print exactly this.
//   - expected_errors.txt: compilation must fail with exactly these diagnostics.
//     (Errors in unsafe go code come from building the generated Go.)
func TestCases(t *testing.T) {
	// Other integration tests read these goldens; finish updates before they run.
	if !*update {
		t.Parallel()
	}
	root := filepath.Join("..", "..", "testdata", "cases")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			t.Parallel()
			if _, err := os.Stat(filepath.Join(dir, "expected_errors.txt")); err == nil {
				runErrorCase(t, dir)
				return
			}
			if _, err := os.Stat(filepath.Join(dir, "expected_test_output.txt")); err == nil {
				runTestCase(t, dir)
				return
			}
			runOutputCase(t, dir)
		})
	}
}

func runOutputCase(t *testing.T, dir string) {
	exe, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatalf("build failed:\n%v", err)
	}
	cmd := exec.Command(exe)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("program failed: %v\n%s", err, out)
	}
	compare(t, filepath.Join(dir, "expected_output.txt"), string(out))
	if _, err := os.Stat(filepath.Join(dir, "expected_stderr.txt")); err == nil {
		got := strings.ReplaceAll(stderr.String(), dir+string(filepath.Separator), "")
		compare(t, filepath.Join(dir, "expected_stderr.txt"), got)
	}
}

// runTestCase runs a package's tests (bork test) and compares the
// report, including the exit code.
func runTestCase(t *testing.T, dir string) {
	var out strings.Builder
	code, err := Test(dir, &out, TestOptions{})
	if err != nil {
		t.Fatalf("test build failed:\n%v", err)
	}
	got := strings.ReplaceAll(out.String(), dir+string(filepath.Separator), "")
	compare(t, filepath.Join(dir, "expected_test_output.txt"), fmt.Sprintf("%sexit code %d\n", got, code))
}

func runErrorCase(t *testing.T, dir string) {
	_, err := Emit(dir)
	if err == nil {
		// Errors in unsafe go code are found by the Go compiler.
		err = Build(dir, filepath.Join(t.TempDir(), "program"))
	}
	var de *DiagError
	if !errors.As(err, &de) {
		t.Fatalf("expected compile errors, got: %v", err)
	}
	// Report paths relative to the case directory.
	got := strings.ReplaceAll(de.Error(), dir+string(filepath.Separator), "") + "\n"
	compare(t, filepath.Join(dir, "expected_errors.txt"), got)
	if jsonPath := filepath.Join(dir, "expected_diagnostics.jsonl"); fileExists(jsonPath) {
		var out strings.Builder
		if err := de.Diags.WriteJSON(&out); err != nil {
			t.Fatal(err)
		}
		compare(t, jsonPath, strings.ReplaceAll(out.String(), dir+string(filepath.Separator), ""))
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func compare(t *testing.T, expectedPath, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(expectedPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("mismatch for %s\n--- want ---\n%s--- got ---\n%s", expectedPath, want, got)
	}
}

// TestExamples runs every program under examples/ and compares its
// output with testdata/examples/<name>.txt. A program runs in its own
// directory, with the arguments in its args.txt (one per line) if it
// has one, and the KEY=VALUE environment variables in its env.txt if it
// has one. Its output includes standard error, and how it exited if it
// failed.
func TestExamples(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "examples")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			exe, err := buildFixtureOutput(t, filepath.Join(root, name))
			if err != nil {
				t.Fatalf("build failed:\n%v", err)
			}
			var args []string
			if text, err := os.ReadFile(filepath.Join(root, name, "args.txt")); err == nil {
				args = strings.Fields(string(text))
			}
			cmd := exec.Command(exe, args...)
			cmd.Dir = filepath.Join(root, name)
			if text, err := os.ReadFile(filepath.Join(root, name, "env.txt")); err == nil {
				cmd.Env = os.Environ()
				for line := range strings.Lines(string(text)) {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					if !strings.Contains(line, "=") {
						t.Fatalf("env.txt line %q is not KEY=VALUE", line)
					}
					cmd.Env = append(cmd.Env, line)
				}
			}
			out, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				out = fmt.Appendf(out, "exit code %d\n", exitErr.ExitCode())
			} else if err != nil {
				t.Fatalf("program failed: %v\n%s", err, out)
			}
			compare(t, filepath.Join("..", "..", "testdata", "examples", name+".txt"), string(out))
			// An example's tests must pass.
			if _, info, err := Check(filepath.Join(root, name)); err == nil && len(info.Tests) > 0 {
				var report strings.Builder
				code, err := Test(filepath.Join(root, name), &report, TestOptions{})
				if err != nil || code != 0 {
					t.Fatalf("tests failed (%v):\n%s", err, report.String())
				}
			}
		})
	}
}
