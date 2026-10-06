package check

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// DeriveTemplate is source code expanded and type checked for each requested
// target. Its lexical package is independent of the package selecting instances.
type DeriveTemplate struct {
	Decl *syntax.InstanceDecl
	Pkg  *Package
}

func (c *checker) declareDeriveTemplates(files []*syntax.File) {
	for _, file := range files {
		c.inFile(file)
		c.markDeriveImports(file)
		for _, decl := range file.Templates {
			class := c.lookupClass(decl.Class)
			if class != nil {
				c.noteDeriveSource(decl.ClassPos, decl.Class, class.Decl.Pos, "class")
			}
			switch {
			case class == nil:
				c.errorf(decl.ClassPos, "unknown class %s", decl.Class)
			case class.Pkg != c.pkg:
				c.errorf(decl.Pos, "declare the derive template for %s in the class's package %s", decl.Class, deriveOwner(class.Pkg))
			case class.Template != nil:
				c.errorf(decl.Pos, "derive template for %s is already declared at %s", decl.Class, class.Template.Decl.Pos)
			case len(decl.TypeParams) != 1 || len(decl.TypeParams[0].Bounds) != 0 || decl.Type.Name != decl.TypeParams[0].Name || len(decl.Type.Args) != 0:
				c.errorf(decl.Pos, "a derive template must have one unconstrained target parameter, as in derive instance labels[T]: Labels[T]")
			default:
				seen := map[string]bool{}
				valid := true
				for _, method := range decl.Methods {
					if method.IsGo() || method.Body == nil || len(method.TypeParams) != 0 {
						c.errorf(method.Pos, "derive template methods must have Bork bodies and no method type parameters")
						valid = false
					}
					expected := class.Method(method.Name)
					if expected == nil || seen[method.Name] {
						c.errorf(method.Pos, "unexpected or duplicate method %s in derive template for %s", method.Name, class.Name)
						valid = false
					}
					seen[method.Name] = true
				}
				for _, method := range class.Methods {
					if !seen[method.Decl.Name] {
						c.errorf(decl.Pos, "derive template for %s is missing method %s", class.Name, method.Decl.Name)
						valid = false
					}
				}
				if valid {
					plan := &deriveExpansion{c: c, template: &DeriveTemplate{Decl: decl, Pkg: c.pkg}, env: map[string]any{decl.TypeParams[0].Name: class.Param}, budget: &deriveBudget{remaining: 100000}, names: map[string]bool{}}
					for _, method := range decl.Methods {
						c.noWhere(method)
						expected := class.Method(method.Name)
						if len(method.Params) != len(expected.Params) {
							c.errorf(method.Pos, "template method %s must match its class signature", method.Name)
							valid = false
							continue
						}
						for i, param := range method.Params {
							if param.Default != nil || param.In != "" {
								c.errorf(param.Pos, "template parameters cannot have defaults or scope annotations")
								valid = false
							}
							written := plan.clone(reflect.ValueOf(param.Type)).Interface().(*syntax.TypeExpr)
							if actual := c.resolveType(written); !identical(actual, expected.Params[i]) {
								c.errorf(param.Pos, "template parameter %s must match its class signature", param.Name)
								valid = false
							}
						}
						result := Ok
						if method.Result != nil {
							result = c.resolveType(plan.clone(reflect.ValueOf(method.Result)).Interface().(*syntax.TypeExpr))
						}
						if !identical(result, expected.Result) || c.effectsOf(method.Uses) != expected.Effects || method.Needs != nil || method.Requires != nil {
							c.errorf(method.Pos, "template method %s must match its class result, effects and requirements", method.Name)
							valid = false
						}
					}
				}
				if valid {
					class.Template = &DeriveTemplate{Decl: decl, Pkg: c.pkg}
					class.ForeignRecord = c.foreignRecordMetadata(class)
				}
			}
		}
	}
}

type shapeField struct {
	field   *Field
	index   int
	owner   Type
	variant *Variant
}

func (field shapeField) positional() bool {
	if field.variant != nil {
		return field.variant.Positional
	}
	record, ok := field.owner.(*Record)
	return ok && record.Tuple
}

type shapeVariant struct {
	variant     *Variant
	constraints []*Constraint
}
type shapeSequence struct {
	items   []any
	element Type
}
type shapeEnum string

// A plan has a bounded, deterministic metadata evaluator. It never invokes the
// ordinary native comptime evaluator or executes unsafe Go.
type deriveExpansion struct {
	c           *checker
	template    *DeriveTemplate
	target      Type
	instance    *ClassInstance
	active      map[*syntax.FuncDecl]bool
	scope       *Package
	names       map[string]bool
	env         map[string]any
	origins     map[string]diag.Pos
	typeFacts   map[string][]*Constraint
	budget      *deriveBudget
	failed      bool
	helperArgs  []syntax.Expr
	helperOrder []int
	patternTest bool
	// layout also evaluates literals of the shape layout records.
	layout bool
}

