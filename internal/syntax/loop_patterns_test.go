package syntax

import (
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestLoopIterationPatterns(t *testing.T) {
	for _, source := range []string{
		"for (key, value) in entries {}",
		"for ((key, value) in entries) {}",
		"for ((first, _), (second,)) in entries {}",
		"for (_, _) in entries {}",
		"for (name) in entries {}",
	} {
		t.Run(source, func(t *testing.T) {
			d := &diag.List{}
			f := Parse("patterns.bork", []byte("fn f(){"+source+"}"), d)
			if d.Len() != 0 {
				t.Fatal(d.Error())
			}
			loop := f.Funcs[0].Body.Tail.(*For)
			if loop.Pattern == nil || len(f.IterationOperators) != 1 {
				t.Fatal("iteration pattern metadata missing")
			}
		})
	}
	for _, source := range []string{"for key, value in entries {}", "for (key, value in entries) {}"} {
		d := &diag.List{}
		Parse("patterns.bork", []byte("fn f(){"+source+"}"), d)
		if d.Len() == 0 {
			t.Fatalf("invalid iteration pattern accepted: %s", source)
		}
	}
}
