package syntax

import (
	"os"
	"reflect"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestParseEmbeddedIsolation(t *testing.T) {
	src := []byte("// a comment\nfn answer(): Int { 1 }\n")
	want := Parse("embedded/isolation.bork", src, &diag.List{})
	first := ParseEmbedded("embedded/isolation.bork", src, &diag.List{})
	if !reflect.DeepEqual(first, want) {
		t.Fatal("cached parse differs from ordinary parse")
	}
	first.Funcs[0].Name = "changed"
	first.Funcs[0].Body.Tail.(*IntLit).Text = "99"
	first.Comments[0].Text = "changed comment"
	second := ParseEmbedded("embedded/isolation.bork", src, &diag.List{})
	if !reflect.DeepEqual(second, want) {
		t.Fatal("cached parse shared mutable syntax or comments")
	}
	changed := []byte("// another comment\nfn answer(): Int { 2 }\n")
	got := ParseEmbedded("embedded/isolation.bork", changed, &diag.List{})
	fresh := Parse("embedded/isolation.bork", changed, &diag.List{})
	if !reflect.DeepEqual(got, fresh) {
		t.Fatal("content change reused stale tokens")
	}
}

func TestParseEmbeddedErrors(t *testing.T) {
	for _, src := range []string{"fn main() { @ }", "fn main( {", "fn main() { \"unterminated }"} {
		t.Run(src, func(t *testing.T) {
			want := &diag.List{}
			Parse("embedded/errors.bork", []byte(src), want)
			if want.Len() == 0 {
				t.Fatal("fixture must fail")
			}
			for range 2 {
				got := &diag.List{}
				ParseEmbedded("embedded/errors.bork", []byte(src), got)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("diagnostics = %v, want %v", got, want)
				}
			}
		})
	}
}

func TestParseEmbeddedParallel(t *testing.T) {
	src := []byte("fn answer(): Int { 42 }\n")
	for range 16 {
		t.Run("parse", func(t *testing.T) {
			t.Parallel()
			diags := &diag.List{}
			file := ParseEmbedded("embedded/parallel.bork", src, diags)
			if diags.Len() != 0 || file.Funcs[0].Name != "answer" {
				t.Fatalf("parse: %v, %v", file, diags)
			}
			// Mutating a returned tree must not race with another parse.
			file.Funcs[0].Name = "private"
		})
	}
}

func BenchmarkEmbeddedParse(b *testing.B) {
	src, err := os.ReadFile("../prelude/lists.bork")
	if err != nil {
		b.Fatal(err)
	}
	for _, cached := range []bool{false, true} {
		name := "ordinary"
		if cached {
			name = "cached"
		}
		b.Run(name, func(b *testing.B) {
			if cached {
				ParseEmbedded("prelude/lists.bork", src, &diag.List{})
			}
			b.ReportAllocs()
			for b.Loop() {
				diags := &diag.List{}
				if cached {
					ParseEmbedded("prelude/lists.bork", src, diags)
				} else {
					Parse("prelude/lists.bork", src, diags)
				}
				if diags.Len() != 0 {
					b.Fatal(diags)
				}
			}
		})
	}
}
