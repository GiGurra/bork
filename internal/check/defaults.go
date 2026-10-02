package check

import "github.com/GiGurra/bork/internal/syntax"

// Default parameter values: `fn info(msg: String, attrs: Map[String,
// Int] = {:})` can be called as info("x"). A default is a closed value,
// which means the same at every call: a literal (a number, string,
// rune, or Bool, or a list or map literal of them), or a record or
// variant value of closed values (`Level.Info`, `Option.None`,
// `Config { retries: 3 }`). A call that leaves parameters out gets
// their defaults as arguments, which the rest of the compiler sees as
// if written there: a copy of a literal (which then takes its type
// from the call, so `xs: List[T] = []` works for every T), or else the
// default itself, checked once where it was declared, so its names
// mean what they mean there.

// ensureDefaults checks fn's defaults, once: closed values, of the
// parameter's type, and only on the last parameters.
func (c *checker) ensureDefaults(fn *Func) {
	if fn.defaultsChecked || fn.Decl == nil {
		return
	}
	fn.defaultsChecked = true
	params := fn.Decl.Params
	if len(params) != len(fn.Params) {
		return
	}
	// Check in the declaring package, outside any function.
	saved := *c
	c.pkg, c.fn, c.inPrelude = fn.Pkg, nil, fn.Prelude
	c.scopes = []map[string]*local{{}}
	c.typeParams, c.lambdaDepth, c.have = nil, 0, nil
	defer func() {
		shared := c.sharedDefaults
		*c = saved
		c.sharedDefaults = shared
	}()
	for i, p := range params {
		if p.Default == nil {
			if i > 0 && params[i-1].Default != nil {
				c.errorf(p.Pos, "parameter %s needs a default value too: parameters after one with a default must have one", p.Name)
			}
			continue
		}
		x := p.Default
		switch {
		case isLiteral(x):
			x = copyLiteral(x)
		case !isClosed(x):
			c.errorf(x.Position(), "a parameter's default must be a closed value: a literal, or a record or variant of them (such as Level.Info)")
			continue
		case hasTypeParam(fn.Params[i]):
			c.errorf(x.Position(), "the default of %s can only be a literal, since its type depends on a type parameter", p.Name)
			continue
		default:
			if c.sharedDefaults == nil {
				c.sharedDefaults = map[syntax.Expr]bool{}
			}
			c.sharedDefaults[x] = true
		}
		if t := c.exprWant(x, fn.Params[i]); t != Invalid && !assignable(t, fn.Params[i]) {
			c.errorf(p.Default.Position(), "the default of %s must be %s, found %s", p.Name, fn.Params[i], t)
		}
	}
}

// addDefaults adds the defaults of the parameters a call leaves out.
func (c *checker) addDefaults(e *syntax.Call, fn *Func) {
	if fn.Decl == nil || len(e.Args) >= len(fn.Decl.Params) {
		return
	}
	c.ensureDefaults(fn)
	missing := fn.Decl.Params[len(e.Args):]
	for _, p := range missing {
		if p.Default == nil || !isLiteral(p.Default) && !c.sharedDefaults[p.Default] {
			return
		}
	}
	for _, p := range missing {
		if isLiteral(p.Default) {
			e.Args = append(e.Args, copyLiteral(p.Default))
		} else {
			e.Args = append(e.Args, p.Default)
		}
	}
}

// isClosed reports whether x is a closed value: a literal, or a list,
// map, record, or variant value made of closed values.
func isClosed(x syntax.Expr) bool {
	switch x := x.(type) {
	case *syntax.Ident:
		return false
	case *syntax.Selector:
		// A variant without fields (Level.Info), or a qualified one.
		return isPath(x.X)
	case *syntax.RecordLit:
		for _, f := range x.Fields {
			if f.Value == nil || !isClosed(f.Value) {
				return false
			}
		}
		return true
	case *syntax.Unary:
		return x.Op == syntax.Minus && isClosed(x.X)
	case *syntax.ListLit:
		for _, el := range x.Elems {
			if !isClosed(el) {
				return false
			}
		}
		return true
	case *syntax.MapLit:
		for i := range x.Keys {
			if !isClosed(x.Keys[i]) || !isClosed(x.Values[i]) {
				return false
			}
		}
		return true
	}
	return isLiteral(x)
}

func isPath(x syntax.Expr) bool {
	switch x := x.(type) {
	case *syntax.Ident:
		return true
	case *syntax.Selector:
		return isPath(x.X)
	}
	return false
}

func hasTypeParam(t Type) bool {
	switch t := t.(type) {
	case *TypeParam:
		return true
	case *List:
		return hasTypeParam(t.Elem)
	case *Map:
		return hasTypeParam(t.Key) || hasTypeParam(t.Value)
	case *FuncType:
		for _, p := range t.Params {
			if hasTypeParam(p) {
				return true
			}
		}
		return hasTypeParam(t.Result)
	case *Union:
		for _, m := range t.Members {
			if hasTypeParam(m) {
				return true
			}
		}
	case *Record, *Sealed:
		for _, a := range TypeArgs(t) {
			if hasTypeParam(a) {
				return true
			}
		}
	}
	return false
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
