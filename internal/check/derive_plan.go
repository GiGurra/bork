package check

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// DerivePlanStore stores owned serialized plans. Implementations must return
// independent bytes and bound retained memory and content-store size.
type DerivePlanStore interface {
	DerivePlanGet(key string) []byte
	DerivePlanPut(key string, plan []byte)
}

type derivePlanProvider interface {
	DerivePlanStore() DerivePlanStore
}

type pendingDerivePlan struct {
	store DerivePlanStore
	key   string
	data  []byte
}

type DerivePlanStats struct {
	Hits, Misses, Declines uint64
}

// PublishDerivePlans is called only after the driver's complete checking,
// including effects, lifetimes and facts, has succeeded. Provisional rounds
// and unsuccessful programs never publish plans.
func (info *Info) PublishDerivePlans() {
	for _, pending := range info.derivePlanPending {
		pending.store.DerivePlanPut(pending.key, pending.data)
	}
	info.derivePlanPending = nil
}

// The first replay capability supports expansions whose only compiler actions
// are metadata selection and ordinary syntax generation. Typed field reads,
// builders, views and helper specializations need stable action references;
// without that inventory they must use the cold path.
func (p *deriveExpansion) reusablePlan(body *syntax.Block) bool {
	valid := true
	seen := map[reflect.Value]bool{}
	var walk func(reflect.Value)
	walk = func(value reflect.Value) {
		if !valid || !value.IsValid() {
			return
		}
		if (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) && value.IsNil() {
			return
		}
		if value.CanInterface() {
			if value.Type().Comparable() && p.c.info.assemblyNames[value.Interface()] != "" {
				valid = false
				return
			}
			switch node := value.Interface().(type) {
			case *syntax.Comptime, *syntax.Generate, *syntax.Yield, *syntax.MockStmt:
				valid = false
				return
			case *syntax.Ident:
				if node.Name == p.template.Decl.TypeParams[0].Name {
					valid = false
					return
				}
			case *syntax.Selector:
				if node.Name == "Type" || p.c.info.shapeViewReads[node] != nil || p.c.info.shapeReadOwners[node] != nil {
					valid = false
					return
				}
			case *syntax.TypeExpr:
				if p.c.info.assemblyTypes[node] != nil || len(p.c.info.shapeTypeFacts[node]) != 0 {
					valid = false
					return
				}
				if node.Name == p.template.Decl.TypeParams[0].Name || strings.HasSuffix(node.Name, ".Type") {
					valid = false
					return
				}
			case *syntax.VariantPat:
				if len(node.Path) == 1 && node.Path[0] == p.template.Decl.TypeParams[0].Name {
					valid = false
					return
				}
			case *syntax.Call:
				if p.c.info.shapeBuildCalls[node] != nil || p.c.info.shapeProjects[node] != nil || p.c.info.shapeDefaults[node] != nil {
					valid = false
					return
				}
				if id, ok := node.Fun.(*syntax.Ident); ok {
					alias, member, qualified := strings.Cut(id.Name, ".")
					pkg := p.template.Pkg.imports[alias]
					if qualified && pkg != nil && pkg.Path == "bork/shape" {
						switch member {
						case "fields", "variants", "kind", "name", "owner", "positional", "facts":
							if len(node.TypeArgs) != 1 || len(node.Args) != 0 || node.TypeArgs[0].Name != p.template.Decl.TypeParams[0].Name || len(node.TypeArgs[0].Args) != 0 || len(node.TypeArgs[0].Where) != 0 {
								valid = false
							}
							return // The simple target argument is consumed by metadata evaluation.
						default:
							valid = false
							return
						}
					}
					if helper, _ := p.c.deriveHelperNamed(p.template.Pkg, id.Name); helper != nil {
						valid = false
						return
					}
				}
				if selector, ok := node.Fun.(*syntax.Selector); ok {
					switch selector.Name {
					case "read", "default", "project", "builder", "set", "finish":
						valid = false
						return
					}
				}
			}
		}
		switch value.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !value.IsNil() {
				if value.Kind() == reflect.Pointer {
					if seen[value] {
						return
					}
					seen[value] = true
				}
				walk(value.Elem())
			}
		case reflect.Struct:
			for _, index := range walkableSyntaxFields(value.Type()) {
				walk(value.Field(index))
			}
		case reflect.Slice:
			for i := range value.Len() {
				walk(value.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(body))
	return valid
}

// Dependency inventory deliberately includes every declaration and ordinary
// helper body. Only the root entry point's runtime body is irrelevant to
// expansion; editing it must not invalidate unchanged derivations.
func (p *deriveExpansion) planKey(method *Func) string {
	head := stableDeriveShape(p.target)
	if head == "" {
		return ""
	}
	rootFiles := map[string]bool{}
	for _, file := range p.c.files {
		if pkg := p.c.pkgs[file.Package]; pkg != nil && pkg.Root && !file.Prelude {
			rootFiles[file.Path] = true
		}
	}
	active := map[reflect.Value]bool{}
	var normalize func(reflect.Value) any
	normalize = func(value reflect.Value) any {
		if !value.IsValid() {
			return nil
		}
		if value.Type() == reflect.TypeFor[diag.Pos]() {
			return nil
		}
		switch value.Kind() {
		case reflect.Pointer, reflect.Interface:
			if value.IsNil() {
				return nil
			}
			if value.Kind() == reflect.Pointer {
				if active[value] {
					return nil
				}
				active[value] = true
				defer delete(active, value)
			}
			return normalize(value.Elem())
		case reflect.Struct:
			out := map[string]any{"$type": value.Type().String()}
			fn, isFunction := value.Interface().(syntax.FuncDecl)
			for i := range value.NumField() {
				field := value.Type().Field(i)
				if !field.IsExported() || field.Name == "Instance" && isFunction {
					continue
				}
				if value.Type() == reflect.TypeFor[syntax.File]() && (field.Name == "Source" || field.Name == "ExpressionSpans" || field.Name == "PatternTestOperators" || field.Name == "Comments") {
					continue
				}
				if isFunction && field.Name == "Body" && fn.Name == "main" && !fn.IsPred && fn.Instance == nil && rootFiles[fn.Pos.File] {
					continue
				}
				out[field.Name] = normalize(value.Field(i))
			}
			return out
		case reflect.Slice:
			out := make([]any, value.Len())
			for i := range value.Len() {
				out[i] = normalize(value.Index(i))
			}
			return out
		case reflect.String:
			return value.String()
		case reflect.Bool:
			return value.Bool()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return value.Int()
		}
		return nil
	}
	constraints := []string{}
	for _, constraint := range p.instance.Constraints {
		constraints = append(constraints, constraint.Text(nil))
	}
	data, err := json.Marshal([]any{derivePlanABI, normalize(reflect.ValueOf(p.c.files)), p.scope.Path, classIdentity(p.instance.Class), p.instance.Name, method.Decl.Name, head, constraints, p.c.deriveBounds})
	if err != nil || len(data) > derivePlanMaxBytes {
		return ""
	}
	key := sha256.Sum256(data)
	return hex.EncodeToString(key[:])
}

func (p *deriveExpansion) expandBody(method *Func) *syntax.Block {
	source := method.Decl.Body
	store := p.c.info.derivePlanStore
	key := ""
	if store != nil && p.reusablePlan(source) {
		key = p.planKey(method)
		if key != "" {
			body, work, depth := decodeDerivePlan(source, key, store.DerivePlanGet(key))
			if body != nil && p.reusablePlan(body) {
				if !p.charge(source.Pos, work) {
					return &syntax.Block{Pos: source.Pos}
				}
				if p.budget.depth+depth > 256 {
					p.error(source.Pos, "derive template expansion exceeds its compile-time depth limit")
					return &syntax.Block{Pos: source.Pos}
				}
				p.budget.observeDepth(p.budget.depth + depth)
				p.c.info.DerivePlans.Hits++
				return body
			}
			p.c.info.DerivePlans.Misses++
		}
	}
	if key == "" {
		p.c.info.DerivePlans.Declines++
	}
	before := p.budget.remaining
	frame := &deriveBudgetFrame{start: p.budget.depth}
	p.budget.frames = append(p.budget.frames, frame)
	body := p.expr(source).(*syntax.Block)
	p.budget.frames = p.budget.frames[:len(p.budget.frames)-1]
	if key != "" && !p.failed && !frame.deferred && p.reusablePlan(body) {
		if data := encodeDerivePlan(source, body, key, before-p.budget.remaining, frame.peak); data != nil {
			p.c.info.derivePlanPending = append(p.c.info.derivePlanPending, pendingDerivePlan{store: store, key: key, data: data})
		}
	}
	return body
}

// Declaration identities and generic arguments remain stable across checks;
// the ordinary checker typeKey intentionally uses pointers and cannot serve a
// retained plan. Foreign layouts without a complete inventory force a miss.
func stableDeriveType(typ Type) string {
	args := func(types []Type) string {
		values := make([]string, len(types))
		for i, typ := range types {
			values[i] = stableDeriveType(typ)
			if values[i] == "" {
				return ""
			}
		}
		return "[" + strings.Join(values, ",") + "]"
	}
	switch typ := typ.(type) {
	case *Opaque, *Resource:
		return ""
	case *TypeParam:
		bounds := make([]string, len(typ.Bounds))
		for i, bound := range typ.Bounds {
			bounds[i] = classIdentity(bound)
		}
		return "parameter:" + typ.Name + "[" + strings.Join(bounds, ",") + "]"
	case *Record:
		if typ.GoGenerated || typ.GoMirror != nil || typ.Pkg == nil && !typ.Tuple {
			return ""
		}
		arguments := args(TypeArgs(typ))
		if arguments == "" {
			return ""
		}
		if typ.Tuple {
			return "tuple" + arguments
		}
		return "record:" + typ.Pkg.Path + ":" + typ.Name + arguments
	case *Sealed:
		arguments := args(TypeArgs(typ))
		if arguments == "" || typ.Pkg == nil {
			return ""
		}
		return "sealed:" + typ.Pkg.Path + ":" + typ.Name + arguments
	case *List:
		if element := stableDeriveType(typ.Elem); element != "" {
			return "list:" + element
		}
		return ""
	case *Map:
		if elements := args([]Type{typ.Key, typ.Value}); elements != "" {
			return "map:" + elements
		}
		return ""
	case *Seq, *FuncType, *Union:
		// These can contain effects or foreign ownership. Admit them only
		// after their normalized action inventory has been implemented.
		return ""
	default:
		if typ == nil || typ == Invalid {
			return ""
		}
		return typ.String()
	}
}

// Inventory the resolved shape as well as its source declaration. Imported
// foreign layouts and unsupported type forms cannot be inferred from Bork
// syntax alone and conservatively decline reuse.
func stableDeriveShape(target Type) string {
	seen := map[Type]bool{}
	var nodes []any
	valid := true
	constraints := func(values []*Constraint) []any {
		result := make([]any, len(values))
		for i, value := range values {
			owner := ""
			if value.Pkg != nil {
				owner = value.Pkg.Path
			}
			result[i] = []any{value.Text(nil), value.Path, owner}
		}
		return result
	}
	var walk func(Type, int)
	walk = func(typ Type, depth int) {
		if !valid || seen[typ] {
			return
		}
		if depth > 256 || len(nodes) >= 4096 {
			valid = false
			return
		}
		head := stableDeriveType(typ)
		if head == "" {
			valid = false
			return
		}
		seen[typ] = true
		var fields func([]*Field) []any
		fields = func(values []*Field) []any {
			result := make([]any, len(values))
			for i, field := range values {
				fieldHead := stableDeriveType(field.Type)
				if fieldHead == "" {
					valid = false
					return nil
				}
				// The complete syntax inventory in planKey includes default/computed
				// bodies; these flags and docs also capture resolved representation.
				tags := make([][2]string, len(field.GoTags))
				for i, tag := range field.GoTags {
					tags[i] = [2]string{tag.Name, tag.Value}
				}
				result[i] = []any{field.Name, fieldHead, field.Computed, field.Lazy, field.Prelude, field.Doc, field.Decl != nil && field.Decl.Default != nil, constraints(field.Constraints), tags}
				walk(field.Type, depth+1)
			}
			return result
		}
		switch typ := typ.(type) {
		case *Record:
			if !typ.Tuple && typ.Decl == nil || typ.GoStruct || len(typ.GoFields) != 0 {
				valid = false
				return
			}
			nodes = append(nodes, []any{head, typ.Tuple, typ.Prelude, fields(typ.Fields), constraints(typ.Constraints)})
		case *Sealed:
			if typ.Decl == nil {
				valid = false
				return
			}
			variants := make([]any, len(typ.Variants))
			for i, variant := range typ.Variants {
				variants[i] = []any{variant.Name, variant.Index, variant.Positional, fields(variant.Fields), constraints(variant.Constraints)}
			}
			nodes = append(nodes, []any{head, typ.Prelude, variants, constraints(typ.Constraints)})
		case *List:
			nodes = append(nodes, head)
			walk(typ.Elem, depth+1)
		case *Map:
			nodes = append(nodes, head)
			walk(typ.Key, depth+1)
			walk(typ.Value, depth+1)
		default:
			nodes = append(nodes, head)
		}
	}
	walk(target, 0)
	if !valid {
		return ""
	}
	data, err := json.Marshal(nodes)
	if err != nil || len(data) > derivePlanMaxBytes {
		return ""
	}
	return string(data)
}
