package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClosedDefaultsWholeInvariant(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"bad record", `type Range = { lo: Int = 5, hi: Int = 3 } where ordered
pred ordered(r: Range) { r.lo <= r.hi }`, "defaults of Range require the completed value to be ordered"},
		{"good record", `type Range = { lo: Int = 3, hi: Int = 5 } where ordered
pred ordered(r: Range) { r.lo <= r.hi }`, ""},
		{"partial record", `type Range = { lo: Int = 5, hi: Int } where ordered
pred ordered(r: Range) { r.lo <= r.hi }`, ""},
		{"bad sealed", `type Span = sealed { Closed { lo: Int = 5, hi: Int = 3 } } where ordered
pred ordered(r: Span) { match (r) { .Closed { lo, hi } => lo <= hi } }`, "defaults of Span require the completed value to be ordered"},
		{"variant invariant", `type Span = sealed { Closed { lo: Int = 5, hi: Int = 3 } where ordered }
pred ordered(r: Span) { match (r) { .Closed { lo, hi } => lo <= hi } }`, "defaults of Span require the completed value to be ordered"},
		{"good native predicate", `type Label = { text: String = "hello!" } where hasMarker
pred hasMarker(label: Label) { label.text.contains("!") }`, ""},
		{"native predicate", `type Label = { text: String = "hello" } where hasMarker
pred hasMarker(label: Label) { label.text.contains("!") }`, "defaults of Label require the completed value to be hasMarker"},
		{"good sealed", `type Span = sealed { Closed { lo: Int = 3, hi: Int = 5 } } where ordered
pred ordered(r: Span) { match (r) { .Closed { lo, hi } => lo <= hi } }`, ""},
		{"lazy defaults stay deferred", `type Range = { lazy lo: Int = 5, hi: Int = 3 } where ordered
pred ordered(r: Range) { r.lo <= r.hi }`, ""},
		{"generic sealed specialization", `type Span[T] = sealed { Closed { lo: Int = 5, hi: Int = 3, values: List[T] = [] } } where ordered
pred ordered[T](r: Span[T]) { match (r) { .Closed { lo, hi, values: _ } => lo <= hi } }
fn unused(r: Span[String]) { _ = r }`, "defaults of Span[String] require the completed value to be ordered"},
		{"generic specialization", `type Range[T] = { lo: Int = 5, hi: Int = 3, values: List[T] = [] } where ordered
pred ordered[T](r: Range[T]) { r.lo <= r.hi }
fn unused(r: Range[String]) { _ = r }`, "defaults of Range[String] require the completed value to be ordered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "main.bork")
			if err := os.WriteFile(path, []byte(tc.source+"\nfn main() {}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(path)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
			if !strings.Contains(err.Error(), path+":1:") {
				t.Fatalf("diagnostic must point to type declaration: %v", err)
			}
		})
	}
}
