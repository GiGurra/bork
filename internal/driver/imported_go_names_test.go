package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestUnsafeGoBorkPackageNames(t *testing.T) {
	t.Parallel()
	source := `import "example.com/validator/foreign"
fn Read():Int unsafe go {
 box:=foreign.Box[int64]{value:3}
 value:=foreign.Shape_Value{value:box.value}
 answer:=value.value+foreign.Increment(1)
 foreign:=struct{Value int64}{Value:7}
 return answer+foreign.Value
}
fn main(){println(Read());println(comptime{Read()})}`
	dir := validatorFixture(t, source)
	pkgDir := filepath.Join(dir, "foreign")
	if err := os.Mkdir(pkgDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "types.bork"), []byte(`type Box[T]={value:T}
type Shape=sealed{Value{value:Int}}
fn Increment(value:Int):Int{value+1}`), 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if string(output) != "12\n12\n" {
		t.Fatalf("output: %s", output)
	}
}
