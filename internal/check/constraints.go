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
type Constraint struct {
	Pred *Func
	Args []CArg
	Pos  diag.Pos
	Or   []*Constraint
}

// CArg is an argument of a constraint's predicate, after the value
// itself: a constant, or the name of one of the function's parameters
// (`to: AccountId where notEqual(from)`).
type CArg struct {
	Const constant.Value // nil for a parameter
	Param string
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
func (c *Constraint) String() string {
	if c.Or != nil {
		alts := make([]string, len(c.Or))
		for i, a := range c.Or {
			alts[i] = a.String()
		}
		return strings.Join(alts, " or ")
	}
	if len(c.Args) == 0 {
		return c.Pred.Decl.Name
	}
	args := make([]string, len(c.Args))
	for i, a := range c.Args {
		args[i] = a.String()
	}
	return c.Pred.Decl.Name + "(" + strings.Join(args, ", ") + ")"
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
			c.fieldConstraints(t.Fields, t.Decl.Fields)
		case *Sealed:
			for _, v := range t.Variants {
				c.fieldConstraints(v.Fields, t.Decl.Variants[v.Index].Fields)
			}
		}
	}
	for _, f := range files {
		for _, td := range f.Types {
			if e := c.decls[td.Name]; e != nil && e.decl == td && td.Kind == syntax.AliasType {
				c.aliasConstraints(e)
			}
		}
	}
	for _, f := range files {
		for _, fd := range f.Funcs {
			fn := c.info.Funcs[fd.Name]
			if fn == nil || fn.Decl != fd {
				continue
			}
			scope := map[string]Type{}
			for i, p := range fd.Params {
				scope[p.Name] = fn.Params[i]
			}
			fn.ParamConstraints = make([][]*Constraint, len(fd.Params))
			for i, p := range fd.Params {
				fn.ParamConstraints[i] = c.constraintsOf(p.Type, fn.Params[i], scope)
			}
			fn.ResultConstraints = c.memberConstraints(fd.Result, fn.Result, scope)
		}
	}
}

func (c *checker) fieldConstraints(fields []*Field, decls []*syntax.FieldDecl) {
	for _, f := range fields {
		for _, fd := range decls {
			if fd.Name == f.Name {
				f.Constraints = c.constraintsOf(fd.Type, f.Type, nil)
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
	if t == nil || typ == Invalid {
		return nil
	}
	var out []*Constraint
	if t.Union == nil && len(t.Args) == 0 {
		if e, ok := c.decls[t.Name]; ok && e.decl.Kind == syntax.AliasType {
			out = append(out, c.aliasConstraints(e)...)
		}
	}
	for _, ref := range t.Where {
		if con := c.constraint(ref, typ, scope); con != nil {
			out = append(out, con)
		}
	}
	return out
}

func (c *checker) aliasConstraints(e *typeEntry) []*Constraint {
	if !e.constraintsDone {
		e.constraintsDone = true
		e.constraints = c.constraintsOf(e.decl.Alias, e.typ, nil)
	}
	return e.constraints
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
	fn, ok := c.info.Funcs[ref.Name]
	if !ok {
		c.errorf(ref.Pos, "unknown predicate %s", ref.Name)
		return nil
	}
	if !fn.Decl.IsPred {
		c.errorf(ref.Pos, "%s is a function, not a predicate (declare it with pred)", ref.Name)
		return nil
	}
	if len(fn.Params) == 0 {
		return nil
	}
	if !assignable(subject, fn.Params[0]) {
		c.errorf(ref.Pos, "%s applies to %s, not %s", ref.Name, fn.Params[0], subject)
		return nil
	}
	if len(ref.Args) != len(fn.Params)-1 {
		c.errorf(ref.Pos, "%s takes %d argument(s) after the value, but %d were given", ref.Name, len(fn.Params)-1, len(ref.Args))
		return nil
	}
	con := &Constraint{Pred: fn, Pos: ref.Pos}
	for i, a := range ref.Args {
		want := fn.Params[i+1]
		if id, ok := a.(*syntax.Ident); ok {
			pt, ok := scope[id.Name]
			if !ok {
				c.errorf(a.Position(), "%s is not a parameter here; predicate arguments are constants or parameter names", id.Name)
				return nil
			}
			if !assignable(pt, want) {
				c.errorf(a.Position(), "argument %d of %s must be %s, found %s", i+1, ref.Name, want, pt)
				return nil
			}
			con.Args = append(con.Args, CArg{Param: id.Name})
			continue
		}
		at := c.exprWant(a, want)
		v := c.info.constantOf(a)
		switch {
		case at == Invalid:
			return nil
		case v == nil:
			c.errorf(a.Position(), "predicate arguments are constants or parameter names")
			return nil
		case !assignable(at, want):
			c.errorf(a.Position(), "argument %d of %s must be %s, found %s", i+1, ref.Name, want, at)
			return nil
		}
		con.Args = append(con.Args, CArg{Const: v})
	}
	return con
}
