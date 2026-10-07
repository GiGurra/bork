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

func TestDebugEvaluateBounds(t *testing.T) {
	for _, outcome := range []string{"true", "false", "invalid", "moved", "delve"} {
		t.Run(outcome, func(t *testing.T) {
			r := presentationFixture()
			r.metadata.Expressions = map[string]check.DebugShape{
				"int64":   {Name: "Int", Kind: "scalar"},
				"[]int64": {Name: "List[Int]", Kind: "list", Element: "int64", Option: "main.Option[int64]", Some: "main.Option_Some[int64]", None: "main.Option_None[int64]"},
			}
			r.frames = map[float64]diag.Pos{100: {File: "main.bork", Line: 5}}
			r.metadata.Bindings = []gen.DebugBindings{{Start: diag.Pos{File: "main.bork", Line: 1}, End: diag.Pos{File: "main.bork", Line: 10}, Names: map[string]string{"xs": "xs"}}}
			var upstream, downstream bytes.Buffer
			r.upstream, r.downstream = &upstream, &downstream
			handle := func(message map[string]any, request bool) {
				t.Helper()
				if skip, err := r.evaluate(message, request); !skip || err != nil {
					t.Fatalf("evaluate: %v %v", skip, err)
				}
			}
			handle(map[string]any{"seq": float64(42), "type": "request", "command": "evaluate", "arguments": map[string]any{"expression": "xs.get(0)", "frameId": float64(100), "context": "watch"}}, true)
			scopes := takeDebugMessage(t, &upstream)
			handle(map[string]any{"type": "response", "command": "scopes", "request_seq": scopes["seq"], "success": true, "body": map[string]any{"scopes": []any{map[string]any{"name": "Locals", "variablesReference": float64(10)}}}}, false)
			variables := takeDebugMessage(t, &upstream)
			handle(map[string]any{"type": "response", "command": "variables", "request_seq": variables["seq"], "success": true, "body": map[string]any{"variables": []any{map[string]any{"name": "xs", "type": "[]int64"}}}}, false)
			bounds := takeDebugMessage(t, &upstream)
			if bounds["command"] != "evaluate" || bounds["seq"].(float64) >= 0 || bounds["arguments"].(map[string]any)["expression"] != "0 >= 0 && 0 < len(xs)" {
				t.Fatalf("bounds request: %v", bounds)
			}
			if outcome == "moved" {
				r.evaluationEpoch++
			}
			handle(map[string]any{"type": "response", "command": "evaluate", "request_seq": bounds["seq"], "success": outcome != "delve", "message": "not stopped", "body": map[string]any{"result": outcome}}, false)
			if outcome == "true" || outcome == "false" {
				translated := takeDebugMessage(t, &upstream)
				want := "main.Option_Some[int64]{E0: xs[0]}"
				if outcome == "false" {
					want = "main.Option_None[int64]{}"
				}
				if translated["seq"] != float64(42) || translated["arguments"].(map[string]any)["expression"] != want || r.evaluationTypes[42] != "main.Option[int64]" {
					t.Fatalf("selected read: %v", translated)
				}
			} else {
				failure := takeDebugMessage(t, &downstream)
				if failure["request_seq"] != float64(42) || failure["success"] != false {
					t.Fatalf("failure: %v", failure)
				}
			}
			if upstream.Len() != 0 || downstream.Len() != 0 || len(r.evaluations) != 0 {
				t.Fatal("staged evaluation leaked messages or requests")
			}
		})
	}
}

func TestDebugEvaluateFloatStages(t *testing.T) {
	r := presentationFixture()
	r.metadata.Expressions = map[string]check.DebugShape{"float64": {Name: "Float", Kind: "scalar"}}
	r.frames = map[float64]diag.Pos{100: {File: "main.bork", Line: 5}}
	r.metadata.Bindings = []gen.DebugBindings{{Start: diag.Pos{File: "main.bork", Line: 1}, End: diag.Pos{File: "main.bork", Line: 10}, Names: map[string]string{"f": "f"}}}
	var upstream, downstream bytes.Buffer
	r.upstream, r.downstream = &upstream, &downstream
	handle := func(message map[string]any, request bool) {
		t.Helper()
		if skip, err := r.evaluate(message, request); !skip || err != nil {
			t.Fatalf("evaluate: %v %v", skip, err)
		}
	}
	start := func(seq float64, expression string) map[string]any {
		t.Helper()
		handle(map[string]any{"seq": seq, "type": "request", "command": "evaluate", "arguments": map[string]any{"expression": expression, "frameId": float64(100), "context": "watch"}}, true)
		scopes := takeDebugMessage(t, &upstream)
		handle(map[string]any{"type": "response", "command": "scopes", "request_seq": scopes["seq"], "success": true, "body": map[string]any{"scopes": []any{map[string]any{"name": "Locals", "variablesReference": float64(10)}}}}, false)
		variables := takeDebugMessage(t, &upstream)
		handle(map[string]any{"type": "response", "command": "variables", "request_seq": variables["seq"], "success": true, "body": map[string]any{"variables": []any{map[string]any{"name": "f", "type": "float64"}}}}, false)
		return takeDebugMessage(t, &upstream)
	}
	respond := func(query map[string]any, result string, success bool) {
		t.Helper()
		handle(map[string]any{"type": "response", "command": "evaluate", "request_seq": query["seq"], "success": success, "message": "not stopped", "body": map[string]any{"result": result}}, false)
	}
	// Complete the second plan while the first still waits for its operand.
	first := start(41, "f + 1.0 == f")
	second := start(42, "f * 2.0")
	respond(second, "4611686018427387904 = 0x4000000000000000", true)
	second = takeDebugMessage(t, &upstream)
	if second["seq"].(float64) >= 0 {
		t.Fatal("intermediate query escaped its internal sequence")
	}
	respond(second, "4", true)
	completed := takeDebugMessage(t, &upstream)
	if completed["seq"] != float64(42) || completed["arguments"].(map[string]any)["expression"] != "(float64(0x1p+02))" || r.evaluationTypes[42] != "float64" {
		t.Fatalf("second result: %v", completed)
	}
	for _, result := range []string{"4611686018427387904", "3", "4611686018427387904"} {
		respond(first, result, true)
		first = takeDebugMessage(t, &upstream)
	}
	if first["seq"] != float64(41) || !strings.Contains(first["arguments"].(map[string]any)["expression"].(string), "==") || r.evaluationTypes[41] != "bool" {
		t.Fatalf("first result: %v", first)
	}
	for _, failure := range []string{"moved", "invalid", "delve"} {
		query := start(43, "f / 1.0")
		respond(query, "4611686018427387904", true)
		query = takeDebugMessage(t, &upstream)
		if failure == "moved" {
			r.evaluationEpoch++
		}
		respond(query, "invalid", failure != "delve")
		response := takeDebugMessage(t, &downstream)
		if response["request_seq"] != float64(43) || response["success"] != false {
			t.Fatalf("failure %s: %v", failure, response)
		}
		if failure == "moved" && !strings.Contains(response["message"].(string), "execution moved") {
			t.Fatalf("epoch failure: %v", response)
		}
	}
	if upstream.Len() != 0 || downstream.Len() != 0 || len(r.evaluations) != 0 {
		t.Fatal("float plans leaked requests or responses")
	}
}
