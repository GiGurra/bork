package check

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"

	"github.com/GiGurra/bork/internal/syntax"
)

// selectExpr checks a select by lowering it to a block of ordinary code
// that calls the prelude's compilerSelect functions:
//
//	select {
//	  n = numbers.receive(s) => f(n)
//	  results.send(s, x) => g()
//	  _ => h()
//	}
//
// becomes
//
//	{
//	  __select1_ch0 = numbers; __select1_s0 = s
//	  __select1_ch1 = results; __select1_s1 = s; __select1_x1 = x
//	  __select1_outcome = compilerSelect([compilerSelectReceive(__select1_ch0, __select1_s0),
//	    compilerSelectSend(__select1_ch1, __select1_s1, __select1_x1)], false)
//	  match (compilerSelectIndex(__select1_outcome)) {
//	    0 => { n = compilerSelectReceived(__select1_outcome, __select1_ch0); f(n) }
//	    1 => g()
//	    2 => h()
//	    _ => compilerSelectCancelled(__select1_outcome)
//	  }
//	}
//
// so the operands are evaluated once, in source order, and the arms'
// bodies are code of the enclosing function: return, ? and break mean
// what they mean around the select.
func (c *checker) selectExpr(e *syntax.Select, want Type) Type {
	type op struct {
		arm  *syntax.SelectArm
		call *syntax.Call
		send bool
	}
	var ops []op
	var fallback *syntax.SelectArm
	ok := true
	for _, arm := range e.Arms {
		if arm.Op == nil {
			if fallback != nil {
				c.errorf(arm.Pos, "a select has at most one _ arm")
				ok = false
			}
			fallback = arm
			continue
		}
		call, isCall := arm.Op.(*syntax.Call)
		var sel *syntax.Selector
		if isCall {
			sel, _ = call.Fun.(*syntax.Selector)
		}
		switch {
		case sel != nil && sel.Name == "receive" && len(call.Args) == 1 && len(call.TypeArgs) == 0:
			ops = append(ops, op{arm: arm, call: call})
		case sel != nil && sel.Name == "send" && len(call.Args) == 2 && len(call.TypeArgs) == 0:
			ops = append(ops, op{arm: arm, call: call, send: true})
		default:
			c.errorf(arm.Op.Position(), "a select arm is a channel operation, ch.receive(s) or ch.send(s, x), optionally named (n = ch.receive(s)), or _ for when none is ready")
			ok = false
		}
		if isCall {
			for _, a := range call.Arguments {
				if a.Name != "" {
					c.errorf(a.Pos, "a select arm's operation takes its arguments by position")
					ok = false
				}
			}
		}
	}
	if len(ops) == 0 && ok {
		c.errorf(e.Pos, "a select needs at least one channel operation arm: n = ch.receive(s) => ... or ch.send(s, x) => ...")
		ok = false
	}
	if !ok {
		return Invalid
	}
	c.selectSerial++
	pos := e.Pos
	// Leading underscores are reserved, so these names cannot shadow user code.
	prefix := fmt.Sprintf("__select%d_", c.selectSerial)
	id := func(name string, at syntax.Expr) *syntax.Ident {
		p := pos
		if at != nil {
			p = at.Position()
		}
		return &syntax.Ident{Pos: p, Name: prefix + name}
	}
	if c.assemblyFuncs == nil {
		c.assemblyFuncs = map[string]*Func{}
	}
	helper := func(name string, at syntax.Expr, args ...syntax.Expr) *syntax.Call {
		ref := prefix + name
		c.assemblyFuncs[ref] = c.preludePkg.Funcs[name]
		p := pos
		if at != nil {
			p = at.Position()
		}
		return &syntax.Call{Start: p, Pos: p, End: p, Fun: &syntax.Ident{Pos: p, Name: ref}, Args: args}
	}
	block := &syntax.Block{Pos: pos, End: e.Close}
	bind := func(name string, value syntax.Expr) {
		b := &syntax.Binding{Pos: value.Position(), Name: prefix + name, Value: value}
		c.info.assemblyNames[b] = writtenText(value)
		block.Stmts = append(block.Stmts, b)
	}
	arms := &syntax.ListLit{Pos: pos}
	for i, o := range ops {
		receiver := o.call.Fun.(*syntax.Selector).X
		n := strconv.Itoa(i)
		bind("ch"+n, receiver)
		bind("s"+n, o.call.Args[0])
		if o.send {
			bind("x"+n, helper("compilerSelectValue", o.call.Args[1], id("ch"+n, receiver), o.call.Args[1]))
			arms.Elems = append(arms.Elems, helper("compilerSelectSend", o.arm.Op, id("ch"+n, receiver), id("s"+n, o.call.Args[0]), id("x"+n, o.call.Args[1])))
		} else {
			arms.Elems = append(arms.Elems, helper("compilerSelectReceive", o.arm.Op, id("ch"+n, receiver), id("s"+n, o.call.Args[0])))
		}
	}
	bind("outcome", helper("compilerSelect", nil, arms, &syntax.BoolLit{Pos: pos, Value: fallback == nil}))
	outcome := func() *syntax.Ident { return id("outcome", nil) }
	m := &syntax.Match{Pos: pos, Close: e.Close, X: helper("compilerSelectIndex", nil, outcome())}
	index := func(i int) syntax.Pattern {
		return &syntax.LitPat{Pos: pos, Value: &syntax.IntLit{Pos: pos, Text: strconv.Itoa(i)}}
	}
	for i, o := range ops {
		body := o.arm.Body
		if o.arm.Name != "" {
			var result syntax.Expr
			if o.send {
				result = helper("compilerSelectSent", o.arm.Op, outcome())
			} else {
				result = helper("compilerSelectReceived", o.arm.Op, outcome(), id("ch"+strconv.Itoa(i), nil))
			}
			binding := &syntax.Binding{Pos: o.arm.NamePos, Name: o.arm.Name, Value: result}
			body = &syntax.Block{Pos: o.arm.Pos, End: o.arm.Body.Position(), Stmts: []syntax.Stmt{binding}, Tail: o.arm.Body}
		}
		m.Arms = append(m.Arms, &syntax.Arm{Pattern: index(i), Body: body})
	}
	if fallback != nil {
		m.Arms = append(m.Arms, &syntax.Arm{Pattern: index(len(ops)), Body: fallback.Body})
	}
	m.Arms = append(m.Arms, &syntax.Arm{Pattern: &syntax.WildcardPat{Pos: pos}, Body: helper("compilerSelectCancelled", nil, outcome())})
	block.Tail = m
	c.info.selectMatches[m] = true
	c.info.selects[e] = block
	mark := c.diags.Len()
	t := c.exprWant(block, want)
	c.diags.Rewrite(mark, selectDiagnostic)
	return t
}

