package check

import (
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Carried rebinding (see docs/design/loops.md). A loop body may give a
// new value to a name that the block containing the loop may rebind, at
// its top level or in the blocks of if, match and block statements in
// it. Such a name is carried: each iteration starts with the value the
// previous one ended with, and after the loop the name has the value it
// had when the loop ended.
//
// The checker gives each carried name a value of its own at the head of
// each iteration (head), at the end of an iteration, which the post
// clause reads (latch), and after the loop (after); and where the
// branches of a statement meet, it joins the values they end with
// (join). Lowering turns these into variables (see Carry and Join).

// transparentKey marks a scope of a loop body, or of a statement branch
// in it, in which a carried name may be rebound. It is never a name.
const transparentKey = "\x00carry"

// carryNode declares a carried name's value that no binding gives: at
// the head of an iteration, at its end, after the loop, or after a
// statement whose branches gave it different values.
type carryNode struct {
	name string
	pos  diag.Pos
	kind string // "head", "latch", "after" or "join"
	// origin is the name's first binding, which editors show as its
	// definition.
	origin any
}

// carryOrigin is the first binding of the name whose value decl is.
func carryOrigin(decl any) any {
	if n, ok := decl.(*carryNode); ok {
		return n.origin
	}
	return decl
}

// carryLoop is a loop's carried names: its header names, then the names
// it carries from outside.
type carryLoop struct {
	pos diag.Pos
	// base is the index of the loop's own scope.
	base   int
	slots  []*carrySlot
	byName map[string]*carrySlot
}

type carrySlot struct {
	name string
	typ  Type
	// outer is the value before the loop; nil for a header name, whose
	// header binding gives its first value and is its head.
	outer  *local
	header *syntax.Binding
	head   any
	latch  *carryNode
	after  *carryNode // nil for a header name
	post   *syntax.Binding
	// end is the value at the end of the body; nil when the body never
	// gets there.
	end any
}

// carryFrame is a loop body, or a branch of a statement in one, being
// checked: the latest values it gave the names its loop carries.
type carryFrame struct {
	loop *carryLoop
	cur  map[string]*local
}

// joinCarry is a carried name joined after an if, match or block
// statement: its value before the statement, and the value each branch
// ends with (nil for a branch that never ends).
type joinCarry struct {
	slot  *carrySlot
	node  *carryNode
	prior any
	in    []any
}

// joining is a statement whose branches may give carried names new
// values.
type joining struct {
	loop     *carryLoop
	branches map[int]*carryFrame
	reaches  map[int]bool
}

func (c *checker) transparent(i int) bool {
	if i < 0 {
		return false
	}
	_, ok := c.scopes[i][transparentKey]
	return ok
}

// compound reports whether x is an if, a match or a block, whose
// branches may rebind carried names when x is a statement in a loop.
func compound(x syntax.Expr) bool {
	switch x.(type) {
	case *syntax.If, *syntax.Match, *syntax.Block:
		return true
	}
	return false
}

// lookupAt finds the local a name refers to, and the index of its
// scope, without using it.
func (c *checker) lookupAt(name string) (*local, int) {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if l, ok := c.scopes[i][name]; ok {
			return l, i
		}
	}
	return nil, -1
}

// carriedAt reports whether l, found in scope i, is the value of a name
// a loop carries, and whether the code being checked may give it a new
// value: every scope from i inwards is in that loop's body, outside
// lambdas, scope and with blocks, initializers, and values.
func (c *checker) carriedAt(l *local, i int) bool {
	if l == nil || l.carry == nil {
		return false
	}
	for j := i + 1; j < len(c.scopes); j++ {
		if !c.transparent(j) {
			return false
		}
	}
	return true
}

// carriedHere is the slot of a carried name that a binding here would
// give a new value, or nil.
func (c *checker) carriedHere(name string) *carrySlot {
	if c.noCarry {
		return nil
	}
	if l, i := c.lookupAt(name); c.carriedAt(l, i) {
		return l.carry.byName[name]
	}
	return nil
}

