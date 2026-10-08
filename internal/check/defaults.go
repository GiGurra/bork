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
// from the call, so `xs: List[T] = []` works for every T). Fieldless
// variants also specialize per use; other closed values reuse the
// default itself, checked once where it was declared, so its names
// mean what they mean there. The call's syntax is left as written; its
// arguments with the defaults are in Info.callArgs, and the typed tree.

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
	c.typeParams, c.lambdaDepth, c.mapperLambdas, c.have = nil, 0, 0, nil
	c.session = nil // the defaults' calls are inferred on their own
	c.typeParams = map[string]*TypeParam{}
	for _, tp := range fn.TypeParams {
		c.typeParams[tp.Name] = tp
	}
	defer func() {
		shared, solved, mapKeys := c.sharedDefaults, c.solved, c.mapKeyChecks
		*c = saved
		c.sharedDefaults, c.solved, c.mapKeyChecks = shared, solved, mapKeys
	}()
	for i, p := range params {
		if p.Default == nil {
			if fn.Decl.Constructor == nil && i > 0 && params[i-1].Default != nil {
				c.errorf(p.Pos, "parameter %s needs a default value too: parameters after one with a default must have one", p.Name)
			}
			continue
		}
		x := p.Default
		switch {
		case isLiteral(x):
			x = copyLiteral(x)
		case !isClosed(x, c.closedConstructorCandidate):
			c.errorf(x.Position(), "a parameter's default must be a closed value: a literal, or a record or variant of them (such as Level.Info)")
			continue
		case hasTypeParam(fn.Params[i]) && !c.emptyVariantDefault(x, fn.Params[i]):
			c.errorf(x.Position(), "the default of %s can only be a literal or a fieldless variant, since its type depends on a type parameter", p.Name)
			continue
		default:
			if c.sharedDefaults == nil {
				c.sharedDefaults = map[syntax.Expr]bool{}
			}
			c.sharedDefaults[x] = true
		}
		if t := c.exprWant(x, fn.Params[i]); t != Invalid {
			if !c.checkedClosedValue(x) {
				c.errorf(x.Position(), "a parameter's default must be a closed value: a literal, or a record or variant of them (such as Level.Info)")
			} else if !assignable(t, fn.Params[i]) {
				c.errorf(p.Default.Position(), "the default of %s must be %s, found %s", p.Name, fn.Params[i], t)
			}
		}
	}
}

// withDefaults is a call's arguments followed by the defaults of the
// parameters it leaves out (or the arguments alone, if one of those has
// no usable default).
func (c *checker) withDefaults(args []syntax.Expr, fn *Func) []syntax.Expr {
	if fn.Decl == nil || len(args) >= len(fn.Decl.Params) {
		return args
	}
	c.ensureDefaults(fn)
	missing := fn.Decl.Params[len(args):]
	for _, p := range missing {
		if p.Default == nil || !isLiteral(p.Default) && !c.sharedDefaults[p.Default] {
			return args
		}
	}
	out := append([]syntax.Expr(nil), args...)
	for _, p := range missing {
		out = append(out, c.copyDefault(p.Default))
	}
	return out
}

// isClosed reports whether x is a closed value: a literal, or a list,
// map, record, or variant value made of closed values.
func isClosed(x syntax.Expr, callAllowed ...func(*syntax.Call) bool) bool {
	switch x := x.(type) {
	case *syntax.ContextName:
		return x.Name != ""
	case *syntax.Ident:
		return x.Name == "Ok"
	case *syntax.Selector:
		// A variant without fields (Level.Info), or a qualified one.
		return isPath(x.X)
	case *syntax.RecordLit:
		for _, f := range x.Fields {
			if f.Value == nil || !isClosed(f.Value, callAllowed...) {
				return false
			}
		}
		return true
	case *syntax.Call:
		// Before checking, calls are candidates for positional construction.
		// Afterwards, only calls resolved to actual variants are closed values.
		if len(callAllowed) > 0 && !callAllowed[0](x) {
			return false
		}
		for _, arg := range x.Args {
			if !isClosed(arg, callAllowed...) {
				return false
			}
		}
		return true
	case *syntax.Unary:
		return (x.Op == syntax.Minus || x.Op == syntax.Caret) && isClosed(x.X, callAllowed...)
	case *syntax.TupleLit:
		for _, elem := range x.Elems {
			if !isClosed(elem, callAllowed...) {
				return false
			}
		}
		return true
	case *syntax.ListLit:
		for _, el := range x.Elems {
			if !isClosed(el, callAllowed...) {
				return false
			}
		}
		return true
	case *syntax.MapLit:
		for i := range x.Keys {
			if !isClosed(x.Keys[i], callAllowed...) || !isClosed(x.Values[i], callAllowed...) {
				return false
			}
		}
		return true
	}
	return isLiteral(x)
}

