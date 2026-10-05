package check

import (
	"reflect"
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// Target-independent literal annotations and return paths use the ordinary
// contextual checker even when the definition is never requested. Dependent
// expressions remain obligations of symbolic/body checking and expansion.
func (c *checker) checkDeriveLiteralTypes(method *syntax.FuncDecl, typeNames map[string]bool) {
	if method.Body == nil {
		return
	}
	checked := map[syntax.Expr]bool{}
	checkLiteral := func(expr syntax.Expr, want Type) {
		if expr == nil || want == nil || want == Invalid || checked[expr] || !deriveLiteralExpression(expr) {
			return
		}
		checked[expr] = true
		actual := c.exprWant(expr, want)
		if actual != Invalid && !assignable(actual, want) {
			c.errorf(expr.Position(), "derive expression must be %s, found %s", want, actual)
		}
	}
	var result Type
	if deriveConcreteType(method.Result, typeNames) {
		result = c.resolveType(method.Result)
	}
	var tail func(syntax.Expr, Type)
	tail = func(expr syntax.Expr, want Type) {
		if expr == nil || want == nil {
			return
		}
		if value := reflect.ValueOf(expr); value.Kind() == reflect.Pointer && value.IsNil() {
			return
		}
		switch expr := expr.(type) {
		case *syntax.Block:
			tail(expr.Tail, want)
		case *syntax.If:
			tail(expr.Then, want)
			tail(expr.Else, want)
		case *syntax.Match:
			for _, arm := range expr.Arms {
				tail(arm.Body, want)
			}
		default:
			checkLiteral(expr, want)
		}
	}
	tail(method.Body, result)
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			switch node := v.Interface().(type) {
			case *syntax.Lambda:
				return // Its result is inferred in its own contextual function scope.
			case *syntax.Return:
				tail(node.Value, result)
			case *syntax.Binding:
				if node.Type != nil && deriveConcreteType(node.Type, typeNames) {
					checkLiteral(node.Value, c.resolveType(node.Type))
				}
			}
			walk(v.Elem())
		case reflect.Struct:
			for _, index := range walkableSyntaxFields(v.Type()) {
				walk(v.Field(index))
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(method.Body))
}

func deriveLiteralExpression(expr syntax.Expr) bool {
	switch expr := expr.(type) {
	case *syntax.StringLit, *syntax.BoolLit, *syntax.IntLit, *syntax.FloatLit, *syntax.RuneLit:
		return true
	case *syntax.Unary:
		return deriveLiteralExpression(expr.X)
	case *syntax.Binary:
		return deriveLiteralExpression(expr.X) && deriveLiteralExpression(expr.Y)
	case *syntax.ListLit:
		for _, elem := range expr.Elems {
			if !deriveLiteralExpression(elem) {
				return false
			}
		}
		return true
	case *syntax.MapLit:
		for i, key := range expr.Keys {
			if !deriveLiteralExpression(key) || !deriveLiteralExpression(expr.Values[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// Resolve only annotations that do not refer to a symbolic target or a
// descriptor type projection. Their concrete meaning is independent of shape.
func deriveConcreteType(typ *syntax.TypeExpr, typeNames map[string]bool) bool {
	if typ == nil {
		return true
	}
	if typeNames[typ.Name] {
		return false
	}
	if _, member, projected := strings.Cut(typ.Name, "."); projected && member == "Type" {
		return false
	}
	for _, arg := range typ.Args {
		if !deriveConcreteType(arg, typeNames) {
			return false
		}
	}
	for _, member := range typ.Union {
		if !deriveConcreteType(member, typeNames) {
			return false
		}
	}
	if typ.Func != nil {
		for _, param := range typ.Func.Params {
			if !deriveConcreteType(param, typeNames) {
				return false
			}
		}
		return deriveConcreteType(typ.Func.Result, typeNames)
	}
	return true
}
