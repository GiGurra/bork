package gen

import (
	"go/ast"
	"go/parser"
	"go/token"

	"github.com/GiGurra/bork/internal/check"
)

// DebugMap describes compiler-owned names; adapters need no lowering rules.
type DebugMap struct {
	Source         string               `json:"source"`
	Names          map[string]string    `json:"names"`
	Version        int                  `json:"version"`
	Types          map[string]DebugType `json:"types"`
	Functions      map[string]string    `json:"functions"`
	HiddenPrefixes []string             `json:"hiddenPrefixes"`
}

type DebugType struct {
	Name   string                `json:"name"`
	Kind   string                `json:"kind"`
	Fields map[string]DebugField `json:"fields,omitempty"`
}

type DebugField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func (g *gen) debugMap(source []byte) *DebugMap {
	m := &DebugMap{Source: g.debugSource, Names: map[string]string{}, Version: 1, Types: map[string]DebugType{}, Functions: map[string]string{}, HiddenPrefixes: []string{"_", "~", "."}}
	for typ, goName := range basicGoNames {
		if len(goName) > 0 && goName[0] == '_' {
			goName = "main." + goName
		}
		m.Types[goName] = DebugType{Name: typ.String(), Kind: "scalar"}
	}
	for reserved := range goReserved {
		m.Names[name(reserved).Name] = reserved
	}
	fields := func(fs []*check.Field) map[string]DebugField {
		out := map[string]DebugField{}
		for _, f := range fs {
			out[name(f.Name).Name] = DebugField{Name: f.Name, Type: f.Type.String()}
		}
		return out
	}
	for _, typ := range g.info.TypeOrder {
		switch t := typ.(type) {
		case *check.Record:
			m.Types["main."+typeName(t.Name, t.Pkg).Name] = DebugType{Name: t.Name, Kind: "record", Fields: fields(t.Fields)}
		case *check.Sealed:
			goName := "main." + typeName(t.Name, t.Pkg).Name
			m.Types[goName] = DebugType{Name: t.Name, Kind: "union"}
			for _, v := range t.Variants {
				kind, label := "variant", t.Name+"."+v.Name
				if t.Prelude && t.Name == "Option" {
					kind, label = "option", v.Name
				}
				m.Types[goName+"_"+v.Name] = DebugType{Name: label, Kind: kind, Fields: fields(v.Fields)}
			}
		}
	}
	for _, fn := range g.info.FuncOf {
		if fn.Decl != nil && g.debugFiles[fn.Decl.Pos.File] {
			m.Functions["main."+g.funcName(fn).Name] = fn.Decl.Name
		}
	}
	if g.usesScopes {
		if label, ok := m.Functions["main.main"]; ok {
			m.Functions["main._borkMain"] = label
			m.Functions["main.main"] = ""
		}
	}
	// Empty labels identify generated helpers. Source paths also cover methods and closures.
	if file, err := parser.ParseFile(token.NewFileSet(), "", source, 0); err == nil {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				key := "main." + fn.Name.Name
				if _, ok := m.Functions[key]; !ok {
					m.Functions[key] = ""
				}
			}
		}
	}
	return m
}
