package check

import (
	"reflect"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// A checked value keeps its declaring package's lexical identities. Runtime
// materialization reuses checked source, as field.default does.
type metadataChecked struct{ value syntax.Expr }

func (p *deriveExpansion) tagTypeHead(pos diag.Pos, typ Type) *syntax.TypeHead {
	written := &syntax.TypeExpr{Pos: pos, Name: typ.String()}
	p.c.info.assemblyTypes[written] = typ
	return &syntax.TypeHead{Type: written, End: pos}
}

func (p *deriveExpansion) checkedTagLiteral(pos diag.Pos, value metadataChecked) syntax.Expr {
	if !p.chargeDefaultSyntax(pos, reflect.ValueOf(value.value)) {
		return &syntax.Block{Pos: pos}
	}
	call := &syntax.Call{Pos: pos, Fun: &syntax.Ident{Pos: pos, Name: p.generatedName("tag", p.template.Pkg)}}
	if p.c.info.tagValues == nil {
		p.c.info.tagValues = map[*syntax.Call]syntax.Expr{}
	}
	p.c.info.tagValues[call] = value.value
	return call
}

func (p *deriveExpansion) tagged(call *syntax.Call) (any, bool) {
	var groups []*syntax.TagGroup
	var written *syntax.TypeExpr
	if selector, ok := call.Fun.(*syntax.Selector); ok && selector.Name == "tagged" {
		descriptor, known := p.eval(selector.X)
		if !known {
			return nil, false
		}
		if len(call.Args) != 0 || len(call.TypeArgs) != 1 {
			p.error(call.Pos, "tagged takes one metadata type and no arguments")
			return nil, false
		}
		written = call.TypeArgs[0]
		switch descriptor := descriptor.(type) {
		case shapeField:
			if descriptor.field.Decl != nil {
				groups = descriptor.field.Decl.TagGroups
			}
		case shapeVariant:
			groups = descriptor.variant.Parent.Decl.Variants[descriptor.variant.Index].TagGroups
		default:
			return nil, false
		}
	} else if id, ok := call.Fun.(*syntax.Ident); ok && p.shapeCall(id.Name) == "tagged" {
		if len(call.Args) != 0 || len(call.TypeArgs) != 2 {
			p.error(call.Pos, "shape.tagged takes a target and metadata type and no arguments")
			return nil, false
		}
		target, _ := p.projectedTypeArg(call.TypeArgs[0])
		switch target := target.(type) {
		case *Record:
			if target.Decl != nil {
				groups = target.Decl.TagGroups
			}
		case *Sealed:
			if target.Decl != nil {
				groups = target.Decl.TagGroups
			}
		}
		written = call.TypeArgs[1]
	} else {
		return nil, false
	}
	typ, facts := p.projectedTypeArg(written)
	if typ == Invalid || hasTypeParam(typ) || len(facts) != 0 {
		p.error(call.Pos, "tagged requires a closed metadata type without runtime facts")
		return nil, false
	}
	option := instantiate(p.c.preludePkg.TypeNamed("Option"), []Type{typ}).(*Sealed)
	for _, group := range groups {
		literal := p.c.info.tagGroups[group]
		if literal != nil && identical(p.c.info.types[literal], typ) {
			if !p.chargeDefaultSyntax(call.Pos, reflect.ValueOf(literal)) {
				return nil, false
			}
			field := option.Variant("Some").Fields[0]
			return metadataVariant{typ: option, name: "Some", fields: map[string]any{field.Name: metadataChecked{value: literal}}}, true
		}
	}
	return metadataVariant{typ: option, name: "None"}, true
}

func (p *deriveExpansion) checkedTagProperty(value metadataChecked, name string) (any, bool) {
	literal, ok := value.value.(*syntax.RecordLit)
	if !ok {
		return nil, false
	}
	for _, init := range p.c.info.recordInits[literal] {
		if init.Name == name {
			return p.checkedTagValue(init.Value)
		}
	}
	return nil, false
}

func (p *deriveExpansion) checkedTagValue(value syntax.Expr) (any, bool) {
	switch value := value.(type) {
	case *syntax.IntLit:
		return p.eval(value)
	case *syntax.Unary:
		if n, ok := p.checkedTagValue(value.X); ok {
			if n, yes := n.(int64); yes {
				if value.Op == syntax.Minus {
					return -n, true
				}
				if value.Op == syntax.Caret {
					return ^n, true
				}
			}
		}
	case *syntax.StringLit:
		return value.Value, true
	case *syntax.BoolLit:
		return value.Value, true
	case *syntax.Selector:
		if variant := p.c.info.selectorVariants[value]; variant != nil {
			return metadataVariant{typ: variant.Parent, name: variant.Name}, true
		}
	case *syntax.ContextName:
		if variant := p.c.info.contextVariants[value]; variant != nil {
			return metadataVariant{typ: variant.Parent, name: variant.Name}, true
		}
	case *syntax.Call:
		if literal := p.c.info.variantCalls[value]; literal != nil {
			return p.checkedTagValue(literal)
		}
	case *syntax.RecordLit:
		if variant, ok := p.c.info.recordTargets[value].(*Variant); ok {
			fields := map[string]any{}
			for _, init := range p.c.info.recordInits[value] {
				fields[init.Name] = metadataChecked{value: init.Value}
			}
			return metadataVariant{typ: variant.Parent, name: variant.Name, fields: fields}, true
		}
	}
	return metadataChecked{value: value}, true
}

func (p *deriveExpansion) tagRecord(literal *syntax.RecordLit) (any, bool) {
	id, ok := literal.Type.(*syntax.Ident)
	if !ok {
		return nil, false
	}
	saved := p.c.pkg
	p.c.pkg = p.template.Pkg
	typ := p.c.typeNamed(id.Name)
	p.c.pkg = saved
	record, ok := typ.(*Record)
	if !ok || hasTypeParam(record) || len(record.TypeParams) != 0 {
		return nil, false
	}
	fields := map[string]any{}
	for _, init := range literal.Fields {
		if record.Field(init.Name) == nil {
			return nil, false
		}
		value, known := p.eval(init.Value)
		if !known {
			return nil, false
		}
		fields[init.Name] = value
	}
	for _, field := range record.Fields {
		if _, present := fields[field.Name]; present {
			continue
		}
		p.c.ensureFieldDefault(field)
		value := p.c.info.fieldDefaults[field]
		if value == nil || field.Computed {
			return nil, false
		}
		fields[field.Name], _ = p.checkedTagValue(value)
	}
	return metadataRecord{typ: record, fields: fields}, true
}

func (p *deriveExpansion) tagVariantLiteral(selector *syntax.Selector) (any, bool) {
	id, ok := selector.X.(*syntax.Ident)
	if !ok {
		return nil, false
	}
	saved := p.c.pkg
	p.c.pkg = p.template.Pkg
	typ := p.c.typeNamed(id.Name)
	p.c.pkg = saved
	sealed, ok := typ.(*Sealed)
	if !ok || len(sealed.TypeParams) != 0 || hasTypeParam(sealed) {
		return nil, false
	}
	variant := sealed.Variant(selector.Name)
	if variant == nil || len(variant.Fields) != 0 {
		return nil, false
	}
	return metadataVariant{typ: sealed, name: variant.Name}, true
}
