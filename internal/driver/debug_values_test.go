package driver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/GiGurra/bork/internal/gen"
)

func presentationFixture() *dapRelay {
	return &dapRelay{
		metadata: &gen.DebugMap{Version: 1, Source: "/generated/main.go", HiddenPrefixes: []string{"_", "~", "."}, Names: map[string]string{"type_": "type"}, Functions: map[string]string{"main._helper": "", "main._m_Record_test": "test"}, Types: map[string]gen.DebugType{
			"main.Record":       {Name: "Record", Kind: "record", Fields: map[string]gen.DebugField{"type_": {Name: "type", Type: "String"}, "data": {Name: "data", Type: "Record"}}},
			"main.Shape":        {Name: "Shape", Kind: "union"},
			"main.Shape_Circle": {Name: "Shape.Circle", Kind: "variant", Fields: map[string]gen.DebugField{"radius": {Name: "radius", Type: "Float"}}},
			"main.Option_Some":  {Name: "Some", Kind: "option", Fields: map[string]gen.DebugField{"value": {Name: "value", Type: "T"}}},
			"main.Option_None":  {Name: "None", Kind: "option"},
			"float64":           {Name: "Float", Kind: "scalar"},
			"int64":             {Name: "Int", Kind: "scalar"},
		}}, references: map[float64]gen.DebugType{}, pending: map[float64]float64{}, internal: map[float64]map[string]any{},
	}
}

func TestDebugValuePreviews(t *testing.T) {
	r := presentationFixture()
	for _, test := range []struct{ in, want string }{
		{`main.Shape(main.Shape_Circle) {radius: 2}`, `Shape.Circle { radius: 2.0 }`},
		{`main.Option[int64](main.Option_Some[int64]) {value: 3}`, `Some(3)`},
		{`main.Option[int64](main.Option_None[int64]) {}`, `None`},
		{`main.Record {type_: "main.Shape_Circle {radius: 2}", data: main.Record {type_: "x:y,z"}}`, `Record { type: "main.Shape_Circle {radius: 2}", data: Record { type: "x:y,z" } }`},
		{`main.Option[int64](main.Option_Some[int64]) {value: ...}`, `Some(...)`},
		{`main.Shape(main.Shape_Circle) {radius:`, `main.Shape(main.Shape_Circle) {radius:`},
		{`interface {}(int64) 8`, `8`},
		{`foreign.Shape {radius: 2}`, `foreign.Shape {radius: 2}`},
		{`"escaped \" main.Shape_Circle {radius: 2}"`, `"escaped \" main.Shape_Circle {radius: 2}"`},
	} {
		if got := r.pretty(test.in); got != test.want {
			t.Errorf("pretty(%s) = %s; want %s", test.in, got, test.want)
		}
	}
}

func TestDebugRelayPresentation(t *testing.T) {
	r := presentationFixture()
	r.references[10] = r.metadata.Types["main.Record"]
	r.pending[1] = 10
	msg := map[string]any{"type": "response", "success": true, "command": "variables", "request_seq": float64(1), "body": map[string]any{"variables": []any{
		map[string]any{"name": "_t1", "value": "4"},
		map[string]any{"name": "type_", "type": "string", "value": `"hello"`, "evaluateName": "record.type_", "variablesReference": float64(0)},
		map[string]any{"name": "data", "type": "main.Record", "value": `main.Record {type_: "x"}`, "variablesReference": float64(11)},
	}}}
	if skip, err := r.expand(msg); skip || err != nil {
		t.Fatalf("ordinary record data field treated as interface: %v %v", skip, err)
	}
	if !r.rewrite(msg) {
		t.Fatal("response not rewritten")
	}
	vars := msg["body"].(map[string]any)["variables"].([]any)
	if len(vars) != 2 || vars[0].(map[string]any)["name"] != "type" || vars[0].(map[string]any)["evaluateName"] != "record.type_" || vars[1].(map[string]any)["variablesReference"] != float64(11) {
		t.Fatalf("names/references not preserved: %v", vars)
	}
	stack := map[string]any{"type": "response", "success": true, "command": "stackTrace", "body": map[string]any{"stackFrames": []any{
		map[string]any{"id": 1, "name": "main._helper"},
		map[string]any{"id": 2, "name": "main._m_Record_test"},
		map[string]any{"id": 3, "name": "main.Record.String", "source": map[string]any{"path": "/generated/main.go"}},
	}}}
	r.rewrite(stack)
	frames := stack["body"].(map[string]any)["stackFrames"].([]any)
	if len(frames) != 3 || frames[0].(map[string]any)["presentationHint"] != "subtle" || frames[1].(map[string]any)["name"] != "test" || frames[2].(map[string]any)["presentationHint"] != "subtle" {
		t.Fatalf("frames: %v", frames)
	}
}

