package driver

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestComptimePanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	// Panics are build failures, never silently deferred to runtime.
	if err := os.WriteFile(path, []byte("fn main(){n:Int=comptime{panic(\"must not run\")};println(n)}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Check(dir)
	if err == nil || !strings.Contains(err.Error(), "comptime failed: panic: must not run") {
		t.Fatalf("unexpected result: %v", err)
	}
}

func TestComptimeLiteralEvaluation(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"table facts and dependencies", `pred nonEmpty(xs:List[Int]){xs.length()>0}
fn table():List[Int]{range(1,5).map(n=>n*n)}
fn main(){xs:List[Int] where nonEmpty=comptime{table()};sum=comptime{xs.fold(0,(a,n)=>a+n)};println(xs);println(sum)}`, "[1, 4, 9, 16]\n30\n"},
		{"expected integer width", `fn f():Int8{comptime{100+27}}
fn main(){println(f())}`, "127\n"},
		{"local returns", `fn f():String{n=comptime{if(true){return 7};9};s"$n"}
fn main(){println(f())}`, "7\n"},
		{"record and capture", `type R={name:String,n:Int}
fn make():R{R{name:"bork",n:42}}
fn main(){r=comptime{make()};println(r);println(comptime{r.n+1})}`, "R { name: \"bork\", n: 42 }\n43\n"},
		{"capture aliases", `fn main(){a=20;b=a;println(comptime{b+2})}`, "22\n"},
		{"float bits", `fn negativeZero():Float64 unsafe go{import "math"
return math.Float64frombits(0x8000000000000000)}
fn nan():Float64 unsafe go{import "math"
return math.Float64frombits(0x7ff8000000000001)}
fn ieeeBits(n:Float64):Uint64 unsafe go{import "math"
return math.Float64bits(n)}
fn main(){println(ieeeBits(comptime{negativeZero()}));println(ieeeBits(comptime{nan()}))}`, "9223372036854775808\n9221120237041090561\n"},
		{"nil list", `fn empty():List[Int] unsafe go{return nil}
fn isNil(xs:List[Int]):Bool unsafe go{return xs==nil}
fn main(){println(isNil(comptime{empty()}))}`, "true\n"},
		{"generic record", `type Box[T]={value:T}
fn make():Box[Int]{Box[Int]{value:42}}
fn main(){b=comptime{make()};println(b.value)}`, "42\n"},
		{"acyclic proof helpers", `pred good(n:Int){n>0}
fn must(n:Int where good):Int{n}
pred p(n:Int){good(n) && must(1)>0}
fn recipe(n:Int where p):Int{n}
fn main(){println(comptime{recipe(1)})}`, "1\n"},
		{"fresh pure computed fields", `type C={n:Int,lazy twice:Int=n*2,lazy more:Int=twice+1}
fn main(){println(comptime{c=C{n:20};c.more})}`, "41\n"},
		{"fresh computed copy", `type C={n:Int,lazy twice:Int=n*2}
fn main(){println(comptime{c=C{n:20};c.copy(n:21).twice})}`, "42\n"},
		{"fresh pure lazy cell", `fn main(){println(comptime{lazy n=21;n*2})}`, "42\n"},
		{"unordered lookup", `fn main(){println(comptime{{"x":1}.unordered().getOr("x",0)})}`, "1\n"},
		{"sorted traversal", `fn main(){println(comptime{{"b":2,"a":1}.sorted().keys()})}`, "[\"a\", \"b\"]\n"},
		{"keyword record field", `type R={var:Int}
fn make():R{R{var:42}}
fn main(){r=comptime{make()};println(r.var)}`, "42\n"},
		{"success value", `fn main(){comptime{Ok}}`, ""},
		{"nested computation", `fn main(){println(comptime{n=comptime{21};n*2})}`, "42\n"},
		{"ordinary helper promise", `pred positive(n:Int){n>0}
fn identity(n:Int where positive):Int where positive{n}
fn main(){n:Int where positive=comptime{identity(3)};println(n)}`, "3\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "main.bork")
			if err := os.WriteFile(filepath.Join(dir, ModFile), []byte("module example.com/comptime\nunsafe \"example.com/comptime\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
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

func TestComptimeProofBeforeExecution(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"eager default final proof argument", `pred positive(n:Int){n>0}
type R={value:Int where positive=0}
fn must(n:Int where positive):Bool{panic("must not execute")}
pred p(r:R){must(r.value)}
fn main(){_=comptime{1};r:R where p=R{};println(r)}`, "positive(0) is false"},
		{"lazy default final proof argument", `pred positive(n:Int){n>0}
type R={lazy value:Int where positive=0}
fn must(n:Int where positive):Bool{panic("must not execute")}
pred p(r:R){must(r.value)}
fn main(){_=comptime{1};r:R where p=R{};println(r)}`, "positive(0) is false"},
		{"eager default precondition", `pred positive(n:Int){n>0}
type R={value:Int where positive=0}
fn must(n:Int where positive):Int{panic("must not execute")}
fn main(){println(comptime{c=R{};must(c.value)})}`, "positive(0) is false"},
		{"eager default predicate helper", `pred positive(n:Int){n>0}
type R={value:Int where positive=0}
fn must(n:Int where positive):Bool{panic("must not execute")}
pred check(n:Int){c=R{};must(c.value)}
fn require(n:Int where check):Int{n}
fn main(){println(comptime{require(1)})}`, "positive(0) is false"},
		{"lazy default precondition", `pred positive(n:Int){n>0}
type R={lazy value:Int where positive=0}
fn must(n:Int where positive):Int{panic("must not execute")}
fn main(){println(comptime{c=R{};must(c.value)})}`, "positive(0) is false"},
		{"lazy default predicate helper", `pred positive(n:Int){n>0}
type R={lazy value:Int where positive=0}
fn must(n:Int where positive):Bool{panic("must not execute")}
pred check(n:Int){c=R{};must(c.value)}
fn require(n:Int where check):Int{n}
fn main(){println(comptime{require(1)})}`, "positive(0) is false"},
		{"predicate helper precondition", `pred good(n:Int){n>0}
fn must(n:Int where good):Bool{panic("must not execute")}
pred p(n:Int){must(-1)}
fn recipe(n:Int where p):Int{n}
fn main(){println(comptime{recipe(1)})}`, "good(-1) is false"},
		{"nil and empty predicate identity", `fn nils():List[Int] unsafe go{return nil}
pred isNil(xs:List[Int]) unsafe go{return xs==nil}
fn require(xs:List[Int] where isNil):Bool{true}
fn main(){a=comptime{nils()};b:List[Int]=comptime{[]};println(require(a));println(require(b))}`, "isNil([]) is false"},
		{"output predicate helper precondition", `pred good(n:Int){n>0}
fn must(n:Int where good):Bool{panic("must not execute")}
pred p(n:Int){must(-1)}
fn main(){n:Int where p=comptime{1};println(n)}`, "good(-1) is false"},
		{"function where prerequisites", `pred good(n:Int){n>0}
pred must(n:Int where good){panic("must not execute")}
fn recipe() where must(-1):Int{1}
fn main(){println(comptime{recipe()})}`, "good(-1) is false"},
		{"predicate argument cycle", `pred self(n:Int where self){true}
fn require(n:Int where self):Int{n}
fn main(){println(comptime{require(1)})}`, "cyclic comptime predicate argument proof"},
		{"constrained generic proof argument", `pred good(n:Int){n>0}
type Positive=Int where good
pred must[T](n:T){panic("must not execute")}
fn recipe() where must[Positive](-1):Int{1}
fn main(){println(comptime{recipe()})}`, "good(-1) is false"},
		{"false precondition", `pred positive(n:Int){n>0}
fn must(n:Int where positive):Int{panic("must not execute")}
fn main(){println(comptime{must(-1)})}`, "positive(-1) is false"},
		{"enclosing guard", `pred positive(n:Int){n>0}
fn must(n:Int where positive):Int{panic("must not execute")}
fn main(){n=0;if(positive(n)){println(comptime{must(n)})}}`, "positive(0) is false"},
		{"captured false annotation", `pred positive(n:Int){n>0}
fn must(n:Int where positive):Int{panic("must not execute")}
fn main(){n:Int where positive=0;println(comptime{must(n)})}`, "positive(0) is false"},
		{"invalid helper promise", `pred positive(n:Int){n>0}
fn broken():Int where positive{-1}
fn main(){println(comptime{broken()})}`, "positive(-1) is false"},
		{"invalid computed record before dependency", `pred positive(r:R){r.n>0}
type R={n:Int} where positive
fn invalid():R unsafe go{return R{n:-1}}
fn consume(r:R):Int{panic("must not execute")}
fn main(){r=comptime{invalid()};println(comptime{consume(r)})}`, "positive(R { n: -1 }) is false"},
		{"false computed output", `pred positive(n:Int){n>0}
fn raw():Int{-1}
fn main(){n:Int where positive=comptime{raw()};println(n)}`, "positive(-1) is false"},
		{"computation dependency cycle", `fn a():Int{comptime{b()}}
fn b():Int{a()}
fn main(){println(a())}`, "cyclic comptime"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ModFile), []byte("module example.com/comptime\nunsafe \"example.com/comptime\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			if strings.Contains(err.Error(), "comptime failed: panic:") {
				t.Fatal("executed before proving preconditions")
			}
		})
	}
}

