package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunTestProtocol(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	src := "// 😀\nfn main() {}\ntest \"first\" { assert(true) }\ntest \"second\" { assert(true) }\n"
	s := &server{initialized: true, docs: map[string]document{path: {src, 1}}}
	params, _ := json.Marshal(map[string]any{"textDocument": map[string]string{"uri": fileURI(path)}})
	result, rpcErr, _ := s.handle(message{Method: "textDocument/codeLens", Params: params})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	lenses := result.([]any)
	if len(lenses) != 3 {
		t.Fatalf("lenses: %+v", lenses)
	}
	encoded, _ := json.Marshal(lenses)
	if !strings.Contains(string(encoded), "bork.runTest") || !strings.Contains(string(encoded), "bork.run") {
		t.Fatal(string(encoded))
	}
	result, rpcErr, _ = s.handle(message{Method: "bork/tests", Params: params})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	tests := result.([]runnableTest)
	if len(tests) != 2 || tests[0].ID == tests[1].ID || tests[0].Range.Start.Line != 2 || tests[0].Path != dir {
		t.Fatalf("tests: %+v", tests)
	}
	s.docs[path] = document{"fn broken(\ntest \"edited\" { assert(true) }\n", 2}
	result, rpcErr, _ = s.handle(message{Method: "textDocument/codeLens", Params: params})
	if rpcErr != nil || result == nil {
		t.Fatalf("broken edit: %v %v", result, rpcErr)
	}
	s.docs[path] = document{"#!/usr/bin/env -S bork script\nprintln(42)\n", 3}
	result, rpcErr, _ = s.handle(message{Method: "textDocument/codeLens", Params: params})
	if rpcErr != nil || len(result.([]any)) != 1 {
		t.Fatalf("script: %v %v", result, rpcErr)
	}
	encoded, _ = json.Marshal(result)
	if !strings.Contains(string(encoded), "script") {
		t.Fatal(string(encoded))
	}
	init := &server{}
	result, rpcErr, _ = init.handle(message{Method: "initialize"})
	if rpcErr != nil || result.(map[string]any)["capabilities"].(map[string]any)["codeLensProvider"] == nil {
		t.Fatal("missing capability")
	}
}

func BenchmarkRunTestDiscoveryHTTPServer(b *testing.B) {
	path, _ := filepath.Abs("../../examples/http_server/main.bork")
	src, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		codeLenses(path, string(src))
	}
}
