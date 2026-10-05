package driver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
)

func TestLazyBindingChecks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"unused effects", "fn work() uses io: Int { println(1);1 }\nfn f(){ lazy ignored=work();_=()=>ignored }", "uses io"},
		{"typed proof", "pred positive(x:Int){x>0}\nfn f(flag:Bool):Int{lazy x:Int where positive={if(flag){return -1};1};x}", "is false"},
		{"inferred proof", "pred positive(x:Int){x>0}\nfn f(x:Int where positive):Int where positive {lazy y=x;y}", ""},
		{"validator context", "type C={x:Int} where Valid\npred Valid(c:C){lazy proof=requiresValid(c);_=()=>proof;true}\nfn requiresValid(c:C where Valid):Int{1}\nfn main(){println(C{x:-1})}", "invariant is unavailable"},
		{"nested validator context", "type C={x:Int} where Valid\npred Valid(c:C){lazy proof={lazy inner=requiresValid(c);inner};_=()=>proof;true}\nfn requiresValid(c:C where Valid):Int{1}\nfn main(){println(C{x:-1})}", "invariant is unavailable"},
		{"pure constant candidate", "pred p(x:Int){lazy value=x;value>0}\nfn main(){n:Int where p=1;println(n)}", ""},
		{"pure unused panic candidate", "pred p(x:Int){lazy unused:Int=panic(\"unused\");_=()=>unused;x>0}\nfn main(){n:Int where p=1;println(n)}", ""},
		{"pure invalid constant candidate", "pred p(x:Int){lazy value=x;value>0}\nfn main(){n:Int where p=-1;println(n)}", "is false"},
		{"self reference", "fn main(){lazy x=x;println(x)}", "undefined: x"},
		{"forward reference", "fn main(){lazy x=y;lazy y=x;println(y)}", "undefined: y"},
		{"drop", "fn main(){lazy _=1}", "single binding name"},
		{"destructure", "fn main(){lazy (x,y)=(1,2)}", "expected"},
		{"independent field", "type C={lazy value:Int}\nfn main(){}", ""},
		{"package binding", "lazy x=1\nfn main(){}", ""},
		{"cross loop", "fn main(){for(_ in [1]){lazy x:Int={break;1};println(x)}}", "require a loop in the same function"},
		{"cross yield", "fn main(){x=generate[Int]{lazy y:Int={yield 1;2};yield y};println(x.toList())}", "yield requires a generator"},
		{"nested lambda return", "fn main(){lazy x:()=>Int=()=>{return 1};println(x())}", "return cannot be used in a lambda"},
		{"nested lambda try", "fn main(){n=1000;lazy x:()=>Int8=()=>toInt8(n)?;println(x())}", "? cannot be used in a lambda"},
		{"owner result", "fn main(){scope s{lazy owner=openScope(s);closeScope(owner)}}", "lazy initializer cannot return an OwnedScope"},
		{"owner consumption", "fn main(){scope s{owner=openScope(s);lazy x={closeScope(owner);1};closeScope(owner);println(x)}}", "lambda cannot close or pass on"},
		{"released scalar capture", "fn read(s:Scope) uses state:Int{_ = checkpoint(s);1}\nfn main(){scope s{owner=openScope(s);lazy x=read(owner.scope);closeScope(owner);println(x)}}", "may be released"},
		{"scalar closure escape", "fn read(s:Scope) uses state:Int{_ = checkpoint(s);1}\nfn make() uses state:()=>Int {scope s{lazy x=read(s);()=>x}}", "cannot return this value"},
		{"forced scalar escape", "fn read(s:Scope) uses state:Int{_ = checkpoint(s);1}\nfn make() uses state:Int {scope s{lazy x=read(s);x}}", ""},
		{"closure retains forced capture", "fn read(s:Scope) uses state:Int{_ = checkpoint(s);1}\nfn make() uses state:()=>Int {scope s{lazy x=read(s);_ = x;()=>x}}", "cannot return this value"},
		{"inner scoped result", "type R=resource\nfn open(s:Scope):R unsafe go{return R{}}\nfn main(){lazy x=scope s{open(s)};println(x)}", "belongs to scope s"},
		{"binding keyword name", "fn lazy(x:Int):Int{x}\nfn main(){lazy x=lazy(1);println(x)}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := fixtureDir(t)
			if err := os.WriteFile(filepath.Join(dir, "bork.mod"), []byte("module example.com/lazytest\nunsafe \"example.com/lazytest\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
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

func TestDescribeLazyNeverForces(t *testing.T) {
	t.Parallel()
	source := "fn work(n:Int) uses io:Int{println(n);n}\nfn main(){n=7\n lazy x=work(n)\n println(x)}"
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, position := range []string{"3:2", "3:7", "4:10"} {
		result, err := Describe(fmt.Sprintf("%s:%s", path, position), "")
		if err != nil {
			t.Fatal(err)
		}
		if result.Type != "Int" || result.Lazy == nil || result.Lazy.Effects != "io" || strings.Join(result.Lazy.Captures, ",") != "n" {
			t.Fatalf("%+v", result)
		}
		output, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(output), `"initializer_effects":"io"`) {
			t.Fatal(string(output))
		}
	}
}

func TestImmediateLazyWarning(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		source string
		warn   bool
	}{
		{"fn main(){lazy x=1;println(x)}", true},
		{`fn main(){lazy x:Int=panic("failure");println(x)}`, false},
		{"fn main(){lazy x=1;y=x;println(y)}", true},
		{"fn main(){lazy x={return 1};println(x)}", false},
		{"fn main(){lazy x=1;f=()=>x;println(f())}", false},
		{"fn main(){lazy x=1;if(true){println(x)}}", false},
		{"fn work()uses io:Int{println(1);1}\nfn main(){lazy x=work();println(x)}", false},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "main.bork")
		if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
			t.Fatal(err)
		}
		_, info, err := Check(dir)
		if err != nil {
			t.Fatal(err)
		}
		warnings := check.LazyWarnings(info).Sorted()
		if (len(warnings) > 0) != tc.warn {
			t.Fatalf("%s: %+v", tc.source, warnings)
		}
		if tc.warn {
			if len(warnings[0].Fixes) != 1 || warnings[0].Code != "lazy.immediate-force" {
				t.Fatalf("%+v", warnings)
			}
			offset := func(pos diag.Pos) int {
				lines := strings.SplitAfter(tc.source, "\n")
				off := pos.Col - 1
				for _, line := range lines[:pos.Line-1] {
					off += len(line)
				}
				return off
			}
			edit := warnings[0].Fixes[0].Edits[0]
			fixed := tc.source[:offset(edit.Start)] + edit.Replacement + tc.source[offset(edit.End):]
			if err := os.WriteFile(path, []byte(fixed), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Check(dir); err != nil {
				t.Fatalf("invalid fix: %v", err)
			}
		}
	}
}
