package driver

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestHermetic runs testdata/hermetic with bork test --hermetic: the
// tests that can reach the network with no mock in force fail without
// running, and say how.
func TestHermetic(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "..", "testdata", "hermetic")
	var out strings.Builder
	code, err := Test(dir, &out, TestOptions{Hermetic: true})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(out.String(), dir+string(filepath.Separator), "")
	compare(t, filepath.Join(dir, "expected_test_output.txt"), fmt.Sprintf("%sexit code %d\n", got, code))
}
