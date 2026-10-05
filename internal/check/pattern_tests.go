package check

import (
	"github.com/GiGurra/bork/internal/syntax"
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
	c.pushScope()
	saved := c.patternTest
	c.patternTest = true
	pat := x.Pattern
	// Bare type names also carry their alias's facts. Record/variant
	// destructuring still goes through the ordinary pattern checker.
	if p, ok := pat.(*syntax.VariantPat); ok && len(p.Path) == 1 && !p.Braces && c.typeNamed(p.Path[0]) != nil {
		pat = &syntax.TypePat{Pos: p.Pos, Type: &syntax.TypeExpr{Pos: p.Pos, Name: p.Path[0]}}
	}
	checked := c.pattern(pat, st)
	c.patternTest = saved
	if checked != nil {
		c.patSources(checked, x.X, "", nil, nil)
		c.info.patternTests[x] = checked
	}
	c.popScope()
	if checked == nil {
		return Invalid
	}
	return Bool
}
