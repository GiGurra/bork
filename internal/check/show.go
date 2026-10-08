package check

// IsShow identifies the coherent prelude renderer; user-defined classes
// named Show retain the ordinary instance rules.
func IsShow(class *Class) bool { return class.Prelude && class.Name == "Show" }

func showBase(t Type) (Type, *Package, []*TypeParam, []Type) {
	switch t := t.(type) {
	case *Record:
		if t.Base != nil {
			return t.Base, t.Pkg, t.Base.TypeParams, t.Args
		}
		return t, t.Pkg, t.TypeParams, nil
	case *Sealed:
		if t.Base != nil {
			return t.Base, t.Pkg, t.Base.TypeParams, t.Args
		}
		return t, t.Pkg, t.TypeParams, nil
	}
	return nil, nil, nil, nil
}

func (c *checker) validShow(ci *ClassInstance) bool {
	base, pkg, params, args := showBase(ci.Type)
	if base == nil {
		c.errorf(ci.Decl.Type.Pos, "Show instances require a declared record or sealed type; wrap basic types, lists and maps in a declared type")
		return false
	}
	if pkg != c.pkg || ci.Prelude {
		c.errorf(ci.Decl.Type.Pos, "a Show instance must be declared in the type's own package, so its text is the same everywhere")
		return false
	}
	universal := len(ci.TypeParams) == len(params) && len(args) == len(params)
	used := map[*TypeParam]bool{}
	for _, arg := range args {
		tp, ok := arg.(*TypeParam)
		if !ok || used[tp] {
			universal = false
			break
		}
		used[tp] = true
	}
	for _, tp := range ci.TypeParams {
		if !used[tp] {
			universal = false
		}
		for _, bound := range tp.Bounds {
			if !IsShow(bound) {
				c.errorf(tp.Decl.Pos, "a generic Show instance may only have Show bounds: its renderer must work for every instantiation")
				return false
			}
		}
	}
	if !universal {
		c.errorf(ci.Decl.Type.Pos, "Show instances for generic types must cover every instantiation, as in Show[Box[T]]; specialized renderers would make generic printing inconsistent")
		return false
	}
	for _, other := range c.info.ClassInstances {
		otherBase, _, _, _ := showBase(other.Type)
		if IsShow(other.Class) && otherBase == base {
			c.errorf(ci.Decl.Pos, "type %s already has a Show instance (%s); Show has one renderer per type so printing is consistent everywhere", base, other.Name)
			return false
		}
	}
	return true
}

// Implicit Go rendering methods cannot accept the membership environment that
// ordinary generic calls carry. Reject these renderers before generation.
func (c *checker) checkShowMembership() {
	needed := RuntimeMembershipParameters(c.info)
	for _, ci := range c.info.ClassInstances {
		if !IsShow(ci.Class) {
			continue
		}
		for _, p := range ci.TypeParams {
			if needed[p] {
				c.errorf(ci.Decl.Type.Pos, "generic Show instance cannot inspect union membership of %s: implicit rendering cannot preserve it; match concrete members instead, or use a sealed type", p.Name)
				break
			}
		}
	}
}
