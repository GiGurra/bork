package check

import (
	"sort"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// contextTarget selects only from the expected nominal types, never from
// field shapes or the packages in scope. Unsolved union heads may add candidates.
func (c *checker) contextTarget(e *syntax.ContextName, want Type, pattern bool) Type {
	want = c.zonk(want)
	candidates, unresolved := c.contextCandidates(e.Name, want)
	if unresolved || want == nil {
		c.contextError(e, "type.context_missing", "cannot tell the constructor of %s; give the value an expected record or sealed type, or write its constructor explicitly", contextText(e))
		return Invalid
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	if len(candidates) == 0 {
		if e.Name != "" {
			if fixes := c.variantContextSuggestions(e, want, pattern); len(fixes) > 0 {
				c.diags.AddCode(e.Pos, "type.context_variant_unknown", "no expected sealed type in %s has variant %s", want, e.Name)
				c.diags.Suggest(e.Pos, "type.context_variant_unknown", e.End, fixes...)
				return Invalid
			}
		}
		kind := "record"
		if e.Name != "" {
			kind = "sealed type with variant " + e.Name
		}
		c.diags.AddCode(e.Pos, "type.context_kind", "%s needs an expected %s, found %s", contextText(e), kind, want)
		return Invalid
	}
	var names []string
	var fixes []diag.Fix
	private := false
	for _, t := range candidates {
		names = append(names, TypeText(t, c.pkg))
		if fix := c.explicitContextFix(e, t, pattern); fix != nil {
			fixes = append(fixes, *fix)
		} else {
			private = true
		}
	}
	hint := ""
	if private {
		hint = "; use the owning package's public construction API for private candidates"
	}
	c.diags.AddCode(e.Pos, "type.context_ambiguous", "%s has several expected constructors: %s; write the constructor explicitly%s", contextText(e), strings.Join(names, ", "), hint)
	c.diags.Suggest(e.Pos, "type.context_ambiguous", e.End, fixes...)
	return Invalid
}

func (c *checker) variantContextSuggestions(e *syntax.ContextName, want Type, pattern bool) []diag.Fix {
	members := []Type{want}
	if u, ok := want.(*Union); ok {
		members = u.Members
	}
	type candidate struct {
		owner *Sealed
		name  string
	}
	var closest []candidate
	distance := 3
	for _, t := range members {
		s, ok := t.(*Sealed)
		if !ok {
			continue
		}
		for _, v := range s.Variants {
			if s.Pkg != c.pkg && !Exported(v.Name) {
				continue
			}
			d := nameDistance(e.Name, v.Name)
			if d >= 3 {
				continue
			}
			if d < distance {
				closest, distance = nil, d
			}
			if d == distance {
				closest = append(closest, candidate{s, v.Name})
			}
		}
	}
	var fixes []diag.Fix
	for _, candidate := range closest {
		name := *e
		name.Name = candidate.name
		constructors, _ := c.contextCandidates(name.Name, want)
		if len(closest) == 1 && len(constructors) == 1 {
			fixes = append(fixes, diag.Fix{Message: "use variant " + name.Name, Edits: []diag.TextEdit{{Start: e.Pos, End: e.End, Replacement: "." + name.Name}}})
		} else {
			if fix := c.explicitContextFix(&name, candidate.owner, pattern); fix != nil {
				fixes = append(fixes, *fix)
			}
		}
	}
	return fixes
}

func contextText(e *syntax.ContextName) string {
	if e.Name == "" {
		return ".{ ... }"
	}
	return "." + e.Name
}

func (c *checker) contextCandidates(name string, want Type) ([]Type, bool) {
	return contextCandidates(name, c.zonk(want))
}

func contextCandidates(name string, want Type) ([]Type, bool) {
	members := []Type{want}
	if u, ok := want.(*Union); ok {
		members = u.Members
	}
	var out, sealed []Type
	unresolved := false
	for _, t := range members {
		switch t := t.(type) {
		case *TypeParam:
			unresolved = true
		case *Record:
			if name == "" {
				out = append(out, t)
			}
		case *Sealed:
			sealed = append(sealed, t)
			if name != "" && t.Variant(name) != nil {
				out = append(out, t)
			}
		}
	}
	// A single sealed owner can explain a misspelled variant itself.
	if name != "" && len(out) == 0 && len(sealed) == 1 {
		out = sealed
	}
	return out, unresolved
}

func (c *checker) contextError(e *syntax.ContextName, code, format string, args ...any) {
	c.diags.AddCode(e.Pos, code, format, args...)
	replacement := "Type"
	if e.Name != "" {
		replacement += "." + e.Name
	}
	c.diags.Suggest(e.Pos, code, e.End, diag.Fix{
		Message: "write an explicit constructor (replace Type with its name)", RequiresInput: true,
		Edits: []diag.TextEdit{{Start: e.Pos, End: e.End, Replacement: replacement}},
	})
}

func (c *checker) explicitContextFix(e *syntax.ContextName, t Type, pattern bool) *diag.Fix {
	switch v := t.(type) {
	case *Record:
		if v.Decl.Private && v.Pkg != c.pkg {
			return nil
		}
	case *Sealed:
		if v.Pkg != c.pkg && !Exported(e.Name) {
			return nil
		}
	}
	name, visible := c.constructorTypeName(t)
	// Patterns do not accept a specialized constructor head such as
	// Option[Int].Some. A visible alias works; otherwise ask for one.
	if pattern && strings.Contains(name, "[") {
		return &diag.Fix{Message: "name or declare a type alias for " + TypeText(t, c.pkg) + " (replace Type)", RequiresInput: true,
			Edits: []diag.TextEdit{{Start: e.Pos, End: e.End, Replacement: "Type." + e.Name}}}
	}
	if !visible {
		name = "Type"
	}
	if e.Name != "" {
		name += "." + e.Name
	}
	fix := &diag.Fix{Message: "use constructor " + name, RequiresInput: !visible, Edits: []diag.TextEdit{{Start: e.Pos, End: e.End, Replacement: name}}}
	if !visible {
		fix.Message = "name or import a visible constructor for " + TypeText(t, c.pkg) + " (replace Type)"
	}
	return fix
}

// A public alias may expose a private or transitively imported nominal owner.
// Prefer an unconstrained alias, then recursively spell resolvable names.
func (c *checker) constructorTypeName(t Type) (string, bool) {
	type alias struct {
		name  string
		entry *typeEntry
	}
	var aliases []alias
	collect := func(pkg *Package, prefix string) {
		for name, e := range pkg.types {
			if e.decl.Kind == syntax.AliasType && (pkg == c.pkg || pkg == c.preludePkg || Exported(name)) {
				aliases = append(aliases, alias{prefix + name, e})
			}
		}
	}
	collect(c.pkg, "")
	collect(c.preludePkg, "")
	for prefix, pkg := range c.pkg.imports {
		collect(pkg, prefix+".")
	}
	sort.Slice(aliases, func(i, j int) bool { return aliases[i].name < aliases[j].name })
	for _, a := range aliases {
		if identical(c.resolveDecl(a.entry), t) && !c.hasFacts(&syntax.TypeExpr{Name: a.name}) {
			return a.name, true
		}
	}
	var name string
	switch v := t.(type) {
	case *Record:
		name = qualify(v.Name, v.Pkg, c.pkg)
	case *Sealed:
		name = qualify(v.Name, v.Pkg, c.pkg)
	case *Opaque:
		name = qualify(v.Name, v.Pkg, c.pkg)
	case *Resource:
		name = qualify(v.Name, v.Pkg, c.pkg)
	case *TypeParam:
		return v.Name, c.typeParams[v.Name] == v
	case *Seq:
		elem, ok := c.constructorTypeName(v.Elem)
		text := "Seq[" + elem + "]"
		if v.Effects != 0 {
			text += " uses " + v.Effects.String()
		}
		return text, ok
	case *List:
		elem, ok := c.constructorTypeName(v.Elem)
		return "List[" + elem + "]", ok
	case *Map:
		key, kok := c.constructorTypeName(v.Key)
		value, vok := c.constructorTypeName(v.Value)
		return "Map[" + key + ", " + value + "]", kok && vok
	case *Union:
		var parts []string
		for _, m := range v.Members {
			n, ok := c.constructorTypeName(m)
			if !ok {
				return "", false
			}
			if _, function := m.(*FuncType); function {
				n = "(" + n + ")"
			}
			parts = append(parts, n)
		}
		return strings.Join(parts, " | "), true
	case *FuncType:
		var parts []string
		for _, p := range v.Params {
			n, ok := c.constructorTypeName(p)
			if !ok {
				return "", false
			}
			parts = append(parts, n)
		}
		result, ok := c.constructorTypeName(v.Result)
		effects := ""
		if v.Effects != 0 {
			effects = " uses " + v.Effects.String()
		}
		return "(" + strings.Join(parts, ", ") + ")" + effects + " => " + result, ok
	default:
		name := TypeText(t, c.pkg)
		if b := basicTypes[name]; b != nil && identical(b, t) {
			return name, true
		}
		return "", false
	}
	base := c.typeNamed(name)
	if base == nil || !identical(genericBaseOrSelf(base), genericBaseOrSelf(t)) {
		return "", false
	}
	args := TypeArgs(t)
	if len(args) == 0 {
		return name, true
	}
	var parts []string
	for _, a := range args {
		n, ok := c.constructorTypeName(a)
		if !ok {
			return "", false
		}
		parts = append(parts, n)
	}
	return name + "[" + strings.Join(parts, ", ") + "]", true
}

func (c *checker) contextVariant(e *syntax.ContextName, want Type) Type {
	t := c.contextTarget(e, want, false)
	s, ok := t.(*Sealed)
	if !ok {
		return Invalid
	}
	v := c.contextVariantOf(e, s)
	if v == nil {
		return Invalid
	}
	if len(v.Fields) > 0 {
		c.diags.AddCode(e.Pos, "type.context_variant_fields", "%s.%s has fields; build it with .%s { ... }", s.Name, e.Name, e.Name)
		c.diags.Suggest(e.Pos, "type.context_variant_fields", e.End, diag.Fix{
			Message: "add the variant field braces", RequiresInput: requiredFields(v.Fields),
			Edits: []diag.TextEdit{{Start: e.End, End: e.End, Replacement: " {}"}},
		})
		return Invalid
	}
	c.info.contextVariants[e] = v
	return s
}

func requiredFields(fields []*Field) bool {
	for _, f := range fields {
		if f.Decl == nil || f.Decl.Default == nil {
			return true
		}
	}
	return false
}

func (c *checker) contextVariantOf(e *syntax.ContextName, s *Sealed) *Variant {
	v := s.Variant(e.Name)
	if v == nil {
		c.diags.AddCode(e.Pos, "type.context_variant_unknown", "%s has no variant %s", TypeText(s, c.pkg), e.Name)
		var params []*syntax.Param
		for _, v := range s.Variants {
			if v.Parent.Pkg == c.pkg || Exported(v.Name) {
				params = append(params, &syntax.Param{Name: v.Name})
			}
		}
		if name := closestParam(e.Name, params); name != "" {
			start := e.NamePos
			if start.File == "" {
				start = e.Pos
				start.Col++
			}
			c.diags.Suggest(e.Pos, "type.context_variant_unknown", e.End, diag.Fix{
				Message: "use variant " + name, Edits: []diag.TextEdit{{Start: start, End: e.End, Replacement: name}},
			})
		}
		return nil
	}
	if !c.visibleVariant(e.Pos, s, e.Name) {
		return nil
	}
	return v
}

func (c *checker) contextRecord(e *syntax.RecordLit, name *syntax.ContextName, want Type) Type {
	target := c.contextTarget(name, want, false)
	switch t := target.(type) {
	case *Record:
		if !c.recordConstruction(name.Pos, t, "construct") {
			c.skipFieldInits(e)
			return Invalid
		}
		if c.open(t) {
			return c.genericContextLit(e, genericBase(t), "", t.Name, t)
		}
		c.info.recordTargets[e] = t
		c.fieldInits(e, t.Fields, t.Name)
		return t
	case *Sealed:
		v := c.contextVariantOf(name, t)
		if v == nil {
			c.skipFieldInits(e)
			return Invalid
		}
		if c.open(t) {
			return c.genericContextLit(e, genericBase(t), name.Name, t.Name+"."+name.Name, t)
		}
		c.info.recordTargets[e] = v
		c.fieldInits(e, v.Fields, t.Name+"."+v.Name)
		return t
	}
	c.skipFieldInits(e)
	return Invalid
}

func (c *checker) contextVariantCall(e *syntax.Call, name *syntax.ContextName, want Type) Type {
	t := c.contextTarget(name, want, false)
	c.diags.AddCode(name.Pos, "type.context_variant_call", "variants use named fields, not positional calls; write %s { ... }", contextText(name))
	var valueWant Type
	if s, ok := t.(*Sealed); ok && genericBaseOrSelf(s) == c.info.Named["Option"] && name.Name == "Some" && len(e.Args) == 1 && !hasNamedArgs(e) {
		valueWant = s.Variant("Some").Fields[0].Type
		end := e.End
		end.Col--
		openEnd := e.Pos
		openEnd.Col++
		c.diags.Suggest(name.Pos, "type.context_variant_call", e.End, diag.Fix{
			Message: "use the Some value field", Edits: []diag.TextEdit{
				{Start: e.Pos, End: openEnd, Replacement: " { value: "},
				{Start: end, End: e.End, Replacement: " }"},
			},
		})
	}
	for _, a := range e.Args {
		c.exprWant(a, valueWant)
	}
	return Invalid
}

// contextNeedsType identifies context-bearing expressions whose expected
// constructor is not known yet, so other call arguments are checked first.
func (c *checker) contextNeedsType(x syntax.Expr, want Type) bool {
	x = debugSyntaxValue(x)
	want = c.zonk(want)
	if IsOption(want) && !c.optionalConstructor(x) {
		if c.open(want) {
			return !c.knownOptionalSource(x)
		}
		want = c.promotionContext(x, want)
	}
	switch x := x.(type) {
	case *syntax.ContextName:
		_, unknown := c.contextCandidates(x.Name, want)
		return want == nil || unknown
	case *syntax.RecordLit:
		if n, ok := x.Type.(*syntax.ContextName); ok {
			ts, unknown := c.contextCandidates(n.Name, want)
			if want == nil || unknown {
				return true
			}
			if len(ts) != 1 {
				return false
			} // ready to diagnose the ambiguity
			var fields []*Field
			switch t := ts[0].(type) {
			case *Record:
				fields = t.Fields
			case *Sealed:
				if v := t.Variant(n.Name); v != nil {
					fields = v.Fields
				}
			}
			for _, f := range x.Fields {
				if fd := findField(fields, f.Name); fd != nil && c.contextNeedsType(f.Value, fd.Type) {
					return true
				}
			}
			return false
		}
		// An explicit generic owner supplies its nominal head, but its
		// shorthand fields can still depend on another argument or result.
		if _, ok := x.Type.(*syntax.TypeHead); ok {
			return false
		}
		if sel, ok := x.Type.(*syntax.Selector); ok {
			if _, ok := sel.X.(*syntax.TypeHead); ok {
				return false
			}
		}
		return hasContextLiteral(x) && (want == nil || c.open(want))
	case *syntax.TupleLit:
		for i, elem := range x.Elems {
			var elemWant Type
			if tuple, ok := want.(*Record); ok && tuple.Tuple && i < len(tuple.Fields) {
				elemWant = tuple.Fields[i].Type
			}
			if c.contextNeedsType(elem, elemWant) {
				return true
			}
		}
	case *syntax.ListLit:
		var elem Type
		if t, ok := want.(*List); ok {
			elem = t.Elem
		}
		for _, e := range x.Elems {
			if c.contextNeedsType(e, elem) {
				return true
			}
		}
	case *syntax.MapLit:
		var key, value Type
		if t, ok := want.(*Map); ok {
			key, value = t.Key, t.Value
		}
		for i, e := range x.Keys {
			if c.contextNeedsType(e, key) || c.contextNeedsType(x.Values[i], value) {
				return true
			}
		}
	case *syntax.Lambda:
		var result Type
		if t, ok := want.(*FuncType); ok {
			result = t.Result
		}
		return c.contextNeedsType(x.Body, result)
	case *syntax.Block:
		return c.contextNeedsType(x.Tail, want)
	case *syntax.WithExpr:
		return c.contextNeedsType(x.Body, want)
	case *syntax.If:
		return c.contextNeedsType(x.Then, want) || c.contextNeedsType(x.Else, want)
	case *syntax.Match:
		for _, a := range x.Arms {
			if c.contextNeedsType(a.Body, want) {
				return true
			}
		}
	case *syntax.Call:
		if !hasContextLiteral(x) {
			return false
		}
		// A generic nested call can obtain its type parameters from the
		// outer expected result. Concrete declared parameters need no such help.
		if id, ok := x.Fun.(*syntax.Ident); ok {
			if local := c.lookup(id.Name); local != nil {
				if ft, ok := local.typ.(*FuncType); ok && !c.open(ft) {
					return false
				}
			} else if fn, ok := c.funcNamed(id.Name); ok && len(fn.TypeParams) == 0 {
				return false
			}
		}
		if sel, ok := x.Fun.(*syntax.Selector); ok {
			if id, ok := sel.X.(*syntax.Ident); ok {
				if local := c.lookup(id.Name); local != nil {
					if r, ok := local.typ.(*Record); ok {
						if f := findField(r.Fields, sel.Name); f != nil {
							if ft, ok := f.Type.(*FuncType); ok && !c.open(ft) {
								return false
							}
						}
					}
					if fn, _ := c.methodNamed(local.typ, sel.Name); fn != nil && len(fn.TypeParams) == 0 {
						return false
					}
				}
			}
		}
		return want == nil || c.open(want)
	}
	return false
}

func hasContextLiteral(x syntax.Expr) bool {
	switch x := x.(type) {
	case *syntax.TupleLit:
		for _, elem := range x.Elems {
			if hasContextLiteral(elem) {
				return true
			}
		}
	case *syntax.ContextName:
		return true
	case *syntax.RecordLit:
		if _, ok := x.Type.(*syntax.ContextName); ok {
			return true
		}
		for _, f := range x.Fields {
			if hasContextLiteral(f.Value) {
				return true
			}
		}
	case *syntax.ListLit:
		for _, e := range x.Elems {
			if hasContextLiteral(e) {
				return true
			}
		}
	case *syntax.MapLit:
		for i, e := range x.Keys {
			if hasContextLiteral(e) || hasContextLiteral(x.Values[i]) {
				return true
			}
		}
	case *syntax.Lambda:
		return hasContextLiteral(x.Body)
	case *syntax.WithExpr:
		return hasContextLiteral(x.Body)
	case *syntax.Block:
		if hasContextLiteral(x.Tail) {
			return true
		}
		for _, s := range x.Stmts {
			if e, ok := s.(*syntax.ExprStmt); ok && hasContextLiteral(e.X) {
				return true
			}
		}
	case *syntax.Call:
		if _, ok := x.Fun.(*syntax.ContextName); ok {
			return true
		}
		for _, a := range x.Args {
			if hasContextLiteral(a) {
				return true
			}
		}
	case *syntax.If:
		return hasContextLiteral(x.Then) || hasContextLiteral(x.Else)
	case *syntax.Match:
		for _, a := range x.Arms {
			if hasContextLiteral(a.Body) {
				return true
			}
		}
	}
	return false
}

// genericContextLit keeps the known nominal heads in a generic constructor's
// fields while allowing their type arguments to be inferred from shorthand.
func (c *checker) genericContextLit(e *syntax.RecordLit, base Type, variant, label string, want Type) Type {
	outer := c.session == nil
	if outer {
		c.session = &session{}
		defer c.closeSession()
	}
	args := make([]Type, len(typeParamsOf(base)))
	for i, tp := range typeParamsOf(base) {
		args[i] = newUnknown(tp)
	}
	inst := instantiate(base, args)
	if expected := instanceIn(want, base); expected != nil {
		c.solve(inst, expected)
	}
	var fields []*Field
	switch t := inst.(type) {
	case *Record:
		fields = t.Fields
	case *Sealed:
		v := t.Variant(variant)
		if v == nil {
			c.errorf(e.Type.Position(), "%s has no variant %s", t.Name, variant)
			c.skipFieldInits(e)
			return Invalid
		}
		if !c.visibleVariant(e.Type.Position(), t, variant) {
			c.skipFieldInits(e)
			return Invalid
		}
		fields = v.Fields
	}
	types := make([]Type, len(e.Fields))
	check := func(i int) {
		fi := e.Fields[i]
		var pw Type
		if f := findField(fields, fi.Name); f != nil {
			pw = c.zonk(f.Type)
		}
		field := findField(fields, fi.Name)
		if field != nil && field.Lazy {
			types[i] = c.fieldInitializer(fi.Value, field)
		} else {
			types[i] = c.exprWant(fi.Value, pw)
		}
		if pw != nil {
			c.solve(pw, types[i])
		}
	}
	var pending []int
	for i, fi := range e.Fields {
		field := findField(fields, fi.Name)
		if c.needsContext(fi.Value) || field != nil && c.contextNeedsType(fi.Value, field.Type) {
			pending = append(pending, i)
		} else {
			check(i)
		}
	}
	for len(pending) > 0 {
		var remaining []int
		for _, i := range pending {
			var pw Type
			if f := findField(fields, e.Fields[i].Name); f != nil {
				pw = f.Type
			}
			if c.contextNeedsType(e.Fields[i].Value, pw) {
				remaining = append(remaining, i)
			} else {
				check(i)
			}
		}
		if len(remaining) == len(pending) {
			for _, i := range remaining {
				check(i)
			}
			break
		}
		pending = remaining
	}
	inst = c.zonk(inst)
	complete := func() {
		resolved := c.zonk(inst)
		if c.open(resolved) {
			return
		}
		for i, t := range types {
			types[i] = c.zonk(t)
		}
		switch t := resolved.(type) {
		case *Record:
			c.info.recordTargets[e] = t
			c.fieldInitsTyped(e, t.Fields, label, types)
		case *Sealed:
			v := t.Variant(variant)
			c.info.recordTargets[e] = v
			c.fieldInitsTyped(e, v.Fields, label, types)
		}
	}
	if c.open(inst) {
		for _, t := range types {
			if t == Invalid {
				return Invalid
			}
		}
		report := func() {
			for _, t := range types {
				if c.open(t) {
					return
				}
			}
			var missing []string
			for i, tp := range typeParamsOf(base) {
				if c.open(args[i]) {
					missing = append(missing, tp.Name)
				}
			}
			c.errorf(e.Type.Position(), "cannot tell what %s is in this %s; use it where its type is known", strings.Join(missing, " and "), label)
		}
		if outer {
			report()
			return Invalid
		}
		c.addOrigin(inst, report)
		c.session.finish = append(c.session.finish, complete)
	} else {
		complete()
	}
	return inst
}
