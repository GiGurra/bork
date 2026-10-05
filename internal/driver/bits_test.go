package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBitwiseChecking(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"unsigned count", "fn f(x:Int8,n:Uint64):Int8{x<<n}", ""},
		{"unproven count", "fn f(x:Int,n:Int):Int{x<<n}", "shift count must be proven nonnegative"},
		{"negative count", "fn f(x:Int):Int{x<< -1}", "shift count must be proven nonnegative"},
		{"guarded count", "fn f(x:Int,n:Int):Int{if(n>=0){x<<n}else{x}}", ""},
		{"bounded count", "fn f(x:Int,n:Int):Int{if(n>=3){x<<n}else{x}}", ""},
		{"bounded predicate", "pred atLeastThree(n:Int){n>=3}\nfn f(x:Int,n:Int where atLeastThree):Int{x<<n}", ""},
		{"overflowing computed count", "pred nonNegative(n:Int){n>=0}\nfn f(x:Int,n:Int where nonNegative):Int{x<<(n+1)}", "shift count must be proven nonnegative"},
		{"positive count", "fn f(x:Int,n:Int):Int{if(n>0){x<<n}else{x}}", ""},
		{"early return", "fn f(x:Int,n:Int):Int{if(n<0){return x};x>>n}", ""},
		{"predicate count", "pred nonNegative(n:Int){n>=0}\nfn f(x:Int,n:Int where nonNegative):Int{x<<n}", ""},
		{"constant alias count", "fn f(x:Int):Int{n=3;x<<n}", ""},
		{"Bool AND", "fn f():Bool{true&false}", "use && for Bool"},
		{"Bool OR", "fn f():Bool{true|false}", "use || for Bool"},
		{"Bool XOR", "fn f():Bool{true^false}", "use != for Bool"},
		{"float", "fn f(x:Float):Float{x&x}", "needs two integers"},
		{"mixed widths", "fn f(x:Int8,y:Int16):Int8{x|y}", "same type"},
		{"float shift", "fn f(x:Int,n:Float):Int{x<<n}", "needs integer operands"},
		{"complement default", "fn f(n:Byte=^0):Byte{n}\nfn main(){println(f())}", ""},
		{"complement context", "fn f():Byte{^0}", ""},
		{"exact constant with shift", "fn f():Byte{(1<<8)-1}", ""},
		{"composite constant complement", "fn f():Byte{^0 & 15}", ""},
		{"max unsigned constant", "fn f():Uint64{(1<<64)-1}", ""},
		{"shift context", "fn f():Byte{1<<7}", ""},
		{"overflowing constant shift", "fn f():Byte{1<<8}", "does not fit"},
		{"huge constant shift", "fn f():Int{1<<1000000000}", "does not fit"},
		{"huge zero shift", "fn f():Int{0<<1000000000}", ""},
		{"huge right shift", "fn f():Int{-1>>1000000000}", ""},
		{"wrong complement spelling", "fn f(x:Int):Int{~x}", "bitwise NOT is written ^x"},
		{"union type", "fn f(x:Int|String):Int|String{x}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}
