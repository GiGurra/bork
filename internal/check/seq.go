package check

import "github.com/GiGurra/bork/internal/syntax"

type producerContext struct {
	elem  Type
	depth int
}

func (c *checker) generate(e *syntax.Generate) Type {
	elem := c.resolveType(e.Elem)
	c.info.generateConstraints[e] = c.constraintsOf(e.Elem, elem, c.paramScope())
	if elem != Invalid && !isValue(elem) {
		c.errorf(e.Pos, "a sequence cannot yield %s", elem)
		elem = Invalid
	}
	outer, loops, producer := c.used, c.loops, c.producer
	c.used, c.loops = 0, nil
	c.producer = &producerContext{elem: elem, depth: c.lambdaDepth}
	body := c.block(e.Body, Unit)
	effects := c.used
	c.used, c.loops, c.producer = outer, loops, producer
	if body != Unit && body != Never && body != Invalid {
		c.errorf(e.Body.Pos, "a generator body must have type Unit, found %s", body)
	}
	if effects&EffOpen != 0 {
		c.errorf(e.Pos, "a generator cannot capture an open-effect callback; declare the callback's fixed effects")
	}
	return &Seq{Elem: elem, Effects: effects &^ EffOpen}
}

func (c *checker) yieldExpr(e *syntax.Yield) Type {
	if c.producer == nil || c.producer.depth != c.lambdaDepth {
		c.expr(e.Value)
		c.errorf(e.Pos, "yield requires a generator body and cannot cross a lambda boundary")
		return Invalid
	}
	got := c.exprWant(e.Value, c.producer.elem)
	if !assignable(got, c.producer.elem) {
		c.errorf(e.Pos, "generator yields %s, but this value is %s", c.producer.elem, got)
	}
	return Unit
}

func (c *checker) forExpr(e *syntax.For) Type {
	source := c.expr(e.Items)
	elem := Type(Invalid)
	switch t := source.(type) {
	case *List:
		elem = t.Elem
	case *Seq:
		elem = t.Elem
		c.used |= t.Effects
	default:
		if source != Invalid {
			c.errorf(e.Items.Position(), "for requires a List or Seq, found %s", source)
		}
	}
	c.pushScope()
	c.bind(e.Name, e.NamePos, elem, e)
	c.scopes[len(c.scopes)-1][e.Name].node = nil
	c.loops = append(c.loops, c.lambdaDepth)
	body := c.block(e.Body, Unit)
	c.loops = c.loops[:len(c.loops)-1]
	c.popScope()
	if body != Unit && body != Never && body != Invalid {
		c.errorf(e.Body.Pos, "a loop body must have type Unit, found %s", body)
	}
	return Unit
}

type seqCallInfo struct {
	op      string
	args    []syntax.Expr
	effects Effects
}

func (c *checker) seqStatic(e *syntax.Call) (Type, bool) {
	sel, ok := e.Fun.(*syntax.Selector)
	if !ok {
		return nil, false
	}
	id, ok := sel.X.(*syntax.Ident)
	if !ok || id.Name != "Seq" {
		return nil, false
	}
	switch sel.Name {
	case "empty":
		if len(e.TypeArgs) != 1 || len(e.Args) != 0 {
			c.errorf(e.Pos, "Seq.empty needs one element type and no arguments")
			return Invalid, true
		}
		elem := c.resolveType(e.TypeArgs[0])
		if elem != Invalid && !isValue(elem) {
			c.errorf(e.Pos, "a sequence cannot hold %s", elem)
			return Invalid, true
		}
		c.info.seqCalls[e] = &seqCallInfo{op: "empty"}
		return &Seq{Elem: elem}, true
	case "unfold":
		elem, state := &TypeParam{Name: "T"}, &TypeParam{Name: "S"}
		step := instantiate(c.preludePkg.TypeNamed("SeqStep"), []Type{elem, state})
		option := instantiate(c.preludePkg.TypeNamed("Option"), []Type{step})
		fn := &Func{Decl: &syntax.FuncDecl{Pos: e.Pos, Name: "unfold", Params: []*syntax.Param{{Pos: e.Pos, Name: "seed"}, {Pos: e.Pos, Name: "step"}}}, Pkg: c.preludePkg, TypeParams: []*TypeParam{elem, state}, Params: []Type{state, &FuncType{Params: []Type{state}, Result: option}}, Result: &Seq{Elem: elem}, ParamIn: []int{-1, -1}}
		result := c.callFunc(e, "Seq.unfold", fn, e.Args, nil, e.TypeArgs, nil)
		if c.fn != nil {
			calls := c.fn.Calls[:0]
			for _, called := range c.fn.Calls {
				if called != fn {
					calls = append(calls, called)
				}
			}
			c.fn.Calls = calls
		}
		c.info.seqCalls[e] = &seqCallInfo{op: "unfold", args: c.info.args(e)}
		return result, true
	case "range":
		if len(e.Args) != 2 || len(e.TypeArgs) != 0 {
			c.errorf(e.Pos, "Seq.range needs start and end Int arguments")
			return Invalid, true
		}
		for _, arg := range e.Args {
			if t := c.exprWant(arg, Int); !assignable(t, Int) {
				c.errorf(arg.Position(), "range needs Int, found %s", t)
			}
		}
		c.info.seqCalls[e] = &seqCallInfo{op: "range", args: e.Args}
		return &Seq{Elem: Int}, true
	default:
		if fn, _ := c.methodNamed(&Seq{Elem: listElem}, sel.Name); fn != nil {
			return nil, false
		}
		c.errorf(sel.Pos, "Seq has no constructor %s", sel.Name)
		return Invalid, true
	}
}