func (p *deriveExpansion) error(pos diag.Pos, format string, args ...any) {
	p.c.errorf(pos, format, args...)
	p.failed = true
}

type deriveBudget struct {
	remaining   int
	depth       int
	depthFailed bool
	frames      []*deriveBudgetFrame
}

type deriveBudgetFrame struct {
	start, peak int
	deferred    bool
}

func (budget *deriveBudget) observeDepth(depth int) {
	for _, frame := range budget.frames {
		if relative := depth - frame.start; relative > frame.peak {
			frame.peak = relative
		}
	}
}

func (p *deriveExpansion) enter(pos diag.Pos) bool {
	if !p.tick(pos) {
		return false
	}
	if p.budget.depth >= 256 {
		if !p.budget.depthFailed {
			p.error(pos, "derive template expansion exceeds its compile-time depth limit")
			p.budget.depthFailed = true
		}
		return false
	}
	p.budget.depth++
	p.budget.observeDepth(p.budget.depth)
	return true
}

func (p *deriveExpansion) tick(pos diag.Pos) bool {
	return p.charge(pos, 1)
}

func (p *deriveExpansion) charge(pos diag.Pos, work int) bool {
	before := p.budget.remaining
	p.budget.remaining -= work
	if before > 0 && p.budget.remaining <= 0 {
		p.error(pos, "derive template expansion exceeds its compile-time work limit")
	}
	return p.budget.remaining > 0
}

func (p *deriveExpansion) shapeCall(name string) string {
	alias, member, ok := strings.Cut(name, ".")
	if !ok {
		return ""
	}
	pkg := p.template.Pkg.imports[alias]
	if pkg == nil || pkg.Path != "bork/shape" {
		return ""
	}
	p.template.Pkg.used[alias] = true
	return member
}

func (p *deriveExpansion) projectedTypeArg(t *syntax.TypeExpr) (Type, []*Constraint) {
	written := p.clone(reflect.ValueOf(t)).Interface().(*syntax.TypeExpr)
	saved := p.c.pkg
	p.c.pkg = p.template.Pkg
	defer func() { p.c.pkg = saved }()
	typ := p.c.resolveType(written)
	facts := p.c.constraintsOf(written, typ, p.c.paramScope())
	p.c.whereReported(t) // The metadata query consumed these source obligations.
	return typ, facts
}

func (p *deriveExpansion) chooseMatch(x *syntax.Match) (syntax.Expr, bool) {
	value, ok := p.eval(x.X)
	if !ok {
		p.error(x.Pos, "comptime match requires a compile-time value")
		return nil, false
	}
	var selected syntax.Expr
	for _, arm := range x.Arms {
		matches := false
		switch pattern := arm.Pattern.(type) {
		case *syntax.WildcardPat:
			matches = true
		case *syntax.LitPat:
			literal, known := p.eval(pattern.Value)
			if !known {
				p.error(pattern.Pos, "comptime match literal must be a supported compile-time value")
			} else {
				matches, _ = p.metadataEqual(&syntax.Binary{Pos: pattern.Pos}, value, literal)
			}
		case *syntax.VariantPat:
			if kind, yes := value.(shapeEnum); yes && len(pattern.Fields) == 0 {
				name := strings.Join(pattern.Path, ".")
				member := p.shapeCall(name)
				if pattern.Context && len(pattern.Path) == 1 {
					member = pattern.Path[0]
				}
				if member != "Record" && member != "Sealed" && member != "Other" {
					p.error(pattern.Pos, "unknown shape kind pattern %s", name)
				}
				matches = string(kind) == member
			} else {
				p.error(pattern.Pos, "comptime match variant patterns require a shape kind")
			}
		default:
			p.error(pattern.Position(), "comptime match patterns must be literals, shape kinds, or a wildcard")
		}
		if matches && selected == nil {
			selected = arm.Body
		}
	}
	if selected != nil {
		return selected, true
	}
	p.error(x.Pos, "comptime match has no arm for its compile-time value")
	return nil, false
}

