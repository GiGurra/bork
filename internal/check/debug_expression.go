package check

import (
	"fmt"
	"go/constant"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// DebugShape retains only the compiler type information needed for read-only
// expressions. Keys in the shape graph are concrete generated Go type names.
type DebugShape struct {
	Name    string                 `json:"name"`
	Kind    string                 `json:"kind"`
	Fields  map[string]DebugMember `json:"fields,omitempty"`
	Element string                 `json:"element,omitempty"`
	Option  string                 `json:"option,omitempty"`
	Some    string                 `json:"some,omitempty"`
	None    string                 `json:"none,omitempty"`
}

// DebugListGet is evaluated in two read-only steps: test the bounds, then read
// the element into a temporary Option variant. The compiler owns both steps.
type DebugListGet struct {
	expr
	List, Index        Expr
	Option, Some, None string
}

type DebugMember struct {
	Type     string `json:"type"`
	Deferred bool   `json:"deferred,omitempty"`
}

// DebugLocal describes a variable actually available in the selected frame.
type DebugLocal struct{ Name, GoName, Type string }

type debugOpaque string

func (t debugOpaque) String() string { return string(t) }

// DebugExpression checks a restricted expression using normal operator and
// field checking, then lowers it through the normal checked-tree builder.
func DebugExpression(source string, shapes map[string]DebugShape, locals []DebugLocal) (Expr, *Info, error) {
	diags := &diag.List{}
	x := syntax.ParseExpression("<debug expression>", []byte(source), diags)
	if diags.Len() != 0 {
		return nil, nil, fmt.Errorf("%s", diags.Error())
	}
	pkg := &Package{}
	info := &Info{types: map[syntax.Expr]Type{}, consts: map[syntax.Expr]constant.Value{}, defs: map[*syntax.Ident]any{}, exprOwners: map[syntax.Expr]*Func{}}
	c := &checker{info: info, diags: diags, pkg: pkg, preludePkg: pkg, scopes: []map[string]*local{{}}}
	types := map[string]Type{}
	var resolve func(string) Type
	resolve = func(key string) Type {
		key = strings.ReplaceAll(key, " ", "")
		if t := types[key]; t != nil {
			return t
		}
		shape, ok := shapes[key]
		if !ok {
			return debugOpaque(key)
		}
		if shape.Kind == "scalar" {
			if t := basicTypes[shape.Name]; t != nil {
				types[key] = t
				return t
			}
		}
		if shape.Kind == "list" {
			list := &List{Elem: resolve(shape.Element)}
			types[key] = list
			return list
		}
		if shape.Kind != "record" {
			return debugOpaque(shape.Name)
		}
		record := &Record{Name: shape.Name, Pkg: pkg}
		types[key] = record
		for name, f := range shape.Fields {
			record.Fields = append(record.Fields, &Field{Name: name, Type: resolve(f.Type), Lazy: f.Deferred})
		}
		return record
	}
	vars := map[any]*Var{}
	ambiguous := map[string]bool{}
	for _, v := range locals {
		if _, exists := c.scopes[0][v.Name]; exists {
			ambiguous[v.Name] = true
		}
		variable := &Var{Name: v.Name, GoName: v.GoName, Type: resolve(v.Type), Kind: VarParam}
		c.scopes[0][v.Name] = &local{typ: variable.Type, decl: variable}
		vars[variable] = variable
	}
	// Reject unsupported syntax before checking; queries cannot trigger calls,
	// inference, effects, or declaration lookup outside the supplied frame.
	root := x
	var validate func(syntax.Expr) error
	validate = func(x syntax.Expr) error {
		switch e := x.(type) {
		case *syntax.IntLit, *syntax.FloatLit, *syntax.StringLit, *syntax.RuneLit, *syntax.BoolLit:
		case *syntax.Ident:
			if ambiguous[e.Name] {
				return fmt.Errorf("debug expression: ambiguous local %s", e.Name)
			}
			if c.lookup(e.Name) == nil {
				return fmt.Errorf("debug expression: undefined local %s", e.Name)
			}
			for _, v := range locals {
				if v.Name == e.Name {
					if _, known := shapes[v.Type]; !known {
						return fmt.Errorf("debug expression: local %s has unsupported debugger type %s", e.Name, v.Type)
					}
				}
			}
		case *syntax.Selector:
			return validate(e.X)
		case *syntax.Unary:
			return validate(e.X)
		case *syntax.Binary:
			if err := validate(e.X); err != nil {
				return err
			}
			return validate(e.Y)
		case *syntax.Call:
			selector, ok := e.Fun.(*syntax.Selector)
			if !ok {
				return fmt.Errorf("debug expression: unsupported expression; function calls require execution")
			}
			if err := validate(selector.X); err != nil {
				return err
			}
			if selector.Name == "runeAt" || selector.Name == "substring" {
				return fmt.Errorf("debug expression: unsupported expression; Unicode string access requires runtime decoding; inspect the string or a source-bound result")
			}
			if selector.Name != "get" || root != e {
				return fmt.Errorf("debug expression: unsupported expression; only a standalone List.get(index) read is supported")
			}
			if len(e.Args) != 1 || len(e.TypeArgs) != 0 {
				return fmt.Errorf("debug expression: List.get expects one Int index")
			}
			for _, argument := range e.Arguments {
				if argument.Name != "" {
					return fmt.Errorf("debug expression: use List.get(index) with a positional Int index")
				}
			}
			return validate(e.Args[0])
		default:
			return fmt.Errorf("debug expression: unsupported expression; use locals, scalar literals, record fields, scalar operators or a standalone List.get(index)")
		}
		return nil
	}
	if err := validate(x); err != nil {
		return nil, nil, err
	}
	var access *DebugListGet
	if call, ok := x.(*syntax.Call); ok {
		selector := call.Fun.(*syntax.Selector)
		receiver := c.expr(selector.X)
		list, ok := receiver.(*List)
		if !ok {
			return nil, nil, fmt.Errorf("debug expression: unsupported expression; collection lookup requires runtime helpers (only List.get is supported)")
		}
		if list.Elem == Float || list.Elem == Float32 {
			return nil, nil, fmt.Errorf("debug expression: List.get with floating payloads is unsupported because Delve's temporary Option can lose IEEE special values; inspect the list's children instead")
		}
		index := c.exprWant(call.Args[0], Int)
		if index != Invalid && !assignable(index, Int) {
			return nil, nil, fmt.Errorf("debug expression: List.get index must be Int, got %s", index)
		}
		var shape DebugShape
		for key, typ := range types {
			if typ == receiver {
				shape = shapes[key]
				break
			}
		}
		if shape.Some == "" || shape.None == "" || shape.Option == "" {
			return nil, nil, fmt.Errorf("debug expression: List.get needs concrete Option payload types in this build; inspect the list's children instead")
		}
		access = &DebugListGet{expr: expr{pos: x.Position(), typ: debugOpaque(shape.Option)}, Option: shape.Option, Some: shape.Some, None: shape.None}
	} else {
		c.expr(x)
	}
	for node, typ := range info.types {
		if selector, ok := node.(*syntax.Selector); ok && typ == Invalid {
			if _, opaque := info.types[selector.X].(debugOpaque); opaque {
				return nil, nil, fmt.Errorf("debug expression: payload projection needs checked narrowing in source; pause inside a match arm and inspect its bound locals")
			}
		}
	}
	if diags.Len() != 0 {
		return nil, nil, fmt.Errorf("%s", diags.Error())
	}
	// Aggregate comparison can require runtime helpers even when Go happens to
	// permit ==. Keep the supported operator subset scalar and deterministic.
	for node := range info.types {
		switch e := node.(type) {
		case *syntax.Binary:
			if info.constantOf(e) != nil {
				continue
			}
			if _, ok := info.types[e.X].(*Basic); !ok {
				return nil, nil, fmt.Errorf("debug expression: operators require scalar operands")
			}
			if _, ok := info.types[e.Y].(*Basic); !ok {
				return nil, nil, fmt.Errorf("debug expression: operators require scalar operands")
			}
		case *syntax.Selector:
			if r, ok := info.types[e.X].(*Record); ok && r.Field(e.Name).Lazy {
				return nil, nil, fmt.Errorf("debug expression: deferred field %s requires execution", e.Name)
			}
		}
	}
	l := &lowerer{info: info, vars: vars}
	if access != nil {
		call := x.(*syntax.Call)
		access.List = l.expr(call.Fun.(*syntax.Selector).X)
		access.Index = l.expr(call.Args[0])
		return access, info, nil
	}
	return l.expr(x), info, nil
}
