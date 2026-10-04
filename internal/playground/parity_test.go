package playground

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/GiGurra/bork/internal/driver"
)

func TestNativeDiagnosticParity(t *testing.T) {
	for _, source := range []string{
		`fn main() { println("hello") }`,
		`fn main() { println(missing) }`,
		`fn greet() { println("hi") }`,
		"pred positive(x: Int) { x > 0 }\nfn need(x: Int where positive): Int { x }\nfn safe(x: Int): Int { if (positive(x)) { need(x) } else { 0 } }",
	} {
		browser := Handle(Request{Action: "check", Source: source})
		path := filepath.Join(t.TempDir(), SourceFile)
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := driver.Check(path)
		var native *driver.DiagError
		if err == nil {
			if !browser.OK {
				t.Fatalf("native passed, browser failed: %+v", browser)
			}
			continue
		}
		if !errors.As(err, &native) {
			t.Fatal(err)
		}
		diagnostics := native.Diags.Sorted()
		if browser.OK || len(browser.Diagnostics) != len(diagnostics) {
			t.Fatalf("diagnostic count differs: %+v / %v", browser, diagnostics)
		}
		for i, want := range diagnostics {
			got := browser.Diagnostics[i]
			if got.Code != want.Code || got.Msg != want.Msg || got.Pos.Line != want.Pos.Line || got.Pos.Col != want.Pos.Col {
				t.Fatalf("diagnostics differ: %v / %v", got, want)
			}
		}
	}
}
