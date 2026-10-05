package driver

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestNoTestsPosition(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Test(path, io.Discard, TestOptions{})
	var de *DiagError
	if !errors.As(err, &de) {
		t.Fatalf("expected missing-tests diagnostic, got %v", err)
	}
	diags := de.Diags.Sorted()
	if len(diags) != 1 || diags[0].Code != "package.no-tests" || diags[0].Pos != (diag.Pos{File: path, Line: 1, Col: 1}) {
		t.Fatalf("missing-tests diagnostic must point to the root source: %+v", diags)
	}
}

func TestSuggestedEdits(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, source string
	}{
		{"bytes pipe migration", "fn main() { println([toByte(65)] |> bytes) }\n"},
		{"bytes grouped pipe migration", "fn main() { println([] |> ((bytes())) ) }\n"},
		{"utf8 pipe migration", "fn main() { println(\"a\" |> utf8Bytes) }\n"},
		{"utf8 named migration", "fn main() { println(utf8Bytes(text: \"a\")) }\n"},
		{"utf8 shebang migration", "#!/usr/bin/env bork\nprintln(utf8Bytes(\"a\"))\n"},
		{"bytes conditional migration", "fn main() { println(bytes(if (true) { [] } else { [] })) }\n"},
		{"bytes match migration", "fn main() { println(bytes(match (true) { true => []; false => [] })) }\n"},
		{"bytes block migration", "fn main() { println(bytes({ [] })) }\n"},
		{"bytes migration", "fn main() { println(bytes([toByte(65)])) }\n"},
		{"empty bytes migration", "fn main() { println(bytes([])) }\n"},
		{"named bytes migration", "fn main() { println(bytes(values: [toByte(65)])) }\n"},
		{"utf8 encoder migration", "fn main() { println(utf8Bytes(\"hé\")) }\n"},
		{"utf8 decoder migration", "fn main() { println(utf8String([toByte(65)].toBytes())) }\n"},
		{"utf8 existing import migration", "import codec \"bork/encoding\"\nfn main() { println(utf8Bytes(\"a\")) }\n"},
		{"utf8 alias migration", "import codec \"bork/encoding\"\nfn main() { println(codec.Hex(utf8Bytes(\"a\"))) }\n"},
		{"utf8 later collision migration", "fn main() { println(utf8Bytes(\"a\")); encoding = 1; println(encoding) }\n"},
		{"utf8 parameter collision migration", "fn other(encoding: Int): Int { encoding }\nfn main() { println(utf8Bytes(\"a\"), other(1)) }\n"},
		{"utf8 ambient collision migration", "ambient encoding: Int\nfn main() { println(utf8Bytes(\"a\")) }\n"},
		{"utf8 successive alias collisions", "fn other(encoding: Int, encoding2: Int): Int { encoding + encoding2 }\nfn main() { println(utf8Bytes(\"a\"), other(1, 2)) }\n"},
		{"utf8 interpolation binding collision", "fn main() { println(utf8Bytes(\"a\")); println(s\"${match (1) { encoding: Int => encoding }}\") }\n"},
		{"utf8 collision migration", "fn main() { encoding = 1; println(encoding, utf8Bytes(\"a\")) }\n"},
		{"utf8 grouped migration", "fn main() { println((utf8Bytes)(\"a\")) }\n"},
		{"pipe method", "fn main() { println([1, 2] |> map(x => x + 1)) }\n"},
		{"pipe result context", "fn main() { ys: List[List[Int]] = [1] |> map(x => []); println(ys) }\n"},
		{"pipe flatMap result context", "fn main() { ys: List[String] = [1] |> flatMap(x => []); println(ys) }\n"},
		{"generic pipe method", "fn main() { println([1, 2] |> map[String](x => toString(x))) }\n"},
		{"grouped bare target", "fn main() { println([1, 2] |> (length)) }\n"},
		{"grouped callee", "fn main() { println([1, 2] |> (map)(x => x + 1)) }\n"},
		{"grouped generic callee", "fn main() { println([1, 2] |> ((map))[String](x => toString(x))) }\n"},
		{"grouped call target", "fn main() { println([1, 2] |> (map(x => x + 1))) }\n"},
		{"bare pipe method", "fn main() { println([1, 2] |> length) }\n"},
		{"binary pipe receiver", "fn (x: Int) doubled(): Int { x * 2 }\nfn main() { println(1 + 2 |> doubled()) }\n"},
		{"unary pipe receiver", "fn (x: Int) doubled(): Int { x * 2 }\nfn main() { println(-2 |> doubled) }\n"},
		{"chained pipe receiver", "fn keep(xs: List[Int]): List[Int] { xs }\nfn main() { println([1] |> keep |> length) }\n"},
		{"multiline pipe receiver", "fn main() { println([\n 1, 2,\n ] |> length) }\n"},
		{"pipe interpolation", "fn main() { println(s\"å ${[1, 2] |> length}\") }\n"},
		{"operator", "fn main() { println(true & false) }\n"},
		{"unknown argument", "fn config(port: Int): Int { port }\nfn main() { println(config(prot: 9)) }\n"},
		{"duplicate argument", "fn config(port: Int): Int { port }\nfn main() { println(config(1, port: 9)) }\n"},
		{"copy separator", "type Config = { port: Int }\nfn main() { println(Config { port: 1 }.copy(port = 2)) }\n"},
		{"context nested Some braces", "type Config = { value: Int }\nfn main() { x: Option[Config] = .Some { value: .{ value: 1 } }; println(x) }\n"},
		{"context Some braces", "fn main() { x: Option[Int] = .Some { value: 1 }; println(x) }\n"},
		{"context generic variant typo", "fn option[T](value: Option[T]): Option[T] { value }\nfn main() { println(option(.Som(1))) }\n"},
		{"context variant typo", "type State = sealed { Ready }\nfn main() { x: State = .Reedy; println(x) }\n"},
		{"context variant defaults", "type State = sealed { Ready { value: Int = 1 } }\nfn main() { x: State = .Ready; println(x) }\n"},
		{"byte columns", "fn main() {\n\tprintln(\"å\"); xs = []; f = x => x\n _ = xs; _ = f\n}\n"},
		{"interpolation", "fn main() { println(s\"å ${true & false}\") }\n"},
		{"annotations", "fn main() {\n xs = []\n m = {:}\n f = x => x\n g = (y) => y\n h = (a, b) => a\n _ = xs; _ = m; _ = f; _ = g; _ = h\n}\n"},
		{"multiline", "fn main() {\n xs = [\n ]\n m = {\n :}\n f = (\n x,\n y\n ) => x\n _ = xs; _ = m; _ = f\n}\n"},
		{"missing effect", "fn say(s: String) { println(s) }\nfn main() { say(\"a\") }\n"},
		{"missing effect, multi-line params", "fn say(\n  s: String,\n): String {\n  println(s)\n  s\n}\nfn main() { _ = say(\"a\") }\n"},
		{"replacing uses nothing", "fn say(s: String) uses nothing { println(s) }\nfn main() { say(\"a\") }\n"},
		{"unused effect", "fn add(a: Int)uses io: Int { a + 1 }\nfn main() { println(add(1)) }\n"},
		{"partly unused", "fn say(s: String) uses io + net { println(s) }\nfn main() { say(\"a\") }\n"},
		{"missing and unused", "fn say(s: String) uses net { println(s) }\nfn main() { say(\"a\") }\n"},
		{"missing needs", "ambient t: String\nambient u: String\nfn tag(m: String) needs t: String { t + m }\nfn tag2(m: String) needs u: String { u + m }\nfn f(): String { tag(\"a\") + tag(\"b\") + tag2(\"c\") + t }\nfn main() { println(with (t: \"x\", u: \"y\") { f() }) }\n"},
		{"missing needs, existing clause", "ambient t: String\nambient u: String\nfn tag(m: String) needs t + u: String { t + u + m }\nfn f() uses io needs u { println(tag(\"a\") + tag(\"b\")) }\nfn main() { with (t: \"x\", u: \"y\") { f() } }\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "main.bork")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(path)
			var de *DiagError
			if !errors.As(err, &de) {
				t.Fatalf("expected diagnostics, got %v", err)
			}
			var edits []diag.TextEdit
			for _, d := range de.Diags.Sorted() {
				for _, fix := range d.Fixes {
					for _, edit := range fix.Edits {
						if fix.RequiresInput {
							edit.Replacement = strings.NewReplacer("Element", "Int", "Key", "String", "Value", "Int", "Type", "Int").Replace(edit.Replacement)
						}
						if edit.Start.File != path || edit.End.File != path {
							t.Fatalf("edit points to another file: %+v", edit)
						}
						edits = append(edits, edit)
					}
				}
			}
			if len(edits) == 0 {
				t.Fatal("no suggested edits")
			}
			offset := func(pos diag.Pos) int {
				lines := strings.SplitAfter(tc.source, "\n")
				n := pos.Col - 1
				for _, line := range lines[:pos.Line-1] {
					n += len(line)
				}
				return n
			}
			sort.Slice(edits, func(i, j int) bool { return offset(edits[i].Start) > offset(edits[j].Start) })
			fixed := tc.source
			for _, edit := range edits {
				fixed = fixed[:offset(edit.Start)] + edit.Replacement + fixed[offset(edit.End):]
			}
			if err := os.WriteFile(path, []byte(fixed), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Check(path); err != nil {
				t.Fatalf("suggested edits did not fix the program:\n%s\n%v", fixed, err)
			}
		})
	}
}

