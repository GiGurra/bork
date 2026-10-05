package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/describe"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/doccomment"
	"github.com/GiGurra/bork/internal/std"
)

// Describe compiles the selected file's package, then answers compiler queries
// at file:line:column. where is an optional clause, with the value implicit.
func Describe(position, where string) (*describe.Result, error) {
	pos, err := describe.ParsePosition(position)
	if err != nil {
		return nil, err
	}
	pos.File, err = filepath.Abs(pos.File)
	if err != nil {
		return nil, err
	}
	src, err := os.ReadFile(pos.File)
	if err != nil {
		return nil, err
	}
	path := filepath.Dir(pos.File)
	if strings.HasPrefix(string(src), "#!") {
		path = pos.File
	}
	program, err := checkProgramObserved(path, nil)
	if err != nil {
		return nil, err
	}
	return describeProgram(program, pos, src, where)
}

func describeProgram(program *compiledProgram, pos diag.Pos, src []byte, where string) (*describe.Result, error) {
	files, info := program.files, program.info
	selected, err := describe.Lookup(files, info, pos, src)
	if err != nil {
		return nil, err
	}
	documentation := ""
	if selected.Definition != nil {
		for _, file := range files {
			if file.Path == selected.Definition.File {
				documentation = doccomment.At(file, *selected.Definition)
				break
			}
		}
	}
	var facts []check.KnownFact
	var proof *check.Proof
	if selected.Value {
		facts, proof, err = check.DescribeFacts(info, selected.Func, selected.Expr, selected.Site, where, evaluatorWithContext(files, info, program.module, program.context))
	} else if where != "" {
		return nil, fmt.Errorf("where queries need a value expression; select the call's opening parenthesis for its result")
	}
	if err != nil {
		return nil, err
	}
	methods := check.VisibleMethods(info, selected.Package, selected.Type)
	// Embedded packages use virtual paths; disk imports use loader paths
	// relative to the working directory and need an absolute output path.
	diskFiles := map[string]bool{}
	for _, file := range files {
		if !file.Prelude && !strings.HasPrefix(file.Package, std.Prefix) {
			diskFiles[file.Path] = true
		}
	}
	absoluteDefinition := func(pos *diag.Pos) {
		if pos != nil && diskFiles[pos.File] {
			if absolute, err := filepath.Abs(pos.File); err == nil {
				pos.File = absolute
			}
		}
	}
	absoluteDefinition(selected.Definition)
	var rebinds *diag.Pos
	if selected.Definition != nil {
		definition := *selected.Definition
		for _, file := range program.files {
			if absolute, err := filepath.Abs(file.Path); err == nil && absolute == definition.File {
				definition.File = file.Path
				break
			}
		}
		rebinds = check.Rebinding(info, definition)
		absoluteDefinition(rebinds)
	}
	for i := range methods {
		absoluteDefinition(methods[i].Definition)
	}
	if facts == nil {
		facts = []check.KnownFact{}
	}
	if methods == nil {
		methods = []check.MethodDescription{}
	}
	if selected.ProviderBundle != nil {
		return &describe.Result{Rebinds: rebinds, Documentation: documentation, SchemaVersion: 1, Position: pos, Type: "provider bundle", Expression: selected.Expression, Definition: selected.Definition, ProviderBundle: selected.ProviderBundle, Methods: methods, Facts: facts}, nil
	}
	var async *check.AsyncDescription
	lazy := info.LazyFieldDescription(selected.Expr)
	if v, ok := selected.Expr.(*check.VarRef); ok && v.Var.Let != nil {
		lazy = v.Var.Let.Lazy
		async = v.Var.Let.Async
	}
	return &describe.Result{Rebinds: rebinds, Documentation: documentation, Async: async, Lazy: lazy, SchemaVersion: 1, Position: pos, Type: check.TypeText(selected.Type, selected.Package), Expression: selected.Expression, Definition: selected.Definition, Methods: methods, Facts: facts, Proof: proof, Callable: selected.Callable, TailCall: selected.TailCall, BelongsTo: belongsTo(info, selected), Ownership: ownership(info, selected), Assembly: selected.Assembly}, nil
}

// ownership says what the selected resource variable is there (see
// check.Info.Ownership).
func ownership(info *check.Info, selected *describe.Selection) string {
	if v, ok := selected.Expr.(*check.VarRef); ok && v.Pos() == v.Var.Pos {
		if text := info.VarOwnership[v.Var]; text != "" {
			return text
		}
	}
	return info.Ownership[selected.Expr]
}

// belongsTo names the scopes the selected value belongs to.
func belongsTo(info *check.Info, selected *describe.Selection) []string {
	if v, ok := selected.Expr.(*check.VarRef); ok && selected.Definition != nil && *selected.Definition == v.Var.Pos {
		if life := info.VarLifetimes[v.Var]; life != nil {
			return life
		}
	}
	return info.Lifetimes[selected.Expr]
}
