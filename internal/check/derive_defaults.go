package check

import (
	"reflect"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Defaults are already checked in their declaring package. Expansion selects
// a typed provider; it does not evaluate the default or resolve its source
// names in the template's package.
func (p *deriveExpansion) fieldDefault(call *syntax.Call, descriptor shapeField) syntax.Expr {
	field := descriptor.field
	if len(call.Args) != 0 || len(call.TypeArgs) != 0 {
		p.error(call.Pos, "field.default takes no arguments or type arguments")
		return &syntax.Block{Pos: call.Pos}
	}
	if field.Computed {
		p.error(call.Pos, "computed field %s requires a complete owner value; use field.read", field.Name)
		return &syntax.Block{Pos: call.Pos}
	}
	if field.Decl == nil || field.Decl.Default == nil {
		p.error(call.Pos, "field %s has no declared default", field.Name)
		return &syntax.Block{Pos: call.Pos}
	}
	p.c.ensureFieldDefault(field)
	if p.c.info.fieldDefaults[field] == nil {
		p.error(call.Pos, "field %s has no checked default provider", field.Name)
		return &syntax.Block{Pos: call.Pos}
	}
	if !p.chargeDefaultSyntax(call.Pos, reflect.ValueOf(p.c.info.fieldDefaults[field])) {
		return &syntax.Block{Pos: call.Pos}
	}
	generated := &syntax.Call{Pos: call.Pos, Fun: &syntax.Ident{Pos: call.Fun.Position(), Name: p.generatedName("default", p.template.Pkg)}}
	if p.c.info.shapeDefaults == nil {
		p.c.info.shapeDefaults = map[*syntax.Call]*Field{}
	}
	p.c.info.shapeDefaults[generated] = field
	return generated
}

// A provider replays checked syntax at each call. Count that output before
// lowering it, so a comprehension cannot replicate a large default for free.
func (p *deriveExpansion) chargeDefaultSyntax(pos diag.Pos, value reflect.Value) bool {
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Interface:
		return value.IsNil() || p.chargeDefaultSyntax(pos, value.Elem())
	case reflect.Pointer:
		if value.IsNil() {
			return true
		}
		if !p.enter(pos) {
			return false
		}
		defer func() { p.budget.depth-- }()
		switch node := value.Interface().(type) {
		case *syntax.RecordLit:
			// Lowering uses checked initializers, including implicit defaults
			// and computed recipes absent from the source's field list.
			inits := p.c.info.recordInits[node]
			if inits == nil {
				inits = node.Fields
			}
			return p.chargeDefaultSyntax(pos, reflect.ValueOf(node.Type)) && p.chargeDefaultSyntax(pos, reflect.ValueOf(inits))
		case *syntax.Call:
			args := p.c.info.callArgs[node]
			if args == nil {
				args = node.Args
			}
			return p.chargeDefaultSyntax(pos, reflect.ValueOf(node.Fun)) &&
				p.chargeDefaultSyntax(pos, reflect.ValueOf(node.TypeArgs)) &&
				p.chargeDefaultSyntax(pos, reflect.ValueOf(args))
		case *syntax.StringLit:
			if !p.charge(pos, len(node.Value)) {
				return false
			}
		case *syntax.IntLit:
			if !p.charge(pos, len(node.Text)) {
				return false
			}
		case *syntax.FloatLit:
			if !p.charge(pos, len(node.Text)) {
				return false
			}
		}
		return p.chargeDefaultSyntax(pos, value.Elem())
	case reflect.Struct:
		for _, index := range walkableSyntaxFields(value.Type()) {
			if !p.chargeDefaultSyntax(pos, value.Field(index)) {
				return false
			}
		}
	case reflect.Slice:
		if !p.charge(pos, value.Len()) {
			return false
		}
		for i := range value.Len() {
			if !p.chargeDefaultSyntax(pos, value.Index(i)) {
				return false
			}
		}
	}
	return true
}
