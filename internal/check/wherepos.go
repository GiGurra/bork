package check

import (
	"reflect"
	"slices"
	"sync"

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
		copy := *f
		copy.Templates, copy.DeriveHelpers = nil, nil
		f = &copy
		if !c.pkg.Root {
			// Imported tests are not checked as part of the root package.
			copy := *f
			copy.Tests = nil
			f = &copy
		}
		c.forTypeExprs(reflect.ValueOf(f), func(t *syntax.TypeExpr, where string) {
			c.unappliedIn(t, where)
		})
	}
	for _, fn := range c.info.ExpandedFunctions {
		c.pkg = fn.TemplatePkg
		c.forTypeExprs(reflect.ValueOf(fn.Decl), func(t *syntax.TypeExpr, where string) { c.unappliedIn(t, where) })
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
		switch where {
		case "in a type pattern":
			hint = "; match the base type and then guard with the predicate"
		case "in a bare or destructuring pattern":
			hint = "; use a bound type pattern (value: " + t.Name + "), then destructure inside its arm"
		}
		switch {
		case where == "in a bare or destructuring pattern" && c.nestedPatternFacts(t):
			c.errorf(t.Pos, "nested constraints in type patterns are not supported yet; match the base type and then guard with the predicate")
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
	for _, elem := range t.Tuple {
		c.unappliedIn(elem, in("on a tuple element"))
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
		if len(t.Where) > 0 || slices.ContainsFunc(t.Union, has) || slices.ContainsFunc(t.Args, has) || slices.ContainsFunc(t.Tuple, has) {
			return true
		}
		if t.Func != nil && (has(t.Func.Result) || slices.ContainsFunc(t.Func.Params, has)) {
			return true
		}
		if t.Union == nil && t.Func == nil && t.Tuple == nil {
			if c.typeParams[t.Name] != nil {
				return len(c.aliasFacts[c.typeParams[t.Name]]) > 0
			}
			// An alias, unless it is part of a cycle (an error already).
			if e := c.lookupType(t.Name); e != nil && e.decl.Kind == syntax.AliasType && !visiting[e.decl] {
				visiting[e.decl] = true
				savedPkg, savedParams, savedFacts := c.pkg, c.typeParams, c.aliasFacts
				c.pkg, c.typeParams, c.aliasFacts = e.pkg, map[string]*TypeParam{}, nil
				for _, tp := range e.params {
					c.typeParams[tp.Name] = tp
				}
				facts := has(e.decl.Alias)
				c.pkg, c.typeParams, c.aliasFacts = savedPkg, savedParams, savedFacts
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
	if t.Union != nil || t.Func != nil || t.Tuple != nil {
		return nil
	}
	if tp := c.typeParams[t.Name]; tp != nil {
		return c.aliasFacts[tp]
	}
	if e := c.lookupType(t.Name); e != nil && e.decl.Kind == syntax.AliasType {
		if len(e.params) > 0 {
			typ := c.info.writtenTypes[t]
			if typ == nil {
				typ = c.resolveType(t)
			}
			return c.genericAliasConstraints(e, t, typ, nil)
		}
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
	for _, x := range append(append(append([]*syntax.TypeExpr{}, t.Union...), t.Args...), t.Tuple...) {
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
func (c *checker) forTypeExprs(v reflect.Value, f func(t *syntax.TypeExpr, where string)) {
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
				if !n.Context && (len(n.Path) == 1 || len(n.Path) == 2) {
					// Bare type names, record patterns, and variant owners
					// have no TypeExpr.
					f(&syntax.TypeExpr{Pos: n.Pos, Name: n.Path[0]}, "in a bare or destructuring pattern")
				}
			case *syntax.TypeHead:
				f(n.Type, "on a constructor's type")
				return
			case *syntax.RecordLit:
				owner := n.Type
				if sel, ok := owner.(*syntax.Selector); ok {
					owner = sel.X
				}
				if id, ok := owner.(*syntax.Ident); ok {
					f(&syntax.TypeExpr{Pos: id.Pos, Name: id.Name}, "on a constructor's type")
				}
			case *syntax.Selector:
				if c.info.selectorVariants[n] != nil {
					if id, ok := n.X.(*syntax.Ident); ok {
						f(&syntax.TypeExpr{Pos: id.Pos, Name: id.Name}, "on a constructor's type")
					}
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
			for _, i := range walkableSyntaxFields(v.Type()) {
				walk(v.Field(i), where)
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), where)
			}
		}
	}
	walk(v, "")
}

// Syntax struct layouts are immutable. Discover walkable exported fields once,
// retaining declaration order and omitting scalar kinds the walker ignores.
var syntaxFields sync.Map // reflect.Type -> []int, in declaration order

func walkableSyntaxFields(typ reflect.Type) []int {
	if fields, ok := syntaxFields.Load(typ); ok {
		return fields.([]int)
	}
	var fields []int
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		switch field.Type.Kind() {
		case reflect.Pointer, reflect.Interface, reflect.Struct, reflect.Slice:
			fields = append(fields, i)
		}
	}
	actual, _ := syntaxFields.LoadOrStore(typ, fields)
	return actual.([]int)
}
