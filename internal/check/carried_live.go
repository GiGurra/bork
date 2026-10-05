package check

// Unused carried values (see docs/design/loops.md). A value of a name a
// loop carries is used if, on some path from it, it is observed: read
// other than to compute the next value of a carried name that is itself
// never observed. That is a fixpoint over the values: count = count + 1
// alone does not keep count alive.

// carryLiveness collects reads and what each carried value feeds.
type carryLiveness struct {
	// feeds maps a value to the values its computation reads.
	feeds    map[*Var][]*Var
	observed map[*Var]bool
	read     map[*Var]bool
	// sources are the bindings checked: carried rebindings, header
	// names, and the bindings before loops that carry them.
	sources []*Var
	seen    map[*Var]bool
}

func (cl *carryLiveness) source(v *Var) {
	if v != nil && !cl.seen[v] {
		cl.seen[v] = true
		cl.sources = append(cl.sources, v)
	}
}

// visit walks x, whose reads feed owner (nil: they observe).
func (cl *carryLiveness) visit(x Expr, owner *Var) {
	WalkComptime(x, func(y Expr) bool {
		switch y := y.(type) {
		case *VarRef:
			cl.read[y.Var] = true
			if owner == nil {
				cl.observed[y.Var] = true
			} else {
				cl.feeds[owner] = append(cl.feeds[owner], y.Var)
			}
		case *Block:
			cl.joins(y.Joins)
			for _, s := range y.Stmts {
				switch s := s.(type) {
				case *Let:
					cl.visit(s.AsyncScope, owner)
					if s.Carried {
						cl.source(s.Var)
						cl.visit(s.Value, s.Var)
					} else {
						cl.visit(s.Value, owner)
					}
				case *ExprStmt:
					cl.visit(s.X, owner)
				case *Trust:
					cl.visit(s.Call, owner)
				case *Mock:
					cl.visit(s.Func.Body, nil)
				}
			}
			cl.visit(y.Tail, owner)
			return false
		case *For:
			cl.visit(y.Items, owner)
			for _, c := range y.Carries {
				if c.Init != nil {
					cl.source(c.Head)
					cl.visit(c.Init, c.Head)
				} else {
					cl.feeds[c.Head] = append(cl.feeds[c.Head], c.Outer)
					if c.Outer.Kind == VarLet && !c.Outer.Let.Carried && c.Outer.PackageBinding == nil {
						cl.source(c.Outer)
					}
				}
				cl.feeds[c.Latch] = append(cl.feeds[c.Latch], c.Latch.Joins...)
				if c.After != nil {
					cl.feeds[c.After] = append(cl.feeds[c.After], c.After.Joins...)
				}
			}
			cl.visit(y.Cond, owner)
			cl.visit(y.Body, owner)
			for _, c := range y.Carries {
				if c.Post != nil {
					cl.visit(c.Post, c.Head)
				} else {
					cl.feeds[c.Head] = append(cl.feeds[c.Head], c.Latch)
				}
			}
			return false
		case *If:
			cl.joins(y.Joins)
		case *Match:
			cl.joins(y.Joins)
		}
		return true
	})
}

func (cl *carryLiveness) joins(js []*Join) {
	for _, j := range js {
		cl.feeds[j.Var] = append(cl.feeds[j.Var], j.Var.Joins...)
	}
}

// checkCarried reports the values of carried names that are never
// observed, in the typed trees roots.
func (c *checker) checkCarried(roots []Expr) {
	if len(c.info.loopCarries) == 0 {
		return
	}
	cl := &carryLiveness{feeds: map[*Var][]*Var{}, observed: map[*Var]bool{}, read: map[*Var]bool{}, seen: map[*Var]bool{}}
	for _, root := range roots {
		cl.visit(root, nil)
	}
	live := map[*Var]bool{}
	var work []*Var
	for v := range cl.observed {
		live[v] = true
		work = append(work, v)
	}
	for len(work) > 0 {
		v := work[len(work)-1]
		work = work[:len(work)-1]
		for _, w := range cl.feeds[v] {
			if !live[w] {
				live[w] = true
				work = append(work, w)
			}
		}
	}
	for _, v := range cl.sources {
		if live[v] || v.Name == "_" || v.Name == "" || v.Name[0] == '_' {
			continue
		}
		pos := v.Pos
		if v.Kind == VarLoop && (cl.read[v] || cl.readThrough(v)) {
			c.diags.AddCode(pos, "binding.unused", "%s is never observed: the loop only computes its next value; leave it out of the header (for { ... } when nothing else is left)", v.Name)
		} else if cl.read[v] || cl.readThrough(v) {
			c.diags.AddCode(pos, "binding.unused", "%s is never observed: it is only read to compute its own next value, and not after the loop", v.Name)
		} else {
			c.diags.AddCode(pos, "binding.unused", "binding %s is never read; discard explicitly with _", v.Name)
		}
	}
}

// readThrough reports whether a value is read through the carried
// values it flows into (joins and loops' heads), not only directly.
func (cl *carryLiveness) readThrough(v *Var) bool {
	seen := map[*Var]bool{v: true}
	work := []*Var{v}
	for len(work) > 0 {
		x := work[len(work)-1]
		work = work[:len(work)-1]
		for w, ins := range cl.feeds {
			if seen[w] || w.Kind != VarJoin && w.Kind != VarLoop {
				continue
			}
			for _, in := range ins {
				if in == x {
					if cl.read[w] {
						return true
					}
					seen[w] = true
					work = append(work, w)
					break
				}
			}
		}
	}
	return false
}
