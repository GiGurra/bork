package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
)

func TestLintDiagnosticsAndFixes(t *testing.T) {
	source := `type unused = { value: Int }
pred positive(n: Int) { n > 0 }
fn spare(n: Int) { unusedLocal = n }
fn constrained(n: Int where positive): Bool { positive(n) }
fn main() uses io {
 value = true
 _ = ((value)) && (true)
}
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
	codes := map[string]int{}
	for _, warning := range warnings {
		if warning.Severity != "warning" {
			t.Fatalf("severity: %+v", warning)
		}
		codes[warning.Code]++
	}
	for _, code := range []string{"lint.unused-binding", "lint.unused-declaration", "lint.needless-effects", "lint.redundant-check", "lint.simplify"} {
		if codes[code] == 0 {
			t.Errorf("missing %s: %+v", code, warnings)
		}
	}
	// Each automatic fix must preserve a valid program independently.
	for _, warning := range warnings {
		for _, fix := range warning.Fixes {
			fixed := applyLintEdits(t, source, fix.Edits)
			if err := os.WriteFile(path, []byte(fixed), 0644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Check(path); err != nil {
				t.Errorf("invalid fix %s: %v\n%s", warning.Code, err, fixed)
			}
		}
	}
}

func applyLintEdits(t *testing.T, source string, edits []diag.TextEdit) string {
	t.Helper()
	for i := len(edits) - 1; i >= 0; i-- {
		edit := edits[i]
		offset := func(pos diag.Pos) int {
			lines := strings.SplitAfter(source, "\n")
			n := 0
			for j := 0; j < pos.Line-1; j++ {
				n += len(lines[j])
			}
			return n + pos.Col - 1
		}
		lo, hi := offset(edit.Start), offset(edit.End)
		source = source[:lo] + edit.Replacement + source[hi:]
	}
	return source
}

func TestLintSuppressionAndEffects(t *testing.T) {
	source := `fn main() {
 // lint:ignore lint.unused-binding intentional
 ignored = 42
 unused = 43
 println("output")
}
fn callback(f: () uses io => Ok) uses io { f() }
fn UnusedParameter(ignored: Int) {}
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
	codes := map[string]int{}
	for _, w := range warnings {
		codes[w.Code]++
		if w.Code == "lint.needless-effects" || w.Code == "lint.unused-binding" && w.Pos.Line == 3 {
			t.Fatalf("false positive: %+v", w)
		}
	}
	if codes["lint.unused-binding"] != 1 || codes["lint.unused-parameter"] != 1 {
		t.Fatalf("warnings: %+v", warnings)
	}
}

func TestLintPatternBindings(t *testing.T) {
	source := `fn main() {
 _ = match ([1, 2]) { [first, ...rest] => 0, _ => 1 }
}
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
	if len(warnings) != 2 {
		t.Fatalf("patterns: %+v", warnings)
	}
	for _, w := range warnings {
		if w.Code != "lint.unused-binding" {
			t.Fatalf("pattern: %+v", w)
		}
	}
}

func TestLintRecursiveDeclarationsAndComparisonFix(t *testing.T) {
	source := `fn spare(n: Int): Int { if (n > 0) { spare(n - 1) } else { 0 } }
fn F(n: Int): Bool { (n > 0) && true }
fn main() {}
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
	var unused, simplify bool
	for _, w := range warnings {
		if w.Code == "lint.unused-declaration" && w.Pos.Line == 1 {
			unused = true
		}
		if w.Code == "lint.simplify" {
			simplify = true
			if len(w.Fixes) != 1 {
				t.Fatal("missing comparison fix")
			}
			fixed := applyLintEdits(t, source, w.Fixes[0].Edits)
			if err := os.WriteFile(path, []byte(fixed), 0644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Check(path); err != nil {
				t.Fatalf("invalid comparison fix: %v\n%s", err, fixed)
			}
		}
	}
	if !unused || !simplify {
		t.Fatalf("regressions: %+v", warnings)
	}
}

func BenchmarkLintManyExpressions(b *testing.B) {
	source := "fn main() {\n" + strings.Repeat(" _ = true && true\n", 2000) + "}\n"
	dir := b.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		b.Fatal(err)
	}
	files, info, err := Check(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = check.LintWarnings(files, info)
	}
}

func TestLintNamedInterpolationFixPreservesCalls(t *testing.T) {
	source := `type Builder = {}
fn Tag(parts: StaticParts): Builder { Builder{} }
fn (b: Builder) Finish(): Bool { true }
fn main() { _ = Tag"abc" && true }
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
	found := false
	for _, w := range warnings {
		if w.Code != "lint.simplify" {
			continue
		}
		found = true
		fixed := applyLintEdits(t, source, w.Fixes[0].Edits)
		if !strings.Contains(fixed, `(Tag"abc")`) {
			t.Fatalf("interpolator dropped: %s", fixed)
		}
		if err := os.WriteFile(path, []byte(fixed), 0644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Check(path); err != nil {
			t.Fatalf("invalid interpolation fix: %v\n%s", err, fixed)
		}
	}
	if !found {
		t.Fatal("missing interpolation simplification")
	}
}
