package check

import (
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
	"testing"
)

func TestTupleBindingRules(t *testing.T) {
	for _, tc := range []struct {
		name, source, code string
		count              int
	}{
		{"rebind", `fn main() { (a, b) = (1, 2); println(a, b); (a, b) = ("three", true); println(a, b) }`, "", 0},
		{"initializer identity", `fn main() { (a, b) = (1, 2); (a, b) = (b, a); println(a, b) }`, "", 0},
		{"nested patterns", `fn main() { ((a, _), b) = ((1, 2), 3); println(a, b); ((a, _), b) = ((4, 5), 6); println(a, b) }`, "", 0},
		{"nested scope", `fn main() { (a, _) = (1, 2); { (a, _) = (3, 4); println(a) }; println(a) }`, "binding.shadow", 1},
		{"unread replacement", `fn main() { (a, _) = (1, 2); (a, _) = (3, 4); println(a) }`, "binding.unused", 1},
		{"unread member", `fn main() { (a, b) = (1, 2); println(a) }`, "binding.unused", 1},
		{"context variant is refutable", `type Choice = sealed { On, Off }; fn main() { (.On,) = (Choice.On,) }`, "type.error", 1},
		{"discard", `fn main() { (_, (_, _)) = (1, (2, 3)) }`, "", 0},
		{"typed tuple discard match", `fn f(value: (Int,) | String): Int { match (value) { _: (Int,) => 1; String => 0 } }`, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &diag.List{}
			files := prelude.Parse(d)
			file := syntax.Parse("main.bork", []byte(tc.source), d)
			file.Package = "example.com/tuplebindings"
			Program(append(files, file), file.Package, d, nil)
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
