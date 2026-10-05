package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatternTestCertaintyWarnings(t *testing.T) {
	source := `import "bork/test"
pred positive(n: Int) { n > 0 }
pred atMost(n: Int, limit: Int) { n <= limit }
fn Certain(n: Int where positive): Bool { n is Int where positive }
fn Partial(n: Int where positive): Bool { n is Int where positive and atMost(100) }
fn Shape(n: Int): Bool { n is Int }
fn Disjoint(n: Int): Bool { n is String }
fn AssertCertain(n: Int where positive): Int { test.AssertIs[Int where positive](n) }
fn AssertPartial(n: Int where positive): Int { test.AssertIs[Int where positive and atMost(100)](n) }
fn AssertDisjoint(n: Int): String { test.AssertIs[String](n) }
fn LiteralTrue(): Bool { 3 is 3 }
fn LiteralFalse(): Bool { 3 is 4 }
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	warnings, err := Lint(path)
	if err != nil {
		t.Fatal(err)
	}
	found := map[int]string{}
	for _, warning := range warnings {
		if warning.Code == "lint.pattern-always-true" || warning.Code == "lint.pattern-always-false" {
			found[warning.Pos.Line] = warning.Code
		}
	}
	for line, want := range map[int]string{4: "lint.pattern-always-true", 6: "lint.pattern-always-true", 7: "lint.pattern-always-false", 8: "lint.pattern-always-true", 10: "lint.pattern-always-false", 11: "lint.pattern-always-true", 12: "lint.pattern-always-false"} {
		if found[line] != want {
			t.Errorf("line %d: got %q, want %q; warnings: %+v", line, found[line], want, warnings)
		}
	}
	for _, line := range []int{5, 9} {
		if found[line] != "" {
			t.Errorf("partial test warned on line %d", line)
		}
	}
}

func TestPatternAssertionsParallelAttribution(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "cases", "pattern_tests_tests")
	want, err := os.ReadFile(filepath.Join(dir, "expected_test_output.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	code, err := Test(dir, &out, TestOptions{Parallel: 4})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(out.String(), dir+string(filepath.Separator), "") + fmt.Sprintf("exit code %d\n", code)
	if got != string(want) {
		t.Fatalf("parallel results differ:\n%s\nwant:\n%s", got, want)
	}
}

func TestPatternAssertionDescribeFactsAndCallable(t *testing.T) {
	source := `import checks "bork/test"
pred positive(n: Int) { n > 0 }
fn Example(input: Int | String): Int {
  value = checks.AssertIs[Int where positive](input)
  value
}
`
	result := describeAt(t, source, "value\n", "positive")
	if result.typ != "Int" || !result.proven || result.facts == 0 {
		t.Fatalf("returned facts: %+v", result)
	}
	result = describeAt(t, source, "checks.AssertIs[", "")
	if !result.defined || result.typ != "(Int | String) => Int" {
		t.Fatalf("instantiated callable: %+v", result)
	}
}
