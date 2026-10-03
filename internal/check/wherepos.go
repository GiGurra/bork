package check

import (
	"reflect"
	"slices"

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
		if !c.pkg.Root {
			// Imported tests are not checked as part of the root package.
			copy := *f
			copy.Tests = nil
			f = &copy
		}
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
		hint := ""
		if where == "in a type pattern" {
			hint = "; match the base type and then guard with the predicate"
		}
		switch {
		case len(t.Where) > 0:
			c.errorf(t.Where[0].Pos, "where clauses %s are not supported yet, so the fact would not be checked%s", at, hint)
		case len(c.constrainedAlias(t)) > 0:
			c.errorf(t.Pos, "%s is a constrained type (where %s), and constrained types %s are not supported yet, so the fact would not be checked%s",
				t.Name, constraintsText(c.constrainedAlias(t), c.pkg), at, hint)
		}
	}
	// Inside a type whose facts are dropped, the parts' facts are
	// dropped for the same reason.
	in := func(own string) string {
		if !c.appliedWhere[t] && where != "" {
			return where
		}
		return own
	}
	for _, m := range t.Union {
		c.unappliedIn(m, in("on a member of a union"))
	}
	if t.Func != nil {
		for _, p := range t.Func.Params {
			c.unappliedIn(p, in("on a function type's parameters"))
		}
		if t.Func.Result != nil {
			c.unappliedIn(t.Func.Result, in("on a function type's result"))
		}
	}
	for i, a := range t.Args {
		switch {
		case t.Name == "Map" && i == 0:
			c.unappliedIn(a, "on a Map's keys")
		case t.Name == "Map":
			c.unappliedIn(a, "on a Map's values")
		default:
			c.unappliedIn(a, in("on this type argument of "+t.Name))
		}
	}
}

// hasFacts reports whether the written type t has facts anywhere: a
// where clause, or a constrained alias. (It looks at what is written,
// so it works before predicates are resolved.)
func (c *checker) hasFacts(t *syntax.TypeExpr) bool {
	visiting := map[*syntax.TypeDecl]bool{}
	var has func(t *syntax.TypeExpr) bool
	has = func(t *syntax.TypeExpr) bool {
		if t == nil {
			return false
		}
		if len(t.Where) > 0 || slices.ContainsFunc(t.Union, has) || slices.ContainsFunc(t.Args, has) {
			return true
		}
		if t.Func != nil && (has(t.Func.Result) || slices.ContainsFunc(t.Func.Params, has)) {
			return true
		}
		if t.Union == nil && t.Func == nil && len(t.Args) == 0 {
			if c.typeParams[t.Name] != nil {
				return false
			}
			// An alias, unless it is part of a cycle (an error already).
			if e := c.lookupType(t.Name); e != nil && e.decl.Kind == syntax.AliasType && !visiting[e.decl] {
				visiting[e.decl] = true
				savedPkg, savedParams := c.pkg, c.typeParams
				c.pkg, c.typeParams = e.pkg, nil
				facts := has(e.decl.Alias)
				c.pkg, c.typeParams = savedPkg, savedParams
				return facts
			}
		}
		return false
	}
	return has(t)
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
	type pointerKey struct {
		typ reflect.Type
		ptr uintptr
	}
	key := func(v reflect.Value) pointerKey { return pointerKey{v.Type(), v.Pointer()} }
	seen := map[pointerKey]bool{}
	var walk func(v reflect.Value, where string)
	walk = func(v reflect.Value, where string) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[key(v)] {
				return
			}
			seen[key(v)] = true
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
			case *syntax.TypePat:
				if n.Type != nil {
					seen[key(reflect.ValueOf(n.Type))] = true
					f(n.Type, "in a type pattern")
				}
			case *syntax.VariantPat:
				if len(n.Path) == 1 || len(n.Path) == 2 {
					// Bare type names, record patterns, and variant owners
					// have no TypeExpr.
					f(&syntax.TypeExpr{Pos: n.Pos, Name: n.Path[0]}, "in a type pattern")
				}
			}
			for _, p := range params {
				if p.Type != nil {
					seen[key(reflect.ValueOf(p.Type))] = true
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
