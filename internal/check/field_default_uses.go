package check

import (
	"sort"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Keep the first concrete use of each specialization for default diagnostics.
func (c *checker) noteDefaultTypeUse(t Type, pos diag.Pos) {
	var visit func(Type, map[Type]bool)
	visit = func(t Type, seen map[Type]bool) {
		if t == nil || seen[t] {
			return
		}
		seen[t] = true
		switch t := t.(type) {
		case *List:
			visit(t.Elem, seen)
		case *Map:
			visit(t.Key, seen)
			visit(t.Value, seen)
		case *Union:
			for _, m := range t.Members {
				visit(m, seen)
			}
		case *FuncType:
			for _, p := range t.Params {
				visit(p, seen)
			}
			visit(t.Result, seen)
		case *Record:
			if t.Base != nil && !hasTypeParam(t) && c.info.typeUses[t].File == "" {
				c.info.typeUses[t] = pos
			}
			for _, f := range t.Fields {
				visit(f.Type, seen)
			}
		case *Sealed:
			if t.Base != nil && !hasTypeParam(t) && c.info.typeUses[t].File == "" {
				c.info.typeUses[t] = pos
			}
			for _, v := range t.Variants {
				for _, f := range v.Fields {
					visit(f.Type, seen)
				}
			}
		}
	}
	visit(t, map[Type]bool{})
}

// Generic bodies are checked once. Materialize the concrete record types
// their calls use, including records inside helpers that return no record.
// This lets defaults validate against the actual types and dictionaries.
func (c *checker) materializeDefaultUses() {
	type use struct {
		inst  *Instance
		owner *Func
		pos   diag.Pos
	}
	var uses []use
	for e, inst := range c.info.instances {
		uses = append(uses, use{inst, c.info.exprOwners[e], e.Position()})
	}
	for e, inst := range c.info.funcRefs {
		uses = append(uses, use{inst, c.info.exprOwners[e], e.Position()})
	}
	sort.Slice(uses, func(i, j int) bool {
		a, b := uses[i].pos, uses[j].pos
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
	type typed struct {
		expr syntax.Expr
		typ  Type
	}
	var exprs []typed
	for e, t := range c.info.types {
		exprs = append(exprs, typed{e, t})
	}
	sort.Slice(exprs, func(i, j int) bool {
		a, b := exprs[i].expr.Position(), exprs[j].expr.Position()
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
	// Group the sorted lists once, so each specialization visits only its body.
	usesByOwner := map[*Func][]use{}
	for _, u := range uses {
		usesByOwner[u.owner] = append(usesByOwner[u.owner], u)
	}
	exprsByOwner := map[*Func][]typed{}
	for _, e := range exprs {
		owner := c.info.exprOwners[e.expr]
		exprsByOwner[owner] = append(exprsByOwner[owner], e)
	}
	seen := map[*Func]map[string]bool{}
	depth, remaining := 0, 100000
	limitReported := false
	var visit func(*Instance, diag.Pos)
	var dict func(*Dict, map[*TypeParam]Type, diag.Pos, map[*Dict]bool)
	dict = func(d *Dict, bound map[*TypeParam]Type, pos diag.Pos, active map[*Dict]bool) {
		if d == nil || active[d] {
			return
		}
		active[d] = true
		if d.Inst != nil {
			args := make([]Type, len(d.TypeArgs))
			for i, t := range d.TypeArgs {
				args[i] = subst(t, bound)
			}
			for _, fn := range d.Inst.Methods {
				visit(&Instance{Func: fn, TypeArgs: args}, pos)
			}
		}
		for _, a := range d.Args {
			dict(a, bound, pos, active)
		}
	}
	visit = func(inst *Instance, pos diag.Pos) {
		if limitReported {
			return
		}
		for _, t := range inst.TypeArgs {
			if hasTypeParam(t) {
				return
			}
		}
		remaining--
		if depth >= 64 || remaining < 0 {
			limitReported = true
			c.errorf(pos, "generic default dependency expansion exceeds the work or specialization depth limit")
			return
		}
		depth++
		defer func() { depth-- }()
		for _, t := range inst.TypeArgs {
			c.noteDefaultTypeUse(t, pos)
		}
		fn := inst.Func
		key := argsKey(inst.TypeArgs)
		if seen[fn] == nil {
			seen[fn] = map[string]bool{}
		}
		if seen[fn][key] {
			return
		}
		seen[fn][key] = true
		bound := bindParams(fn.TypeParams, inst.TypeArgs)
		for _, e := range exprsByOwner[fn] {
			if hasGenericFieldDefault(e.typ, map[Type]bool{}) {
				c.noteDefaultTypeUse(subst(e.typ, bound), pos)
			}
		}
		for _, u := range usesByOwner[fn] {
			args := make([]Type, len(u.inst.TypeArgs))
			for i, t := range u.inst.TypeArgs {
				args[i] = subst(t, bound)
			}
			next := *u.inst
			next.TypeArgs = args
			visit(&next, pos)
			for _, d := range u.inst.Dicts {
				dict(d, bound, pos, map[*Dict]bool{})
			}
		}
	}
	for _, u := range uses {
		visit(u.inst, u.pos)
		for _, d := range u.inst.Dicts {
			dict(d, nil, u.pos, map[*Dict]bool{})
		}
	}
}

// Avoid instantiating unrelated generic records while finding default uses.
func hasGenericFieldDefault(t Type, seen map[Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch t := t.(type) {
	case *List:
		return hasGenericFieldDefault(t.Elem, seen)
	case *Map:
		return hasGenericFieldDefault(t.Key, seen) || hasGenericFieldDefault(t.Value, seen)
	case *Union:
		for _, m := range t.Members {
			if hasGenericFieldDefault(m, seen) {
				return true
			}
		}
	case *FuncType:
		for _, p := range t.Params {
			if hasGenericFieldDefault(p, seen) {
				return true
			}
		}
		return hasGenericFieldDefault(t.Result, seen)
	case *Record:
		for _, f := range t.Fields {
			if f.defaultGeneric && f.Decl != nil && f.Decl.Default != nil || hasGenericFieldDefault(f.Type, seen) {
				return true
			}
		}
	case *Sealed:
		for _, v := range t.Variants {
			for _, f := range v.Fields {
				if f.defaultGeneric && f.Decl != nil && f.Decl.Default != nil || hasGenericFieldDefault(f.Type, seen) {
					return true
				}
			}
		}
	}
	return false
}
