package lsp

import (
	"encoding/json"
	"github.com/GiGurra/bork/internal/diag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptEditorIntegration(t *testing.T) {
	source := "#!/usr/bin/env -S bork script\nfn double(n: Int): Int { n * 2 }\nvalue = double(21)\nprintln(value)\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "run.bork")
	sibling := filepath.Join(dir, "broken.bork")
	for file, text := range map[string]string{path: source, sibling: "fn invalid(\n"} {
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &server{out: &strings.Builder{}, initialized: true, docs: map[string]document{path: {source, 1}}, packages: map[string]*packageState{}, diagnostics: map[string][]diag.Diagnostic{}}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if pkg := s.state(path); pkg == nil || pkg.analysis == nil || pkg.stale {
		t.Fatalf("script included broken sibling: %s", s.out)
	}
	if len(s.diagnostics[path]) != 0 || len(s.diagnostics[sibling]) != 0 {
		t.Fatalf("script diagnostics: %+v", s.diagnostics)
	}
	hover := query(t, s, path, "hover", 3, 9)
	data, _ := json.Marshal(hover)
	if !strings.Contains(string(data), "Int") {
		t.Fatalf("script hover: %s", data)
	}
	def := query(t, s, path, "definition", 3, 9).(location)
	if def.URI != fileURI(path) || def.Range.Start != (position{2, 0}) {
		t.Fatalf("script definition: %+v", def)
	}
	p := documentParams{Position: position{3, 9}, NewName: "amount"}
	rename, err := s.feature("textDocument/rename", path, p)
	if err != nil {
		t.Fatal(err)
	}
	edits := rename.(map[string]any)["changes"].(map[string][]textEdit)[fileURI(path)]
	if len(edits) != 2 {
		t.Fatalf("script rename: %+v", edits)
	}
	syms := query(t, s, path, "documentSymbol", 0, 0).([]any)
	if len(syms) != 1 || syms[0].(map[string]any)["name"] != "double" {
		t.Fatalf("synthetic main leaked into symbols: %+v", syms)
	}
	completion := query(t, s, path, "completion", 3, 0).([]any)
	found := false
	for _, item := range completion {
		label := item.(map[string]any)["label"]
		if label == "main" {
			t.Fatal("synthetic main leaked into completion")
		}
		found = found || label == "double"
	}
	if !found {
		t.Fatalf("script function missing from completion: %+v", completion)
	}
	s.docs[path] = document{strings.Replace(source, "double(21)", "missing", 1), 2}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if !s.state(path).stale || len(s.diagnostics[path]) == 0 {
		t.Fatal("unsaved broken script was not checked")
	}
	for _, d := range s.diagnostics[path] {
		if d.Pos.Line != 3 {
			t.Fatalf("script diagnostic source position shifted: %+v", d)
		}
	}
	data, _ = json.Marshal(query(t, s, path, "hover", 3, 9))
	if !strings.Contains(string(data), "Stale") {
		t.Fatalf("script lost stale hover: %s", data)
	}
	s.docs[path] = document{source, 3}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if s.state(path).stale || len(s.diagnostics[path]) != 0 {
		t.Fatal("script repair did not clear errors")
	}
	disk, err := os.ReadFile(path)
	if err != nil || string(disk) != source {
		t.Fatal("script overlays changed disk source")
	}
}

func TestIndependentOpenScripts(t *testing.T) {
	dir := t.TempDir()
	source := "#!/usr/bin/env -S bork script\nvalue = 42\nprintln(value)\n"
	first, second := filepath.Join(dir, "one.bork"), filepath.Join(dir, "two.bork")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Opening a new, unsaved script must not require a disk file.
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	s := &server{out: &strings.Builder{}, initialized: true, docs: map[string]document{first: {source, 1}, second: {source, 1}}, packages: map[string]*packageState{}, diagnostics: map[string][]diag.Diagnostic{}}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if len(s.packages) != 2 || s.state(first) == s.state(second) {
		t.Fatal("adjacent scripts share package state")
	}
	s.docs[second] = document{strings.Replace(source, "42", "missing", 1), 2}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if s.state(first).stale || !s.state(second).stale || len(s.diagnostics[first]) != 0 {
		t.Fatal("broken adjacent script affected valid script")
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("unsaved script was written to disk")
	}
}
