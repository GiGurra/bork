package driver

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestMocksLeaveProgramsAlone checks that a program whose tests mock
// functions builds as if they did not: no dispatchers, frames, handles
// or call records.
func TestMocksLeaveProgramsAlone(t *testing.T) {
	for _, c := range []string{"mocks", "mocks_expect"} {
		src, err := Emit(filepath.Join("..", "..", "testdata", "cases", c))
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range []string{"_mock", "_real_", "pprof", "Mock[", "FetchCall2", "SaveCall"} {
			if strings.Contains(string(src), w) {
				t.Errorf("%s: the program has %s", c, w)
			}
		}
	}
}