// A closed call must name a positional variant rather than a function or method.
func (c *checker) closedConstructorCandidate(call *syntax.Call) bool {
	var owner Type
	switch head := call.Fun.(type) {
	case *syntax.ContextName:
		return head.Name != ""
	case *syntax.Selector:
		switch head := head.X.(type) {
		case *syntax.Ident:
			owner = c.typeNamed(head.Name)
		case *syntax.TypeHead:
			owner = c.resolveType(head.Type)
		}
		if sealed, ok := owner.(*Sealed); ok {
			variant := sealed.Variant(head.Name)
			return variant != nil && variant.Positional
		}
	}
	return false
}

func (c *checker) checkedClosedValue(x syntax.Expr) bool {
	return isClosed(x, func(call *syntax.Call) bool { return c.info.variantCalls[call] != nil })
}

func isPath(x syntax.Expr) bool {
	switch x := x.(type) {
	case *syntax.Ident, *syntax.TypeHead:
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
		return (x.Op == syntax.Minus || x.Op == syntax.Caret) && isLiteral(x.X)
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

// Fields follow the same closed-value rules as parameter defaults. Each
// specialization checks its own literals so generic defaults retain their type.
func (c *checker) ensureFieldDefault(field *Field) {
	if field.Decl == nil || field.Decl.Default == nil || field.defaultState == 2 {
		return
	}
	if field.defaultState == 1 {
		c.errorf(field.Decl.Default.Position(), "field default %s refers recursively to itself", field.Name)
		return
	}
	field.defaultState = 1
	defer func() { field.defaultState = 2 }()
	saved := *c
	c.pkg, c.fn, c.inPrelude = field.Pkg, nil, field.Prelude
	c.scopes = []map[string]*local{{}}
	c.typeParams, c.lambdaDepth, c.mapperLambdas, c.have = nil, 0, 0, nil
	c.session = nil
	defer func() {
		shared, solved, mapKeys := c.sharedDefaults, c.solved, c.mapKeyChecks
		*c = saved
		c.sharedDefaults, c.solved, c.mapKeyChecks = shared, solved, mapKeys
	}()
	c.typeParams = field.defaultTypes
	x := field.Decl.Default
	if field.defaultBase != nil {
		c.ensureFieldDefault(field.defaultBase)
		x = c.cloneComputedSyntax(x, field.defaultBound)
	}
	if field.Lazy && (!isClosed(x, c.closedConstructorCandidate) || closedDefaultUsesSibling(x, field.siblings)) {
		c.computedFieldDefault(field)
		return
	}
	switch {
	case isLiteral(x):
		x = copyLiteral(x)
	case !isClosed(x, c.closedConstructorCandidate):
		if field.Lazy {
			c.errorf(x.Position(), "computed lazy field defaults are not implemented yet; independent lazy fields require closed defaults")
		} else {
			c.errorf(x.Position(), "a field's default must be a closed value: a literal, or a record or variant of them")
		}
		return
	case hasTypeParam(field.Type) && !c.emptyVariantDefault(x, field.Type):
		c.errorf(x.Position(), "the default of %s can only be a literal or a fieldless variant, since its type depends on a type parameter", field.Name)
		return
	default:
		if c.sharedDefaults == nil {
			c.sharedDefaults = map[syntax.Expr]bool{}
		}
		c.sharedDefaults[x] = true
	}
	x = c.copyDefault(x)
	if t := c.fieldInitializer(x, field); t != Invalid {
		if !c.checkedClosedValue(x) {
			c.errorf(x.Position(), "a field's default must be a closed value: a literal, or a record or variant of them")
		} else if !assignable(c.zonk(t), c.zonk(field.Type)) {
			c.errorf(field.Decl.Default.Position(), "the default of %s must be %s, found %s", field.Name, field.Type, t)
		}
	}
	c.info.fieldDefaults[field] = x
}

func (c *checker) ensureAllFieldDefaults() {
	for _, t := range c.info.TypeOrder {
		switch t := t.(type) {
		case *Record:
			for _, f := range t.Fields {
				c.ensureFieldDefault(f)
			}
			c.checkComputedCycles(t.Fields)
			for _, inst := range t.insts.byKey {
				for _, f := range inst.(*Record).Fields {
					if f.defaultGeneric {
						f.defaultUse = c.info.typeUses[inst]
					}
					c.ensureFieldDefault(f)
				}
				c.checkComputedCycles(inst.(*Record).Fields)
			}
		case *Sealed:
			for _, v := range t.Variants {
				for _, f := range v.Fields {
					c.ensureFieldDefault(f)
				}
				c.checkComputedCycles(v.Fields)
			}
			for _, inst := range t.insts.byKey {
				for _, v := range inst.(*Sealed).Variants {
					for _, f := range v.Fields {
						if f.defaultGeneric {
							f.defaultUse = c.info.typeUses[inst]
						}
						c.ensureFieldDefault(f)
					}
					c.checkComputedCycles(v.Fields)
				}
			}
		}
	}
}

// Check constructors in the declaration's scope before permitting specialization.
func (c *checker) emptyVariantDefault(x syntax.Expr, want Type) bool {
	switch x.(type) {
	case *syntax.Selector, *syntax.ContextName, *syntax.RecordLit:
	default:
		return false
	}
	if c.exprWant(x, want) == Invalid {
		return false
	}
	return c.defaultVariant(x) != nil
}
func (c *checker) defaultVariant(x syntax.Expr) *Variant {
	var v *Variant
	switch x := x.(type) {
	case *syntax.Selector:
		v = c.info.selectorVariants[x]
	case *syntax.ContextName:
		v = c.info.contextVariants[x]
	case *syntax.RecordLit:
		v, _ = c.info.recordTargets[x].(*Variant)
	}
	if v != nil && len(v.Fields) == 0 {
		return v
	}
	return nil
}

// Fieldless defaults need separate typing nodes; retain the declaration-bound name.
func (c *checker) copyDefault(x syntax.Expr) syntax.Expr {
	if isLiteral(x) {
		return copyLiteral(x)
	}
	v := c.defaultVariant(x)
	if v == nil {
		return x
	}
	var cp syntax.Expr
	switch x := x.(type) {
	case *syntax.Selector:
		clone := *x
		cp = &clone
		c.info.selectorVariants[&clone] = v
	case *syntax.ContextName:
		clone := *x
		cp = &clone
		c.info.contextVariants[&clone] = v
	case *syntax.RecordLit:
		clone := *x
		cp = &clone
		c.info.recordTargets[&clone] = v
		c.info.recordInits[&clone] = c.info.recordInits[x]
	}
	c.info.types[cp] = c.info.types[x]
	c.info.constructorConstraints[cp] = c.info.constructorConstraints[x]
	c.sharedDefaults[cp] = true
	return cp
}
func (c *checker) specializeDefault(x syntax.Expr, bound map[*TypeParam]Type) Type {
	t := subst(c.info.types[x], bound)
	if v := c.defaultVariant(x); v != nil {
		c.info.types[x] = t
		c.info.constructorConstraints[x] = substConstraints(c.info.constructorConstraints[x], bound)
		if sealed, ok := t.(*Sealed); ok {
			v = sealed.Variant(v.Name)
			switch x := x.(type) {
			case *syntax.Selector:
				c.info.selectorVariants[x] = v
			case *syntax.ContextName:
				c.info.contextVariants[x] = v
			case *syntax.RecordLit:
				c.info.recordTargets[x] = v
			}
		}
	}
	return t
}
