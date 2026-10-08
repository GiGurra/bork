package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestDurationMigrationFixes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"positional", `fn main() { scope s { println(delay(s, 5000)) } }`, "5.seconds()"},
		{"dynamic", `fn wait(s: Scope, ms: Int) uses io + clock + state { println(delay(s, ms + 1)) }`, "(ms + 1).millis()"},
		{"named", `import "bork/http"
fn main() { scope s { println(http.Get("http://localhost", s, timeoutMs: 5000)) } }`, "timeout: 5.seconds()"},
		{"named duration", `import "bork/http"
fn main() { scope s { println(http.Get("http://localhost", s, timeoutMs: 5.seconds())) } }`, "timeout: 5.seconds()"},
		{"nanos", `import "bork/time"
fn main() { println(time.FormatDuration(time.Nanoseconds(-1))) }`, "(-1).nanos()"},
		{"millis", `import "bork/time"
fn main() { println(time.FormatDuration(time.Milliseconds(5000))) }`, "5.seconds()"},
		{"millis try", `import "bork/time"
fn value(): Duration | OutOfRange { time.Milliseconds(5000)? }
fn main() { println(time.FormatDuration(0.seconds())); println(value()) }`, "5.seconds()"},
		{"mock", `fn work() uses io { println("work") }
test "wait" { m = mock work() {}; m.waitFor(1, ms: 10) }`, "timeout: 10.millis()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.bork")
			if err := os.WriteFile(path, []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(path)
			failure, ok := err.(*DiagError)
			if !ok {
				t.Fatalf("expected migration diagnostic, got %v", err)
			}
			var edits []diag.TextEdit
			for _, d := range failure.Diags.Sorted() {
				for _, fix := range d.Fixes {
					if strings.Contains(fix.Message, "Duration") || strings.HasPrefix(fix.Message, "use .") {
						edits = append(edits, fix.Edits...)
					}
				}
			}
			if len(edits) == 0 {
				t.Fatalf("missing migration edit: %v", err)
			}
			fixed := applyLintEdits(t, tc.source, edits)
			if !strings.Contains(fixed, tc.want) {
				t.Fatalf("wrong migration: %s", fixed)
			}
			if err := os.WriteFile(path, []byte(fixed), 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Check(path); err != nil {
				t.Fatalf("migration does not compile: %v\n%s", err, fixed)
			}
		})
	}
}

func TestDurationLiteralFacts(t *testing.T) {
	t.Parallel()
	source := `import "bork/http"
import "bork/net"
fn main() { scope s {
  _ = http.Get("http://localhost", s, timeout: 5.seconds())
  _ = net.Dial("localhost:80", s, timeout: 250.millis())
  budget = http.OpenRetryBudget(s, capacity: 1, refill: 2.minutes())
  _ = http.Retry(s, budget, attempt => http.Text(200, "ok"), baseDelay: 1.seconds(), maxDelay: 2.seconds())
} }`
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Check(path); err != nil {
		t.Fatal(err)
	}
	source = strings.Replace(source, "maxDelay: 2.seconds()", "maxDelay: 500.millis()", 1)
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Check(path); err == nil || !strings.Contains(err.Error(), "AtLeastBase") {
		t.Fatalf("missing duration order requirement: %v", err)
	}
}

func TestDurationDescribe(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"fn main() { duration = 5.seconds(); println(duration) }",
		"import \"bork/time\"\nfn main() { duration: time.Duration = 5.seconds(); println(duration) }",
	} {
		if result := describeAt(t, source, "duration)", ""); result.typ != "Duration" {
			t.Fatalf("noncanonical duration type: %+v", result)
		}
	}
}
