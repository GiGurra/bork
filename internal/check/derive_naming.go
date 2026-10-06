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
	if !ok || len(call.TypeArgs) != 0 {
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
	switch member {
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
	case "wireNamesUnique":
		if len(call.Args) != 1 {
			return nil, false
		}
		value, known := p.eval(call.Args[0])
		names, ok := value.(metadataList)
		if !known || !ok {
			return nil, false
		}
		seen := map[string]bool{}
		for _, item := range names.items {
			name, ok := item.(string)
			if !ok {
				return nil, false
			}
			if !p.charge(call.Pos, len(name)) {
				return nil, false
			}
			if seen[name] {
				return false, true
			}
			seen[name] = true
		}
		return true, true
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
