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
//	  jobs.handOver(s, conn) => k()
//	  _ => h()
//	}
//
// becomes
//
//	{
//	  __select1_ch0 = numbers; __select1_s0 = s
//	  __select1_ch1 = results; __select1_s1 = s; __select1_x1 = x
//	  __select1_ch2 = jobs; __select1_s2 = s; __select1_x2 = conn
//	  __select1_outcome = compilerSelect([compilerSelectReceive(__select1_ch0, __select1_s0),
//	    compilerSelectSend(__select1_ch1, __select1_s1, __select1_x1),
//	    compilerSelectHandOver(__select1_ch2, __select1_s2, __select1_x2)], false)
//	  match (compilerSelectIndex(__select1_outcome)) {
//	    0 => { n = compilerSelectReceived(__select1_outcome, __select1_ch0); f(n) }
//	    1 => g()
//	    2 => { _ = compilerSelectHandedOver(__select1_outcome, __select1_ch2, __select1_x2); k() }
//	    3 => h()
//	    _ => compilerSelectCancelled(__select1_outcome)
//	  }
//	}
//
// so the operands are evaluated once, in source order, and the arms'
// bodies are code of the enclosing function: return, ? and break mean
// what they mean around the select. A receive from a Handoff calls the
// Handoff helpers instead (chosen once its channel is checked). A
// hand-over arm moves conn only if it is the arm completed: the lifetimes
// see it moved where that arm's body starts (compilerSelectHandedOver),
// and owned still in the other arms.
func (c *checker) selectExpr(e *syntax.Select, want Type) Type {
	type op struct {
		arm      *syntax.SelectArm
		call     *syntax.Call
		send     bool
		handOver bool
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
		case sel != nil && sel.Name == "handOver" && len(call.Args) == 2 && len(call.TypeArgs) == 0:
			ops = append(ops, op{arm: arm, call: call, handOver: true})
		default:
			c.errorf(arm.Op.Position(), "a select arm is a channel operation, ch.receive(s) or ch.send(s, x), or a handoff's h.receive(s) or h.handOver(s, r), optionally named (n = ch.receive(s)), or _ for when none is ready")
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
		c.errorf(e.Pos, "a select needs at least one channel operation arm: n = ch.receive(s) => ..., ch.send(s, x) => ... or h.handOver(s, r) => ...")
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
	call := func(ref string, at syntax.Expr, args []syntax.Expr) *syntax.Call {
		p := pos
		if at != nil {
			p = at.Position()
		}
		return &syntax.Call{Start: p, Pos: p, End: p, Fun: &syntax.Ident{Pos: p, Name: ref}, Args: args}
	}
	helper := func(name string, at syntax.Expr, args ...syntax.Expr) *syntax.Call {
		ref := prefix + name
		c.assemblyFuncs[ref] = c.preludePkg.Funcs[name]
		return call(ref, at, args)
	}
	if c.assemblyResolvers == nil {
		c.assemblyResolvers = map[string]func() *Func{}
	}
	// receiveHelper calls arm n's receive helper: the Handoff one when the
	// arm's channel, checked by then, is a Handoff.
	receiveHelper := func(n, name, handoffName string, at syntax.Expr, args ...syntax.Expr) *syntax.Call {
		ref, ch := prefix+name+n, prefix+"ch"+n
		c.assemblyResolvers[ref] = func() *Func {
			if l := c.lookup(ch); l != nil && handoffType(l.typ) {
				return c.preludePkg.Funcs[handoffName]
			}
			return c.preludePkg.Funcs[name]
		}
		return call(ref, at, args)
	}
	block := &syntax.Block{Pos: pos, End: e.Close}
	bind := func(name string, value syntax.Expr, written syntax.Expr) {
		b := &syntax.Binding{Pos: value.Position(), Name: prefix + name, Value: value}
		c.info.assemblyNames[b] = writtenText(written)
		block.Stmts = append(block.Stmts, b)
	}
	arms := &syntax.ListLit{Pos: pos}
	for i, o := range ops {
		receiver := o.call.Fun.(*syntax.Selector).X
		n := strconv.Itoa(i)
		bind("ch"+n, receiver, receiver)
		bind("s"+n, o.call.Args[0], o.call.Args[0])
		switch {
		case o.send:
			bind("x"+n, helper("compilerSelectValue", o.call.Args[1], id("ch"+n, receiver), o.call.Args[1]), o.call.Args[1])
			arms.Elems = append(arms.Elems, helper("compilerSelectSend", o.arm.Op, id("ch"+n, receiver), id("s"+n, o.call.Args[0]), id("x"+n, o.call.Args[1])))
		case o.handOver:
			// The resource itself, so that its origin is known; its type
			// is checked against the handoff's by compilerSelectHandOver.
			bind("x"+n, o.call.Args[1], o.call.Args[1])
			arms.Elems = append(arms.Elems, helper("compilerSelectHandOver", o.arm.Op, id("ch"+n, receiver), id("s"+n, o.call.Args[0]), id("x"+n, o.call.Args[1])))
		default:
			arms.Elems = append(arms.Elems, receiveHelper(n, "compilerSelectReceive", "compilerSelectHandoffReceive", o.arm.Op, id("ch"+n, receiver), id("s"+n, o.call.Args[0])))
		}
	}
	selected := helper("compilerSelect", nil, arms, &syntax.BoolLit{Pos: pos, Value: fallback == nil})
	bind("outcome", selected, selected)
	outcome := func() *syntax.Ident { return id("outcome", nil) }
	m := &syntax.Match{Pos: pos, Close: e.Close, X: helper("compilerSelectIndex", nil, outcome())}
	index := func(i int) syntax.Pattern {
		return &syntax.LitPat{Pos: pos, Value: &syntax.IntLit{Pos: pos, Text: strconv.Itoa(i)}}
	}
	for i, o := range ops {
		body := o.arm.Body
		n := strconv.Itoa(i)
		if o.arm.Name != "" || o.handOver {
			var result syntax.Expr
			switch {
			case o.send:
				result = helper("compilerSelectSent", o.arm.Op, outcome())
			case o.handOver:
				// The resource is handed over where the chosen arm starts,
				// whether or not the arm names the outcome.
				result = helper("compilerSelectHandedOver", o.arm.Op, outcome(), id("ch"+n, nil), id("x"+n, nil))
			default:
				result = receiveHelper(n, "compilerSelectReceived", "compilerSelectHandoffReceived", o.arm.Op, outcome(), id("ch"+n, nil))
			}
			name, namePos := o.arm.Name, o.arm.NamePos
			if name == "" {
				name, namePos = "_", o.arm.Op.Position()
			}
			binding := &syntax.Binding{Pos: namePos, Name: name, Value: result}
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
		case fn == "compilerSelectReceived" || fn == "compilerSelectSent" || fn == "compilerSelectHandoffReceived" || fn == "compilerSelectHandedOver":
			return false
		case fn == "compilerSelectValue" && index == "1":
			// compilerSelectSend reports the receiver too.
			return false
		case fn == "compilerSelectValue":
			d.Msg = "the value sent must be " + expected + ", found " + found
		case fn == "compilerSelectHandOver" && index == "3":
			d.Msg = "the resource handed over must be " + expected + ", found " + found
		case fn == "compilerSelectSend" && index == "1" && handoffText(found):
			d.Msg = "a select arm sends only on a Channel; to a Handoff, hand the resource over (h.handOver(s, r)): this is " + found
		case fn == "compilerSelectHandOver" && index == "1":
			d.Msg = "a select arm hands over only to a Handoff; on a Channel, send (ch.send(s, x)): this is " + found
		case index == "1":
			d.Msg = "a select arm's operation must be on a Channel or a Handoff, but this is " + found
		case index == "2":
			d.Msg = "the scope a select arm waits in must be a Scope, found " + found
		default:
			d.Msg = "the value sent must be " + expected + ", found " + found
		}
		return true
	}
	d.Msg = strings.NewReplacer("compilerSelectHandoffReceive", "receive", "compilerSelectHandOver", "handOver", "compilerSelectReceive", "receive", "compilerSelectSend", "send").Replace(d.Msg)
	return true
}

// handoffType reports whether t is a Handoff.
func handoffType(t Type) bool {
	r, ok := genericBaseOrSelf(t).(*Record)
	return ok && r.Prelude && r.Name == "Handoff"
}

// handoffText reports whether the type text of a message is a Handoff's.
func handoffText(text string) bool {
	return strings.HasPrefix(text, "Handoff[")
}

var selectArgument = regexp.MustCompile(`^argument (\d+) to (\w+) must be (.+), found (.+)$`)
