package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComputedFieldChecks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"computed function equality", "type C={n:Int,lazy action:()=>Int=()=>n}\nfn f(a:C,b:C):Bool{a==b}", ""},
		{"computed function keys", "type C={n:Int,lazy action:()=>Int=()=>n}\nfn f(c:C):Map[C,Int]{{c:1}}", ""},
		{"computed generic equality", "type C[T]=sealed{Item{n:Int,lazy action:()=>T=()=>panic(s\"${n}\")}}\nfn f(a:C[()=>Int],b:C[()=>Int]):Bool{a==b}", ""},
		{"known computed string", "pred valid(s:String){s==\"hi Joe\"}\ntype C={name:String,lazy label:String where valid=s\"hi ${name}\"}\nfn f():C{C{name:\"Joe\"}}", ""},
		{"invalid known computed string", "pred valid(s:String){s==\"hi Joe\"}\ntype C={name:String,lazy label:String where valid=s\"hi ${name}\"}\nfn f():C{C{name:\"Ann\"}}", "valid"},
		{"known computed invariant", "pred valid(c:C){c.label==\"hi Joe\"}\ntype C={name:String,lazy label:String=s\"hi ${name}\"} where valid\nfn f():C{C{name:\"Joe\"}}", ""},
		{"computed scope escape", "type C={callback:()=>Int,lazy value:Int=callback()}\nfn read(s:Scope):Int{1}\nfn make():C{scope s{C{callback:()=>read(s)}}}\nfn main(){}", "scope"},
		{"computed generic promise", "pred positive(n:Int){n>0}\ntype C[T]={item:T,lazy repeated:T=item}\nfn make():C[Int]{C[Int where positive]{item:-1}}", "positive"},
		{"computed option failure", "pred valid(x:Option[Int]){match(x){Option.Some(_)=>true,Option.None=>false}}\ntype C={source:Option[Int],lazy value:Option[Int] where valid={n=source?;Option.Some(n)}}\nfn make(x:Option[Int]):C{C{source:x}}", "valid"},
		{"computed comptime result", "type C={n:Int,lazy value:Int=n+1}\nfn main(){c=comptime{C{n:1}};println(c.n)}", "lazy cell"},
		{"computed comptime capture", "type C={n:Int,lazy value:Int=n+1}\nfn main(){c=C{n:1};println(comptime{c.value})}", "cannot capture runtime value"},
		{"nested computed comptime result", "type C={n:Int,lazy value:Int=n+1}\ntype D={child:C}\nfn main(){c=comptime{D{child:C{n:1}}};println(c.child.n)}", "lazy cell"},
		{"forward", "type C={lazy twice:Int=n*2,n:Int}\nfn f():C{C{n:3}}", ""},
		{"transitive", "type C={n:Int,lazy twice:Int=n*2,lazy more:Int=twice+1}\nfn f():C{C{n:3}}", ""},
		{"generic", "type C[T]={n:T,lazy value:T=n}\nfn f():C[Int]{C[Int]{n:3}}", ""},
		{"generic explicit types", "fn id[T](n:T):T{n}\ntype C[T]={n:T,lazy value:T=id[T](n)}\nfn f():C[Int]{C[Int]{n:3}}", ""},
		{"self cycle", "type C={lazy value:Int=value+1}\nfn main(){}", "dependency cycle: value -> value"},
		{"mutual cycle", "type C={lazy a:Int=b,lazy b:Int=a}\nfn main(){}", "dependency cycle"},
		{"supplied computed", "type C={n:Int,lazy value:Int=n+1}\nfn main(){c=C{n:1,value:2};println(c)}", "cannot be supplied"},
		{"copy computed", "type C={n:Int,lazy value:Int=n+1}\nfn f(c:C):C{c.copy(value:2)}", "cannot be updated"},
		{"nested computed copy", "type D={n:Int}\ntype C={child:D,lazy derived:D=child}\nfn f(c:C):C{c.copy(derived.n:2)}", "cannot be updated"},
		{"impure default", "fn work(n:Int)uses io:Int{println(n);n}\ntype C={n:Int,lazy value:Int=work(n)}\nfn main(){}", "must be pure"},
		{"closed expression without dependency", "type C={lazy value:Int=1+2}\nfn main(){}", "must be a closed value"},
		{"lexical shadow", "type C={n:Int,lazy value:Int={n=2;n+1}}\nfn main(){}", "must be a closed value"},
		{"invalid computed promise", "pred positive(n:Int){n>0}\ntype C={n:Int,lazy value:Int where positive=n+1}\nfn f():C{C{n:-3}}", "positive"},
		{"invalid copy promise", "pred positive(n:Int){n>0}\ntype C={n:Int,lazy value:Int where positive=n+1}\nfn f(c:C):C{c.copy(n:-3)}", "positive"},
		{"early result promise", "pred positive(n:Int){n>0}\ntype C={n:Int,lazy value:Int where positive={if(n==1){return -1};2}}\nfn f(n:Int):C{C{n:n}}", "positive"},
		{"unguarded default call", "pred positive(n:Int){n>0}\nfn require(n:Int where positive):Int{n}\ntype C={n:Int,lazy value:Int=require(n)}\nfn f():C{C{n:1}}", "to be positive"},
		{"guarded default call", "pred positive(n:Int){n>0}\nfn require(n:Int where positive):Int{n}\ntype C={n:Int where positive,lazy value:Int=require(n)}\nfn f():C{C{n:1}}", ""},
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

func TestDescribeComputedFieldNeverForces(t *testing.T) {
	t.Parallel()
	source := "type C={n:Int,lazy value:Int=if(n==0){panic(\"must not run\")}else{n}}\nfn main(){c=C{n:0};println(c.value)}"
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	line := strings.Split(source, "\n")[1]
	read, err := Describe(fmt.Sprintf("%s:2:%d", path, strings.Index(line, "c.value")+3), "")
	if err != nil {
		t.Fatal(err)
	}
	if read.Lazy == nil || read.Type != "Int" || read.Lazy.Kind != "computed field" || read.Lazy.Effects != "nothing" || strings.Join(read.Lazy.Dependencies, ",") != "n" {
		t.Fatalf("read: %+v", read)
	}
}
