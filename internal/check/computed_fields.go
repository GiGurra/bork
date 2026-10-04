package check

import (
	"reflect"
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// Computed recipes bind sibling identities, not promises about a nominal value.
func (c *checker) computedFieldDefault(field *Field) {
	source := field.Decl.Default
	if field.defaultBase != nil {
		c.ensureFieldDefault(field.defaultBase)
		if field.defaultBase.defaultSyntax != nil {
			source = field.defaultBase.defaultSyntax
		}
	}
	field.defaultSyntax = c.cloneComputedSyntax(source, field.defaultBound)
	c.typeParams = field.defaultTypes
	for _, sibling := range field.siblings {
		param := &syntax.Param{Pos: sibling.Decl.Pos, Name: sibling.Name}
		field.defaultParams = append(field.defaultParams, param)
		c.bind(sibling.Name, param.Pos, sibling.Type, param)
	}
	boundary := &Func{Pkg: field.Pkg, Decl: &syntax.FuncDecl{Name: "computed " + field.Name, Params: field.defaultParams}, Result: field.Type}
	for _, sibling := range field.siblings {
		boundary.Params = append(boundary.Params, sibling.Type)
	}
	c.fn = boundary
	typ := c.fieldInitializer(field.defaultSyntax, field)
	field.DefaultCalls = append([]*Func(nil), boundary.Calls...)
	if len(boundary.Needs) != 0 {
		c.errorf(source.Position(), "computed field %s cannot require ambient values", field.Name)
	}
	for i, param := range field.defaultParams {
		if local := c.lookup(param.Name); local != nil && local.decl == param && local.used {
			field.Dependencies = append(field.Dependencies, field.siblings[i])
		}
	}
	field.Computed = len(field.Dependencies) != 0
	if !field.Computed {
		c.errorf(source.Position(), "an independent lazy field default must be a closed value")
		return
	}
	if metadata := c.info.lazyFields[field.defaultSyntax]; metadata != nil {
		metadata.Kind = "computed field"
		for _, dependency := range field.Dependencies {
			metadata.Dependencies = append(metadata.Dependencies, dependency.Name)
		}
	}
	if metadata := c.info.lazyFields[field.defaultSyntax]; metadata != nil && metadata.Effects != "nothing" {
		c.errorf(source.Position(), "computed field %s must be pure, found uses %s", field.Name, metadata.Effects)
	}
	if typ, want := c.settle(typ, field.Type); typ != Invalid && !assignable(typ, want) {
		c.errorf(source.Position(), "the default of %s must be %s, found %s", field.Name, want, typ)
	}
	c.info.fieldDefaults[field] = field.defaultSyntax
	if c.sharedDefaults == nil {
		c.sharedDefaults = map[syntax.Expr]bool{}
	}
	c.sharedDefaults[field.defaultSyntax] = true
}

// Every specialization gets fresh syntax identities and concrete written types.
func (c *checker) cloneComputedSyntax(source syntax.Expr, bound map[*TypeParam]Type) syntax.Expr {
	var clone func(reflect.Value) reflect.Value
	clone = func(value reflect.Value) reflect.Value {
		switch value.Kind() {
		case reflect.Pointer:
			if value.IsNil() {
				return value
			}
			out := reflect.New(value.Type().Elem())
			out.Elem().Set(clone(value.Elem()))
			if original, ok := value.Interface().(*syntax.TypeExpr); ok {
				if typ := c.info.writtenTypes[original]; typ != nil {
					c.info.assemblyTypes[out.Interface().(*syntax.TypeExpr)] = subst(typ, bound)
				}
			}
			return out
		case reflect.Interface:
			if value.IsNil() {
				return value
			}
			out := reflect.New(value.Type()).Elem()
			out.Set(clone(value.Elem()))
			return out
		case reflect.Struct:
			out := reflect.New(value.Type()).Elem()
			out.Set(value)
			for _, index := range walkableSyntaxFields(value.Type()) {
				out.Field(index).Set(clone(value.Field(index)))
			}
			return out
		case reflect.Slice:
			if value.IsNil() {
				return value
			}
			out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
			for i := range value.Len() {
				out.Index(i).Set(clone(value.Index(i)))
			}
			return out
		}
		return value
	}
	return clone(reflect.ValueOf(source)).Interface().(syntax.Expr)
}

func (c *checker) checkComputedCycles(fields []*Field) {
	if len(fields) == 0 || fields[0].cycleChecked {
		return
	}
	for _, field := range fields {
		field.cycleChecked = true
	}
	states := map[*Field]int{}
	var path []*Field
	var visit func(*Field)
	visit = func(field *Field) {
		if states[field] == 2 {
			return
		}
		if states[field] == 1 {
			start := 0
			for path[start] != field {
				start++
			}
			names := []string{}
			for _, member := range path[start:] {
				names = append(names, member.Name)
			}
			names = append(names, field.Name)
			c.errorf(field.Decl.Pos, "computed field dependency cycle: %s", strings.Join(names, " -> "))
			return
		}
		states[field] = 1
		path = append(path, field)
		for _, dependency := range field.Dependencies {
			if dependency.Computed {
				visit(dependency)
			}
		}
		path = path[:len(path)-1]
		states[field] = 2
	}
	for _, field := range fields {
		if field.Computed {
			visit(field)
		}
	}
}

// Copy the complete recipe tree so each construction has its own variables,
// pattern identities and nested recipes. Predicate-only substitution is too
// narrow for runtime defaults (blocks, callbacks and local returns are legal).
func instantiateComputedDefault(info *Info, value Expr, bound map[*Var]Expr) Expr {
	return transformComputedDefault(info, value, func(ref *VarRef) Expr { return bound[ref.Var] })
}

func transformComputedDefault(info *Info, value Expr, replace func(*VarRef) Expr) Expr {
	exprType := reflect.TypeFor[Expr]()
	stmtType := reflect.TypeFor[Stmt]()
	owned := map[reflect.Type]bool{}
	for _, sample := range []any{(*Var)(nil), (*VarSource)(nil), (*Pat)(nil), (*PatField)(nil), (*FieldValue)(nil), (*FieldUpdate)(nil), (*MatchArm)(nil)} {
		owned[reflect.TypeOf(sample)] = true
	}
	copies := map[any]reflect.Value{}
	var clone func(reflect.Value) reflect.Value
	clone = func(v reflect.Value) reflect.Value {
		switch v.Kind() {
		case reflect.Interface:
			if v.IsNil() {
				return v
			}
			if !v.Type().Implements(exprType) && !v.Type().Implements(stmtType) {
				return v
			}
			out := reflect.New(v.Type()).Elem()
			out.Set(clone(v.Elem()))
			return out
		case reflect.Pointer:
			if v.IsNil() || !v.Type().Implements(exprType) && !v.Type().Implements(stmtType) && !owned[v.Type()] {
				return v
			}
			if ref, ok := v.Interface().(*VarRef); ok {
				if replacement := replace(ref); replacement != nil {
					return reflect.ValueOf(replacement)
				}
			}
			// Closed compile-time recipes have no sibling references to replace.
			// Preserve their identity so every instantiation sees the baked result.
			if _, ok := v.Interface().(*Comptime); ok {
				return v
			}
			if old, ok := copies[v.Interface()]; ok {
				return old
			}
			out := reflect.New(v.Type().Elem())
			copies[v.Interface()] = out
			out.Elem().Set(v.Elem())
			for i := range v.Elem().NumField() {
				if v.Elem().Type().Field(i).IsExported() {
					out.Elem().Field(i).Set(clone(v.Elem().Field(i)))
				}
			}
			if source, ok := v.Interface().(Expr); ok && info != nil {
				if metadata := info.fieldRecipes[source]; metadata != nil {
					info.fieldRecipes[out.Interface().(Expr)] = metadata
				}
			}
			return out
		case reflect.Slice:
			if v.IsNil() {
				return v
			}
			out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
			for i := range v.Len() {
				out.Index(i).Set(clone(v.Index(i)))
			}
			return out
		}
		return v
	}
	return clone(reflect.ValueOf(value)).Interface().(Expr)
}

func completedCandidate(value Expr) *Var {
	variable := &Var{Name: "_completed", Pos: value.Pos(), Type: value.Type(), Kind: VarLet, Unvalidated: true}
	variable.Let = &Let{Pos: value.Pos(), Var: variable, Value: value}
	return variable
}

func computedRecipe(info *Info, field *Field, template Expr, root Expr) Expr {
	bound := map[*Var]Expr{}
	for _, param := range field.DefaultVars {
		sibling := param.Sibling
		bound[param] = &Select{expr: expr{pos: template.Pos(), typ: sibling.Type}, X: root, Name: sibling.Name, Field: sibling}
	}
	return instantiateComputedDefault(info, template, bound)
}

func unvalidatedDefaultRoot(value Expr) bool {
	switch value := value.(type) {
	case *VarRef:
		return value.Var.Unvalidated
	case *Select:
		return unvalidatedDefaultRoot(value.X)
	}
	return false
}

// Whole-sibling invalidation is conservative: every explicit nested update
// changes its ancestors, even if the replacement happens to compare equal.
func (l *lowerer) refreshComputedCopy(value *Copy) {
	type group struct {
		fields  []*Field
		path    []string
		changed map[string]bool
	}
	groups := map[string]*group{}
	var ordered []*group
	for _, update := range value.Updates {
		record := value.X.Type().(*Record)
		var prefix []string
		for _, name := range update.Path {
			key := strings.Join(prefix, ".")
			current := groups[key]
			if current == nil {
				current = &group{fields: record.Fields, path: append([]string(nil), prefix...), changed: map[string]bool{}}
				groups[key] = current
				ordered = append(ordered, current)
			}
			current.changed[name] = true
			prefix = append(prefix, name)
			record, _ = record.Field(name).Type.(*Record)
		}
	}
	for _, current := range ordered {
		for progress := true; progress; {
			progress = false
			for _, field := range current.fields {
				if !field.Computed || current.changed[field.Name] {
					continue
				}
				for _, dependency := range field.Dependencies {
					if current.changed[dependency.Name] {
						current.changed[field.Name] = true
						progress = true
						break
					}
				}
			}
		}
		for _, field := range current.fields {
			if !field.Computed || !current.changed[field.Name] {
				continue
			}
			if value.Candidate == nil {
				value.Candidate = completedCandidate(value)
			}
			var root Expr = &VarRef{expr: value.expr, Var: value.Candidate}
			record := value.X.Type().(*Record)
			for _, name := range current.path {
				parent := record.Field(name)
				root = &Select{expr: expr{pos: value.Pos(), typ: parent.Type}, X: root, Name: name, Field: parent}
				record, _ = parent.Type.(*Record)
			}
			source := l.info.fieldDefaults[field]
			template := l.expr(source)
			recipe := computedRecipe(l.info, field, template, root)
			metadata := l.info.lazyFields[source]
			thunk := &Lambda{expr: expr{pos: source.Position(), typ: &FuncType{Result: field.Type}}, Body: recipe}
			path := append(append([]string(nil), current.path...), field.Name)
			value.Updates = append(value.Updates, &FieldUpdate{Path: path, Field: field, Value: recipe, Thunk: thunk, Lazy: metadata})
			l.info.fieldRecipes[recipe] = metadata
		}
	}
}

// Closed syntax has no lexical binders. A field path otherwise resembles a
// qualified variant head to the ordinary closed-value classifier.
func closedDefaultUsesSibling(value syntax.Expr, siblings []*Field) bool {
	switch value := value.(type) {
	case *syntax.Ident:
		return findField(siblings, value.Name) != nil
	case *syntax.Selector:
		return closedDefaultUsesSibling(value.X, siblings)
	case *syntax.RecordLit:
		for _, field := range value.Fields {
			if closedDefaultUsesSibling(field.Value, siblings) {
				return true
			}
		}
	case *syntax.ListLit:
		for _, item := range value.Elems {
			if closedDefaultUsesSibling(item, siblings) {
				return true
			}
		}
	case *syntax.MapLit:
		for i, key := range value.Keys {
			if closedDefaultUsesSibling(key, siblings) || closedDefaultUsesSibling(value.Values[i], siblings) {
				return true
			}
		}
	}
	return false
}

// ComputedFieldInitializer rebuilds a checked recipe at a runtime data boundary.
// It does not change checker metadata, so cached programs remain immutable while
// independent emitters generate Go from the same checked result.
func ComputedFieldInitializer(field *Field, rootName string, rootType Type) *Lambda {
	root := &Var{Name: rootName, GoName: rootName, Type: rootType, Kind: VarLet}
	rootRef := &VarRef{expr: expr{pos: field.Default.Pos(), typ: rootType}, Var: root}
	value := computedRecipe(nil, field, field.Default, rootRef)
	return &Lambda{expr: expr{pos: field.Default.Pos(), typ: &FuncType{Result: field.Type}}, Body: value}
}
