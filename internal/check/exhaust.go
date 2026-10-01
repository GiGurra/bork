package check

import (
	"go/constant"
	"strings"
)

// Exhaustiveness and reachability of match arms, by the usefulness
// algorithm over pattern matrices (Maranget, "Warnings for pattern
// matching"): an arm is reachable if it is useful after the arms before
// it, and a match is exhaustive if `_` is not useful after all its arms.
//
// Values are taken apart by constructors: the variants of a sealed type,
// the members of a union, true and false, a record's single
// constructor, and the literals of Int, String, and other types with
// infinitely many values (which only `_` covers completely).

// ctor is a constructor of values of some type.
type ctor struct {
	key   string
	args  []Type // the types of its parts
	label string
	// One of these describes it.
	variant *Variant
	member  Type // a union member
	record  *Record
	lit     constant.Value
}

// ctorsOf lists the constructors of t, if it has finitely many.
func ctorsOf(t Type) ([]*ctor, bool) {
	switch t := t.(type) {
	case *Sealed:
		var cs []*ctor
		for _, v := range t.Variants {
			cs = append(cs, variantCtor(v))
		}
		return cs, true
	case *Union:
		var cs []*ctor
		for _, m := range t.Members {
			cs = append(cs, memberCtor(m))
		}
		return cs, true
	case *Record:
		return []*ctor{recordCtor(t)}, true
	}
	if t == Bool {
		return []*ctor{litCtor(constant.MakeBool(true)), litCtor(constant.MakeBool(false))}, true
	}
	return nil, false
}

func variantCtor(v *Variant) *ctor {
	c := &ctor{key: "variant " + v.Name, label: v.Parent.Name + "." + v.Name, variant: v}
	for _, f := range v.Fields {
		c.args = append(c.args, f.Type)
	}
	return c
}

func memberCtor(m Type) *ctor {
	return &ctor{key: "member " + m.String(), label: m.String(), member: m, args: []Type{m}}
}

func recordCtor(r *Record) *ctor {
	c := &ctor{key: "record", label: r.Name, record: r}
	for _, f := range r.Fields {
		c.args = append(c.args, f.Type)
	}
	return c
}

func litCtor(v constant.Value) *ctor {
	return &ctor{key: "lit " + v.ExactString(), label: v.ExactString(), lit: v}
}

// heads lists the constructors a non-wildcard pattern starts with, and
// the patterns of its parts for each (nil parts match anything). A
// type pattern for several union members has several heads.
func heads(p *Pat) []*ctor {
	switch p.Kind {
	case PatLit:
		return []*ctor{litCtor(p.Lit)}
	case PatVariant:
		return []*ctor{variantCtor(p.Variant)}
	case PatRecord:
		return []*ctor{recordCtor(p.Type.(*Record))}
	case PatType:
		var cs []*ctor
		for _, m := range p.Members {
			cs = append(cs, memberCtor(m))
		}
		return cs
	}
	return nil
}

// parts are the sub-patterns of p, for its head constructor c.
func parts(p *Pat, c *ctor) []*Pat {
	out := make([]*Pat, len(c.args))
	var fields []*Field
	switch {
	case c.variant != nil:
		fields = c.variant.Fields
	case c.record != nil:
		fields = c.record.Fields
	case c.member != nil:
		out[0] = p.Sub
		return out
	}
	for i, f := range fields {
		for _, pf := range p.Fields {
			if pf.Name == f.Name {
				out[i] = pf.Pat
			}
		}
	}
	return out
}

func isWild(p *Pat) bool { return p == nil || p.Kind == PatWild }

type row []*Pat

// specialize keeps the rows that can start with c, replacing their
// first pattern by its parts.
func specialize(rows []row, c *ctor) []row {
	var out []row
	for _, r := range rows {
		head := r[0]
		if isWild(head) {
			out = append(out, append(make(row, len(c.args)), r[1:]...))
			continue
		}
		for _, h := range heads(head) {
			if h.key == c.key {
				out = append(out, append(row(parts(head, c)), r[1:]...))
				break
			}
		}
	}
	return out
}

// defaults keeps the rows that start with a wildcard, without it.
func defaults(rows []row) []row {
	var out []row
	for _, r := range rows {
		if isWild(r[0]) {
			out = append(out, r[1:])
		}
	}
	return out
}

