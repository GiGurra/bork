package check

import (
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// instanceIn finds the instance of the generic type base that t is, or
// that t contains as a union member. It returns nil if there is none,
// or if a union holds several different instances.
func instanceIn(t Type, base Type) Type {
	if t == nil || base == nil {
		return nil
	}
	if genericBase(t) == base {
		return t
	}
	u, ok := t.(*Union)
	if !ok {
		return nil
	}
	var found Type
	for _, m := range u.Members {
		if genericBase(m) == base {
			if found != nil {
				return nil
			}
			found = m
		}
	}
	return found
}

// article is "a" or "an", for the word w.
func article(w string) string {
	if w != "" && strings.ContainsRune("AEIOUaeiou", rune(w[0])) {
		return "an"
	}
	return "a"
}

// optionIn finds the Option type that t is or contains (see instanceIn).
func (c *checker) optionIn(t Type) *Sealed {
	s, _ := instanceIn(t, c.info.Named["Option"]).(*Sealed)
	return s
}

// typeNamed resolves a type name used in an expression or pattern
// (`Shape` in `Shape.Circle`). It returns nil if the name is not a type.
func (c *checker) typeNamed(name string) Type {
	if t, ok := basicTypes[name]; ok {
		return t
	}
	if e := c.lookupType(name); e != nil {
		return c.resolveDecl(e)
	}
	return nil
}

// variantRef resolves `Owner.Variant`, where Owner is a sealed type
// name. For a generic sealed type (Option), the instance comes from ctx
// (the expected type, or the matched value's type). Returns nil after
// reporting an error.
func (c *checker) variantRef(pos diag.Pos, owner, name string, ctx Type) *Variant {
	t := c.typeNamed(owner)
	if t == nil {
		c.unknownType(pos, owner)
		return nil
	}
	sealed, ok := t.(*Sealed)
	if !ok {
		c.errorf(pos, "%s is not a sealed type, so it has no variants", owner)
		return nil
	}
	if sealed.Variant(name) == nil {
		c.errorf(pos, "%s has no variant %s", owner, name)
		return nil
	}
	if !c.visibleVariant(pos, sealed, name) {
		return nil
	}
	if len(sealed.TypeParams) > 0 {
		generic := owner + "[" + paramNames(sealed.TypeParams) + "]"
		report := func() {
			c.errorf(pos, "cannot tell which %s type %s.%s is here; use it where %s %s is expected", owner, owner, name, article(owner), generic)
		}
		inst, _ := instanceIn(ctx, sealed).(*Sealed)
		switch {
		case c.unbound(ctx):
			// Which one is decided later in the call it is given to.
			inst = c.newOrigin(sealed, report).(*Sealed)
			c.solve(ctx, inst)
		case inst == nil:
			report()
			return nil
		}
		sealed = inst
	}
	return sealed.Variant(name)
}

// visibleVariant applies package visibility to constructors and patterns.
func (c *checker) visibleVariant(pos diag.Pos, sealed *Sealed, name string) bool {
	if sealed.Pkg != nil && sealed.Pkg != c.pkg && !Exported(name) {
		c.errorf(pos, "%s.%s is not exported by package %s (only variants starting with an upper-case letter are)", sealed.Name, name, sealed.Pkg.Path)
		return false
	}
	return true
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
	if fn, why, isMethod := c.methodReference(e); isMethod {
		if fn == nil {
			c.errorf(e.Pos, "%s", why)
			return Invalid
		}
		return c.funcValue(e, writtenText(e), fn, want)
	}
	if owner, ok := c.isTypeRef(e.X); ok {
		v := c.variantRef(e.Pos, owner, e.Name, want)
		if v == nil {
			return Invalid
		}
		if len(v.Fields) > 0 {
			c.errorf(e.Pos, "%s.%s has fields; build it with %s.%s { ... }", owner, e.Name, owner, e.Name)
			return Invalid
		}
		c.info.selectorVariants[e] = v
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
			c.unknownType(t.Pos, t.Name)
			c.skipFieldInits(e)
			return Invalid
		}
		switch typ := typ.(type) {
		case *Record:
			if len(typ.TypeParams) > 0 {
				return c.genericLit(e, typ, "", t.Name, want)
			}
			c.info.recordTargets[e] = typ
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
		if s, ok := c.typeNamed(owner).(*Sealed); ok && len(s.TypeParams) > 0 {
			return c.genericLit(e, s, t.Name, owner+"."+t.Name, want)
		}
		v := c.variantRef(t.Pos, owner, t.Name, want)
		if v == nil {
			c.skipFieldInits(e)
			return Invalid
		}
		c.info.recordTargets[e] = v
		c.fieldInits(e, v.Fields, owner+"."+v.Name)
		return v.Parent
	}
	return Invalid
}

// genericLit checks a literal of a generic record, or of a variant of a
// generic sealed type (`Option.Some { value: 1 }`). The type arguments
// come from the expected type, or else from the fields.
func (c *checker) genericLit(e *syntax.RecordLit, base Type, variant, label string, want Type) Type {
	var fields []*Field
	if s, ok := base.(*Sealed); ok {
		v := s.Variant(variant)
		if v == nil {
			c.errorf(e.Type.Position(), "%s has no variant %s", s.Name, variant)
			c.skipFieldInits(e)
			return Invalid
		}
		if !c.visibleVariant(e.Type.Position(), s, variant) {
			c.skipFieldInits(e)
			return Invalid
		}
		fields = v.Fields
	} else {
		fields = base.(*Record).Fields
	}
	in := typeInference(typeParamsOf(base))
	if inst := instanceIn(want, base); inst != nil {
		for i, a := range TypeArgs(inst) {
			if !c.open(a) {
				in.bound[in.params[i]] = a
			}
		}
	}
	types := make([]Type, len(e.Fields))
	check := func(i int) {
		fi := e.Fields[i]
		f := findField(fields, fi.Name)
		if f == nil {
			types[i] = c.expr(fi.Value)
			return
		}
		pw := in.subst(f.Type)
		if in.open(pw) {
			pw = nil
		}
		types[i] = c.exprWant(fi.Value, pw)
		in.unify(f.Type, types[i])
	}
	for i, fi := range e.Fields {
		if !c.needsContext(fi.Value) {
			check(i)
		}
	}
	for i, fi := range e.Fields {
		if c.needsContext(fi.Value) {
			check(i)
		}
	}
	if missing := in.unsolved(); len(missing) > 0 {
		for _, t := range types {
			if t == Invalid {
				return Invalid
			}
		}
		c.errorf(e.Type.Position(), "cannot tell what %s is in this %s; use it where its type is known", strings.Join(missing, " and "), label)
		return Invalid
	}
	inst := instantiate(base, in.args())
	if s, ok := inst.(*Sealed); ok {
		v := s.Variant(variant)
		c.info.recordTargets[e] = v
		c.fieldInitsTyped(e, v.Fields, label, types)
		return s
	}
	c.info.recordTargets[e] = inst
	c.fieldInitsTyped(e, inst.(*Record).Fields, label, types)
	return inst
}

func (c *checker) fieldInits(e *syntax.RecordLit, fields []*Field, owner string) {
	c.fieldInitsTyped(e, fields, owner, nil)
}

// fieldInitsTyped checks a literal's fields. If types is not nil, it
// holds the types of the field values, already checked.
func (c *checker) fieldInitsTyped(e *syntax.RecordLit, fields []*Field, owner string, types []Type) {
	given := map[string]bool{}
	for i, fi := range e.Fields {
		f := findField(fields, fi.Name)
		if f == nil {
			c.errorf(fi.Pos, "%s has no field %s", owner, fi.Name)
			if types == nil {
				c.expr(fi.Value)
			}
			continue
		}
		if given[fi.Name] {
			c.errorf(fi.Pos, "field %s is given twice", fi.Name)
		}
		given[fi.Name] = true
		var t Type
		if types != nil {
			t = types[i]
		} else {
			t = c.exprWant(fi.Value, f.Type)
		}
		ft := f.Type
		if t, ft = c.settle(t, ft); !assignable(t, ft) {
			c.errorf(fi.Value.Position(), "field %s of %s must be %s, found %s", fi.Name, owner, ft, t)
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
		if target != nil {
			if t, tt := c.settle(t, target.Type); !assignable(t, tt) {
				c.errorf(u.Value.Position(), "%s must be %s, found %s", strings.Join(u.Path, "."), tt, t)
			}
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
		c.info.tries[e] = info
		return info.Kept
	case *Sealed:
		if IsOption(t) {
			noneOf := c.optionIn(result)
			if noneOf == nil {
				c.errorf(e.Pos, "? on %s would return Option.None from %s, but %s returns %s", t, c.fn.Decl.Name, c.fn.Decl.Name, result)
				return Invalid
			}
			c.info.tries[e] = &TryInfo{Kept: t.Args[0], Option: t, NoneOf: noneOf}
			return t.Args[0]
		}
	}
	if xt != Invalid {
		c.errorf(e.Pos, "? needs a union or an Option, found %s", xt)
	}
	return Invalid
}
