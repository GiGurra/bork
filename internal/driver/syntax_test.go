package driver

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
)

// TestCheckLeavesSyntax checks that type checking, lifetimes, and facts
// leave the syntax tree as written: what they work out is recorded in
// check.Info.
func TestCheckLeavesSyntax(t *testing.T) {
	var dirs []string
	for _, root := range []string{filepath.Join("..", "..", "testdata", "cases"), filepath.Join("..", "..", "examples")} {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(root, e.Name()))
			}
		}
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()
			files, root, diags, err := load(dir)
			if err != nil || diags.Len() > 0 {
				return // a case of syntax errors
			}
			want, _, _, err := load(dir)
			if err != nil {
				t.Fatal(err)
			}
			info := check.Program(files, root, diags)
			if diags.Len() == 0 {
				check.Lifetimes(files, info, diags)
			}
			if diags.Len() == 0 {
				check.Facts(files, info, &diag.List{}, nil)
			}
			for i := range files {
				if !reflect.DeepEqual(files[i], want[i]) {
					t.Errorf("checking changed the syntax of %s", files[i].Path)
				}
			}
		})
	}
}
