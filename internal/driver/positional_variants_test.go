package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPositionalVariantsRejected(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"empty declaration", `type A=sealed{Empty()}`, "needs at least one payload type"},
		{"too few values", `type A=sealed{Pair(Int,String)};fn main(){println(A.Pair(1))}`, "needs 2 payload values"},
		{"too many values", `fn main(){println(Option.Some(1,2))}`, "needs 1 payload values"},
		{"named argument", `fn main(){println(Option.Some(value:1))}`, "do not accept named arguments"},
		{"fieldless call", `fn main(){println(Option[Int].None())}`, "no positional payload"},
		{"named variant call", `type A=sealed{Value{value:Int}};fn main(){println(A.Value(1))}`, "no positional payload"},
		{"positional braces", `fn main(){println(Option.Some{value:1})}`, "uses positional payloads"},
		{"variant type arguments", `fn main(){println(Option.Some[Int](1))}`, "type arguments belong on the variant owner"},
		{"payload type", `fn main(){println(Option[Int].Some("bad"))}`, "payload 0"},
		{"mixed pattern", `type A=sealed{Pair(Int,Int)};fn f(x:A):Int{match(x){.Pair{bogus:missing}(a,b)=>a+b}}`, "cannot mix"},
		{"pattern arity", `fn f(x:Option[Int]):Int{match(x){.Some(a,b)=>a,.None=>0}}`, "needs 1 payload patterns"},
		{"fieldless pattern call", `fn f(x:Option[Int]):Int{match(x){.Some(_)=>1,.None()=>0}}`, "no positional payload"},
		{"named pattern parentheses", `type A=sealed{Value{value:Int}};fn f(x:A):Int{match(x){.Value(n)=>n}}`, "no positional payload"},
		{"legacy pattern", `fn f(x:Option[Int]):Int{match(x){.Some{value:n}=>n,.None=>0}}`, "uses positional payloads"},
		{"refutable payload", `type A=sealed{Value(Bool),Empty};fn f(x:A):Int{match(x){.Value(true)=>1,.Empty=>0}}`, "missing A.Value(false)"},
		{"unproven constraint", `pred positive(x:Int){x>0};type A=sealed{Value(Int where positive)};fn f(n:Int):A{.Value(n)}`, "not proven"},
		{"is binding", `fn f(x:Option[Int]):Bool{x is .Some(n)}`, "unknown type n"},
		{"wrong specialization", `fn f(x:Option[Int]):Int{match(x){Option[String].Some(_)=>1,_=>0}}`, "cannot occur"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.bork")
			if err := os.WriteFile(path, []byte(tc.source+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}
