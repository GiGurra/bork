package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOptionPromotion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"positions", `type C = { value: Option[Int] = 1 }
fn take(x: Option[Int]): Option[Int] { x }
fn make(): Option[Int] { return 2 }
fn main() {
 x: Option[Int] = 3
 println(take(4)); println(take(x: 5)); println(make()); println(x)
 c = C {}; println(c.copy(value: 6))
 xs: List[Option[Int]] = [7, .None]; println(xs)
 m: Map[String, Option[Int]] = {"x": 8}; println(m)
 f: () => Option[Int] = () => 9; println(f())
 println(C { value: if (true) { 10 } else { .None } })
}`, ""},
		{"payload context", `type C = { label: String }
fn main() { c: Option[C] = .{label: "x"}; println(c)
 xs: Option[List[Int]] = []; println(xs)
 m: Option[Map[String,Int]] = {:}; println(m)
 f: Option[(Int) => Int] = x => x+1; println(f)
 x: Option[Int8] = 100; println(x) }`, ""},
		{"generic witness", `fn choose[T](value: Option[T], witness: T): Option[T] { value }
fn reversed[T](witness: T, value: Option[T]): Option[T] { value }
fn main() { println(choose(1,2)); println(reversed(2,1)); x: Option[Int] = choose(1,2); println(x) }`, ""},
		{"generic result", `fn raw[T](value: T): T { value }
fn none[T](): Option[T] { .None }
fn empty[T](): List[T] { [] }
fn main() { x: Option[Int] = none(); println(x)
 y: Option[Int] = raw(1); println(y)
 z: Option[List[Int]] = empty(); println(z)
 w: Option[Int] = dbg(none()); println(w) }`, ""},
		{"record witness", `type Box[T] = { value: Option[T], witness: T }
fn main() { println(Box { value: 1, witness: 2 }); println(Box { witness: 2, value: 1 }) }`, ""},
		{"aliases and ordinary unions", `type Number = Int | String
type Optional = Option[Number]
fn wrap(x: Number): Optional { x }
fn main() { x: Optional = 1; println(x); println(wrap("x")) }`, ""},
		{"source optional union", `fn wrap(x: Int | Option[Int]): Option[Int | Option[Int]] { x }
fn main() {println(wrap(1))}`, "body produces"},
		{"source rigid union", `fn wrap[T](x: Int | T): Option[Int | T] { x }
fn main() {println(wrap[String](1))}`, "body produces"},
		{"explicit inference", `fn take[T](x:Option[T]) uses io {println(x)}
fn main() {x=Option.Some{value:1}; take(x); take(.Some{value:1}); take(Option.Some{value:1})}`, ""},
		{"generic callback result", `fn apply[T](witness:T, f:() => Option[T]):Option[T] {f()}
fn main() {println(apply(1,()=>2))}`, ""},
		{"generic expected result", `fn take[T](value:Option[T]):Option[T] {value}
fn main() {x:Option[Int]=take(1);println(x)}`, ""},
		{"factory payload", `type Box[T]={value:T}
fn factory[T](value:T):Box[T] {Box{value:value}}
fn none[T]():T {todo()}
fn main() {x:Option[Box[Int]]=factory(1);println(x); y:Option[Int]=none();println(y)}`, ""},
		{"invariant payload", `type Range={lo:Int=1,hi:Int=2} where ordered
pred ordered(r:Range) {r.lo<=r.hi}
fn main() {r:Option[Range]=.{hi:0};println(r)}`, "is false"},
		{"sibling payload", `pred above(x:Int,lo:Int){x>=lo}
type Range={lo:Int,hi:Int where above(lo)}
fn main() {r:Option[Range]=.{lo:2,hi:1};println(r)}`, "is false"},
		{"no later context", `fn main() {x=.{value:1}; y:Option[Int]=x;println(y)}`, "expected record"},

		{"optional expression witness", `type C={value:Option[Int]}
fn choose[T](a:Option[T],b:Option[T]):Option[T]{a}
fn (c:C) Get():Option[Int] {c.value}
fn identity[T](x:T):T{x}
fn main(){c=C{value:.Some{value:2}}; f:()=>Option[Int]=()=>3
println(choose(1,c.value)); println(choose(c.value,1))
println(choose(1,f())); println(choose(f(),1))
println(choose(1,c.Get()));println(choose(c.Get(),1))
println(choose(1,identity(c.value)));println(choose(identity(c.value),1))
println(choose(1,identity[Option[Int]](c.value)))
println(choose(1,{y:Option[Int]=.Some{value:2};y}))}`, ""},

		{"optional lifetime escape", `fn escape():Option[Task[Int]] {scope s {spawn(s,()=>1)}}
fn main() {println(escape())}`, "belongs to scope s"},
		{"callback effects", `fn take(f:Option[()=>Int]) uses io {println(f)}
fn main() {take(()=>{println("effect");1})}`, "uses io"},
		{"closed positive alias", `pred positive(x:Int){x>0}
type Positive=Int where positive
fn take(x:Option[Positive]) uses io {println(x)}
fn main() {take(0)}`, "is false"},
		{"whole payload proof", `type Range={lo:Int,hi:Int} where ordered
pred ordered(r:Range){r.lo<=r.hi}
fn wrap(r:Range):Option[Range]{r}
fn main(){println(wrap(Range{lo:1,hi:2}))}`, ""},

		{"composite and constructor witnesses", `fn choose[T](a:Option[T],b:Option[T]):Option[T]{a}
fn identity[T](x:T):T{x}
fn opt():Option[Int]{.Some{value:2}}
type C[T]={value:T}
fn(c:C[T]) get[T]():T{c.value}
fn main(){println(choose([1],{x:Option[List[Int]]=.Some{value:[2]};x}))
println(choose(1,identity(Option[Int].Some{value:2})))
c=C{value:Option[Int].Some{value:2}};println(choose(1,c.get()))
println(choose(1,match(true){true=>Option[Int].Some{value:2},false=>Option[Int].None}))
println(choose(1,if(true){opt()}else{Option[Int].None}))}`, ""},

		{"annotated and structural witnesses", `fn choose[T](a:Option[T],b:Option[T]):Option[T]{a}
type C={value:Int}
fn(c:C) identity[T](x:T):T{x}
type Holder={value:Option[Int]}
fn main(){println(choose(1,{x:Option[Int|String]=2;x}))
f:()=>Int=()=>1;println(choose(f,{g:Option[()=>Int]=()=>2;g}))
c=C{value:0};x:Option[Int]=2;println(choose(1,c.identity(x)))
println(choose(1,if(true){x}else{.None}));println(choose(1,if(true){x}else{panic("x")}))
h=Holder{value:2};println(choose(1,match(h){Holder{value:v}=>v}))}`, ""},

		{"rigid composite witness", `fn choose[T](a:Option[T],b:Option[T]):Option[T]{a}
fn pass[T](xs:List[T]) uses io {println(choose(xs,{w:Option[List[T]]=.Some{value:xs};w}))}
fn main(){pass([1])}`, ""},

		{"unknown witness annotation", `fn choose[T](a:Option[T],b:Option[T]):Option[T]{a}
fn main(){println(choose(1,{w:Option[Unknown]=.None;w}))}`, "Unknown"},

		{"explicit nested", `fn main() { x: Option[Option[Int]] = .Some {value: .Some {value: 1}}; println(x) }`, ""},
		{"known rigid head", `fn make[T](value: List[T]): Option[List[T]] { value }
fn main() { println(make([1])) }`, ""},
		{"nested rejection", `fn main() { x: Option[Option[Int]] = Option[Int].Some {value:1}; println(x) }`, "found Option[Int]"},
		{"rigid rejection", `fn make[T](value:T): Option[T] { value }
fn main() { println(make(1)) }`, "body produces T"},
		{"no inference", `fn take[T](value: Option[T]) uses io { println(value) }
fn main() { take(1) }`, "must be Option[T]"},
		{"target union", `type C = { value: Int }
fn main() { x: Option[Int] | C = 1; println(x) }`, "found Int"},
		{"payload fact", `pred positive(x:Int) {x>0}
fn take(x:Option[Int where positive]) uses io {println(x)}
fn main() {take(0)}`, "is false"},
		{"payload fact guarded", `pred positive(x:Int) {x>0}
fn take(x:Option[Int where positive]) uses io {println(x)}
fn run(x:Int) uses io {if (x>0) {take(x)}}
fn main() {run(1)}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(fixtureDir(t), "main.bork")
			if err := os.WriteFile(path, []byte(tc.source+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(path)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestDescribeOptionPromotion(t *testing.T) {
	t.Parallel()
	source := `type C = { value: Int }
fn main() {
 value = 2
 scalar: Option[Int] = value
 println(scalar)
 record: Option[C] = .{value: value}
 println(record)
 }`
	got := describeAt(t, source, "value\n println(scalar)", "")
	if got.typ != "Option[Int]" || !got.defined {
		t.Fatalf("promoted name: %+v", got)
	}
	got = describeAt(t, source, ".{value:", "")
	if got.typ != "Option[C]" || !got.defined {
		t.Fatalf("promoted record: %+v", got)
	}
	got = describeAt(t, source, "value}\n println(record)", "")
	if got.typ != "Int" || !got.defined {
		t.Fatalf("inner name: %+v", got)
	}
}

func TestOptionPromotionPrivatePayload(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ source, want string }{
		{`fn main(){x:Option[api.Config]=.{value:1};println(x)}`, "controls its construction"},
		{`fn main(){x:Option[api.Config]=api.New(1);println(x)}`, ""},
	} {
		dir := t.TempDir()
		files := map[string]string{"bork.mod": "module example.com/optional\n", "api/api.bork": "type Config=private {value:Int}\nfn New(value:Int):Config{.{value:value}}\n", "main.bork": "import \"example.com/optional/api\"\n" + tc.source + "\n"}
		for name, src := range files {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(src), 0644); err != nil {
				t.Fatal(err)
			}
		}
		_, _, err := Check(filepath.Join(dir, "main.bork"))
		if tc.want == "" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("want %q, got %v", tc.want, err)
		}
	}
}