func (p *deriveExpansion) eval(x syntax.Expr) (any, bool) {
	if x == nil {
		return nil, false
	}
	if !p.enter(x.Position()) {
		return nil, false
	}
	defer func() { p.budget.depth-- }()
	switch x := x.(type) {
	case *syntax.Block:
		return p.evalBlock(x)
	case *syntax.If:
		if x.Comptime {
			chosen, known := p.condition(x.Cond)
			if !known {
				return nil, false
			}
			if chosen {
				return p.evalBlock(x.Then)
			}
			if x.Else != nil {
				return p.eval(x.Else)
			}
		}
	case *syntax.Match:
		if x.Comptime {
			if selected, ok := p.chooseMatch(x); ok {
				return p.eval(selected)
			}
		}
	case *syntax.StringLit:
		return x.Value, true
	case *syntax.BoolLit:
		return x.Value, true
	case *syntax.IntLit:
		n, err := strconv.ParseInt(x.Text, 0, 64)
		return n, err == nil
	case *syntax.RecordLit:
		if p.layout {
			return p.layoutRecord(x)
		}
	case *syntax.ListLit:
		if p.layout {
			return p.layoutList(x)
		}
	case *syntax.Ident:
		if kind := p.shapeCall(x.Name); kind == "Record" || kind == "Sealed" || kind == "Other" {
			return shapeEnum(kind), true
		}
		if p.layout {
			if value, known := p.layoutConstant(x); known {
				return value, true
			}
		}
		value, ok := p.env[x.Name]
		if origin, present := p.origins[x.Name]; ok && present {
			p.c.noteDeriveSource(x.Pos, x.Name, origin, "variable")
		}
		if _, runtime := value.(shapeRuntimeType); runtime {
			return nil, false
		}
		return value, ok
	case *syntax.Selector:
		if id, yes := x.X.(*syntax.Ident); yes && x.Name == "Type" {
			if runtime, known := p.env[id.Name].(shapeRuntimeType); known {
				return runtime.typ, true
			}
		}
		if p.layout {
			if value, known := p.layoutConstant(x); known {
				return value, true
			}
		}
		value, ok := p.eval(x.X)
		if !ok {
			return nil, false
		}
		switch value := value.(type) {
		case shapeField:
			switch x.Name {
			case "name":
				if value.positional() {
					return "", true
				}
				return value.field.Name, true
			case "positional":
				return value.positional(), true
			case "index":
				return int64(value.index), true
			case "doc":
				return value.field.Doc, true
			case "computed":
				return value.field.Computed, true
			case "hasDefault":
				return value.field.Decl != nil && value.field.Decl.Default != nil, true
			case "Type", "RawType":
				return value.field.Type, true
			case "tags":
				return p.fieldTags(x.Pos, value.field)
			case "facts":
				return p.factSequence(x.Pos, value.owner, value.field, value.field.Constraints)
			}
		case shapeVariant:
			switch x.Name {
			case "Type":
				view := p.variantView(value.variant)
				return view, view != nil
			case "name":
				return value.variant.Name, true
			case "positional":
				return value.variant.Positional, true
			case "index":
				return int64(value.variant.Index), true
			case "fields":
				if !p.charge(x.Pos, len(value.variant.Fields)) {
					return nil, false
				}
				result := make([]any, len(value.variant.Fields))
				for i, f := range value.variant.Fields {
					result[i] = shapeField{field: f, index: i, owner: value.variant.Parent, variant: value.variant}
				}
				return shapeSequence{items: result, element: p.descriptorType("Field", value.variant.Parent)}, true
			case "facts":
				return p.factSequence(x.Pos, value.variant.Parent, nil, value.variant.Constraints)
			}
		case shapeFact:
			switch x.Name {
			case "text":
				return p.factText(x.Pos, value.constraint)
			case "path":
				return value.constraint.Path, true
			case "independent":
				return value.field != nil && !value.field.Computed && !value.constraint.HasSiblingArgs(), true
			}
		}
	case *syntax.Unary:
		value, ok := p.eval(x.X)
		if boolean, yes := value.(bool); ok && yes && x.Op == syntax.Not {
			return !boolean, true
		}
	case *syntax.Binary:
		left, ok := p.eval(x.X)
		if !ok {
			return nil, false
		}
		// Preserve short circuiting when an unselected metadata expression is invalid.
		if b, yes := left.(bool); yes {
			if x.Op == syntax.AndAnd && !b {
				return false, true
			}
			if x.Op == syntax.OrOr && b {
				return true, true
			}
		}
		right, ok := p.eval(x.Y)
		if !ok {
			return nil, false
		}
		switch x.Op {
		case syntax.Plus:
			leftText, leftString := left.(string)
			rightText, rightString := right.(string)
			if leftString && rightString {
				if !p.charge(x.Pos, len(leftText)+len(rightText)) {
					return nil, false
				}
				return leftText + rightText, true
			}
		case syntax.Eq:
			return p.metadataEqual(x, left, right)
		case syntax.NotEq:
			equal, valid := p.metadataEqual(x, left, right)
			return !equal, valid
		case syntax.AndAnd:
			a, aok := left.(bool)
			b, bok := right.(bool)
			return a && b, aok && bok
		case syntax.OrOr:
			a, aok := left.(bool)
			b, bok := right.(bool)
			return a || b, aok && bok
		}
	case *syntax.Call:
		if selector, ok := x.Fun.(*syntax.Selector); ok {
			if value, known := p.stringOperation(x, selector); known {
				return value, true
			}
		}
		if selector, ok := x.Fun.(*syntax.Selector); ok && (selector.Name == "length" || selector.Name == "isEmpty") {
			if receiver, known := p.eval(selector.X); known {
				if sequence, yes := receiver.(shapeSequence); yes {
					if len(x.Args) != 0 || len(x.TypeArgs) > 1 {
						p.error(x.Pos, "metadata sequence %s takes no arguments", selector.Name)
						return nil, false
					}
					if len(x.TypeArgs) == 1 {
						actual, facts := p.projectedTypeArg(x.TypeArgs[0])
						if !identical(actual, sequence.element) || len(facts) != 0 {
							p.error(x.Pos, "metadata sequence element type must be %s without additional runtime facts", sequence.element)
							return nil, false
						}
					}
					if selector.Name == "isEmpty" {
						return len(sequence.items) == 0, true
					}
					return int64(len(sequence.items)), true
				}
			}
		}
		id, ok := x.Fun.(*syntax.Ident)
		if ok {
			if helper, pkg := p.c.deriveHelperNamed(p.template.Pkg, id.Name); helper != nil {
				return p.evalHelper(x, helper, pkg)
			}
		}
		if !ok || len(x.TypeArgs) != 1 || len(x.Args) != 0 {
			return nil, false
		}
		member := p.shapeCall(id.Name)
		if member == "" {
			return nil, false
		}
		target, headFacts := p.projectedTypeArg(x.TypeArgs[0])
		if (member == "fields" || member == "variants" || member == "facts") && !p.accessible(target, x.Pos) {
			return nil, false
		}
		switch member {
		case "fields":
			record, ok := target.(*Record)
			if !ok {
				p.error(x.Pos, "shape.fields requires a record target, got %s", target)
				return nil, false
			}
			if !p.charge(x.Pos, len(record.Fields)) {
				return nil, false
			}
			result := make([]any, len(record.Fields))
			for i, field := range record.Fields {
				result[i] = shapeField{field: field, index: i, owner: target}
			}
			return shapeSequence{items: result, element: p.descriptorType("Field", target)}, true
		case "variants":
			sealed, ok := target.(*Sealed)
			if !ok {
				p.error(x.Pos, "shape.variants requires a sealed target, got %s", target)
				return nil, false
			}
			if !p.charge(x.Pos, len(sealed.Variants)) {
				return nil, false
			}
			result := make([]any, len(sealed.Variants))
			for i, variant := range sealed.Variants {
				result[i] = shapeVariant{variant: variant, constraints: headFacts}
			}
			return shapeSequence{items: result, element: p.descriptorType("Variant", target)}, true
		case "kind":
			switch target.(type) {
			case *Record:
				return shapeEnum("Record"), true
			case *Sealed:
				return shapeEnum("Sealed"), true
			default:
				return shapeEnum("Other"), true
			}
		case "positional":
			record, ok := target.(*Record)
			return ok && record.Tuple, true
		case "facts":
			return p.factSequence(x.Pos, target, nil, targetShapeFacts(target), headFacts)
		case "typeName":
			return target.String(), true
		case "name":
			switch target := target.(type) {
			case *Record:
				return target.Name, true
			case *Sealed:
				return target.Name, true
			}
			return target.String(), true
		case "owner":
			switch target := target.(type) {
			case *Record:
				if target.Pkg == nil {
					return "", true
				}
				return target.Pkg.Path, true
			case *Sealed:
				return target.Pkg.Path, true
			}
			return "", true
		}
	}
	return nil, false
}