func (c *checker) seqMethod(e *syntax.Call, sel *syntax.Selector, receiver Type) (Type, bool) {
	if list, ok := receiver.(*List); ok && sel.Name == "toSeq" {
		if len(e.Args) != 0 || len(e.TypeArgs) != 0 {
			c.errorf(e.Pos, "toSeq takes no arguments")
			return Invalid, true
		}
		c.info.seqCalls[e] = &seqCallInfo{op: "fromList", args: []syntax.Expr{sel.X}}
		return &Seq{Elem: list.Elem}, true
	}
	seq, ok := receiver.(*Seq)
	if !ok {
		return nil, false
	}
	op := sel.Name
	arity := map[string]int{"map": 1, "filter": 1, "flatMap": 1, "take": 1, "drop": 1, "forEach": 1, "fold": 2, "toList": 0, "first": 0}
	n, ok := arity[op]
	if !ok {
		return nil, false
	}
	if len(e.Args) != n {
		c.errorf(e.Pos, "Seq.%s needs %d arguments", op, n)
		return Invalid, true
	}
	if len(e.TypeArgs) > 0 {
		c.errorf(e.Pos, "Seq.%s infers its types; omit explicit type arguments", op)
	}
	for _, arg := range e.Arguments {
		if arg.Name != "" {
			c.errorf(arg.Pos, "Seq.%s accepts positional arguments", op)
		}
	}
	result := Type(seq)
	effects := seq.Effects
	callback := func(arg syntax.Expr, params []Type, want Type) *FuncType {
		ft, ok := c.exprWant(arg, &FuncType{Params: params, Result: want, Effects: EffOpen}).(*FuncType)
		if !ok {
			c.errorf(arg.Position(), "Seq.%s needs a callback", op)
			return nil
		}
		if len(ft.Params) != len(params) {
			c.errorf(arg.Position(), "Seq.%s callback needs %d parameters", op, len(params))
			return nil
		}
		for i, p := range params {
			if !identical(ft.Params[i], p) {
				c.errorf(arg.Position(), "Seq.%s callback parameter must be %s", op, p)
			}
		}
		if ft.Effects&EffOpen != 0 {
			c.errorf(arg.Position(), "Seq.%s requires a callback with fixed effects", op)
		}
		effects |= ft.Effects &^ EffOpen
		return ft
	}
	switch op {
	case "take", "drop":
		if t := c.exprWant(e.Args[0], Int); !assignable(t, Int) {
			c.errorf(e.Args[0].Position(), "Seq.%s count must be Int", op)
		}
	case "map", "flatMap":
		if ft := callback(e.Args[0], []Type{seq.Elem}, nil); ft != nil {
			elem := ft.Result
			if op == "flatMap" {
				if inner, ok := elem.(*Seq); ok {
					elem = inner.Elem
					effects |= inner.Effects
				} else {
					c.errorf(e.Args[0].Position(), "flatMap callback must return Seq, found %s", elem)
					elem = Invalid
				}
			}
			if elem != Invalid && !isValue(elem) {
				c.errorf(e.Args[0].Position(), "Seq.%s cannot yield %s", op, elem)
				elem = Invalid
			}
			result = &Seq{Elem: elem, Effects: effects}
		} else {
			result = Invalid
		}
	case "filter":
		if ft := callback(e.Args[0], []Type{seq.Elem}, Bool); ft != nil && !assignable(ft.Result, Bool) {
			c.errorf(e.Args[0].Position(), "filter callback must return Bool")
		}
		result = &Seq{Elem: seq.Elem, Effects: effects}
	case "forEach":
		if ft := callback(e.Args[0], []Type{seq.Elem}, Unit); ft != nil && ft.Result != Unit && ft.Result != Never {
			c.errorf(e.Args[0].Position(), "forEach callback must return Unit")
		}
		result = Unit
	case "fold":
		seed := c.expr(e.Args[0])
		if ft := callback(e.Args[1], []Type{seed, seq.Elem}, seed); ft != nil && !assignable(ft.Result, seed) {
			c.errorf(e.Args[1].Position(), "fold callback must return %s", seed)
		}
		result = seed
	case "toList":
		result = &List{Elem: seq.Elem}
	case "first":
		result = instantiate(c.preludePkg.TypeNamed("Option"), []Type{seq.Elem})
	}
	terminal := op == "forEach" || op == "fold" || op == "toList" || op == "first"
	charged := Effects(0)
	if terminal {
		charged = effects
		c.used |= effects
	}
	c.info.seqCalls[e] = &seqCallInfo{op: op, args: append([]syntax.Expr{sel.X}, e.Args...), effects: charged}
	return result, true
}
