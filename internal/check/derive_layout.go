package check

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Layout values are compile-time literals of the public bork/shape layout
// records, their lists, and ForeignOption constants. Only layout evaluation
// (a template's ForeignRecord metadata) builds records and lists; declared
// field tags are available to every template.
type metadataRecord struct {
	typ    *Record
	fields map[string]any
}
type metadataList struct{ items []any }
type metadataVariant struct {
	typ  *Sealed
	name string
}

func (p *deriveExpansion) shapeType(name string) Type {
	if shape := p.c.pkgs["bork/shape"]; shape != nil {
		if typ := shape.TypeNamed(name); typ != nil {
			return typ
		}
	}
	return Invalid
}

// fieldTags lists a field's declared `go { name: "value" }` tags in order.
func (p *deriveExpansion) fieldTags(pos diag.Pos, field *Field) (any, bool) {
	if !p.charge(pos, len(field.GoTags)) {
		return nil, false
	}
	tag, _ := p.shapeType("Tag").(*Record)
	items := make([]any, len(field.GoTags))
	for i, declared := range field.GoTags {
		items[i] = metadataRecord{typ: tag, fields: map[string]any{"name": declared.Name, "value": declared.Value}}
	}
	return metadataList{items: items}, tag != nil
}

// layoutConstant evaluates a ForeignOption constant such as
// shape.ForeignOption.Pointer.
func (p *deriveExpansion) layoutConstant(x syntax.Expr) (any, bool) {
	var path string
	switch x := x.(type) {
	case *syntax.Ident:
		path = p.shapeCall(x.Name)
	case *syntax.Selector:
		if id, ok := x.X.(*syntax.Ident); ok {
			path = p.shapeCall(id.Name) + "." + x.Name
		}
	}
	typeName, variant, ok := strings.Cut(path, ".")
	if !ok || typeName != "ForeignOption" {
		return nil, false
	}
	sealed, _ := p.shapeType(typeName).(*Sealed)
	if sealed == nil || sealed.Variant(variant) == nil {
		p.error(x.Position(), "shape.ForeignOption has no variant %s", variant)
		return nil, false
	}
	return metadataVariant{typ: sealed, name: variant}, true
}

// stringOperation folds pure, bounded String methods of a metadata String.
func (p *deriveExpansion) stringOperation(x *syntax.Call, selector *syntax.Selector) (any, bool) {
	switch selector.Name {
	case "toUpper", "toLower", "capitalize":
	default:
		return nil, false
	}
	receiver, known := p.eval(selector.X)
	text, ok := receiver.(string)
	if !known || !ok || len(x.Args) != 0 || len(x.TypeArgs) != 0 {
		return nil, false
	}
	// Fold only the prelude's method; a package's own String method wins.
	if method, _ := p.c.methodNamed(String, selector.Name); method == nil || !method.Prelude || !p.charge(x.Pos, len(text)) {
		return nil, false
	}
	switch selector.Name {
	case "toUpper":
		return strings.ToUpper(text), true
	case "toLower":
		return strings.ToLower(text), true
	}
	first, size := utf8.DecodeRuneInString(text)
	if size == 0 {
		return text, true
	}
	return string(unicode.ToUpper(first)) + text[size:], true
}

func (p *deriveExpansion) layoutList(x *syntax.ListLit) (any, bool) {
	var items []any
	for _, element := range x.Elems {
		loop, ok := element.(*syntax.For)
		if !ok || !loop.Comptime || !loop.Comprehension {
			value, known := p.eval(element)
			if !known {
				return nil, false
			}
			items = append(items, value)
			continue
		}
		value, known := p.eval(loop.Items)
		sequence, yes := value.(shapeSequence)
		if !known || !yes {
			p.error(loop.Pos, "comptime for requires a compile-time shape sequence")
			return nil, false
		}
		saved, existed := p.env[loop.Name]
		for _, item := range sequence.items {
			if !p.tick(loop.Pos) {
				return nil, false
			}
			p.env[loop.Name] = item
			body := loop.Body.Tail
			if guard, ok := body.(*syntax.If); ok && guard.Comptime && guard.Else == nil {
				chosen, valid := p.condition(guard.Cond)
				if !valid {
					return nil, false
				}
				if !chosen {
					continue
				}
				body = guard.Then.Tail
			}
			value, known := p.eval(body)
			if !known {
				return nil, false
			}
			items = append(items, value)
		}
		if existed {
			p.env[loop.Name] = saved
		} else {
			delete(p.env, loop.Name)
		}
	}
	return metadataList{items: items}, true
}

func (p *deriveExpansion) layoutRecord(x *syntax.RecordLit) (any, bool) {
	id, ok := x.Type.(*syntax.Ident)
	if !ok {
		return nil, false
	}
	member := p.shapeCall(id.Name)
	if member != "Tag" && member != "ForeignField" && member != "ForeignRecord" {
		return nil, false
	}
	record := p.shapeType(member).(*Record)
	values := map[string]any{}
	for _, init := range x.Fields {
		field := record.Field(init.Name)
		if field == nil || values[init.Name] != nil {
			p.error(init.Pos, "unexpected or repeated field %s in a compile-time shape.%s", init.Name, member)
			return nil, false
		}
		value, known := p.eval(init.Value)
		if !known {
			return nil, false
		}
		if !layoutValue(value, field.Type) {
			p.error(init.Pos, "compile-time shape.%s field %s must be a compile-time %s", member, init.Name, field.Type)
			return nil, false
		}
		values[init.Name] = value
	}
	for _, field := range record.Fields {
		if values[field.Name] == nil {
			p.error(x.Type.Position(), "compile-time shape.%s requires field %s", member, field.Name)
			return nil, false
		}
	}
	return metadataRecord{typ: record, fields: values}, true
}

func layoutValue(value any, typ Type) bool {
	switch typ := typ.(type) {
	case *Basic:
		switch value.(type) {
		case string:
			return typ == String
		case int64:
			return typ == Int
		case bool:
			return typ == Bool
		}
	case *List:
		list, ok := value.(metadataList)
		for _, item := range list.items {
			if !layoutValue(item, typ.Elem) {
				return false
			}
		}
		return ok
	case *Record:
		record, ok := value.(metadataRecord)
		return ok && record.typ == typ
	case *Sealed:
		variant, ok := value.(metadataVariant)
		return ok && variant.typ == typ
	}
	return false
}

// layoutLiteral renders a layout value as checked source in the template's
// package, so runtime metadata equals the compile-time layout.
func (p *deriveExpansion) layoutLiteral(pos diag.Pos, value any) syntax.Expr {
	alias := ""
	for name, pkg := range p.template.Pkg.imports {
		if pkg.Path == "bork/shape" {
			alias = name
		}
	}
	switch value := value.(type) {
	case metadataList:
		out := &syntax.ListLit{Pos: pos}
		for _, item := range value.items {
			out.Elems = append(out.Elems, p.literal(pos, item))
		}
		return out
	case metadataRecord:
		out := &syntax.RecordLit{Type: &syntax.Ident{Pos: pos, Name: alias + "." + value.typ.Name}, End: pos}
		for _, field := range value.typ.Fields {
			out.Fields = append(out.Fields, &syntax.FieldInit{Pos: pos, Name: field.Name, Value: p.literal(pos, value.fields[field.Name])})
		}
		return out
	case metadataVariant:
		return &syntax.Ident{Pos: pos, Name: alias + "." + value.typ.Name + "." + value.name}
	}
	return nil
}
