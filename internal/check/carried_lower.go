package check

import (
	"fmt"

	"github.com/GiGurra/bork/internal/syntax"
)

// Lowering of carried rebinding (see carried.go): each value of a
// carried name that no binding gives becomes a variable of its own,
// and the paths that pass values on (the end of a branch or a loop
// body, a break or a continue) carry edges to the joins they reach.

// carryVar is the variable of carried value n.
func (l *lowerer) carryVar(n *carryNode, t Type) *Var {
	if v := l.vars[n]; v != nil {
		return v
	}
	kind := VarJoin
	if n.kind == "head" {
		kind = VarLoop
	}
	v := &Var{Name: n.name, GoName: fmt.Sprintf("_%s_%d_%d_%s", n.kind, n.pos.Line, n.pos.Col, n.name), Pos: n.pos, Type: t, Kind: kind}
	l.vars[n] = v
	return v
}

// invariantOf is what every value of the name v is a value of declares
// about it, when a loop carries it.
func (l *lowerer) invariantOf(v *Var) []*Constraint {
	if inv, ok := l.invariants[v]; ok {
		return inv
	}
	switch v.Kind {
	case VarLoop:
		return v.Invariant
	case VarLet:
		if v.Let != nil && v.Let.Declared {
			return v.Let.Constraints
		}
	}
	return nil
}

func (l *lowerer) setInvariant(v *Var, inv []*Constraint) {
	if l.invariants == nil {
		l.invariants = map[*Var][]*Constraint{}
	}
	l.invariants[v] = inv
	if v.Kind == VarJoin {
		// It has them when every path's value does (see facts.go).
		v.Invariant = inv
	}
}

// addJoin adds from to the values the VarJoin v may have.
func addJoin(v, from *Var) {
	for _, in := range v.Joins {
		if in == from {
			return
		}
	}
	v.Joins = append(v.Joins, from)
}

// loop lowers the condition, carried names and body of loop x into out.
func (l *lowerer) loop(x *syntax.For, out *For) *For {
	loop := l.info.loopCarries[x]
	var slots []*carrySlot
	if loop != nil {
		slots = loop.slots
	}
	for _, slot := range slots {
		c := &Carry{}
		if b := slot.header; b != nil {
			c.Init = l.expr(b.Value)
			c.Head = &Var{Name: b.Name, Pos: b.Pos, Type: l.info.bindings[b], Kind: VarLoop, Unused: l.info.unused[b], Invariant: l.info.bindingConstraints[b]}
			l.vars[b] = c.Head
		} else {
			c.Outer = l.vars[slot.outer.decl]
			c.Head = l.carryVar(slot.head.(*carryNode), slot.typ)
			c.Head.Invariant = l.invariantOf(c.Outer)
			c.After = l.carryVar(slot.after, slot.typ)
			l.setInvariant(c.After, c.Head.Invariant)
		}
		c.Latch = l.carryVar(slot.latch, slot.typ)
		l.setInvariant(c.Latch, c.Head.Invariant)
		out.Carries = append(out.Carries, c)
	}
	if x.Cond != nil {
		out.Cond = l.expr(x.Cond)
	}
	l.loops = append(l.loops, out.Carries)
	out.Body = l.block(x.Body)
	l.loops = l.loops[:len(l.loops)-1]
	for i, slot := range slots {
		c := out.Carries[i]
		if slot.end != nil {
			from := l.vars[slot.end]
			addJoin(c.Latch, from)
			if from != c.Head {
				out.Body.Carry = append(out.Body.Carry, &CarryEdge{To: c.Latch, From: from})
			}
		}
		if slot.post != nil {
			c.Post = l.expr(slot.post.Value)
		}
		if c.After != nil && (out.Cond != nil || out.Items != nil) {
			// The loop may end between iterations.
			addJoin(c.After, c.Head)
		}
	}
	return out
}

// loopControl lowers a break or continue: the values it passes on.
func (l *lowerer) loopControl(x *syntax.LoopControl, at expr) *LoopControl {
	out := &LoopControl{expr: at, Continue: x.Continue}
	values := l.info.loopEdges[x]
	if values == nil || len(l.loops) == 0 {
		return out
	}
	carries := l.loops[len(l.loops)-1]
	for i, decl := range values {
		c := carries[i]
		from := l.vars[decl]
		to := c.After
		if x.Continue {
			to = c.Latch
		}
		if to == nil || from == nil {
			continue
		}
		addJoin(to, from)
		if from != c.Head {
			out.Carry = append(out.Carry, &CarryEdge{To: to, From: from})
		}
	}
	return out
}

// joins lowers the joins after statement x, whose branch i is the
// block branch(i) gives.
func (l *lowerer) joins(x syntax.Expr, branch func(int) *Block) []*Join {
	var out []*Join
	for _, jc := range l.info.joins[x] {
		v := l.carryVar(jc.node, jc.slot.typ)
		prior := l.vars[jc.prior]
		l.setInvariant(v, l.invariantOf(prior))
		j := &Join{Var: v}
		for i, decl := range jc.in {
			if decl == nil {
				continue
			}
			from := l.vars[decl]
			addJoin(v, from)
			if from == prior {
				j.Prior = prior
				continue
			}
			b := branch(i)
			b.Carry = append(b.Carry, &CarryEdge{To: v, From: from})
		}
		out = append(out, j)
	}
	return out
}

// branchBlock is x as a block, to which a branch's edges can be added.
func branchBlock(x Expr) *Block {
	if b, ok := x.(*Block); ok {
		return b
	}
	return &Block{expr: expr{pos: x.Pos(), typ: x.Type()}, Tail: x}
}
