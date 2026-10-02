package check

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/GiGurra/bork/internal/syntax"
)

// Methods: `fn (xs: List[T]) first[T](): Option[T] { ... }` declares a
// method of List, called as xs.first(). A method is a function whose
// first parameter is its receiver; `x.m(a)` calls m with x and a, as
// `x |> m(a)` would, but finds m among the methods of x's type rather
// than the functions in scope. So methods of different types can share
// names (List's get and Map's get), and chains read left to right:
// xs.filter(f).map(g).
//
// A package can declare methods on any type, its own or not. A call
// sees the methods of its own package first; then the exported ones of
// the receiver type's package and of the packages it imports; then the
// prelude's (which has those of List, Map, and String). A record's
// field holding a function is called as before: r.f() calls the field.

// methodKey names the types methods can be declared on: List, Map,
// the basic types, and declared types (whatever their type arguments).
func methodKey(t Type) (string, bool) {
	switch t := t.(type) {
	case *List:
		return "List", true
	case *Map:
		return "Map", true
	case *Record, *Sealed:
		return fmt.Sprintf("%p", genericBaseOrSelf(t)), true
	case *Resource:
		return fmt.Sprintf("%p", t), true
	case *Basic:
		if isValue(t) && t != Scope {
			return t.String(), true
		}
	}
	return "", false
}

func (c *checker) declareMethod(fd *syntax.FuncDecl, prelude bool) {
	fn := &Func{Decl: fd, Pkg: c.pkg, Prelude: prelude}
	fn.TypeParams = c.declareTypeParams(fd, prelude)
	fn.Effects = c.effectsOf(fd.Uses)
	fn.Result = c.resolveType(fd.Result)
	for _, p := range fd.Params {
		fn.Params = append(fn.Params, c.resolveType(p.Type))
	}
	c.openSignature(fn)
	c.typeParams = nil
	recv := fn.Params[0]
	if recv == Invalid {
		return
	}
	key, ok := methodKey(recv)
	if !ok {
		c.errorf(fd.Params[0].Type.Pos, "methods can be declared on List, Map, the basic types, and declared types, not on %s", recv)
		return
	}
	if fd.Params[0].Default != nil {
		c.errorf(fd.Params[0].Pos, "a receiver cannot have a default")
	}
	if c.pkg.methods == nil {
		c.pkg.methods = map[string]map[string]*Func{}
	}
	if c.pkg.methods[key] == nil {
		c.pkg.methods[key] = map[string]*Func{}
	}
	if prev := c.pkg.methods[key][fd.Name]; prev != nil {
		c.errorf(fd.Pos, "method %s of %s is already declared at %s", fd.Name, recv, prev.Decl.Pos)
		return
	}
	c.pkg.methods[key][fd.Name] = fn
	c.info.FuncOf[fd] = fn
}

func exported(name string) bool {
	for _, r := range name {
		return unicode.IsUpper(r)
	}
	return false
}

// methodNamed finds the method name of type t that code here sees, or
// says why there is none.
func (c *checker) methodNamed(t Type, name string) (*Func, string) {
	key, ok := methodKey(t)
	if !ok {
		return nil, fmt.Sprintf("%s has no methods", t)
	}
	if c.inPrelude {
		if fn := c.preludePkg.methods[key][name]; fn != nil {
			return fn, ""
		}
		return nil, fmt.Sprintf("%s has no method %s", t, name)
	}
	if fn := c.pkg.methods[key][name]; fn != nil {
		return fn, ""
	}
	// The exported methods of the type's package and of the imports.
	var found []*Func
	seen := map[*Package]bool{c.pkg: true}
	look := func(p *Package) {
		if p == nil || seen[p] {
			return
		}
		seen[p] = true
		if fn := p.methods[key][name]; fn != nil && exported(name) {
			found = append(found, fn)
		}
	}
	switch t := t.(type) {
	case *Record:
		look(t.Pkg)
	case *Sealed:
		look(t.Pkg)
	case *Resource:
		look(t.Pkg)
	}
	for _, p := range c.pkg.imports {
		look(p)
	}
	switch len(found) {
	case 1:
		return found[0], ""
	case 0:
	default:
		var where []string
		for _, fn := range found {
			where = append(where, fn.Pkg.Path)
		}
		return nil, fmt.Sprintf("method %s of %s is ambiguous: it is declared in %s", name, t, strings.Join(where, " and "))
	}
	if fn := c.preludePkg.methods[key][name]; fn != nil {
		return fn, ""
	}
	return nil, fmt.Sprintf("%s has no method %s", t, name)
}

