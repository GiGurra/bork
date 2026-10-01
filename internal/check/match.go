package check

import (
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// A case is one thing a matched value can be, for exhaustiveness: a
// variant of a sealed type, a member of a union, `true`/`false`, or a
// whole type (Int, String, a record) that only `_` or a type pattern
// covers.
type matchCase struct {
	key   string
	label string
}

func variantKey(v *Variant) string { return "variant " + v.Parent.String() + "." + v.Name }
func typeKey(t Type) string        { return "type " + t.String() }

// casesOf lists the cases of a matched value of type t.
func casesOf(t Type) []matchCase {
	switch t := t.(type) {
	case *Sealed:
		var cs []matchCase
		for _, v := range t.Variants {
			cs = append(cs, matchCase{key: variantKey(v), label: t.Name + "." + v.Name})
		}
		return cs
	case *Union:
		var cs []matchCase
		for _, m := range t.Members {
			cs = append(cs, casesOf(m)...)
		}
		return cs
	}
	if t == Bool {
		return []matchCase{{key: "true", label: "true"}, {key: "false", label: "false"}}
	}
	return []matchCase{{key: typeKey(t), label: t.String()}}
}

func (c *checker) match(m *syntax.Match, want Type) Type {
	st := c.expr(m.X)
	if len(m.Arms) == 0 {
		c.errorf(m.Pos, "match needs at least one arm")
		return Invalid
	}
	if !isValue(st) {
		if st != Invalid {
			c.errorf(m.X.Position(), "cannot match on a value of type %s", st)
		}
		for _, arm := range m.Arms {
			c.pushScope()
			c.expr(arm.Body)
			c.popScope()
		}
		return Invalid
	}
	cases := casesOf(st)
	covered := map[string]bool{}
	seenLits := map[string]bool{}
	var armTypes []Type
	for _, arm := range m.Arms {
		c.pushScope()
		keys, ok := c.pattern(arm.Pattern, st, cases, seenLits)
		if ok {
			fresh := false
			for _, k := range keys {
				if !covered[k] {
					fresh = true
				}
				covered[k] = true
			}
			if !fresh && len(keys) > 0 {
				c.errorf(arm.Pattern.Position(), "unreachable match arm: every value it matches is handled by an earlier arm")
			}
		}
		armTypes = append(armTypes, c.exprWant(arm.Body, want))
		c.popScope()
	}
	var missing []string
	for _, mc := range cases {
		if !covered[mc.key] {
			missing = append(missing, mc.label)
		}
	}
	if len(missing) > 0 {
		c.errorf(m.Pos, "match is not exhaustive: missing %s", strings.Join(missing, ", "))
	}
	return c.unify(m.Pos, "match arms have", armTypes, want)
}

// pattern checks one pattern against the matched type st, binds the
// names it introduces, and returns the keys of the cases it fully
// covers. ok is false if the pattern has an error.
func (c *checker) pattern(p syntax.Pattern, st Type, cases []matchCase, seenLits map[string]bool) (keys []string, ok bool) {
	switch p := p.(type) {
	case *syntax.WildcardPat:
		for _, mc := range cases {
			keys = append(keys, mc.key)
		}
		return keys, true

	case *syntax.LitPat:
		lt := c.exprWant(p.Value, st)
		if lt == Invalid {
			return nil, false
		}
		if !identical(lt, st) {
			c.errorf(p.Pos, "cannot match a %s literal against a value of type %s", lt, st)
			return nil, false
		}
		text := literalText(p.Value, c.info)
		if seenLits[text] {
			c.errorf(p.Pos, "unreachable match arm: %s is already matched by an earlier arm", text)
			return nil, false
		}
		seenLits[text] = true
		if lt == Bool {
			return []string{text}, true
		}
		// An Int or String literal never covers the whole type, but it
		// is unreachable once the type is covered.
		return nil, true

	case *syntax.TypePat:
		t := c.resolveType(p.Type)
		if t == Invalid {
			c.bind(p.Name, p.Pos, Invalid, p)
			return nil, false
		}
		keys, ok := c.typeCover(t, st, p)
		c.info.PatTypes[p] = t
		c.bind(p.Name, p.Pos, t, p)
		return keys, ok

	case *syntax.VariantPat:
		return c.variantPattern(p, st)
	}
	return nil, false
}

// typeCover returns the cases covered by a pattern that matches type t
// against a value of type st: all of st if t is st, or the members of a
// union st that t names.
func (c *checker) typeCover(t, st Type, p syntax.Pattern) ([]string, bool) {
	if identical(t, st) {
		var keys []string
		for _, mc := range casesOf(st) {
			keys = append(keys, mc.key)
		}
		return keys, true
	}
	u, ok := st.(*Union)
	if !ok {
		c.errorf(p.Position(), "pattern type %s does not match a value of type %s", t, st)
		return nil, false
	}
	members := []Type{t}
	if tu, ok := t.(*Union); ok {
		members = tu.Members
	}
	var keys []string
	for _, m := range members {
		if !containsMember(u, m) {
			c.errorf(p.Position(), "%s is not one of the types in %s", m, st)
			return nil, false
		}
		for _, mc := range casesOf(m) {
			keys = append(keys, mc.key)
		}
	}
	return keys, true
}

func (c *checker) variantPattern(p *syntax.VariantPat, st Type) ([]string, bool) {
	switch len(p.Path) {
	case 1:
		// A bare type name: `NotFound`, or a record with field bindings
		// `User { name }`.
		name := p.Path[0]
		t := c.typeNamed(name)
		if t == nil {
			c.errorf(p.Pos, "unknown type %s (variants are written with their type, as in Shape.Circle)", name)
			return nil, false
		}
		keys, ok := c.typeCover(t, st, p)
		c.info.PatTypes[p] = t
		if len(p.Fields) > 0 {
			rec, isRec := t.(*Record)
			if !isRec {
				c.errorf(p.Pos, "%s is not a record, so it has no fields to bind", name)
				return nil, false
			}
			c.bindFieldPats(p, rec.Fields, name)
		}
		return keys, ok
	case 2:
		owner, name := p.Path[0], p.Path[1]
		v := c.variantRef(p.Pos, owner, name, st)
		if v == nil {
			return nil, false
		}
		if !variantBelongs(v, st) {
			c.errorf(p.Pos, "%s.%s cannot occur in a value of type %s", owner, name, st)
			return nil, false
		}
		c.info.PatVariants[p] = v
		c.bindFieldPats(p, v.Fields, owner+"."+name)
		return []string{variantKey(v)}, true
	}
	c.errorf(p.Pos, "invalid pattern %s", strings.Join(p.Path, "."))
	return nil, false
}

func variantBelongs(v *Variant, st Type) bool {
	if identical(v.Parent, st) {
		return true
	}
	u, ok := st.(*Union)
	return ok && containsMember(u, v.Parent)
}

func (c *checker) bindFieldPats(p *syntax.VariantPat, fields []*Field, owner string) {
	for _, fp := range p.Fields {
		f := findField(fields, fp.Field)
		if f == nil {
			c.errorf(fp.Pos, "%s has no field %s", owner, fp.Field)
			c.bind(fp.Bind, fp.Pos, Invalid, fp)
			continue
		}
		c.bind(fp.Bind, fp.Pos, f.Type, fp)
	}
}

// literalText renders a literal pattern's value, to detect duplicates.
func literalText(e syntax.Expr, info *Info) string {
	if v, ok := info.Consts[e]; ok {
		return v.ExactString()
	}
	switch e := e.(type) {
	case *syntax.StringLit:
		return `"` + e.Value + `"`
	case *syntax.BoolLit:
		if e.Value {
			return "true"
		}
		return "false"
	}
	return "?"
}
