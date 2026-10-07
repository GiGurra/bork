package format

import (
	"strings"
	"testing"
)

func TestSimplifyControlHeads(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"if (ready) {}", "if ready {}"},
		{"if(ready){}", "if ready {}"},
		{"match(value){ _ => 1 }", "match value { _ => 1 }"},
		{"if ((ready)) {}", "if ready {}"},
		{"if (ready) && other {}", "if (ready) && other {}"},
		{"match (value) { _ => 1 }", "match value { _ => 1 }"},
		{"match (1, 2) { _ => 1 }", "match (1, 2) { _ => 1 }"},
		{"if (User { name: \"n\" }) {}", "if (User { name: \"n\" }) {}"},
		{"if ((User { name: \"n\" })) {}", "if (User { name: \"n\" }) {}"},
		{"for (x in xs) {}", "for x in xs {}"},
		{"for (x in [1]) {}", "for x in [1] {}"},
		{"for (ready) {}", "for ready {}"},
		{"for (i = 0; i < 3; i = i + 1) {}", "for i = 0; i < 3; i = i + 1 {}"},
		{"for (; ready;) {}", "for ; ready; {}"},
		{"for (x in User { name: \"n\" }) {}", "for (x in User { name: \"n\" }) {}"},
		{"if (check(User { name: \"n\" })) {}", "if check(User { name: \"n\" }) {}"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			source := []byte("fn f() { " + tc.input + " }")
			original, err := Source("heads.bork", source)
			if err != nil {
				t.Fatal(err)
			}
			got, err := SourceWithOptions("heads.bork", source, Options{Simplify: true})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), tc.want) {
				t.Fatalf("got %s; want %s", got, tc.want)
			}
			again, err := SourceWithOptions("heads.bork", got, Options{Simplify: true})
			if err != nil || string(again) != string(got) {
				t.Fatalf("not idempotent: %s, %v", again, err)
			}
			if strings.Count(string(original), "(") != strings.Count(string(source), "(") {
				t.Fatalf("default formatting removed parens: %s", original)
			}
		})
	}
}

func TestSimplifyStagedHeadsKeepsComprehensionDelimiters(t *testing.T) {
	source := []byte(`derive fn helper[T]() {
 comptime for (f in fields()) { comptime if (f.public) { comptime match (f.name) { _ => Ok } } }
 [comptime for (f in fields()) comptime if (f.public) f.name]
 }`)
	got, err := SourceWithOptions("derive.bork", source, Options{Simplify: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"comptime for f in fields()", "comptime if f.public", "comptime match f.name", "[comptime for (f in fields()) comptime if (f.public) f.name]"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

func TestSimplifyHeadsPreservesCommentsAndIncompleteInput(t *testing.T) {
	for _, source := range []string{
		"fn f() { if(/* before */true/* after */) {} }",
		"fn f() { if (ready // comment\n) {} }",
		"fn f() { if (",
	} {
		got, err := SourceWithOptions("heads.bork", []byte(source), Options{Simplify: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, comment := range []string{"/* before */", "/* after */", "// comment"} {
			if strings.Contains(source, comment) && !strings.Contains(string(got), comment) {
				t.Fatalf("lost comment: %s", got)
			}
		}
		again, err := SourceWithOptions("heads.bork", got, Options{Simplify: true})
		if err != nil || string(got) != string(again) {
			t.Fatalf("not idempotent: %s, %v", again, err)
		}
	}
}

func TestSimplifyHeadsInsideInterpolation(t *testing.T) {
	source := []byte(`fn f() { println(s"${if (true) { 1 } else { 2 }}") }`)
	got, err := SourceWithOptions("heads.bork", source, Options{Simplify: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `${if true { 1 } else { 2 }}`) {
		t.Fatalf("interpolation head was not simplified: %s", got)
	}
	again, err := SourceWithOptions("heads.bork", got, Options{Simplify: true})
	if err != nil || string(again) != string(got) {
		t.Fatalf("not idempotent: %s, %v", again, err)
	}
}
