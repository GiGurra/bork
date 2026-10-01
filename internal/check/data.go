package check

import (
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// optionIn finds the Option type that t is, or that t contains as a
// union member. It returns nil if there is none, or if a union holds
// several different Options.
func optionIn(t Type) *Sealed {
	if IsOption(t) {
		return t.(*Sealed)
	}
	u, ok := t.(*Union)
	if !ok {
		return nil
	}
	var found *Sealed
	for _, m := range u.Members {
		if IsOption(m) {
			if found != nil {
				return nil
			}
			found = m.(*Sealed)
		}
	}
	return found
}

// typeNamed resolves a type name used in an expression or pattern
// (`Shape` in `Shape.Circle`). It returns nil if the name is not a type.
func (c *checker) typeNamed(name string) Type {
	if t, ok := basicTypes[name]; ok {
		return t
	}
	if e, ok := c.decls[name]; ok {
		return c.resolveDecl(e)
	}
	return nil
}

// variantRef resolves `Owner.Variant`, where Owner is a sealed type name
// or Option. For Option, the concrete Option[T] comes from ctx (the
// expected type, or the matched value's type). Returns nil after
// reporting an error.
func (c *checker) variantRef(pos diag.Pos, owner, name string, ctx Type) *Variant {
	var sealed *Sealed
	if owner == "Option" {
		sealed = optionIn(ctx)
		if sealed == nil {
			c.errorf(pos, "cannot tell which Option type Option.%s is here; use it where an Option[T] is expected", name)
			return nil
		}
	} else {
		t := c.typeNamed(owner)
		if t == nil {
			c.errorf(pos, "unknown type %s", owner)
			return nil
		}
		s, ok := t.(*Sealed)
		if !ok {
			c.errorf(pos, "%s is not a sealed type, so it has no variants", owner)
			return nil
		}
		sealed = s
	}
	v := sealed.Variant(name)
	if v == nil {
		c.errorf(pos, "%s has no variant %s", sealed, name)
		return nil
	}
	return v
}

// isTypeRef reports whether x (an identifier) names a type rather than
// a value.
func (c *checker) isTypeRef(x syntax.Expr) (string, bool) {
	id, ok := x.(*syntax.Ident)
	if !ok || c.lookup(id.Name) != nil {
		return "", false
	}
	return id.Name, c.isTypeName(id.Name)
}

func (c *checker) selector(e *syntax.Selector, want Type) Type {
	if owner, ok := c.isTypeRef(e.X); ok {
		v := c.variantRef(e.Pos, owner, e.Name, want)
		if v == nil {
			return Invalid
		}
		if len(v.Fields) > 0 {
			c.errorf(e.Pos, "%s.%s has fields; build it with %s.%s { ... }", owner, e.Name, owner, e.Name)
			return Invalid
		}
		c.info.SelectorVariants[e] = v
		return v.Parent
	}
	xt := c.expr(e.X)
	switch xt := xt.(type) {
	case *Record:
		if f := xt.Field(e.Name); f != nil {
			return f.Type
		}
		c.errorf(e.Pos, "%s has no field %s", xt, e.Name)
		return Invalid
	case *Sealed:
		c.errorf(e.Pos, "cannot read %s from a %s directly; use match to find out which variant it is", e.Name, xt)
		return Invalid
	case *Union:
		c.errorf(e.Pos, "cannot read %s from a %s directly; use match or ? first", e.Name, xt)
		return Invalid
	}
	if xt != Invalid {
		c.errorf(e.Pos, "%s has no fields", xt)
	}
	return Invalid
}

func (c *checker) recordLit(e *syntax.RecordLit, want Type) Type {
	switch t := e.Type.(type) {
	case *syntax.Ident:
		typ := c.typeNamed(t.Name)
		if typ == nil {
			c.errorf(t.Pos, "unknown type %s", t.Name)
			c.skipFieldInits(e)
			return Invalid
		}
		switch typ := typ.(type) {
		case *Record:
			c.info.RecordTargets[e] = typ
			c.fieldInits(e, typ.Fields, typ.Name)
			return typ
		case *Sealed:
			c.errorf(t.Pos, "%s is a sealed type; build one of its variants, such as %s.%s { ... }", t.Name, t.Name, typ.Variants[0].Name)
		default:
			c.errorf(t.Pos, "%s is not a record type", t.Name)
		}
		c.skipFieldInits(e)
		return Invalid
	case *syntax.Selector:
		owner := t.X.(*syntax.Ident).Name
		if owner == "Option" && t.Name == "Some" {
			return c.someLit(e, t, want)
		}
		v := c.variantRef(t.Pos, owner, t.Name, want)
		if v == nil {
			c.skipFieldInits(e)
			return Invalid
		}
		c.info.RecordTargets[e] = v
		c.fieldInits(e, v.Fields, owner+"."+v.Name)
		return v.Parent
	}
	return Invalid
}

// someLit checks `Option.Some { value: x }`. The element type comes
// from the expected type when there is one, otherwise from x.
func (c *checker) someLit(e *syntax.RecordLit, sel *syntax.Selector, want Type) Type {
	expected := optionIn(want)
	if len(e.Fields) != 1 || e.Fields[0].Name != "value" {
		c.errorf(sel.Pos, "Option.Some has exactly one field: Option.Some { value: ... }")
		c.skipFieldInits(e)
		return Invalid
	}
	var elemWant Type
	if expected != nil {
		elemWant = expected.Args[0]
	}
	vt := c.exprWant(e.Fields[0].Value, elemWant)
	if vt == Invalid {
		return Invalid
	}
	if !isValue(vt) {
		c.errorf(e.Fields[0].Value.Position(), "Option.Some cannot hold a value of type %s", vt)
		return Invalid
	}
	opt := Option(vt)
	if expected != nil && assignable(vt, expected.Args[0]) {
		opt = expected
	}
	c.info.RecordTargets[e] = opt.Variants[0]
	return opt
}

func (c *checker) fieldInits(e *syntax.RecordLit, fields []*Field, owner string) {
	given := map[string]bool{}
	for _, fi := range e.Fields {
		f := findField(fields, fi.Name)
		if f == nil {
			c.errorf(fi.Pos, "%s has no field %s", owner, fi.Name)
			c.expr(fi.Value)
			continue
		}
		if given[fi.Name] {
			c.errorf(fi.Pos, "field %s is given twice", fi.Name)
		}
		given[fi.Name] = true
		t := c.exprWant(fi.Value, f.Type)
		if !assignable(t, f.Type) {
			c.errorf(fi.Value.Position(), "field %s of %s must be %s, found %s", fi.Name, owner, f.Type, t)
		}
	}
	var missing []string
	for _, f := range fields {
		if !given[f.Name] {
			missing = append(missing, f.Name)
		}
	}
	if len(missing) > 0 {
		c.errorf(e.Type.Position(), "%s is missing field(s): %s", owner, strings.Join(missing, ", "))
	}
}

func (c *checker) skipFieldInits(e *syntax.RecordLit) {
	for _, fi := range e.Fields {
		c.expr(fi.Value)
	}
}

func (c *checker) copyExpr(e *syntax.Copy) Type {
	xt := c.expr(e.X)
	rec, ok := xt.(*Record)
	if !ok {
		if xt != Invalid {
			c.errorf(e.Pos, "copy needs a record, found %s", xt)
		}
		for _, u := range e.Updates {
			c.expr(u.Value)
		}
		return Invalid
	}
	var paths []string
	for _, u := range e.Updates {
		target := c.copyTarget(rec, u)
		var want Type
		if target != nil {
			want = target.Type
		}
		t := c.exprWant(u.Value, want)
		if target != nil && !assignable(t, target.Type) {
			c.errorf(u.Value.Position(), "%s must be %s, found %s", strings.Join(u.Path, "."), target.Type, t)
		}
		path := strings.Join(u.Path, ".")
		for _, prev := range paths {
			if prev == path {
				c.errorf(u.Pos, "%s is updated twice", path)
			} else if strings.HasPrefix(path, prev+".") || strings.HasPrefix(prev, path+".") {
				c.errorf(u.Pos, "updates of %s and %s overlap", prev, path)
			}
		}
		paths = append(paths, path)
	}
	return rec
}

// copyTarget resolves a copy update's field path, which may go through
// nested records (`address.city`).
func (c *checker) copyTarget(rec *Record, u *syntax.CopyUpdate) *Field {
	cur := rec
	for i, name := range u.Path {
		f := cur.Field(name)
		if f == nil {
			c.errorf(u.Pos, "%s has no field %s", cur, name)
			return nil
		}
		if i == len(u.Path)-1 {
			return f
		}
		next, ok := f.Type.(*Record)
		if !ok {
			c.errorf(u.Pos, "cannot update %s: %s is a %s, not a record", strings.Join(u.Path, "."), strings.Join(u.Path[:i+1], "."), f.Type)
			return nil
		}
		cur = next
	}
	return nil
}

func (c *checker) try(e *syntax.Try) Type {
	xt := c.expr(e.X)
	if c.lambdaDepth > 0 {
		if xt != Invalid {
			c.errorf(e.Pos, "? cannot be used in a lambda (it would return from the enclosing function); use match")
		}
		return Invalid
	}
	result := c.fn.Result
	switch t := xt.(type) {
	case *Union:
		info := &TryInfo{Kept: t.Members[0], Rest: t.Members[1:]}
		var misfits []string
		for _, m := range info.Rest {
			if !assignable(m, result) {
				misfits = append(misfits, m.String())
			}
		}
		if len(misfits) > 0 {
			c.errorf(e.Pos, "? would return %s from %s, but %s returns %s", strings.Join(misfits, " | "), c.fn.Decl.Name, c.fn.Decl.Name, result)
			return Invalid
		}
		c.info.Tries[e] = info
		return info.Kept
	case *Sealed:
		if IsOption(t) {
			noneOf := optionIn(result)
			if noneOf == nil {
				c.errorf(e.Pos, "? on %s would return Option.None from %s, but %s returns %s", t, c.fn.Decl.Name, c.fn.Decl.Name, result)
				return Invalid
			}
			c.info.Tries[e] = &TryInfo{Kept: t.Args[0], Option: t, NoneOf: noneOf}
			return t.Args[0]
		}
	}
	if xt != Invalid {
		c.errorf(e.Pos, "? needs a union or an Option, found %s", xt)
	}
	return Invalid
}
