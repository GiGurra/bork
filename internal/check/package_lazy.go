package check

import (
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// PackageBinding is a transparent immutable value, memoized on first read.
type PackageBinding struct {
	Decl         *syntax.Binding
	Pkg          *Package
	Type         Type
	Var          *Var
	Value        *Let
	Boundary     *Func
	Dependencies []*PackageBinding
	state        int
}

func (c *checker) declarePackageBindings(files []*syntax.File) {
	saved := *c
	defer func() { *c = saved }()
	c.scopes = nil
	for _, file := range files {
		c.inFile(file)
		for _, declaration := range file.Bindings {
			if previous := c.pkg.bindings[declaration.Name]; previous != nil {
				c.errorf(declaration.Pos, "package value %s is already declared at %s", declaration.Name, previous.Decl.Pos)
				continue
			}
			if c.nameTaken(declaration.Name, declaration.Pos) {
				continue
			}
			if c.pkg.bindings == nil {
				c.pkg.bindings = map[string]*PackageBinding{}
			}
			binding := &PackageBinding{Decl: declaration, Pkg: c.pkg}
			c.pkg.bindings[declaration.Name] = binding
			c.info.PackageBindings = append(c.info.PackageBindings, binding)
		}
	}
}

func (c *checker) packageBindingNamed(name string) *PackageBinding {
	if pkg, member, qualified := c.qualified(name); qualified {
		if !Exported(member) {
			return nil
		}
		return pkg.bindings[member]
	}
	return c.pkg.bindings[name]
}

func (c *checker) ensurePackageBinding(binding *PackageBinding) Type {
	if binding.state == 2 {
		return binding.Type
	}
	if binding.state == 1 {
		names := []string{}
		start := 0
		for start < len(c.packagePath) && c.packagePath[start] != binding {
			start++
		}
		for _, member := range c.packagePath[start:] {
			names = append(names, qualify(member.Decl.Name, member.Pkg, nil))
		}
		names = append(names, binding.Decl.Name)
		c.errorf(binding.Decl.Pos, "package value dependency cycle: %s", strings.Join(names, " -> "))
		return Invalid
	}
	binding.state = 1
	defer func() { binding.state = 2 }()
	saved := *c
	c.packagePath = append(append([]*PackageBinding(nil), c.packagePath...), binding)
	c.pkg, c.inPrelude = binding.Pkg, false
	c.scopes = []map[string]*local{{}}
	c.typeParams, c.lambdaDepth, c.have = nil, 0, nil
	c.session = nil
	c.initializerContext, c.comptimeContext = nil, nil
	c.used = 0
	boundary := &Func{Pkg: binding.Pkg, Decl: &syntax.FuncDecl{Pos: binding.Decl.Pos, Name: "package value " + binding.Decl.Name, Body: &syntax.Block{Pos: binding.Decl.Pos}}}
	binding.Boundary = boundary
	c.fn = boundary
	defer func() {
		shared, solved, mapKeys := c.sharedDefaults, c.solved, c.mapKeyChecks
		*c = saved
		c.sharedDefaults, c.solved, c.mapKeyChecks = shared, solved, mapKeys
	}()
	var wanted Type
	if binding.Decl.Type != nil {
		wanted = c.resolveType(binding.Decl.Type)
	}
	actual := c.valueInitializer(binding.Decl, wanted, "package value initializer")
	if actual == Ok || actual == Never {
		c.errorf(binding.Decl.Pos, "package value %s must produce a value", binding.Decl.Name)
		actual = Invalid
	}
	if wanted != nil {
		if actual, wanted = c.settle(actual, wanted); actual != Invalid && wanted != Invalid && !assignable(actual, wanted) {
			c.errorf(binding.Decl.Pos, "%s must be %s, found %s", binding.Decl.Name, wanted, actual)
		}
		actual = wanted
	}
	binding.Type = actual
	boundary.Result = actual
	c.info.bindings[binding.Decl] = actual
	if binding.Decl.Type != nil && actual != Invalid {
		c.info.bindingConstraints[binding.Decl] = c.constraintsOf(binding.Decl.Type, actual, nil)
	}
	metadata := c.info.lazyBindings[binding.Decl]
	metadata.Kind = "package binding"
	if metadata.Effects != "nothing" {
		c.errorf(binding.Decl.Pos, "package value %s requires a pure initializer, found uses %s", binding.Decl.Name, metadata.Effects)
	}
	if len(boundary.Needs) != 0 {
		c.errorf(binding.Decl.Pos, "package value %s cannot require ambient values", binding.Decl.Name)
	}
	return actual
}

func (c *checker) packageBindingRead(node *syntax.Ident, binding *PackageBinding) Type {
	typ := c.ensurePackageBinding(binding)
	c.info.defs[node] = binding.Decl
	if len(c.packagePath) != 0 {
		owner := c.packagePath[len(c.packagePath)-1]
		seen := false
		for _, dependency := range owner.Dependencies {
			seen = seen || dependency == binding
		}
		if !seen {
			owner.Dependencies = append(owner.Dependencies, binding)
		}
	}
	return typ
}

type packageDependencies struct {
	info      *Info
	values    map[*PackageBinding]bool
	calls     map[*Func]bool
	fields    map[*Field]bool
	instances map[*Func]bool
}

func newPackageDependencies(info *Info) packageDependencies {
	return packageDependencies{info: info, values: map[*PackageBinding]bool{}, calls: map[*Func]bool{}, fields: map[*Field]bool{}, instances: map[*Func]bool{}}
}

func (dependencies packageDependencies) instance(instance *Instance) {
	if instance == nil {
		return
	}
	dependencies.calls[instance.Func] = true
	if instance.Func != nil && len(instance.TypeArgs) != 0 && !dependencies.instances[instance.Func] {
		dependencies.instances[instance.Func] = true
		bound := bindParams(instance.Func.TypeParams, instance.TypeArgs)
		WalkComptime(instance.Func.Body, func(node Expr) bool {
			switch node := node.(type) {
			case *Interp:
				for _, value := range node.Exprs {
					dependencies.render(subst(value.Type(), bound), map[Type]bool{})
				}
			case *CallBuiltin:
				if node.Builtin == BuiltinToString || node.Builtin == BuiltinPrintln || node.Builtin == BuiltinDbg {
					for _, value := range node.Args {
						dependencies.render(subst(value.Type(), bound), map[Type]bool{})
					}
				}
			case *Call:
				dependencies.specializedInstance(node.Inst, bound)
			case *FuncRef:
				dependencies.specializedInstance(node.Inst, bound)
			}
			return true
		})
		delete(dependencies.instances, instance.Func)
	}
	seen := map[*Dict]bool{}
	var dict func(*Dict)
	dict = func(dictionary *Dict) {
		if dictionary == nil || seen[dictionary] {
			return
		}
		seen[dictionary] = true
		if dictionary.Inst != nil {
			for _, method := range dictionary.Inst.Methods {
				dependencies.calls[method] = true
			}
		}
		for _, argument := range dictionary.Args {
			dict(argument)
		}
	}
	for _, dictionary := range instance.Dicts {
		dict(dictionary)
	}
}

func (dependencies packageDependencies) specializedInstance(instance *Instance, bound map[*TypeParam]Type) {
	if instance == nil {
		return
	}
	specialized := *instance
	specialized.TypeArgs = make([]Type, len(instance.TypeArgs))
	for i, typ := range instance.TypeArgs {
		specialized.TypeArgs[i] = subst(typ, bound)
	}
	dependencies.instance(&specialized)
}

func (dependencies packageDependencies) tree(root Expr) {
	WalkComptime(root, func(node Expr) bool {
		switch node := node.(type) {
		case *Interp:
			for _, value := range node.Exprs {
				dependencies.render(value.Type(), map[Type]bool{})
			}
		case *CallBuiltin:
			if node.Builtin == BuiltinToString || node.Builtin == BuiltinPrintln || node.Builtin == BuiltinDbg {
				for _, value := range node.Args {
					dependencies.render(value.Type(), map[Type]bool{})
				}
			}
		case *VarRef:
			if node.Var.PackageBinding != nil {
				dependencies.values[node.Var.PackageBinding] = true
			}
		case *Call:
			dependencies.calls[node.Func] = true
			dependencies.instance(node.Inst)
		case *FuncRef:
			dependencies.instance(node.Inst)
		case *Select:
			if node.Field != nil && node.Field.Computed && !dependencies.fields[node.Field] {
				dependencies.fields[node.Field] = true
				dependencies.tree(node.Field.Default)
			}
		case *RecordLit:
			if node.Variant != nil {
				for _, constraint := range node.Variant.Constraints {
					dependencies.constraint(constraint, node.Type())
				}
			}
			for _, constraint := range node.Constraints {
				dependencies.constraint(constraint, node.Type())
			}
			for _, constraint := range TypeConstraints(node.Type()) {
				dependencies.constraint(constraint, node.Type())
			}
			for _, field := range node.Fields {
				for _, constraint := range field.Field.Constraints {
					dependencies.constraint(constraint, field.Field.Type)
				}
			}
		}
		return true
	})
}

// Rendering calls nominal Show methods even when no explicit call appears in
// the source. Structural renderers recurse through independent data fields.
func (dependencies packageDependencies) render(typ Type, seen map[Type]bool) {
	if typ == nil || seen[typ] {
		return
	}
	seen[typ] = true
	nominal := func(typ Type) Type {
		switch typ := typ.(type) {
		case *Record:
			if typ.Base != nil {
				return typ.Base
			}
		case *Sealed:
			if typ.Base != nil {
				return typ.Base
			}
		}
		return typ
	}
	if dependencies.info != nil {
		for _, instance := range dependencies.info.ClassInstances {
			if IsShow(instance.Class) && nominal(instance.Type) == nominal(typ) {
				for _, method := range instance.Methods {
					dependencies.calls[method] = true
					if len(method.TypeParams) != 0 && len(method.Params) != 0 {
						dependencies.instance(method.InstanceFor(typ))
					}
				}
				return
			}
		}
	}
	fields := func(fields []*Field) {
		for _, field := range fields {
			if !field.Computed {
				dependencies.render(field.Type, seen)
			}
		}
	}
	switch typ := typ.(type) {
	case *Record:
		fields(typ.Fields)
	case *Sealed:
		for _, variant := range typ.Variants {
			fields(variant.Fields)
		}
	case *List:
		dependencies.render(typ.Elem, seen)
	case *Map:
		dependencies.render(typ.Key, seen)
		dependencies.render(typ.Value, seen)
	case *Union:
		for _, member := range typ.Members {
			dependencies.render(member, seen)
		}
	}
}

func (dependencies packageDependencies) constraint(constraint *Constraint, subject Type) {
	subjects := constraintSubjects(subject, constraint.Path)
	if constraint.Pred != nil {
		dependencies.calls[constraint.Pred] = true
		for _, typ := range subjects {
			instance := constraint.InstanceFor(typ)
			if instance != nil && dependencies.info != nil && dependencies.info.PredicateDicts(constraint.Pkg, instance) {
				dependencies.instance(instance)
			}
		}
	}
	for _, alternative := range constraint.Or {
		for _, typ := range subjects {
			dependencies.constraint(alternative, typ)
		}
	}
}

// A type-argument constraint applies at its path through fields/collections.
func constraintSubjects(subject Type, path string) []Type {
	subjects := []Type{subject}
	for _, step := range strings.Split(strings.TrimPrefix(path, "."), ".") {
		if step == "" {
			continue
		}
		var projected []Type
		fields := func(fields []*Field) {
			if field := findField(fields, step); field != nil {
				projected = append(projected, field.Type)
			}
		}
		for _, typ := range subjects {
			switch typ := typ.(type) {
			case *List:
				if step == "[]" {
					projected = append(projected, typ.Elem)
				}
			case *Seq:
				if step == "[]" {
					projected = append(projected, typ.Elem)
				}
			case *Record:
				fields(typ.Fields)
			case *Sealed:
				for _, variant := range typ.Variants {
					fields(variant.Fields)
				}
			}
		}
		subjects = projected
	}
	return subjects
}

// Resolve dependencies through pure helper bodies after every body is lowered.
// This also marks predicate boundaries that would read runtime cells.
func (c *checker) packageDependencyGraph() {
	if len(c.info.PackageBindings) == 0 {
		return
	}
	graph := map[*Func]packageDependencies{}
	c.info.packageGraph = graph
	for _, function := range c.info.FuncOf {
		dependencies := newPackageDependencies(c.info)
		dependencies.tree(function.Body)
		dependencies.tree(function.Requires)
		for _, callee := range function.Calls {
			dependencies.calls[callee] = true
		}
		if function.Derived != nil {
			for _, dictionaries := range function.Derived.FieldDicts {
				dependencies.instance(&Instance{Dicts: dictionaries})
			}
		}
		if function.Derived != nil && function.Of.Class.Name == "Decode" {
			for _, constraint := range TypeConstraints(function.Of.Type) {
				dependencies.constraint(constraint, function.Of.Type)
			}
			var groups [][]*Field
			switch typ := function.Of.Type.(type) {
			case *Record:
				groups = append(groups, typ.Fields)
			case *Sealed:
				for _, variant := range typ.Variants {
					for _, constraint := range variant.Constraints {
						dependencies.constraint(constraint, function.Of.Type)
					}
					groups = append(groups, variant.Fields)
				}
			}
			for _, fields := range groups {
				for _, field := range fields {
					if field.Default != nil {
						dependencies.tree(field.Default)
					}
					for _, constraint := range field.Constraints {
						dependencies.constraint(constraint, field.Type)
					}
				}
			}
		}
		graph[function] = dependencies
		function.RuntimePackageReads = len(dependencies.values) != 0
	}
	for changed := true; changed; {
		changed = false
		for function, dependencies := range graph {
			if function.RuntimePackageReads {
				continue
			}
			for callee := range dependencies.calls {
				if callee != nil && callee.RuntimePackageReads {
					function.RuntimePackageReads = true
					changed = true
					break
				}
			}
		}
	}
	for field := range c.info.fieldDefaults {
		dependencies := newPackageDependencies(c.info)
		dependencies.tree(field.Default)
		field.RuntimePackageReads = len(dependencies.values) != 0
		for callee := range dependencies.calls {
			field.RuntimePackageReads = field.RuntimePackageReads || (callee != nil && callee.RuntimePackageReads)
		}
	}
	for _, binding := range c.info.PackageBindings {
		dependencies := newPackageDependencies(c.info)
		dependencies.tree(binding.Value.Value)
		seen := map[*Func]bool{}
		var visit func(*Func)
		visit = func(function *Func) {
			if function == nil || seen[function] {
				return
			}
			seen[function] = true
			for value := range graph[function].values {
				dependencies.values[value] = true
			}
			for callee := range graph[function].calls {
				visit(callee)
			}
		}
		for callee := range dependencies.calls {
			visit(callee)
		}
		binding.Dependencies = nil
		metadata := c.info.lazyBindings[binding.Decl]
		metadata.Dependencies = nil
		for _, candidate := range c.info.PackageBindings {
			if dependencies.values[candidate] {
				binding.Dependencies = append(binding.Dependencies, candidate)
				metadata.Dependencies = append(metadata.Dependencies, qualify(candidate.Decl.Name, candidate.Pkg, binding.Pkg))
			}
		}
	}
	states := map[*PackageBinding]int{}
	var path []*PackageBinding
	var visit func(*PackageBinding)
	visit = func(binding *PackageBinding) {
		if states[binding] == 2 {
			return
		}
		if states[binding] == 1 {
			start := 0
			for path[start] != binding {
				start++
			}
			names := []string{}
			for _, member := range path[start:] {
				names = append(names, member.Pkg.Path+"."+member.Decl.Name)
			}
			names = append(names, binding.Pkg.Path+"."+binding.Decl.Name)
			c.errorf(binding.Decl.Pos, "package value dependency cycle: %s", strings.Join(names, " -> "))
			return
		}
		states[binding] = 1
		path = append(path, binding)
		for _, dependency := range binding.Dependencies {
			visit(dependency)
		}
		path = path[:len(path)-1]
		states[binding] = 2
	}
	for _, binding := range c.info.PackageBindings {
		visit(binding)
	}
}

func packageRuntimeReads(info *Info, value Expr) bool {
	dependencies := newPackageDependencies(info)
	dependencies.tree(value)
	if len(dependencies.values) != 0 {
		return true
	}
	for callee := range dependencies.calls {
		if callee != nil && callee.RuntimePackageReads {
			return true
		}
	}
	return false
}

// Generic predicates can receive runtime-reading instance methods even when
// their own body contains only a dictionary dispatch.
func packageInstanceRuntimeReads(info *Info, instance *Instance) bool {
	if instance == nil {
		return false
	}
	dependencies := newPackageDependencies(info)
	dependencies.instance(instance)
	for function := range dependencies.calls {
		if function != nil && function.RuntimePackageReads {
			return true
		}
	}
	return false
}
