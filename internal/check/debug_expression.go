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
	Name   string                 `json:"name"`
	Kind   string                 `json:"kind"`
	Fields map[string]DebugMember `json:"fields,omitempty"`
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
		default:
			return fmt.Errorf("debug expression: unsupported expression; use locals, scalar literals, record fields and scalar operators (calls and collection access are not supported)")
		}
		return nil
	}
	if err := validate(x); err != nil {
		return nil, nil, err
	}
	c.expr(x)
	if diags.Len() != 0 {
		return nil, nil, fmt.Errorf("%s", diags.Error())
	}
	// Aggregate comparison can require runtime helpers even when Go happens to
	// permit ==. Keep the supported operator subset scalar and deterministic.
	for node := range info.types {
		switch e := node.(type) {
		case *syntax.Unary:
			if t := info.types[e]; t == Float || t == Float32 {
				if _, literal := e.X.(*syntax.FloatLit); !literal {
					return nil, nil, fmt.Errorf("debug expression: floating-point arithmetic is unsupported because Delve does not preserve runtime rounding; inspect values or compare operands directly")
				}
			}
		case *syntax.Binary:
			if t := info.types[e]; t == Float || t == Float32 {
				return nil, nil, fmt.Errorf("debug expression: floating-point arithmetic is unsupported because Delve does not preserve runtime rounding; inspect values or compare operands directly")
			}
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
	return l.expr(x), info, nil
}
