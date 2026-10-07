package check

import (
	"strconv"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

type shapeTuple []any
type shapeIndex int64

// stagedIterationNames validates the same irrefutable tuple/name/wildcard
// patterns as runtime loops before evaluating any iteration's body.
func (p *deriveExpansion) stagedIterationNames(pattern syntax.Pattern, typ Type, names map[string]diag.Pos) bool {
	switch pattern := pattern.(type) {
	case *syntax.WildcardPat:
		return true
	case *syntax.VariantPat:
		if len(pattern.Path) == 1 && !pattern.Context && !pattern.Braces && !pattern.Positional {
			name := pattern.Path[0]
			if _, duplicate := names[name]; duplicate {
				p.error(pattern.Pos, "tuple binding repeats name %s", name)
				return false
			}
			if p.names[name] {
				p.error(pattern.Pos, "%s is already defined in an enclosing scope", name)
				return false
			}
			names[name] = pattern.Pos
			return true
		}
	case *syntax.TuplePat:
		tuple, ok := typ.(*Record)
		if !ok || !tuple.Tuple {
			p.error(pattern.Pos, "tuple binding requires a tuple value, found %s", typ)
			return false
		}
		if len(pattern.Elems) != len(tuple.Fields) {
			p.error(pattern.Pos, "cannot match a %d-element tuple pattern against %s", len(pattern.Elems), typ)
			return false
		}
		for i, element := range pattern.Elems {
			if !p.stagedIterationNames(element, tuple.Fields[i].Type, names) {
				return false
			}
		}
		return true
	}
	p.error(pattern.Position(), "tuple binding requires names, wildcards or nested tuple patterns")
	return false
}

func (p *deriveExpansion) bindStagedIteration(pattern syntax.Pattern, value any) {
	switch pattern := pattern.(type) {
	case *syntax.VariantPat:
		if index, ok := value.(int64); ok {
			value = shapeIndex(index)
		}
		p.env[pattern.Path[0]] = value
	case *syntax.TuplePat:
		values := value.(shapeTuple)
		for i, element := range pattern.Elems {
			p.bindStagedIteration(element, values[i])
		}
	}
}

// deriveIterationType preserves tuple projections on named indexed metadata.
type deriveIterationType struct {
	kind  deriveDescriptor
	index bool
	tuple []*deriveIterationType
}

func (m *deriveMetadataTypes) iterationType(expr syntax.Expr) *deriveIterationType {
	switch expr := expr.(type) {
	case *syntax.Ident:
		return m.iterations[m.c.lookup(expr.Name)]
	case *syntax.Selector:
		if parent := m.iterationType(expr.X); parent != nil {
			index, err := strconv.Atoi(expr.Name)
			if err == nil && index >= 0 && index < len(parent.tuple) {
				return parent.tuple[index]
			}
		}
	}
	return nil
}

func (m *deriveMetadataTypes) sequenceType(expr syntax.Expr) *deriveIterationType {
	if id, ok := expr.(*syntax.Ident); ok {
		if typ := m.sequences[m.c.lookup(id.Name)]; typ != nil {
			return typ
		}
	}
	if call, ok := expr.(*syntax.Call); ok {
		if selector, ok := call.Fun.(*syntax.Selector); ok && selector.Name == "indexed" {
			if element := m.sequenceType(selector.X); element != nil {
				return &deriveIterationType{tuple: []*deriveIterationType{{index: true}, element}}
			}
		}
	}
	if kind := m.kind(expr); kind >= deriveFields {
		return &deriveIterationType{kind: kind - deriveFields + deriveField}
	}
	return nil
}

func (m *deriveMetadataTypes) bindIteration(loop *syntax.For, symbolic *deriveSymbolicTypes) {
	typ := m.sequenceType(loop.Items)
	if typ == nil {
		return
	}
	if m.iterations == nil {
		m.iterations = map[*local]*deriveIterationType{}
	}
	var bind func(syntax.Pattern, *deriveIterationType)
	bind = func(pattern syntax.Pattern, typ *deriveIterationType) {
		switch pattern := pattern.(type) {
		case *syntax.VariantPat:
			if len(pattern.Path) != 1 {
				return
			}
			local := m.c.lookup(pattern.Path[0])
			if local == nil {
				return
			}
			m.iterations[local] = typ
			if typ.index {
				local.typ = Int
				symbolic.locals[local] = deriveNativeTerm(Int, nil)
			}
			if typ.kind != 0 {
				m.locals[local] = typ.kind
			}
		case *syntax.TuplePat:
			if len(pattern.Elems) == len(typ.tuple) {
				for i, elem := range pattern.Elems {
					bind(elem, typ.tuple[i])
				}
			}
		}
	}
	if loop.Pattern != nil {
		bind(loop.Pattern, typ)
	} else {
		m.iterations[m.c.lookup(loop.Name)] = typ
		m.locals[m.c.lookup(loop.Name)] = typ.kind
	}
}
