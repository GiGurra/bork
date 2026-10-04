package check

import (
	"fmt"
	"sort"
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
	GoFieldDecoders []*Dict
	Name            string
	Decl            *syntax.InstanceDecl
	Pkg             *Package
	Prelude         bool
	Class           *Class
	TypeParams      []*TypeParam
	Type            Type
	// Methods holds the implementations, in the class's method order.
	Methods []*Func
	// Constraints are those of a constrained instance's type
	// (`Decode[Int where positive]`). Such an instance is used only for
	// values known to satisfy them.
	Constraints []*Constraint
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
	// Or built in: Eq for comparable types, or Show for every value.
	Builtin bool
}

// IsEq reports whether class is the prelude's Eq, which every type
// whose values can be compared has, built in.
func IsEq(class *Class) bool { return class.Prelude && class.Name == "Eq" }

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
			fn.Effects = c.effectsOf(md.Uses)
			fn.Result = c.resolveType(md.Result)
			names := map[string]diag.Pos{}
			for _, p := range md.Params {
				if prev, exists := names[p.Name]; exists {
					c.errorf(p.Pos, "parameter %s is already declared at %s", p.Name, prev)
				}
				names[p.Name] = p.Pos
				fn.Params = append(fn.Params, c.resolveType(p.Type))
			}
			c.openSignature(fn)
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
		if c.hasFacts(p.Type) {
			c.errorf(p.Type.Pos, "where clauses on the methods of classes and instances are not supported yet")
			c.signatureReported(md)
			return
		}
	}
	if c.hasFacts(md.Result) {
		c.errorf(md.Result.Pos, "where clauses on the methods of classes and instances are not supported yet")
		c.signatureReported(md)
	}
}

// signatureReported marks the facts in md's signature as reported.
func (c *checker) signatureReported(md *syntax.FuncDecl) {
	for _, p := range md.Params {
		c.whereReported(p.Type)
	}
	c.whereReported(md.Result)
}

// noParamWhere rejects where clauses on an instance method's
// parameters: calls through the class could not prove them. (Results
// may promise facts; a constrained instance's must.)
func (c *checker) noParamWhere(md *syntax.FuncDecl, instType *syntax.TypeExpr, method *Func, param *TypeParam) {
	for i, p := range md.Params {
		assumed := i < len(method.Params) && method.Params[i] == Type(param) && sameWrittenType(p.Type, instType)
		if c.hasFacts(p.Type) && !assumed {
			c.errorf(p.Type.Pos, "where clauses on the parameters of instance methods are not supported (an instance for a constrained type assumes its constraints)")
			for _, p := range md.Params {
				c.whereReported(p.Type)
			}
			return
		}
	}
}

// sameWrittenType reports whether a and b are the same plain type name
// (the type of a constrained instance, whose constraints its methods
// assume).
func sameWrittenType(a, b *syntax.TypeExpr) bool {
	return a != nil && b != nil && a.Name == b.Name && a.Union == nil && b.Union == nil && a.Func == nil && b.Func == nil &&
		len(a.Args) == 0 && len(b.Args) == 0 && len(a.Where) == 0 && len(b.Where) == 0
}

