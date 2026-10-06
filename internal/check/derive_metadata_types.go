package check

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// Descriptor identities are lexical, and their scalar projections have types
// independent of the eventual target. They never become ordinary runtime
// records in the definition checker.
type deriveDescriptor uint8

const (
	deriveField deriveDescriptor = iota + 1
	deriveVariant
	deriveFact
	deriveFields
	deriveVariants
	deriveFacts
)

type deriveMetadataTypes struct {
	c         *checker
	locals    map[*local]deriveDescriptor
	typeNames map[string]bool
}

func (m *deriveMetadataTypes) annotation(typ *syntax.TypeExpr) deriveDescriptor {
	if typ == nil {
		return 0
	}
	if typ.Name == "List" && len(typ.Args) == 1 {
		if element := m.annotation(typ.Args[0]); element >= deriveField && element <= deriveFact {
			return element - deriveField + deriveFields
		}
		return 0
	}
	alias, member, qualified := strings.Cut(typ.Name, ".")
	pkg := m.c.pkg.imports[alias]
	if !qualified || pkg == nil || pkg.Path != "bork/shape" || len(typ.Args) != 1 {
		if deriveConcreteType(typ, m.typeNames) {
			if record, ok := m.c.resolveType(typ).(*Record); ok && record.Pkg != nil && record.Pkg.Path == "bork/shape" {
				switch record.Name {
				case "Field":
					return deriveField
				case "Variant":
					return deriveVariant
				case "Fact":
					return deriveFact
				}
			}
		}
		return 0
	}
	switch member {
	case "Field":
		return deriveField
	case "Variant":
		return deriveVariant
	case "Fact":
		return deriveFact
	}
	return 0
}

func (m *deriveMetadataTypes) kind(expr syntax.Expr) deriveDescriptor {
	switch expr := expr.(type) {
	case *syntax.Ident:
		return m.locals[m.c.lookup(expr.Name)]
	case *syntax.Call:
		id, ok := expr.Fun.(*syntax.Ident)
		if !ok {
			return 0
		}
		alias, member, qualified := strings.Cut(id.Name, ".")
		pkg := m.c.pkg.imports[alias]
		if !qualified || pkg == nil || pkg.Path != "bork/shape" || len(expr.TypeArgs) != 1 || len(expr.Args) != 0 {
			return 0
		}
		switch member {
		case "fields":
			return deriveFields
		case "variants":
			return deriveVariants
		case "facts":
			return deriveFacts
		}
	case *syntax.Selector:
		switch m.kind(expr.X) {
		case deriveVariant:
			if expr.Name == "fields" {
				return deriveFields
			}
			fallthrough
		case deriveField:
			if expr.Name == "facts" {
				return deriveFacts
			}
		}
	}
	return 0
}

func (m *deriveMetadataTypes) scalar(expr syntax.Expr) Type {
	if id, ok := expr.(*syntax.Ident); ok {
		alias, member, qualified := strings.Cut(id.Name, ".")
		pkg := m.c.pkg.imports[alias]
		if qualified && pkg != nil && pkg.Path == "bork/shape" && (member == "Record" || member == "Sealed" || member == "Other") {
			return pkg.TypeNamed("Kind")
		}
	}
	if call, ok := expr.(*syntax.Call); ok {
		var metadata *syntax.TypeExpr
		if selector, ok := call.Fun.(*syntax.Selector); ok && selector.Name == "tagged" && (m.kind(selector.X) == deriveField || m.kind(selector.X) == deriveVariant) && len(call.TypeArgs) == 1 && len(call.Args) == 0 {
			metadata = call.TypeArgs[0]
		}
		if id, ok := call.Fun.(*syntax.Ident); ok {
			alias, member, qualified := strings.Cut(id.Name, ".")
			pkg := m.c.pkg.imports[alias]
			if qualified && pkg != nil && pkg.Path == "bork/shape" && member == "tagged" && len(call.TypeArgs) == 2 && len(call.Args) == 0 {
				metadata = call.TypeArgs[1]
			}
		}
		if metadata != nil {
			typ := m.c.resolveType(metadata)
			if hasTypeParam(typ) {
				m.c.errorf(metadata.Pos, "tagged requires a closed metadata type")
			}
			return instantiate(m.c.preludePkg.TypeNamed("Option"), []Type{typ})
		}
		if id, ok := call.Fun.(*syntax.Ident); ok && len(call.TypeArgs) == 1 && len(call.Args) == 0 {
			alias, member, qualified := strings.Cut(id.Name, ".")
			pkg := m.c.pkg.imports[alias]
			if qualified && pkg != nil && pkg.Path == "bork/shape" {
				switch member {
				case "kind":
					return pkg.TypeNamed("Kind")
				case "name", "typeName", "owner":
					return String
				case "positional":
					return Bool
				}
			}
		}
		if selector, ok := call.Fun.(*syntax.Selector); ok && m.kind(selector.X) >= deriveFields && len(call.Args) == 0 && len(call.TypeArgs) <= 1 {
			switch selector.Name {
			case "length":
				return Int
			case "isEmpty":
				return Bool
			}
		}
	}
	selector, ok := expr.(*syntax.Selector)
	if !ok {
		return nil
	}
	if id, ok := selector.X.(*syntax.Ident); ok {
		pkg := m.c.pkg.imports[id.Name]
		if pkg != nil && pkg.Path == "bork/shape" && (selector.Name == "Record" || selector.Name == "Sealed" || selector.Name == "Other") {
			return pkg.TypeNamed("Kind")
		}
	}
	switch m.kind(selector.X) {
	case deriveField:
		switch selector.Name {
		case "name", "doc":
			return String
		case "index":
			return Int
		case "positional", "computed", "hasDefault":
			return Bool
		case "tags":
			if shape := m.c.pkgs["bork/shape"]; shape != nil {
				return &List{Elem: shape.TypeNamed("Tag")}
			}
		}
	case deriveVariant:
		switch selector.Name {
		case "name", "doc":
			return String
		case "index":
			return Int
		case "positional":
			return Bool
		}
	case deriveFact:
		switch selector.Name {
		case "text", "path":
			return String
		case "independent":
			return Bool
		}
	}
	return nil
}

