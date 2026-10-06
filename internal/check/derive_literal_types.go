package check

import (
	"reflect"
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// Concrete expression islands use ordinary contextual typing at definition
// time. Symbolic targets and descriptor projections remain dependent: this pass
// never supplies a guessed runtime type for them.
func (c *checker) checkDeriveLiteralTypes(method *syntax.FuncDecl, typeNames map[string]bool) {
	if method.Body == nil {
		return
	}
	outer, effects := c.scopes, c.used
	defer func() { c.scopes, c.used = outer, effects }()
	c.used = 0
	c.scopes = append(c.scopes, map[string]*local{})
	metadata := deriveMetadataTypes{c: c, locals: map[*local]deriveDescriptor{}}
	bind := func(name string, typ Type, node any) {
		if typ == nil {
			typ = Invalid
		}
		c.scopes[len(c.scopes)-1][name] = &local{typ: typ, decl: node, used: true}
	}
	for _, param := range method.Params {
		var typ Type
		if param.Type != nil && deriveConcreteType(param.Type, typeNames) {
			typ = c.resolveType(param.Type)
		}
		bind(param.Name, typ, param)
		if kind := metadata.annotation(param.Type); kind != 0 {
			metadata.locals[c.lookup(param.Name)] = kind
		}
	}
	var concrete func(syntax.Expr) bool
	concrete = func(expr syntax.Expr) bool {
		if metadata.scalar(expr) != nil {
			return true
		}
		switch expr := expr.(type) {
		case *syntax.Ident:
			local := c.lookup(expr.Name)
			return local != nil && local.typ != Invalid
		case *syntax.Selector:
			return metadata.scalar(expr) != nil
		case *syntax.Call:
			id, named := expr.Fun.(*syntax.Ident)
			if !named || expr.Pipe.File != "" {
				return false
			}
			fn, found := c.funcNamed(id.Name)
			if !found || len(fn.Needs) != 0 || len(fn.TypeParams) > 0 && len(expr.TypeArgs) == 0 {
				return false
			}
			for _, arg := range expr.TypeArgs {
				if !deriveConcreteType(arg, typeNames) {
					return false
				}
			}
			for _, arg := range expr.Args {
				if !concrete(arg) {
					return false
				}
			}
			return true
		case *syntax.Unary:
			return concrete(expr.X)
		case *syntax.Binary:
			return concrete(expr.X) && concrete(expr.Y)
		case *syntax.TupleLit:
			for _, elem := range expr.Elems {
				if !concrete(elem) {
					return false
				}
			}
			return true
		case *syntax.ListLit:
			for _, elem := range expr.Elems {
				if !concrete(elem) {
					return false
				}
			}
			return true
		case *syntax.MapLit:
			for i, key := range expr.Keys {
				if !concrete(key) || !concrete(expr.Values[i]) {
					return false
				}
			}
			return true
		default:
			return deriveLiteralExpression(expr)
		}
	}
	check := func(expr syntax.Expr, want Type) Type {
		if expr == nil || !concrete(expr) {
			return nil
		}
		actual := metadata.check(expr, want)
		if want != nil && actual != Invalid && !assignable(actual, want) {
			c.errorf(expr.Position(), "derive expression must be %s, found %s", want, actual)
		}
		return actual
	}
	var result Type
	if deriveConcreteType(method.Result, typeNames) {
		result = c.resolveType(method.Result)
	}
	var bindPattern func(syntax.Pattern)
	bindPattern = func(pattern syntax.Pattern) {
		switch pattern := pattern.(type) {
		case *syntax.TypePat:
			bind(pattern.Name, nil, pattern)
		case *syntax.TuplePat:
			for _, element := range pattern.Elems {
				bindPattern(element)
			}
		case *syntax.ListPat:
			bind(pattern.Rest, nil, pattern)
			for _, element := range pattern.Elems {
				bindPattern(element)
			}
		case *syntax.VariantPat:
			if len(pattern.Path) == 1 && !pattern.Context && !pattern.Braces && !pattern.Positional && len(pattern.Fields) == 0 && !c.isTypeName(pattern.Path[0]) && !typeNames[pattern.Path[0]] {
				bind(pattern.Path[0], nil, pattern)
			}
			for _, element := range pattern.Elems {
				bindPattern(element)
			}
			for _, field := range pattern.Fields {
				if field.Pattern == nil {
					bind(field.Field, nil, field)
				} else {
					bindPattern(field.Pattern)
				}
			}
		}
	}
	var walk func(reflect.Value, Type)
	walk = func(v reflect.Value, want Type) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem(), want)
			}
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			switch node := v.Interface().(type) {
			case *syntax.Block:
				c.scopes = append(c.scopes, map[string]*local{})
				for _, stmt := range node.Stmts {
					walk(reflect.ValueOf(stmt), nil)
				}
				walk(reflect.ValueOf(node.Tail), want)
				c.scopes = c.scopes[:len(c.scopes)-1]
				return
			case *syntax.Lambda:
				// A lambda has its own return context and lexical parameters,
				// even when its body contains dependent descriptor operations.
				// Check the independent parts without guessing those operations'
				// types or inferring an unannotated symbolic parameter.
				context, _ := want.(*FuncType)
				if context != nil && len(context.Params) != len(node.Params) {
					c.errorf(node.Pos, "expected a function taking %d argument(s), but this lambda takes %d", len(context.Params), len(node.Params))
					context = nil
				}
				c.scopes = append(c.scopes, map[string]*local{})
				for i, parameter := range node.Params {
					var typ Type
					if parameter.Type != nil && deriveConcreteType(parameter.Type, typeNames) {
						typ = c.resolveType(parameter.Type)
						if context != nil && typ != Invalid && !identical(typ, context.Params[i]) {
							c.errorf(parameter.Type.Pos, "parameter %s must be %s here, found %s", parameter.Name, context.Params[i], typ)
						}
					} else if parameter.Type == nil && context != nil {
						typ = context.Params[i]
					}
					bind(parameter.Name, typ, parameter)
					if kind := metadata.annotation(parameter.Type); kind != 0 {
						metadata.locals[c.lookup(parameter.Name)] = kind
					}
				}
				outerResult := result
				result = nil
				if context != nil && context.Result != Ok {
					result = context.Result
				}
				walk(reflect.ValueOf(node.Body), result)
				result = outerResult
				c.scopes = c.scopes[:len(c.scopes)-1]
				return
			case *syntax.If:
				check(node.Cond, Bool)
				walk(reflect.ValueOf(node.Then), want)
				walk(reflect.ValueOf(node.Else), want)
				return
			case *syntax.Match:
				walk(reflect.ValueOf(node.X), nil)
				for _, arm := range node.Arms {
					// Pattern values remain dependent, but unrelated outer
					// locals and descriptor identities keep their known types.
					c.scopes = append(c.scopes, map[string]*local{})
					bindPattern(arm.Pattern)
					walk(reflect.ValueOf(arm.Body), want)
					c.scopes = c.scopes[:len(c.scopes)-1]
				}
				return
			case *syntax.Return:
				walk(reflect.ValueOf(node.Value), result)
				return
			case *syntax.Binding:
				var declared Type
				if node.Type != nil && deriveConcreteType(node.Type, typeNames) {
					declared = c.resolveType(node.Type)
				}
				actual := check(node.Value, declared)
				if actual == nil {
					walk(reflect.ValueOf(node.Value), declared)
				}
				if node.Type != nil && declared == nil {
					actual = nil
				}
				if declared != nil {
					actual = declared
				}
				bind(node.Name, actual, node)
				kind := metadata.kind(node.Value)
				if kind == 0 {
					kind = metadata.annotation(node.Type)
				}
				if kind != 0 {
					metadata.locals[c.lookup(node.Name)] = kind
				}
				return
			case *syntax.For:
				c.scopes = append(c.scopes, map[string]*local{})
				for _, init := range node.Init {
					walk(reflect.ValueOf(init), nil)
				}
				check(node.Cond, Bool)
				walk(reflect.ValueOf(node.Items), nil)
				bind(node.Name, nil, node)
				if kind := metadata.kind(node.Items); kind >= deriveFields {
					metadata.locals[c.lookup(node.Name)] = kind - deriveFields + deriveField
				}
				walk(reflect.ValueOf(node.Body), nil)
				for _, post := range node.Post {
					check(post.Value, nil)
				}
				c.scopes = c.scopes[:len(c.scopes)-1]
				return
			case syntax.Expr:
				if concrete(node) {
					check(node, want)
					return
				}
			}
			walk(v.Elem(), nil)
		case reflect.Struct:
			for _, index := range walkableSyntaxFields(v.Type()) {
				walk(v.Field(index), nil)
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), nil)
			}
		}
	}
	walk(reflect.ValueOf(method.Body), result)
}

func deriveLiteralExpression(expr syntax.Expr) bool {
	switch expr := expr.(type) {
	case *syntax.StringLit, *syntax.BoolLit, *syntax.IntLit, *syntax.FloatLit, *syntax.RuneLit:
		return true
	case *syntax.Unary:
		return deriveLiteralExpression(expr.X)
	case *syntax.Binary:
		return deriveLiteralExpression(expr.X) && deriveLiteralExpression(expr.Y)
	case *syntax.TupleLit:
		for _, elem := range expr.Elems {
			if !deriveLiteralExpression(elem) {
				return false
			}
		}
		return true
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
	for _, elem := range typ.Tuple {
		if !deriveConcreteType(elem, typeNames) {
			return false
		}
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
