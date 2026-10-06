package syntax

import (
	"github.com/GiGurra/bork/internal/diag"
	"strings"
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

func TestParseScriptExplicitMain(t *testing.T) {
	for _, source := range []string{
		"fn main(){}",
		"#!/usr/bin/env -S bork script\nlazy Value=1\ntype Item={value:Int}\nfn helper():Int{Value}\nfn main(){println(helper())}\ntest \"helper\"{assert(helper()==1)}",
	} {
		d := &diag.List{}
		file := ParseScript("script.bork", []byte(source), d)
		if d.Len() != 0 || !file.Script {
			t.Fatalf("%+v: %s", file, d.Error())
		}
		for _, fn := range file.Funcs {
			if fn.ScriptMain {
				t.Fatal("explicit main gained an implicit main")
			}
		}
	}
}

func TestParseScriptMixedEntrypoints(t *testing.T) {
	for _, source := range []string{"println(1)\nfn main(){}", "fn main(){}\nx=1", "fn main(){}\n(a,b)=(1,2)"} {
		d := &diag.List{}
		ParseScript("script.bork", []byte(source), d)
		if d.Len() != 1 {
			t.Fatalf("want one mixed-entrypoint error: %s", d.Error())
		}
		entry := d.Sorted()[0]
		if entry.Code != "script.mixed-entrypoints" || !strings.Contains(d.Error(), "script.bork:1:1") || !strings.Contains(d.Error(), "script.bork:2:1") {
			t.Fatalf("missing both source positions or diagnostic code: %+v", entry)
		}
	}
}
