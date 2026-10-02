package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSnapshotName(t *testing.T) {
	taken := map[string]bool{}
	for _, c := range []struct{ test, want string }{
		{"renders an invoice", "renders_an_invoice"},
		{"Names: with punctuation!", "Names_with_punctuation"},
		{"a b", "a_b"},
		{"a-b", "a_b_2"},
		{"A B", "A_B_3"}, // a_b.snap and A_B.snap are one file on some file systems
		{"a b 2", "a_b_2_2"},
		{"!!", "test"},
		{"é", "test_2"},
	} {
		if got := snapshotName(c.test, taken); got != c.want {
			t.Errorf("snapshotName(%q) = %q, want %q", c.test, got, c.want)
		}
	}
}

// TestSnapshotDiff runs _snapshotDiff, from the snapshot runtime, on
// edge cases.
func TestSnapshotDiff(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a Go test")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":       "module snaprt\n\ngo 1.26\n",
		"test.go":      testRuntime,
		"snapshot.go":  snapshotRuntime,
		"snap_test.go": snapshotRuntimeTest,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("snapshot runtime test failed: %v\n%s", err, out)
	}
}

const snapshotRuntimeTest = `package main

import (
	"strings"
	"testing"
)

func TestDiff(t *testing.T) {
	lines := func(from, to int) string {
		var out []string
		for i := from; i <= to; i++ {
			out = append(out, strings.Repeat("x", i))
		}
		return strings.Join(out, "\n")
	}
	for _, c := range []struct{ old, new, want string }{
		{"", "a", "-\n+ a"},
		{"a", "", "- a\n+"},
		{"a\nb", "c\nb", "- a\n+ c\n  b"},
		{"a\nb", "a\nc", "  a\n- b\n+ c"},
		{"a", "a\n", "  a\n+"},
		// One line far from the change is shown; two are left out.
		{lines(1, 4), lines(1, 3) + "\ny", "  x\n  xx\n  xxx\n- xxxx\n+ y"},
		{lines(1, 5), lines(1, 4) + "\ny", "  ...\n  xxx\n  xxxx\n- xxxxx\n+ y"},
		{"y\n" + lines(1, 7), lines(1, 7), "- y\n  x\n  xx\n  ..."},
	} {
		if got := _snapshotDiff(c.old, c.new); got != c.want {
			t.Errorf("_snapshotDiff(%q, %q) =\n%s\nwant\n%s", c.old, c.new, got, c.want)
		}
	}
}

// A long snapshot with one changed line is diffed without comparing
// every pair of lines.
func TestDiffLarge(t *testing.T) {
	old := strings.Repeat("same\n", 100000) + "old"
	got := _snapshotDiff(old, strings.Repeat("same\n", 100000)+"new")
	if want := "  ...\n  same\n  same\n- old\n+ new"; got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	// A large middle that differs is shown whole rather than diffed.
	got = _snapshotDiff(strings.Repeat("a\n", 5000)+"a", strings.Repeat("b\n", 5000)+"b")
	if strings.Count(got, "- a") != 5001 || strings.Count(got, "+ b") != 5001 {
		t.Errorf("large middle: got %d lines", strings.Count(got, "\n")+1)
	}
}
`
