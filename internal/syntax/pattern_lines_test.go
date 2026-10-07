package syntax

import (
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestLeadingDotPatternLists(t *testing.T) {
	for _, gap := range []string{"\n", "\n\n// next pattern\n", " /* next\npattern */ "} {
		for _, pattern := range []string{
			"[.Some(n)" + gap + ".None]",
			".Pair(.Some(n)" + gap + ".None)",
			".Pair(\" ) ] \"" + gap + ".None)",
			"[name" + gap + ".Some(n)]",
			"[Option.Some(n)" + gap + ".None]",
			"[1" + gap + ".Some(n)]",
			"[\"text\"" + gap + ".None]",
			"[(a, b)" + gap + ".None]",
			"[.Wrap { values: [.Some(n)" + gap + ".None] }" + gap + ".None]",
			".Pair(Option.\nSome(n)" + gap + ".None)",
		} {
			for _, body := range []string{
				"match xs { " + pattern + " => true; _ => false }",
				"if xs is " + pattern + " { true } else { false }",
				"for { " + pattern + " in xs } yield 1",
			} {
				t.Run(body, func(t *testing.T) {
					d := &diag.List{}
					file := Parse("patterns.bork", []byte("fn f() {\n"+body+"\n}"), d)
					if d.Len() != 0 {
						t.Fatal(d.Error())
					}
					if len(file.PatternLineStarts) == 0 {
						t.Fatal("leading-dot pattern boundary was not recorded")
					}
				})
			}
		}
	}
}

func TestLeadingDotPatternListsNeedSeparators(t *testing.T) {
	for _, pattern := range []string{"[.Some(n) .None]", ".Pair(.Some(n) .None)", "[name .Some(n)]"} {
		d := &diag.List{}
		Parse("patterns.bork", []byte("fn f() { match xs { "+pattern+" => true } }"), d)
		// A same-line qualified name remains a single pattern, not two.
		if pattern == "[name .Some(n)]" {
			if d.Len() != 0 {
				t.Fatal(d.Error())
			}
		} else if !strings.Contains(d.Error(), "expected ','") {
			t.Fatalf("%s: expected missing-separator error, got %s", pattern, d.Error())
		}
	}
}

func TestLeadingDotAfterBindingPattern(t *testing.T) {
	d := &diag.List{}
	file := Parse("patterns.bork", []byte("import name \"example/name\"\nfn f() { match xs { [name\n.Some(n)] => n } }"), d)
	if d.Len() != 0 {
		t.Fatal(d.Error())
	}
	pattern := file.Funcs[0].Body.Tail.(*Match).Arms[0].Pattern.(*ListPat)
	if len(pattern.Elems) != 2 || pattern.Elems[0].(*VariantPat).Path[0] != "name" || !pattern.Elems[1].(*VariantPat).Context {
		t.Fatalf("expected a binding followed by a contextual variant, got %#v", pattern)
	}
}

func TestPatternListPredicateArgumentsKeepChains(t *testing.T) {
	source := `fn f() {
  match xs {
    [n: Int where accepts(" x "
      .trim())
     .Some(v)] => n
  }
}`
	d := &diag.List{}
	file := Parse("patterns.bork", []byte(source), d)
	if d.Len() != 0 {
		t.Fatal(d.Error())
	}
	pattern := file.Funcs[0].Body.Tail.(*Match).Arms[0].Pattern.(*ListPat)
	arg := pattern.Elems[0].(*TypePat).Type.Where[0].Args[0]
	if len(pattern.Elems) != 2 || arg.(*Call).Fun.(*Selector).Name != "trim" || len(file.PatternLineStarts) != 1 {
		t.Fatalf("pattern separation changed predicate arguments: %#v, %#v", pattern, file.PatternLineStarts)
	}
}
