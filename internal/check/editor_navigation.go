package check

import (
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// EditorNavigationItem is a compiler declaration, before source-name mapping.
type EditorNavigationItem struct {
	Name      string
	Pos       diag.Pos
	Kind      string
	Container string
}
type EditorCallEdge struct {
	Caller, Callee, SourceCallee EditorNavigationItem
	Start, End                   diag.Pos
}

// EditorTypeDefinitions follows checked types, including generic/container and
// union components, without resolving names a second time.
func EditorTypeDefinitions(typ Type) []EditorNavigationItem {
	out := []EditorNavigationItem{}
	seen := map[Type]bool{}
	var visit func(Type)
	visit = func(typ Type) {
		if typ == nil || seen[typ] {
			return
		}
		seen[typ] = true
		add := func(decl *syntax.TypeDecl, pkg *Package) {
			if decl != nil {
				container := ""
				if pkg != nil {
					container = pkg.Path
				}
				out = append(out, EditorNavigationItem{Name: decl.Name, Pos: decl.Pos, Kind: "type", Container: container})
			}
		}
		switch typ := typ.(type) {
		case *Record:
			add(typ.Decl, typ.Pkg)
		case *Sealed:
			add(typ.Decl, typ.Pkg)
		case *Resource:
			add(typ.Decl, typ.Pkg)
		case *Opaque:
			add(typ.Decl, typ.Pkg)
		case *TypeParam:
			if typ.Decl != nil {
				out = append(out, EditorNavigationItem{Name: typ.Name, Pos: typ.Decl.Pos, Kind: "typeParameter"})
			}
		case *Union:
			for _, member := range typ.Members {
				visit(member)
			}
		case *List:
			visit(typ.Elem)
		case *Seq:
			visit(typ.Elem)
		case *Map:
			visit(typ.Key)
			visit(typ.Value)
		case *FuncType:
			for _, param := range typ.Params {
				visit(param)
			}
			visit(typ.Result)
		}
	}
	visit(typ)
	return uniqueNavigationItems(out)
}

// EditorWrittenType uses recorded compiler resolution at the written name.
func EditorWrittenType(info *Info, pos diag.Pos) Type {
	for written, typ := range info.writtenTypes {
		if written.Pos.File == pos.File && written.Pos.Line == pos.Line && pos.Col >= written.Pos.Col && pos.Col < written.Pos.Col+len(written.Name) {
			return typ
		}
	}
	return nil
}

func EditorDeclaredType(info *Info, decl *syntax.TypeDecl) Type {
	for _, pkg := range info.Packages {
		for _, entry := range pkg.types {
			if entry.decl == decl {
				return entry.typ
			}
		}
	}
	return nil
}

// EditorImplementations maps class declarations/methods to their checked
// instances, and sealed types to their source variants.
func EditorImplementations(info *Info, declaration diag.Pos, typ Type) []EditorNavigationItem {
	out := []EditorNavigationItem{}
	for _, class := range info.Classes {
		method := -1
		matches := class.Decl.Pos == declaration
		for i, fn := range class.Methods {
			if fn.Decl.Pos == declaration {
				matches = true
				method = i
			}
		}
		if !matches {
			continue
		}
		for _, instance := range info.ClassInstances {
			if instance.Class != class {
				continue
			}
			if method >= 0 && method < len(instance.Methods) {
				fn := instance.Methods[method]
				out = append(out, navigationFunction(fn))
			} else if instance.Decl != nil {
				out = append(out, EditorNavigationItem{Name: instance.Name, Pos: instance.Decl.Pos, Kind: "instance", Container: instance.Pkg.Path})
			}
		}
	}
	var visit func(Type)
	seen := map[Type]bool{}
	visit = func(typ Type) {
		if typ == nil || seen[typ] {
			return
		}
		seen[typ] = true
		switch typ := typ.(type) {
		case *Sealed:
			if typ.Decl != nil {
				for _, variant := range typ.Decl.Variants {
					out = append(out, EditorNavigationItem{Name: variant.Name, Pos: variant.Pos, Kind: "variant", Container: typ.Name})
				}
			}
		case *Union:
			for _, member := range typ.Members {
				visit(member)
			}
		}
	}
	visit(typ)
	return uniqueNavigationItems(out)
}

func navigationFunction(fn *Func) EditorNavigationItem {
	if fn == nil || fn.Decl == nil {
		return EditorNavigationItem{}
	}
	name, kind := fn.Decl.Name, "function"
	if fn.Decl.IsMethod {
		kind = "method"
	}
	if fn.Decl.IsPred {
		kind = "predicate"
	}
	if fn.Test != nil {
		name, kind = fn.Test.Name, "test"
	}
	container := ""
	if fn.Pkg != nil {
		container = fn.Pkg.Path
	}
	return EditorNavigationItem{Name: name, Pos: fn.Decl.Pos, Kind: kind, Container: container}
}

// EditorCalls retains checked source calls, including calls eliminated by
// constant folding. Indirect function-value calls have no static declaration.
func EditorCalls(info *Info) []EditorCallEdge {
	out := []EditorCallEdge{}
	for call, target := range info.callFuncs {
		caller := info.exprOwners[call]
		if caller == nil || target == nil {
			continue
		}
		sourceCallee := navigationFunction(target)
		if instance := info.instances[call]; target.Class != nil && instance != nil {
			for _, dict := range instance.Dicts {
				if dict.Class == target.Class && dict.Inst != nil {
					for i, method := range target.Class.Methods {
						if method == target && i < len(dict.Inst.Methods) {
							target = dict.Inst.Methods[i]
							break
						}
					}
				}
			}
		}
		start, end := call.Start, call.FunEnd
		if start.Line == 0 {
			start = call.Fun.Position()
		}
		if end.Line == 0 {
			end = call.Pos
		}
		out = append(out, EditorCallEdge{Caller: navigationFunction(caller), Callee: navigationFunction(target), SourceCallee: sourceCallee, Start: start, End: end})
	}
	slices.SortFunc(out, func(a, b EditorCallEdge) int {
		if n := strings.Compare(a.Start.File, b.Start.File); n != 0 {
			return n
		}
		if a.Start.Line != b.Start.Line {
			return a.Start.Line - b.Start.Line
		}
		return a.Start.Col - b.Start.Col
	})
	return out
}

func uniqueNavigationItems(items []EditorNavigationItem) []EditorNavigationItem {
	slices.SortFunc(items, func(a, b EditorNavigationItem) int {
		if n := strings.Compare(a.Pos.File, b.Pos.File); n != 0 {
			return n
		}
		if a.Pos.Line != b.Pos.Line {
			return a.Pos.Line - b.Pos.Line
		}
		return a.Pos.Col - b.Pos.Col
	})
	return slices.CompactFunc(items, func(a, b EditorNavigationItem) bool { return a.Pos == b.Pos })
}
