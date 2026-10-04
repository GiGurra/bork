package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageValueChecks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"forward", "Twice=Answer*2\nAnswer:Int=21\nfn main(){println(Twice)}", ""},
		{"mixed cycle", "A:Int=B\nlazy B:Int=A\nfn main(){}", "dependency cycle"},
		{"helper cycle", "A:Int=read()\nfn read():Int{A}\nfn main(){}", "dependency cycle"},
		{"effect", "Value={println(1);1}\nfn main(){}", "requires a pure initializer"},
		{"annotation", "Value:Int=\"wrong\"\nfn main(){}", "must be Int"},
		{"promise", "pred positive(n:Int){n>0}\nValue:Int where positive=1\nfn main(){n:Int where positive=Value;println(n)}", ""},
		{"bad promise", "pred positive(n:Int){n>0}\nValue:Int where positive=-1\nfn main(){}", "positive"},
		{"early return promise", "pred positive(n:Int){n>0}\nValue:Int where positive={if(true){return -1};1}\nfn main(){}", "positive"},
		{"comptime capture", "Value=42\nfn main(){println(comptime{Value})}", "comptime cannot read runtime package value"},
		{"comptime initializer", "Value=comptime{21*2}\nfn main(){println(Value)}", ""},
		{"duplicate mixed", "Value=1\nlazy Value=2\nfn main(){}", "already declared"},
		{"function value", "Add:(Int)=>Int=n=>n+1\nfn main(){println(Add(2))}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := fixtureDir(t)
			if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(dir)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q; got %v", tc.want, err)
			}
		})
	}
}

func TestDescribePackageValueNeverForces(t *testing.T) {
	t.Parallel()
	source := "Unread:Int=panic(\"must not force\")\nAnswer=21\nTwice=Answer*2\nfn main(){println(Unread)}"
	dir := fixtureDir(t)
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, pos := range []string{"1:1", "4:19", "3:1"} {
		result, err := Describe(fmt.Sprintf("%s:%s", path, pos), "")
		if err != nil {
			t.Fatal(err)
		}
		if result.Type != "Int" || result.Lazy == nil || result.Lazy.Kind != "package binding" || result.Lazy.Effects != "nothing" {
			t.Fatalf("%+v", result)
		}
		if pos == "3:1" && strings.Join(result.Lazy.Dependencies, ",") != "Answer" {
			t.Fatalf("%+v", result)
		}
	}
}
