package driver

import (
	"errors"
	"github.com/GiGurra/bork/internal/diag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiteralHeadsRejected(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"nominal invariant", "type Range[T] = { label: T, lo: Int = 1, hi: Int = 2 } where ordered\npred ordered[T](r: Range[T]) { r.lo <= r.hi }\nfn main() { println(Range[String] { label: \"bad\", hi: 0 }) }", "is false"},
		{"sibling fact", "pred atLeast(x: Int, lo: Int) { x>=lo }\ntype Range[T] = { label: T, lo: Int, hi: Int where atLeast(lo) }\nfn main() { println(Range[String] { label: \"bad\", lo: 2, hi: 1 }) }", "is false"},

		{"outer argument obligation", "pred atLeast(value: Int, minimum: Int) { value >= minimum }\nfn make(minimum: Int, value: Int): Option[Int] { Option[Int where atLeast(minimum)].Some { value: value } }\nfn main() { println(make(2,1)) }", "not proven"},

		{"outer argument field collision", "pred atLeast(value: Int, minimum: Int) { value >= minimum }\ntype Box[T] = { value: T, minimum: Int }\nfn make(minimum: Int): Box[Int] { Box[Int where atLeast(minimum)] { value: 1, minimum: 0 } }\nfn main() { println(make(2)) }", "not proven"},

		{"arity", "type Box[T] = { value: T }\nfn main() { println(Box[Int,String] { value: 1 }) }", "needs 1 type argument"},
		{"plain type arguments", "type Box = { value: Int }\nfn main() { println(Box[Int] { value: 1 }) }", "does not take type arguments"},
		{"alias arguments", "type Box[T] = { value: T }\ntype IntBox = Box[Int]\nfn main() { println(IntBox[Int] { value: 1 }) }", "does not take type arguments"},
		{"kind", "type Box[T] = { value: T }\nfn main() { println(Box[Ok] { value: 1 }) }", "is not allowed"},
		{"result context", "fn main() { x: Option[Int] = Option[String].None; println(x) }", "found Option[String]"},
		{"field context", "fn main() { println(Option[Int].Some { value: \"x\" }) }", "must be Int"},
		{"missing braces", "fn main() { println(Option[Int].Some) }", "has fields"},
		{"record variant", "type Box[T] = { value: T }\nfn main() { println(Box[Int].Missing {}) }", "not a sealed type"},
		{"sealed owner", "fn main() { println(Option[Int] {}) }", "sealed type"},
		{"missing variant", "fn main() { println(Option[Int].Missing) }", "has no variant"},
		{"bare head", "fn main() { println(Option[Int]) }", "expected a call"},
		{"variant arguments", "fn main() { println(Option.Some[Int] { value: 1 }) }", "expected a call"},
		{"specialized method owner", "fn main() { println(Option[Int].isSome) }", "has no variant"},
		{"argument obligation", "pred positive(x: Int) { x > 0 }\nfn main() { println(Option[Int where positive].Some { value: 0 }) }", "is false"},
		{"nested unsupported facts", "pred positive(x: Int) { x > 0 }\ntype Box[T] = { values: List[T] }\nfn main() { println(Box[Int where positive] { values: [1] }) }", "would not be checked"},
		{"unvalidated facts", "pred positive(x: Int) { x > 0 }\nfn make(x: Int): Option[Int] { Option[Int where positive].Some { value: x } }\nfn main() { println(make(1)) }", "not proven"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.bork")
			if err := os.WriteFile(path, []byte(tc.source+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestLiteralHeadVisibilityAndFixes(t *testing.T) {
	for _, tc := range []struct {
		name, source, want string
		fixes              int
		input              bool
	}{
		{"imported heads", "fn main() { println(api.Box[Int] { value: 1 }); println(api.State[Int].Ready) }", "", -1, false},
		{"private record", "fn main() { println(api.Config[Int] { value: 1 }) }", "controls its construction", -1, false},
		{"private variant", "fn main() { println(api.State[Int].hidden) }", "not exported", -1, false},
		{"alias alternatives", "fn main() { x: api.Ints | api.Strings = .{ values: [] }; println(x) }", "several expected constructors", 2, false},
		{"private alternative omitted", "fn main() { x: api.Config[Int] | api.Box[Int] = .{ value: 1 }; println(x) }", "several expected constructors", 1, false},
		{"inaccessible owner", "fn main() { api.Choose(.{ values: [] }) }", "several expected constructors", 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"bork.mod":  "module example.com/heads\n",
				"main.bork": "import \"example.com/heads/api\"\n" + tc.source + "\n",
				"api/api.bork": `type Box[T] = { value: T }
type Config[T] = private { value: T }
type State[T] = sealed { Ready, hidden }
type secret[T] = { values: List[T] }
type Ints = secret[Int]
type Strings = secret[String]
pred valid(x: secret[Int]) { true }
// Checked sorts before Ints; fixes must skip this constrained alias.
type Checked = secret[Int] where valid
type hidden[T] = { values: List[T] }
fn Choose(value: hidden[Int] | hidden[String]) uses io { println(value) }
`,
			}
			for rel, src := range files {
				p := filepath.Join(dir, rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(dir, "main.bork")
			_, _, err := Check(path)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			if tc.fixes < 0 {
				return
			}
			var de *DiagError
			if !errors.As(err, &de) {
				t.Fatal(err)
			}
			var fixes []diag.Fix
			for _, d := range de.Diags.Sorted() {
				if d.Code == "type.context_ambiguous" {
					fixes = append(fixes, d.Fixes...)
				}
			}
			if len(fixes) != tc.fixes {
				t.Fatalf("want %d alternatives, got %+v", tc.fixes, fixes)
			}
			for _, fix := range fixes {
				if fix.RequiresInput != tc.input {
					t.Fatalf("unexpected requires_input %+v", fix)
				}
				if tc.input {
					continue
				}
				source := files["main.bork"]
				for _, edit := range fix.Edits {
					offset := func(p diag.Pos) int {
						lines := strings.SplitAfter(source, "\n")
						n := p.Col - 1
						for _, line := range lines[:p.Line-1] {
							n += len(line)
						}
						return n
					}
					source = source[:offset(edit.Start)] + edit.Replacement + source[offset(edit.End):]
				}
				if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, _, err := Check(path); err != nil {
					t.Fatalf("fix fails: %+v\n%s\n%v", fix, source, err)
				}
			}
		})
	}
}
