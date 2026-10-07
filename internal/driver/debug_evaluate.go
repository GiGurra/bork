package driver

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/gen"
)

type dapEvaluation struct {
	site    diag.Pos
	epoch   uint64
	request map[string]any
	locals  []check.DebugLocal
	scopes  []float64
	plan    *gen.DebugEvaluation
}

// evaluate collects raw frame locals; the compiler owns expression semantics.
func (r *dapRelay) evaluate(msg map[string]any, requests bool) (bool, error) {
	if requests {
		if msg["type"] != "request" || msg["command"] != "evaluate" || r.metadata == nil || r.metadata.Expressions == nil {
			return false, nil
		}
		args, _ := msg["arguments"].(map[string]any)
		frame, ok := args["frameId"].(float64)
		if !ok {
			return true, r.evaluateError(msg, "debug expression: select a paused stack frame first")
		}
		evaluation := &dapEvaluation{request: msg, epoch: r.evaluationEpoch, site: r.frames[frame]}
		return true, r.evaluateRequest(evaluation, "scopes", map[string]any{"frameId": frame})
	}
	if msg["type"] != "response" {
		return false, nil
	}
	seq, _ := msg["request_seq"].(float64)
	evaluation := r.evaluations[seq]
	if evaluation == nil {
		return false, nil
	}
	delete(r.evaluations, seq)
	if evaluation.epoch != r.evaluationEpoch {
		return true, r.evaluateError(evaluation.request, "debug expression: execution moved; evaluate again in the paused frame")
	}
	if msg["success"] != true {
		message, _ := msg["message"].(string)
		return true, r.evaluateError(evaluation.request, "debug expression: cannot inspect selected frame: "+message)
	}
	body, _ := msg["body"].(map[string]any)
	switch msg["command"] {
	case "scopes":
		scopes, _ := body["scopes"].([]any)
		for _, item := range scopes {
			scope, _ := item.(map[string]any)
			if scope["name"] == "Locals" || scope["name"] == "Arguments" || scope["presentationHint"] == "locals" || scope["presentationHint"] == "arguments" {
				if ref, ok := scope["variablesReference"].(float64); ok && ref != 0 {
					evaluation.scopes = append(evaluation.scopes, ref)
				}
			}
		}
	case "variables":
		vars, _ := body["variables"].([]any)
		for _, item := range vars {
			variable, _ := item.(map[string]any)
			name, _ := variable["name"].(string)
			if name == "" {
				continue
			}
			if _, mapped := r.metadata.Names[name]; !mapped && r.hidden(name) {
				continue
			}
			typ, _ := variable["type"].(string)
			evaluation.locals = append(evaluation.locals, check.DebugLocal{Name: name, GoName: name, Type: typ})
		}
	case "evaluate":
		result, _ := body["result"].(string)
		if err := evaluation.plan.Advance(result); err != nil {
			return true, r.evaluateError(evaluation.request, err.Error())
		}
	}
	if len(evaluation.scopes) > 0 {
		ref := evaluation.scopes[0]
		evaluation.scopes = evaluation.scopes[1:]
		return true, r.evaluateRequest(evaluation, "variables", map[string]any{"variablesReference": ref})
	}
	args, _ := evaluation.request["arguments"].(map[string]any)
	source, _ := args["expression"].(string)
	if evaluation.plan == nil {
		plan, err := gen.DebugExpressionPlan(source, r.metadata, evaluation.site, evaluation.locals)
		if err != nil {
			return true, r.evaluateError(evaluation.request, err.Error())
		}
		evaluation.plan = plan
	}
	if evaluation.plan.Read != "" {
		return true, r.evaluateRequest(evaluation, "evaluate", map[string]any{"frameId": args["frameId"], "expression": evaluation.plan.Read, "context": args["context"]})
	}
	args["expression"] = evaluation.plan.Expression
	resultType := evaluation.plan.Type
	if resultType != "" {
		if r.evaluationTypes == nil {
			r.evaluationTypes = map[float64]string{}
		}
		seq, _ := evaluation.request["seq"].(float64)
		r.evaluationTypes[seq] = resultType
		if evaluation.plan.ReadFailure != "" {
			if r.evaluationFailures == nil {
				r.evaluationFailures = map[float64]string{}
			}
			r.evaluationFailures[seq] = evaluation.plan.ReadFailure
		}
	}
	return true, r.writeDebugMessage(r.upstream, evaluation.request, true)
}

func (r *dapRelay) evaluateRequest(evaluation *dapEvaluation, command string, args map[string]any) error {
	r.internalSeq--
	if r.evaluations == nil {
		r.evaluations = map[float64]*dapEvaluation{}
	}
	r.evaluations[r.internalSeq] = evaluation
	return r.writeDebugMessage(r.upstream, map[string]any{"seq": r.internalSeq, "type": "request", "command": command, "arguments": args}, true)
}

func (r *dapRelay) evaluateError(request map[string]any, message string) error {
	return r.writeDebugMessage(r.downstream, map[string]any{"seq": float64(0), "type": "response", "request_seq": request["seq"], "command": "evaluate", "success": false, "message": message, "body": map[string]any{"error": map[string]any{"id": 1, "format": message, "showUser": true}}}, false)
}

func (r *dapRelay) writeDebugMessage(dst io.Writer, msg map[string]any, upstream bool) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if upstream {
		r.writeMu.Lock()
		defer r.writeMu.Unlock()
	} else {
		r.clientWriteMu.Lock()
		defer r.clientWriteMu.Unlock()
	}
	_, err = fmt.Fprintf(dst, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}

// Negotiate types with Delve for compiler queries even when the editor does not
// display them, then preserve the editor's requested response shape.
func stripDebugTypes(msg map[string]any) bool {
	if msg["type"] != "response" || msg["success"] != true {
		return false
	}
	body, _ := msg["body"].(map[string]any)
	switch msg["command"] {
	case "evaluate":
		delete(body, "type")
		return true
	case "variables":
		vars, _ := body["variables"].([]any)
		for _, item := range vars {
			if v, ok := item.(map[string]any); ok {
				delete(v, "type")
			}
		}
		return true
	}
	return false
}
