package driver

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/describe"
	"github.com/GiGurra/bork/internal/diag"
)

type EditorNavigationItem struct {
	Name, Kind, Detail                       string
	Start, End, SelectionStart, SelectionEnd diag.Pos
}
type EditorCallEdge struct {
	Caller, Callee EditorNavigationItem
	Start, End     diag.Pos
}

func (a *EditorAnalysis) navigationItem(item check.EditorNavigationItem) EditorNavigationItem {
	declaration := a.absoluteSourcePosition(item.Pos)
	out := EditorNavigationItem{Name: item.Name, Kind: item.Kind, Detail: item.Container, Start: declaration}
	if a.navigationSymbols == nil {
		a.navigationSymbols = map[diag.Pos]map[string]check.Symbol{}
		for _, symbol := range a.Symbols() {
			if a.navigationSymbols[symbol.Declaration] == nil {
				a.navigationSymbols[symbol.Declaration] = map[string]check.Symbol{}
			}
			a.navigationSymbols[symbol.Declaration][symbol.Name] = symbol
		}
	}
	symbol, found := a.navigationSymbols[declaration][item.Name]
	if found {
		out.Kind = symbol.Kind
		out.SelectionStart = symbol.Definition
		out.SelectionEnd = symbol.End
		out.End = symbol.End
	}
	if !found {
		if item.Kind != "test" {
			return EditorNavigationItem{}
		}
		out.SelectionStart = declaration
		out.SelectionEnd = declaration
		out.SelectionEnd.Col += len("test")
		out.End = out.SelectionEnd
	}
	for _, fn := range a.program.info.FuncOf {
		if fn.Decl.Pos == item.Pos && fn.Decl.End.Line != 0 {
			out.End = a.absoluteSourcePosition(fn.Decl.End)
		}
	}
	for _, fn := range a.program.info.Tests {
		if fn.Decl.Pos == item.Pos && fn.Test.Body != nil {
			out.End = a.absoluteSourcePosition(fn.Test.Body.End)
			out.End.Col++
		}
	}
	return out
}

func (a *EditorAnalysis) EditorTypeDefinitions(pos diag.Pos) []EditorNavigationItem {
	file, _ := a.editorFile(pos.File)
	if file == nil {
		return nil
	}
	pos.File = file.Path
	typ := check.EditorWrittenType(a.program.info, pos)
	ref := a.ReferenceAt(a.absoluteSourcePosition(pos))
	if ref != nil {
		for _, decl := range file.Types {
			item := a.navigationItem(check.EditorNavigationItem{Name: decl.Name, Pos: decl.Pos, Kind: "type"})
			if item.SelectionStart == ref.Definition {
				typ = check.EditorDeclaredType(a.program.info, decl)
				break
			}
		}
	}
	if typ == nil {
		if selected, err := describe.Lookup(a.program.files, a.program.info, pos, []byte(file.Source)); err == nil {
			typ = selected.Type
		}
	}
	out := []EditorNavigationItem{}
	for _, item := range check.EditorTypeDefinitions(typ) {
		out = append(out, a.navigationItem(item))
	}
	return out
}

func (a *EditorAnalysis) EditorImplementations(pos diag.Pos) []EditorNavigationItem {
	file, _ := a.editorFile(pos.File)
	if file == nil {
		return nil
	}
	pos.File = file.Path
	declaration := diag.Pos{}
	typ := check.EditorWrittenType(a.program.info, pos)
	ref := a.ReferenceAt(a.absoluteSourcePosition(pos))
	if ref != nil {
		matches := func(raw check.EditorNavigationItem) bool {
			return a.navigationItem(raw).SelectionStart == ref.Definition
		}
		for _, class := range a.program.info.Classes {
			if matches(check.EditorNavigationItem{Name: class.Name, Pos: class.Decl.Pos, Kind: "class"}) {
				declaration = class.Decl.Pos
			}
		}
		for _, fn := range a.program.info.FuncOf {
			if fn.Class != nil && matches(check.EditorNavigationItem{Name: fn.Decl.Name, Pos: fn.Decl.Pos, Kind: "method"}) {
				declaration = fn.Decl.Pos
			}
		}
		for _, decl := range file.Types {
			if matches(check.EditorNavigationItem{Name: decl.Name, Pos: decl.Pos, Kind: "type"}) {
				typ = check.EditorDeclaredType(a.program.info, decl)
			}
		}
	}
	if selected, err := describe.Lookup(a.program.files, a.program.info, pos, []byte(file.Source)); err == nil {
		if typ == nil {
			typ = selected.Type
		}
		if selected.Definition != nil && declaration.Line == 0 {
			declaration = *selected.Definition
		}
	}
	out := []EditorNavigationItem{}
	for _, item := range check.EditorImplementations(a.program.info, declaration, typ) {
		out = append(out, a.navigationItem(item))
	}
	return out
}

func (a *EditorAnalysis) EditorCalls() []EditorCallEdge {
	if a.navigationCalls != nil {
		return slices.Clone(a.navigationCalls)
	}
	out := []EditorCallEdge{}
	sources := a.Sources()
	items := map[check.EditorNavigationItem]EditorNavigationItem{}
	item := func(raw check.EditorNavigationItem) EditorNavigationItem {
		if value, ok := items[raw]; ok {
			return value
		}
		value := a.navigationItem(raw)
		items[raw] = value
		return value
	}
	for _, edge := range check.EditorCalls(a.program.info) {
		start, end := edge.Start, edge.End
		absolute, err := filepath.Abs(start.File)
		if err != nil {
			continue
		}
		if _, ok := sources[absolute]; !ok {
			continue
		}
		start.File = absolute
		end.File = absolute
		caller, callee := item(edge.Caller), item(edge.Callee)
		if caller.SelectionStart.Line == 0 || callee.SelectionStart.Line == 0 {
			continue
		}
		out = append(out, EditorCallEdge{Caller: caller, Callee: callee, Start: start, End: end})
	}
	a.navigationCalls = out
	return slices.Clone(out)
}

func (a *EditorAnalysis) EditorCallHierarchy(pos diag.Pos) (*EditorNavigationItem, error) {
	for _, edge := range a.EditorCalls() {
		if pos.File == edge.Start.File && pos.Line == edge.Start.Line && pos.Col >= edge.Start.Col && (pos.Line < edge.End.Line || pos.Col < edge.End.Col) {
			item := edge.Callee
			return &item, nil
		}
	}
	definition, _ := a.Definition(pos)
	functions := make([]*check.Func, 0, len(a.program.info.FuncOf)+len(a.program.info.Tests))
	for _, fn := range a.program.info.FuncOf {
		functions = append(functions, fn)
	}
	functions = append(functions, a.program.info.Tests...)
	for _, fn := range functions {
		name, kind := fn.Decl.Name, "function"
		if fn.Test != nil {
			name, kind = fn.Test.Name, "test"
		}
		item := a.navigationItem(check.EditorNavigationItem{Name: name, Pos: fn.Decl.Pos, Kind: kind, Container: fn.Pkg.Path})
		matches := definition != nil && item.SelectionStart == *definition
		if fn.Test != nil && pos.File == item.SelectionStart.File && pos.Line == item.SelectionStart.Line && pos.Col >= item.SelectionStart.Col && pos.Col < item.SelectionEnd.Col {
			matches = true
		}
		if matches {
			return &item, nil
		}
	}
	return nil, fmt.Errorf("the selected symbol is not a declared function or test")
}
