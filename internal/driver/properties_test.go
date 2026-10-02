package driver

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestAutoProperties runs the properties_auto case with
// --auto-properties, and compares the report with
// expected_auto_test_output.txt.
func TestAutoProperties(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "cases", "properties_auto")
	var out strings.Builder
	code, err := Test(dir, &out, TestOptions{AutoProperties: true})
	if err != nil {
		t.Fatalf("test build failed:\n%v", err)
	}
	got := strings.ReplaceAll(out.String(), dir+string(filepath.Separator), "")
	compare(t, filepath.Join(dir, "expected_auto_test_output.txt"), fmt.Sprintf("%sexit code %d\n", got, code))
}

var seedLine = regexp.MustCompile(`case \d+, seed (-?\d+)`)

// TestPropertySeed checks that a failure's seed reproduces it, and that
// --cases sets how many cases run.
func TestPropertySeed(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "cases", "properties")
	var first strings.Builder
	if _, err := Test(dir, &first, TestOptions{}); err != nil {
		t.Fatal(err)
	}
	report := first.String()
	start := strings.Index(report, "FAIL  a failure is shrunk")
	if start < 0 {
		t.Fatalf("no failure to reproduce:\n%s", report)
	}
	m := seedLine.FindStringSubmatch(report[start:])
	if m == nil {
		t.Fatalf("no seed in:\n%s", report[start:])
	}
	var seed int64
	if _, err := fmt.Sscan(m[1], &seed); err != nil {
		t.Fatal(err)
	}
	var again strings.Builder
	if _, err := Test(dir, &again, TestOptions{Seed: seed}); err != nil {
		t.Fatal(err)
	}
	failure := report[start:]
	failure = failure[:strings.Index(failure, m[0])]
	if !strings.Contains(again.String(), failure) {
		t.Errorf("--seed %d does not reproduce\n%s\ngot:\n%s", seed, failure, again.String())
	}

	// With --cases, the command to reproduce a failure includes it.
	var more strings.Builder
	if _, err := Test(dir, &more, TestOptions{Cases: 300}); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`\(bork test --seed -?\d+ --cases 300\)`).MatchString(more.String()) {
		t.Errorf("--cases is not in the command:\n%s", more.String())
	}

	// With 1 case of size 1, the list has at most one element.
	var one strings.Builder
	if _, err := Test(dir, &one, TestOptions{Cases: 1}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(one.String(), "ok    a failure is shrunk\n") {
		t.Errorf("--cases 1 still fails:\n%s", one.String())
	}
}
