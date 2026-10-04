package check

import "github.com/GiGurra/bork/internal/syntax"

// Named interpolators use ordinary calls so types, effects, and lifetimes are
// checked by the same machinery as their explicit library counterparts.
func (c *checker) interpolator(e *syntax.Interp, want Type) Type {
	parts := &syntax.StaticPartsLit{Pos: e.Pos, Parts: e.Parts}
	call := &syntax.Call{Pos: e.Pos, Fun: e.Prefix, Args: []syntax.Expr{parts}}
	for _, hole := range e.Exprs {
		pos := hole.Position()
		call = &syntax.Call{Pos: pos, Fun: &syntax.Selector{Pos: pos, X: call, Name: "Interpolate"}, Args: []syntax.Expr{hole}}
	}
	call = &syntax.Call{Pos: e.Pos, Fun: &syntax.Selector{Pos: e.Pos, X: call, Name: "Finish"}}
	c.info.interpolatorCalls[e] = call
	return c.exprWant(call, want)
}
