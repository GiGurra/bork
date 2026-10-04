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
	isType := c.isTypeName(id.Name)
	if isType {
		c.noteSourceType(id.Pos, id.Name)
	}
	return id.Name, isType
}

func (c *checker) selector(e *syntax.Selector, want Type) Type {
	if head, ok := e.X.(*syntax.TypeHead); ok {
		typ := c.resolveType(head.Type)
		s, ok := typ.(*Sealed)
		if !ok {
			if typ != Invalid {
				c.errorf(e.Pos, "%s is not a sealed type, so it has no variants", head.Type.Name)
			}
			return Invalid
		}
		v := c.specializedVariant(e.Pos, s, e.Name)
		if v == nil {
			return Invalid
		}
		if len(v.Fields) > 0 {
			c.errorf(e.Pos, "%s.%s has fields; build it with braces", head.Type.Name, e.Name)
			return Invalid
		}
		c.info.selectorVariants[e] = v
		c.info.constructorConstraints[e] = c.constraintsOf(head.Type, typ, c.paramScope())
		return typ
	}

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
	if xt == OwnedScope {
		if e.Name != "scope" {
			c.errorf(e.Pos, "an OwnedScope has no field %s; b.scope borrows its Scope", e.Name)
			return Invalid
		}
		fn := c.preludePkg.Funcs["scopeOf"]
		c.info.ownerScopes[e] = fn
		if c.fn != nil {
			c.fn.Calls = append(c.fn.Calls, fn)
		}
		return Scope
	}
	switch xt := xt.(type) {
	case *Record:
		if c.storeFields(e.Pos, xt) {
			return Invalid
		}
		if f := xt.Field(e.Name); f != nil {
			return f.Type
		}
		if why := c.info.mockCallLeftOut(xt, e.Name); why != "" {
			c.errorf(e.Pos, "%s", why)
			return Invalid
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
	if rec := c.info.conversionRecords[e]; rec != nil {
		if !c.recordConstruction(e.Position(), rec, "convert into") {
			c.skipFieldInits(e)
			return Invalid
		}
		c.info.recordTargets[e] = rec
		c.fieldInits(e, rec.Fields, rec.Name)
		return rec
	}
	switch t := e.Type.(type) {
	case *syntax.ContextName:
		return c.contextRecord(e, t, want)
	case *syntax.TypeHead:
		return c.specializedLit(e, t, "")
	case *syntax.Ident:
		typ := c.typeNamed(t.Name)
		c.noteSourceType(t.Pos, t.Name)
		if typ == nil {
			c.unknownType(t.Pos, t.Name)
			c.skipFieldInits(e)
			return Invalid
		}
		switch typ := typ.(type) {
		case *Record:
			if !c.recordConstruction(t.Pos, typ, "construct") {
				c.skipFieldInits(e)
				return Invalid
			}
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
		if head, ok := t.X.(*syntax.TypeHead); ok {
			return c.specializedLit(e, head, t.Name)
		}
		owner := t.X.(*syntax.Ident).Name
		c.noteSourceType(t.X.Position(), owner)
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
	return c.genericContextLit(e, base, variant, label, want)
}

func (c *checker) fieldInits(e *syntax.RecordLit, fields []*Field, owner string) {
	c.fieldInitsTyped(e, fields, owner, nil)
}

// fieldInitsTyped checks a literal's fields. If types is not nil, it
// holds the types of the field values, already checked.
func (c *checker) fieldInitsTyped(e *syntax.RecordLit, fields []*Field, owner string, types []Type) {
	for _, field := range fields {
		c.ensureFieldDefault(field)
	}
	c.checkComputedCycles(fields)
	inits := append([]*syntax.FieldInit(nil), e.Fields...)
	present := map[string]bool{}
	for _, fi := range inits {
		present[fi.Name] = true
	}
	for _, field := range fields {
		if present[field.Name] {
			continue
		}
		if x := c.info.fieldDefaults[field]; x != nil {
			if isLiteral(x) {
				x = copyLiteral(x)
			}
			inits = append(inits, &syntax.FieldInit{Pos: e.Position(), Name: field.Name, Value: x})
		}
	}
	c.info.recordInits[e] = inits
	given := map[string]bool{}
	for i, fi := range inits {
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
		if f.Computed && present[fi.Name] {
			c.errorf(fi.Pos, "computed field %s cannot be supplied; change its dependencies instead", fi.Name)
		}
		given[fi.Name] = true
		var t Type
		if types != nil && i < len(types) {
			t = types[i]
		} else if c.sharedDefaults[fi.Value] {
			t = c.info.types[fi.Value]
		} else {
			t = c.fieldInitializer(fi.Value, f)
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
	c.recordConstruction(e.Pos, rec, "copy")
	var paths []string
	for _, u := range e.Updates {
		target := c.copyTarget(rec, u)
		t := c.fieldInitializer(u.Value, target)
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
		c.ensureFieldDefault(f)
		if f.Computed {
			c.errorf(u.Pos, "computed field %s cannot be updated; change its dependencies instead", f.Name)
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
		c.recordConstruction(u.Pos, next, "update fields of")
		cur = next
	}
	return nil
}

func (c *checker) try(e *syntax.Try) Type {
	xt := c.expr(e.X)
	if c.producer != nil && c.producer.depth == c.lambdaDepth {
		c.errorf(e.Pos, "? cannot be used in a generator; yield an explicit error value instead")
		return Invalid
	}
	ctx := c.initializerContext
	inDeferred := ctx != nil && ctx.depth == c.lambdaDepth
	if c.lambdaDepth > 0 && !inDeferred {
		if xt != Invalid {
			c.errorf(e.Pos, "? cannot be used in a lambda (it would return from the enclosing function); use match")
		}
		return Invalid
	}
	result := c.fn.Result
	if inDeferred {
		result = ctx.want
		if result == nil {
			if u, ok := xt.(*Union); ok {
				ctx.returns = append(ctx.returns, u.Members[1:]...)
				c.info.tries[e] = &TryInfo{Kept: u.Members[0], Rest: u.Members[1:]}
				return u.Members[0]
			}
			c.errorf(e.Pos, "? on an Option in a %s needs a result type annotation", ctx.name)
			return Invalid
		}
	}
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

// Reading and matching a private record stays public; only construction and
// updates of its representation belong to the declaring package.
func (c *checker) recordConstruction(pos diag.Pos, rec *Record, operation string) bool {
	if rec.Decl == nil || !rec.Decl.Private || rec.Pkg == c.pkg {
		return !c.storeFields(pos, rec)
	}
	pkg := rec.Pkg.Path
	if rec.Prelude {
		pkg = "prelude"
	}
	c.diags.AddCode(pos, "construction.private_record", "cannot %s %s: package %s controls its construction; use an exported constructor or update method from that package", operation, rec.Name, pkg)
	return false
}

// Explicit heads resolve once; expected types cannot replace their arguments.
func (c *checker) specializedLit(e *syntax.RecordLit, head *syntax.TypeHead, variant string) Type {
	typ := c.resolveType(head.Type)
	var fields []*Field
	switch t := typ.(type) {
	case *Record:
		if variant != "" {
			c.errorf(e.Type.Position(), "%s is not a sealed type, so it has no variants", head.Type.Name)
			break
		}
		if !c.recordConstruction(head.Position(), t, "construct") {
			break
		}
		c.info.recordTargets[e], fields = t, t.Fields
	case *Sealed:
		if variant == "" {
			c.errorf(head.Position(), "%s is a sealed type; build one of its variants", head.Type.Name)
			break
		}
		v := c.specializedVariant(e.Type.Position(), t, variant)
		if v == nil {
			break
		}
		c.info.recordTargets[e], fields = v, v.Fields
	default:
		if typ != Invalid {
			c.errorf(head.Position(), "%s is not a record type", head.Type.Name)
		}
	}
	if c.info.recordTargets[e] == nil {
		c.skipFieldInits(e)
		return Invalid
	}
	c.info.constructorConstraints[e] = c.constraintsOf(head.Type, typ, c.paramScope())
	c.fieldInits(e, fields, writtenText(e.Type))
	return typ
}

func (c *checker) specializedVariant(pos diag.Pos, owner *Sealed, name string) *Variant {
	v := owner.Variant(name)
	if v == nil {
		c.errorf(pos, "%s has no variant %s", TypeText(owner, c.pkg), name)
		return nil
	}
	if !c.visibleVariant(pos, owner, name) {
		return nil
	}
	return v
}
