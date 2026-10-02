package check

import "github.com/GiGurra/bork/internal/syntax"

// Default parameter values: `fn info(msg: String, attrs: Map[String,
// Int] = {:})` can be called as info("x"). A default is a literal (a
// number, string, rune, or Bool, or a list or map literal of them), so
// it means the same at every call, in any package. A call that leaves
// parameters out gets a copy of their defaults as arguments, which the
// rest of the compiler sees as if written there.

// checkDefaults checks fn's defaults: literals, of the parameter's
// type, and only on the last parameters.
func (c *checker) checkDefaults(fn *Func) {
	params := fn.Decl.Params
	for i, p := range params {
		if p.Default == nil {
			if i > 0 && params[i-1].Default != nil {
				c.errorf(p.Pos, "parameter %s needs a default value too: parameters after one with a default must have one", p.Name)
			}
			continue
		}
		if !isLiteral(p.Default) {
			c.errorf(p.Default.Position(), "a parameter's default must be a literal (a number, string, rune, Bool, or a list or map of them)")
			continue
		}
		if t := c.exprWant(copyLiteral(p.Default), fn.Params[i]); t != Invalid && !assignable(t, fn.Params[i]) {
			c.errorf(p.Default.Position(), "the default of %s must be %s, found %s", p.Name, fn.Params[i], t)
		}
	}
}

// addDefaults adds the defaults of the parameters a call leaves out.
func (c *checker) addDefaults(e *syntax.Call, fn *Func) {
	if fn.Decl == nil || len(e.Args) >= len(fn.Decl.Params) {
		return
	}
	missing := fn.Decl.Params[len(e.Args):]
	for _, p := range missing {
		if p.Default == nil || !isLiteral(p.Default) {
			return
		}
	}
	for _, p := range missing {
		e.Args = append(e.Args, copyLiteral(p.Default))
	}
}

// requiredParams is the number of fn's parameters without a default.
func requiredParams(fn *Func) int {
	n := len(fn.Params)
	if fn.Decl != nil {
		for n > 0 && n <= len(fn.Decl.Params) && fn.Decl.Params[n-1].Default != nil {
			n--
		}
	}
	return n
}

func isLiteral(x syntax.Expr) bool {
	switch x := x.(type) {
	case *syntax.IntLit, *syntax.FloatLit, *syntax.RuneLit, *syntax.StringLit, *syntax.BoolLit:
		return true
	case *syntax.Unary:
		return x.Op == syntax.Minus && isLiteral(x.X)
	case *syntax.ListLit:
		for _, el := range x.Elems {
			if !isLiteral(el) {
				return false
			}
		}
		return true
	case *syntax.MapLit:
		for i := range x.Keys {
			if !isLiteral(x.Keys[i]) || !isLiteral(x.Values[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// copyLiteral copies a literal, so each use has its own type.
func copyLiteral(x syntax.Expr) syntax.Expr {
	switch x := x.(type) {
	case *syntax.IntLit:
		cp := *x
		return &cp
	case *syntax.FloatLit:
		cp := *x
		return &cp
	case *syntax.RuneLit:
		cp := *x
		return &cp
	case *syntax.StringLit:
		cp := *x
		return &cp
	case *syntax.BoolLit:
		cp := *x
		return &cp
	case *syntax.Unary:
		cp := *x
		cp.X = copyLiteral(x.X)
		return &cp
	case *syntax.ListLit:
		cp := &syntax.ListLit{Pos: x.Pos}
		for _, el := range x.Elems {
			cp.Elems = append(cp.Elems, copyLiteral(el))
		}
		return cp
	case *syntax.MapLit:
		cp := &syntax.MapLit{Pos: x.Pos}
		for i := range x.Keys {
			cp.Keys = append(cp.Keys, copyLiteral(x.Keys[i]))
			cp.Values = append(cp.Values, copyLiteral(x.Values[i]))
		}
		return cp
	}
	return x
}