// methodCallOf checks e if it is a method call, x.m(args), and reports
// whether it was. The call is left as written: the checker records its
// arguments as m takes them, x first (see Info.callArgs).
func (c *checker) methodCallOf(e *syntax.Call, want Type) (Type, bool) {
	sel, ok := e.Fun.(*syntax.Selector)
	if !ok {
		return nil, false
	}
	if _, isType := c.isTypeRef(sel.X); isType {
		return nil, false
	}
	if c.isVariantPath(sel.X) {
		return nil, false
	}
	xt := c.expr(sel.X)
	if xt == Invalid {
		for _, a := range e.Args {
			c.expr(a)
		}
		return Invalid, true
	}
	if r, ok := xt.(*Record); ok && r.Field(sel.Name) != nil {
		// A field holding a function.
		if len(e.TypeArgs) > 0 {
			c.errorf(e.Pos, "only a declared generic function can be given type arguments")
		}
		return c.callFuncValue(e, c.record(sel, r.Field(sel.Name).Type)), true
	}
	if c.unbound(xt) {
		// The receiver's type is not known (`[]` that nothing decides):
		// that is reported where it comes from.
		for _, a := range e.Args {
			if _, isLambda := a.(*syntax.Lambda); !isLambda {
				c.expr(a)
			}
		}
		return Invalid, true
	}
	fn, why := c.methodNamed(xt, sel.Name)
	if fn == nil {
		c.errorf(sel.Pos, "%s", why)
		for _, a := range e.Args {
			c.expr(a)
		}
		return Invalid, true
	}
	typeArgs := e.TypeArgs
	if len(e.TypeArgs) > 0 {
		full, ok := c.methodTypeArgs(e, sel, fn)
		if !ok {
			for _, a := range e.Args {
				if _, isLambda := a.(*syntax.Lambda); !isLambda {
					c.expr(a)
				}
			}
			return Invalid, true
		}
		typeArgs = full
	}
	args := append([]syntax.Expr{sel.X}, e.Args...)
	return c.callFunc(e, sel.Name, fn, args, xt, typeArgs, want), true
}

// methodTypeArgs places the type arguments written in a method call
// among the method's type parameters: those its receiver does not
// decide (xs.map[String](f) gives map[A, B] its B), or else all of
// them. The receiver's places are left nil.
func (c *checker) methodTypeArgs(e *syntax.Call, sel *syntax.Selector, fn *Func) ([]*syntax.TypeExpr, bool) {
	var free []int
	for i, tp := range fn.TypeParams {
		if !mentionsParam(fn.Params[0], tp) {
			free = append(free, i)
		}
	}
	full := make([]*syntax.TypeExpr, len(fn.TypeParams))
	switch len(e.TypeArgs) {
	case len(free):
		for k, i := range free {
			full[i] = e.TypeArgs[k]
		}
	case len(fn.TypeParams):
		copy(full, e.TypeArgs)
	default:
		if len(free) == 0 {
			c.errorf(sel.Pos, "method %s takes no type arguments: its receiver decides them", sel.Name)
		} else {
			c.errorf(sel.Pos, "method %s takes %d type argument(s) (%s), but %d were given", sel.Name, len(free), freeNames(fn, free), len(e.TypeArgs))
		}
		return nil, false
	}
	return full, true
}

func freeNames(fn *Func, free []int) string {
	names := make([]string, len(free))
	for k, i := range free {
		names[k] = fn.TypeParams[i].Name
	}
	return strings.Join(names, ", ")
}

// isVariantPath reports whether x names a sealed type's variant or a
// package (pkg.Type.Variant), which are not values with methods.
func (c *checker) isVariantPath(x syntax.Expr) bool {
	s, ok := x.(*syntax.Selector)
	if !ok {
		return false
	}
	_, isType := c.isTypeRef(s.X)
	return isType
}
