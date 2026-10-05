package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
)

func TestUnitMigrationFixes(t *testing.T) {
	t.Parallel()
	source := `type Error = { message: String }
type Result = Unit | Error
type Work = () => Unit
fn finish(work: Work): Result { work(); Ok }
fn succeed(): Unit { Ok }
fn main() {
  // Unit stays in comments and strings.
  println("Unit")
  results: List[Unit | Error] = [finish(() => Ok)]
  results.forEach(result => match (result) {
    _: Unit => {}
    error: Error => println(error.message)
  })
  succeed()
}
`
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	_, info, err := Check(path)
	if err != nil {
		t.Fatal(err)
	}
	warnings := check.MigrationWarnings(info).Sorted()
	if len(warnings) != 5 {
		t.Fatalf("expected five type migration warnings, got %+v", warnings)
	}
	for i := len(warnings) - 1; i >= 0; i-- {
		warning := warnings[i]
		if warning.Code != "migration.unit" || warning.Severity != "warning" || len(warning.Fixes) != 1 || len(warning.Fixes[0].Edits) != 1 {
			t.Fatalf("unexpected migration warning: %+v", warning)
		}
		edit := warning.Fixes[0].Edits[0]
		offset := 0
		for _, line := range strings.SplitAfter(source, "\n")[:edit.Start.Line-1] {
			offset += len(line)
		}
		offset += edit.Start.Col - 1
		if source[offset:offset+4] != "Unit" || edit.End.Col != edit.Start.Col+4 || edit.End.Line != edit.Start.Line || edit.Replacement != "Ok" {
			t.Fatalf("wrong migration range: %+v", edit)
		}
		source = source[:offset] + edit.Replacement + source[offset+4:]
	}
	if !strings.Contains(source, "// Unit stays") || !strings.Contains(source, `println("Unit")`) {
		t.Fatal("migration changed a comment or string")
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	_, info, err = Check(path)
	if err != nil || check.MigrationWarnings(info).Len() != 0 {
		t.Fatalf("migration did not produce a warning-free program: %v", err)
	}
}

func TestUnitVariantIsNotDeprecated(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "main.bork")
	source := "type Result = sealed { Unit { value: Int } }\nfn main() { println(Result.Unit { value: 1 }) }\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	_, info, err := Check(path)
	if err != nil {
		t.Fatal(err)
	}
	if warnings := check.MigrationWarnings(info); warnings.Len() != 0 {
		t.Fatalf("variant was mistaken for deprecated builtin: %s", warnings)
	}
}

func TestDescribeOk(t *testing.T) {
	t.Parallel()
	for _, spelling := range []string{"Ok", "Unit"} {
		source := "fn success(): " + spelling + " { Ok }\nfn main() { success() }\n"
		if result := describeAt(t, source, "success() }", ""); result.typ != "() => Ok" {
			t.Fatalf("noncanonical type spelling: %+v", result)
		}
		if result := describeAt(t, source, "Ok }", ""); result.typ != "Ok" {
			t.Fatalf("wrong success expression type: %+v", result)
		}
	}
}
