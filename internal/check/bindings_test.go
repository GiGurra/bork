package check

import (
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
)

func TestLocalBindingRules(t *testing.T) {
	for _, tc := range []struct {
		name, source, code string
		count              int
	}{
		{"prelude name", `fn main() { range = 1; println(range) }`, "", 0},
		{"shadowed prelude call", `fn main() { range = 1; _ = range(0, 2) }`, "binding.not-callable", 1},
		{"different types", `fn main() { x = 1; x = s"$x"; println(x) }`, "", 0},
		{"parameter", `fn f(x: Int): String { x = s"$x"; x }`, "", 0},
		{"lambda parameter", `fn main() { f = (x: Int) => { x = s"$x"; x }; println(f(1)) }`, "", 0},
		{"capture", `fn main() { x = 1; old = () => x; x = 2; println(old(), x) }`, "", 0},
		{"nested block", `fn main() { x = 1; { x = 2; println(x) }; println(x) }`, "binding.shadow", 1},
		{"nested lambda", `fn main() { x = 1; f = () => { x = 2; x }; println(f(), x) }`, "binding.shadow", 1},
		{"lambda parameter shadow", `fn main() { x = 1; f = (x: Int) => x; println(f(x)) }`, "binding.shadow", 1},
		{"sibling blocks", `fn main() { { x = 1; println(x) }; { x = "two"; println(x) } }`, "", 0},
		{"overwritten unread", `fn main() { x = 1; x = 2; println(x) }`, "binding.unused", 1},
		{"latest unread", `fn main() { x = 1; x = x + 1 }`, "binding.unused", 1},
		{"multiple unread", `fn main() { x = 1; x = "two"; x = true }`, "binding.unused", 3},
		{"unused parameters", `fn f(x: Int) { _ = (y: Int) => 1 }`, "", 0},
		{"function protected", `fn f(): Int { 1 }; fn main() { f = 1; println(f) }`, "binding.shadow", 1},
		{"package protected", `Value = 1; fn main() { Value = 2; println(Value) }`, "binding.shadow", 1},
		{"patterns", `fn main() { _ = match ([1]) { [first, ...rest] => 0; _ => 1 } }`, "binding.unused", 2},
		{"typed pattern", `fn f(x: Int | String): Int { match (x) { n: Int => 1; String => 2 } }`, "binding.unused", 1},
		{"guarded pattern", `pred positive(x: Int) { x > 0 }; fn f(x: Int): Int { match (x) { n: Int where positive => 1; _ => 2 } }`, "binding.unused", 1},
		{"typed discards", `pred positive(x: Int) { x > 0 }; fn main() { _: List[Int] = []; _ = match (1) { _: Int where positive => 1; _ => 2 }; for (_ in [1]) {} }`, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &diag.List{}
			files := prelude.Parse(d)
			file := syntax.Parse("main.bork", []byte(tc.source), d)
			file.Package = "example.com/bindings"
			files = append(files, file)
			Program(files, file.Package, d, nil)
			if d.Len() != tc.count {
				t.Fatalf("got %d diagnostics, want %d: %s", d.Len(), tc.count, d.Error())
			}
			for _, diagnostic := range d.Sorted() {
				if diagnostic.Code != tc.code {
					t.Fatalf("wrong diagnostic: %+v", diagnostic)
				}
			}
		})
	}
}
