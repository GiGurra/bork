package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
)

func TestCallerLocations(t *testing.T) {
	const helpers = `fn Location(): String { compilerCallerLocation() }
fn Forward(): String { _ = compilerCallerLocation(); Location() }
fn Plain(): String { Location() }
fn Equal(actual: Int, expected: Int) { _ = compilerCallerLocation(); assertEqual(actual, expected) }
fn Debug(value: Int): Int { _ = compilerCallerLocation(); dbg(value) }
fn Emit() uses io: String { println("real"); compilerCallerLocation() }
ambient Prefix: String
fn Needed() needs Prefix: String { s"$Prefix:${compilerCallerLocation()}" }
fn Generic[A](value: A): String { _ = value; compilerCallerLocation() }
fn Snapshot(value: String) uses io { _ = compilerCallerLocation(); assertSnapshot(value) }
fn Later(): () => String { _ = compilerCallerLocation(); () => Location() }
fn Crash(): Never { _ = compilerCallerLocation(); panic("fail") }
`
	cases := []struct {
		name, source, want string
		tests              bool
	}{
		{"direct, saved, forwarding, generic and ambient", `import helper "bork/caller"
fn main() {
  println(helper.Location())
  saved = helper.Location
  println(saved())
  println(helper.Forward())
  println(helper.Plain())
  println(helper.Debug(42))
  println(helper.Generic(42))
  with(helper.Prefix: "prefix") { println(helper.Needed()) }
  later = helper.Later()
  println(later())
}
`, "user.bork:3:11\nuser.bork:4:11\nuser.bork:6:11\nstd/caller.bork:3:22\nuser.bork:8:11 value = 42\n42\nuser.bork:9:11\nprefix:user.bork:10:43\nuser.bork:11:11\n", false},
		{"saved Never helper", `import helper "bork/caller"
fn main() { saved = helper.Crash; saved() }
`, "panic: fail", false},
		{"assertion", `import helper "bork/caller"
fn main() { helper.Equal(1, 2) }
`, "panic: user.bork:2:13: expected 2, got 1", false},
		{"mock passthrough and saved value", `import helper "bork/caller"
test "located mock" {
  mock helper.Emit() { helper.Emit() }
  assertEqual(helper.Emit(), "user.bork:4:15")
  saved = helper.Emit
  assertEqual(saved(), "user.bork:5:11")
}
`, "real\nreal\nok    located mock\n1 passed, 0 failed\n", true},
		{"snapshot in production", `import helper "bork/caller"
fn main() { helper.Snapshot("value") }
`, "panic: user.bork:2:13: assertSnapshot works only in tests (bork test)", false},
		{"snapshot", `import helper "bork/caller"
test "located snapshot" { helper.Snapshot("value") }
`, "user.bork:2:27: no snapshot", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var diags diag.List
			files := prelude.Parse(&diags)
			library := syntax.Parse("std/caller.bork", []byte(helpers), &diags)
			library.Package = "bork/caller"
			root := syntax.Parse("user.bork", []byte(tc.source), &diags)
			root.Package = "app"
			files = append(files, library, root)
			info := check.Program(files, "app", &diags, nil)
			check.CheckEffects(files, info, &diags)
			if diags.Len() > 0 {
				t.Fatalf("check: %v", diags.Sorted())
			}
			var source []byte
			var err error
			if tc.tests {
				source, err = Tests(files, info, false)
			} else {
				source, err = Package(files, info)
			}
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "main.go")
			if err = os.WriteFile(path, source, 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("go", "run", path)
			cmd.Dir = dir
			out, runErr := cmd.CombinedOutput()
			if tc.tests {
				if !strings.Contains(string(out), tc.want) {
					t.Fatalf("want %q, got %s (%v)", tc.want, out, runErr)
				}
			} else if strings.HasPrefix(tc.want, "panic:") {
				if runErr == nil || !strings.HasPrefix(string(out), tc.want) {
					t.Fatalf("want %q, got %s (%v)", tc.want, out, runErr)
				}
			} else if runErr != nil || string(out) != tc.want {
				t.Fatalf("want %q, got %s (%v)", tc.want, out, runErr)
			}
		})
	}
}

func TestCallerLocationRestrictions(t *testing.T) {
	for _, tc := range []struct{ source, pkg, want string }{
		{"fn main() { compilerCallerLocation() }", "app", "only available in prelude and standard-library helpers"},
		{"pred Good(x: Int) { compilerCallerLocation() == \"caller\" }", "bork/caller", "only available in prelude and standard-library helpers"},
		{"fn Helper(): String { compilerCallerLocation(1) }", "bork/caller", "takes no arguments"},
		{"fn Helper(): String { compilerCallerLocation[Int]() }", "bork/caller", "takes no type arguments"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			var diags diag.List
			files := prelude.Parse(&diags)
			file := syntax.Parse("caller.bork", []byte(tc.source), &diags)
			file.Package = tc.pkg
			check.Program(append(files, file), tc.pkg, &diags, nil)
			var found bool
			for _, d := range diags.Sorted() {
				if strings.Contains(d.Msg, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected %q, got %v", tc.want, diags.Sorted())
			}
		})
	}
}

func TestCallerLocationAutoProperty(t *testing.T) {
	var diags diag.List
	files := prelude.Parse(&diags)
	source := `pred Positive(x: Int) { x > 0 }
fn Located(x: Int): String {
  _ = compilerCallerLocation()
  trust Positive(x)
  "ok"
}
`
	root := syntax.Parse("std/caller.bork", []byte(source), &diags)
	root.Package = "bork/caller"
	files = append(files, root)
	info := check.Program(files, root.Package, &diags, nil)
	if diags.Len() > 0 {
		t.Fatalf("check: %v", diags.Sorted())
	}
	generated, err := Tests(files, info, true)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err = os.WriteFile(path, generated, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", path)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	// This deliberate bad trust must run as a property failure, rather than
	// fail to compile because the generated call omitted its hidden argument.
	if !strings.Contains(string(out), "trusted fact does not hold: Positive(x)") || !strings.Contains(string(out), "0 passed, 1 failed") {
		t.Fatalf("auto property did not run: %s", out)
	}
}
