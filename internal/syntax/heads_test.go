package syntax

import (
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestOptionalControlHeadParentheses(t *testing.T) {
	for _, head := range []string{
		"if ready {} else if other {} else {}",
		"if (ready) && other {}",
		"if value is Int {}", "if value is Option.None {}",
		"if value is (User { name: \"n\" }) {}",
		"if value is Option.Some(User { name: \"n\" }) {}",
		"if check(User { name: \"n\" }) {}",
		"if row.copy(child: User { name: \"n\" }).ready {}",
		"if row.into[Other](child: User { name: \"n\" }).ready {}",
		"match .{ child: User { name: \"n\" } } { _ => 1 }",
		"if (User { name: \"n\" }).name == \"n\" {}",
		"if [User { name: \"n\" }].size() > 0 {}",
		"if { x = User { name: \"n\" }; true } {}",
		"match value { _ => 1 }", "match .{ value: 1 } { _ => 1 }", "match (1, 2) { _ => 1 }",
		"match (User { name: \"n\" }) { _ => 1 }",
		"for x in xs {}", "for _ in xs {}", "for ready {}",
		"for (ready) && other {}", "for i = 0; i < 3; i = i + 1 {}",
		"for ; ready; {}", "for {}", "for (x in xs) {}",
		"for (x in User { name: \"n\" }) {}",
		"for x in (User { name: \"n\" }) {}",
	} {
		t.Run(head, func(t *testing.T) {
			d := &diag.List{}
			Parse("heads.bork", []byte("fn f() { "+head+" }"), d)
			if d.Len() != 0 {
				t.Fatal(d.Error())
			}
		})
	}
}

func TestBareControlHeadNamesDoNotBecomeRecords(t *testing.T) {
	for _, source := range []string{"if ready {}", "match value { _ => 1 }", "match .{ value: 1 } { _ => 1 }", "for x in xs {}", "for ready {}", "for ; ready; x = next {}"} {
		d := &diag.List{}
		f := Parse("heads.bork", []byte("fn f() { "+source+" }"), d)
		if d.Len() != 0 {
			t.Fatal(d.Error())
		}
		if len(f.Funcs) != 1 {
			t.Fatal("missing function")
		}
	}
	for _, source := range []string{"if User { name: \"n\" } {}", "match User { name: \"n\" } { _ => 1 }", "for x in User { name: \"n\" } {}"} {
		d := &diag.List{}
		Parse("heads.bork", []byte("fn f() { "+source+" }"), d)
		if d.Len() == 0 {
			t.Fatalf("bare record accepted: %s", source)
		}
	}
}

func TestOptionalStagedControlHeads(t *testing.T) {
	source := `derive fn helper[T]() {
 comptime for f in fields() { comptime if f.public { comptime match f.name { _ => Ok } } }
 [comptime for (f in fields()) comptime if (f.public) f.name]
 }`
	d := &diag.List{}
	f := Parse("derive.bork", []byte(source), d)
	if d.Len() != 0 {
		t.Fatal(d.Error())
	}
	loop := f.DeriveHelpers[0].Body.Stmts[0].(*ExprStmt).X.(*For)
	if !loop.Comptime || !loop.Body.Tail.(*If).Comptime {
		t.Fatal("missing staging markers")
	}
	for _, bad := range []string{
		`derive fn helper[T]() { [comptime for f in fields() f.name] }`,
		`derive fn helper[T]() { [comptime for (f in fields()) comptime if f.public f.name] }`,
	} {
		d = &diag.List{}
		Parse("derive.bork", []byte(bad), d)
		if d.Len() == 0 {
			t.Fatalf("comprehension accepted missing parens: %s", bad)
		}
	}
}
