package driver

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/describe"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// EditorCompletion contains compiler-owned symbol metadata, independent of LSP.
type EditorCompletion struct {
	Name, Detail, Kind, Text string
	typeOf                   check.Type
	Rank                     int
	ImportPath               string
}

func (a *EditorAnalysis) editorFile(path string) (*syntax.File, *check.Package) {
	for _, file := range a.program.files {
		abs, _ := filepath.Abs(file.Path)
		if abs != path {
			continue
		}
		for _, pkg := range a.program.info.Packages {
			if pkg.Path == file.Package {
				return file, pkg
			}
		}
	}
	return nil, nil
}

// EditorSymbols lists package, prelude and lexically visible local symbols.
func (a *EditorAnalysis) EditorSymbols(pos diag.Pos) []EditorCompletion {
	file, from := a.editorFile(pos.File)
	if file == nil {
		return nil
	}
	var out []EditorCompletion
	seen := map[string]bool{}
	add := func(name, detail, kind string, rank int, typ check.Type) {
		if name == "" || name == "_" {
			return
		}
		if seen[name] {
			if rank == 0 {
				for i, c := range out {
					if c.Name == name {
						out[i] = EditorCompletion{Name: name, Detail: detail, Kind: kind, Rank: rank, typeOf: typ}
						break
					}
				}
			}
			return
		}
		seen[name] = true
		out = append(out, EditorCompletion{Name: name, Detail: detail, Kind: kind, Rank: rank, typeOf: typ})
	}
	before := func(p diag.Pos) bool { return p.Line < pos.Line || p.Line == pos.Line && p.Col <= pos.Col }
	inside := func(body *check.Block) bool { return body != nil && before(body.Pos()) && !before(body.End) }
	tokens, _ := syntax.Lex(file.Path, []byte(file.Source), &diag.List{})
	ends := map[diag.Pos]diag.Pos{}
	for _, t := range tokens {
		ends[t.Pos] = t.End
	}
	exprEnd := func(x check.Expr) diag.Pos {
		end := x.Pos()
		check.WalkComptime(x, func(child check.Expr) bool {
			at := child.TokenPos()
			if block, ok := child.(*check.Block); ok {
				at = block.End
			}
			if at.File != file.Path {
				return false
			}
			if tokenEnd, ok := ends[at]; ok {
				at = tokenEnd
			}
			if at.Line > end.Line || at.Line == end.Line && at.Col > end.Col {
				end = at
			}
			return true
		})
		return end
	}
	insideExpr := func(x check.Expr) bool {
		if x == nil || !before(x.Pos()) {
			return false
		}
		if block, ok := x.(*check.Block); ok {
			return inside(block)
		}
		end := exprEnd(x)
		return pos.Line < end.Line || pos.Line == end.Line && pos.Col <= end.Col
	}
	addVar := func(v *check.Var) {
		if v != nil {
			add(v.Name, check.TypeText(v.Type, from), "variable", 0, v.Type)
		}
	}
	var addPattern func(*check.Pat)
	addPattern = func(p *check.Pat) {
		if p == nil {
			return
		}
		addVar(p.Var)
		addPattern(p.Sub)
		addPattern(p.Rest)
		for _, field := range p.Fields {
			addPattern(field.Pat)
		}
		for _, elem := range p.Elems {
			addPattern(elem)
		}
	}
	for _, fn := range a.editorFunctions() {
		if fn.Decl.Pos.File != file.Path || !inside(fn.Body) {
			continue
		}
		for _, v := range fn.ParamVars {
			addVar(v)
		}
		check.WalkComptime(fn.Body, func(x check.Expr) bool {
			switch x := x.(type) {
			case *check.Block:
				if !inside(x) {
					return false
				}
				for _, stmt := range x.Stmts {
					if let, ok := stmt.(*check.Let); ok && before(exprEnd(let.Value)) && !insideExpr(let.Value) {
						addVar(let.Var)
					}
				}
			case *check.For:
				if inside(x.Body) {
					addVar(x.Var)
				}
			case *check.ScopeBlock:
				if inside(x.Body) {
					addVar(x.Var)
				}
			case *check.Lambda:
				if !insideExpr(x.Body) {
					return false
				}
				for _, param := range x.Params {
					addVar(param)
				}
			case *check.Match:
				for _, arm := range x.Arms {
					if insideExpr(arm.Body) || insideExpr(arm.Pat.Guard) {
						addPattern(arm.Pat)
					}
				}
			}
			return before(x.Pos())
		})
	}
	for _, fn := range a.program.info.Funcs {
		if !fn.Decl.ScriptMain && !fn.Decl.IsMethod {
			add(fn.Decl.Name, check.TypeText(&check.FuncType{Params: fn.Params, Result: fn.Result, Effects: fn.Effects}, from), "function", 1, nil)
		}
	}
	for name, typ := range check.EditorVisibleTypes(a.program.info, from) {
		add(name, check.TypeText(typ, from), "type", 1, typ)
	}
	for _, b := range a.program.info.PackageBindings {
		if b.Var.Pos.File == file.Path || b.Boundary.Pkg == from {
			add(b.Var.Name, check.TypeText(b.Var.Type, from), "variable", 1, b.Var.Type)
		}
	}
	for _, imp := range file.Imports {
		add(imp.Name, imp.Path, "module", 1, nil)
	}
	slices.SortFunc(out, func(a, b EditorCompletion) int {
		if a.Rank != b.Rank {
			return a.Rank - b.Rank
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// EditorMembers exposes checked fields and methods of a selected value.
func (a *EditorAnalysis) EditorMembers(pos diag.Pos) []EditorCompletion {
	file, from := a.editorFile(pos.File)
	if file == nil {
		return nil
	}
	pos.File = file.Path
	selected, err := describe.Lookup(a.program.files, a.program.info, pos, []byte(file.Source))
	if err != nil {
		return nil
	}
	out := editorFields(selected.Type, from)
	for _, method := range check.VisibleMethods(a.program.info, from, selected.Type) {
		if method.Ambiguity == "" {
			out = append(out, EditorCompletion{Name: method.Name, Detail: method.Type, Kind: "method"})
		}
	}
	return out
}

func editorFields(typ check.Type, from *check.Package) []EditorCompletion {
	var fields []*check.Field
	switch t := typ.(type) {
	case *check.Record:
		fields = t.Fields
	}
	var out []EditorCompletion
	for _, f := range fields {
		out = append(out, EditorCompletion{Name: f.Name, Detail: check.TypeText(f.Type, from), Kind: "field"})
	}
	return out
}

// EditorTypeFields resolves a literal or destructuring head using the parser
// and checker, including import aliases and generic substitutions.
func (a *EditorAnalysis) EditorTypeFields(path, head string) []EditorCompletion {
	_, from := a.editorFile(path)
	if from == nil {
		return nil
	}
	parse := func(text string) *syntax.TypeExpr {
		d := &diag.List{}
		files := syntax.ParseFiles([]string{path}, [][]byte{[]byte("type Completion = " + text + "\n")}, false, d)
		if d.Len() != 0 || len(files) != 1 || len(files[0].Types) != 1 {
			return nil
		}
		return files[0].Types[0].Alias
	}
	if typ := parse(head); typ != nil {
		fields := check.EditorRecordFields(a.program.info, from, typ)
		var out []EditorCompletion
		for _, field := range fields {
			out = append(out, EditorCompletion{Name: field.Name, Detail: check.TypeText(field.Type, from), Kind: "field"})
		}
		if len(out) > 0 {
			return out
		}
	}
	if dot := strings.LastIndexByte(head, '.'); dot > 0 {
		if owner := parse(head[:dot]); owner != nil {
			var out []EditorCompletion
			for _, field := range check.EditorVariantFields(a.program.info, from, owner, head[dot+1:]) {
				out = append(out, EditorCompletion{Name: field.Name, Detail: check.TypeText(field.Type, from), Kind: "field"})
			}
			return out
		}
	}
	return nil
}

// EditorMatchArms describes patterns for the compiler-selected scrutinee type.
func (a *EditorAnalysis) EditorMatchArms(pos diag.Pos) []EditorCompletion {
	return a.editorMatchArms(pos, false)
}

func (a *EditorAnalysis) editorMatchArms(pos diag.Pos, context bool) []EditorCompletion {
	file, from := a.editorFile(pos.File)
	if file == nil {
		return nil
	}
	pos.File = file.Path
	var typ check.Type
	for _, fn := range a.editorFunctions() {
		if fn.Decl.Pos.File != file.Path {
			continue
		}
		check.WalkComptime(fn.Body, func(x check.Expr) bool {
			if m, ok := x.(*check.Match); ok && m.Pos() == pos {
				typ = m.X.Type()
				return false
			}
			return true
		})
	}
	if typ == nil {
		return nil
	}
	var out []EditorCompletion
	var add func(check.Type)
	add = func(member check.Type) {
		switch t := member.(type) {
		case *check.Sealed:
			owner := strings.Split(check.TypeText(t, from), "[")[0]
			for _, v := range check.EditorVisibleVariants(a.program.info, from, t) {
				if context && !check.EditorContextVariantUnique(typ, v.Name) {
					continue
				}
				name := owner + "." + v.Name
				if context {
					name = v.Name
				}
				text := name
				if len(v.Fields) > 0 {
					var fields []string
					for _, f := range v.Fields {
						fields = append(fields, f.Name)
					}
					text += " { " + strings.Join(fields, ", ") + " }"
				}
				out = append(out, EditorCompletion{Name: name, Text: text + " => ", Detail: "match arm", Kind: "enumMember"})
			}
		case *check.Union:
			for _, member := range t.Members {
				add(member)
			}
		default:
			if context {
				return
			}
			name := check.TypeText(member, from)
			out = append(out, EditorCompletion{Name: name, Text: fmt.Sprintf("value: %s => ", name), Detail: "match arm", Kind: "type"})
		}
	}
	add(typ)
	return out
}

// EditorNamedMembers resolves a visible binding in the checked snapshot when an
// unfinished edit has moved the receiver away from its former source position.
func (a *EditorAnalysis) EditorNamedMembers(pos diag.Pos, name string) []EditorCompletion {
	_, from := a.editorFile(pos.File)
	for _, symbol := range a.EditorSymbols(pos) {
		if symbol.Name != name || symbol.typeOf == nil {
			continue
		}
		out := editorFields(symbol.typeOf, from)
		for _, method := range check.VisibleMethods(a.program.info, from, symbol.typeOf) {
			if method.Ambiguity == "" {
				out = append(out, EditorCompletion{Name: method.Name, Detail: method.Type, Kind: "method"})
			}
		}
		return out
	}
	return nil
}

func (a *EditorAnalysis) EditorNamedCallable(path, name string) *check.CallableDescription {
	_, from := a.editorFile(path)
	if from == nil {
		return nil
	}
	return check.EditorCallable(a.program.info, from, name)
}

// EditorPackageSymbols lists exports of a package already imported by the file.
func (a *EditorAnalysis) EditorPackageSymbols(path, alias string) []EditorCompletion {
	file, from := a.editorFile(path)
	if file == nil {
		return nil
	}
	pkgPath := ""
	for _, imp := range file.Imports {
		if imp.Name == alias {
			pkgPath = imp.Path
		}
	}
	if pkgPath == "" {
		return nil
	}
	var out []EditorCompletion
	for _, target := range a.program.files {
		if target.Package != pkgPath {
			continue
		}
		for _, decl := range target.Funcs {
			fn := a.program.info.FuncOf[decl]
			if fn != nil && check.Exported(decl.Name) && !decl.IsMethod {
				out = append(out, EditorCompletion{Name: decl.Name, Kind: "function", Detail: check.TypeText(&check.FuncType{Params: fn.Params, Result: fn.Result, Effects: fn.Effects}, from)})
			}
		}
		for _, decl := range target.Types {
			if check.Exported(decl.Name) {
				out = append(out, EditorCompletion{Name: decl.Name, Kind: "type", Detail: "type"})
			}
		}
		for _, b := range target.Bindings {
			if check.Exported(b.Name) {
				out = append(out, EditorCompletion{Name: b.Name, Kind: "variable", Detail: "package value"})
			}
		}
	}
	return out
}

// EditorSymbolsInBuffer maps an unfinished edit back to the checked snapshot.
func (a *EditorAnalysis) EditorSymbolsInBuffer(path, src string, pos diag.Pos) []EditorCompletion {
	return a.EditorSymbols(a.editorSnapshotPosition(path, src, pos))
}

func (a *EditorAnalysis) editorSnapshotPosition(path, current string, pos diag.Pos) diag.Pos {
	file, _ := a.editorFile(path)
	if file == nil {
		return pos
	}
	old := file.Source
	prefix := 0
	for prefix < len(old) && prefix < len(current) && old[prefix] == current[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(current)-prefix && old[len(old)-1-suffix] == current[len(current)-1-suffix] {
		suffix++
	}
	offset, _ := editorByteOffset(current, pos)
	if offset > prefix {
		if offset >= len(current)-suffix {
			offset += len(old) - len(current)
		} else {
			offset = prefix
		}
	}
	offset = min(max(offset, 0), len(old))
	line := strings.Count(old[:offset], "\n") + 1
	start := strings.LastIndexByte(old[:offset], '\n') + 1
	return diag.Pos{File: path, Line: line, Col: offset - start + 1}
}

func (a *EditorAnalysis) editorFunctions() []*check.Func {
	out := append([]*check.Func{}, a.program.info.Tests...)
	for _, fn := range a.program.info.FuncOf {
		out = append(out, fn)
	}
	return out
}
