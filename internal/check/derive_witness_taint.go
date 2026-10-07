package check

import "reflect"

// Taint marks the values of a definition witness that may differ between
// expansions: holes, staged selections, values whose type depends on the
// target, and everything computed from them. A diagnostic about a tainted
// value, or one whose proof could use a fact about one, belongs to the
// expansion; only untainted failures are reported for the definition.
type deriveWitnessTaint struct {
	vars  map[*Var]bool
	exprs map[Expr]bool
}

func newDeriveWitnessTaint(fn *Func) *deriveWitnessTaint {
	t := &deriveWitnessTaint{vars: map[*Var]bool{}}
	for _, v := range fn.ParamVars {
		if witnessDependentType(v.Type) {
			t.vars[v] = true
		}
	}
	// Variables are tainted by their sources, which can follow their uses
	// across loop iterations and joins: repeat until nothing changes.
	for changed := true; changed; {
		before := len(t.vars)
		t.exprs = map[Expr]bool{}
		t.markVars(fn.Body)
		changed = len(t.vars) != before
	}
	t.exprs = map[Expr]bool{}
	return t
}

func witnessDependentType(typ Type) bool {
	if typ == nil {
		return false
	}
	if typ == Invalid || hasTypeParam(typ) {
		return true
	}
	switch typ := typ.(type) {
	case *Seq:
		return witnessDependentType(typ.Elem)
	case *List:
		return witnessDependentType(typ.Elem)
	case *Map:
		return witnessDependentType(typ.Key) || witnessDependentType(typ.Value)
	case *FuncType:
		for _, p := range typ.Params {
			if witnessDependentType(p) {
				return true
			}
		}
		return witnessDependentType(typ.Result)
	case *Union:
		for _, m := range typ.Members {
			if witnessDependentType(m) {
				return true
			}
		}
	case *Record, *Sealed:
		for _, a := range TypeArgs(typ) {
			if witnessDependentType(a) {
				return true
			}
		}
	}
	return false
}

// witnessCall reports whether x calls a hole or another witness. A helper's
// witness body has holes too, so what its result is known to be may differ
// between expansions.
func witnessCall(x Expr) (*Func, bool) {
	call, ok := x.(*Call)
	if !ok || call.Func == nil || !call.Func.Witness {
		return nil, false
	}
	return call.Func, true
}

// witnessStaged reports whether x is the opaque condition of a staged branch.
func witnessStaged(x Expr) bool {
	fn, ok := witnessCall(x)
	return ok && fn.WitnessStage
}

func (t *deriveWitnessTaint) markVars(root Expr) {
	taintAll := func(vars ...*Var) {
		for _, v := range vars {
			if v != nil {
				t.vars[v] = true
			}
		}
	}
	visit := func(node any) {
		switch node := node.(type) {
		case *Let:
			if node.Var != nil && (t.tainted(node.Value) || witnessDependentType(node.Var.Type)) {
				t.vars[node.Var] = true
			}
		case *If:
			if t.tainted(node.Cond) {
				for _, join := range node.Joins {
					taintAll(join.Var)
				}
			}
			for _, join := range node.Joins {
				if t.vars[join.Prior] {
					taintAll(join.Var)
				}
			}
		case *Match:
			if t.tainted(node.X) {
				for _, join := range node.Joins {
					taintAll(join.Var)
				}
				for _, arm := range node.Arms {
					taintAll(patternVars(arm.Pat)...)
				}
			}
			for _, join := range node.Joins {
				if t.vars[join.Prior] {
					taintAll(join.Var)
				}
			}
		case *For:
			if t.tainted(node.Items) || t.tainted(node.Cond) {
				taintAll(node.Var)
				for _, carry := range node.Carries {
					taintAll(carry.Head, carry.Latch, carry.After)
				}
			}
		case *Block:
			for _, edge := range node.Carry {
				if t.vars[edge.From] {
					taintAll(edge.To)
				}
			}
			for _, join := range node.Joins {
				if t.vars[join.Prior] {
					taintAll(join.Var)
				}
			}
		case *LoopControl:
			for _, edge := range node.Carry {
				if t.vars[edge.From] {
					taintAll(edge.To)
				}
			}
		case *Var:
			if witnessDependentType(node.Type) {
				t.vars[node] = true
			}
		}
	}
	walkWitnessTree(reflect.ValueOf(root), visit)
}

