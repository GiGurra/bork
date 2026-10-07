package check

import (
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

type producerContext struct {
	elem  Type
	depth int
	// comprehension is set for a comprehension's generator; elem is nil
	// until its yield gives it.
	comprehension *comprehension
}

// comprehension is what the checker knows of a comprehension's parsed
// form: its generator loops and its one yield.
type comprehension struct {
	loops map[*syntax.For]bool
	yield *syntax.Yield
	// base is the number of scopes outside the comprehension; reported
	// holds the bindings already reported as not new.
	base     int
	reported map[diag.Pos]bool
}

func (c *checker) generate(e *syntax.Generate, want Type) Type {
	var elem Type
	var comp *comprehension
	if e.Comprehension {
		comp = c.comprehension(e)
		if seq, ok := want.(*Seq); ok && !mentionsOpen(seq.Elem) {
			elem = seq.Elem
		}
	} else {
		elem = c.resolveType(e.Elem)
		c.info.generateConstraints[e] = c.constraintsOf(e.Elem, elem, c.paramScope())
		if elem != Invalid && !isValue(elem) {
			c.errorf(e.Pos, "a sequence cannot yield %s", elem)
			elem = Invalid
		}
	}
	outer, loops, producer := c.used, c.loops, c.producer
	c.used, c.loops = 0, nil
	c.producer = &producerContext{elem: elem, depth: c.lambdaDepth, comprehension: comp}
	body := c.block(e.Body, Ok)
	effects := c.used
	if elem == nil {
		elem = c.producer.elem
		if elem == nil {
			elem = Invalid
		}
	}
	c.used, c.loops, c.producer = outer, loops, producer
	if body != Ok && body != Never && body != Invalid && !isOkTask(body) {
		c.errorf(e.Body.Pos, "a generator body must have type Ok, found %s", body)
	}
	if effects&EffOpen != 0 {
		c.errorf(e.Pos, "a generator cannot capture an open-effect callback; declare the callback's fixed effects")
	}
	return &Seq{Elem: elem, Effects: effects &^ EffOpen}
}

func (c *checker) yieldExpr(e *syntax.Yield) Type {
	c.inPostClause(e.Pos, "yield")
	if c.producer == nil || c.producer.depth != c.lambdaDepth {
		c.expr(e.Value)
		c.errorf(e.Pos, "yield requires a generator body and cannot cross a lambda boundary")
		return Invalid
	}
	if comp := c.producer.comprehension; comp != nil {
		if e != comp.yield {
			c.expr(e.Value)
			c.errorf(e.Pos, "a comprehension yields only its final value, after the closing '}'")
			return Invalid
		}
		if c.producer.elem == nil {
			errors := c.diags.Len()
			got := c.expr(e.Value)
			if got == Never && c.diags.Len() == errors {
				c.errorf(e.Value.Position(), "a comprehension's yield never produces a value")
			}
			if got == Never || got != Invalid && !isValue(got) {
				if got != Never {
					c.errorf(e.Value.Position(), "a comprehension cannot yield %s", got)
				}
				got = Invalid
			}
			c.producer.elem = got
			return Ok
		}
	}
	got := c.exprWant(e.Value, c.producer.elem)
	if !assignable(got, c.producer.elem) {
		what := "generator"
		if c.producer.comprehension != nil {
			what = "comprehension"
		}
		c.errorf(e.Pos, "%s yields %s, but this value is %s", what, c.producer.elem, got)
	}
	return Ok
}

// loopContext is a loop being checked: the lambda depth of its body,
// whether a break leaves it, and the names it carries.
type loopContext struct {
	depth  int
	broken bool
	carry  *carryLoop
	// comprehension is set for a comprehension's generator clause,
	// which break and continue cannot leave.
	comprehension bool
}

func (c *checker) forExpr(e *syntax.For) Type {
	if e.Items == nil {
		return c.loopExpr(e)
	}
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
	outer, names := c.carriable(e)
	c.nextTransparent = true
	c.pushScope()
	if e.Name == "" && e.Pattern != nil {
		c.info.loopPats[e] = c.bindingPattern(e.Pattern, elem, false)
	} else if e.Name != "_" {
		c.bind(e.Name, e.NamePos, elem, e)
	}
	loop := c.carry(e, outer, names)
	ctx := &loopContext{depth: c.lambdaDepth, carry: loop}
	if p := c.producer; p != nil && p.comprehension != nil && p.comprehension.loops[e] {
		ctx.comprehension = true
	}
	c.loops = append(c.loops, ctx)
	body := c.loopBodyCarrying(loop, e.Body)
	c.loops = c.loops[:len(c.loops)-1]
	loop.broken = ctx.broken
	c.popScope()
	c.endCarry(loop)
	if body != Ok && body != Never && body != Invalid && !isOkTask(body) {
		c.errorf(e.Body.Pos, "a loop body must have type Ok, found %s", body)
	}
	return Ok
}

// loopBody checks a loop's body, which may leave the loop even when the
// loop is in another loop's condition or post clause.
func (c *checker) loopBody(b *syntax.Block) Type {
	savedPost, savedCond := c.postClause, c.loopCond
	c.postClause, c.loopCond = 0, 0
	defer func() { c.postClause, c.loopCond = savedPost, savedCond }()
	return c.block(b, Ok)
}

// loopExpr checks `for { }`, `for (cond) { }`, and
// `for (init; cond; post) { }`. The header names are bound in a scope
// of the loop's own; each post binding gives one of them, or a name
// the loop carries from outside, its next value.
func (c *checker) loopExpr(e *syntax.For) Type {
	outer, names := c.carriable(e)
	c.nextTransparent = true
	c.pushScope()
	if c.headers == nil {
		c.headers = map[*syntax.Binding]bool{}
	}
	for _, b := range e.Init {
		c.headers[b] = true
	}
	for _, b := range e.Init {
		if c.stmt(b) == Never {
			c.errorf(b.Value.Position(), "a loop's header binding cannot leave the function")
		}
	}
	loop := c.carry(e, outer, names)
	if e.Cond != nil {
		saved := c.loopCond
		c.loopCond = c.lambdaDepth + 1
		if t := c.exprWant(e.Cond, Bool); t != Bool && t != Invalid {
			c.errorf(e.Cond.Position(), "a loop's condition must be Bool, found %s", t)
		}
		c.loopCond = saved
	}
	ctx := &loopContext{depth: c.lambdaDepth, carry: loop}
	c.loops = append(c.loops, ctx)
	body := c.loopBodyCarrying(loop, e.Body)
	c.loops = c.loops[:len(c.loops)-1]
	loop.broken = ctx.broken
	if body != Ok && body != Never && body != Invalid && !isOkTask(body) {
		c.errorf(e.Body.Pos, "a loop body must have type Ok, found %s", body)
	}
	c.checkPost(e, loop)
	c.popScope()
	c.endCarry(loop)
	if e.Cond == nil && !ctx.broken {
		return Never
	}
	return Ok
}

// checkPost checks a loop's post clause: each binding gives a name the
// loop carries its next value, computed from the values at the end of
// the iteration.
func (c *checker) checkPost(e *syntax.For, loop *carryLoop) {
	if len(e.Post) == 0 {
		return
	}
	savedPost := c.postClause
	c.postClause = c.lambdaDepth + 1
	c.loops = append(c.loops, &loopContext{depth: -1}) // break and continue cannot leave it either
	c.postScope(loop)
	defer func() {
		c.popScope()
		c.postClause, c.loops = savedPost, c.loops[:len(c.loops)-1]
	}()
	seen := map[string]bool{}
	for _, b := range e.Post {
		slot := loop.byName[b.Name]
		if slot == nil {
			c.expr(b.Value)
			c.errorf(b.Pos, "the post clause can only rebind the loop's header names and the names it carries, and %s is neither", b.Name)
			continue
		}
		if seen[b.Name] {
			c.errorf(b.Pos, "%s is rebound twice in the post clause", b.Name)
		}
		seen[b.Name] = true
		slot.post = b
		want := slot.typ
		t := c.exprWant(b.Value, want)
		if t, want := c.settle(t, want); t != Invalid && want != Invalid && !assignable(t, want) {
			c.errorf(b.Value.Position(), "%s is carried by the loop, so its next value must be %s, found %s", b.Name, want, t)
		}
		c.info.bindings[b] = want
	}
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
	arity := map[string]int{"map": 1, "filter": 1, "flatMap": 1, "take": 1, "drop": 1, "forEach": 1, "fold": 2, "toList": 0, "first": 0, "indexed": 0}
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
	case "indexed":
		result = &Seq{Elem: tupleType([]Type{Int, seq.Elem}), Effects: seq.Effects}
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
		if ft := callback(e.Args[0], []Type{seq.Elem}, Ok); ft != nil && ft.Result != Ok && ft.Result != Never {
			c.errorf(e.Args[0].Position(), "forEach callback must return Ok")
		}
		result = Ok
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

// comprehension collects a comprehension's generator loops and yield,
// and reports a binding line that rebinds one of the comprehension's
// earlier names: each name it binds is new. (A generator's names already
// cannot shadow; bindRebinding reports names bound outside.)
func (c *checker) comprehension(e *syntax.Generate) *comprehension {
	comp := &comprehension{loops: map[*syntax.For]bool{}, base: len(c.scopes), reported: map[diag.Pos]bool{}}
	seen := map[string]bool{}
	var names func(syntax.Pattern, func(string, diag.Pos))
	names = func(p syntax.Pattern, visit func(string, diag.Pos)) {
		switch p := p.(type) {
		case *syntax.VariantPat:
			if len(p.Path) == 1 && !p.Context && !p.Braces && c.typeNamed(p.Path[0]) == nil {
				visit(p.Path[0], p.Pos)
				return
			}
			for _, elem := range p.Elems {
				names(elem, visit)
			}
			for _, field := range p.Fields {
				if field.Pattern == nil {
					visit(field.Field, field.Pos)
				} else {
					names(field.Pattern, visit)
				}
			}
		case *syntax.TuplePat:
			for _, elem := range p.Elems {
				names(elem, visit)
			}
		case *syntax.TypePat:
			visit(p.Name, p.Pos)
		case *syntax.ListPat:
			for _, elem := range p.Elems {
				names(elem, visit)
			}
			if p.Rest != "" {
				visit(p.Rest, p.RestPos)
			}
		}
	}
	bound := func(name string, pos diag.Pos) {
		if name != "_" && seen[name] && !comp.reported[pos] {
			comp.reported[pos] = true
			c.errorf(pos, "a comprehension's names are new, and %s is already bound", name)
		}
		seen[name] = true
	}
	for body := e.Body; body != nil; {
		next := (*syntax.Block)(nil)
		for _, stmt := range body.Stmts {
			switch s := stmt.(type) {
			case *syntax.Binding:
				bound(s.Name, s.Pos)
			case *syntax.TupleBinding:
				names(s.Pattern, bound)
			case *syntax.ExprStmt:
				switch x := s.X.(type) {
				case *syntax.For:
					comp.loops[x] = true
					if x.Pattern != nil {
						names(x.Pattern, func(name string, _ diag.Pos) { seen[name] = true })
					} else {
						seen[x.Name] = true
					}
					next = x.Body
				case *syntax.If:
					next = x.Then
				case *syntax.Match:
					// A refutable generator: its first arm holds the rest.
					names(x.Arms[0].Pattern, func(name string, _ diag.Pos) { seen[name] = true })
					next, _ = x.Arms[0].Body.(*syntax.Block)
				case *syntax.Yield:
					comp.yield = x
				}
			}
		}
		body = next
	}
	return comp
}

// comprehensionRebinding reports a binding, directly in a comprehension
// being checked, of a name bound outside it or already reported, and
// whether it did. Its new value then has no type.
func (c *checker) comprehensionRebinding(name string, pos diag.Pos, node any, i int) bool {
	p := c.producer
	if p == nil || p.comprehension == nil || p.depth != c.lambdaDepth {
		return false
	}
	comp := p.comprehension
	if !comp.reported[pos] {
		if i >= comp.base {
			return false
		}
		comp.reported[pos] = true
		c.errorf(pos, "a comprehension's names are new, and %s is already bound", name)
	}
	c.scopes[len(c.scopes)-1][name] = &local{typ: Invalid, decl: node, used: true}
	return true
}
