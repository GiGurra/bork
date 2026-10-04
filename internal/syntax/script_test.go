package syntax

import (
	"github.com/GiGurra/bork/internal/diag"
	"testing"
)

func TestParseScript(t *testing.T) {
	for _, source := range []string{"#!/usr/bin/env -S bork script\nx=1\nprintln(x)", "x=1\nprintln(x)"} {
		d := &diag.List{}
		file := ParseScript("script.bork", []byte(source), d)
		if d.Len() != 0 || !file.Script || len(file.Funcs) != 1 || !file.Funcs[0].ScriptMain || len(file.Funcs[0].Body.Stmts) != 2 {
			t.Fatalf("%+v: %s", file, d.Error())
		}
		if file.Funcs[0].Body.Stmts[0].(*Binding).Pos.Line != 1+len(file.Comments) {
			t.Fatal("lost source positions")
		}
	}
	d := &diag.List{}
	f := Parse("script.bork", []byte("#!/usr/bin/env -S bork script\nprintln(42)"), d)
	if d.Len() != 0 || !f.Script {
		t.Fatalf("%+v: %s", f, d.Error())
	}
	d = &diag.List{}
	Lex("script.bork", []byte("\n#!/bin/bork"), d)
	if d.Len() == 0 {
		t.Fatal("shebang allowed after first line")
	}
}
