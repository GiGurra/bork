package check

import (
	"go/constant"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// A Constraint is one predicate of a `where` clause, applied to the
// constrained value: `between(1, 65535)` on a port. Constraints are not
// part of a value's Type: ordinary type checking ignores them, and the
// facts pass (facts.go) checks them afterwards.
//
// A constraint with alternatives, `positive or zero`, has a nil Pred and
// the alternatives in Or; at least one of them must hold.
//
// A constraint inside a type argument applies at a path into the value:
// ".[]" for every element of a List, ".value" for the value in an
// Option.Some (`List[Int where positive]` has Path ".[]").
//
// The predicate can also be a parameter of the function, a function
// value (`List[T where keep]`); PredParam names it, and Pred is nil.
type Constraint struct {
	Pred      *Func
	PredParam string
	Args      []CArg
	Pos       diag.Pos
	Or        []*Constraint
	Path      string
	Pkg       *Package // scope in which predicate dictionaries are resolved
}

// CArg is an argument of a constraint's predicate, after the value
// itself: a constant, a function parameter, or a sibling field name
// (`to: AccountId where notEqual(from)`).
type CArg struct {
	Const   constant.Value // nil for a parameter or sibling
	Param   string
	Type    Type        // argument type, including inference from sibling fields
	Sibling bool        // argument names a field of the enclosing record or variant
	source  syntax.Expr // the argument as written, for runtime pattern guards
}

func (a CArg) String() string {
	if a.Const == nil {
		return a.Param
	}
	if a.Const.Kind() == constant.String {
		return strconv.Quote(constant.StringVal(a.Const))
	}
	return a.Const.String()
}

// String renders the constraint as written: `positive`, `between(1, 5)`,
// `positive or zero`.
func (c *Constraint) String() string { return c.Text(nil) }

// Text renders the constraint as code in package from would write it.
func (c *Constraint) Text(from *Package) string {
	if c.Or != nil {
		alts := make([]string, len(c.Or))
		for i, a := range c.Or {
			alts[i] = a.Text(from)
		}
		return strings.Join(alts, " or ")
	}
	if c.PredParam != "" {
		return c.PredParam
	}
	if len(c.Args) == 0 {
		return c.Pred.QualifiedName(from)
	}
	args := make([]string, len(c.Args))
	for i, a := range c.Args {
		args[i] = a.String()
	}
	return c.Pred.QualifiedName(from) + "(" + strings.Join(args, ", ") + ")"
}

// MemberConstraints are the constraints on the values of one member of a
// function's result (the whole result, if it is not a union).
type MemberConstraints struct {
	Type        Type
	Constraints []*Constraint
}

// resolveConstraints resolves every where clause in the package, once
// all predicates are declared.
func (c *checker) resolveConstraints(files []*syntax.File) {
	for _, t := range c.info.TypeOrder {
		switch t := t.(type) {
		case *Record:
			c.pkg, c.inPrelude = t.Pkg, t.Prelude
			c.fieldConstraints(t.Fields, t.Decl.Fields)
			t.Constraints = c.typeConstraints(t.Decl.Where, t)
		case *Sealed:
			c.pkg, c.inPrelude = t.Pkg, t.Prelude
			t.Constraints = c.typeConstraints(t.Decl.Where, t)
			for _, v := range t.Variants {
				decls := t.Decl.Variants[v.Index].Fields
				if v.Positional {
					for _, field := range v.Fields {
						decls = append(decls, field.Decl)
					}
				}
				c.fieldConstraints(v.Fields, decls)
				v.Constraints = c.typeConstraints(t.Decl.Variants[v.Index].Where, t)
			}
		}
	}
	// Instances made while resolving type declarations precede field facts.
	// Refresh their constraint metadata without replacing their field identities.
	for _, t := range c.info.TypeOrder {
		switch base := t.(type) {
		case *Record:
			for _, t := range base.insts.byKey {
				inst := t.(*Record)
				bound := bindParams(base.TypeParams, inst.Args)
				inst.Constraints = substConstraints(base.Constraints, bound)
				for i, field := range inst.Fields {
					field.Constraints = substConstraints(base.Fields[i].Constraints, bound)
				}
			}
		case *Sealed:
			for _, t := range base.insts.byKey {
				inst := t.(*Sealed)
				bound := bindParams(base.TypeParams, inst.Args)
				inst.Constraints = substConstraints(base.Constraints, bound)
				for i, variant := range inst.Variants {
					variant.Constraints = substConstraints(base.Variants[i].Constraints, bound)
					for j, field := range variant.Fields {
						field.Constraints = substConstraints(base.Variants[i].Fields[j].Constraints, bound)
					}
				}
			}
		}
	}
	c.inPrelude = false
	for _, f := range files {
		c.inFile(f)
		for _, td := range f.Types {
			if e := c.pkg.types[td.Name]; e != nil && e.decl == td && td.Kind == syntax.AliasType {
				c.aliasConstraints(e)
			}
		}
	}
	for _, f := range files {
		for _, fd := range f.Funcs {
			fn := c.info.FuncOf[fd]
			if fn == nil {
				continue
			}
			c.resolveFunctionConstraints(fn)
		}
	}
	for _, fn := range c.info.ExpandedFunctions {
		c.resolveFunctionConstraints(fn)
	}
}

func (c *checker) resolveFunctionConstraints(fn *Func) {
	fd := fn.Decl
	c.pkg = fn.Pkg
	if fn.TemplatePkg != nil {
		c.pkg = fn.TemplatePkg
	}
	c.inPrelude = fn.Prelude
	scope := map[string]Type{}
	for i, p := range fd.Params {
		scope[p.Name] = fn.Params[i]
	}
	positions := map[string]diag.Pos{}
	for _, p := range fd.Params {
		positions[p.Name] = p.Pos
	}
	c.useTypeParams(fn)
	c.fieldWhere = fd.Constructor != nil
	fn.ParamConstraints = make([][]*Constraint, len(fd.Params))
	for i, p := range fd.Params {
		fn.ParamConstraints[i] = c.constraintsOf(p.Type, fn.Params[i], scope)
		c.noteConstraintSources(fn.ParamConstraints[i], positions)
	}
	c.fieldWhere = false
	fn.ResultConstraints = c.memberConstraints(fd.Result, fn.Result, scope)
	for _, member := range fn.ResultConstraints {
		c.noteConstraintSources(member.Constraints, positions)
	}
	c.useTypeParams(nil)
	c.inPrelude = false
}

func (c *checker) typeConstraints(refs []*syntax.PredRef, typ Type) []*Constraint {
	var out []*Constraint
	for _, ref := range refs {
		if containsOpaque(typ, map[Type]bool{}) {
			c.bindErr(ref.Pos, "facts cannot apply to %s, which holds a Go value that can change", typ)
			continue
		}
		if con := c.constraint(ref, typ, nil); con != nil {
			out = append(out, con)
		}
	}
	return out
}

// TypeConstraints are the guarantees carried by every valid value of typ.
func TypeConstraints(typ Type) []*Constraint {
	switch typ := typ.(type) {
	case *Record:
		return typ.Constraints
	case *Sealed:
		return typ.Constraints
	}
	return nil
}

func (c *checker) fieldConstraints(fields []*Field, decls []*syntax.FieldDecl) {
	c.fieldWhere = true
	defer func() { c.fieldWhere = false }()
	scope := map[string]Type{}
	positions := map[string]diag.Pos{}
	for _, f := range fields {
		if f.Decl != nil {
			positions[f.Name] = f.Decl.Pos
		}
		scope[f.Name] = f.Type
	}
	for _, f := range fields {
		for _, fd := range decls {
			if fd.Name == f.Name {
				f.Constraints = c.constraintsOf(fd.Type, f.Type, scope)
				c.noteConstraintSources(f.Constraints, positions)
				var mark func(*Constraint)
				mark = func(con *Constraint) {
					for i := range con.Args {
						con.Args[i].Sibling = con.Args[i].Const == nil
					}
					for _, alt := range con.Or {
						mark(alt)
					}
				}
				for _, con := range f.Constraints {
					mark(con)
				}
			}
		}
	}
}

// memberConstraints splits the constraints of a written result type by
// union member.
func (c *checker) memberConstraints(t *syntax.TypeExpr, typ Type, scope map[string]Type) []MemberConstraints {
	if t == nil {
		return nil
	}
	if t.Union == nil || len(t.Where) > 0 {
		if cs := c.constraintsOf(t, typ, scope); len(cs) > 0 {
			return []MemberConstraints{{Type: typ, Constraints: cs}}
		}
		return nil
	}
	var out []MemberConstraints
	for _, m := range t.Union {
		mt := c.resolveType(m)
		if cs := c.constraintsOf(m, mt, scope); len(cs) > 0 {
			out = append(out, MemberConstraints{Type: mt, Constraints: cs})
		}
	}
	return out
}

// constraintsOf resolves the where clause of a written type whose
// resolved type is typ, including those of a constrained alias it names.
// Predicate arguments may name the parameters in scope.
func (c *checker) constraintsOf(t *syntax.TypeExpr, typ Type, scope map[string]Type) []*Constraint {
	if t == nil {
		return nil
	}
	if typ == Invalid {
		c.whereReported(t) // the type is already an error
		return nil
	}
	c.appliedWhere[t] = true
	if _, ok := typ.(*Seq); ok && len(t.Where) > 0 {
		c.errorf(t.Pos, "predicates on a Seq are not supported; constrain its element type instead")
		return nil
	}
	if len(t.Where) > 0 && containsOpaque(typ, map[Type]bool{}) {
		c.bindErr(t.Pos, "facts cannot apply to %s, which holds a Go value that can change", typ)
		return nil
	}
	out := append([]*Constraint(nil), c.info.shapeTypeFacts[t]...)
	if tp := c.typeParams[t.Name]; tp != nil && t.Union == nil && t.Func == nil && t.Tuple == nil && len(t.Args) == 0 {
		out = append(out, c.aliasFacts[tp]...)
	}
	alias := false
	if t.Union == nil && t.Func == nil && t.Tuple == nil && c.typeParams[t.Name] == nil {
		if e := c.lookupType(t.Name); e != nil && e.decl.Kind == syntax.AliasType {
			if len(e.params) > 0 {
				out = append(out, c.genericAliasConstraints(e, t, typ, scope)...)
				alias = true
			} else if _, isParam := typ.(*TypeParam); !isParam {
				out = append(out, c.aliasConstraints(e)...)
			}
		}
	}
	for _, ref := range t.Where {
		if con := c.constraint(ref, typ, scope); con != nil {
			out = append(out, con)
		}
	}
	if alias {
		return out
	}
	// Constraints inside type arguments apply to the elements.
	inner := func(cons []*Constraint, step string) {
		for _, con := range cons {
			cp := *con
			cp.Path = step + con.Path
			out = append(out, &cp)
		}
	}
	if tuple, ok := typ.(*Record); ok && tuple.Tuple && len(t.Tuple) == len(tuple.Fields) {
		for i, elem := range t.Tuple {
			cons := c.constraintsOf(elem, tuple.Fields[i].Type, scope)
			tuple.Fields[i].Constraints = cons
			inner(cons, "."+tuple.Fields[i].Name)
		}
	}
	if tt, ok := typ.(*Seq); ok && len(t.Args) == 1 {
		inner(c.constraintsOf(t.Args[0], tt.Elem, scope), ".[]")
	}
	if tt, ok := typ.(*List); ok && len(t.Args) == 1 {
		inner(c.constraintsOf(t.Args[0], tt.Elem, scope), ".[]")
	}
	// In a generic type, a type argument's constraints apply to the
	// fields declared with that parameter: `Option[Int where positive]`
	// to the Some's value. A field that holds the parameter inside
	// another type could not be checked.
	if base := genericBase(typ); base != nil && len(t.Args) == len(typeParamsOf(base)) {
		args := TypeArgs(typ)
		for i, tp := range typeParamsOf(base) {
			// Tuple annotations may record their field facts. Generic
			// instances are shared, so annotate a copy of the argument.
			cons := c.constraintsOf(t.Args[i], subst(args[i], nil), scope)
			if len(cons) == 0 {
				continue
			}
			if f := fieldHoldingInside(base, tp); f != nil && isPreludeType(base) {
				c.errorf(t.Args[i].Pos, "facts on the type argument of %s are not supported yet, so they would not be checked", t.Name)
				continue
			} else if f != nil {
				c.errorf(t.Args[i].Pos, "facts on the type argument %s of %s are not supported yet, so they would not be checked: its field %s holds %s inside %s",
					tp.Name, t.Name, f.Name, tp.Name, f.Type)
				continue
			}
			for _, path := range fieldPathsOf(base, tp) {
				inner(cons, path)
			}
		}
	}
	return out
}

func isPreludeType(t Type) bool {
	switch t := t.(type) {
	case *Record:
		return t.Prelude
	case *Sealed:
		return t.Prelude
	}
	return false
}

// fieldHoldingInside returns a field of the generic type base (in any
// variant) whose type holds the type parameter tp but is not tp itself,
// or nil if there is none.
func fieldHoldingInside(base Type, tp *TypeParam) *Field {
	var fields []*Field
	switch b := base.(type) {
	case *Record:
		fields = b.Fields
	case *Sealed:
		for _, v := range b.Variants {
			fields = append(fields, v.Fields...)
		}
	}
	for _, f := range fields {
		if f.Type != Type(tp) && mentions(f.Type, tp) {
			return f
		}
	}
	return nil
}

// fieldPathsOf lists the fields of a generic type (in any variant)
// declared with exactly the type parameter tp.
func fieldPathsOf(base Type, tp *TypeParam) []string {
	var fields []*Field
	switch b := base.(type) {
	case *Record:
		fields = b.Fields
	case *Sealed:
		for _, v := range b.Variants {
			fields = append(fields, v.Fields...)
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, f := range fields {
		if f.Type == Type(tp) && !seen[f.Name] {
			seen[f.Name] = true
			out = append(out, "."+f.Name)
		}
	}
	return out
}

func (c *checker) aliasConstraints(e *typeEntry) []*Constraint {
	if !e.constraintsDone {
		e.constraintsDone = true
		savedPkg, savedPrelude, savedParams, savedFacts, savedTypes := c.pkg, c.inPrelude, c.typeParams, c.aliasFacts, c.aliasTypes
		c.pkg, c.inPrelude = e.pkg, e.prelude
		c.typeParams, c.aliasFacts, c.aliasTypes = map[string]*TypeParam{}, nil, nil
		for _, tp := range e.params {
			c.typeParams[tp.Name] = tp
		}
		e.constraints = c.constraintsOf(e.decl.Alias, e.typ, nil)
		c.pkg, c.inPrelude, c.typeParams, c.aliasFacts, c.aliasTypes = savedPkg, savedPrelude, savedParams, savedFacts, savedTypes
	}
	return e.constraints
}

// Expand argument facts in the alias's lexical scope, applying the ordinary
// position checks to its target rather than to the alias's argument list.
func (c *checker) genericAliasConstraints(e *typeEntry, written *syntax.TypeExpr, typ Type, scope map[string]Type) []*Constraint {
	if len(written.Args) != len(e.params) || typ == Invalid {
		return nil
	}
	facts := map[*TypeParam][]*Constraint{}
	types := map[*TypeParam]Type{}
	for i, arg := range written.Args {
		argType := c.info.writtenTypes[arg]
		if argType == nil {
			argType = c.resolveType(arg)
		}
		// Nested argument annotations belong to the generic declaration.
		// Specialize them before attaching tuple facts to avoid mutating it.
		argType = subst(argType, c.aliasTypes)
		types[e.params[i]] = argType
		facts[e.params[i]] = c.constraintsOf(arg, argType, scope)
	}
	savedPkg, savedPrelude, savedParams, savedFacts, savedTypes, savedApplied := c.pkg, c.inPrelude, c.typeParams, c.aliasFacts, c.aliasTypes, c.appliedWhere
	c.pkg, c.inPrelude, c.aliasFacts, c.aliasTypes = e.pkg, e.prelude, facts, types
	c.typeParams, c.appliedWhere = map[string]*TypeParam{}, map[*syntax.TypeExpr]bool{}
	for _, tp := range e.params {
		c.typeParams[tp.Name] = tp
	}
	out := c.constraintsOf(e.decl.Alias, typ, scope)
	c.unappliedIn(e.decl.Alias, "in a type alias")
	c.pkg, c.inPrelude, c.typeParams, c.aliasFacts, c.aliasTypes, c.appliedWhere = savedPkg, savedPrelude, savedParams, savedFacts, savedTypes, savedApplied
	return out
}

func (c *checker) constraint(ref *syntax.PredRef, subject Type, scope map[string]Type) *Constraint {
	if len(ref.Or) == 0 {
		return c.constraintAtom(ref, subject, scope)
	}
	con := &Constraint{Pos: ref.Pos}
	first := *ref
	first.Or = nil
	for _, alt := range append([]*syntax.PredRef{&first}, ref.Or...) {
		a := c.constraintAtom(alt, subject, scope)
		if a == nil {
			return nil
		}
		con.Or = append(con.Or, a)
	}
	return con
}

func (c *checker) constraintAtom(ref *syntax.PredRef, subject Type, scope map[string]Type) *Constraint {
	result := c.checkedConstraintAtom(ref, subject, scope)
	if result != nil && c.recordPredicateRefs {
		if c.info.predicateRefs == nil {
			c.info.predicateRefs = map[diag.Pos]*Constraint{}
		}
		c.info.predicateRefs[ref.Pos] = result
	}
	return result
}

func (c *checker) checkedConstraintAtom(ref *syntax.PredRef, subject Type, scope map[string]Type) *Constraint {
	if containsOpaque(subject, map[Type]bool{}) {
		c.bindErr(ref.Pos, "facts cannot apply to %s, which holds a Go value that can change", subject)
		return nil
	}

	if pt, ok := scope[ref.Name]; ok && !c.fieldWhere {
		// A function parameter used as a predicate.
		ft, isFunc := pt.(*FuncType)
		switch {
		case !isFunc || len(ft.Params) != 1 || ft.Result != Bool:
			c.errorf(ref.Pos, "%s is a parameter of type %s; a predicate parameter must be a function from the value to Bool", ref.Name, pt)
			return nil
		case !assignable(subject, ft.Params[0]):
			c.errorf(ref.Pos, "%s applies to %s, not %s", ref.Name, ft.Params[0], subject)
			return nil
		case len(ref.Args) > 0:
			c.errorf(ref.Pos, "%s is a parameter and takes no arguments here", ref.Name)
			return nil
		}
		return &Constraint{PredParam: ref.Name, Pos: ref.Pos}
	}
	fn, ok := c.funcNamed(ref.Name)
	if !ok {
		c.errorf(ref.Pos, "unknown predicate %s", ref.Name)
		return nil
	}
	c.info.sourceDefinitions[ref.Pos] = fn.Decl.Pos
	c.info.sourceNames[ref.Pos] = ref.Name
	if !fn.Decl.IsPred {
		c.errorf(ref.Pos, "%s is a function, not a predicate (declare it with pred)", ref.Name)
		return nil
	}
	if len(fn.Params) == 0 {
		return nil
	}
	// A generic predicate (`pred notEmpty[T](xs: List[T])`) takes its
	// type arguments from the value and the arguments.
	param := func(i int) Type { return fn.Params[i] }
	var in *inference
	if len(fn.TypeParams) > 0 {
		in = newInference(fn)
		in.unify(fn.Params[0], subject)
		param = func(i int) Type { return in.subst(fn.Params[i]) }
	}
	if !assignable(subject, param(0)) {
		c.errorf(ref.Pos, "%s applies to %s, not %s", ref.Name, param(0), subject)
		return nil
	}
	if len(ref.Args) != len(fn.Params)-1 {
		c.errorf(ref.Pos, "%s takes %d argument(s) after the value, but %d were given", ref.Name, len(fn.Params)-1, len(ref.Args))
		return nil
	}
	con := &Constraint{Pred: fn, Pos: ref.Pos, Pkg: c.pkg}
	for i, a := range ref.Args {
		want := param(i + 1)
		if id, ok := a.(*syntax.Ident); ok {
			pt, ok := scope[id.Name]
			if !ok {
				if c.fieldWhere {
					c.errorf(a.Position(), "%s is not a sibling field here; predicate arguments are constants or sibling field names", id.Name)
				} else {
					c.errorf(a.Position(), "%s is not a parameter here; predicate arguments are constants or parameter names", id.Name)
				}
				return nil
			}
			if in != nil {
				in.unify(fn.Params[i+1], pt)
				want = param(i + 1)
			}
			if !assignable(pt, want) {
				c.errorf(a.Position(), "argument %d of %s must be %s, found %s", i+1, ref.Name, want, pt)
				return nil
			}
			con.Args = append(con.Args, CArg{Param: id.Name, Type: pt, source: a})
			continue
		}
		if in.open(want) {
			want = nil
		}
		at := c.exprWant(a, want)
		if in != nil {
			in.unify(fn.Params[i+1], at)
			want = param(i + 1)
		}
		v := c.info.constantOf(a)
		switch {
		case at == Invalid:
			return nil
		case v == nil:
			if c.fieldWhere {
				c.errorf(a.Position(), "predicate arguments are constants or sibling field names")
			} else {
				c.errorf(a.Position(), "predicate arguments are constants or parameter names")
			}
			return nil
		case !assignable(at, want):
			c.errorf(a.Position(), "argument %d of %s must be %s, found %s", i+1, ref.Name, want, at)
			return nil
		}
		con.Args = append(con.Args, CArg{Const: v, Type: at, source: a})
	}
	if in != nil {
		if missing := in.unsolved(); len(missing) > 0 {
			c.errorf(ref.Pos, "cannot tell what %s is for %s here", strings.Join(missing, " and "), ref.Name)
			return nil
		}
	}
	for i := range fn.Params {
		if containsOpaque(param(i), map[Type]bool{}) {
			c.bindErr(ref.Pos, "predicate %s cannot take %s, which holds a Go value that can change", ref.Name, param(i))
			return nil
		}
	}
	inst := con.InstanceFor(subject)
	if inst != nil && !c.resolveDicts(inst, ref.Pos) {
		return nil
	}
	return con
}

// HasSiblingArgs reports whether a field constraint depends on sibling values.
func (c *Constraint) HasSiblingArgs() bool {
	for _, a := range c.Args {
		if a.Sibling {
			return true
		}
	}
	for _, a := range c.Or {
		if a.HasSiblingArgs() {
			return true
		}
	}
	return false
}

// InstanceFor instantiates a constraint predicate using all its arguments.
// Some type parameters occur only in a sibling or function parameter.
func (c *Constraint) InstanceFor(subject Type) *Instance {
	if c.Pred == nil {
		return nil
	}
	in := newInference(c.Pred)
	in.unify(c.Pred.Params[0], subject)
	for i, arg := range c.Args {
		if arg.Type != nil {
			in.unify(c.Pred.Params[i+1], arg.Type)
		}
	}
	if len(in.unsolved()) > 0 {
		return nil
	}
	return in.instance()
}

func substConstraints(cons []*Constraint, bound map[*TypeParam]Type) []*Constraint {
	if cons == nil {
		return nil
	}
	out := make([]*Constraint, len(cons))
	for i, con := range cons {
		cp := *con
		cp.Args = append([]CArg(nil), con.Args...)
		for j, arg := range cp.Args {
			if arg.Type != nil {
				cp.Args[j].Type = subst(arg.Type, bound)
			}
		}
		cp.Or = substConstraints(con.Or, bound)
		out[i] = &cp
	}
	return out
}

// PredicateDicts resolves dictionaries in the scope that declared a constraint.
// Runtime validation may instantiate that constraint at a concrete field type.
func (info *Info) PredicateDicts(from *Package, inst *Instance) bool {
	c := &checker{deriveHelperState: &deriveHelperState{}, info: info, pkg: from, diags: &diag.List{}, typeParams: map[string]*TypeParam{}}
	for _, typ := range inst.TypeArgs {
		if tp, ok := typ.(*TypeParam); ok {
			c.typeParams[tp.Name] = tp
		}
	}
	return c.resolveDicts(inst, diag.Pos{})
}

// Retain the checked scope identities of names that appear only in facts.
func (c *checker) noteConstraintSources(constraints []*Constraint, positions map[string]diag.Pos) {
	for _, con := range constraints {
		if pos, ok := positions[con.PredParam]; ok {
			c.info.sourceDefinitions[con.Pos] = pos
		}
		for _, arg := range con.Args {
			if pos, ok := positions[arg.Param]; ok && arg.Const == nil && arg.source != nil {
				c.info.sourceDefinitions[arg.source.Position()] = pos
			}
		}
		c.noteConstraintSources(con.Or, positions)
	}
}
