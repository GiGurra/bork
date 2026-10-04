package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestComptimeDataShapes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"map order and facts", `pred nonEmpty(m:Map[String,Int]){m.size()>0}
fn main(){m:Map[String,Int] where nonEmpty=comptime{{"b":2,"a":1}};println(m.keys());println(m.values());println(comptime{m.size()})}`, "[\"b\", \"a\"]\n[2, 1]\n2\n"},
		{"sorted export", `fn main(){m=comptime{{"b":2,"a":1}.sorted().inOrder()};println(m.keys())}`, "[\"a\", \"b\"]\n"},
		{"fieldless sealed variants", `type Flag=sealed{On,Off}
fn flag():Flag{Flag.On}
fn main(){println(comptime{flag()})}`, "Flag.On\n"},
		{"keyword sealed field", `type Value=sealed{N{var:Int}}
fn make():Value{Value.N{var:42}}
fn main(){println(comptime{make()})}`, "Value.N { var: 42 }\n"},
		{"sealed variants", `fn some():Option[Int]{.Some{value:42}}
fn none():Option[Int]{.None}
fn main(){println(comptime{some()});println(comptime{none()})}`, "Option.Some { value: 42 }\nOption.None\n"},
		{"nested generic variants", `type Box[T]={value:T}
fn make():Box[Option[List[Int]]]{Box[Option[List[Int]]]{value:.Some{value:[1,2]}}}
fn main(){b=comptime{make()};println(b.value)}`, "Option.Some { value: [1, 2] }\n"},
		{"union integer member", `fn choose():Int|String{n:Int=42;n}
fn main(){v=comptime{choose()};println(match(v){n:Int=>s"int:$n";s:String=>s})}`, "int:42\n"},
		{"nested union width", `type Box={value:Int8|Int}
fn make():Box{n:Int8=7;Box{value:n}}
fn main(){b=comptime{make()};println(match(b.value){n:Int8=>s"small:$n";n:Int=>s"large:$n"})}`, "small:7\n"},
		{"sealed union member", `fn choose():Option[Int]|String{Option.Some{value:9}}
fn main(){println(comptime{choose()})}`, "Option.Some { value: 9 }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := fixtureDir(t)
			if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(dir, "program")
			if err := Build(dir, exe); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(exe).CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if string(out) != tc.want {
				t.Fatalf("got %q, want %q", out, tc.want)
			}
		})
	}
}

func TestComptimeRejectsBehavioralMapOutputs(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`fn main(){println(comptime{{"a":1}.sorted()})}`,
		`fn main(){println(comptime{{"a":1}.unordered()})}`,
		`type Box={value:Map[String,Int]}
fn main(){println(comptime{Box{value:{"a":1}.sorted()}})}`,
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := Check(dir)
		if err == nil || !strings.Contains(err.Error(), "comptime map result must use insertion order") {
			t.Fatalf("unexpected behavioral map result: %v", err)
		}
	}
}

func TestComptimeDataShapeValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"cyclic foreign data", `type Node={kids:List[Node]}
fn cycle():Node unsafe go{kids:=make([]Node,1);root:=Node{kids:kids};kids[0]=root;return root}
fn main(){println(comptime{cycle()})}`, "result exceeds depth limit"},
		{"decoded sealed invariant", `pred valid(v:Value){match(v){Value.N{n}=>n>0;Value.Empty=>true}}
type Value=sealed{N{n:Int},Empty} where valid
fn bad():Value unsafe go{return Value_N{n:-1}}
fn main(){println(comptime{bad()})}`, "valid(Value.N { n: -1 }) is false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := fixtureDir(t)
			for name, data := range map[string]string{ModFile: "module example.com/comptime\nunsafe \"example.com/comptime\"\n", "main.bork": tc.source} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := Check(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}
