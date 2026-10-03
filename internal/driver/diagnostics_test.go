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
	cases := []struct {
		name, source string
	}{
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
		{"byte columns", "fn main() {\n\tprintln(\"å\"); xs = []; f = x => x\n}\n"},
		{"interpolation", "fn main() { println(s\"å ${true & false}\") }\n"},
		{"annotations", "fn main() {\n xs = []\n m = {:}\n f = x => x\n g = (y) => y\n h = (a, b) => a\n}\n"},
		{"multiline", "fn main() {\n xs = [\n ]\n m = {\n :}\n f = (\n x,\n y\n ) => x\n}\n"},
		{"missing effect", "fn say(s: String) { println(s) }\nfn main() { say(\"a\") }\n"},
		{"missing effect, multi-line params", "fn say(\n  s: String,\n): String {\n  println(s)\n  s\n}\nfn main() { _ = say(\"a\") }\n"},
		{"replacing uses nothing", "fn say(s: String) uses nothing { println(s) }\nfn main() { say(\"a\") }\n"},
		{"unused effect", "fn add(a: Int)uses io: Int { a + 1 }\nfn main() { println(add(1)) }\n"},
		{"partly unused", "fn say(s: String) uses io + net { println(s) }\nfn main() { say(\"a\") }\n"},
		{"missing and unused", "fn say(s: String) uses net { println(s) }\nfn main() { say(\"a\") }\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
	for _, field := range []string{"length: Int", "length: () => Int"} {
		t.Run(field, func(t *testing.T) {
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
