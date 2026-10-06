package check

import (
	"fmt"
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
	info               *Info
	values             map[*PackageBinding]bool
	calls              map[*Func]bool
	fields             map[*Field]bool
	instances          map[*Func][]*Instance
	constructionFields map[string]bool
	exhausted          *bool
	remaining          *int
}

func newPackageDependencies(info *Info) packageDependencies {
	remaining := 100000
	return packageDependencies{info: info, values: map[*PackageBinding]bool{}, calls: map[*Func]bool{}, fields: map[*Field]bool{}, instances: map[*Func][]*Instance{}, constructionFields: map[string]bool{}, exhausted: new(bool), remaining: &remaining}
}

func (dependencies packageDependencies) instance(instance *Instance) {
	if instance == nil {
		return
	}
	if *dependencies.exhausted {
		return
	}
	*dependencies.remaining--
	if *dependencies.remaining < 0 {
		*dependencies.exhausted = true
		return
	}
	dependencies.calls[instance.Func] = true
	active := false
	for _, previous := range dependencies.instances[instance.Func] {
		if sameDependencyInstance(previous, instance) {
			active = true
			break
		}
	}
	if instance.Func != nil && len(instance.TypeArgs) != 0 && !active {
		previous := dependencies.instances[instance.Func]
		if len(previous) >= 64 {
			*dependencies.exhausted = true
			return
		}
		dependencies.instances[instance.Func] = append(previous, instance)
		bound := bindParams(instance.Func.TypeParams, instance.TypeArgs)
		dependencies.specializedTree(instance.Func.Body, bound, instance.Dicts)
		dependencies.instances[instance.Func] = previous
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
				if len(method.TypeParams) != 0 && len(method.TypeParams) == len(dictionary.TypeArgs) {
					dependencies.instance(&Instance{Func: method, TypeArgs: dictionary.TypeArgs, Dicts: dictionary.Args})
				}
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

// Follow implicit construction calls under the same generic bindings as the
// calling function; a predicate dictionary may be concrete only at this point.
func (dependencies packageDependencies) specializedTree(root Expr, bound map[*TypeParam]Type, dictionaries ...[]*Dict) {
	var available []*Dict
	if len(dictionaries) != 0 {
		available = dictionaries[0]
	}
	WalkComptime(root, func(node Expr) bool {
		if *dependencies.exhausted {
			return false
		}
		switch node := node.(type) {
		case *Interp:
			for _, value := range node.Exprs {
				dependencies.render(subst(value.Type(), bound), map[Type]bool{})
			}
		case *CallBuiltin:
			if node.Builtin == BuiltinShapeMetadata {
				dependencies.metadata(substituteRequirementDict(node.Dictionary, bound, available), subst(TypeArgs(node.Type())[0], bound))
			}
			if node.Builtin == BuiltinShapeFinish && node.Construction != nil {
				dependencies.construction(node.Construction, bound)
			}
			if node.Builtin == BuiltinShapeValidate && node.Validation != nil {
				dependencies.validation(node.Validation, bound)
			}
			if node.Builtin == BuiltinToString || node.Builtin == BuiltinPrintln || node.Builtin == BuiltinEprintln || node.Builtin == BuiltinDbg {
				for _, value := range node.Args {
					dependencies.render(subst(value.Type(), bound), map[Type]bool{})
				}
			}
		case *Call:
			dependencies.specializedInstance(node.Inst, bound, available)
		case *FuncRef:
			dependencies.specializedInstance(node.Inst, bound, available)
		}
		return true
	})
}

func (dependencies packageDependencies) construction(layout *ShapeConstruction, bound map[*TypeParam]Type) {
	owner := subst(layout.Owner, bound)
	constraints := func(facts []*Constraint, subject Type) {
		for _, fact := range substConstraints(facts, bound) {
			dependencies.constraint(fact, subject)
		}
	}
	for _, field := range layout.Fields {
		if field.Default != nil {
			key := fmt.Sprintf("%p:%s", field, typeKey(owner))
			if !dependencies.constructionFields[key] {
				dependencies.constructionFields[key] = true
				dependencies.tree(field.Default)
				if len(bound) != 0 {
					dependencies.specializedTree(field.Default, bound)
				}
			}
		}
		constraints(field.Constraints, subst(field.Type, bound))
	}
	constraints(TypeConstraints(layout.Owner), owner)
	constraints(variantConstraints(layout.Variant), owner)
	constraints(layout.Constraints, owner)
}

func (dependencies packageDependencies) specializedInstance(instance *Instance, bound map[*TypeParam]Type, available []*Dict) {
	if instance == nil {
		return
	}
	specialized := *instance
	specialized.TypeArgs = make([]Type, len(instance.TypeArgs))
	for i, typ := range instance.TypeArgs {
		specialized.TypeArgs[i] = subst(typ, bound)
	}
	specialized.Dicts = make([]*Dict, len(instance.Dicts))
	for i, dictionary := range instance.Dicts {
		specialized.Dicts[i] = substituteRequirementDict(dictionary, bound, available)
	}
	dependencies.instance(&specialized)
}

func (dependencies packageDependencies) tree(root Expr) {
	WalkComptime(root, func(node Expr) bool {
		if *dependencies.exhausted {
			return false
		}
		switch node := node.(type) {
		case *Interp:
			for _, value := range node.Exprs {
				dependencies.render(value.Type(), map[Type]bool{})
			}
		case *CallBuiltin:
			if node.Builtin == BuiltinShapeMetadata {
				dependencies.metadata(node.Dictionary, TypeArgs(node.Type())[0])
			}
			if node.Builtin == BuiltinShapeFinish && node.Construction != nil {
				dependencies.construction(node.Construction, nil)
			}
			if node.Builtin == BuiltinShapeValidate && node.Validation != nil {
				dependencies.validation(node.Validation, nil)
			}
			if node.Builtin == BuiltinToString || node.Builtin == BuiltinPrintln || node.Builtin == BuiltinEprintln || node.Builtin == BuiltinDbg {
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
		if function.Derived != nil && IsCodec(function.Of.Class, "Decode") {
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
		if *dependencies.exhausted {
			c.errorf(function.Decl.Pos, "generic package dependency expansion exceeds the work or specialization depth limit")
		}
		graph[function] = dependencies
		function.RuntimePackageReads = *dependencies.exhausted || len(dependencies.values) != 0
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
		if *dependencies.exhausted {
			c.errorf(field.Default.Pos(), "generic field default dependency expansion exceeds the work or specialization depth limit")
		}
		field.RuntimePackageReads = *dependencies.exhausted || len(dependencies.values) != 0
		for callee := range dependencies.calls {
			field.RuntimePackageReads = field.RuntimePackageReads || (callee != nil && callee.RuntimePackageReads)
		}
	}
	for _, binding := range c.info.PackageBindings {
		dependencies := newPackageDependencies(c.info)
		dependencies.tree(binding.Value.Value)
		if *dependencies.exhausted {
			c.errorf(binding.Decl.Pos, "generic package value dependency expansion exceeds the work or specialization depth limit")
		}
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
	if *dependencies.exhausted {
		return true
	}
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
	if *dependencies.exhausted {
		return true
	}
	for function := range dependencies.calls {
		if function != nil && function.RuntimePackageReads {
			return true
		}
	}
	return false
}

func (dependencies packageDependencies) validation(layout *ShapeFieldValidation, bound map[*TypeParam]Type) {
	subject := subst(layout.Field.Type, bound)
	for _, constraint := range substConstraints(layout.Constraints, bound) {
		dependencies.constraint(constraint, subject)
	}
}

// A metadata query invokes only the initializer for its resolved key. Other
// dictionary methods and metadata providers remain lazy.
func (dependencies packageDependencies) metadata(dictionary *Dict, key Type) {
	if dictionary == nil || dictionary.Inst == nil {
		return
	}
	bound := bindParams(dictionary.Inst.TypeParams, dictionary.TypeArgs)
	for _, initializer := range dictionary.Inst.Metadata {
		if identical(subst(initializer.Result, bound), key) {
			dependencies.instance(&Instance{Func: initializer, TypeArgs: dictionary.TypeArgs, Dicts: dictionary.Args})
		}
	}
}

func sameDependencyInstance(left, right *Instance) bool {
	if len(left.TypeArgs) != len(right.TypeArgs) || len(left.Dicts) != len(right.Dicts) {
		return false
	}
	for i, argument := range left.TypeArgs {
		if !identical(argument, right.TypeArgs[i]) {
			return false
		}
	}
	seen := map[[2]*Dict]bool{}
	var sameDictionary func(*Dict, *Dict) bool
	sameDictionary = func(left, right *Dict) bool {
		if left == right {
			return true
		}
		if left == nil || right == nil || left.Class != right.Class || left.Inst != right.Inst || left.Param != right.Param || left.Builtin != right.Builtin || !identical(left.Type, right.Type) || len(left.TypeArgs) != len(right.TypeArgs) || len(left.Args) != len(right.Args) {
			return false
		}
		pair := [2]*Dict{left, right}
		if seen[pair] {
			return true
		}
		seen[pair] = true
		for i, argument := range left.TypeArgs {
			if !identical(argument, right.TypeArgs[i]) {
				return false
			}
		}
		for i, argument := range left.Args {
			if !sameDictionary(argument, right.Args[i]) {
				return false
			}
		}
		return true
	}
	for i, dictionary := range left.Dicts {
		if !sameDictionary(dictionary, right.Dicts[i]) {
			return false
		}
	}
	return true
}
