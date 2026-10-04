package playground

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		name, source, message string
		ok, local             bool
	}{
		{"hello", `fn main() { println("Hello!") }`, "", true, false},
		{"instance", "class Show[T] { fn show(x: T): String }\ninstance intShow: Show[Int] { fn show(x: Int): String { \"hi\" } }\nfn main() {}", "", true, false},
		{"instance comptime", "class Show[T] { fn show(x: T): String }\ninstance intShow: Show[Int] { fn show(x: Int): String { comptime { \"hi\" } } }\nfn main() {}", "comptime execution", false, true},
		{"undefined", `fn main() { println(missing) }`, "undefined: missing", false, false},
		{"effect", `fn greet() { println("hi") }`, "uses io", false, false},
		{"parse", `fn main( {`, "", false, false},
		{"import", "import \"bork/http\"\nfn main() {}", "package imports", false, true},
		{"unsafe", `fn x(): Int unsafe go { return 1 }`, "Go interop", false, true},
		{"bound function", `fn env(s: String): String unsafe go "os.Getenv"`, "Go interop", false, true},
		{"go type", `type File = resource go "*os.File"`, "Go type bindings", false, true},
		{"comptime", `fn main() { x = comptime { 1 }; println(x) }`, "comptime execution", false, true},
		{"nested comptime", `type X = { value: Int = comptime { 1 } }`, "comptime execution", false, true},
		{"guarded facts", "pred positive(x: Int) { x > 0 }\nfn need(x: Int where positive): Int { x }\nfn safe(x: Int): Int { if (positive(x)) { need(x) } else { 0 } }", "", true, false},
		{"deferred true proof", "pred positive(x: Int) { x > 0 }\nfn need(x: Int where positive): Int { x }\nfn main() { println(need(3)) }", "needs local bork", false, true},
		{"deferred false proof", "pred positive(x: Int) { x > 0 }\nfn need(x: Int where positive): Int { x }\nfn main() { println(need(-3)) }", "needs local bork", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := Handle(Request{Action: "check", Source: tc.source})
			if result.OK != tc.ok || result.NeedsLocal != tc.local {
				t.Fatalf("unexpected result: %+v", result)
			}
			if !tc.ok && len(result.Diagnostics) == 0 {
				t.Fatalf("missing diagnostics: %+v", result)
			}
			if tc.message != "" && (len(result.Diagnostics) == 0 || !strings.Contains(result.Diagnostics[0].Msg, tc.message)) {
				t.Fatalf("wanted %q: %+v", tc.message, result)
			}
			if result.Diagnostics == nil {
				t.Fatal("diagnostics must encode as an array")
			}
			if result.Error != "" {
				t.Fatal(result.Error)
			}
		})
	}
}

func TestFormatAndDescribe(t *testing.T) {
	result := Handle(Request{Action: "format", Source: "fn main(){println(1)}"})
	if !result.OK || result.Source != "fn main() { println(1) }\n" {
		t.Fatalf("format: %+v", result)
	}
	source := "fn main() { text = \"å😀\"; println(text) }"
	column := strings.LastIndex(source, "text") + 1
	result = Handle(Request{Action: "describe", Source: source, Line: 1, Column: column})
	if !result.OK || result.Description == nil || result.Description.Type != "String" {
		t.Fatalf("describe Unicode: %+v", result)
	}
	result = Handle(Request{Action: "describe", Source: source, Line: 2, Column: 1})
	if result.OK || result.Error == "" {
		t.Fatalf("invalid position accepted: %+v", result)
	}
	result = Handle(Request{Action: "check", Source: strings.Repeat(" ", MaxSourceBytes+1)})
	if result.OK || result.Error == "" {
		t.Fatal("oversized input accepted")
	}
}