// selectDiagnostic says what a problem with a select's lowered code means
// in terms of what was written, and drops those that only follow from
// another.
func selectDiagnostic(d *diag.Diagnostic) bool {
	if !strings.Contains(d.Msg, "compilerSelect") {
		return true
	}
	if m := selectArgument.FindStringSubmatch(d.Msg); m != nil {
		index, fn, expected, found := m[1], m[2], m[3], m[4]
		switch {
		case fn == "compilerSelectReceived" || fn == "compilerSelectSent":
			return false
		case fn == "compilerSelectValue" && index == "1":
			// compilerSelectSend reports the receiver too.
			return false
		case fn == "compilerSelectValue":
			d.Msg = "the value sent must be " + expected + ", found " + found
		case index == "1":
			d.Msg = "a select arm's operation must be a channel's receive or send, but this is " + found
		case index == "2":
			d.Msg = "the scope a select arm waits in must be a Scope, found " + found
		default:
			d.Msg = "the value sent must be " + expected + ", found " + found
		}
		return true
	}
	d.Msg = strings.NewReplacer("compilerSelectReceive", "receive", "compilerSelectSend", "send").Replace(d.Msg)
	return true
}

var selectArgument = regexp.MustCompile(`^argument (\d+) to (\w+) must be (.+), found (.+)$`)
