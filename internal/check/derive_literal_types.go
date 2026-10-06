package check

import (
	"reflect"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Concrete expression islands use ordinary contextual typing at definition
// time. Symbolic targets and descriptor projections remain dependent: this pass
// never supplies a guessed runtime type for them.
func (c *checker) checkDeriveLiteralTypes(method *syntax.FuncDecl, typeNames map[string]bool) {
	if method.Body == nil {
		return
	}
	outer, effects, function := c.scopes, c.used, c.fn
	defer func() { c.scopes, c.used, c.fn = outer, effects, function }()
	partial := &Func{Decl: method, Pkg: c.pkg, Effects: c.effectsOf(method.Uses), Result: Invalid}
	c.fn = partial
	c.used = 0
	c.scopes = append(c.scopes, map[string]*local{})
	metadata := deriveMetadataTypes{c: c, locals: map[*local]deriveDescriptor{}, typeNames: typeNames}
	symbolic := deriveSymbolicTypes{c: c, metadata: &metadata, names: typeNames, locals: map[*local]*deriveTypeTerm{}}
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
		if typ != nil {
			typ = c.openParamAt(typ, param.Type)
		}
		bind(param.Name, typ, param)
		if typ == nil {
			typ = Invalid
		}
		partial.Params = append(partial.Params, typ)
		partial.ParamConstraints = append(partial.ParamConstraints, nil)
		symbolic.locals[c.lookup(param.Name)] = c.deriveOpenSignatureTerm(symbolic.annotation(param.Type, nil), param.Type, true)
		if kind := metadata.annotation(param.Type); kind != 0 {
			metadata.locals[c.lookup(param.Name)] = kind
		}
	}
	scope := c.paramScope()
	for i, parameter := range method.Params {
		if partial.Params[i] != Invalid && c.deriveIndependentPredicates(parameter.Type, scope) {
			partial.ParamConstraints[i] = c.constraintsOf(parameter.Type, partial.Params[i], scope)
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
			// Build calls register captured files during ordinary checking.
			// Definition checking reads their signature without capturing inputs.
			if fn.Effects&EffBuild != 0 {
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
		if method.Result != nil {
			result = c.openAt(result, method.Result)
		}
	}
	symbolicResult := c.deriveOpenSignatureTerm(symbolic.annotation(method.Result, nil), method.Result, false)
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
	var walk func(reflect.Value, Type, *deriveTypeTerm)
	walk = func(v reflect.Value, want Type, symbolicWant *deriveTypeTerm) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem(), want, symbolicWant)
			}
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if expression, ok := v.Interface().(syntax.Expr); ok {
				symbolic.check(expression, symbolicWant)
			}
			switch node := v.Interface().(type) {
			case *syntax.Comptime:
				outerEffects := c.used
				c.used = 0
				walk(reflect.ValueOf(node.Body), want, symbolicWant)
				c.checkDeriveKnownEffects(node.Pos, c.used, EffBuild, "comptime block")
				c.used = outerEffects
				return
			case *syntax.Block:
				c.scopes = append(c.scopes, map[string]*local{})
				for _, stmt := range node.Stmts {
					walk(reflect.ValueOf(stmt), nil, nil)
				}
				walk(reflect.ValueOf(node.Tail), want, symbolicWant)
				c.scopes = c.scopes[:len(c.scopes)-1]
				return
			case *syntax.Lambda:
				// A lambda has its own return context and lexical parameters,
				// even when its body contains dependent descriptor operations.
				// Check the independent parts without guessing those operations'
				// types or inferring an unannotated symbolic parameter.
				context, _ := want.(*FuncType)
				var symbolicContext *deriveTypeTerm
				if context == nil && symbolicWant != nil && symbolicWant.head == "function" && len(symbolicWant.args) != len(node.Params)+1 {
					c.errorf(node.Pos, "expected a function taking %d argument(s), but this lambda takes %d", len(symbolicWant.args)-1, len(node.Params))
				}
				if symbolicWant != nil && symbolicWant.head == "function" && len(symbolicWant.args) == len(node.Params)+1 {
					symbolicContext = symbolicWant
				}
				if context != nil && len(context.Params) != len(node.Params) {
					c.errorf(node.Pos, "expected a function taking %d argument(s), but this lambda takes %d", len(context.Params), len(node.Params))
					context = nil
				}
				c.scopes = append(c.scopes, map[string]*local{})
				for i, parameter := range node.Params {
					var typ Type
					if parameter.Type != nil && deriveConcreteType(parameter.Type, typeNames) {
						typ = c.resolveType(parameter.Type)
						if context != nil && independentDeriveType(context.Params[i]) != nil && typ != Invalid && !identical(typ, context.Params[i]) {
							c.errorf(parameter.Type.Pos, "parameter %s must be %s here, found %s", parameter.Name, context.Params[i], typ)
						}
					} else if parameter.Type == nil && context != nil {
						typ = independentDeriveType(context.Params[i])
					}
					parameterTerm := symbolic.annotation(parameter.Type, nil)
					if symbolicContext != nil {
						if parameter.Type == nil {
							parameterTerm = symbolicContext.args[i]
						} else if parameterTerm != nil && symbolicContext.args[i] != nil && (parameterTerm.dependent || symbolicContext.args[i].dependent) && deriveTermMismatch(parameterTerm, symbolicContext.args[i]) {
							c.errorf(parameter.Pos, "derive parameter %s must be %s, found %s", parameter.Name, symbolicContext.args[i], parameterTerm)
						}
					}
					if typ == nil && parameter.Type == nil {
						typ = deriveConcreteTermType(parameterTerm)
					}
					bind(parameter.Name, typ, parameter)
					if parameter.Type != nil && typ != nil && typ != Invalid && symbolicContext != nil && (context == nil || independentDeriveType(context.Params[i]) == nil) {
						if expected := deriveConcreteTermType(symbolicContext.args[i]); expected != nil && !identical(typ, expected) {
							c.errorf(parameter.Type.Pos, "parameter %s must be %s here, found %s", parameter.Name, expected, typ)
						}
					}
					symbolic.locals[c.lookup(parameter.Name)] = parameterTerm
					kind := metadata.annotation(parameter.Type)
					if kind == 0 && parameter.Type == nil && context != nil {
						kind = deriveResolvedDescriptor(context.Params[i])
					}
					if kind == 0 {
						kind = deriveTermDescriptor(parameterTerm)
					}
					if kind != 0 {
						metadata.locals[c.lookup(parameter.Name)] = kind
					}
				}
				outerSymbolicResult := symbolicResult
				symbolicResult = nil
				if symbolicContext != nil {
					symbolicResult = symbolicContext.args[len(node.Params)]
					if deriveConcreteTermType(symbolicResult) == Ok {
						symbolicResult = nil
					}
				}
				outerResult := result
				result = nil
				if context != nil && context.Result != Ok {
					result = independentDeriveType(context.Result)
				}
				if result == nil {
					result = deriveConcreteTermType(symbolicResult)
				}
				outerEffects := c.used
				c.used = 0
				walk(reflect.ValueOf(node.Body), result, symbolicResult)
				if context != nil {
					c.checkDeriveKnownEffects(node.Pos, c.used, context.Effects, "lambda")
				} else if symbolicContext != nil {
					c.checkDeriveKnownEffects(node.Pos, c.used, symbolicContext.effects, "lambda")
				}
				c.used = outerEffects
				result = outerResult
				symbolicResult = outerSymbolicResult
				c.scopes = c.scopes[:len(c.scopes)-1]
				return
			case *syntax.If:
				walk(reflect.ValueOf(node.Cond), Bool, deriveNativeTerm(Bool, nil))
				walk(reflect.ValueOf(node.Then), want, symbolicWant)
				walk(reflect.ValueOf(node.Else), want, symbolicWant)
				return
			case *syntax.Match:
				walk(reflect.ValueOf(node.X), nil, nil)
				for _, arm := range node.Arms {
					// Pattern values remain dependent, but unrelated outer
					// locals and descriptor identities keep their known types.
					c.scopes = append(c.scopes, map[string]*local{})
					bindPattern(arm.Pattern)
					walk(reflect.ValueOf(arm.Body), want, symbolicWant)
					c.scopes = c.scopes[:len(c.scopes)-1]
				}
				return
			case *syntax.Return:
				walk(reflect.ValueOf(node.Value), result, symbolicResult)
				return
			case *syntax.Binding:
				symbolicDeclared := symbolic.annotation(node.Type, nil)
				symbolicActual := symbolic.expr(node.Value)
				if node.Type != nil {
					symbolicActual = symbolicDeclared
				}
				var declared Type
				if node.Type != nil && deriveConcreteType(node.Type, typeNames) {
					declared = c.resolveType(node.Type)
				}
				actual := check(node.Value, declared)
				if actual != nil {
					symbolic.check(node.Value, symbolicDeclared)
				}
				if actual == nil {
					walk(reflect.ValueOf(node.Value), declared, symbolicDeclared)
					if call, ok := node.Value.(*syntax.Call); ok {
						if signature := c.deriveCallSignature(call, typeNames); signature != nil {
							actual = signature.result
						}
					}
				}
				if actual == nil {
					actual = deriveConcreteTermType(symbolicActual)
				}
				if node.Type != nil && declared == nil {
					actual = nil
				}
				if declared != nil {
					actual = declared
				}
				bind(node.Name, actual, node)
				symbolic.locals[c.lookup(node.Name)] = symbolicActual
				kind := metadata.kind(node.Value)
				if kind == 0 {
					kind = metadata.annotation(node.Type)
				}
				if kind == 0 {
					kind = deriveTermDescriptor(symbolicActual)
				}
				if kind != 0 {
					metadata.locals[c.lookup(node.Name)] = kind
				}
				return
			case *syntax.For:
				c.scopes = append(c.scopes, map[string]*local{})
				for _, init := range node.Init {
					walk(reflect.ValueOf(init), nil, nil)
				}
				walk(reflect.ValueOf(node.Cond), Bool, deriveNativeTerm(Bool, nil))
				walk(reflect.ValueOf(node.Items), nil, nil)
				bind(node.Name, nil, node)
				if kind := metadata.kind(node.Items); kind >= deriveFields {
					metadata.locals[c.lookup(node.Name)] = kind - deriveFields + deriveField
					if !node.Comptime {
						c.errorf(node.Pos, "shape metadata cannot be iterated by a runtime for; add comptime")
						c.diags.Suggest(node.Pos, "type.error", node.Pos, diag.Fix{Message: "add comptime", Edits: []diag.TextEdit{{Start: node.Pos, End: node.Pos, Replacement: "comptime "}}})
					}
				}
				walk(reflect.ValueOf(node.Body), nil, nil)
				for _, post := range node.Post {
					check(post.Value, nil)
				}
				c.scopes = c.scopes[:len(c.scopes)-1]
				return
			case *syntax.Selector:
				if concrete(node) {
					check(node, want)
					return
				}
				metadata.checkMember(node)
			case *syntax.Call:
				symbolicParams, symbolicCallResult, symbolicNames := symbolic.call(node)
				signature := c.deriveCallSignature(node, typeNames)
				if signature != nil {
					c.used |= signature.effects
				} else if function := symbolic.expr(node.Fun); function != nil && function.head == "function" {
					c.used |= function.effects
				} else if id, named := node.Fun.(*syntax.Ident); named && c.lookup(id.Name) == nil {
					if kind := builtins[id.Name]; kind == BuiltinPrintln || kind == BuiltinAssertSnapshot {
						c.used |= EffIO
					}
				}
				if concrete(node) {
					check(node, want)
					return
				}
				metadata.checkCall(node)
				if signature != nil {
					if want != nil && signature.result != nil && !assignable(signature.result, want) {
						c.errorf(node.Pos, "derive expression must be %s, found %s", want, signature.result)
					}
					for i, argument := range node.Args {
						// Literal arguments already have a signature check in the
						// definition call-shape pass; lexical values need this context.
						if deriveLiteralExpression(argument) {
							symbolic.check(argument, deriveTermArgument(node, i, symbolicParams, symbolicNames))
							continue
						}
						context := signature.argument(node, i)
						if _, lambda := argument.(*syntax.Lambda); !lambda && context != nil && hasTypeParam(context) {
							context = nil
						}
						walk(reflect.ValueOf(argument), context, deriveTermArgument(node, i, symbolicParams, symbolicNames))
					}
					walk(reflect.ValueOf(node.Fun), nil, nil)
					return
				}

				if symbolicParams != nil {
					if actual := deriveConcreteTermType(symbolicCallResult); want != nil && actual != nil && !assignable(actual, want) {
						c.errorf(node.Pos, "derive expression must be %s, found %s", want, actual)
					}
					for i, argument := range node.Args {

						argumentTerm := deriveTermArgument(node, i, symbolicParams, symbolicNames)
						walk(reflect.ValueOf(argument), deriveConcreteTermType(argumentTerm), argumentTerm)
					}
					walk(reflect.ValueOf(node.Fun), nil, nil)
					return
				}
			case syntax.Expr:
				if concrete(node) {
					check(node, want)
					return
				}
			}
			walk(v.Elem(), nil, nil)
		case reflect.Struct:
			for _, index := range walkableSyntaxFields(v.Type()) {
				walk(v.Field(index), nil, nil)
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), nil, nil)
			}
		}
	}
	walk(reflect.ValueOf(method.Body), result, symbolicResult)
	c.checkDeriveKnownEffects(method.Pos, c.used, partial.Effects, method.Name)
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
	if _, member, projected := strings.Cut(typ.Name, "."); projected && (member == "Type" || member == "RawType") {
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

// Unknown predicate inputs remain expansion obligations. Resolved parameter
// annotations retain their declared facts for independent dictionary selection.
func (c *checker) deriveIndependentPredicates(written *syntax.TypeExpr, scope map[string]Type) bool {
	if written == nil {
		return true
	}
	var independent func(*syntax.PredRef) bool
	independent = func(predicate *syntax.PredRef) bool {
		if typ, parameter := scope[predicate.Name]; parameter {
			if typ == Invalid || len(predicate.Args) != 0 {
				return false
			}
		} else if function, found := c.funcNamed(predicate.Name); !found || !function.Decl.IsPred || len(predicate.Args) != len(function.Params)-1 {
			// Reference and arity errors were reported by the lexical pass.
			return false
		}
		for _, argument := range predicate.Args {
			if id, named := argument.(*syntax.Ident); named {
				if typ, parameter := scope[id.Name]; parameter && typ == Invalid {
					return false
				}
			}
		}
		for _, alternative := range predicate.Or {
			if !independent(alternative) {
				return false
			}
		}
		return true
	}
	for _, predicate := range written.Where {
		if !independent(predicate) {
			return false
		}
	}
	for _, child := range written.Args {
		if !c.deriveIndependentPredicates(child, scope) {
			return false
		}
	}
	for _, child := range written.Tuple {
		if !c.deriveIndependentPredicates(child, scope) {
			return false
		}
	}
	for _, child := range written.Union {
		if !c.deriveIndependentPredicates(child, scope) {
			return false
		}
	}
	if written.Func != nil {
		for _, parameter := range written.Func.Params {
			if !c.deriveIndependentPredicates(parameter, scope) {
				return false
			}
		}
		return c.deriveIndependentPredicates(written.Func.Result, scope)
	}
	return true
}
