package check

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/std"
	"github.com/GiGurra/bork/internal/syntax"
)

// A complete set of closed defaults describes one candidate even when no
// source expression constructs it. Validate it at the type declaration.
func (f *factChecker) defaultInvariants() {
	seen := map[Type]bool{}
	var visit func(Type)
	visit = func(typ Type) {
		if seen[typ] {
			return
		}
		seen[typ] = true
		var instances *instanceSet
		switch typ := typ.(type) {
		case *Record:
			instances = typ.insts
			if typ.Decl != nil && !typ.Prelude && !hasTypeParam(typ) {
				f.defaultInvariant(typ, typ.Fields, nil, typ.Decl, typ.Pkg)
			}
		case *Sealed:
			instances = typ.insts
			if typ.Decl != nil && !typ.Prelude && !hasTypeParam(typ) {
				for _, variant := range typ.Variants {
					f.defaultInvariant(typ, variant.Fields, variant, typ.Decl, typ.Pkg)
				}
			}
		}
		if instances != nil {
			for _, instance := range slices.SortedFunc(maps.Values(instances.byKey), func(a, b Type) int {
				return cmp.Compare(TypeText(a, nil), TypeText(b, nil))
			}) {
				visit(instance)
			}
		}
	}
	for _, typ := range f.info.TypeOrder {
		visit(typ)
	}
}

func (f *factChecker) defaultInvariant(typ Type, fields []*Field, variant *Variant, decl *syntax.TypeDecl, pkg *Package) {
	if len(fields) == 0 || len(TypeConstraints(typ)) == 0 && (variant == nil || len(variant.Constraints) == 0) {
		return
	}
	if pkg != nil && !pkg.Root && strings.HasPrefix(pkg.Path, std.Prefix) {
		return
	}
	candidate := &RecordLit{expr: expr{pos: decl.Pos, typ: typ}, Variant: variant}
	candidate.Record, _ = typ.(*Record)
	for _, field := range fields {
		if field.Default == nil || field.Lazy || field.Computed || hasTypeParam(field.Type) {
			return
		}
		candidate.Fields = append(candidate.Fields, &FieldValue{Name: field.Name, Field: field, Value: field.Default, IsDefault: true})
	}
	f.fn = &Func{Pkg: pkg}
	f.recordObligations(candidate, env{})
	f.nominalObligationsFor(candidate, variant, env{}, "defaults of "+TypeText(typ, pkg)+" require the completed value to be ")
}

// validationContexts removes a type's own guarantees while checking the
// predicates that establish them, including their declared callees.
func validationContexts(info *Info) map[*Func]map[Type]bool {
	out := map[*Func]map[Type]bool{}
	var visit func(*Func, Type)
	visit = func(fn *Func, typ Type) {
		if fn == nil || out[fn][typ] {
			return
		}
		if out[fn] == nil {
			out[fn] = map[Type]bool{}
		}
		out[fn][typ] = true
		for _, callee := range fn.Calls {
			visit(callee, typ)
		}
	}
	var constraints func([]*Constraint, Type)
	constraints = func(cons []*Constraint, typ Type) {
		for _, con := range cons {
			visit(con.Pred, typ)
			constraints(con.Or, typ)
		}
	}
	for _, typ := range info.TypeOrder {
		constraints(TypeConstraints(typ), typ)
		if sealed, ok := typ.(*Sealed); ok {
			for _, v := range sealed.Variants {
				constraints(v.Constraints, typ)
			}
		}
	}
	return out
}

func (f *factChecker) validatorRequirements() {
	var check func([]*Constraint)
	check = func(cons []*Constraint) {
		for _, con := range cons {
			if con.Pred != nil {
				for _, required := range con.Pred.ParamConstraints {
					if len(required) > 0 {
						f.diags.AddCode(con.Pos, "facts.invariant_precondition", "invariant predicate %s must accept its candidate without where requirements", con.Pred.QualifiedName(con.Pkg))
						f.validatorInvalid = true
						break
					}
				}
			}
			check(con.Or)
		}
	}
	for _, typ := range f.info.TypeOrder {
		check(TypeConstraints(typ))
		if sealed, ok := typ.(*Sealed); ok {
			for _, v := range sealed.Variants {
				check(v.Constraints)
			}
		}
	}
}

func (f *factChecker) validates(typ Type) bool {
	return f.validators[f.fn][genericBaseOrSelf(typ)]
}

func (f *factChecker) nominalObligations(x Expr, variant *Variant, e env) {
	f.nominalObligationsFor(x, variant, e, TypeText(x.Type(), f.from())+" requires its completed value to be ")
}

func (f *factChecker) nominalObligationsFor(x Expr, variant *Variant, e env, requirement string) {
	cons := TypeConstraints(x.Type())
	if variant != nil {
		cons = append(append([]*Constraint{}, cons...), variant.Constraints...)
	}
	if len(cons) == 0 {
		return
	}
	if f.validates(x.Type()) {
		// A proof's supplied candidate already exists. Its field/default
		// promises need validation before the predicate runs, while the type's
		// own invariants are precisely what that predicate is establishing.
		if f.proofArguments {
			return
		}
		f.diags.AddCode(x.Pos(), "facts.invariant_cycle", "a validator of %s cannot construct that type before its invariant is established", x.Type())
		f.validatorInvalid = true
		return
	}
	saved := f.candidate
	f.candidate = x
	for _, con := range cons {
		f.oblige(x, con, noParams, e, requirement+con.Text(f.from()))
	}
	f.candidate = saved
}

func (f *factChecker) patternInvariants(p *Pat, subject Expr, e env) env {
	if p == nil || subject == nil {
		return e
	}
	if p.Kind == PatVariant && !f.validates(p.Variant.Parent) {
		for _, con := range p.Variant.Constraints {
			for _, k := range f.knownOf(con, noParams) {
				ft := f.factOf(f.key(subject), k)
				ft.value = f.argOf(subject)
				e = e.with(ft)
			}
		}
	}
	for _, field := range p.Fields {
		e = f.patternInvariants(field.Pat, f.project(subject, field.Name), e)
	}
	return f.patternInvariants(p.Sub, subject, e)
}
