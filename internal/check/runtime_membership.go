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
			// Only foreign implementations get generated runtime contract wrappers.
			// Instantiate each predicate so concrete constraints do not require an
			// unrelated caller type parameter's membership environment.
			if fn.Decl.GoBody != nil || fn.Decl.GoBind != nil {
				calls := runtimeInvariantCalls(info, fn.Result, map[Type]bool{})
				for _, member := range fn.ResultConstraints {
					for _, con := range member.Constraints {
						calls = append(calls, runtimeConstraintCalls(info, con, member.Type)...)
					}
				}
				for _, inst := range calls {
					propagate(inst.Func.TypeParams, inst.TypeArgs)
					for _, d := range inst.Dicts {
						dictionary(d)
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

func runtimeInvariantCalls(info *Info, t Type, seen map[Type]bool) []*Instance {
	if seen[t] {
		return nil
	}
	seen[t] = true
	var calls []*Instance
	for _, con := range TypeConstraints(t) {
		calls = append(calls, runtimeConstraintCalls(info, con, t)...)
	}
	fields := func(fields []*Field) {
		for _, field := range fields {
			for _, con := range field.Constraints {
				calls = append(calls, runtimeConstraintCalls(info, con, field.Type)...)
			}
			calls = append(calls, runtimeInvariantCalls(info, field.Type, seen)...)
		}
	}
	switch t := t.(type) {
	case *Record:
		fields(t.Fields)
	case *Sealed:
		for _, variant := range t.Variants {
			for _, con := range variant.Constraints {
				calls = append(calls, runtimeConstraintCalls(info, con, t)...)
			}
			fields(variant.Fields)
		}
	case *Union:
		for _, member := range t.Members {
			calls = append(calls, runtimeInvariantCalls(info, member, seen)...)
		}
	case *List:
		calls = append(calls, runtimeInvariantCalls(info, t.Elem, seen)...)
	case *Map:
		calls = append(calls, runtimeInvariantCalls(info, t.Key, seen)...)
		calls = append(calls, runtimeInvariantCalls(info, t.Value, seen)...)
	}
	return calls
}

func runtimeConstraintCalls(info *Info, con *Constraint, t Type) []*Instance {
	var calls []*Instance
	for _, alternative := range con.Or {
		calls = append(calls, runtimeConstraintCalls(info, alternative, t)...)
	}
	for _, subject := range constraintSubjects(t, con.Path) {
		if inst := con.InstanceFor(subject); inst != nil && info.PredicateDicts(con.Pkg, inst) {
			calls = append(calls, inst)
		}
	}
	return calls
}