// rebindCarried checks b, which binds the name of l (found in scope i),
// a value a loop carries.
func (c *checker) rebindCarried(b *syntax.Binding, l *local, i int, t Type) {
	slot := l.carry.byName[b.Name]
	scope := c.scopes[len(c.scopes)-1]
	if !c.carriedAt(l, i) {
		c.errorf(b.Pos, "%s is carried by the loop at line %d, which can give it a new value only in its body and post clause, and in the blocks of if, match and block statements there; not in a lambda, a scope or with block, an initializer, or a branch whose value is used (move the binding out, or make the if or match a statement)", b.Name, l.carry.pos.Line)
		scope[b.Name] = &local{typ: Invalid, decl: b, used: true}
		return
	}
	switch {
	case b.Lazy || b.AsyncScope != nil:
		c.errorf(b.Pos, "%s is carried by the loop at line %d, so its new value cannot be lazy or async; bind the value under another name, then rebind %s to it", b.Name, l.carry.pos.Line, b.Name)
	case b.Type != nil:
		if t != Invalid && slot.typ != Invalid && !identical(t, slot.typ) {
			c.errorf(b.Pos, "%s is carried by the loop at line %d, so it keeps its type %s; it cannot be declared %s", b.Name, l.carry.pos.Line, slot.typ, t)
		}
	case t != Invalid && slot.typ != Invalid && !assignable(t, slot.typ):
		c.errorf(b.Value.Position(), "%s is carried to the next iteration of the loop at line %d, so its new value must be %s (its type before the loop), found %s; declare its first binding with a type that holds both", b.Name, l.carry.pos.Line, slot.typ, t)
	}
	c.info.bindings[b] = slot.typ
	c.info.rebindings[b] = l.decl
	c.info.carriedBindings[b] = slot
	c.setCarried(b.Name, &local{typ: slot.typ, node: b, decl: b, carry: l.carry})
}

// misplacedRebinding reports a binding of name, bound in scope i
// outside a loop being checked, where the loop cannot carry it.
func (c *checker) misplacedRebinding(name string, pos diag.Pos, i int) bool {
	for _, loop := range c.loops {
		if loop.carry != nil && loop.carry.base > i {
			placed := true
			for j := loop.carry.base; j < len(c.scopes); j++ {
				placed = placed && c.transparent(j)
			}
			if placed {
				c.errorf(pos, "the loop at line %d cannot carry %s: a loop carries only names that the block containing it may rebind (bound in that block, or carried by an enclosing loop)", loop.carry.pos.Line, name)
				return true
			}
			c.errorf(pos, "%s is bound before the loop at line %d, which can give it new values only in its body and post clause, and in the blocks of if, match and block statements there; not in a lambda, a scope or with block, an initializer, or a branch whose value is used (move the binding out, or make the if or match a statement)", name, loop.carry.pos.Line)
			return true
		}
	}
	return false
}

// setCarried makes l a carried name's value for the rest of the
// innermost scope, and the latest value of the body or branch being
// checked.
func (c *checker) setCarried(name string, l *local) {
	c.scopes[len(c.scopes)-1][name] = l
	if n := len(c.carryFrames); n > 0 {
		if f := c.carryFrames[n-1]; f.loop.byName[name] != nil {
			f.cur[name] = l
		}
	}
}

// rebound lists the names given values in the statements of b that a
// loop could carry: at its top level, in if, match and block
// statements, and in nested loops' bodies and post clauses.
func rebound(b *syntax.Block, names *[]string, seen map[string]bool) {
	add := func(name string) {
		if name != "_" && !seen[name] {
			seen[name] = true
			*names = append(*names, name)
		}
	}
	var expr func(x syntax.Expr)
	expr = func(x syntax.Expr) {
		switch x := x.(type) {
		case *syntax.If:
			rebound(x.Then, names, seen)
			if x.Else != nil {
				expr(x.Else)
			}
		case *syntax.Match:
			for _, arm := range x.Arms {
				expr(arm.Body)
			}
		case *syntax.Block:
			rebound(x, names, seen)
		case *syntax.For:
			rebound(x.Body, names, seen)
			for _, p := range x.Post {
				add(p.Name)
			}
		}
	}
	for _, s := range b.Stmts {
		switch s := s.(type) {
		case *syntax.Binding:
			add(s.Name)
		case *syntax.ExprStmt:
			expr(s.X)
		}
	}
	if b.Tail != nil {
		expr(b.Tail)
	}
}