// instanceConstraints resolves the constraints of constrained instances'
// types. Their methods assume them of parameters of the type, and must
// promise them of results of it.
func (c *checker) instanceConstraints() {
	for _, ci := range c.info.ClassInstances {
		if ci.Decl == nil || ci.Decl.Type == nil {
			continue
		}
		c.pkg, c.inPrelude = ci.Pkg, ci.Prelude
		cons := c.constraintsOf(ci.Decl.Type, ci.Type, nil)
		if len(cons) == 0 {
			continue
		}
		if IsShow(ci.Class) {
			c.errorf(ci.Decl.Type.Pos, "Show instances cannot require facts: the renderer must work for every value of the declared type")
			continue
		}
		for _, con := range cons {
			if con.Path != "" {
				c.errorf(ci.Decl.Type.Pos, "an instance's type can only be constrained as a whole, as in Decode[Int where positive]")
				cons = nil
				break
			}
		}
		ci.Constraints = cons
		param := ci.Class.Param
		for i, m := range ci.Methods {
			cm := ci.Class.Methods[i]
			for j, p := range cm.Params {
				if p == Type(param) && j < len(m.ParamConstraints) {
					m.ParamConstraints[j] = append(m.ParamConstraints[j], cons...)
				}
			}
			if !mentionsMember(cm.Result, param) {
				continue
			}
			var promised []*Constraint
			for _, mc := range m.ResultConstraints {
				if identical(mc.Type, ci.Type) {
					promised = mc.Constraints
				}
			}
			if missing := missingConstraints(promised, cons); len(missing) > 0 {
				pos := m.Decl.Pos
				if m.Decl.Result != nil {
					pos = m.Decl.Result.Pos
				}
				c.errorf(pos, "method %s of instance %s must promise %s for its %s result, since the instance is for %s where %s: write %s where %s in its result type",
					m.Decl.Name, ci.Name, constraintsText(missing, c.pkg), ci.Type, ci.Type, constraintsText(cons, c.pkg), ci.Type, constraintsText(cons, c.pkg))
			}
		}
	}
	c.inPrelude = false
}

// mentionsMember reports whether t is tp, or a union with tp as a
// member.
func mentionsMember(t Type, tp *TypeParam) bool {
	if t == Type(tp) {
		return true
	}
	if u, ok := t.(*Union); ok {
		for _, m := range u.Members {
			if m == Type(tp) {
				return true
			}
		}
	}
	return false
}

// missingConstraints lists the constraints of need that have does not
// include.
func missingConstraints(have, need []*Constraint) []*Constraint {
	var out []*Constraint
	for _, n := range need {
		found := false
		for _, h := range have {
			if h.Path == n.Path && h.Text(nil) == n.Text(nil) {
				found = true
			}
		}
		if !found {
			out = append(out, n)
		}
	}
	return out
}

