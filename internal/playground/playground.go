// Package playground exposes the compiler's process-free operations to browsers.
package playground

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/describe"
	"github.com/GiGurra/bork/internal/diag"
	borkformat "github.com/GiGurra/bork/internal/format"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
)

const MaxSourceBytes = 32 * 1024
const SourceFile = "playground.bork"

type Request struct {
	Action string `json:"action"`
	Source string `json:"source"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

type Response struct {
	OK          bool              `json:"ok"`
	NeedsLocal  bool              `json:"needs_local,omitempty"`
	Diagnostics []diag.Diagnostic `json:"diagnostics"`
	Source      string            `json:"source"`
	Description *describe.Result  `json:"description,omitempty"`
	Error       string            `json:"error,omitempty"`
}

// Handle never evaluates user code or starts a Go tool. Each request owns a
// fresh typed tree, so edits cannot reuse stale types or proven facts.
func Handle(request Request) (response Response) {
	response.Diagnostics = []diag.Diagnostic{}
	defer func() {
		if r := recover(); r != nil {
			response = Response{Error: fmt.Sprintf("checker failed: %v", r), Diagnostics: []diag.Diagnostic{}}
		}
		if response.Diagnostics == nil {
			response.Diagnostics = []diag.Diagnostic{}
		}
	}()
	if len(request.Source) > MaxSourceBytes {
		response.Error = "source exceeds the 32 KiB playground limit"
		return response
	}
	if request.Action == "format" {
		source, err := borkformat.Source(SourceFile, []byte(request.Source))
		if err != nil {
			response.Error = err.Error()
			return response
		}
		response.OK, response.Source = true, string(source)
		return response
	}
	if request.Action != "check" && request.Action != "describe" {
		response.Error = "unknown playground action"
		return response
	}
	diags := &diag.List{}
	file := syntax.Parse(SourceFile, []byte(request.Source), diags)
	if diags.Len() != 0 {
		response.Diagnostics = diags.Sorted()
		return response
	}
	unavailable := func(pos diag.Pos, feature string) {
		response.NeedsLocal = true
		diags.AddCode(pos, "playground.unsupported", "needs local bork: %s is unavailable in the browser; download the file and run bork check", feature)
	}
	for _, imp := range file.Imports {
		unavailable(imp.Pos, "package imports")
	}
	for _, fn := range file.Funcs {
		if fn.IsGo() {
			unavailable(fn.Pos, "Go interop")
		}
	}
	for _, typ := range file.Types {
		if typ.GoName != nil {
			unavailable(typ.Pos, "Go type bindings")
		}
	}
	walkSyntax(reflect.ValueOf(file), make(map[any]bool), func(node *syntax.Comptime) { unavailable(node.Pos, "comptime execution") })
	if diags.Len() != 0 {
		response.Diagnostics = diags.Sorted()
		return response
	}
	files := append(prelude.Parse(diags), file)
	info := check.Program(files, "", diags, nil)
	if diags.Len() == 0 && len(info.InterpolationBatches) > 0 {
		unavailable(diag.Pos{File: SourceFile, Line: 1, Col: 1}, "compile-time interpolation validation")
	}
	if diags.Len() == 0 {
		check.CheckEffects(files, info, diags)
	}
	if diags.Len() == 0 {
		check.CheckTailCalls(files, info, diags)
	}
	if diags.Len() == 0 {
		check.Lifetimes(files, info, diags)
	}
	if diags.Len() == 0 {
		// A nil evaluator silently skips pending proofs. An unavailable evaluator
		// must fail the check rather than accepting facts it has not established.
		check.Facts(files, info, diags, func([]check.Query) ([]bool, error) {
			response.NeedsLocal = true
			return nil, errors.New("needs local bork: compile-time predicate execution is unavailable in the browser")
		})
	}
	response.Diagnostics = diags.Sorted()
	if diags.Len() != 0 {
		return response
	}
	if request.Action == "describe" {
		pos := diag.Pos{File: SourceFile, Line: request.Line, Col: request.Column}
		selected, err := describe.Lookup(files, info, pos, []byte(request.Source))
		if err != nil {
			response.Error = err.Error()
			return response
		}
		response.Description = &describe.Result{
			SchemaVersion: 1, Position: pos, Type: check.TypeText(selected.Type, selected.Package),
			Expression: selected.Expression, Definition: selected.Definition, Callable: selected.Callable,
		}
	}
	diags.Append(check.DebugWarnings(info))
	diags.Append(check.LazyWarnings(info))
	diags.Append(check.MigrationWarnings(info))
	response.Diagnostics = diags.Sorted()
	response.OK = true
	return response
}

// Inspect nested comptime expressions before checking. Instance methods point
// back to their owning declarations, so visit each pointer only once.
func walkSyntax(value reflect.Value, seen map[any]bool, visit func(*syntax.Comptime)) {
	if !value.IsValid() {
		return
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return
		}
		if value.Kind() == reflect.Pointer {
			key := value.Interface()
			if seen[key] {
				return
			}
			seen[key] = true
		}
		if node, ok := value.Interface().(*syntax.Comptime); ok {
			visit(node)
			return
		}
		walkSyntax(value.Elem(), seen, visit)
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			walkSyntax(value.Field(i), seen, visit)
		}
	case reflect.Slice:
		for i := 0; i < value.Len(); i++ {
			walkSyntax(value.Index(i), seen, visit)
		}
	}
}