func (p *deriveExpansion) metadataEqual(x *syntax.Binary, left, right any) (bool, bool) {
	if reflect.TypeOf(left) != reflect.TypeOf(right) {
		p.error(x.Pos, "metadata equality requires operands of the same type")
		return false, false
	}
	switch left.(type) {
	case bool, string, int64, shapeEnum:
		return left == right, true
	default:
		p.error(x.Pos, "metadata equality requires scalar operands")
		return false, false
	}
}

func (p *deriveExpansion) condition(x syntax.Expr) (bool, bool) {
	value, ok := p.eval(x)
	boolean, yes := value.(bool)
	if !ok || !yes {
		p.error(x.Position(), "comptime condition must be a compile-time Bool")
		return false, false
	}
	return boolean, true
}

func (p *deriveExpansion) literal(pos diag.Pos, value any) syntax.Expr {
	switch value := value.(type) {
	case string:
		return &syntax.StringLit{Pos: pos, Value: value}
	case bool:
		return &syntax.BoolLit{Pos: pos, Value: value}
	case int64:
		return &syntax.IntLit{Pos: pos, Text: strconv.FormatInt(value, 10)}
	case metadataList, metadataRecord, metadataVariant:
		return p.layoutLiteral(pos, value)
	default:
		p.error(pos, "shape metadata cannot escape into runtime code; use its properties inside a derive template")
		return &syntax.Block{Pos: pos}
	}
}

