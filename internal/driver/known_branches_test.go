package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnownResultBranches(t *testing.T) {
	t.Parallel()
	const positive = "pred positive(n:Int){n>0}\n"
	for _, tc := range []struct{ name, source, want string }{
		{"computed field", "type C={n:Int,lazy value:Int where positive={if(n==1){return -1};2}}\nfn main(){println(C{n:0}.value)}", ""},
		{"reachable computed failure", "type C={n:Int,lazy value:Int where positive={if(n==1){return -1};2}}\nfn main(){println(C{n:1}.value)}", "positive"},
		{"unknown computed guard", "type C={n:Int,lazy value:Int where positive={if(n==1){return -1};2}}\nfn make(n:Int):C{C{n:n}}", "positive"},
		{"eager alias", "fn value():Int where positive{n=0;if(n==1){return -1};2}", ""},
		{"local lazy", "fn main(){n=0;lazy value:Int where positive={if(n==1){return -1};2};println(value)}", ""},
		{"async", "fn main(){n=0;scope s{async(s) value:Int where positive={if(n==1){return -1};2};println(value)}}", ""},
		{"nested guards", "fn value():Int where positive{n=0;if(!(n==0)||n>1){return -1};if(n==0){if(n!=0){return -2};return 2};-3}", ""},
		{"eager eager field", "type C={n:Int}\nfn value():Int where positive{c=C{n:0};if(c.n==1){return -1};2}", ""},
		{"known copy", "type C={n:Int,lazy value:Int where positive={if(n==1){return -1};2}}\nfn main(){c=C{n:0};println(c.copy(n:2).value)}", ""},
		{"nested copy", "type Child={n:Int}\ntype C={child:Child,lazy value:Int where positive={if(child.n==1){return -1};2}}\nfn main(){c=C{child:Child{n:0}};println(c.copy(child.n:2).value)}", ""},
		{"value-position if", "fn main(){n=0;value:Int where positive=if(n==0){2}else{-1};println(value)}", ""},
		{"value-position failure", "fn main(){n=0;value:Int where positive=if(n==0){-1}else{2};println(value)}", "positive"},
		{"union member value", "type Problem={n:Int}\npred approved(p:Problem){p.n>0}\nfn value():Int|Problem where approved{n=0;x:Int|Problem=if(n==0){Problem{n:2}}else{2};x}", ""},
		{"union member failure", "type Problem={n:Int}\npred approved(p:Problem){p.n>0}\nfn value():Int|Problem where approved{n=0;x:Int|Problem=if(n==0){Problem{n:-1}}else{2};x}", "approved"},
		{"left guard return", "fn value(flag:Bool):Int where positive{if({if(flag){return -1};true}&&false){return 3};2}", "positive"},
		{"left guard try", "type Failed={}\npred valid(x:Int|Failed){match(x){n:Int=>n>0,_:Failed=>false}}\nfn value(x:Bool|Failed):Int|Failed where valid{if(x?&&false){return 3};2}", "valid"},
		{"failure after known return", "type Failed={}\npred valid(x:Int|Failed){match(x){n:Int=>n>0,_:Failed=>false}}\nfn value(x:Int|Failed):Int|Failed where valid{n=0;if(n==0){return 2};_=x?;2}", ""},
		{"unknown guard", "fn value(n:Int):Int where positive{if(n==1){return -1};2}", "positive"},
		{"runtime lazy guard", "fn value():Int where positive{lazy n=0;if(n==1){return -1};2}", "positive"},
		{"runtime lazy record", "type C={n:Int}\nfn value():Int where positive{lazy c=C{n:0};if(c.n==1){return -1};2}", "positive"},
		{"runtime lazy field", "type C={lazy n:Int}\nfn value():Int where positive{c=C{n:0};if(c.n==1){return -1};2}", "positive"},
		{"package lazy guard", "lazy N=0\nfn value():Int where positive{if(N==1){return -1};2}", "positive"},
		{"lazy field copy guard", "type C={lazy n:Int}\nfn value():Int where positive{c=C{n:0};d=c.copy(n:0);if(d.n==1){return -1};2}", "positive"},
		{"float rounding", "fn value():Int where positive{n:Float32=16777217.0;if(n!=16777216.0){return -1};2}", ""},
		{"float reachable failure", "fn value():Int where positive{n:Float32=16777217.0;if(n==16777216.0){return -1};2}", "positive"},
		{"union member identity", "fn value():Int where positive{n:Int|Int8=1;small:Int8=1;if(n==small){2}else{-1}}", "positive"},
		{"pure call unknown", "fn zero():Int{0}\nfn value():Int where positive{n=zero();if(n==1){return -1};2}", "positive"},
		{"overflow guard", "fn value():Int where positive{n:Int8=127;if(n+1<0){return -1};2}", "positive"},
		{"unknown try failure", "type Failed={}\npred valid(x:Int|Failed){match(x){n:Int=>n>0,_:Failed=>false}}\nfn value(x:Int|Failed):Int|Failed where valid{_=x?;n=0;if(n==1){return -1};2}", "valid"},
		{"unreachable try failure", "type Failed={}\npred valid(x:Int|Failed){match(x){n:Int=>n>0,_:Failed=>false}}\nfn value(x:Int|Failed):Int|Failed where valid{n=0;if(n==1){_=x?};2}", ""},
		{"guard try failure", "type Failed={}\npred valid(x:Int|Failed){match(x){n:Int=>n>0,_:Failed=>false}}\nfn value(x:Int|Failed):Int|Failed where valid{n=0;if({_=x?;n==1}){return 3};2}", "valid"},
		{"short circuit try failure", "type Failed={}\npred valid(x:Int|Failed){match(x){n:Int=>n>0,_:Failed=>false}}\nfn value(x:Int|Failed):Int|Failed where valid{n=0;if(n==1&&{_=x?;true}){return -1};2}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := fixtureDir(t)
			if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(positive+tc.source), 0o644); err != nil {
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
