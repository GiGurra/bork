package check

import (
	"strings"

	"github.com/GiGurra/bork/internal/naming"
	"github.com/GiGurra/bork/internal/syntax"
)

// These shipped pure helpers use the same Go implementation in generated
// code. Recognize their resolved package/function identity during expansion.
func (p *deriveExpansion) namingCall(call *syntax.Call) (any, bool) {
	id, ok := call.Fun.(*syntax.Ident)
	if !ok {
		return nil, false
	}
	alias, member, qualified := strings.Cut(id.Name, ".")
	pkg := p.template.Pkg
	if qualified {
		pkg = pkg.imports[alias]
	} else {
		member = id.Name
	}
	if pkg == nil || pkg.Path != "bork/codec" {
		return nil, false
	}
	if member == "omitAllowed" {
		if len(call.TypeArgs) != 1 || len(call.Args) != 2 {
			return nil, false
		}
		value, known := p.eval(call.Args[0])
		descriptor, fieldOK := value.(shapeField)
		policy, policyKnown := p.eval(call.Args[1])
		variant, policyOK := policy.(metadataVariant)
		if !known || !fieldOK || !policyKnown || !policyOK || variant.typ.Pkg == nil || variant.typ.Pkg.Path != "bork/codec" || variant.typ.Name != "Omit" {
			return nil, false
		}
		if variant.name == "Never" {
			return true, true
		}
		field := descriptor.field
		if field.Computed || field.Lazy || descriptor.positional() {
			return false, true
		}
		if variant.name == "Default" || variant.name == "NoneOrDefault" {
			if field.Decl == nil || field.Decl.Default == nil || !comparable(field.Type) {
				return false, true
			}
		}
		if variant.name == "None" || variant.name == "NoneOrDefault" {
			option, ok := field.Type.(*Sealed)
			if !ok || option.Base != p.c.preludePkg.TypeNamed("Option") {
				return false, true
			}
			if field.Decl != nil && field.Decl.Default != nil {
				p.c.ensureFieldDefault(field)
				value, known := p.checkedTagValue(p.c.info.fieldDefaults[field])
				defaultVariant, ok := value.(metadataVariant)
				if !known || !ok || defaultVariant.name != "None" || defaultVariant.typ.Base != option.Base {
					return false, true
				}
			}
		}
		return true, true
	}
	if len(call.TypeArgs) != 0 {
		return nil, false
	}
	switch member {
	case "wireNamesHave":
		if len(call.Args) != 2 {
			return nil, false
		}
		value, known := p.eval(call.Args[0])
		list, ok := value.(metadataList)
		target, targetKnown := p.eval(call.Args[1])
		name, nameOK := target.(string)
		if !known || !ok || !targetKnown || !nameOK {
			return nil, false
		}
		for _, item := range list.items {
			if !p.tick(call.Pos) {
				return nil, false
			}
			if item == name {
				return true, true
			}
		}
		return false, true
	case "wireNames", "wireAliasesAllowed":
		if len(call.Args) != 2 {
			return nil, false
		}
		first, firstKnown := p.eval(call.Args[0])
		second, secondKnown := p.eval(call.Args[1])
		if !firstKnown || !secondKnown {
			return nil, false
		}
		if member == "wireNames" {
			name, ok := first.(string)
			aliases, aliasesOK := second.(metadataList)
			if !ok || !aliasesOK || !p.charge(call.Pos, len(aliases.items)+len(name)) {
				return nil, false
			}
			return metadataList{items: append([]any{name}, aliases.items...), element: String}, true
		}
		aliases, ok := first.(metadataList)
		variant, variantOK := second.(bool)
		if !ok || !variantOK {
			return nil, false
		}
		for _, item := range aliases.items {
			name, ok := item.(string)
			if !ok || !p.charge(call.Pos, len(name)) {
				return nil, false
			}
			if name == "" || name == "<<" || variant && !naming.YAMLString(name) {
				return false, true
			}
		}
		return true, true
	case "wireNamespacesUnique":
		if len(call.Args) != 1 {
			return nil, false
		}
		value, known := p.eval(call.Args[0])
		namespaces, ok := value.(metadataList)
		if !known || !ok {
			return nil, false
		}
		seen := map[string]bool{}
		for _, item := range namespaces.items {
			names, ok := item.(metadataList)
			if !ok {
				return nil, false
			}
			for _, item := range names.items {
				name, ok := item.(string)
				if !ok || !p.charge(call.Pos, len(name)) {
					return nil, false
				}
				if seen[name] {
					return false, true
				}
				seen[name] = true
			}
		}
		return true, true
	case "wireNameAllowed", "variantNameAllowed":
		if len(call.Args) != 1 {
			return nil, false
		}
		value, known := p.eval(call.Args[0])
		name, ok := value.(string)
		if !known || !ok || !p.charge(call.Pos, len(name)) {
			return nil, false
		}
		return name != "" && name != "<<" && (member != "variantNameAllowed" || naming.YAMLString(name)), true
	case "Words":
		if len(call.Args) != 1 {
			return nil, false
		}
		value, known := p.eval(call.Args[0])
		text, ok := value.(string)
		if !known || !ok || !p.charge(call.Pos, len(text)) {
			return nil, false
		}
		words := naming.Words(text)
		items := make([]any, len(words))
		for i, word := range words {
			items[i] = word
		}
		return metadataList{items: items, element: String}, true
	case "JoinWords", "WireName":
		if len(call.Args) != 2 {
			return nil, false
		}
		value, known := p.eval(call.Args[0])
		policy, policyKnown := p.eval(call.Args[1])
		variant, ok := policy.(metadataVariant)
		if !known || !policyKnown || !ok || variant.typ.Pkg == nil || variant.typ.Pkg.Path != "bork/codec" || variant.typ.Name != "Naming" {
			return nil, false
		}
		var words []string
		if member == "WireName" {
			text, ok := value.(string)
			if !ok || !p.charge(call.Pos, len(text)) {
				return nil, false
			}
			if variant.name == "Verbatim" {
				return text, true
			}
			words = naming.Words(text)
		} else {
			list, ok := value.(metadataList)
			if !ok {
				return nil, false
			}
			for _, item := range list.items {
				word, ok := item.(string)
				if !ok || !p.charge(call.Pos, len(word)) {
					return nil, false
				}
				words = append(words, word)
			}
		}
		return naming.JoinWords(words, variant.name), true
	}
	return nil, false
}
