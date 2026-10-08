package check

// RuntimeMembershipParameters identifies type parameters whose Bork union
// membership must accompany generic calls, including generated contracts.
func RuntimeMembershipParameters(info *Info) map[*TypeParam]bool {
	needed := map[*TypeParam]bool{}
	var mark func(Type)
	mark = func(t Type) {
		switch t := t.(type) {
		case *TypeParam:
			needed[t] = true
		case *Union:
			for _, m := range t.Members {
				mark(m)
			}
		}
	}
	var pattern func(*Pat)
	pattern = func(p *Pat) {
		if p == nil {
			return
		}
		for _, m := range p.Members {
			mark(m)
		}
		pattern(p.Sub)
		for _, f := range p.Fields {
			pattern(f.Pat)
		}
		for _, e := range p.Elems {
			pattern(e)
		}
		pattern(p.Rest)
	}
	funcs := map[*Func]bool{}
	for _, fn := range info.FuncOf {
		funcs[fn] = true
	}
	for _, fn := range info.ExpandedFunctions {
		funcs[fn] = true
	}
	for _, fn := range info.Mocks {
		funcs[fn] = true
	}
	for _, ci := range info.ClassInstances {
		for _, fn := range ci.Methods {
			funcs[fn] = true
		}
		for _, fn := range ci.Metadata {
			funcs[fn] = true
		}
	}
	for fn := range funcs {
		WalkComptime(fn.Body, func(e Expr) bool {
			switch e := e.(type) {
			case *Match:
				for _, a := range e.Arms {
					pattern(a.Pat)
				}
			case *Try:
				if e.Option == nil {
					mark(e.Kept)
					for _, t := range e.Rest {
						mark(t)
					}
				}
			}
			return true
		})
	}
	for {
		before := len(needed)
		propagate := func(params []*TypeParam, args []Type) {
			for i, p := range params {
				if needed[p] && i < len(args) {
					mark(args[i])
				}
			}
		}
		var dictionary func(*Dict)
		dictionary = func(d *Dict) {
			if d == nil {
				return
			}
			if d.Inst != nil {
				propagate(d.Inst.TypeParams, d.TypeArgs)
			}
			for _, a := range d.Args {
				dictionary(a)
			}
		}
		for fn := range funcs {
			// Test-mode wrappers call result and field predicates outside fn.Body.
			// Their instantiated constraints can mention any of the caller's parameters.
			predicates := runtimeInvariantPredicates(fn.Result, map[Type]bool{})
			for _, member := range fn.ResultConstraints {
				for _, con := range member.Constraints {
					predicates = append(predicates, runtimeConstraintPredicates(con)...)
				}
			}
			for _, pred := range predicates {
				for _, p := range pred.TypeParams {
					if needed[p] {
						for _, caller := range fn.TypeParams {
							mark(caller)
						}
					}
				}
			}
			WalkComptime(fn.Body, func(e Expr) bool {
				var inst *Instance
				switch e := e.(type) {
				case *Call:
					inst = e.Inst
				case *FuncRef:
					inst = e.Inst
				case *CallBuiltin:
					dictionary(e.Dictionary)
				}
				if inst != nil {
					propagate(inst.Func.TypeParams, inst.TypeArgs)
					for _, d := range inst.Dicts {
						dictionary(d)
					}
				}
				return true
			})
		}
		if len(needed) == before {
			break
		}
	}
	return needed
}

func runtimeInvariantPredicates(t Type, seen map[Type]bool) []*Func {
	if seen[t] {
		return nil
	}
	seen[t] = true
	var out []*Func
	for _, con := range TypeConstraints(t) {
		out = append(out, runtimeConstraintPredicates(con)...)
	}
	fields := func(fs []*Field) {
		for _, f := range fs {
			for _, con := range f.Constraints {
				out = append(out, runtimeConstraintPredicates(con)...)
			}
			out = append(out, runtimeInvariantPredicates(f.Type, seen)...)
		}
	}
	switch t := t.(type) {
	case *Record:
		fields(t.Fields)
	case *Sealed:
		for _, v := range t.Variants {
			for _, con := range v.Constraints {
				out = append(out, runtimeConstraintPredicates(con)...)
			}
			fields(v.Fields)
		}
	case *Union:
		for _, m := range t.Members {
			out = append(out, runtimeInvariantPredicates(m, seen)...)
		}
	case *List:
		out = append(out, runtimeInvariantPredicates(t.Elem, seen)...)
	case *Map:
		out = append(out, runtimeInvariantPredicates(t.Key, seen)...)
		out = append(out, runtimeInvariantPredicates(t.Value, seen)...)
	}
	return out
}

func runtimeConstraintPredicates(con *Constraint) []*Func {
	var out []*Func
	for _, alt := range con.Or {
		out = append(out, runtimeConstraintPredicates(alt)...)
	}
	if con.Pred != nil {
		out = append(out, con.Pred)
	}
	return out
}
