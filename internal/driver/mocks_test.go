package driver

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMocksLeaveProgramsAlone checks that a program whose tests mock
// functions builds as if they did not: no dispatchers, frames, handles
// or call records.
func TestMocksLeaveProgramsAlone(t *testing.T) {
	for _, c := range []string{"mocks", "mocks_expect", "mocks_generic"} {
		src, err := Emit(filepath.Join("..", "..", "testdata", "cases", c))
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range []string{"_mock", "_real_", "pprof", "Mock[", "FetchCall2", "SaveCall", "_mockBody", "_mockEnv"} {
			if strings.Contains(string(src), w) {
				t.Errorf("%s: the program has %s", c, w)
			}
		}
	}
}

// TestGenericMockRecursion checks that a generic mock whose body
// reaches its target at a type built from its type parameters is
// reported, directly or through another function, rather than failing
// the Go build.
func TestGenericMockRecursion(t *testing.T) {
	dir := t.TempDir()
	src := `fn Describe[T: Show](x: T) uses net: String {
  s"real ${show(x)}"
}

fn helper[U: Show](u: U) uses net: String {
  Describe(u)
}

fn main() {
  println(Describe(1))
}

test "directly" {
  mock Describe(x) { if (false) { Describe([x]) } else { "m" } }
}

test "through another function" {
  mock Describe(x) { if (false) { helper([x]) } else { "m" } }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Test(dir, io.Discard, TestOptions{})
	var diags *DiagError
	if !errors.As(err, &diags) {
		t.Fatalf("got %v, want diagnostics", err)
	}
	got := strings.ReplaceAll(err.Error(), dir+string(filepath.Separator), "")
	for _, want := range []string{
		"main.bork:14:35: the mock of Describe reaches Describe again at type List[T]",
		"main.bork:18:35: the mock of Describe reaches Describe again (through helper) at type List[T]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("diagnostics lack %q:\n%s", want, got)
		}
	}
}