func (p *deriveExpansion) expr(x syntax.Expr) syntax.Expr {
	if x == nil {
		return nil
	}
	if !p.enter(x.Position()) {
		return &syntax.Block{Pos: x.Position()}
	}
	defer func() { p.budget.depth-- }()
	switch x := x.(type) {
	case *syntax.Block:
		saved := p.env
		savedNames := p.names
		savedOrigins := p.origins
		p.origins = make(map[string]diag.Pos, len(savedOrigins))
		for name, origin := range savedOrigins {
			p.origins[name] = origin
		}
		p.names = make(map[string]bool, len(savedNames))
		for name, present := range savedNames {
			p.names[name] = present
		}
		p.env = make(map[string]any, len(saved))
		for name, value := range saved {
			p.env[name] = value
		}
		defer func() { p.env = saved; p.names = savedNames; p.origins = savedOrigins }()
		out := &syntax.Block{Pos: x.Pos, End: x.End}
		for _, stmt := range x.Stmts {
			if binding, ok := stmt.(*syntax.Binding); ok && !binding.Lazy && binding.AsyncScope == nil {
				if value, known := p.eval(binding.Value); known {
					if _, exists := p.env[binding.Name]; exists {
						p.error(binding.Pos, "%s is already defined in an enclosing compile-time scope", binding.Name)
					}
					p.env[binding.Name] = value
					p.origins[binding.Name] = binding.Pos
					if metadataValue(value) {
						if binding.Type != nil {
							p.checkMetadataType(binding.Type, value, binding.Pos)
						}
						_, builtin := builtins[binding.Name]
						if p.names[binding.Name] || p.template.Pkg.imports[binding.Name] != nil || p.template.Pkg.Funcs[binding.Name] != nil || builtin {
							p.error(binding.Pos, "%s is already defined in an enclosing scope", binding.Name)
						}
						p.names[binding.Name] = true
						continue
					}
				}
			}
			expanded := p.clone(reflect.ValueOf(stmt)).Interface().(syntax.Stmt)
			out.Stmts = append(out.Stmts, expanded)
			if binding, ok := expanded.(*syntax.Binding); ok {
				if call, yes := binding.Value.(*syntax.Call); yes {
					if operation := p.c.info.shapeBuildCalls[call]; operation != nil && operation.operation == "create" {
						p.env[binding.Name] = shapeRuntimeType{operation.layout.Storage}
						p.origins[binding.Name] = binding.Pos
					}
				}
			}
			if binding, ok := stmt.(*syntax.Binding); ok {
				p.names[binding.Name] = true
			}
		}
		out.Tail = p.expr(x.Tail)
		return out
	case *syntax.Is:
		value := p.expr(x.X)
		saved := p.patternTest
		p.patternTest = true
		pattern := p.clone(reflect.ValueOf(x.Pattern)).Interface().(syntax.Pattern)
		p.patternTest = saved
		return &syntax.Is{Pos: x.Pos, End: x.End, X: value, Pattern: pattern}
	case *syntax.Match:
		if x.Comptime {
			if selected, ok := p.chooseMatch(x); ok {
				return p.expr(selected)
			}
			return &syntax.Block{Pos: x.Pos}
		}
	case *syntax.Call:
		if id, ok := x.Fun.(*syntax.Ident); ok {
			if p.shapeCall(id.Name) == "builder" {
				return p.builderCreation(x, nil)
			}
			if p.shapeCall(id.Name) == "fail" {
				if len(x.TypeArgs) != 0 || len(x.Args) != 1 {
					p.error(x.Pos, "shape.fail takes one compile-time String message")
				} else if message, known := p.eval(x.Args[0]); known {
					if text, yes := message.(string); yes {
						site := x.Pos
						if p.instance != nil && p.instance.Decl != nil {
							site = p.instance.Decl.Pos
						}
						p.error(site, "%s (template operation at %s)", text, x.Pos)
					} else {
						p.error(x.Pos, "shape.fail message must be a compile-time String")
					}
				} else {
					p.error(x.Pos, "shape.fail message must be a compile-time String")
				}
				return &syntax.Block{Pos: x.Pos}
			}

			if helper, pkg := p.c.deriveHelperNamed(p.template.Pkg, id.Name); helper != nil {
				if value, known := p.evalHelper(x, helper, pkg); known {
					return p.literal(x.Pos, value)
				}
				return p.runtimeHelper(x, helper, pkg)
			}
		}
		if selector, ok := x.Fun.(*syntax.Selector); ok {
			if selector.Name == "builder" {
				if value, known := p.eval(selector.X); known {
					if variant, yes := value.(shapeVariant); yes {
						return p.builderCreation(x, variant.variant, variant.constraints)
					}
				}
			}
			if expanded := p.fieldValidation(x, selector); expanded != nil {
				return expanded
			}
			if expanded := p.builderOperation(x, selector); expanded != nil {
				return expanded
			}
		}
		if selector, ok := x.Fun.(*syntax.Selector); ok && selector.Name == "project" {
			if value, known := p.eval(selector.X); known {
				if variant, yes := value.(shapeVariant); yes {
					return p.variantProject(x, variant.variant)
				}
			}
		}
		if selector, ok := x.Fun.(*syntax.Selector); ok && selector.Name == "read" {
			if value, known := p.eval(selector.X); known {
				if field, yes := value.(shapeField); yes {
					if len(x.Args) != 1 || len(x.TypeArgs) != 0 {
						p.error(x.Pos, "field.read takes its proven owner value")
						return &syntax.Block{Pos: x.Pos}
					}
					read := &syntax.Selector{Pos: selector.Pos, X: p.expr(x.Args[0]), Name: field.field.Name}
					if p.c.info.shapeReadOwners == nil {
						p.c.info.shapeReadOwners = map[*syntax.Selector]Type{}
					}
					if field.variant != nil {
						if p.c.info.shapeViewReads == nil {
							p.c.info.shapeViewReads = map[*syntax.Selector]*shapeViewRead{}
						}
						view := p.variantView(field.variant)
						if view == nil {
							return &syntax.Block{Pos: x.Pos}
						}
						p.c.info.shapeViewReads[read] = &shapeViewRead{variant: field.variant, field: field.field, view: view}
					} else {
						p.c.info.shapeReadOwners[read] = field.owner
					}
					return read
				}
			}
		}
		if selector, ok := x.Fun.(*syntax.Selector); ok && selector.Name == "default" {
			if value, known := p.eval(selector.X); known {
				if field, yes := value.(shapeField); yes {
					return p.fieldDefault(x, field)
				}
			}
		}
	case *syntax.ListLit:
		out := &syntax.ListLit{Pos: x.Pos}
		for _, item := range x.Elems {
			if loop, ok := item.(*syntax.For); ok && loop.Comptime && loop.Comprehension {
				out.Elems = append(out.Elems, p.iterate(loop, true)...)
			} else {
				out.Elems = append(out.Elems, p.expr(item))
			}
		}
		return out
	case *syntax.For:
		if x.Comptime {
			return &syntax.Block{Pos: x.Pos, Stmts: p.loopStatements(x)}
		}
		if value, ok := p.eval(x.Items); ok {
			if _, sequence := value.(shapeSequence); !sequence {
				break
			}
			p.error(x.Pos, "shape metadata cannot be iterated by a runtime for; add comptime")
			p.c.diags.Suggest(x.Pos, "type.error", x.Pos, diag.Fix{Message: "add comptime", Edits: []diag.TextEdit{{Start: x.Pos, End: x.Pos, Replacement: "comptime "}}})
		}
	case *syntax.If:
		if x.Comptime {
			chosen, ok := p.condition(x.Cond)
			if !ok {
				return &syntax.Block{Pos: x.Pos}
			}
			if chosen {
				return p.expr(x.Then)
			}
			if x.Else != nil {
				return p.expr(x.Else)
			}
			return &syntax.Block{Pos: x.Pos}
		}
	case *syntax.Comptime:
		p.error(x.Pos, "derive templates use typed comptime controls; native comptime blocks are not available during template expansion")
		return &syntax.Block{Pos: x.Pos}
	}
	// Fold only metadata references and intrinsic queries. Ordinary runtime
	// arithmetic and function calls retain their normal typing and execution.
	switch x.(type) {
	case *syntax.Selector, *syntax.Ident, *syntax.Call:
		if value, ok := p.eval(x); ok {
			if _, identifier := x.(*syntax.Ident); identifier && !metadataValue(value) {
				break
			}
			return p.literal(x.Position(), value)
		}
	}
	return p.clone(reflect.ValueOf(x)).Interface().(syntax.Expr)
}