// witness is a value (pattern) that no row matches. other marks a value
// of a type with infinitely many values that the rows don't cover; a
// nil ctor without other means anything.
type witness struct {
	ctor  *ctor
	args  []*witness
	other bool
	typ   Type
}

// uncovered finds values of the given types matched by q but by none of
// the rows. It returns nil if there are none (q is not useful).
func uncovered(rows []row, q row, types []Type) []*witness {
	if len(q) == 0 {
		if len(rows) == 0 {
			return []*witness{}
		}
		return nil
	}
	t := types[0]
	if !isWild(q[0]) {
		for _, c := range heads(q[0]) {
			if w := uncoveredFor(rows, c, append(row(parts(q[0], c)), q[1:]...), types); w != nil {
				return w
			}
		}
		return nil
	}
	all, finite := ctorsOf(t)
	used := map[string]bool{}
	for _, r := range rows {
		if !isWild(r[0]) {
			for _, h := range heads(r[0]) {
				used[h.key] = true
			}
		}
	}
	complete := finite
	for _, c := range all {
		if !used[c.key] {
			complete = false
		}
	}
	if complete {
		for _, c := range all {
			if w := uncoveredFor(rows, c, append(make(row, len(c.args)), q[1:]...), types); w != nil {
				return w
			}
		}
		return nil
	}
	rest := uncovered(defaults(rows), q[1:], types[1:])
	if rest == nil {
		return nil
	}
	// Name a missing constructor if there is one.
	for _, c := range all {
		if !used[c.key] {
			w := &witness{ctor: c, typ: t}
			for _, at := range c.args {
				w.args = append(w.args, &witness{typ: at})
			}
			return append([]*witness{w}, rest...)
		}
	}
	return append([]*witness{{other: len(used) > 0, typ: t}}, rest...)
}

// uncoveredFor is uncovered for values starting with constructor c,
// where q has already been specialized to c.
func uncoveredFor(rows []row, c *ctor, q row, types []Type) []*witness {
	argTypes := append(append([]Type{}, c.args...), types[1:]...)
	w := uncovered(specialize(rows, c), q, argTypes)
	if w == nil {
		return nil
	}
	n := len(c.args)
	head := &witness{ctor: c, args: w[:n], typ: types[0]}
	return append([]*witness{head}, w[n:]...)
}

// useful reports whether p matches some value that none of earlier
// matches.
func useful(earlier []*Pat, p *Pat) bool {
	rows := make([]row, len(earlier))
	for i, e := range earlier {
		rows[i] = row{e}
	}
	return uncovered(rows, row{p}, []Type{p.Type}) != nil
}

// missingCases describes the values of type t that no pattern matches,
// one per top-level constructor (so all missing variants are listed).
func missingCases(pats []*Pat, t Type) []string {
	rows := make([]row, len(pats))
	for i, p := range pats {
		rows[i] = row{p}
	}
	all, finite := ctorsOf(t)
	if !finite || len(all) == 1 {
		w := uncovered(rows, row{nil}, []Type{t})
		if w == nil {
			return nil
		}
		return []string{w[0].describe(true)}
	}
	var missing []string
	for _, c := range all {
		if w := uncoveredFor(rows, c, make(row, len(c.args)), []Type{t}); w != nil {
			missing = append(missing, w[0].describe(true))
		}
	}
	return missing
}

// describe renders a witness as a pattern. At the top of a match (or
// as a whole union member), an unconstrained value is named by its type.
func (w *witness) describe(top bool) string {
	c := w.ctor
	switch {
	case c == nil && (top || w.other):
		if top {
			return w.typ.String()
		}
		return "_"
	case c == nil:
		return "_"
	case c.member != nil:
		return w.args[0].describe(true)
	case c.lit != nil:
		return c.label
	}
	var fields []*Field
	if c.variant != nil {
		fields = c.variant.Fields
	} else {
		fields = c.record.Fields
	}
	var shown []string
	for i, a := range w.args {
		if a.ctor != nil || a.other {
			shown = append(shown, fields[i].Name+": "+a.describe(false))
		}
	}
	if len(shown) == 0 {
		return c.label
	}
	return c.label + " { " + strings.Join(shown, ", ") + " }"
}