func TestDebugLazyExpansion(t *testing.T) {
	r := presentationFixture()
	var upstream bytes.Buffer
	r.upstream = &upstream
	r.references[10] = r.metadata.Types["main.Shape"]
	r.pending[1] = 10
	msg := map[string]any{"type": "response", "success": true, "command": "variables", "request_seq": float64(1), "body": map[string]any{"variables": []any{
		map[string]any{"name": "data", "type": "main.Shape_Circle", "value": "main.Shape_Circle {radius: 2}", "variablesReference": float64(11)},
	}}}
	if skip, err := r.expand(msg); !skip || err != nil {
		t.Fatalf("expansion: %v %v", skip, err)
	}
	data, err := readDAP(bufio.NewReader(&upstream))
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err = json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if request["seq"] != float64(-1) || request["arguments"].(map[string]any)["variablesReference"] != float64(11) {
		t.Fatalf("lazy request: %v", request)
	}
	response := map[string]any{"type": "response", "success": false, "command": "variables", "request_seq": float64(-1), "message": "cannot load"}
	if skip, err := r.expand(response); skip || err != nil {
		t.Fatalf("fallback: %v %v", skip, err)
	}
	if response["request_seq"] != float64(1) || response["success"] != true {
		t.Fatalf("failed load lost original preview: %v", response)
	}
}

func TestDebugDAPFraming(t *testing.T) {
	for _, input := range []string{"Content-Length: -1\r\n\r\n", "Content-Length: nope\r\n\r\n", "Other: 3\r\n\r\n", "Content-Length: 999999999\r\n\r\n", "Content-Length: 3\r\n\r\nx"} {
		if _, err := readDAP(bufio.NewReader(strings.NewReader(input))); err == nil {
			t.Fatalf("accepted invalid frame %q", input)
		}
	}
	data, err := readDAP(bufio.NewReader(strings.NewReader("Content-Type: application/json\r\nContent-Length: 2\r\n\r\n{}")))
	if err != nil || string(data) != "{}" {
		t.Fatalf("valid frame: %s %v", data, err)
	}
}

func TestDebugSpecialFloats(t *testing.T) {
	r := presentationFixture()
	for _, value := range []string{"NaN", "+Inf", "-Inf"} {
		body := map[string]any{"type": "float64", "result": value}
		r.value(body, "result")
		if body["result"] != value {
			t.Fatalf("special float: %v", body)
		}
		input := "main.Shape_Circle {radius: " + value + "}"
		if got := r.pretty(input); got != "Shape.Circle { radius: "+value+" }" {
			t.Fatalf("special float in variant: %s", got)
		}
	}
}
func TestDebugAdapterEarlyExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a Unix shell")
	}
	path := filepath.Join(t.TempDir(), "dlv")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'DAP server listening at: 127.0.0.1:1\\n'\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := debugDAPRelay(ctx, path, "127.0.0.1:0", io.Discard, io.Discard)
	if err == nil || ctx.Err() != nil {
		t.Fatalf("adapter exit did not terminate relay: %v, context %v", err, ctx.Err())
	}
}
