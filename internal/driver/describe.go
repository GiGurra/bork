package driver

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/describe"
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
	files, info, err := Check(path)
	if err != nil {
		return nil, err
	}
	selected, err := describe.Lookup(files, info, pos, src)
	if err != nil {
		return nil, err
	}
	var facts []check.KnownFact
	var proof *check.Proof
	if selected.Value {
		facts, proof, err = check.DescribeFacts(info, selected.Func, selected.Expr, selected.Site, where, evaluator(path, files, info))
	} else if where != "" {
		return nil, fmt.Errorf("where queries need a value expression; select the call's opening parenthesis for its result")
	}
	if err != nil {
		return nil, err
	}
	methods := check.VisibleMethods(info, selected.Package, selected.Type)
	if facts == nil {
		facts = []check.KnownFact{}
	}
	if methods == nil {
		methods = []check.MethodDescription{}
	}
	return &describe.Result{SchemaVersion: 1, Position: pos, Type: check.TypeText(selected.Type, selected.Package), Definition: selected.Definition, Methods: methods, Facts: facts, Proof: proof}, nil
}