func patternVars(p *Pat) []*Var {
	if p == nil {
		return nil
	}
	out := []*Var{p.Var}
	for _, field := range p.Fields {
		out = append(out, patternVars(field.Pat)...)
	}
	for _, elem := range p.Elems {
		out = append(out, patternVars(elem)...)
	}
	return append(append(out, patternVars(p.Sub)...), patternVars(p.Rest)...)
}

// tainted reports whether x, or anything it is computed from, may differ
// between expansions.
func (t *deriveWitnessTaint) tainted(x Expr) bool {
	if x == nil || reflect.ValueOf(x).IsNil() {
		return false
	}
	if known, ok := t.exprs[x]; ok {
		return known
	}
	t.exprs[x] = false // A cyclic reference adds nothing.
	result := witnessDependentType(x.Type())
	if _, call := witnessCall(x); call {
		result = true
	}
	if ref, ok := x.(*VarRef); ok && t.vars[ref.Var] {
		result = true
	}
	if !result {
		walkWitnessChildren(reflect.ValueOf(x), func(node any) bool {
			switch node := node.(type) {
			case Expr:
				if t.tainted(node) {
					result = true
				}
				return false
			case *Var:
				if t.vars[node] {
					result = true
				}
			}
			return !result
		})
	}
	t.exprs[x] = result
	return result
}

func (t *deriveWitnessTaint) argument(a argVal) bool {
	return a.expr != nil && t.tainted(a.expr) || witnessDependentType(a.typ)
}

func (t *deriveWitnessTaint) obligation(ob obligation) bool {
	for _, a := range ob.args {
		if t.argument(a) {
			return true
		}
	}
	for _, alternative := range ob.or {
		if t.obligation(alternative) {
			return true
		}
	}
	return false
}

// stagedExit reports whether x contains an early exit from a staged
// selection, or code replaced by a hole that might have left early: an
// expansion that keeps it may add the exit's facts to what follows.
func (t *deriveWitnessTaint) stagedExit(x any) bool {
	found := false
	var inStage func(any, bool)
	inStage = func(node any, staged bool) {
		if found {
			return
		}
		switch node := node.(type) {
		case *If:
			if witnessStaged(node.Cond) {
				staged = true
			}
		case *Return, *Try, *LoopControl:
			if staged {
				found = true
				return
			}
		case *Call:
			if fn, ok := witnessCall(node); ok && fn.WitnessRepair {
				found = true
				return
			}
		case *Lambda:
			return // Its exits leave only the lambda.
		}
		walkWitnessChildren(reflect.ValueOf(node), func(child any) bool {
			if _, ok := child.(*Var); ok {
				return true
			}
			inStage(child, staged)
			return false
		})
	}
	inStage(x, false)
	return found
}

// walkWitnessTree visits every typed node and variable below root.
func walkWitnessTree(root reflect.Value, visit func(any)) {
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		walkWitnessChildren(v, func(node any) bool {
			visit(node)
			if _, isVar := node.(*Var); !isVar {
				walk(reflect.ValueOf(node))
			}
			return false
		})
	}
	if root.IsValid() && root.CanInterface() {
		visit(root.Interface())
	}
	walk(root)
}

// walkWitnessChildren calls each with the typed nodes directly below v: its
// expressions, statements, arms, patterns, field values and variables.
// Returning true from each descends into a node's own fields instead.
func walkWitnessChildren(v reflect.Value, each func(any) bool) {
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	var field func(reflect.Value)
	field = func(f reflect.Value) {
		switch f.Kind() {
		case reflect.Slice:
			for i := 0; i < f.Len(); i++ {
				field(f.Index(i))
			}
		case reflect.Interface, reflect.Pointer:
			if f.IsNil() || !f.CanInterface() {
				return
			}
			node := f.Interface()
			switch node.(type) {
			case Expr, Stmt, *MatchArm, *Pat, *PatField, *FieldValue, *FieldUpdate, *CarryEdge, *Join, *Carry:
				if each(node) {
					walkWitnessChildren(f, each)
				}
			case *Var:
				each(node)
			}
		}
	}
	for i := 0; i < v.NumField(); i++ {
		if v.Type().Field(i).IsExported() {
			field(v.Field(i))
		}
	}
}
