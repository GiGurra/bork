package check

import "github.com/GiGurra/bork/internal/syntax"

// ShowDicts are the optional custom renderers visible where e is printed.
func (i *Info) ShowDicts(e syntax.Expr) []*Dict { return i.showDicts[e] }

func (c *checker) recordShow(e syntax.Expr, t Type) {
	var show *Class
	for _, cl := range c.info.Classes {
		if cl.Prelude && cl.Name == "Show" {
			show = cl
			break
		}
	}
	if show == nil || t == Invalid {
		return
	}
	var dicts []*Dict
	seen := map[Type]bool{}
	unknown := false
	var visit func(Type)
	visit = func(t Type) {
		if seen[t] {
			return
		}
		seen[t] = true
		if d := c.findDict(show, t, e.Position(), 0, true); d != nil {
			dicts = append(dicts, d)
			return
		}
		savedHave := c.have
		c.have = nil
		defer func() { c.have = savedHave }()
		fields := func(fs []*Field) {
			for _, f := range fs {
				visit(f.Type)
			}
		}
		switch t := t.(type) {
		case *TypeParam:
			unknown = true
		case *List:
			visit(t.Elem)
		case *Map:
			visit(t.Key)
			visit(t.Value)
		case *Record:
			fields(t.Fields)
		case *Sealed:
			for _, v := range t.Variants {
				fields(v.Fields)
			}
		case *Union:
			for _, m := range t.Members {
				visit(m)
			}
		}
	}
	saved := c.have
	c.have = c.declaredFacts(e)
	visit(t)
	c.have = saved
	// An unbounded generic value can still have a concrete instance visible
	// in its defining package. Generic instance heads need a known type.
	if unknown {
		for _, ci := range c.pkg.inScope {
			if ci.Class == show && len(ci.TypeParams) == 0 && len(ci.Constraints) == 0 {
				visit(ci.Type)
			}
		}
	}
	if len(dicts) > 0 {
		if c.info.showDicts == nil {
			c.info.showDicts = map[syntax.Expr][]*Dict{}
		}
		c.info.showDicts[e] = dicts
	}
}
