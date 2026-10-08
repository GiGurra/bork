package gen

import (
	"go/ast"
	"maps"

	"github.com/GiGurra/bork/internal/check"
)

// listReuse proves that one carried slice has a single live owner inside a
// loop. A clone at entry isolates values retained outside it. Every transfer
// consumes the old name; branches are checked separately. Unrecognized
// expressions and escaping values leave the ordinary immutable calls intact.
type listReuse struct {
	known map[*check.Var]bool
	calls map[*check.Call]bool
	carry *check.Carry
	ok    bool
}

type listOwnership struct {
	live map[*check.Var]bool
	dead bool
}

func (g *gen) reuseList(e *check.For, c *check.Carry) bool {
	if _, ok := c.Head.Type.(*check.List); !ok || c.Post != nil {
		return false
	}
	a := &listReuse{known: map[*check.Var]bool{c.Head: true}, calls: map[*check.Call]bool{}, carry: c, ok: true}
	s := &listOwnership{live: map[*check.Var]bool{c.Head: true}}
	a.plain(e.Cond, s)
	a.expr(e.Body, s)
	for _, other := range e.Carries {
		a.plain(other.Post, s)
	}
	if !a.ok || len(a.calls) == 0 || (!s.dead && !s.live[c.Latch]) {
		return false
	}
	if g.listReuseCalls == nil {
		g.listReuseCalls = map[*check.Call]bool{}
	}
	maps.Copy(g.listReuseCalls, a.calls)
	return true
}

func (a *listReuse) take(v *check.Var, s *listOwnership) bool {
	if !a.known[v] {
		return false
	}
	if !s.live[v] {
		a.ok = false
	}
	delete(s.live, v)
	return true
}

func (a *listReuse) edges(edges []*check.CarryEdge, s *listOwnership) {
	for _, edge := range edges {
		if a.take(edge.From, s) {
			a.known[edge.To] = true
			s.live[edge.To] = true
		}
	}
}

func (a *listReuse) expr(x check.Expr, s *listOwnership) bool {
	if x == nil || s.dead || !a.ok {
		return false
	}
	switch x := x.(type) {
	case *check.VarRef:
		return a.take(x.Var, s)
	case *check.Call:
		if x.Func.Prelude && x.Func.Decl.IsMethod && len(x.Args) == 2 && (x.Func.Decl.Name == "append" || x.Func.Decl.Name == "concat") {
			ref, direct := x.Args[0].(*check.VarRef)
			borrow := direct && s.live[ref.Var]
			if a.expr(x.Args[0], s) {
				// The argument is evaluated before append writes. Size queries
				// may borrow a direct receiver until then, but cannot retain it.
				if borrow {
					s.live[ref.Var] = true
				}
				a.plain(x.Args[1], s)
				if borrow {
					delete(s.live, ref.Var)
				}
				for _, arg := range x.Captures {
					a.plain(arg, s)
				}
				for _, arg := range x.Needs {
					a.plain(arg, s)
				}
				if x.ArgOrder != nil {
					a.ok = false
				}
				a.calls[x] = true
				return true
			}
		}
		a.plain(x, s)
	case *check.Block:
		if len(x.Labels) > 0 || x.Assembly != nil {
			a.ok = false
			return false
		}
		for _, stmt := range x.Stmts {
			if s.dead {
				break
			}
			switch stmt := stmt.(type) {
			case *check.Let:
				if stmt.Initializer != nil {
					a.ok = false
					return false
				}
				if a.expr(stmt.Value, s) {
					a.known[stmt.Var] = true
					s.live[stmt.Var] = true
				}
			case *check.ExprStmt:
				if a.expr(stmt.X, s) {
					a.ok = false
				} // discarding an owned result is not a carry
			default:
				a.ok = false
			}
		}
		owned := a.expr(x.Tail, s)
		if !s.dead {
			a.edges(x.Carry, s)
		}
		return owned
	case *check.If:
		a.plain(x.Cond, s)
		return a.branches([]check.Expr{x.Then, x.Else}, x.Joins, s)
	case *check.Match:
		a.plain(x.X, s)
		if x.Assertion != nil {
			a.ok = false
			return false
		}
		var arms []check.Expr
		for _, arm := range x.Arms {
			for _, guard := range arm.Pat.Guards() {
				a.plain(guard, s)
			}
			arms = append(arms, arm.Body)
		}
		return a.branches(arms, x.Joins, s)
	case *check.LoopControl:
		a.edges(x.Carry, s)
		target := a.carry.After
		if x.Continue {
			target = a.carry.Latch
		}
		// Unchanged heads need no emitted carry edge: the latch already
		// holds that value. Header names have no After on a break.
		if !s.live[target] && !s.live[a.carry.Head] && (x.Continue || target != nil) {
			a.ok = false
		}
		s.dead = true
	case *check.Return:
		a.plain(x.Value, s)
		s.dead = true
	default:
		a.plain(x, s)
	}
	return false
}

func (a *listReuse) branches(arms []check.Expr, joins []*check.Join, s *listOwnership) bool {
	var merged *listOwnership
	owned := false
	for _, arm := range arms {
		branch := &listOwnership{live: maps.Clone(s.live)}
		result := a.expr(arm, branch)
		if branch.dead {
			continue
		}
		for _, join := range joins {
			if !branch.live[join.Var] && join.Prior != nil && a.take(join.Prior, branch) {
				a.known[join.Var] = true
				branch.live[join.Var] = true
			}
		}
		if merged == nil {
			merged = branch
			owned = result
			continue
		}
		if owned != result {
			a.ok = false
		}
		for v := range merged.live {
			if !branch.live[v] {
				delete(merged.live, v)
			}
		}
	}
	if merged == nil {
		s.dead = true
		return false
	}
	s.live = merged.live
	return owned
}

// plain accepts expressions that cannot retain or return the owned slice.
// The whitelist also keeps future IR nodes from silently bypassing the proof.
func (a *listReuse) plain(x check.Expr, s *listOwnership) {
	check.WalkComptime(x, func(x check.Expr) bool {
		switch x := x.(type) {
		case *check.VarRef:
			if a.known[x.Var] {
				a.ok = false
			}
		case *check.Call:
			if x.Func.Prelude && len(x.Args) == 1 && (x.Func.Decl.Name == "length" || x.Func.Decl.Name == "isEmpty") {
				if ref, ok := x.Args[0].(*check.VarRef); ok && a.known[ref.Var] {
					if !s.live[ref.Var] {
						a.ok = false
					}
					return false
				}
			}
		case *check.Const, *check.FloatBits, *check.Unary, *check.Binary, *check.Interp, *check.CallBuiltin, *check.CallValue, *check.Select, *check.ListLit, *check.MapLit, *check.VariantValue:
		default:
			a.ok = false
			return false
		}
		return true
	})
}

func (g *gen) reusedListCall(e *check.Call) ([]ast.Stmt, ast.Expr) {
	stmts, xs := g.values(e.Args)
	if xs == nil {
		return stmts, nil
	}
	params := e.Inst.Params
	for i := range xs {
		xs[i] = g.convert(xs[i], e.Args[i].Type(), params[i])
	}
	call := &ast.CallExpr{Fun: ast.NewIdent("append"), Args: xs}
	if e.Func.Decl.Name == "concat" {
		call.Ellipsis = 1
	}
	return stmts, call
}
