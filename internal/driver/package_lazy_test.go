package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageLazyChecks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"inferred", "lazy Answer=21\nfn main(){println(Answer)}", ""},
		{"forward", "lazy Twice=Answer*2\nlazy Answer:Int=21\nfn main(){println(Twice)}", ""},
		{"function value", "lazy Add:(Int)=>Int=n=>n+1\nfn main(){println(Add(2))}", ""},
		{"function value type args", "lazy Add:(Int)=>Int=n=>n+1\nfn main(){println(Add[Int](2))}", "only a declared generic function"},
		{"contextual name", "lazy lazy=1\nfn main(){println(lazy)}", ""},
		{"underscore", "lazy _=1\nfn main(){}", "single binding name"},
		{"self cycle", "lazy A:Int=A\nfn main(){}", "dependency cycle"},
		{"mutual cycle", "lazy A:Int=B\nlazy B:Int=A\nfn main(){}", "dependency cycle"},
		{"helper cycle", "lazy A:Int=read()\nfn read():Int{A}\nfn main(){}", "dependency cycle"},
		{"helper mutual cycle", "lazy A:Int=first()\nlazy B:Int=second()\nfn first():Int{B}\nfn second():Int{A}\nfn main(){}", "dependency cycle"},
		{"effects", "fn work()uses io:Int{println(1);1}\nlazy Value=work()\nfn main(){}", "pure initializer"},
		{"scope escape", "fn read(s:Scope):Int{1}\nlazy Value=scope s{()=>read(s)}\nfn main(){}", "scope"},
		{"no value", "fn work(){}\nlazy Value=work()\nfn main(){}", "must produce a value"},
		{"annotation", "lazy Value:Int=\"x\"\nfn main(){}", "must be Int"},
		{"duplicate", "lazy Value=1\nlazy Value=2\nfn main(){}", "already declared"},
		{"function name collision", "lazy value=1\nfn value():Int{2}\nfn main(){}", "already the name of a function"},
		{"local shadow", "lazy Value=1\nfn main(){Value=2;println(Value)}", "package value"},
		{"typed positive", "pred positive(n:Int){n>0}\nlazy Value:Int where positive=1\nfn main(){println(Value)}", ""},
		{"invalid promise", "pred positive(n:Int){n>0}\nlazy Value:Int where positive=-1\nfn main(){}", "positive"},
		{"early return promise", "pred positive(n:Int){n>0}\nlazy Value:Int where positive={if(true){return -1};1}\nfn main(){}", "positive"},
		{"option failure promise", "pred valid(x:Option[Int]){match(x){Option.Some{value:_}=>true,Option.None=>false}}\nlazy Input:Option[Int]=.None\nlazy Value:Option[Int] where valid={n=Input?;.Some{value:n}}\nfn main(){}", "valid"},
		{"direct comptime", "lazy Value:Int=panic(\"must not run\")\nfn main(){println(comptime{Value})}", "must not run"},
		{"helper comptime", "lazy Value:Int=panic(\"must not run\")\nfn read():Int{Value}\nfn main(){println(comptime{read()})}", "must not run"},
		{"computed global comptime", "lazy Value:Int=panic(\"must not run\")\ntype C={n:Int,lazy result:Int=n+Value}\nfn main(){println(comptime{C{n:1}.result})}", "must not run"},
		{"runtime predicate", "lazy Value=1\npred positive(n:Int){n>Value}\nfn main(){n:Int where positive=3;println(n)}", "not proven"},
		{"helper runtime predicate", "lazy Value=1\nfn read():Int{Value}\npred positive(n:Int){n>read()}\nfn main(){n:Int where positive=3;println(n)}", "not proven"},
		{"predicate guard", "lazy Value=1\npred positive(n:Int){n>Value}\nfn f(n:Int):Int{if(positive(n)){value:Int where positive=n;value}else{0}}\nfn main(){println(f(3))}", ""},
		{"renderer cycle", "type C={}\ninstance Render:Show[C]{fn show(c:C):String{Value}}\nlazy Value:String=s\"${C{}}\"\nfn main(){}", "dependency cycle"},
		{"nested renderer cycle", "type C={}\ntype Outer={value:C}\ninstance Render:Show[C]{fn show(c:C):String{Value}}\nlazy Value:String=toString([Outer{value:C{}}])\nfn main(){}", "dependency cycle"},
		{"generic renderer cycle", "type C={}\ninstance Render:Show[C]{fn show(c:C):String{Value}}\nfn render[T](value:T):String{s\"$value\"}\nlazy Value:String=render(C{})\nfn main(){}", "dependency cycle"},
		{"generic renderer reference cycle", "type C={}\ninstance Render:Show[C]{fn show(c:C):String{Value}}\nfn render[T](value:T):String{s\"$value\"}\nfn wrap[T](value:T):String{f:(T)=>String=render;f(value)}\nlazy Value:String=wrap(C{})\nfn main(){}", "dependency cycle"},
		{"generic Show cycle", "type D={}\ninstance ShowD:Show[D]{fn show(d:D):String{Value}}\ntype C[T]={n:T}\ninstance ShowC[T:Show]:Show[C[T]]{fn show(c:C[T]):String{s\"${c.n}\"}}\nlazy Value:String=s\"${C[D]{n:D{}}}\"\nfn main(){}", "dependency cycle"},
		{"generic predicate renderer", "type C={}\ninstance Render:Show[C]{fn show(c:C):String{Value}}\nlazy Value:String=\"expected\"\npred valid[T](value:T){s\"$value\"==\"expected\"}\nfn main(){c:C where valid=C{};println(c)}", "not proven"},
		{"ambient", "ambient label:String\nfn read()needs label:String{label}\nlazy Value=read()\nfn main(){}", "signature does not provide"},
		{"fresh comptime initializer", "lazy Value=comptime{21*2}\nfn main(){println(Value)}", ""},
		{"generic plain renderer", "type C={}\ninstance Render:Show[C]{fn show(c:C):String{Value}}\nfn render[T](value:T):String{s\"$value\"}\nlazy Value:String=render(1)\nfn main(){}", ""},
		{"decoder invariant cycle", "type C={n:Int} where Valid derive(Decode)\npred Valid(c:C){Value>0}\nlazy Value:Int={_=json.Decode[C](\"{\\\"n\\\":1}\");1}\nfn main(){}", "dependency cycle"},
		{"derived encoder cycle", "type Child={}\ninstance ChildEncode:Encode[Child]{fn encode(c:Child):Json{Json.String{value:Value}}}\ntype Outer={child:Child} derive(Encode)\nlazy Value:String=json.Encode(Outer{child:Child{}})\nfn main(){}", "dependency cycle"},
		{"derived decoder cycle", "type Child={}\ninstance ChildDecode:Decode[Child]{fn decode(j:Json):Child|DecodeError{_=Value;Child{}}}\ntype Outer={child:Child} derive(Decode)\nlazy Value:Int={_=json.Decode[Outer](\"{\\\"child\\\":null}\");1}\nfn main(){}", "dependency cycle"},
		{"derived encoder comptime", "type Child={}\ninstance ChildEncode:Encode[Child]{fn encode(c:Child):Json{Json.String{value:Value}}}\ntype Outer={child:Child} derive(Encode)\nlazy Value:String=\"package\"\nfn main(){println(comptime{json.Encode(Outer{child:Child{}})})}", ""},
		{"generic computed renderer comptime", "type C={}\ninstance Render:Show[C]{fn show(c:C):String{Value}}\nlazy Value:String=\"package\"\ntype Holder[T]={n:T,lazy text:String=s\"$n\"}\nfn main(){println(comptime{Holder[C]{n:C{}}.text})}", ""},
		{"decoder predicate dictionary cycle", "class Bound[T]{fn bound(n:T):Bool}\ninstance Limit:Bound[Int]{fn bound(n:Int):Bool{Value>0}}\npred valid[T:Bound](n:T){bound(n)}\ntype C={n:Int where valid} derive(Decode)\nlazy Value:Int={_=json.Decode[C](\"{\\\"n\\\":1}\");1}\nfn main(){}", "dependency cycle"},
		{"decoder element predicate cycle", "class Bound[T]{fn bound(n:T):Bool}\ninstance Limit:Bound[Int]{fn bound(n:Int):Bool{Value>0}}\npred valid[T:Bound](n:T){bound(n)}\ntype C={ns:List[Int where valid]} derive(Decode)\nlazy Value:Int={_=json.Decode[C](\"{\\\"ns\\\":[1]}\");1}\nfn main(){}", "dependency cycle"},
		{"instance predicate", "class Bound[T]{fn bound(n:T):Bool}\ninstance Limit:Bound[Int]{fn bound(n:Int):Bool{n>Value}}\nlazy Value=1\npred good[T:Bound](n:T){bound(n)}\nfn main(){n:Int where good=3;println(n)}", "not proven"},
		{"computed helper cycle", "lazy Value:Int=read()\ntype C={n:Int,lazy value:Int=n+Value}\nfn read():Int{C{n:0}.value}\nfn main(){}", "dependency cycle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := fixtureDir(t)
			source := tc.source
			if strings.Contains(source, "json.") {
				source = "import \"bork/json\"\n" + source
			}
			if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0o644); err != nil {
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

func TestPackageLazyImports(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ source, want string }{
		{"import \"example.com/package-lazy/values\"\nlazy Local=values.Value\nfn main(){println(Local)}", ""},
		{"import \"example.com/package-lazy/values\"\nfn main(){println(values.hidden)}", "not exported"},
		{"import \"example.com/package-lazy/values\"\nfn main(){println(comptime{values.Value})}", ""},
	} {
		t.Run(tc.source, func(t *testing.T) {
			t.Parallel()
			dir := fixtureDir(t)
			if err := os.Mkdir(filepath.Join(dir, "values"), 0o755); err != nil {
				t.Fatal(err)
			}
			for path, source := range map[string]string{
				"bork.mod":           "module example.com/package-lazy\n",
				"main.bork":          tc.source,
				"values/first.bork":  "lazy Value=Later+1\nlazy hidden=0\n",
				"values/second.bork": "lazy Later=20\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, path), []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
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

func TestDescribePackageLazyNeverForces(t *testing.T) {
	t.Parallel()
	source := "lazy Unread:Int=panic(\"must not force\")\nlazy Answer=21\nlazy Twice=Answer*2\nfn main(){println(Unread)}"
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, position := range []string{"1:1", "1:6", "4:19"} {
		result, err := Describe(fmt.Sprintf("%s:%s", path, position), "")
		if err != nil {
			t.Fatal(err)
		}
		if result.Type != "Int" || result.Lazy == nil || result.Lazy.Kind != "package binding" || result.Lazy.Effects != "nothing" {
			t.Fatalf("%+v", result)
		}
	}
	result, err := Describe(fmt.Sprintf("%s:3:6", path), "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Lazy == nil || strings.Join(result.Lazy.Dependencies, ",") != "Answer" {
		t.Fatalf("%+v", result)
	}
}
