package driver

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAsyncBindingChecks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"compile time candidate", "pred p(x:Int){scope s{async(s) y=x;y>0}}\nfn main(){n:Int where p=1;println(n)}", "cannot schedule an async initializer"},
		{"unused effects", "fn work() uses io:Int{println(1);1}\nfn f(){scope s{async(s) ignored=work();_=()=>ignored}}", "uses io"},
		{"owner effects", "fn owner(s:Scope) uses state:Scope{_ = checkpoint(s);s}\nfn f(){scope s{async(owner(s)) ignored=1;_=()=>ignored}}", "uses state"},
		{"non scope", "fn main(){async(1) x=2;println(x)}", "async requires a Scope"},
		{"predicate try failure", "type Failed={}\npred Success(x:Int|Failed){x!=Failed{}}\nfn bad(flag:Bool):Int|Failed{if(flag){Failed{}}else{1}}\nfn make(flag:Bool):Int|Failed{scope s{async(s) value:(Int|Failed) where Success={_=bad(flag)?;1};value}}", "is false"},

		{"predicate normal failure", "type Failed={}\npred Success(x:Int|Failed){x!=Failed{}}\nfn make():Int|Failed{scope s{async(s) value:(Int|Failed) where Success=Failed{};value}}", "is false"},
		{"predicate explicit failure", "type Failed={}\npred Success(x:Int|Failed){x!=Failed{}}\nfn make(flag:Bool):Int|Failed{scope s{async(s) value:(Int|Failed) where Success={if(flag){return Failed{}};1};value}}", "is false"},
		{"predicate option failure", "pred Success(x:Option[Int]){match(x){Option.Some{value:_}=>true,Option.None=>false}}\nfn make(x:Option[Int]):Option[Int]{scope s{async(s) value:Option[Int] where Success={n=x?;Option.Some{value:n}};value}}", "is false"},
		{"predicate nested lazy failure", "type Failed={}\npred Success(x:Int|Failed){x!=Failed{}}\nfn bad(flag:Bool):Int|Failed{if(flag){Failed{}}else{1}}\nfn make(flag:Bool):Int|Failed{scope s{async(s) value:(Int|Failed) where Success={lazy inner=bad(flag);_=inner?;1};value}}", "is false"},
		{"predicate nested async failure", "type Failed={}\npred Success(x:Int|Failed){x!=Failed{}}\nfn bad(flag:Bool):Int|Failed{if(flag){Failed{}}else{1}}\nfn make(flag:Bool):Int|Failed{scope s{async(s) value:(Int|Failed) where Success={async(s) inner=bad(flag);_=inner?;1};value}}", "is false"},
		{"eager union literal type", "type Failed={}\npred Valid(x:Int|Failed){match(x){n:Int=>n>0,_:Failed=>true}}\nfn main(){value:(Int|Failed) where Valid=1;println(value)}", ""},
		{"predicate preserves parameter fact", "type Failed={}\npred Valid(x:Int|Failed){match(x){n:Int=>n>0,_:Failed=>true}}\nfn make(x:(Int|Failed) where Valid):Int|Failed{scope s{async(s) value:(Int|Failed) where Valid={n=x?;n};value}}", ""},
		{"predicate missing source fact", "type Failed={}\npred Valid(x:Int|Failed){match(x){n:Int=>n>0,_:Failed=>true}}\nfn make(x:Int|Failed):Int|Failed{scope s{async(s) value:(Int|Failed) where Valid={n=x?;n};value}}", "not proven"},
		{"predicate preserves guard fact", "type Failed={}\npred Valid(x:Int|Failed){match(x){n:Int=>n>0,_:Failed=>true}}\nfn make(x:Int|Failed):Int|Failed{if(Valid(x)){scope s{async(s) value:(Int|Failed) where Valid={n=x?;n};value}}else{Failed{}}}", ""},
		{"predicate allowed failure", "type Failed={}\npred Valid(x:Int|Failed){match(x){n:Int=>n>0,_:Failed=>true}}\nfn bad(flag:Bool):Int|Failed{if(flag){Failed{}}else{1}}\nfn make(flag:Bool):Int|Failed{scope s{async(s) value:(Int|Failed) where Valid={_=bad(flag)?;1};value}}", ""},
		{"typed proof", "pred positive(x:Int){x>0}\nfn f(flag:Bool):Int{scope s{async(s) x:Int where positive={if(flag){return -1};1};x}}", "is false"},
		{"inferred proof", "pred positive(x:Int){x>0}\nfn f(x:Int where positive):Int where positive{scope s{async(s) y=x;y}}", ""},
		{"self reference", "fn main(){scope s{async(s) x=x;println(x)}}", "undefined: x"},
		{"drop", "fn main(){scope s{async(s) _=1}}", "single binding name"},
		{"package", "async(s) x=1\nfn main(){}", "package async bindings are not supported"},
		{"cross loop", "fn main(){scope s{for(_ in [1]){async(s) x:Int={break;1};println(x)}}}", "require a loop in the same function"},
		{"cross yield", "fn main(){scope s{x=generate[Int]{async(s) y:Int={yield 1;2};yield y};println(x.toList())}}", "yield requires a generator"},
		{"option annotation", "fn main(){scope s{async(s) x={n=Option.Some{value:1}?;n};println(x)}}", "async initializer needs a result type annotation"},
		{"owner result", "fn main(){scope s{async(s) owner=openScope(s);closeScope(owner)}}", "async initializer cannot return an OwnedScope"},
		{"owner capture", "fn main(){scope s{owner=openScope(s);async(s) x={closeScope(owner);1};closeScope(owner);println(x)}}", "lambda cannot close or pass on"},
		{"short capture", "fn read(s:Scope) uses state:Int{_ = checkpoint(s);1}\nfn main(){scope outer{scope inner{async(outer) x=read(inner);println(x)}}}", "async initializer may not live as long"},
		{"short cell capture", "fn main(){scope outer{scope inner{async(inner) x=1;async(outer) y=x;println(y)}}}", "async initializer may not live as long"},
		{"released scalar", "fn main(){scope s{owner=openScope(s);async(owner.scope) x=1;closeScope(owner);println(x)}}", "may be released"},
		{"scalar closure escape", "fn make():()=>Int{scope s{async(s) x=1;()=>x}}", "cannot return this value"},
		{"forced closure escape", "fn make():()=>Int{scope s{async(s) x=1;_ = x;()=>x}}", "cannot return this value"},
		{"scalar value escape", "fn make():Int{scope s{async(s) x=1;x}}", ""},
		{"keyword function", "fn async(x:Int):Int{x}\nfn main(){scope s{async(s) x=async(1);println(x)}}", ""},
		{"owner unconditional return", "fn f():Int{scope s{async(return 7) x=1}}", ""},
		{"resolved channel escape", "fn main(){scope outer{ch=channel[Int](outer,1);copy=scope inner{async(inner) value=ch;value};_ = send(copy,7);println(receive(copy))}}", ""},
		{"resolved channel cell escape", "fn make(s:Scope):()=>Channel[Int]{ch=channel[Int](s,1);scope inner{async(inner) value=ch;()=>value}}", "cannot return this value"},
		{"released channel cell", "fn main(){scope s{ch=channel[Int](s,1);owner=openScope(s);async(owner.scope) value=ch;closeScope(owner);println(receive(value))}}", "may be released"},
		{"owner early return", "fn f():Int{scope s{async({if(true){return 7};s}) x=1;x}}", ""},
		{"owner early try", "type Failed={}\nfn choice(s:Scope):Scope|Failed{s}\nfn f():Int|Failed{scope s{async(choice(s)?) x=1;x}}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(dir)
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

func TestDescribeAsyncNeverStarts(t *testing.T) {
	t.Parallel()
	source := "fn work(n:Int) uses io:Int{println(n);n}\nfn main(){n=7\n scope s {\n async(s) x=work(n)\n println(x)\n }}"
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, position := range []string{"4:2", "4:11", "5:10"} {
		result, err := Describe(fmt.Sprintf("%s:%s", path, position), "")
		if err != nil {
			t.Fatal(err)
		}
		if result.Type != "Int" || result.Async == nil || result.Lazy != nil || result.Async.Scope != "s" || result.Async.Effects != "io" || strings.Join(result.Async.Captures, ",") != "n" {
			t.Fatalf("%+v", result)
		}
	}
}

// Exercise generated scope, initializer and memo code together under -race.
func TestAsyncBindingRace(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds and runs a Go race executable")
	}
	path := filepath.Join("..", "..", "testdata", "cases", "async_bindings")
	files, _, source, err := emit(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeGoModule(dir, files); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", "-race", "-mod=readonly", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("generated async bindings: %v\n%s\n%s", err, out, stderr.String())
	}
	compare(t, filepath.Join(path, "expected_output.txt"), string(out))
}