func (p *deriveExpansion) loopStatements(loop *syntax.For) []syntax.Stmt {
	var result []syntax.Stmt
	for _, body := range p.iterate(loop, false) {
		result = append(result, &syntax.ExprStmt{X: body})
	}
	return result
}

func (p *deriveExpansion) iterate(loop *syntax.For, list bool) []syntax.Expr {
	value, ok := p.eval(loop.Items)
	sequence, yes := value.(shapeSequence)
	items := sequence.items
	if !ok || !yes {
		p.error(loop.Pos, "comptime for requires a compile-time shape sequence")
		return nil
	}
	if p.names[loop.Name] {
		p.error(loop.NamePos, "%s is already defined in an enclosing scope", loop.Name)
		return nil
	}
	p.names[loop.Name] = true
	defer delete(p.names, loop.Name)
	if p.origins == nil {
		p.origins = map[string]diag.Pos{}
	}
	savedOrigin, hadOrigin := p.origins[loop.Name]
	p.origins[loop.Name] = loop.NamePos
	defer func() {
		if hadOrigin {
			p.origins[loop.Name] = savedOrigin
		} else {
			delete(p.origins, loop.Name)
		}
	}()
	saved, existed := p.env[loop.Name]
	defer func() {
		if existed {
			p.env[loop.Name] = saved
		} else {
			delete(p.env, loop.Name)
		}
	}()
	var result []syntax.Expr
	for _, item := range items {
		if !p.tick(loop.Pos) {
			break
		}
		p.env[loop.Name] = item
		if list {
			element := loop.Body.Tail
			if guard, ok := element.(*syntax.If); ok && guard.Comptime && guard.Else == nil {
				chosen, valid := p.condition(guard.Cond)
				if !valid || !chosen {
					continue
				}
				element = guard.Then.Tail
			}
			result = append(result, p.expr(element))
		} else {
			result = append(result, p.expr(loop.Body))
		}
	}
	return result
}

