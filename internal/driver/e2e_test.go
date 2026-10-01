package driver

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite expected files with actual results")

// TestCases runs every case under testdata/cases. A case is a directory
// of .bork files plus either:
//
//   - expected_output.txt: the program must build, run, and print exactly this.
//   - expected_errors.txt: compilation must fail with exactly these diagnostics.
func TestCases(t *testing.T) {
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
			runOutputCase(t, dir)
		})
	}
}

func runOutputCase(t *testing.T, dir string) {
	exe := filepath.Join(t.TempDir(), "program")
	if err := Build(dir, exe); err != nil {
		t.Fatalf("build failed:\n%v", err)
	}
	out, err := exec.Command(exe).Output()
	if err != nil {
		t.Fatalf("program failed: %v\n%s", err, out)
	}
	compare(t, filepath.Join(dir, "expected_output.txt"), string(out))
}

func runErrorCase(t *testing.T, dir string) {
	_, err := Emit(dir)
	var de *DiagError
	if !errors.As(err, &de) {
		t.Fatalf("expected compile errors, got: %v", err)
	}
	// Report paths relative to the case directory.
	got := strings.ReplaceAll(de.Error(), dir+string(filepath.Separator), "") + "\n"
	compare(t, filepath.Join(dir, "expected_errors.txt"), got)
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
