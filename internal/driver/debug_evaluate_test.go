package driver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/gen"
)

func takeDebugMessage(t *testing.T, buffer *bytes.Buffer) map[string]any {
	t.Helper()
	data, err := readDAP(bufio.NewReader(buffer))
	if err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func TestDebugEvaluateRelay(t *testing.T) {
	r := presentationFixture()
	r.metadata.Expressions = map[string]check.DebugShape{"int64": {Name: "Int", Kind: "scalar"}}
	r.metadata.Names["range_"] = "range"
	r.frames = map[float64]diag.Pos{100: {File: "main.bork", Line: 5}}
	r.metadata.Bindings = []gen.DebugBindings{{Start: diag.Pos{File: "main.bork", Line: 1}, End: diag.Pos{File: "main.bork", Line: 10}, Names: map[string]string{"range_": "range"}}}
	var upstream, downstream bytes.Buffer
	r.upstream, r.downstream = &upstream, &downstream
	handle := func(message map[string]any, request bool) {
		t.Helper()
		if skipped, err := r.evaluate(message, request); !skipped || err != nil {
			t.Fatalf("evaluate: %v, %v", skipped, err)
		}
	}
	// Two requests can collect locals independently. Their internal sequence
	// numbers must never escape to the editor or replace original request IDs.
	for _, seq := range []float64{41, 42} {
		handle(map[string]any{"seq": seq, "type": "request", "command": "evaluate", "arguments": map[string]any{"expression": "range + 1", "frameId": float64(100), "context": "watch"}}, true)
		scopes := takeDebugMessage(t, &upstream)
		handle(map[string]any{"type": "response", "command": "scopes", "success": true, "request_seq": scopes["seq"], "body": map[string]any{"scopes": []any{
			map[string]any{"name": "Locals", "variablesReference": float64(10)}, map[string]any{"name": "Globals", "variablesReference": float64(20)},
		}}}, false)
		vars := takeDebugMessage(t, &upstream)
		if vars["command"] != "variables" || vars["arguments"].(map[string]any)["variablesReference"] != float64(10) {
			t.Fatalf("wrong scope: %v", vars)
		}
		// Complete the second request first.
		r.internal[seq] = vars
	}
	for _, seq := range []float64{42, 41} {
		vars := r.internal[seq]
		handle(map[string]any{"type": "response", "command": "variables", "success": true, "request_seq": vars["seq"], "body": map[string]any{"variables": []any{
			map[string]any{"name": "range_", "type": "int64", "value": "3"}, map[string]any{"name": "_t1", "type": "int64", "value": "4"},
		}}}, false)
		translated := takeDebugMessage(t, &upstream)
		if translated["seq"] != seq || translated["arguments"].(map[string]any)["expression"] != "range_ + 1" {
			t.Fatalf("translated: %v", translated)
		}
	}
	if len(r.evaluations) != 0 || downstream.Len() != 0 {
		t.Fatal("internal requests leaked")
	}
}

func TestDebugEvaluateFailure(t *testing.T) {
	for _, failure := range []string{"no frame", "moved", "delve", "unsupported"} {
		t.Run(failure, func(t *testing.T) {
			r := presentationFixture()
			r.metadata.Expressions = map[string]check.DebugShape{}
			var upstream, downstream bytes.Buffer
			r.upstream, r.downstream = &upstream, &downstream
			args := map[string]any{"expression": "println(1)", "frameId": float64(100)}
			if failure == "no frame" {
				delete(args, "frameId")
			}
			req := map[string]any{"type": "request", "command": "evaluate", "seq": float64(42), "arguments": args}
			if skip, err := r.evaluate(req, true); !skip || err != nil {
				t.Fatalf("request: %v %v", skip, err)
			}
			if failure != "no frame" {
				request := takeDebugMessage(t, &upstream)
				if failure == "moved" {
					r.evaluationEpoch++
				}
				resp := map[string]any{"type": "response", "command": "scopes", "request_seq": request["seq"], "success": failure != "delve", "message": "not stopped", "body": map[string]any{}}
				if skip, err := r.evaluate(resp, false); !skip || err != nil {
					t.Fatalf("response: %v %v", skip, err)
				}
			}
			response := takeDebugMessage(t, &downstream)
			if response["request_seq"] != float64(42) || response["success"] != false || !strings.Contains(response["message"].(string), "debug expression") {
				t.Fatalf("error response: %v", response)
			}
			if upstream.Len() != 0 || len(r.evaluations) != 0 {
				t.Fatal("failure forwarded or leaked requests")
			}
		})
	}
}