// Fresh syntax identities are essential: differently typed loop iterations
// must never share the inference, dictionary, or fact caches of another field.
func (p *deriveExpansion) clone(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return value
		}
		out := reflect.New(value.Type()).Elem()
		if x, ok := value.Interface().(syntax.Expr); ok {
			out.Set(reflect.ValueOf(p.expr(x)))
		} else {
			out.Set(p.clone(value.Elem()))
		}
		return out
	case reflect.Pointer:
		if value.IsNil() {
			return value
		}
		if pattern, ok := value.Interface().(*syntax.VariantPat); ok && !pattern.Context && !pattern.Braces && !pattern.Positional && len(pattern.Fields) == 0 && len(pattern.Path) == 1 {
			if _, known := p.env[pattern.Path[0]].(Type); known {
				written := &syntax.TypeExpr{Pos: pattern.Pos, Name: pattern.Path[0]}
				name := ""
				if !p.patternTest {
					name = p.generatedName("pattern", p.template.Pkg)
				}
				expanded := &syntax.TypePat{Pos: pattern.Pos, Name: name, Type: p.clone(reflect.ValueOf(written)).Interface().(*syntax.TypeExpr)}
				p.c.info.assemblyNames[expanded] = "derived type match"
				return reflect.ValueOf(expanded)
			}
		}
		// Several syntax nodes store their body as *Block rather than Expr.
		// Enter those bodies through expansion too, preserving their scopes.
		if block, ok := value.Interface().(*syntax.Block); ok {
			return reflect.ValueOf(p.expr(block))
		}
		out := reflect.New(value.Type().Elem())
		out.Elem().Set(p.clone(value.Elem()))
		if written, ok := out.Interface().(*syntax.TypeExpr); ok {
			typ, yes := p.env[written.Name].(Type)
			var projected *Field
			if owner, member, ok := strings.Cut(written.Name, "."); ok && (member == "Type" || member == "RawType") {
				if runtime, known := p.env[owner].(shapeRuntimeType); known && member == "Type" {
					typ, yes = runtime.typ, true
				}
				if variant, known := p.env[owner].(shapeVariant); known && member == "Type" {
					view := p.variantView(variant.variant)
					if view != nil {
						typ, yes = view, true
					}
				}
				if field, known := p.env[owner].(shapeField); known {
					typ, yes = field.field.Type, true
					if member == "Type" {
						projected = field.field
					} else {
						if p.c.info.shapeRawHeads == nil {
							p.c.info.shapeRawHeads = map[*syntax.TypeExpr][]*Constraint{}
						}
						for _, constraint := range field.field.Constraints {
							if constraint.Path == "" && !constraint.HasSiblingArgs() {
								p.c.info.shapeRawHeads[written] = append(p.c.info.shapeRawHeads[written], constraint)
							}
						}
					}
					if origin, known := p.origins[owner]; known {
						p.c.noteDeriveSource(written.Pos, owner, origin, "variable")
					}
				}
			}
			if yes {
				if len(written.Args) != 0 || written.Func != nil || written.Union != nil {
					p.error(written.Pos, "a derive target reference cannot have type arguments")
					return out
				}
				p.c.info.assemblyTypes[written] = typ
				facts := p.typeFacts[written.Name]
				if projected != nil {
					// A dependent field type can retain only facts whose
					// arguments do not require the complete owner value.
					// Sibling facts stay on the descriptor and owner proof.
					facts = nil
					for _, constraint := range projected.Constraints {
						if !constraint.HasSiblingArgs() {
							facts = append(facts, constraint)
						}
					}
				}
				if len(facts) != 0 {
					if p.c.info.shapeTypeFacts == nil {
						p.c.info.shapeTypeFacts = map[*syntax.TypeExpr][]*Constraint{}
					}
					p.c.info.shapeTypeFacts[written] = append([]*Constraint(nil), facts...)
				}
			}
		}
		return out
	case reflect.Struct:
		out := reflect.New(value.Type()).Elem()
		out.Set(value)
		for _, index := range walkableSyntaxFields(value.Type()) {
			out.Field(index).Set(p.clone(value.Field(index)))
		}
		return out
	case reflect.Slice:
		if value.IsNil() {
			return value
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := range value.Len() {
			out.Index(i).Set(p.clone(value.Index(i)))
		}
		return out
	}
	return value
}

