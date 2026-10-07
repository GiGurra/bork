package format

import (
	"strings"
	"testing"
)

func TestFormatLeadingDotPatternLists(t *testing.T) {
	want := `fn f() {
  match xs {
    [
      .Some(n)
      // The next pattern stays at this level.
      .None
    ] => n
    .Pair(
      .Some(n)
      .None
    ) => n
  }
  values = [
    " x "
      .trim()
  ]
  call(
    " y "
      .trim()
  )
}
`
	for _, simplify := range []bool{false, true} {
		for _, source := range []string{want, strings.ReplaceAll(want, "\n  ", "\n")} {
			got, err := SourceWithOptions("patterns.bork", []byte(source), Options{Simplify: simplify})
			if err != nil || string(got) != want {
				t.Fatalf("simplify=%v: got %q, %v; want %q", simplify, got, err, want)
			}
		}
	}
}
