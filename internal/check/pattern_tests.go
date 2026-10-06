package check

import (
	"github.com/GiGurra/bork/internal/syntax"
	"go/constant"
	"go/token"
	"strconv"
)

func (c *checker) isExpr(x *syntax.Is) Type {
	st := c.expr(x.X)
	if st == Invalid {
		return Invalid
	}
	if !isValue(st) {
		c.errorf(x.Pos, "cannot test a value of type %s against a pattern", st)
		return Invalid
	}
	if !c.bindingFreePattern(x.Pattern) {
		return Invalid
	}
	c.pushScope()
	saved := c.patternTest
	c.patternTest = true
	pat := x.Pattern
	// Bare type names also carry their alias's facts. Record/variant
	// destructuring still goes through the ordinary pattern checker.
	if p, ok := pat.(*syntax.VariantPat); ok && !p.Context && len(p.Path) == 1 && !p.Braces && !p.Positional && c.typeNamed(p.Path[0]) != nil {
		pat = &syntax.TypePat{Pos: p.Pos, Type: &syntax.TypeExpr{Pos: p.Pos, Name: p.Path[0]}}
	}
	checked := c.pattern(pat, st)
	if checked != nil && impossiblePattern(checked) {
		copy := *checked
		copy.Kind = PatNever
		checked = &copy
	}
	c.patternTest = saved
	if checked != nil {
		c.patSources(checked, x.X, "", nil, nil)
		c.patternTestLabels(checked, writtenText(x.X))
		c.info.patternTests[x] = checked
		if checked.Kind == PatLit {
			if value := c.info.constantOf(x.X); value != nil && value.Kind() != constant.Unknown {
				c.info.patternCertainties[x] = constant.Compare(value, token.EQL, checked.Lit)
			}
		}
	}
	c.popScope()
	if checked == nil {
		return Invalid
	}
	return Bool
}

func (c *checker) patternTestLabels(p *Pat, fallback string) {
	if p == nil {
		return
	}
	if p.Bind != "" {
		if src := c.info.patSources[p.bindNode]; src != nil {
			c.info.assemblyNames[p.bindNode] = writtenText(src.Subject) + src.Path
		} else {
			c.info.assemblyNames[p.bindNode] = fallback
		}
	}
	for _, field := range p.Fields {
		c.patternTestLabels(field.Pat, fallback+"."+field.Name)
	}
	for index, elem := range p.Elems {
		c.patternTestLabels(elem, fallback+"["+strconv.Itoa(index)+"]")
	}
	c.patternTestLabels(p.Sub, fallback)
}

func (c *checker) bindingFreePattern(p syntax.Pattern) bool {
	ok := true
	switch p := p.(type) {
	case *syntax.TypePat:
		if p.Name != "" && p.Name != "_" {
			c.errorf(p.Pos, "is patterns cannot bind names; write the type without a binding")
			return false
		}
	case *syntax.VariantPat:
		for _, elem := range p.Elems {
			ok = c.bindingFreePattern(elem) && ok
		}
		for _, field := range p.Fields {
			if field.Pattern == nil {
				c.errorf(field.Pos, "is patterns cannot bind fields; write %s: _ to ignore the value", field.Field)
				ok = false
			} else {
				ok = c.bindingFreePattern(field.Pattern) && ok
			}
		}
	case *syntax.TuplePat:
		for _, elem := range p.Elems {
			ok = c.bindingFreePattern(elem) && ok
		}
	case *syntax.ListPat:
		if p.Rest != "" {
			c.errorf(p.RestPos, "is patterns cannot bind the rest of a list; write ... without a name")
			ok = false
		}
		for _, elem := range p.Elems {
			ok = c.bindingFreePattern(elem) && ok
		}
	}
	return ok
}
func impossiblePattern(p *Pat) bool {
	if p == nil {
		return false
	}
	if p.Kind == PatNever {
		return true
	}
	for _, field := range p.Fields {
		if impossiblePattern(field.Pat) {
			return true
		}
	}
	for _, elem := range p.Elems {
		if impossiblePattern(elem) {
			return true
		}
	}
	return impossiblePattern(p.Sub)
}
func (c *checker) disjointPattern(p syntax.Pattern) *Pat {
	switch p := p.(type) {
	case *syntax.WildcardPat:
		return &Pat{Kind: PatWild, Type: Invalid}
	case *syntax.TuplePat:
		checked := &Pat{Kind: PatNever, Type: Invalid}
		for _, elem := range p.Elems {
			child := c.disjointPattern(elem)
			if child == nil {
				return nil
			}
			checked.Elems = append(checked.Elems, child)
		}
		return checked
	case *syntax.ListPat:
		checked := &Pat{Kind: PatNever, Type: Invalid}
		for _, elem := range p.Elems {
			child := c.disjointPattern(elem)
			if child == nil {
				return nil
			}
			checked.Elems = append(checked.Elems, child)
		}
		return checked
	case *syntax.TypePat:
		t := c.resolveType(p.Type)
		if t != Invalid {
			return c.pattern(p, t)
		}
	case *syntax.LitPat:
		t := c.expr(p.Value)
		if t != Invalid {
			return c.pattern(p, t)
		}
	case *syntax.VariantPat:
		t := Invalid
		if !p.Context && len(p.Path) > 0 {
			if named := c.typeNamed(p.Path[0]); named != nil {
				t = named
			}
		}
		return c.pattern(p, t)
	}
	return nil
}