// carry starts checking loop e's carried names, in the loop's own scope
// (just pushed), after its header names are bound: those, then the
// names from outside its body and post clause give new values (outer,
// found before the loop's scope was pushed).
func (c *checker) carry(e *syntax.For, outer []*local, outerNames []string) *carryLoop {
	loop := &carryLoop{pos: e.Pos, base: len(c.scopes) - 1, byName: map[string]*carrySlot{}}
	scope := c.scopes[len(c.scopes)-1]
	add := func(slot *carrySlot) {
		origin := any(slot.header)
		if slot.outer != nil {
			origin = carryOrigin(slot.outer.decl)
		}
		slot.latch = &carryNode{name: slot.name, pos: e.Pos, kind: "latch", origin: origin}
		if n, ok := slot.head.(*carryNode); ok {
			n.origin = origin
		}
		if slot.after != nil {
			slot.after.origin = origin
		}
		loop.slots = append(loop.slots, slot)
		loop.byName[slot.name] = slot
	}
	for _, b := range e.Init {
		if l := scope[b.Name]; l != nil && b.Name != "_" {
			l.carry = loop
			add(&carrySlot{name: b.Name, typ: l.typ, header: b, head: l.decl})
		}
	}
	for i, l := range outer {
		name := outerNames[i]
		if b, ok := l.node.(*syntax.Binding); ok && (b.Lazy || b.AsyncScope != nil) {
			c.errorf(e.Pos, "the loop gives %s new values, but %s is %s; bind its value under another name before the loop, and rebind that", name, name, map[bool]string{true: "lazy", false: "async"}[b.Lazy])
		}
		if l.typ == OwnedScope {
			c.errorf(e.Pos, "the loop gives owned scope %s new values, but an owned scope cannot be carried from one iteration to the next", name)
		}
		// Whether the value before the loop is read is decided over the
		// whole loop (see CheckCarried).
		l.used = true
		head := &carryNode{name: name, pos: e.Pos, kind: "head"}
		scope[name] = &local{typ: l.typ, decl: head, used: true, carry: loop}
		add(&carrySlot{name: name, typ: l.typ, outer: l, head: head, after: &carryNode{name: name, pos: e.Pos, kind: "after"}})
	}
	c.info.loopCarries[e] = loop
	return loop
}

// carriable finds the names that loop e, about to be checked in the
// innermost scope, carries from outside: those its body and post
// clause give new values that the innermost block may rebind.
func (c *checker) carriable(e *syntax.For) ([]*local, []string) {
	var names []string
	rebound(e.Body, &names, map[string]bool{})
	for _, p := range e.Post {
		names = append(names, p.Name)
	}
	header := map[string]bool{}
	for _, b := range e.Init {
		header[b.Name] = true
	}
	var locals []*local
	var out []string
	seen := map[string]bool{}
	top := len(c.scopes) - 1
	for _, name := range names {
		if header[name] || seen[name] {
			continue
		}
		seen[name] = true
		if l, i := c.lookupAt(name); l != nil && (i == top || c.carriedAt(l, i)) && l.typ != Invalid {
			locals = append(locals, l)
			out = append(out, name)
		}
	}
	return locals, out
}

// loopBodyCarrying checks loop's body, recording each name's value at
// its end.
func (c *checker) loopBodyCarrying(loop *carryLoop, b *syntax.Block) Type {
	frame := &carryFrame{loop: loop, cur: map[string]*local{}}
	c.carryFrames = append(c.carryFrames, frame)
	c.nextTransparent = true
	t := c.loopBody(b)
	c.nextTransparent = false
	c.carryFrames = c.carryFrames[:len(c.carryFrames)-1]
	if t != Never {
		for _, slot := range loop.slots {
			slot.end = slot.head
			if l := frame.cur[slot.name]; l != nil {
				slot.end = l.decl
			}
		}
	}
	return t
}