func TestComptimeLimits(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"async owner helper", `fn owner(s:Scope):Scope{s}
fn main(){println(comptime{scope s{async(owner(s)) x=1;x}})}`, "compile-time evaluation cannot schedule an async initializer"},
		{"evaluation timeout", `fn spin():Int unsafe go{for{}}
fn main(){println(comptime{spin()})}`, "evaluation exceeded 10s"},
		{"proof timeout", `pred never(n:Int) unsafe go{for{}}
fn must(n:Int where never):Int{n}
fn main(){println(comptime{must(1)})}`, "predicate evaluation exceeded 10s"},
		{"result size", `fn huge():String unsafe go{import "strings"
return strings.Repeat("x",17<<20)}
fn main(){println(comptime{huge()})}`, "result exceeds 16 MiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, source := range map[string]string{ModFile: "module example.com/comptime\nunsafe \"example.com/comptime\"\n", "main.bork": tc.source} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
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

func TestComptimeRequiresNativeTarget(t *testing.T) {
	target := "windows"
	if runtime.GOOS == target {
		target = "linux"
	}
	t.Setenv("GOOS", target)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte("fn main(){println(comptime{1})}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Check(dir)
	if err == nil || !strings.Contains(err.Error(), "comptime requires native target") {
		t.Fatalf("unexpected cross-target result: %v", err)
	}
}

func TestComptimeRejectsUnorderedTraversal(t *testing.T) {
	for _, source := range []string{
		`fn main(){println(comptime{{"a":1}.unordered().keys()})}`,
		`fn fail(n:Int):Int{panic(s"value $n")}
fn main(){println(comptime{{"a":1,"b":2}.unordered().mapValues(fail).getOr("a",0)})}`,

		`pred p(n:Int){{"a":1}.unordered().keys().length()>0}
fn main(){n:Int where p=comptime{1};println(n)}`,
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := Check(dir)
		if err == nil || !strings.Contains(err.Error(), "comptime cannot iterate an unordered map") {
			t.Fatalf("unexpected unordered traversal result: %v", err)
		}
	}
}

func TestSessionComptimeBypassesUntrackedInputs(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER", "off")
	dir := t.TempDir()
	foreign := filepath.Join(t.TempDir(), "data")
	source := fmt.Sprintf(`fn readForeign(path:String):String unsafe go{
import "text/template"
t,err:=template.ParseFiles(path);if err!=nil{panic(err)};return t.Tree.Root.String()
}
fn main(){println(comptime{readForeign(%q)})}`, foreign)
	for name, data := range map[string]string{ModFile: "module example.com/comptime\nunsafe \"example.com/comptime\"\n", "main.bork": source} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(foreign, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession()
	first, err := session.Emit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if session.Stats().Reason != "compile-time evaluator" || session.Stats().Bypasses != 1 {
		t.Fatalf("initial comptime result retained: %+v", session.Stats())
	}
	if err := os.WriteFile(foreign, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := session.Emit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) || session.Stats().Hits != 0 || session.Stats().Bypasses != 2 {
		t.Fatalf("foreign file edit reused comptime result: %+v", session.Stats())
	}
}

func TestComptimeImportedDefaultPreflight(t *testing.T) {
	for _, lazy := range []string{"", "lazy "} {
		t.Run(lazy+"field", func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				ModFile: "module example.com/review\n",
				"api/api.bork": `pred positive(n:Int){n>0}
type Inner={value:Int where positive}
type Outer={` + lazy + `inner:Inner=Inner{value:0}}
fn Must(n:Int where positive):Int{panic("must not execute")}`,
				"main.bork": `import "example.com/review/api"
fn main(){println(comptime{o=api.Outer{};api.Must(o.inner.value)})}`,
			}
			for name, source := range files {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := Check(dir)
			if err == nil || !strings.Contains(err.Error(), "positive(0) is false") || strings.Contains(err.Error(), "must not execute") {
				t.Fatalf("default was not checked before execution: %v", err)
			}
		})
	}
}
