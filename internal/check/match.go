package check

import (
	"go/constant"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// PatKind says what a checked pattern tests.
type PatKind int

const (
	// PatWild matches anything (`_`, or a name that binds the value).
	PatWild PatKind = iota
	// PatNever is a well-formed test disjoint from its input type.
	PatNever
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
	// type (narrowed, for PatType), and bindNode the syntax node that
	// introduced it.
	Bind     string
	BindType Type
	bindNode any
	// Var is the variable of the bound name in the typed tree, set
	// when the tree is built (see lower.go).
	Var *Var
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
	// Guard is the runtime predicate condition, evaluated after the
	// structural tests succeed. guard is its syntax before lowering.
	Guard Expr
	guard syntax.Expr
}

// HasGuard reports whether any part of the pattern tests a predicate.
func (p *Pat) HasGuard() bool {
	if p == nil {
		return false
	}
	if p.Guard != nil || p.guard != nil {
		return true
	}
	for _, f := range p.Fields {
		if f.Pat.HasGuard() {
			return true
		}
	}
	for _, e := range p.Elems {
		if e.HasGuard() {
			return true
		}
	}
	return p.Sub.HasGuard() || p.Rest.HasGuard()
}

// Guards lists the predicate conditions in the order they are tested.
func (p *Pat) Guards() []Expr {
	if p == nil {
		return nil
	}
	var out []Expr
	for _, f := range p.Fields {
		out = append(out, f.Pat.Guards()...)
	}
	out = append(out, p.Sub.Guards()...)
	for _, e := range p.Elems {
		out = append(out, e.Guards()...)
	}
	out = append(out, p.Rest.Guards()...)
	if p.Guard != nil {
		out = append(out, p.Guard)
	}
	return out
}

// PatField is one field of a destructuring pattern.
type PatField struct {
	Name string
	Pat  *Pat
}

// Narrowed is the type of the value a PatType lets through.
func (p *Pat) Narrowed() Type { return newUnion(p.Members) }

// patSources records where each name a pattern binds comes from: the
// matched subject, the path to it, and the union member the pattern
// narrowed the subject to. Payload bindings keep that member too.
func (c *checker) patSources(p *Pat, subject syntax.Expr, path string, field *Field, member Type) {
	if p == nil {
		return
	}
	if path == "" && p.Kind == PatType {
		member = p.Narrowed()
	}
	if p.Bind != "" {
		src := &patSource{Subject: subject, Path: path, Field: field, Member: member}
		c.info.patSources[p.bindNode] = src
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
		c.patSources(pf.Pat, subject, path+"."+pf.Name, findField(fields, pf.Name), member)
	}
	if p.Sub != nil {
		c.patSources(p.Sub, subject, path, field, member)
	}
}

func (c *checker) match(m *syntax.Match, want Type) Type {
	// As a statement in a loop body, the arms may give the names the loop
	// carries new values (see carried.go).
	j := c.startJoin()
	defer c.endJoin(j, m, len(m.Arms))
	st := c.expr(m.X)
	if len(m.Arms) == 0 {
		c.errorf(m.Pos, "match needs at least one arm")
		if st != Invalid {
			c.diags.Suggest(m.Pos, "type.error", m.Close, c.missingMatchFix(m, nil, st))
		}
		return Invalid
	}
	if !isValue(st) {
		if st != Invalid {
			c.errorf(m.X.Position(), "cannot match on a value of type %s", st)
		}
		for _, arm := range m.Arms {
			c.pushScope()
			// Bind the patterns' names anyway, to avoid follow-up errors.
			saved := c.diags
			c.diags = &diag.List{}
			c.pattern(arm.Pattern, Invalid)
			c.diags = saved
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
		if (want == nil || c.unbound(want)) && c.branchNeedsContext(arm.Body) {
			later = append(later, i)
		} else {
			order = append(order, i)
		}
	}
	for _, i := range append(order, later...) {
		arm := m.Arms[i]
		armTypes[i] = c.joinBranch(j, i, func() Type {
			c.nextTransparent = j != nil
			c.pushScope()
			defer c.popScope()
			p := c.pattern(arm.Pattern, st)
			if p == nil {
				ok = false
			} else {
				c.info.armPats[arm] = p
				c.patSources(p, m.X, "", nil, nil)
			}
			pats[i] = p
			armWant := want
			if (armWant == nil || c.unbound(armWant)) && len(later) > 0 && len(order) > 0 {
				armWant = armTypes[order[0]]
			}
			return c.branchExpr(j, arm.Body, armWant)
		})
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
		if !p.HasGuard() {
			valid = append(valid, p)
		}
	}
	if ok {
		if missing := missingCases(valid, st); len(missing) > 0 {
			c.errorf(m.Pos, "match is not exhaustive: missing %s", strings.Join(missing, ", "))
			c.diags.Suggest(m.Pos, "type.error", m.Close, c.missingMatchFix(m, valid, st))
		}
	}
	if c.info.selectMatches[m] && len(armTypes) > 0 {
		// A select's value is its arms' joined, and Cancelled (the last
		// arm, taken when a scope was cancelled).
		arms := c.unify(m.Pos, "select arms have", armTypes[:len(armTypes)-1], want)
		switch arms {
		case Invalid:
			return Invalid
		case Never:
			return armTypes[len(armTypes)-1]
		}
		return newUnion([]Type{arms, armTypes[len(armTypes)-1]})
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
		if c.patternTest {
			if u, yes := st.(*Union); yes {
				t := c.expr(p.Value)
				if t == Invalid {
					return nil
				}
				if !containsMember(u, t) {
					return &Pat{Kind: PatNever, Type: st}
				}
				return &Pat{Kind: PatType, Type: st, Members: []Type{t}, Sub: &Pat{Kind: PatLit, Type: t, Lit: c.literalValue(p.Value)}}
			}
		}
		if _, isUnion := st.(*Union); isUnion {
			c.errorf(p.Pos, "cannot match a literal against a value of type %s; match its type first (as in n: Int)", st)
			return nil
		}
		lt := c.exprWant(p.Value, st)
		if lt == Invalid {
			return nil
		}
		if !identical(lt, st) {
			if c.patternTest {
				return &Pat{Kind: PatNever, Type: st}
			}
			c.errorf(p.Pos, "cannot match a %s literal against a value of type %s", lt, st)
			return nil
		}
		return &Pat{Kind: PatLit, Type: st, Lit: c.literalValue(p.Value)}

	case *syntax.TypePat:
		if c.patternTest && p.Name != "" && p.Name != "_" {
			c.errorf(p.Pos, "is patterns cannot bind names; write the type without a binding")
			return nil
		}
		unboundType := p.Name == ""
		if p.Name == "" {
			copy := *p
			copy.Name = "_patternValue" + strconv.Itoa(p.Pos.Line) + "_" + strconv.Itoa(p.Pos.Col)
			p = &copy
		}
		var t Type
		if unboundType && p.Type.Name != "" && len(p.Type.Args) == 0 {
			if base := c.typeNamed(p.Type.Name); base != nil && genericBase(base) == base {
				t = instanceIn(st, base)
			}
		}
		if t == nil {
			t = c.resolveType(p.Type)
		} else {
			c.info.writtenTypes[p.Type] = t
			c.noteSourceType(p.Type.Pos, p.Type.Name)
		}
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
		if pat.Kind == PatNever {
			pat.BindType = t
			c.lookup(p.Name).typ = t
		}
		if c.nestedPatternFacts(p.Type) {
			c.whereReported(p.Type)
			c.errorf(p.Type.Pos, "nested constraints in type patterns are not supported yet; match the base type and then guard with the predicate")
			return nil
		}
		before := c.diags.Len()
		cons := c.constraintsOf(p.Type, pat.BindType, c.paramScope())
		valid := c.diags.Len() == before
		for _, con := range cons {
			if con.Path != "" {
				c.errorf(con.Pos, "nested constraints in type patterns are not supported yet; match the base type and then guard with the predicate")
				valid = false
				continue
			}
			guard := c.patternGuard(con, p)
			if guard != nil {
				if pat.guard == nil {
					pat.guard = guard
				} else {
					pat.guard = c.patternLogical(pat.guard, guard, syntax.AndAnd)
				}
			} else {
				valid = false
			}
		}
		// Guards run in their own binding scope in generated Go. Whether
		// the arm's separate binding is used depends on its body alone.
		c.lookup(p.Name).used = false
		if !valid {
			return nil
		}
		return pat

	case *syntax.TuplePat:
		if union, ok := st.(*Union); ok {
			var candidates []*Record
			for _, member := range union.Members {
				if rec, ok := member.(*Record); ok && rec.Tuple && len(rec.Fields) == len(p.Elems) && c.tuplePatternCompatible(p, rec) {
					candidates = append(candidates, rec)
				}
			}
			if len(candidates) == 1 {
				inner := c.pattern(p, candidates[0])
				if inner == nil {
					return nil
				}
				return &Pat{Kind: PatType, Type: st, Members: []Type{candidates[0]}, Sub: inner}
			}
			if len(candidates) > 1 {
				c.errorf(p.Pos, "tuple pattern matches several tuple types in %s; annotate its elements to select one", st)
				return nil
			}
		}
		rec, ok := st.(*Record)
		if !ok || !rec.Tuple || len(rec.Fields) != len(p.Elems) {
			if c.patternTest {
				out := &Pat{Kind: PatNever, Type: st}
				for _, elem := range p.Elems {
					child := c.disjointPattern(elem)
					if child == nil {
						return nil
					}
					out.Elems = append(out.Elems, child)
				}
				return out
			}
			c.errorf(p.Pos, "cannot match a %d-element tuple pattern against %s", len(p.Elems), st)
			return nil
		}
		out := &Pat{Kind: PatRecord, Type: rec}
		for i, elem := range p.Elems {
			pat := c.pattern(elem, rec.Fields[i].Type)
			if pat == nil {
				return nil
			}
			out.Fields = append(out.Fields, &PatField{Name: rec.Fields[i].Name, Pat: pat})
		}
		return out
	case *syntax.VariantPat:
		return c.namePattern(p, st)

	case *syntax.ListPat:
		lt, ok := st.(*List)
		if !ok {
			if c.patternTest && st != Invalid {
				if u, yes := st.(*Union); yes {
					var members []Type
					for _, m := range u.Members {
						if _, yes := m.(*List); yes {
							members = append(members, m)
						}
					}
					if len(members) > 1 {
						c.errorf(p.Pos, "a list pattern needs one element type; test a concrete list type first")
						return nil
					}
					if len(members) == 1 {
						sub := c.pattern(p, members[0])
						if sub == nil {
							return nil
						}
						return &Pat{Kind: PatType, Type: st, Members: members, Sub: sub}
					}
				}
				impossible := &Pat{Kind: PatNever, Type: st}
				for _, elem := range p.Elems {
					checked := c.disjointPattern(elem)
					if checked == nil {
						return nil
					}
					impossible.Elems = append(impossible.Elems, checked)
				}
				return impossible
			}
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
		if c.patternTest && p.Rest != "" {
			c.errorf(p.RestPos, "is patterns cannot bind the rest of a list; write ... without a name")
			return nil
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

func (c *checker) tuplePatternCompatible(p *syntax.TuplePat, rec *Record) bool {
	for i, elem := range p.Elems {
		if !c.tupleElementCompatible(elem, rec.Fields[i].Type) {
			return false
		}
	}
	return true
}

func (c *checker) tupleElementCompatible(elem syntax.Pattern, field Type) bool {
	if typed, ok := elem.(*syntax.TypePat); ok {
		var target Type
		if c.patternTest && typed.Type.Name != "" && len(typed.Type.Args) == 0 {
			if base := c.typeNamed(typed.Type.Name); base != nil && genericBase(base) == base {
				target = instanceIn(field, base)
			}
		}
		if target == nil {
			target = c.resolveType(typed.Type)
		}
		if c.patternTest {
			return patternTypesOverlap(field, target)
		}
		return assignable(target, field)
	}
	if union, ok := field.(*Union); ok {
		for _, member := range union.Members {
			if c.tupleElementCompatible(elem, member) {
				return true
			}
		}
		return false
	}
	switch elem := elem.(type) {
	case *syntax.LitPat:
		return tupleLiteralCompatible(elem.Value, field)
	case *syntax.TuplePat:
		nested, ok := field.(*Record)
		return ok && nested.Tuple && len(nested.Fields) == len(elem.Elems) && c.tuplePatternCompatible(elem, nested)
	case *syntax.VariantPat:
		if elem.Context {
			if owner, ok := field.(*Sealed); ok {
				return owner.Variant(elem.Path[0]) != nil
			}
			_, open := field.(*TypeParam)
			return open
		}
		if len(elem.Path) > 0 {
			owner := c.typeNamed(elem.Path[0])
			if owner != nil {
				if base := genericBase(owner); base == owner {
					return instanceIn(field, base) != nil
				}
				return assignable(owner, field)
			}
		}
	}
	return true
}

func tupleLiteralCompatible(value syntax.Expr, typ Type) bool {
	if union, ok := typ.(*Union); ok {
		for _, member := range union.Members {
			if tupleLiteralCompatible(value, member) {
				return true
			}
		}
		return false
	}
	switch value := value.(type) {
	case *syntax.BoolLit:
		return typ == Bool
	case *syntax.StringLit:
		return typ == String
	case *syntax.RuneLit:
		return typ == Rune
	case *syntax.IntLit:
		return IsInteger(typ)
	case *syntax.FloatLit:
		return IsFloat(typ)
	case *syntax.Unary:
		return tupleLiteralCompatible(value.X, typ)
	}
	return false
}

// nestedPatternFacts checks the written arguments too: a phantom generic
// argument has no field path, so constraintsOf cannot identify it later.
func (c *checker) nestedPatternFacts(t *syntax.TypeExpr) bool {
	seen := map[*syntax.TypeDecl]bool{}
	var nested func(*syntax.TypeExpr) bool
	nested = func(t *syntax.TypeExpr) bool {
		for _, m := range t.Union {
			if nested(m) {
				return true
			}
		}
		for _, a := range append(append([]*syntax.TypeExpr{}, t.Args...), t.Tuple...) {
			if c.hasFacts(a) {
				return true
			}
		}
		if t.Union == nil && t.Func == nil && len(t.Args) == 0 && c.typeParams[t.Name] == nil {
			if e := c.lookupType(t.Name); e != nil && e.decl.Kind == syntax.AliasType && !seen[e.decl] {
				seen[e.decl] = true
				savedPkg, savedParams := c.pkg, c.typeParams
				c.pkg, c.typeParams = e.pkg, nil
				bad := nested(e.decl.Alias)
				c.pkg, c.typeParams = savedPkg, savedParams
				return bad
			}
		}
		return false
	}
	return nested(t)
}

// patternGuard checks a real predicate call, so its generic arguments,
// dependencies and parameter requirements follow the normal call rules.
func (c *checker) patternGuard(con *Constraint, p *syntax.TypePat) syntax.Expr {
	if con.Or != nil {
		var out syntax.Expr
		for _, alt := range con.Or {
			x := c.patternGuard(alt, p)
			if x == nil {
				return nil
			}
			if out == nil {
				out = x
			} else {
				out = c.patternLogical(out, x, syntax.OrOr)
			}
		}
		return out
	}
	args := []syntax.Expr{&syntax.Ident{Pos: p.Pos, Name: p.Name}}
	for _, a := range con.Args {
		args = append(args, a.source)
	}
	call := &syntax.Call{Pos: p.Pos, Args: args}
	if con.PredParam != "" {
		ft := c.paramScope()[con.PredParam].(*FuncType)
		if ft.Effects != 0 {
			c.errorf(con.Pos, "predicate parameter %s in a type pattern must declare uses nothing", con.PredParam)
			return nil
		}
		call.Fun = &syntax.Ident{Pos: con.Pos, Name: con.PredParam}
		if c.expr(call) == Invalid {
			return nil
		}
	} else {
		call.Fun = &syntax.Ident{Pos: con.Pos, Name: con.Pred.Decl.Name}
		if c.record(call, c.callFunc(call, con.Pred.QualifiedName(c.pkg), con.Pred, args, nil, nil, Bool)) == Invalid {
			return nil
		}
	}
	return call
}

func (c *checker) patternLogical(x, y syntax.Expr, op syntax.Kind) syntax.Expr {
	out := &syntax.Binary{Pos: x.Position(), Op: op, X: x, Y: y}
	c.record(out, Bool)
	return out
}

// typePattern is the pattern matching the values of type t within a
// value of type st: everything, if t is st, or some members of a union.
func (c *checker) typePattern(t, st Type, pos diag.Pos) *Pat {
	if target, ok := t.(*Seq); ok {
		if source, ok := st.(*Seq); ok && assignable(source, target) {
			return &Pat{Kind: PatWild, Type: st}
		}
	}
	if c.patternTest {
		return c.testTypePattern(t, st, pos)
	}
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
	for i, m := range members {
		if target, ok := m.(*Seq); ok {
			for _, source := range u.Members {
				if source, ok := source.(*Seq); ok && assignable(source, target) {
					members[i] = source
					break
				}
			}
			m = members[i]
		}
		if !containsMember(u, m) {
			c.errorf(pos, "%s is not one of the types in %s", m, st)
			return nil
		}
	}
	return &Pat{Kind: PatType, Type: st, Members: members}
}

// bindPat makes pat bind name to the value it matches.
func (c *checker) bindPat(pat *Pat, name string, pos diag.Pos, node any) {
	pat.Bind, pat.bindNode, pat.BindType = name, node, pat.Type
	if pat.Kind == PatType {
		pat.BindType = pat.Narrowed()
	}
	if c.tupleBindingMode {
		c.bindRebinding(name, pos, pat.BindType, node)
	} else {
		c.bind(name, pos, pat.BindType, node)
	}
}

// namePattern checks a pattern written as a name: `Shape.Circle { r }`,
// `Option.None`, `NotFound`, `User { name }`, or a name to bind.
func (c *checker) namePattern(p *syntax.VariantPat, st Type) *Pat {
	if p.Context {
		name := &syntax.ContextName{Pos: p.Pos, End: p.End, NamePos: p.NamePos, Name: p.Path[0]}
		owner, ok := c.contextTarget(name, st, true).(*Sealed)
		if !ok {
			return nil
		}
		v := c.contextVariantOf(name, owner)
		if v == nil {
			return nil
		}
		inner := &Pat{Kind: PatVariant, Type: owner, Variant: v}
		if !c.variantPatterns(inner, p, v, owner.Name+"."+v.Name) {
			return nil
		}
		return c.within(inner, owner, st, p.Pos)
	}
	switch len(p.Path) {
	case 1:
		name := p.Path[0]
		t := c.typeNamed(name)
		c.noteSourceType(p.Pos, name)
		if t == nil {
			if c.patternTest {
				c.errorf(p.Pos, "is patterns cannot bind names; %s must name a type", name)
				return nil
			}
			if p.Braces || p.Positional {
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
		if p.Positional {
			c.errorf(p.Pos, "%s is a type, not a positional variant", name)
			return nil
		}
		if !p.Braces {
			return c.typePattern(t, st, p.Pos)
		}
		rec, isRec := t.(*Record)
		if !isRec {
			c.errorf(p.Pos, "%s is not a record, so it has no fields to match", name)
			return nil
		}
		if c.storeFields(p.Pos, rec) {
			return nil
		}
		inner := &Pat{Kind: PatRecord, Type: rec}
		if !c.fieldPatterns(inner, p.Fields, rec.Fields, name) {
			return nil
		}
		return c.within(inner, rec, st, p.Pos)
	case 2:
		owner, name := p.Path[0], p.Path[1]
		c.noteSourceType(p.Pos, owner)
		var v *Variant
		if p.Owner != nil {
			if specialized, ok := c.resolveType(p.Owner).(*Sealed); ok {
				v = c.specializedVariant(p.Pos, specialized, name)
			}
		} else {
			v = c.variantRef(p.Pos, owner, name, st)
		}
		if v == nil {
			return nil
		}
		inner := &Pat{Kind: PatVariant, Type: v.Parent, Variant: v}
		if !c.variantPatterns(inner, p, v, owner+"."+name) {
			return nil
		}
		if !identical(v.Parent, st) {
			if u, ok := st.(*Union); !ok || !containsMember(u, v.Parent) {
				if c.patternTest {
					return &Pat{Kind: PatNever, Type: st, Sub: inner}
				}
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
			if c.patternTest {
				c.errorf(fp.Pos, "is patterns cannot bind fields; write %s: _ to ignore the value", fp.Field)
				ok = false
				continue
			}
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
	if v, ok := c.info.consts[e]; ok {
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

// testTypePattern checks known members and keeps dynamic type-parameter tests.
// Effect annotations disappear in Go; only safe widening can be tested.
func (c *checker) testTypePattern(t, st Type, pos diag.Pos) *Pat {
	targets, sources := []Type{t}, []Type{st}
	if u, ok := t.(*Union); ok {
		targets = u.Members
	}
	if u, ok := st.(*Union); ok {
		sources = u.Members
	}
	var overlap []Type
	all := true
	for _, source := range sources {
		accepted := false
		for _, target := range targets {
			if patternTypeAccepts(source, target) {
				accepted = true
				overlap = append(overlap, source)
				break
			}
		}
		if accepted {
			continue
		}
		all = false
		for _, target := range targets {
			if hasTypeParam(target) {
				c.errorf(pos, "pattern target %s must have concrete type arguments; runtime tests cannot recover generic unions or effect annotations", target)
				return nil
			}
			if hasTypeParam(source) {
				if patternErasedType(target) || patternErasedType(source) {
					c.errorf(pos, "pattern test cannot check erased effects or nested unions through a type parameter")
					return nil
				}
				overlap = append(overlap, target)
			} else if patternSameRepresentation(target, source) {
				c.errorf(pos, "pattern type %s cannot be distinguished from %s at runtime", target, source)
				return nil
			}
		}
	}
	if all {
		return &Pat{Kind: PatWild, Type: st}
	}
	if len(overlap) == 0 {
		return &Pat{Kind: PatNever, Type: st}
	}
	unique := overlap[:0]
	for _, member := range overlap {
		duplicate := false
		for _, existing := range unique {
			duplicate = duplicate || identical(member, existing)
		}
		if !duplicate {
			unique = append(unique, member)
		}
	}
	return &Pat{Kind: PatType, Type: st, Members: unique}
}

func patternTypeAccepts(source, target Type) bool {
	if identical(source, target) {
		return true
	}
	if target, ok := target.(*Seq); ok {
		source, ok := source.(*Seq)
		return ok && assignable(source, target)
	}
	if target, ok := target.(*FuncType); ok {
		source, ok := source.(*FuncType)
		return ok && sameSignature(source, target) && source.Effects&^target.Effects == 0
	}
	return false
}

func patternErasedType(t Type) bool {
	switch t := t.(type) {
	case *FuncType, *Seq, *Union:
		return true
	case *List:
		return patternErasedType(t.Elem)
	case *Map:
		return patternErasedType(t.Key) || patternErasedType(t.Value)
	case *Record:
		for _, arg := range t.Args {
			if patternErasedType(arg) {
				return true
			}
		}
	case *Sealed:
		for _, arg := range t.Args {
			if patternErasedType(arg) {
				return true
			}
		}
	}
	return false
}
func patternSameRepresentation(a, b Type) bool {
	if as, ok := a.(*Seq); ok {
		bs, ok := b.(*Seq)
		return ok && patternSameRepresentation(as.Elem, bs.Elem)
	}
	if al, ok := a.(*List); ok {
		bl, ok := b.(*List)
		return ok && patternSameRepresentation(al.Elem, bl.Elem)
	}
	if am, ok := a.(*Map); ok {
		bm, ok := b.(*Map)
		return ok && patternSameRepresentation(am.Key, bm.Key) && patternSameRepresentation(am.Value, bm.Value)
	}
	return parallelSameRepresentation(a, b)
}

// Candidate selection must keep tuple members that a broader type test can
// accept. The checked pattern subsequently validates erased distinctions.
func patternTypesOverlap(source, target Type) bool {
	if target == Invalid {
		return false
	}
	if u, ok := source.(*Union); ok {
		for _, m := range u.Members {
			if patternTypesOverlap(m, target) {
				return true
			}
		}
		return false
	}
	if u, ok := target.(*Union); ok {
		for _, m := range u.Members {
			if patternTypesOverlap(source, m) {
				return true
			}
		}
		return false
	}
	return hasTypeParam(source) || hasTypeParam(target) || patternTypeAccepts(source, target) || patternSameRepresentation(source, target)
}
