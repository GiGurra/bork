package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSnapshotUpdate runs bork test --update on a copy of the snapshots
// case: it writes the missing and different snapshots, leaves the
// others alone, and then the tests pass.
func TestSnapshotUpdate(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join("..", "..", "testdata", "cases", "snapshots")
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	snaps := filepath.Join(dir, "snapshots")
	unchanged := filepath.Join(snaps, "renders_an_invoice.snap")
	before, err := os.Stat(unchanged)
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	code, err := Test(dir, &out, TestOptions{Update: true})
	if err != nil || code != 0 {
		t.Fatalf("bork test --update failed (code %d, %v):\n%s", code, err, out.String())
	}
	got := strings.ReplaceAll(out.String(), dir+string(filepath.Separator), "")
	for _, want := range []string{
		"ok    a changed value\n      wrote snapshots/a_changed_value.snap\n",
		"ok    a snapshot not yet taken\n      wrote snapshots/a_snapshot_not_yet_taken.snap\n",
		"wrote snapshots/only_the_lines_near_a_change_are_shown.snap\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "wrote "); n != 3 {
		t.Errorf("wrote %d snapshots, want 3:\n%s", n, got)
	}
	text, err := os.ReadFile(filepath.Join(snaps, "a_snapshot_not_yet_taken.snap"))
	if err != nil || string(text) != "42\n" {
		t.Errorf("new snapshot = %q, %v; want %q", text, err, "42\n")
	}
	if after, err := os.Stat(unchanged); err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("an unchanged snapshot was rewritten")
	}

	out.Reset()
	code, err = Test(dir, &out, TestOptions{})
	if err != nil || code != 0 {
		t.Fatalf("tests fail after --update (code %d, %v):\n%s", code, err, out.String())
	}
}

// TestSnapshotOutsideTests checks that assertSnapshot, reached in a
// program rather than a test, panics.
func TestSnapshotOutsideTests(t *testing.T) {
	dir := t.TempDir()
	src := "fn main() {\n  assertSnapshot(1)\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "program")
	if err := Build(dir, exe); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "main.bork:2:3: assertSnapshot works only in tests (bork test)") {
		t.Errorf("got %v:\n%s", err, out)
	}
}