func constraintsText(cons []*Constraint, from *Package) string {
	parts := make([]string, len(cons))
	for i, con := range cons {
		parts[i] = con.Text(from)
	}
	return strings.Join(parts, " and ")
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
	if IsGoStruct(cl) {
		c.errorf(id.ClassPos, "GoStruct instances must be derived for records")
		return
	}
	if cl != nil && IsEq(cl) {
		c.errorf(id.ClassPos, "Eq is built in: every type whose values can be compared has it, with structural ==")
		return
	}
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
	if cl.Prelude && (cl.Name == "Decode" || cl.Name == "Encode") && containsOpaque(ci.Type, map[Type]bool{}) {
		c.bindErr(id.Pos, "%s cannot be defined for %s, which holds an opaque Go value", cl.Name, ci.Type)
		return
	}
	if IsShow(cl) && !c.validShow(ci) {
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
		c.noParamWhere(md, id.Type, m, cl.Param)
		fn := &Func{Decl: md, Pkg: c.pkg, Prelude: prelude, Of: ci, TypeParams: ci.TypeParams}
		fn.Effects = c.effectsOf(md.Uses)
		fn.Result = c.resolveType(md.Result)
		for _, p := range md.Params {
			fn.Params = append(fn.Params, c.resolveType(p.Type))
		}
		c.openSignature(fn)
		want := &FuncType{Result: subst(m.Result, bound), Effects: m.Effects}
		for _, p := range m.Params {
			want.Params = append(want.Params, subst(p, bound))
		}
		if !sameSignature(fn.funcType(), want) {
			c.errorf(md.Pos, "method %s of instance %s must be %s, to match class %s for %s, but is %s", md.Name, id.Name, want, cl.Name, ci.Type, fn.funcType())
		} else if extra := fn.Effects &^ m.Effects; extra != 0 {
			c.diags.AddCode(md.Uses.Pos, "effect.instance", "method %s of instance %s uses %s, but class %s allows %s", md.Name, id.Name, extra, cl.Name, allowedText(m.Effects))
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
	c.declareBundles(files)
	for _, f := range files {
		if f.Prelude {
			continue
		}
		c.inFile(f)
		for _, b := range f.Bundles {
			// Resolved even if unused, for its errors.
			if declared := c.pkg.bundles[b.Name]; declared != nil && declared.decl == b {
				c.bundleInstances(declared)
			}
		}
		for _, u := range f.Uses {
			for _, ci := range c.useItem(u) {
				c.pkg.inScope = addInstance(c.pkg.inScope, ci)
			}
		}
	}
}

func sortedBundleNames(pkg *Package) []string {
	var names []string
	for name := range pkg.bundles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// bundle is a named set of instances: `instances Json { ... }`.
type bundle struct {
	decl  *syntax.Bundle
	pkg   *Package
	state int // 1 while its items are resolved, 2 when done
	insts []*ClassInstance
}

func (c *checker) declareBundles(files []*syntax.File) {
	for _, f := range files {
		if len(f.Bundles) == 0 {
			continue
		}
		c.inFile(f)
		if c.pkg.bundles == nil {
			c.pkg.bundles = map[string]*bundle{}
		}
		for _, b := range f.Bundles {
			if other := c.pkg.bundles[b.Name]; other != nil {
				c.errorf(b.Pos, "instances %s is already declared at %s", b.Name, other.decl.Pos)
				continue
			}
			if ci := c.ownInstance(b.Name); ci != nil {
				c.errorf(b.Pos, "%s is already the name of an instance, declared at %s", b.Name, ci.Decl.Pos)
				continue
			}
			c.pkg.bundles[b.Name] = &bundle{decl: b, pkg: c.pkg}
		}
	}
}

func (c *checker) ownInstance(name string) *ClassInstance {
	for _, ci := range c.pkg.instances {
		if ci.Name == name {
			return ci
		}
	}
	return nil
}

func addInstance(list []*ClassInstance, ci *ClassInstance) []*ClassInstance {
	for _, have := range list {
		if have == ci {
			return list
		}
	}
	return append(list, ci)
}

// bundleInstances resolves the instances of a bundle, in its own
// package.
func (c *checker) bundleInstances(b *bundle) []*ClassInstance {
	switch b.state {
	case 1:
		c.errorf(b.decl.Pos, "instances %s includes itself (sets of instances cannot include each other in a circle)", b.decl.Name)
		return nil
	case 2:
		return b.insts
	}
	b.state = 1
	saved := c.pkg
	c.pkg = b.pkg
	for _, item := range b.decl.Items {
		for _, ci := range c.useItem(item) {
			b.insts = addInstance(b.insts, ci)
		}
	}
	c.pkg = saved
	b.state = 2
	return b.insts
}

// useItem resolves what `use` names: an instance, a set of instances,
// or every exported instance of a package (pkg.*).
func (c *checker) useItem(u *syntax.Use) []*ClassInstance {
	pkg, name, ok := c.qualified(u.Name)
	if !ok {
		if ci := c.ownInstance(u.Name); ci != nil {
			return []*ClassInstance{ci}
		}
		if b := c.pkg.bundles[u.Name]; b != nil {
			return c.bundleInstances(b)
		}
		if i := strings.IndexByte(u.Name, '.'); i >= 0 {
			c.errorf(u.Pos, "%s is not an imported package", u.Name[:i])
		} else {
			c.errorf(u.Pos, "unknown instance %s (an instance of another package is used as pkg.name)", u.Name)
		}
		return nil
	}
	if name == "*" {
		var out []*ClassInstance
		for _, ci := range pkg.instances {
			if Exported(ci.Name) {
				out = append(out, ci)
			}
		}
		return out
	}
	for _, ci := range pkg.instances {
		if ci.Name == name {
			if !Exported(name) {
				c.errorf(u.Pos, "%s is not exported by package %s (only names starting with an upper-case letter are)", name, pkg.Path)
				return nil
			}
			return []*ClassInstance{ci}
		}
	}
	if b := pkg.bundles[name]; b != nil {
		if !Exported(name) {
			c.errorf(u.Pos, "instances %s is not exported by package %s (only names starting with an upper-case letter are)", name, pkg.Path)
			return nil
		}
		return c.bundleInstances(b)
	}
	c.errorf(u.Pos, "package %s has no instance or set of instances %s", pkg.Path, name)
	return nil
}

// resolveDicts decides the instances a call of fn (or a use of it as a
// value) needs: the class's instance for a method, and one for each
// bound of a generic function's type parameters.
func (c *checker) resolveDicts(inst *Instance, pos diag.Pos) bool {
	return c.resolveDictsWith(inst, pos, nil)
}

// resolveDictsWith is resolveDicts, where have holds (per type
// parameter) what is known of its values, for constrained instances.
func (c *checker) resolveDictsWith(inst *Instance, pos diag.Pos, have [][]*Constraint) bool {
	fn := inst.Func
	ok := true
	haveFor := func(i int) []*Constraint {
		if i < len(have) {
			return have[i]
		}
		return nil
	}
	defer func() { c.have = nil }()
	if fn.Class != nil {
		c.have = haveFor(0)
		d := c.dict(fn.Class, inst.TypeArgs[0], pos, 0)
		ok = d != nil
		inst.Dicts = []*Dict{d}
		return ok
	}
	for i, tp := range fn.TypeParams {
		for _, b := range tp.Bounds {
			c.have = haveFor(i)
			d := c.dict(b, inst.TypeArgs[i], pos, 0)
			ok = ok && d != nil
			inst.Dicts = append(inst.Dicts, d)
		}
	}
	return ok
}

// promisesArgFacts checks that a call with constrained type arguments
// (json.Decode[Port]) can promise their constraints for results of
// those types. By parametricity, a generic function gets values of a
// type parameter only from its arguments (which must satisfy the
// constraints) and from the instances of the parameter's bounds, which
// must then be constrained instances that promise them.
func (c *checker) promisesArgFacts(inst *Instance, argFacts [][]*Constraint, name string, pos diag.Pos) bool {
	fn := inst.Func
	// Values of the type that come in any other way than as arguments
	// of exactly the type (from a function argument, say) are not
	// checked.
	for i, tp := range fn.TypeParams {
		if len(argFacts[i]) == 0 {
			continue
		}
		for j, p := range fn.Params {
			if p != Type(tp) && mentionsParam(p, tp) {
				c.errorf(pos, "%s cannot take a constrained type argument for %s: its parameter %s could give it values that are not checked (only parameters of type %s itself are)", name, tp.Name, fn.Decl.Params[j].Name, tp.Name)
				return false
			}
		}
		// The result keeps the facts only where it is the type itself
		// (or a member of a result union).
		members := []Type{fn.Result}
		if u, ok := fn.Result.(*Union); ok {
			members = u.Members
		}
		for _, m := range members {
			if m != Type(tp) && mentionsParam(m, tp) {
				c.errorf(pos, "%s cannot take a constrained type argument for %s: its result holds %s inside %s, where the facts would not be kept (only a result of type %s itself keeps them)", name, tp.Name, tp.Name, m, tp.Name)
				return false
			}
		}
	}
	k := 0
	for i, tp := range fn.TypeParams {
		bounds := tp.Bounds
		if fn.Class != nil {
			bounds = []*Class{fn.Class}
		}
		for _, b := range bounds {
			d := inst.Dicts[k]
			k++
			if len(argFacts[i]) == 0 || d == nil || !producesParam(b) {
				continue
			}
			if d.Inst == nil || len(missingConstraints(d.Inst.Constraints, argFacts[i])) > 0 {
				which := "the " + b.Name + " instance in use"
				if d.Inst != nil {
					which = qualify(d.Inst.Name, d.Inst.Pkg, c.pkg)
				}
				c.errorf(pos, "%s cannot promise that its %s values are %s: they may come from %s, which does not promise it (declare an instance for %s where %s)",
					name, inst.TypeArgs[i], constraintsText(argFacts[i], c.pkg), which, inst.TypeArgs[i], constraintsText(argFacts[i], c.pkg))
				return false
			}
		}
	}
	return true
}

// producesParam reports whether a method of class can give values of
// the class's type (decode can; encode and equals cannot).
func producesParam(class *Class) bool {
	for _, m := range class.Methods {
		if mentionsMember(m.Result, class.Param) {
			return true
		}
	}
	return false
}

// declaredFacts lists the constraints declared for the value of x: a
// parameter's or a binding's where clause, or a record field's.
func (c *checker) declaredFacts(x syntax.Expr) []*Constraint {
	x = debugSyntaxValue(x)
	var cons []*Constraint
	switch x := x.(type) {
	case *syntax.Ident:
		switch d := c.info.defs[x].(type) {
		case *syntax.Binding:
			cons = c.info.bindingConstraints[d]
		case *syntax.Param:
			if c.fn != nil {
				for i, p := range c.fn.Decl.Params {
					if p == d && i < len(c.fn.ParamConstraints) {
						cons = c.fn.ParamConstraints[i]
					}
				}
			}
		}
	case *syntax.Selector:
		if rec, ok := c.info.types[x.X].(*Record); ok {
			if fd := rec.Field(x.Name); fd != nil {
				cons = fd.Constraints
			}
		}
	}
	var out []*Constraint
	for _, con := range cons {
		if con.Path == "" {
			out = append(out, con)
		}
	}
	return out
}

// dict finds the instance of class for type t, reporting an error at
// pos if there is none, or more than one.
func (c *checker) dict(class *Class, t Type, pos diag.Pos, depth int) *Dict {
	if class.Prelude && (class.Name == "Decode" || class.Name == "Encode") && containsOpaque(t, map[Type]bool{}) {
		c.bindErr(pos, "%s cannot apply to %s, which holds an opaque Go value", class.Name, t)
		return nil
	}

	if t == Invalid || t == nil {
		return nil
	}
	if depth > 20 {
		c.errorf(pos, "cannot find an instance of %s for %s: the search does not end (instances refer to each other in a circle)", class.Name, t)
		return nil
	}
	if IsShow(class) {
		if tp, ok := t.(*TypeParam); ok && c.inScopeParam(tp) && tp.HasBound(class) {
			return &Dict{Class: class, Type: t, Param: tp}
		}
		return &Dict{Class: class, Type: t, Builtin: true}
	}
	if tp, ok := t.(*TypeParam); ok && c.inScopeParam(tp) {
		if tp.HasBound(class) {
			return &Dict{Class: class, Type: t, Param: tp}
		}
		c.errorf(pos, "%s needs an instance of %s for %s; require one: [%s: %s]", c.useText(), class.Name, tp.Name, tp.Name, class.Name)
		return nil
	}
	if IsEq(class) {
		if !comparable(t) {
			c.errorf(pos, "values of type %s cannot be compared, so it has no Eq (functions, scopes, and resources have no ==)", t)
			return nil
		}
		return &Dict{Class: class, Type: t, Builtin: true}
	}
	var matches []*Dict
	var partial []*ClassInstance // the head fits, but a bound does not
	for _, ci := range c.pkg.inScope {
		if ci.Class != class {
			continue
		}
		if len(ci.Constraints) > 0 && (depth > 0 || len(missingConstraints(c.have, ci.Constraints)) > 0) {
			continue // the value is not known to satisfy them
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
	matches = mostSpecific(matches)
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

// mostSpecific drops the instances that another one is more specific
// than: one for `Int where positive` over one for Int.
func mostSpecific(ds []*Dict) []*Dict {
	var out []*Dict
	for _, d := range ds {
		beaten := false
		for _, other := range ds {
			if other != d && len(other.Inst.Constraints) > len(d.Inst.Constraints) &&
				len(missingConstraints(other.Inst.Constraints, d.Inst.Constraints)) == 0 {
				beaten = true
			}
		}
		if !beaten {
			out = append(out, d)
		}
	}
	return out
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
	// Sets of instances first: they are what packages suggest.
	for _, pkg := range c.info.Packages {
		if pkg == c.pkg {
			continue
		}
		for _, name := range sortedBundleNames(pkg) {
			if !Exported(name) {
				continue
			}
			for _, ci := range pkg.bundles[name].insts {
				if _, ok := matchHead(ci, t); ok && ci.Class == class {
					found = append(found, "use "+qualify(name, pkg, c.pkg))
					break
				}
			}
		}
	}
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
	return class.Prelude && (class.Name == "Decode" || class.Name == "Encode" || IsGoStruct(class))
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
				case cl == nil && name == "Show":
					c.errorf(td.DerivePos, "Show is not needed: toString shows every value")
					continue
				case cl == nil:
					c.errorf(td.DerivePos, "unknown class %s", name)
					continue
				case IsEq(cl):
					c.errorf(td.DerivePos, "Eq is built in: every type whose values can be compared has it, with no derive needed")
					continue
				case !derivable(cl):
					c.errorf(td.DerivePos, "%s cannot be derived; only Decode, Encode and GoStruct can (yet)", name)
					continue
				}
				if IsGoStruct(cl) {
					if r, ok := e.typ.(*Record); ok && r.Decl != nil && r.Decl.Private && r.Pkg != c.pkg {
						continue
					}
					if r, ok := e.typ.(*Record); !ok || !r.GoStruct {
						continue
					}
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
		tp := &TypeParam{Name: p.Name, Decl: p.Decl}
		if !IsGoStruct(cl) {
			tp.Bounds = []*Class{cl}
		}
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
		fn := &Func{Decl: fd, Pkg: c.pkg, Prelude: prelude, Of: ci, TypeParams: tps, Result: subst(m.Result, bound), Effects: m.Effects, Derived: &Derived{}}
		for _, p := range m.Params {
			fn.Params = append(fn.Params, subst(p, bound))
		}
		ci.Methods = append(ci.Methods, fn)
		c.info.FuncOf[fd] = fn
	}
	c.pkg.instances = append(c.pkg.instances, ci)
	c.info.ClassInstances = append(c.info.ClassInstances, ci)
}

// A provisional derivation is available while defaults are checked, but a
// rejected one must not participate in delegation or instance selection.
func (c *checker) discardDerived(ci *ClassInstance) {
	without := func(instances []*ClassInstance) []*ClassInstance {
		out := make([]*ClassInstance, 0, len(instances))
		for _, instance := range instances {
			if instance != ci {
				out = append(out, instance)
			}
		}
		return out
	}
	c.info.ClassInstances = without(c.info.ClassInstances)
	ci.Pkg.instances = without(ci.Pkg.instances)
	for _, pkg := range c.pkgs {
		pkg.inScope = without(pkg.inScope)
	}
	for _, method := range ci.Methods {
		delete(c.info.FuncOf, method.Decl)
	}
}

// resolveDerived finds the instances the fields of derived instances'
// types need, now that it is known which instances are in scope.
func (c *checker) resolveDerived() {
	for _, ci := range c.info.ClassInstances {
		if !IsGoStruct(ci.Class) && (len(ci.Methods) == 0 || ci.Methods[0].Derived == nil) {
			continue
		}
		if IsGoStruct(ci.Class) {
			if r, ok := ci.Type.(*Record); !ok || !r.GoStruct {
				c.discardDerived(ci)
				continue
			}
		}
		c.pkg, c.inPrelude = ci.Pkg, ci.Prelude
		if private := c.foreignPrivateRepresentation(ci.Type, ci.Class, ci.Pkg, map[Type]bool{}, false); private != nil {
			owner := strings.TrimSuffix(ci.Name, ci.Class.Name)
			switch t := private.(type) {
			case *Sealed:
				c.errorf(ci.Decl.Pos, "cannot derive %s for %s: %s has private variants in package %s; use an instance provided by that package", ci.Class.Name, owner, t.Name, t.Pkg.Path)
			case *Record:
				c.errorf(ci.Decl.Pos, "cannot derive %s for %s: package %s controls construction of %s; use an instance provided by that package", ci.Class.Name, owner, t.Pkg.Path, t.Name)
			}
			c.discardDerived(ci)
			continue
		}
		if IsGoStruct(ci.Class) {
			c.resolveGoStructDecoders(ci)
			continue
		}
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
				if f.Computed {
					row = append(row, nil)
					continue
				}
				// The field's where clause selects constrained instances.
				c.have = nil
				for _, con := range f.Constraints {
					if con.Path == "" {
						c.have = append(c.have, con)
					}
				}
				saved := c.diags
				c.diags = &diag.List{}
				d := c.dict(ci.Class, f.Type, ci.Decl.Pos, 0)
				why := c.diags.Sorted()
				c.diags = saved
				c.have = nil
				if d == nil {
					if len(why) > 0 && strings.HasPrefix(why[0].Msg, "more than one") {
						c.errorf(ci.Decl.Pos, "cannot derive %s for %s: field %s: %s", ci.Class.Name, owners[i], f.Name, why[0].Msg)
					} else {
						c.errorf(ci.Decl.Pos, "cannot derive %s for %s: field %s has type %s, which has no %s instance in scope (%s)", ci.Class.Name, owners[i], f.Name, f.Type, ci.Class.Name, c.instanceHint(ci.Class, f.Type))
					}
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

// Derivation must not expose or construct another package's private variants,
// including variants nested in records, generic specializations, or containers.
// An owner-provided field codec is an explicit boundary: the derive delegates
// instead of inspecting that type's private representation.
func (c *checker) foreignPrivateRepresentation(t Type, class *Class, from *Package, seen map[Type]bool, fieldBoundary bool) Type {
	if t == nil || seen[t] {
		return nil
	}
	seen[t] = true
	if fieldBoundary {
		var owner *Package
		switch t := t.(type) {
		case *Record:
			owner = t.Pkg
		case *Sealed:
			owner = t.Pkg
		}
		// Owner-derived codecs may be declared later in the file traversal.
		// Their dictionaries are resolved after all derivations are declared.
		if record, ok := t.(*Record); ok && record.Decl != nil {
			for _, derived := range record.Decl.Derive {
				if derived != class.Name {
					continue
				}
				delegates := true
				for _, arg := range TypeArgs(record) {
					trial := map[Type]bool{}
					for typ, value := range seen {
						trial[typ] = value
					}
					if c.foreignPrivateRepresentation(arg, class, from, trial, true) != nil {
						delegates = false
					}
				}
				if delegates {
					return nil
				}
			}
		}
		for _, ci := range c.info.ClassInstances {
			if owner == nil || ci.Pkg != owner || ci.Class != class {
				continue
			}
			args, ok := matchHead(ci, t)
			if !ok {
				continue
			}
			delegates := true
			for i, tp := range ci.TypeParams {
				for _, bound := range tp.Bounds {
					if bound != class {
						continue
					}
					trial := map[Type]bool{}
					for t, v := range seen {
						trial[t] = v
					}
					if c.foreignPrivateRepresentation(args[i], class, from, trial, true) != nil {
						delegates = false
					}
				}
			}
			if delegates {
				return nil
			}
		}
	}
	var children []Type
	switch t := t.(type) {
	case *Sealed:
		if t.Pkg != from {
			for _, v := range t.Variants {
				if !Exported(v.Name) {
					return t
				}
			}
		}
		for _, v := range t.Variants {
			for _, f := range v.Fields {
				if !f.Computed {
					children = append(children, f.Type)
				}
			}
		}
	case *Record:
		if t.Decl != nil && t.Decl.Private && t.Pkg != from && (IsGoStruct(class) || class.Name == "Decode") {
			return t
		}
		for _, f := range t.Fields {
			if !f.Computed {
				children = append(children, f.Type)
			}
		}
	case *Seq:
		children = append(children, t.Elem)
	case *List:
		children = append(children, t.Elem)
	case *Map:
		children = append(children, t.Key, t.Value)
	case *Union:
		children = append(children, t.Members...)
	case *FuncType:
		children = append(children, t.Params...)
		children = append(children, t.Result)
	}
	for _, child := range children {
		if sealed := c.foreignPrivateRepresentation(child, class, from, seen, true); sealed != nil {
			return sealed
		}
	}
	return nil
}