func (c *checker) expandDeriveMethod(request *deriveRequest, ci *ClassInstance, method *Func, head Type) *syntax.FuncDecl {
	template := request.class.Template
	var source *syntax.FuncDecl
	for _, candidate := range template.Decl.Methods {
		if candidate.Name == method.Decl.Name {
			source = candidate
			break
		}
	}
	if source == nil {
		return nil
	}
	saved := c.pkg
	c.pkg = template.Pkg
	defer func() { c.pkg = saved }()
	expansion := &deriveExpansion{c: c, template: template, target: head, env: map[string]any{template.Decl.TypeParams[0].Name: head}, budget: &deriveBudget{remaining: 100000}, names: map[string]bool{}}
	fd := &syntax.FuncDecl{Pos: source.Pos, End: source.End, Name: source.Name, Instance: ci.Decl}
	fd.Params = expansion.clone(reflect.ValueOf(source.Params)).Interface().([]*syntax.Param)
	if source.Result != nil {
		fd.Result = expansion.clone(reflect.ValueOf(source.Result)).Interface().(*syntax.TypeExpr)
	}
	fd.Uses = source.Uses
	if len(fd.Params) != len(method.Params) {
		expansion.error(source.Pos, "template method %s must match its class signature", source.Name)
	} else {
		for i, param := range fd.Params {
			if actual := c.resolveType(param.Type); !identical(actual, method.Params[i]) {
				expansion.error(param.Pos, "template parameter %s has type %s; its class requires %s", param.Name, actual, method.Params[i])
			}
		}
	}
	result := Type(Ok)
	if fd.Result != nil {
		result = c.resolveType(fd.Result)
	}
	if !identical(result, method.Result) {
		expansion.error(source.Pos, "template method %s returns %s; its class requires %s", source.Name, result, method.Result)
	}
	fd.Body = source.Body
	return fd
}

// Expand only after defaults and constraints have their final metadata. The
// provisional signatures remain available to resolve mutually dependent classes.
func (c *checker) expandDeriveBodies() {
	for _, instance := range c.info.ClassInstances {
		if instance.Derived == "" || instance.Class.Template == nil {
			continue
		}
		template := instance.Class.Template
		saved := c.pkg
		c.pkg = template.Pkg
		for _, method := range instance.Methods {
			start := c.diags.Len()
			plan := &deriveExpansion{c: c, template: template, target: instance.Type, instance: instance, typeFacts: map[string][]*Constraint{template.Decl.TypeParams[0].Name: instance.Constraints}, active: map[*syntax.FuncDecl]bool{}, scope: instance.Pkg, env: map[string]any{template.Decl.TypeParams[0].Name: instance.Type}, budget: &deriveBudget{remaining: 100000}, names: map[string]bool{}}
			for _, param := range method.Decl.Params {
				plan.names[param.Name] = true
			}
			method.Decl.Body = plan.expr(method.Decl.Body).(*syntax.Block)
			c.diags.DeriveContext(start, instance.Decl.Pos)
		}
		metadataPlan := &deriveExpansion{c: c, template: template, target: instance.Type, instance: instance, typeFacts: map[string][]*Constraint{template.Decl.TypeParams[0].Name: instance.Constraints}, active: map[*syntax.FuncDecl]bool{}, scope: instance.Pkg, env: map[string]any{template.Decl.TypeParams[0].Name: instance.Type}, budget: &deriveBudget{remaining: 100000}, names: map[string]bool{}}
		c.declareInstanceMetadata(instance, template.Decl.Metadata, metadataPlan)
		c.pkg = saved
	}
}

func metadataValue(value any) bool {
	switch value.(type) {
	case shapeField, shapeVariant, shapeFact, shapeSequence, shapeEnum, Type:
		return true
	}
	return false
}

func (p *deriveExpansion) accessible(target Type, pos diag.Pos) bool {
	from := p.scope
	if from == nil {
		from = p.template.Pkg
	}
	switch target := target.(type) {
	case *Record:
		if target.Pkg != from && target.Decl != nil && target.Decl.Private {
			p.error(pos, "cannot inspect private representation of %s from package %s", target, from.Path)
			return false
		}
	case *Sealed:
		if target.Pkg != from {
			for _, variant := range target.Variants {
				if !Exported(variant.Name) {
					p.error(pos, "cannot inspect private variants of %s from package %s", target, from.Path)
					return false
				}
			}
		}
	}
	return true
}

func (c *checker) markDeriveImports(file *syntax.File) {
	mark := func(name string) {
		if alias, _, ok := strings.Cut(name, "."); ok && c.pkg.imports[alias] != nil {
			c.pkg.used[alias] = true
		}
	}
	var methods []*syntax.FuncDecl
	for _, template := range file.Templates {
		methods = append(methods, template.Methods...)
	}
	methods = append(methods, file.DeriveHelpers...)
	for _, method := range methods {
		c.forTypeExprs(reflect.ValueOf(method.Params), func(written *syntax.TypeExpr, _ string) { mark(written.Name) })
		if method.Result != nil {
			mark(method.Result.Name)
		}
		if method.Body == nil {
			continue
		}
		for _, span := range file.ExpressionSpans {
			pos := span.Expr.Position()
			if pos.File != method.Body.Pos.File || pos.Line < method.Body.Pos.Line || pos.Line > method.Body.End.Line {
				continue
			}
			if pos.Line == method.Body.Pos.Line && pos.Col < method.Body.Pos.Col || pos.Line == method.Body.End.Line && pos.Col > method.Body.End.Col {
				continue
			}
			if identifier, ok := span.Expr.(*syntax.Ident); ok {
				mark(identifier.Name)
			}
		}
	}
}
