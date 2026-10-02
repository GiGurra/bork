package check

import (
	"go/constant"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// PatKind says what a checked pattern tests.
type PatKind int

const (
	// PatWild matches anything (`_`, or a name that binds the value).
	PatWild PatKind = iota
	// PatLit matches one literal value.
	PatLit
	// PatVariant matches one variant of a sealed type, and its fields.
	PatVariant
	// PatRecord destructures a record, matching its fields.
	PatRecord
	// PatType matches the members Members of a union, and then the
	// narrowed value against Sub.
	PatType
	// PatList matches a list with as many elements as Elems (or, with
	// Rest, at least as many), and its elements against them.
	PatList
)

// Pat is a checked match pattern. Code generation lowers matches from
// these, and exhaustiveness is decided on them.
type Pat struct {
	Kind PatKind
	// Type is the type of the value matched at this position.
	Type Type
	// Bind, if not empty, names the value matched here; BindType is its
	// type (narrowed, for PatType), and BindNode the syntax node that
	// introduced it.
	Bind     string
	BindType Type
	BindNode any
	// Lit is the value of a PatLit (a number, string, or bool constant).
	Lit constant.Value
	// Variant is the variant of a PatVariant.
	Variant *Variant
	// Fields are the field patterns of a PatVariant or PatRecord, in
	// the order written. Missing fields match anything.
	Fields []*PatField
	// Members are the union members a PatType tests for, and Sub the
	// pattern for the narrowed value (nil: anything).
	Members []Type
	Sub     *Pat
	// Elems and Rest are the parts of a PatList; Rest (a wildcard,
	// possibly binding the remaining elements) is nil for a list of
	// exactly len(Elems) elements.
	Elems []*Pat
	Rest  *Pat
}

// PatField is one field of a destructuring pattern.
type PatField struct {
	Name string
	Pat  *Pat
}

// Narrowed is the type of the value a PatType lets through.
func (p *Pat) Narrowed() Type { return newUnion(p.Members) }

// patSources records where each name a pattern binds comes from: the
// matched subject, the path to it, and (at the top) the union member
// the pattern narrowed it to.
func (c *checker) patSources(p *Pat, subject syntax.Expr, path string, field *Field, top bool) {
	if p == nil {
		return
	}
	if p.Bind != "" {
		src := &PatSource{Subject: subject, Path: path, Field: field}
		if top && p.Kind == PatType {
			src.Member = p.Narrowed()
		}
		c.info.PatSources[p.BindNode] = src
	}
	var fields []*Field
	switch {
	case p.Variant != nil:
		fields = p.Variant.Fields
	case p.Kind == PatRecord:
		if r, ok := p.Type.(*Record); ok {
			fields = r.Fields
		}
	}
	for _, pf := range p.Fields {
		c.patSources(pf.Pat, subject, path+"."+pf.Name, findField(fields, pf.Name), false)
	}
	if p.Sub != nil {
		c.patSources(p.Sub, subject, path, field, false)
	}
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
	pats := make([]*Pat, len(m.Arms))
	armTypes := make([]Type, len(m.Arms))
	ok := true
	// Arms whose type comes from the context (`[]`, `Option.None`) are
	// checked last, against the type of the others if there is no
	// context.
	var order, later []int
	for i, arm := range m.Arms {
		if want == nil && c.branchNeedsContext(arm.Body) {
			later = append(later, i)
		} else {
			order = append(order, i)
		}
	}
	for _, i := range append(order, later...) {
		arm := m.Arms[i]
		c.pushScope()
		p := c.pattern(arm.Pattern, st)
		if p == nil {
			ok = false
		} else {
			c.info.ArmPats[arm] = p
			c.patSources(p, m.X, "", nil, true)
		}
		pats[i] = p
		armWant := want
		if armWant == nil && len(later) > 0 && len(order) > 0 {
			armWant = armTypes[order[0]]
		}
		armTypes[i] = c.exprWant(arm.Body, armWant)
		c.popScope()
	}
	// Broken patterns are left out; and without them, missing cases
	// would only be follow-up errors.
	var valid []*Pat
	for i, p := range pats {
		if p == nil {
			continue
		}
		if !useful(valid, p) {
			c.errorf(m.Arms[i].Pattern.Position(), "unreachable match arm: every value it matches is handled by an earlier arm")
		}
		valid = append(valid, p)
	}
	if ok {
		if missing := missingCases(pats, st); len(missing) > 0 {
			c.errorf(m.Pos, "match is not exhaustive: missing %s", strings.Join(missing, ", "))
		}
	}
	return c.unify(m.Pos, "match arms have", armTypes, want)
}

// pattern checks a pattern against a value of type st, binds the names
// it introduces, and returns the checked pattern (nil after an error).
func (c *checker) pattern(p syntax.Pattern, st Type) *Pat {
	switch p := p.(type) {
	case *syntax.WildcardPat:
		return &Pat{Kind: PatWild, Type: st}

	case *syntax.LitPat:
		if _, isUnion := st.(*Union); isUnion {
			c.errorf(p.Pos, "cannot match a literal against a value of type %s; match its type first (as in n: Int)", st)
			return nil
		}
		lt := c.exprWant(p.Value, st)
		if lt == Invalid {
			return nil
		}
		if !identical(lt, st) {
			c.errorf(p.Pos, "cannot match a %s literal against a value of type %s", lt, st)
			return nil
		}
		return &Pat{Kind: PatLit, Type: st, Lit: c.literalValue(p.Value)}

	case *syntax.TypePat:
		t := c.resolveType(p.Type)
		if t == Invalid {
			c.bind(p.Name, p.Pos, Invalid, p)
			return nil
		}
		pat := c.typePattern(t, st, p.Pos)
		if pat == nil {
			c.bind(p.Name, p.Pos, Invalid, p)
			return nil
		}
		c.bindPat(pat, p.Name, p.Pos, p)
		return pat

	case *syntax.VariantPat:
		return c.namePattern(p, st)

	case *syntax.ListPat:
		lt, ok := st.(*List)
		if !ok {
			if st != Invalid {
				c.errorf(p.Pos, "cannot match a list pattern against a value of type %s", st)
			}
			// Bind its names anyway, to avoid follow-up errors.
			saved := c.diags
			c.diags = &diag.List{}
			for _, e := range p.Elems {
				c.pattern(e, Invalid)
			}
			c.diags = saved
			if p.Rest != "" {
				c.bind(p.Rest, p.RestPos, Invalid, p)
			}
			return nil
		}
		pat := &Pat{Kind: PatList, Type: st}
		ok = true
		for _, e := range p.Elems {
			ep := c.pattern(e, lt.Elem)
			ok = ok && ep != nil
			pat.Elems = append(pat.Elems, ep)
		}
		if p.HasRest {
			pat.Rest = &Pat{Kind: PatWild, Type: st}
			if p.Rest != "" {
				c.bindPat(pat.Rest, p.Rest, p.RestPos, p)
			}
			if len(p.Elems) == 0 {
				return pat.Rest // [...rest] is any list
			}
		}
		if !ok {
			return nil
		}
		return pat
	}
	return nil
}

// typePattern is the pattern matching the values of type t within a
// value of type st: everything, if t is st, or some members of a union.
func (c *checker) typePattern(t, st Type, pos diag.Pos) *Pat {
	if identical(t, st) {
		return &Pat{Kind: PatWild, Type: st}
	}
	u, ok := st.(*Union)
	if !ok {
		c.errorf(pos, "pattern type %s does not match a value of type %s", t, st)
		return nil
	}
	members := []Type{t}
	if tu, ok := t.(*Union); ok {
		members = tu.Members
	}
	for _, m := range members {
		if !containsMember(u, m) {
			c.errorf(pos, "%s is not one of the types in %s", m, st)
			return nil
		}
	}
	return &Pat{Kind: PatType, Type: st, Members: members}
}

// bindPat makes pat bind name to the value it matches.
func (c *checker) bindPat(pat *Pat, name string, pos diag.Pos, node any) {
	pat.Bind, pat.BindNode, pat.BindType = name, node, pat.Type
	if pat.Kind == PatType {
		pat.BindType = pat.Narrowed()
	}
	c.bind(name, pos, pat.BindType, node)
}

// namePattern checks a pattern written as a name: `Shape.Circle { r }`,
// `Option.None`, `NotFound`, `User { name }`, or a name to bind.
func (c *checker) namePattern(p *syntax.VariantPat, st Type) *Pat {
	switch len(p.Path) {
	case 1:
		name := p.Path[0]
		t := c.typeNamed(name)
		if t == nil {
			if p.Braces {
				c.unknownType(p.Pos, name)
				return nil
			}
			// A name that is not a type binds the whole value.
			pat := &Pat{Kind: PatWild, Type: st}
			c.bindPat(pat, name, p.Pos, p)
			return pat
		}
		if base := genericBase(t); base == t {
			// A generic type: the instance the matched value holds.
			if t = instanceIn(st, base); t == nil {
				c.errorf(p.Pos, "%s does not match a value of type %s", name, st)
				return nil
			}
		}
		if !p.Braces {
			return c.typePattern(t, st, p.Pos)
		}
		rec, isRec := t.(*Record)
		if !isRec {
			c.errorf(p.Pos, "%s is not a record, so it has no fields to match", name)
			return nil
		}
		inner := &Pat{Kind: PatRecord, Type: rec}
		if !c.fieldPatterns(inner, p.Fields, rec.Fields, name) {
			return nil
		}
		return c.within(inner, rec, st, p.Pos)
	case 2:
		owner, name := p.Path[0], p.Path[1]
		v := c.variantRef(p.Pos, owner, name, st)
		if v == nil {
			return nil
		}
		inner := &Pat{Kind: PatVariant, Type: v.Parent, Variant: v}
		if !c.fieldPatterns(inner, p.Fields, v.Fields, owner+"."+name) {
			return nil
		}
		if !identical(v.Parent, st) {
			if u, ok := st.(*Union); !ok || !containsMember(u, v.Parent) {
				c.errorf(p.Pos, "%s.%s cannot occur in a value of type %s", owner, name, st)
				return nil
			}
		}
		return c.within(inner, v.Parent, st, p.Pos)
	}
	c.errorf(p.Pos, "invalid pattern %s", strings.Join(p.Path, "."))
	return nil
}

// within places a pattern for values of type t inside a match on st:
// as is if t is st, or after narrowing a union to its member t.
func (c *checker) within(inner *Pat, t, st Type, pos diag.Pos) *Pat {
	if identical(t, st) {
		return inner
	}
	outer := c.typePattern(t, st, pos)
	if outer == nil {
		return nil
	}
	outer.Sub = inner
	return outer
}

func (c *checker) fieldPatterns(pat *Pat, fps []*syntax.FieldPat, fields []*Field, owner string) bool {
	ok := true
	seen := map[string]bool{}
	for _, fp := range fps {
		f := findField(fields, fp.Field)
		if f == nil {
			c.errorf(fp.Pos, "%s has no field %s", owner, fp.Field)
			ok = false
			continue
		}
		if seen[fp.Field] {
			c.errorf(fp.Pos, "field %s is matched twice", fp.Field)
			ok = false
			continue
		}
		seen[fp.Field] = true
		var sub *Pat
		if fp.Pattern == nil {
			sub = &Pat{Kind: PatWild, Type: f.Type}
			c.bindPat(sub, fp.Field, fp.Pos, fp)
		} else if sub = c.pattern(fp.Pattern, f.Type); sub == nil {
			ok = false
			continue
		}
		pat.Fields = append(pat.Fields, &PatField{Name: fp.Field, Pat: sub})
	}
	return ok
}

// literalValue is the value of a literal pattern.
func (c *checker) literalValue(e syntax.Expr) constant.Value {
	if v, ok := c.info.Consts[e]; ok {
		return v
	}
	switch e := e.(type) {
	case *syntax.StringLit:
		return constant.MakeString(e.Value)
	case *syntax.BoolLit:
		return constant.MakeBool(e.Value)
	}
	return constant.MakeUnknown()
}