// postScope pushes the scope a post clause sees: each carried name's
// value at the end of the iteration.
func (c *checker) postScope(loop *carryLoop) {
	c.pushScope()
	for _, slot := range loop.slots {
		c.scopes[len(c.scopes)-1][slot.name] = &local{typ: slot.typ, decl: slot.latch, used: true}
	}
}

// endCarry gives the names a loop carried from outside their values
// after it, in the scope that contains the loop (now innermost).
func (c *checker) endCarry(loop *carryLoop) {
	for _, slot := range loop.slots {
		if slot.outer != nil {
			c.setCarried(slot.name, &local{typ: slot.typ, decl: slot.after, used: true, carry: slot.outer.carry})
		}
	}
}

// loopEdge records the values a break or continue carries out of the
// iteration of loop.
func (c *checker) loopEdge(e *syntax.LoopControl, loop *loopContext) {
	if loop.carry == nil {
		return
	}
	values := make([]any, len(loop.carry.slots))
	for i, slot := range loop.carry.slots {
		if l, _ := c.lookupAt(slot.name); l != nil {
			values[i] = l.decl
		}
	}
	c.info.loopEdges[e] = values
}

// startJoin begins an if, match or block about to be checked. In a loop
// body, as a statement, its branches may give carried names new values,
// joined after it; startJoin then returns how. It must come before
// anything else in the statement is checked.
func (c *checker) startJoin() *joining {
	stmt := c.stmtPos
	c.stmtPos = false
	if !stmt || len(c.carryFrames) == 0 {
		return nil
	}
	return &joining{loop: c.carryFrames[len(c.carryFrames)-1].loop, branches: map[int]*carryFrame{}, reaches: map[int]bool{}}
}

// joinBranch checks branch i of a joining statement with check.
func (c *checker) joinBranch(j *joining, i int, check func() Type) Type {
	if j == nil {
		return check()
	}
	frame := &carryFrame{loop: j.loop, cur: map[string]*local{}}
	c.carryFrames = append(c.carryFrames, frame)
	t := check()
	c.carryFrames = c.carryFrames[:len(c.carryFrames)-1]
	j.branches[i] = frame
	j.reaches[i] = t != Never
	return t
}

// branchBlock checks a block that is a branch of a statement (or of an
// expression, when j is nil).
func (c *checker) branchBlock(j *joining, b *syntax.Block, want Type) Type {
	c.nextTransparent = j != nil
	t := c.block(b, want)
	c.nextTransparent = false
	return t
}

// branchExpr checks a branch of a statement: in a joining one, its
// blocks may rebind carried names too.
func (c *checker) branchExpr(j *joining, x syntax.Expr, want Type) Type {
	if j == nil {
		return c.exprWant(x, want)
	}
	if b, ok := x.(*syntax.Block); ok {
		return c.branchBlock(j, b, want)
	}
	// An if or match as the branch (else if) joins in a scope of its own.
	c.nextTransparent = true
	c.pushScope()
	defer c.popScope()
	c.stmtPos = compound(x)
	t := c.exprWant(x, want)
	c.stmtPos = false
	return t
}

// endJoin joins, after statement x of n branches, the carried names its
// branches gave new values.
func (c *checker) endJoin(j *joining, x syntax.Expr, n int) {
	if j == nil {
		return
	}
	for _, slot := range j.loop.slots {
		prior, _ := c.lookupAt(slot.name)
		if prior == nil {
			continue
		}
		in := make([]any, n)
		changed := false
		for i := 0; i < n; i++ {
			if !j.reaches[i] {
				continue
			}
			in[i] = prior.decl
			if f := j.branches[i]; f != nil {
				if l := f.cur[slot.name]; l != nil {
					in[i] = l.decl
					changed = true
				}
			}
		}
		if !changed {
			continue
		}
		node := &carryNode{name: slot.name, pos: x.Position(), kind: "join", origin: carryOrigin(prior.decl)}
		c.info.joins[x] = append(c.info.joins[x], &joinCarry{slot: slot, node: node, prior: prior.decl, in: in})
		c.setCarried(slot.name, &local{typ: slot.typ, decl: node, used: true, carry: j.loop})
	}
}
