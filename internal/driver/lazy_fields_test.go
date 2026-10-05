package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLazyFieldChecks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"computed default", "type C={first:Int,lazy doubled:Int=first*2}\nfn main(){}", ""},
		{"positive generic argument", "pred positive(x:Int){x>0}\ntype Lazy[T]={lazy value:T}\nfn one(x:Int):Int where positive{1}\nfn f(flag:Bool):Lazy[Int]{Lazy[Int where positive]{value:one({if(flag){return 2};0})}}", ""},
		{"inferred return", "type Lazy[T]={lazy value:T}\nfn main(){x=Lazy{value:{if(true){return 1};2}};println(x.value)}", ""},
		{"inferred return only", "type Lazy[T]={lazy value:T}\nfn main(){x=Lazy{value:{return 1}};println(x.value)}", ""},
		{"rigid return", "type Lazy[T]={lazy value:T}\nfn f[T]():Lazy[T]{Lazy[T]{value:{return 1}}}", "lazy initializer must return T"},
		{"whole predicate try failure", "type Failed={}\npred Success(x:Int|Failed){match(x){_:Int=>true,_:Failed=>false}}\nfn bad(flag:Bool):Int|Failed{if(flag){Failed{}}else{1}}\ntype C={lazy value:Int|Failed} where Valid\npred Valid(c:C){Success(c.value)}\nfn make(flag:Bool):C{C{value:{x=bad(flag)?;x}}}", "is false"},
		{"whole predicate option failure", "pred Success(x:Option[Int]){match(x){Option.Some{value:_}=>true,Option.None=>false}}\ntype C={lazy value:Option[Int]} where Valid\npred Valid(c:C){Success(c.value)}\nfn make(x:Option[Int]):C{C{value:{n=x?;Option.Some{value:n}}}}", "is false"},
		{"field predicate try failure", "type Failed={}\npred Success(x:Int|Failed){x!=Failed{}}\nfn bad(flag:Bool):Int|Failed{if(flag){Failed{}}else{1}}\ntype C={lazy value:(Int|Failed) where Success}\nfn make(flag:Bool):C{C{value:{_=bad(flag)?;1}}}", "is false"},
		{"generic predicate try failure", "type Failed={}\npred Success(x:Int|Failed){x!=Failed{}}\nfn bad(flag:Bool):Int|Failed{if(flag){Failed{}}else{1}}\ntype Lazy[T]={lazy value:T}\nfn make(flag:Bool):Lazy[Int|Failed]{Lazy[(Int|Failed) where Success]{value:{_=bad(flag)?;1}}}", "is false"},
		{"copy predicate try failure", "type Failed={}\npred Success(x:Int|Failed){x!=Failed{}}\nfn bad(flag:Bool):Int|Failed{if(flag){Failed{}}else{1}}\ntype C={lazy value:(Int|Failed) where Success}\nfn make(c:C,flag:Bool):C{c.copy(value:{_=bad(flag)?;1})}", "is false"},
		{"local predicate try failure", "type Failed={}\npred Success(x:Int|Failed){x!=Failed{}}\nfn bad(flag:Bool):Int|Failed{if(flag){Failed{}}else{1}}\nfn make(flag:Bool):Int|Failed{lazy value:(Int|Failed) where Success={_=bad(flag)?;1};value}", "is false"},
		{"field union matching positive", "type Failed={}\npred Valid(x:Int|Failed){match(x){n:Int=>n>0,_:Failed=>true}}\nfn bad(flag:Bool):Int|Failed{if(flag){Failed{}}else{1}}\ntype C={lazy value:(Int|Failed) where Valid}\nfn make(flag:Bool):C{C{value:{_=bad(flag)?;1}}}", ""},
		{"generic union matching positive", "type Failed={}\npred Valid(x:Int|Failed){match(x){n:Int=>n>0,_:Failed=>true}}\nfn bad(flag:Bool):Int|Failed{if(flag){Failed{}}else{1}}\ntype Lazy[T]={lazy value:T}\nfn make(flag:Bool):Lazy[Int|Failed]{Lazy[(Int|Failed) where Valid]{value:{_=bad(flag)?;1}}}", ""},
		{"constrained union parameter", "type Failed={}\npred Valid(x:Int|Failed){x!=Failed{}}\ntype Lazy[T]={lazy value:T}\nfn make(value:(Int|Failed) where Valid):Lazy[Int|Failed]{Lazy[(Int|Failed) where Valid]{value:{x=value?;x}}}", ""},
		{"guarded union parameter", "type Failed={}\npred Valid(x:Int|Failed){x!=Failed{}}\ntype Lazy[T]={lazy value:T}\nfn make(value:Int|Failed):Lazy[Int|Failed]{if(!Valid(value)){return Lazy{value:1}};Lazy[(Int|Failed) where Valid]{value:{x=value?;x}}}", ""},
		{"effects", "type C={lazy value:Int}\nfn work()uses io:Int{println(1);1}\nfn f():C{C{value:work()}}", "uses io"},
		{"return proof", "pred positive(x:Int){x>0}\ntype C={lazy value:Int where positive}\nfn f(flag:Bool):C{C{value:{if(flag){return -1};1}}}", "is false"},
		{"generic argument proof", "pred positive(x:Int){x>0}\ntype Lazy[T]={lazy value:T}\nfn f(flag:Bool):Lazy[Int]{Lazy[Int where positive]{value:{if(flag){return -1};1}}}", "is false"},
		{"generic argument nested return", "pred positive(x:Int){x>0}\ntype Lazy[T]={lazy value:T}\nfn one(x:Int):Int where positive{1}\nfn f(flag:Bool):Lazy[Int]{Lazy[Int where positive]{value:one({if(flag){return -1};0})}}", "is false"},
		{"whole predicate nested return", "pred positive(x:Int){x>0}\ntype C={lazy value:Int} where Valid\npred Valid(c:C){positive(c.value)}\nfn one(x:Int):Int where positive{1}\nfn f(flag:Bool):C{C{value:one({if(flag){return -1};0})}}", "is false"},
		{"copy return proof", "pred positive(x:Int){x>0}\ntype C={lazy value:Int where positive}\nfn f(c:C,flag:Bool):C{c.copy(value:{if(flag){return -1};1})}", "is false"},
		{"scope escape", "type C={lazy value:Int}\nfn read(s:Scope)uses state:Int{_=checkpoint(s);1}\nfn f()uses state:C{scope s{C{value:read(s)}}}", "cannot return this value"},
		{"scope list escape", "type C={lazy value:Int}\nfn read(s:Scope)uses state:Int{_=checkpoint(s);1}\nfn f()uses state:List[C]{scope s{[C{value:read(s)}]}}", "cannot return this value"},
		{"scalar escape", "type C={lazy value:Int}\nfn read(s:Scope)uses state:Int{_=checkpoint(s);1}\nfn f()uses state:Int{scope s{c=C{value:read(s)};c.value}}", ""},
		{"closed owner", "type C={lazy value:Int}\nfn read(s:Scope)uses state:Int{_=checkpoint(s);1}\nfn f()uses io+state{scope s{owner=openScope(s);c=C{value:read(owner.scope)};closeScope(owner);println(c.value)}}", "may be released"},
		{"owner capture", "type C={lazy value:Int}\nfn f()uses io+state{scope s{owner=openScope(s);c=C{value:{closeScope(owner);1}};println(c.value)}}", "lambda cannot close or pass on"},
		{"cross loop", "type C={lazy value:Int}\nfn main(){for(_ in [1]){c=C{value:{break;1}};println(c.value)}}", "require a loop in the same function"},
		{"validator exclusion", "type C={lazy value:Int} where Valid\ntype D={lazy value:Int}\npred Valid(c:C){_=D{value:trusted(c)};true}\nfn trusted(c:C where Valid):Int{1}\nfn main(){println(C{value:1})}", "invariant is unavailable"},
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
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q; got %v", tc.want, err)
			}
		})
	}
}

func TestDescribeLazyFieldNeverForces(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	source := "type C={lazy value:Int}\nfn work(n:Int)uses io:Int{println(n);n}\nfn main(){n=7;c=C{value:work(n)};println(c.value)}"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	line := strings.Split(source, "\n")[2]
	recipe, err := Describe(fmt.Sprintf("%s:3:%d", path, strings.Index(line, "work(n)")+1), "")
	if err != nil {
		t.Fatal(err)
	}
	if recipe.Lazy == nil || recipe.Lazy.Effects != "io" || strings.Join(recipe.Lazy.Captures, ",") != "n" {
		t.Fatalf("recipe: %+v", recipe)
	}
	read, err := Describe(fmt.Sprintf("%s:3:%d", path, strings.Index(line, "c.value")+3), "")
	if err != nil {
		t.Fatal(err)
	}
	if read.Lazy == nil || read.Type != "Int" || read.Lazy.Kind != "independent field" {
		t.Fatalf("read: %+v", read)
	}
}
