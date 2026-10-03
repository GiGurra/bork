package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAsyncBindingChecks(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"compile time candidate", "pred p(x:Int){scope s{async(s) y=x;y>0}}\nfn main(){n:Int where p=1;println(n)}", "cannot schedule an async initializer"},
		{"unused effects", "fn work() uses io:Int{println(1);1}\nfn f(){scope s{async(s) ignored=work()}}", "uses io"},
		{"owner effects", "fn owner(s:Scope) uses state:Scope{_ = checkpoint(s);s}\nfn f(){scope s{async(owner(s)) ignored=1}}", "uses state"},
		{"non scope", "fn main(){async(1) x=2;println(x)}", "async requires a Scope"},
		{"typed proof", "pred positive(x:Int){x>0}\nfn f(flag:Bool):Int{scope s{async(s) x:Int where positive={if(flag){return -1};1};x}}", "is false"},
		{"inferred proof", "pred positive(x:Int){x>0}\nfn f(x:Int where positive):Int where positive{scope s{async(s) y=x;y}}", ""},
		{"self reference", "fn main(){scope s{async(s) x=x;println(x)}}", "undefined: x"},
		{"drop", "fn main(){scope s{async(s) _=1}}", "single binding name"},
		{"package", "async(s) x=1\nfn main(){}", "package async bindings are not supported"},
		{"cross loop", "fn main(){scope s{for(n in [1]){async(s) x:Int={break;1};println(x)}}}", "require a loop in the same function"},
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
		{"owner early return", "fn f():Int{scope s{async({if(true){return 7};s}) x=1;x}}", ""},
		{"owner early try", "type Failed={}\nfn choice(s:Scope):Scope|Failed{s}\nfn f():Int|Failed{scope s{async(choice(s)?) x=1;x}}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
