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
	c      *checker
	locals map[*local]deriveDescriptor
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
		if id, ok := call.Fun.(*syntax.Ident); ok && len(call.TypeArgs) == 1 && len(call.Args) == 0 {
			alias, member, qualified := strings.Cut(id.Name, ".")
			pkg := m.c.pkg.imports[alias]
			if qualified && pkg != nil && pkg.Path == "bork/shape" {
				switch member {
				case "kind":
					return pkg.TypeNamed("Kind")
				case "name", "owner":
					return String
				case "positional":
					return Bool
				}
			}
		}
		if selector, ok := call.Fun.(*syntax.Selector); ok && m.kind(selector.X) >= deriveFields && len(call.Args) == 0 && len(call.TypeArgs) == 0 {
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
		}
	case deriveVariant:
		switch selector.Name {
		case "name":
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