func TestPipeMethodResultContext(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "main.bork")
	source := "fn main() { ys: List[List[Int]] = [1] |> map(x => []); println(ys) }\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Check(path)
	var de *DiagError
	if !errors.As(err, &de) {
		t.Fatalf("expected diagnostics, got %v", err)
	}
	ds := de.Diags.Sorted()
	if len(ds) != 1 || ds[0].Code != "call.pipe-method" {
		t.Fatalf("expected only the pipe diagnostic, got %+v", ds)
	}
}

func TestPipeMethodFieldPrecedence(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"length: Int", "length: () => Int"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "main.bork")
			value := "1"
			if strings.Contains(field, "=>") {
				value = "() => 1"
			}
			source := "type R = { " + field + " }\nfn (r: R) length(x: Int): Int { x }\nfn main() { r = R { length: " + value + " }; println(r |> length(2)) }\n"
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(path)
			var de *DiagError
			if !errors.As(err, &de) {
				t.Fatalf("expected diagnostics, got %v", err)
			}
			ds := de.Diags.Sorted()
			if len(ds) != 1 || ds[0].Msg != "undefined function: length" || len(ds[0].Fixes) != 0 {
				t.Fatalf("expected undefined function without a method fix, got %+v", ds)
			}
		})
	}
}

// Ambiguity fixes are alternatives: apply each independently, not together.
func TestContextConstructorAlternatives(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, source, code string
		count              int
		input              bool
	}{
		{"records", "type A = { value: Int }\ntype B = { value: Int }\nfn main() { x: A | B = .{ value: 1 }; println(x) }\n", "type.context_ambiguous", 2, false},
		{"variants", "type A = sealed { Ready }\ntype B = sealed { Ready }\nfn main() { x: A | B = .Ready; println(x) }\n", "type.context_ambiguous", 2, false},
		{"imported variants", "import chosen \"example.com/context/api\"\ntype Alias = chosen.A\nfn main() { x: Alias | chosen.B = .Reedy; println(x) }\n", "type.context_variant_unknown", 2, false},
		{"union typo", "type A = sealed { Ready }\ntype B = sealed { Ready }\nfn main() { x: A | B = .Reedy; println(x) }\n", "type.context_variant_unknown", 2, false},
		{"nested missing context", "type Config = { value: Int }\nfn main() { x = [.{ value: 1 }]; println(x) }\n", "type.context_missing", 1, true},
		{"generic variants", "type State[T] = sealed { Empty }\nfn main() { x: State[Int] | State[String] = .Empty; println(x) }\n", "type.context_ambiguous", 2, false},
		{"function union arguments", "type Box[T] = { values: List[T] }\nfn main() { x: Box[((Int) => Int) | String] | Box[String] = .{ values: [] }; println(x) }\n", "type.context_ambiguous", 2, false},

		{"sequence arguments", "type Box[T] = { values: List[T] }\nfn main() { x: Box[Seq[Int] uses io] | Box[Seq[String] uses net] = .{ values: [] }; println(x) }\n", "type.context_ambiguous", 2, false},

		{"specializations", "type Box[T] = { values: List[T] }\nfn main() { x: Box[Int] | Box[String] = .{ values: [] }; println(x) }\n", "type.context_ambiguous", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "main.bork")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.name == "imported variants" {
				for rel, content := range map[string]string{"bork.mod": "module example.com/context\n", "api/api.bork": "type A = sealed { Ready }\ntype B = sealed { Ready }\n"} {
					p := filepath.Join(filepath.Dir(path), rel)
					if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			_, _, err := Check(path)
			var de *DiagError
			if !errors.As(err, &de) {
				t.Fatalf("expected diagnostics, got %v", err)
			}
			var fixes []diag.Fix
			for _, d := range de.Diags.Sorted() {
				if d.Code == tc.code {
					fixes = append(fixes, d.Fixes...)
				}
			}
			if len(fixes) != tc.count {
				t.Fatalf("expected %d alternatives, got %+v", tc.count, fixes)
			}
			for _, fix := range fixes {
				if fix.RequiresInput != tc.input {
					t.Fatalf("unexpected requires_input: %+v", fix)
				}
				fixed := tc.source
				for _, edit := range fix.Edits {
					offset := func(pos diag.Pos) int {
						lines := strings.SplitAfter(tc.source, "\n")
						n := pos.Col - 1
						for _, line := range lines[:pos.Line-1] {
							n += len(line)
						}
						return n
					}
					replacement := edit.Replacement
					if tc.name == "nested missing context" {
						replacement = strings.ReplaceAll(replacement, "Type", "Config")
					}
					fixed = fixed[:offset(edit.Start)] + replacement + fixed[offset(edit.End):]
				}

				if err := os.WriteFile(path, []byte(fixed), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, _, err := Check(path); err != nil {
					t.Fatalf("alternative did not fix the program:\n%s\n%v", fixed, err)
				}
			}
		})
	}
}

func TestDebugContextInference(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "main.bork")
	source := `type A = { value: Int }
fn pair[T](first: T, second: T): List[T] { [first, second] }
fn main() {
  println(pair(first: dbg(.{ value: 1 }), second: A { value: 2 }))
  println(pair(second: dbg(.{ value: 2 }), first: A { value: 1 }))
  xs: List[A] = pair(first: dbg(.{ value: 1 }), second: .{ value: 2 })
  println(xs)
}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Check(path); err != nil {
		t.Fatal(err)
	}
}
