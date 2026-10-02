package check

import (
	"reflect"

	"github.com/GiGurra/bork/internal/syntax"
)

// A where clause the checker does not apply would be a fact that is
// neither required of callers nor known to the code: a silent hole in
// the guarantees. So constraintsOf records each written type whose
// facts it applies (its where clauses, or a constrained alias it
// names), and unappliedWheres reports every other written type that
// has facts, in any position a type can be written.

// unappliedWheres reports the facts written in files that no
// constraint was made from.
func (c *checker) unappliedWheres(files []*syntax.File) {
	for _, f := range files {
		if f.Prelude {
			continue
		}
		c.inFile(f)
		forTypeExprs(reflect.ValueOf(f), func(t *syntax.TypeExpr, where string) {
			c.unappliedIn(t, where)
		})
	}
}

// unappliedIn reports the unapplied facts in the written type t, which
// is in the position that where describes ("" if unknown).
func (c *checker) unappliedIn(t *syntax.TypeExpr, where string) {
	if !c.appliedWhere[t] {
		at := where
		if at == "" {
			at = "here"
		}
		switch {
		case len(t.Where) > 0:
			c.errorf(t.Where[0].Pos, "where clauses %s are not supported yet, so the fact would not be checked", at)
		case len(c.constrainedAlias(t)) > 0:
			c.errorf(t.Pos, "%s is a constrained type (where %s), and constrained types %s are not supported yet, so the fact would not be checked",
				t.Name, constraintsText(c.constrainedAlias(t), c.pkg), at)
		}
	}
	for _, m := range t.Union {
		c.unappliedIn(m, "on a member of a union")
	}
	if t.Func != nil {
		for _, p := range t.Func.Params {
			c.unappliedIn(p, "on a function type's parameters")
		}
		if t.Func.Result != nil {
			c.unappliedIn(t.Func.Result, "on a function type's result")
		}
	}
	for i, a := range t.Args {
		switch {
		case t.Name == "Map" && i == 0:
			c.unappliedIn(a, "on a Map's keys")
		case t.Name == "Map":
			c.unappliedIn(a, "on a Map's values")
		default:
			c.unappliedIn(a, "on this type argument of "+t.Name)
		}
	}
}

// constrainedAlias returns the facts of the constrained alias that t
// names, if it names one.
func (c *checker) constrainedAlias(t *syntax.TypeExpr) []*Constraint {
	if t.Union != nil || t.Func != nil || len(t.Args) > 0 {
		return nil
	}
	if e := c.lookupType(t.Name); e != nil && e.decl.Kind == syntax.AliasType {
		return c.aliasConstraints(e)
	}
	return nil
}

// whereReported marks the facts in t, which an error already reports,
// so unappliedWheres leaves them alone.
func (c *checker) whereReported(t *syntax.TypeExpr) {
	if t == nil {
		return
	}
	c.appliedWhere[t] = true
	for _, x := range append(append([]*syntax.TypeExpr{}, t.Union...), t.Args...) {
		c.whereReported(x)
	}
	if t.Func != nil {
		for _, p := range t.Func.Params {
			c.whereReported(p)
		}
		c.whereReported(t.Func.Result)
	}
}

// forTypeExprs calls f on each written type in v that is not part of
// another written type, with a description of its position if known.
func forTypeExprs(v reflect.Value, f func(t *syntax.TypeExpr, where string)) {
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value, where string)
	walk = func(v reflect.Value, where string) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			// The parameters of rules and lambdas are described; types
			// written in a lambda's body are not its parameters'.
			var params []*syntax.Param
			var at string
			switch n := v.Interface().(type) {
			case *syntax.TypeExpr:
				f(n, where)
				return
			case *syntax.RuleDecl:
				params, at = n.Params, "on a rule's variables"
			case *syntax.Lambda:
				params, at = n.Params, "on a lambda's parameters"
			}
			for _, p := range params {
				if p.Type != nil {
					seen[reflect.ValueOf(p.Type).Pointer()] = true
					f(p.Type, at)
				}
			}
			walk(v.Elem(), where)
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem(), where)
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i), where)
				}
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), where)
			}
		}
	}
	walk(v, "")
}
