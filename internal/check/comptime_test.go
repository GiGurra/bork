package check

import (
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
)

func TestComptimeStaticChecks(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"scalar", "fn f():Int {comptime {1+2}}", ""},
		{"expected width", "fn f():Int8 {comptime {127}}", ""},
		{"expected overflow", "fn f():Int8 {comptime {128}}", "does not fit"},
		{"block return", "fn f():Int {comptime {if(true){return 1};2}}", ""},
		{"local return type", "fn f():String {n=comptime {return 1};s\"$n\"}", ""},
		{"typed return", "fn f():Int8 {comptime {return 128}}", "does not fit"},
		{"bare return", "fn f():Int {comptime {return}}", "comptime block must return a value"},
		{"literal capture", "fn f():Int {n=2;comptime {n+3}}", ""},
		{"literal alias", "fn f():Int {n=2;other=n;comptime {other+3}}", ""},
		{"list capture", "fn f():Int {xs=[1,2];comptime {xs.length()}}", ""},
		{"record capture", "type R={value:Int}\nfn f():Int{r=R{value:3};comptime{r.value}}", ""},
		{"dependent block", "fn f():Int {n=comptime {2};comptime {n+3}}", ""},
		{"nested block", "fn f():Int {comptime {n=comptime {2};n+3}}", ""},
		{"runtime parameter", "fn f(n:Int):Int {comptime {n+3}}", "cannot capture runtime value n"},
		{"runtime call", "fn id(n:Int):Int{n}\nfn f():Int {n=id(2);comptime {n+3}}", "cannot capture runtime value n"},
		{"lazy capture", "fn f():Int {lazy n=2;comptime {n+3}}", "cannot capture runtime value n"},
		{"async capture", "fn f():Int {scope s {async(s) n=2;comptime {n+3}}}", "cannot capture runtime value n"},
		{"lazy field capture", "type C={lazy value:Int}\nfn f():Int{c=C{value:1};comptime{c.value}}", "cannot capture runtime value c"},
		{"lazy default capture", "type C={lazy value:Int=1}\nfn f():Int{c=C{};comptime{c.value}}", "cannot capture runtime value c"},
		{"nested lazy field capture", "type C={lazy value:Int}\ntype R={child:C}\nfn f():Int{r=R{child:C{value:1}};comptime{r.child.value}}", "cannot capture runtime value r"},
		{"sealed lazy field capture", "type C=sealed{V{lazy value:Int}}\nfn f():C{c=C.V{value:1};comptime{c}}", "cannot capture runtime value c"},
		{"lazy field list capture", "type C={lazy value:Int}\nfn f():Int{xs=[C{value:1}];comptime{xs.length()}}", "cannot capture runtime value xs"},
		{"fresh lazy field scalar", "type C={lazy value:Int}\nfn f():Int{comptime{c=C{value:1};c.value}}", ""},
		{"loop capture", "fn f(){for(n in [1]){x=comptime{n};println(x)}}", "cannot capture runtime value n"},
		{"ambient capture", "ambient n:Int\nfn f() needs n:Int {comptime {n}}", "cannot capture ambient n"},
		{"ambient callee", "ambient n:Int\nfn read() needs n:Int{n}\nfn f() needs n:Int {comptime {read()}}", "with ambient needs"},
		{"ambient function reference", "ambient n:Int\nfn read() needs n:Int{n}\nfn f() needs n:Int {comptime {cb=read; cb()}}", "with ambient needs"},
		{"ambient higher-order reference", "ambient n:Int\nfn read(x:Int) needs n:Int{x+n}\nfn f() needs n:Int {comptime {[1].map(read).length()}}", "with ambient needs"},
		{"phantom sealed result", "type R[T]=sealed{V{n:Int}}\nfn make[T]():R[T]{R[T].V{n:1}}\nfn f[T]():R[T]{comptime{make()}}", "requires concrete types"},
		{"phantom generic result", "type R[T]={n:Int}\nfn make[T]():R[T]{R[T]{n:1}}\nfn f[T]():R[T]{comptime{make()}}", "requires concrete types"},
		{"impure call", "fn work() uses io:Int{println(1);1}\nfn f():Int{comptime{work()}}", "requires pure code, found uses io"},
		{"main still impure", "fn work() uses io:Int{println(1);1}\nfn main(){n=comptime{work()};println(n)}", "requires pure code, found uses io"},
		{"lambda effect", "fn f():Int{comptime{[1].map(n=>{println(n);n}).length()}}", "requires pure code"},
		{"open callback", "fn f(cb:()=>Int):Int {comptime {cb()}}", "cannot capture runtime value cb"},
		{"generic result", "fn f[T](n:T):T{comptime {n}}", "cannot capture runtime value n"},
		{"generic capture", "fn f[T]():Int{xs:List[T]=[];comptime {xs.length()}}", "cannot capture runtime value xs"},
		{"generic generator recipe", "fn f[T]():Int{comptime{_=generate[T]{};1}}", "requires concrete types"},
		{"generic recipe", "fn id[T](n:Int):Int{n}\nfn f[T]():Int{comptime{id[T](1)}}", "requires concrete types"},
		{"function result", "fn f():()=>Int{comptime {()=>1}}", "cannot bake result type"},
		{"nested function result", "type R={f:()=>Int}\nfn make():R{comptime {R{f:()=>1}}}", "field f: unsupported"},
		{"lazy field result", "type C={lazy value:Int}\nfn f():C{comptime{C{value:1}}}", "field value: lazy cell"},
		{"nested lazy field result", "type C={lazy value:Int}\ntype R={child:C}\nfn f():R{comptime{R{child:C{value:1}}}}", "field child: field value: lazy cell"},
		{"sealed lazy field result", "type C=sealed{V{lazy value:Int}}\nfn f():C{comptime{C.V{value:1}}}", "variant V field value: lazy cell"},
		{"cross loop boundary", "fn f(){for(_ in [1]){x=comptime{break;1};println(x)}}", "require a loop in the same function"},
		{"cross generator boundary", "fn f(){x=generate[Int]{n=comptime{yield 1;2};yield n};println(x.toList())}", "yield requires a generator"},
		{"keyword name", "fn comptime(n:Int):Int{n}\nfn f():Int{comptime(1)}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diags := &diag.List{}
			files := prelude.Parse(diags)
			file := syntax.Parse("test.bork", []byte(tc.source), diags)
			file.Package = "example.com/test"
			files = append(files, file)
			info := Program(files, file.Package, diags, nil)
			if tc.want == "" {
				if diags.Len() != 0 {
					t.Fatal(diags.Error())
				}
				CheckEffects(files, info, diags)
				if diags.Len() != 0 {
					t.Fatal(diags.Error())
				}
				if tc.name == "expected width" && (len(info.Comptimes) != 1 || info.Comptimes[0].Type() != Int8) {
					t.Fatalf("expected Int8 computation: %+v", info.Comptimes)
				}
			} else if !strings.Contains(diags.Error(), tc.want) {
				t.Fatalf("want %q, got %s", tc.want, diags.Error())
			}
		})
	}
}