// Ordinary typing sees typed, nonconstant placeholders for scalar metadata.
// It checks operators and calls without evaluating metadata or supplying fake
// descriptor values, projected field types, or runtime facts.
func (m *deriveMetadataTypes) check(expr syntax.Expr, want Type) Type {
	// Scalar replacement must not bypass the lexical descriptor contracts,
	// including a sequence call nested in an otherwise concrete expression.
	var validate func(reflect.Value)
	validate = func(value reflect.Value) {
		if !value.IsValid() || (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) && value.IsNil() {
			return
		}
		if value.Kind() == reflect.Pointer && value.CanInterface() {
			switch node := value.Interface().(type) {
			case *syntax.Call:
				m.checkCall(node)
			case *syntax.Selector:
				m.checkMember(node)
			}
		}
		switch value.Kind() {
		case reflect.Interface, reflect.Pointer:
			validate(value.Elem())
		case reflect.Struct:
			for _, index := range walkableSyntaxFields(value.Type()) {
				validate(value.Field(index))
			}
		case reflect.Slice:
			for i := range value.Len() {
				validate(value.Index(i))
			}
		}
	}
	validate(reflect.ValueOf(expr))
	var contains func(reflect.Value) bool
	contains = func(value reflect.Value) bool {
		if !value.IsValid() {
			return false
		}
		if value.CanInterface() {
			if expr, ok := value.Interface().(syntax.Expr); ok && m.scalar(expr) != nil {
				return true
			}
		}
		switch value.Kind() {
		case reflect.Interface, reflect.Pointer:
			return !value.IsNil() && contains(value.Elem())
		case reflect.Struct:
			for _, index := range walkableSyntaxFields(value.Type()) {
				if contains(value.Field(index)) {
					return true
				}
			}
		case reflect.Slice:
			for i := range value.Len() {
				if contains(value.Index(i)) {
					return true
				}
			}
		}
		return false
	}
	if !contains(reflect.ValueOf(expr)) {
		return m.c.exprWant(expr, want)
	}
	scope := m.c.scopes[len(m.c.scopes)-1]
	var names []string
	sources := map[syntax.Expr]syntax.Expr{}
	defer func() {
		for _, name := range names {
			delete(scope, name)
		}
		for original, replacement := range sources {
			if typ := m.c.info.types[replacement]; typ != nil {
				m.c.info.types[original] = typ
			}
			if reference := m.c.info.funcRefs[replacement]; reference != nil {
				m.c.info.funcRefs[original] = reference
			}
		}
	}()
	var clone func(reflect.Value) reflect.Value
	clone = func(value reflect.Value) (out reflect.Value) {
		if value.IsValid() && value.CanInterface() {
			if original, ok := value.Interface().(syntax.Expr); ok {
				defer func() {
					if replacement, ok := out.Interface().(syntax.Expr); ok {
						sources[original] = replacement
					}
				}()
			}
		}
		if value.IsValid() && value.CanInterface() {
			if expr, ok := value.Interface().(syntax.Expr); ok {
				if typ := m.scalar(expr); typ != nil {
					name := fmt.Sprintf("\x00derive_metadata_%d", len(names))
					names = append(names, name)
					scope[name] = &local{typ: typ, used: true}
					return reflect.ValueOf(&syntax.Ident{Pos: expr.Position(), Name: name})
				}
			}
		}
		switch value.Kind() {
		case reflect.Interface:
			if value.IsNil() {
				return value
			}
			out := reflect.New(value.Type()).Elem()
			out.Set(clone(value.Elem()))
			return out
		case reflect.Pointer:
			if value.IsNil() {
				return value
			}
			out := reflect.New(value.Type().Elem())
			out.Elem().Set(clone(value.Elem()))
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
	return m.c.exprWant(clone(reflect.ValueOf(expr)).Interface().(syntax.Expr), want)
}

// Descriptor member sets are known before a target is chosen. Dependent
// operations keep their result/head obligations for expansion, but misspelled
// members and impossible call shapes cannot be repaired by any target.
func (m *deriveMetadataTypes) checkMember(selector *syntax.Selector) {
	kind := m.kind(selector.X)
	valid := false
	switch kind {
	case deriveField:
		switch selector.Name {
		case "name", "doc", "index", "positional", "computed", "hasDefault", "tags", "tagged", "facts", "Type", "RawType", "read", "default", "validate", "check":
			valid = true
		}
	case deriveVariant:
		switch selector.Name {
		case "name", "doc", "index", "positional", "fields", "facts", "tagged", "Type", "project", "builder":
			valid = true
		}
	case deriveFact:
		switch selector.Name {
		case "text", "path", "independent":
			valid = true
		}
	case deriveFields, deriveVariants, deriveFacts:
		valid = selector.Name == "length" || selector.Name == "isEmpty"
	default:
		return
	}
	if !valid {
		m.c.errorf(selector.Pos, "shape descriptor has no member %s", selector.Name)
	}
}

func (m *deriveMetadataTypes) checkCall(call *syntax.Call) {
	selector, ok := call.Fun.(*syntax.Selector)
	if !ok {
		return
	}
	kind := m.kind(selector.X)
	if selector.Name == "tagged" && (kind == deriveField || kind == deriveVariant) {
		if len(call.Args) != 0 || len(call.TypeArgs) != 1 {
			m.c.errorf(call.Pos, "tagged takes one metadata type and no arguments")
		}
		return
	}
	if kind >= deriveFields {
		if selector.Name != "length" && selector.Name != "isEmpty" {
			return
		}
		if len(call.Args) != 0 || len(call.TypeArgs) > 1 {
			m.c.errorf(call.Pos, "metadata sequence %s takes no arguments and at most one element type", selector.Name)
		}
		if len(call.TypeArgs) == 1 {
			element := m.annotation(call.TypeArgs[0])
			// Unknown projected/generic aliases remain dependent. A resolved
			// concrete head or a known descriptor head can be checked here.
			if element != 0 && element != kind-deriveFields+deriveField || element == 0 && deriveConcreteType(call.TypeArgs[0], m.typeNames) && m.c.resolveType(call.TypeArgs[0]) != Invalid {
				m.c.errorf(call.TypeArgs[0].Pos, "metadata sequence %s requires its descriptor element type", selector.Name)
			}
		}
		return
	}
	arity := -1
	switch kind {
	case deriveField:
		if selector.Name == "read" || selector.Name == "validate" || selector.Name == "check" {
			arity = 1
		}
		if selector.Name == "default" {
			arity = 0
		}
	case deriveVariant:
		if selector.Name == "project" {
			arity = 1
		}
		if selector.Name == "builder" {
			arity = 0
		}
	}
	if arity >= 0 {
		if len(call.Args) != arity || len(call.TypeArgs) != 0 {
			m.c.errorf(call.Pos, "shape descriptor %s takes %d argument(s) and no type arguments", selector.Name, arity)
		}
		for _, argument := range call.Arguments {
			if argument.Name != "" {
				m.c.errorf(argument.Pos, "shape descriptor %s requires positional arguments", selector.Name)
			}
		}
		return
	}
	if kind >= deriveField && kind <= deriveFact && (m.scalar(selector) != nil || m.kind(selector) != 0 || (selector.Name == "Type" || selector.Name == "RawType")) {
		m.c.errorf(call.Pos, "shape descriptor member %s is a property, not a callable method", selector.Name)
	}
}
