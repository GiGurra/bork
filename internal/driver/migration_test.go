package driver

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
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

func TestComptimeComprehensionMigrationFixes(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"

class Info[T] { fn info(value: T): (List[String], Bool, Int) }

type Meta = { names: List[String], count: Int }

derive instance info[T]: Info[T] {
  fn info(value: T): (List[String], Bool, Int) {
    names = [comptime for (field in shape.fields[T]()) comptime if (!field.computed) field.name]
    empty = ![comptime for (field in shape.fields[T]()) comptime if (field.name == "zzz")
      true].isEmpty()
    multi = [comptime for (
      field in shape.fields[T]()
    ) comptime if (!field.computed)
      field.name]
    kept = [comptime for (field in shape.fields[T]()) // every field
      field.name]
    meta = Meta {
      names: [comptime for ((i, field) in shape.fields[T]().indexed())
        field.name + toString(i)],
      count: [comptime for (field in shape.fields[T]()) 1].length(),
    }
    (names.concat(meta.names).concat(multi).concat(kept), empty, meta.count)
  }
}

type Row = { name: String, age: Int } derive (Info)

fn main() { println(info(Row { name: "Ada", age: 37 })) }
`
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	_, info, err := Check(path)
	if err != nil {
		t.Fatal(err)
	}
	var edits []diag.TextEdit
	unfixed := 0
	for _, warning := range check.MigrationWarnings(info).Sorted() {
		if warning.Code != "migration.comptime-comprehension" || warning.Severity != "warning" || len(warning.Fixes) > 1 {
			t.Fatalf("unexpected migration warning: %+v", warning)
		}
		if len(warning.Fixes) == 0 {
			unfixed++ // a comment inside the list would be lost
			continue
		}
		edits = append(edits, warning.Fixes[0].Edits...)
	}
	if len(edits) == 0 || unfixed != 1 {
		t.Fatalf("expected fixes and one unfixed list, got %d edits and %d unfixed", len(edits), unfixed)
	}
	offset := func(p diag.Pos) int {
		n := 0
		for _, line := range strings.SplitAfter(source, "\n")[:p.Line-1] {
			n += len(line)
		}
		return n + p.Col - 1
	}
	sort.SliceStable(edits, func(i, j int) bool { return offset(edits[i].Start) > offset(edits[j].Start) })
	for _, edit := range edits {
		source = source[:offset(edit.Start)] + edit.Replacement + source[offset(edit.End):]
	}
	for _, want := range []string{
		"names = comptime for { field in shape.fields[T](); if !field.computed } yield field.name",
		"empty = !(comptime for { field in shape.fields[T](); if field.name == \"zzz\" } yield true).isEmpty()",
		"names: comptime for { (i, field) in shape.fields[T]().indexed() } yield field.name + toString(i),",
		"count: (comptime for { field in shape.fields[T]() } yield 1).length(),",
		"multi = comptime for {\n      field in shape.fields[T](); if !field.computed } yield field.name\n",
		"kept = [comptime for (field in shape.fields[T]()) // every field\n      field.name]",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("missing %q in\n%s", want, source)
		}
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	_, info, err = Check(path)
	if err != nil || check.MigrationWarnings(info).Len() != 1 {
		t.Fatalf("migration left more than the commented list: %v\n%s", err, source)
	}
}
