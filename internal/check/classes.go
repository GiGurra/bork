package check

import (
	"fmt"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Class is a type class: methods that a type has through an instance
// (`class Show[T] { fn show(x: T): String }`). Its methods are called
// like functions; which instance a call uses is decided by the types.
type Class struct {
	Name    string
	Decl    *syntax.ClassDecl
	Pkg     *Package
	Prelude bool
	Param   *TypeParam
	Methods []*Func // each has Class set, and Param as its type parameter
}

func (c *Class) String() string { return c.Name }

// Method finds a method of the class by name.
func (c *Class) Method(name string) *Func {
	for _, m := range c.Methods {
		if m.Decl.Name == name {
			return m
		}
	}
	return nil
}

// ClassInstance is a named instance of a class for a type
// (`instance showInt: Show[Int] { ... }`). A generic instance
// (`instance showList[T: Show]: Show[List[T]]`) has type parameters,
// which may need instances themselves.
type ClassInstance struct {
	Name       string
	Decl       *syntax.InstanceDecl
	Pkg        *Package
	Prelude    bool
	Class      *Class
	TypeParams []*TypeParam
	Type       Type
	// Methods holds the implementations, in the class's method order.
	Methods []*Func
}

// Dict is the instance a use of a class method or of a function with
// bounded type parameters gets: a declared instance for a type (with
// the instances its own bounds need), or the instance the enclosing
// function was given for one of its type parameters.
type Dict struct {
	Class *Class
	Type  Type
	// A declared instance:
	Inst     *ClassInstance
	TypeArgs []Type
	Args     []*Dict
	// Or a bound of a type parameter in scope:
	Param *TypeParam
}

// HasBound reports whether the type parameter is bounded by class.
func (t *TypeParam) HasBound(class *Class) bool {
	for _, b := range t.Bounds {
		if b == class {
			return true
		}
	}
	return false
}

// declareClasses declares the classes of the files (pass 1, with types).
func (c *checker) declareClasses(files []*syntax.File) {
	for _, f := range files {
		c.inFile(f)
		for _, cd := range f.Classes {
			switch {
			case c.isTypeName(cd.Name) || c.pkg.classes[cd.Name] != nil:
				c.errorf(cd.Pos, "%s is already the name of a type or class", cd.Name)
				continue
			case len(cd.TypeParams) != 1:
				c.errorf(cd.Pos, "a class has one type parameter, the type its instances are for: class %s[T] { ... }", cd.Name)
				continue
			case len(cd.TypeParams[0].Bounds) > 0:
				c.errorf(cd.TypeParams[0].Pos, "a class's type parameter cannot have bounds (yet)")
			}
			cl := &Class{Name: cd.Name, Decl: cd, Pkg: c.pkg, Prelude: f.Prelude,
				Param: &TypeParam{Name: cd.TypeParams[0].Name, Decl: cd.TypeParams[0]}}
			c.pkg.classes[cd.Name] = cl
			c.info.Classes = append(c.info.Classes, cl)
		}
	}
}

// declareClassMethods declares the classes' methods, which are called
// like functions of the package.
func (c *checker) declareClassMethods() {
	for _, cl := range c.info.Classes {
		c.pkg, c.inPrelude = cl.Pkg, cl.Prelude
		c.typeParams = map[string]*TypeParam{cl.Param.Name: cl.Param}
		for _, md := range cl.Decl.Methods {
			if len(md.TypeParams) > 0 {
				c.errorf(md.Pos, "a class method cannot have type parameters of its own (yet)")
				continue
			}
			if prev, ok := c.pkg.Funcs[md.Name]; ok {
				c.errorf(md.Pos, "%s is already declared at %s", md.Name, prev.Decl.Pos)
				continue
			}
			c.noWhere(md)
			fn := &Func{Decl: md, Pkg: c.pkg, Prelude: cl.Prelude, Class: cl, TypeParams: []*TypeParam{cl.Param}}
			fn.Result = c.resolveType(md.Result)
			for _, p := range md.Params {
				fn.Params = append(fn.Params, c.resolveType(p.Type))
			}
			cl.Methods = append(cl.Methods, fn)
			c.pkg.Funcs[md.Name] = fn
			c.info.FuncOf[md] = fn
		}
	}
	c.typeParams = nil
	c.inPrelude = false
}

// noWhere rejects where clauses in a method's signature: calls through
// the class could not see them (not supported yet).
func (c *checker) noWhere(md *syntax.FuncDecl) {
	for _, p := range md.Params {
		if hasWhere(p.Type) {
			c.errorf(p.Type.Pos, "where clauses on the methods of classes and instances are not supported yet")
			return
		}
	}
	if hasWhere(md.Result) {
		c.errorf(md.Result.Pos, "where clauses on the methods of classes and instances are not supported yet")
	}
}

// lookupClass finds a class by (possibly qualified) name.
func (c *checker) lookupClass(name string) *Class {
	if pkg, n, ok := c.qualified(name); ok {
		if !Exported(n) {
			return nil
		}
		return pkg.classes[n]
	}
	if cl := c.pkg.classes[name]; cl != nil {
		return cl
	}
	return c.preludePkg.classes[name]
}

// bounds resolves the bounds of a type parameter.
func (c *checker) bounds(d *syntax.TypeParam) []*Class {
	var out []*Class
	for _, name := range d.Bounds {
		cl := c.lookupClass(name)
		if cl == nil {
			if why := c.notFound(name); why != "" {
				c.errorf(d.Pos, "%s", why)
			} else {
				c.errorf(d.Pos, "unknown class %s", name)
			}
			continue
		}
		out = append(out, cl)
	}
	return out
}

// declareInstances declares the files' instances and their methods.
func (c *checker) declareInstances(files []*syntax.File) {
	for _, f := range files {
		c.inFile(f)
		c.inPrelude = f.Prelude
		for _, id := range f.Instances {
			c.declareInstance(id, f.Prelude)
		}
	}
	c.inPrelude = false
	c.typeParams = nil
}

func (c *checker) declareInstance(id *syntax.InstanceDecl, prelude bool) {
	for _, other := range c.pkg.instances {
		if other.Name == id.Name {
			c.errorf(id.Pos, "instance %s is already declared at %s", id.Name, other.Decl.Pos)
			return
		}
	}
	cl := c.lookupClass(id.Class)
	if cl == nil {
		if why := c.notFound(id.Class); why != "" {
			c.errorf(id.ClassPos, "%s", why)
		} else {
			c.errorf(id.ClassPos, "unknown class %s", id.Class)
		}
		return
	}
	ci := &ClassInstance{Name: id.Name, Decl: id, Pkg: c.pkg, Prelude: prelude, Class: cl}
	ci.TypeParams = c.declareTypeParamList(id.TypeParams, prelude)
	ci.Type = c.resolveType(id.Type)
	if ci.Type == Invalid {
		return
	}
	bound := map[*TypeParam]Type{cl.Param: ci.Type}
	impls := map[string]*syntax.FuncDecl{}
	for _, md := range id.Methods {
		if impls[md.Name] != nil {
			c.errorf(md.Pos, "method %s is declared twice in instance %s", md.Name, id.Name)
			continue
		}
		impls[md.Name] = md
		if cl.Method(md.Name) == nil {
			c.errorf(md.Pos, "class %s has no method %s", cl.Name, md.Name)
		}
	}
	for _, m := range cl.Methods {
		md := impls[m.Decl.Name]
		if md == nil {
			c.errorf(id.Pos, "instance %s is missing method %s of class %s", id.Name, m.Decl.Name, cl.Name)
			continue
		}
		if len(md.TypeParams) > 0 {
			c.errorf(md.Pos, "an instance's method cannot have type parameters; give the instance its type parameters")
			continue
		}
		c.noWhere(md)
		fn := &Func{Decl: md, Pkg: c.pkg, Prelude: prelude, Of: ci, TypeParams: ci.TypeParams}
		fn.Result = c.resolveType(md.Result)
		for _, p := range md.Params {
			fn.Params = append(fn.Params, c.resolveType(p.Type))
		}
		want := &FuncType{Result: subst(m.Result, bound)}
		for _, p := range m.Params {
			want.Params = append(want.Params, subst(p, bound))
		}
		if !identical(fn.funcType(), want) {
			c.errorf(md.Pos, "method %s of instance %s must be %s, to match class %s for %s, but is %s", md.Name, id.Name, want, cl.Name, ci.Type, fn.funcType())
		}
		ci.Methods = append(ci.Methods, fn)
		c.info.FuncOf[md] = fn
	}
	c.pkg.instances = append(c.pkg.instances, ci)
	c.info.ClassInstances = append(c.info.ClassInstances, ci)
}

// resolveUses decides which instances are in scope in each package: its
// own, the prelude's, and the ones it uses. (Derived instances too need
// `use` in other packages: no instance is in scope on its own.)
func (c *checker) resolveUses(files []*syntax.File) {
	for _, pkg := range c.pkgs {
		pkg.inScope = append(append([]*ClassInstance(nil), pkg.instances...), c.preludePkg.instances...)
	}
	for _, f := range files {
		if f.Prelude {
			continue
		}
		c.inFile(f)
		for _, u := range f.Uses {
			c.use(u)
		}
	}
}

func (c *checker) use(u *syntax.Use) {
	add := func(ci *ClassInstance) {
		for _, have := range c.pkg.inScope {
			if have == ci {
				return
			}
		}
		c.pkg.inScope = append(c.pkg.inScope, ci)
	}
	pkg, name, ok := c.qualified(u.Name)
	if !ok {
		for _, ci := range c.pkg.instances {
			if ci.Name == u.Name {
				return // the package's own instances are in scope anyway
			}
		}
		if i := strings.IndexByte(u.Name, '.'); i >= 0 {
			c.errorf(u.Pos, "%s is not an imported package", u.Name[:i])
		} else {
			c.errorf(u.Pos, "unknown instance %s (an instance of another package is used as pkg.name)", u.Name)
		}
		return
	}
	if name == "*" {
		for _, ci := range pkg.instances {
			if Exported(ci.Name) {
				add(ci)
			}
		}
		return
	}
	for _, ci := range pkg.instances {
		if ci.Name == name {
			if !Exported(name) {
				c.errorf(u.Pos, "%s is not exported by package %s (only names starting with an upper-case letter are)", name, pkg.Path)
				return
			}
			add(ci)
			return
		}
	}
	c.errorf(u.Pos, "package %s has no instance %s", pkg.Path, name)
}

// resolveDicts decides the instances a call of fn (or a use of it as a
// value) needs: the class's instance for a method, and one for each
// bound of a generic function's type parameters.
func (c *checker) resolveDicts(inst *Instance, pos diag.Pos) bool {
	fn := inst.Func
	ok := true
	if fn.Class != nil {
		d := c.dict(fn.Class, inst.TypeArgs[0], pos, 0)
		ok = d != nil
		inst.Dicts = []*Dict{d}
		return ok
	}
	for i, tp := range fn.TypeParams {
		for _, b := range tp.Bounds {
			d := c.dict(b, inst.TypeArgs[i], pos, 0)
			ok = ok && d != nil
			inst.Dicts = append(inst.Dicts, d)
		}
	}
	return ok
}

// dict finds the instance of class for type t, reporting an error at
// pos if there is none, or more than one.
func (c *checker) dict(class *Class, t Type, pos diag.Pos, depth int) *Dict {
	if t == Invalid || t == nil {
		return nil
	}
	if depth > 20 {
		c.errorf(pos, "cannot find an instance of %s for %s: the search does not end (instances refer to each other in a circle)", class.Name, t)
		return nil
	}
	if tp, ok := t.(*TypeParam); ok && c.inScopeParam(tp) {
		if tp.HasBound(class) {
			return &Dict{Class: class, Type: t, Param: tp}
		}
		c.errorf(pos, "%s needs an instance of %s for %s; require one: [%s: %s]", c.useText(), class.Name, tp.Name, tp.Name, class.Name)
		return nil
	}
	var matches []*Dict
	var partial []*ClassInstance // the head fits, but a bound does not
	for _, ci := range c.pkg.inScope {
		if ci.Class != class {
			continue
		}
		args, ok := matchHead(ci, t)
		if !ok {
			continue
		}
		d := &Dict{Class: class, Type: t, Inst: ci, TypeArgs: args}
		fits := true
		for i, tp := range ci.TypeParams {
			for _, b := range tp.Bounds {
				sub := c.dictQuiet(b, args[i], depth+1)
				if sub == nil {
					fits = false
				}
				d.Args = append(d.Args, sub)
			}
		}
		if fits {
			matches = append(matches, d)
		} else {
			partial = append(partial, ci)
		}
	}
	switch {
	case len(matches) == 1:
		return matches[0]
	case len(matches) > 1:
		names := make([]string, len(matches))
		own := 0
		for i, m := range matches {
			names[i] = qualify(m.Inst.Name, m.Inst.Pkg, c.pkg)
			if m.Inst.Pkg == c.pkg || m.Inst.Prelude {
				own++
			}
		}
		advice := "use only one of them"
		if own > 1 {
			advice = "a package's own instances are always in scope, so keep only one of them here, and move the others to packages that code can choose from with use"
		}
		c.errorf(pos, "more than one instance of %s for %s is in scope: %s; %s", class.Name, t, strings.Join(names, " and "), advice)
		return nil
	case len(partial) == 1:
		// Report why the one candidate does not fit.
		ci := partial[0]
		args, _ := matchHead(ci, t)
		for i, tp := range ci.TypeParams {
			for _, b := range tp.Bounds {
				c.dict(b, args[i], pos, depth+1)
			}
		}
		return nil
	}
	msg := fmt.Sprintf("no instance of %s for %s is in scope", class.Name, t)
	if hint := c.instanceHint(class, t); hint != "" {
		msg += " (" + hint + ")"
	}
	c.errorf(pos, "%s", msg)
	return nil
}

// dictQuiet is dict without error messages; it returns nil if there is
// not exactly one instance.
func (c *checker) dictQuiet(class *Class, t Type, depth int) *Dict {
	saved := c.diags
	c.diags = &diag.List{}
	d := c.dict(class, t, diag.Pos{}, depth)
	c.diags = saved
	return d
}

// matchHead matches an instance's type against t, returning the
// instance's type arguments.
func matchHead(ci *ClassInstance, t Type) ([]Type, bool) {
	in := typeInference(ci.TypeParams)
	in.unify(ci.Type, t)
	if len(in.unsolved()) > 0 || !identical(in.subst(ci.Type), t) {
		return nil, false
	}
	return in.args(), true
}

// instanceHint suggests instances of other packages that would do.
func (c *checker) instanceHint(class *Class, t Type) string {
	var found []string
	for _, pkg := range c.info.Packages {
		if pkg == c.pkg {
			continue
		}
		for _, ci := range pkg.instances {
			if ci.Class != class || !Exported(ci.Name) {
				continue
			}
			if _, ok := matchHead(ci, t); ok {
				found = append(found, "use "+qualify(ci.Name, ci.Pkg, c.pkg))
			}
		}
	}
	if len(found) > 0 {
		return strings.Join(found, ", or ")
	}
	if derivable(class) {
		var name string
		var pkg *Package
		switch b := t.(type) {
		case *Record:
			name, pkg = b.Name, b.Pkg
		case *Sealed:
			name, pkg = b.Name, b.Pkg
		}
		if name != "" && pkg == c.pkg {
			return fmt.Sprintf("add `derive (%s)` to type %s, or declare one: instance name: %s[%s] { ... }", class.Name, name, class.Name, t)
		}
	}
	return fmt.Sprintf("declare one: instance name: %s[%s] { ... }", class.Name, t)
}

// inScopeParam reports whether tp is a type parameter of the code being
// checked (a generic function's, or a generic instance's).
func (c *checker) inScopeParam(tp *TypeParam) bool {
	return c.typeParams != nil && c.typeParams[tp.Name] == tp
}

func (c *checker) useText() string {
	if c.fn != nil && c.fn.Of != nil {
		return "instance " + c.fn.Of.Name
	}
	if c.fn != nil {
		return c.fn.Decl.Name
	}
	return "this"
}

// Derived describes an instance method the compiler writes (`derive`):
// FieldDicts holds the class's instance for each field of the type (for
// a sealed type, each variant's fields, variant by variant).
type Derived struct {
	FieldDicts [][]*Dict
}

// derivable reports whether instances of class can be derived.
func derivable(class *Class) bool {
	return class.Prelude && (class.Name == "Decode" || class.Name == "Encode")
}

// declareDerived declares the instances that `derive (...)` asks for.
func (c *checker) declareDerived(files []*syntax.File) {
	for _, f := range files {
		c.inFile(f)
		for _, td := range f.Types {
			if len(td.Derive) == 0 {
				continue
			}
			e := c.pkg.types[td.Name]
			if e == nil || e.decl != td {
				continue
			}
			switch e.typ.(type) {
			case *Record, *Sealed:
			default:
				c.errorf(td.DerivePos, "only records and sealed types can derive instances")
				continue
			}
			for _, name := range td.Derive {
				cl := c.lookupClass(name)
				switch {
				case cl == nil:
					c.errorf(td.DerivePos, "unknown class %s", name)
					continue
				case !derivable(cl):
					c.errorf(td.DerivePos, "%s cannot be derived; only Decode and Encode can (yet)", name)
					continue
				}
				c.deriveInstance(td, e.typ, cl, f.Prelude)
			}
		}
	}
}

func (c *checker) deriveInstance(td *syntax.TypeDecl, t Type, cl *Class, prelude bool) {
	// A generic type's instance is generic too: Pair[A, B] can be decoded
	// if A and B can.
	var tps []*TypeParam
	var args []Type
	for _, p := range typeParamsOf(t) {
		tp := &TypeParam{Name: p.Name, Decl: p.Decl, Bounds: []*Class{cl}}
		tps = append(tps, tp)
		args = append(args, tp)
	}
	head := t
	if len(tps) > 0 {
		head = instantiate(t, args)
	}
	name := td.Name + cl.Name
	decl := &syntax.InstanceDecl{Pos: td.DerivePos, Name: name}
	ci := &ClassInstance{Name: name, Decl: decl, Pkg: c.pkg, Prelude: prelude, Class: cl, TypeParams: tps, Type: head}
	bound := map[*TypeParam]Type{cl.Param: head}
	for _, m := range cl.Methods {
		fd := &syntax.FuncDecl{Pos: td.DerivePos, Name: m.Decl.Name, Params: m.Decl.Params}
		fn := &Func{Decl: fd, Pkg: c.pkg, Prelude: prelude, Of: ci, TypeParams: tps, Result: subst(m.Result, bound), Derived: &Derived{}}
		for _, p := range m.Params {
			fn.Params = append(fn.Params, subst(p, bound))
		}
		ci.Methods = append(ci.Methods, fn)
		c.info.FuncOf[fd] = fn
	}
	c.pkg.instances = append(c.pkg.instances, ci)
	c.info.ClassInstances = append(c.info.ClassInstances, ci)
}

// resolveDerived finds the instances the fields of derived instances'
// types need, now that it is known which instances are in scope.
func (c *checker) resolveDerived() {
	for _, ci := range c.info.ClassInstances {
		if len(ci.Methods) == 0 || ci.Methods[0].Derived == nil {
			continue
		}
		c.pkg, c.inPrelude = ci.Pkg, ci.Prelude
		c.typeParams = map[string]*TypeParam{}
		for _, tp := range ci.TypeParams {
			c.typeParams[tp.Name] = tp
		}
		var groups [][]*Field
		var owners []string
		switch t := ci.Type.(type) {
		case *Record:
			groups, owners = [][]*Field{t.Fields}, []string{t.Name}
		case *Sealed:
			for _, v := range t.Variants {
				groups = append(groups, v.Fields)
				owners = append(owners, t.Name+"."+v.Name)
			}
		}
		var dicts [][]*Dict
		for i, fields := range groups {
			var row []*Dict
			for _, f := range fields {
				d := c.dictQuiet(ci.Class, f.Type, 0)
				if d == nil {
					c.errorf(ci.Decl.Pos, "cannot derive %s for %s: field %s has type %s, which has no %s instance in scope", ci.Class.Name, owners[i], f.Name, f.Type, ci.Class.Name)
				}
				row = append(row, d)
			}
			dicts = append(dicts, row)
		}
		for _, m := range ci.Methods {
			m.Derived.FieldDicts = dicts
		}
	}
	c.typeParams = nil
	c.inPrelude = false
}
