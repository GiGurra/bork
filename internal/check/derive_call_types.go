package check

import (
	"github.com/GiGurra/bork/internal/syntax"
)

// A dependent call still has independent parts of its declared signature.
// Reading them selects neither a dictionary nor a native evaluator result;
// projected heads and formal parameters remain expansion obligations.
type deriveCallSignature struct {
	params  []Type
	result  Type
	names   []string
	effects Effects
}

func (c *checker) deriveCallSignature(call *syntax.Call, typeNames map[string]bool) *deriveCallSignature {
	id, ok := call.Fun.(*syntax.Ident)
	if !ok || call.Pipe.File != "" {
		return nil
	}
	if local := c.lookup(id.Name); local != nil {
		typ, ok := local.typ.(*FuncType)
		if !ok {
			return nil
		}
		return &deriveCallSignature{params: typ.Params, result: independentDeriveType(typ.Result), effects: typ.Effects}
	}
	if helper, pkg := c.deriveHelperNamed(c.pkg, id.Name); helper != nil {
		signature := &deriveCallSignature{params: make([]Type, len(helper.Params)), names: make([]string, len(helper.Params)), effects: deriveWrittenEffects(helper.Uses)}
		helperTypes := map[string]bool{}
		actuals := map[string]Type{}
		for i, parameter := range helper.TypeParams {
			helperTypes[parameter.Name] = true
			if len(call.TypeArgs) == len(helper.TypeParams) && len(call.TypeArgs[i].Where) == 0 && deriveConcreteType(call.TypeArgs[i], typeNames) {
				if actual := c.resolveType(call.TypeArgs[i]); actual != Invalid {
					actuals[parameter.Name] = actual
					delete(helperTypes, parameter.Name)
				}
			}
		}
		saved := c.pkg
		c.pkg = pkg
		defer func() { c.pkg = saved }()
		for i, parameter := range helper.Params {
			signature.names[i] = parameter.Name
			if parameter.Type != nil && deriveConcreteType(parameter.Type, helperTypes) {
				signature.params[i] = c.openParamAt(c.deriveKnownHelperType(parameter.Type, actuals), parameter.Type)
			}
		}
		if deriveConcreteType(helper.Result, helperTypes) {
			signature.result = c.openAt(c.deriveKnownHelperType(helper.Result, actuals), helper.Result)
		}
		return signature
	}
	fn, found := c.funcNamed(id.Name)
	if !found || fn.Pkg != nil && fn.Pkg.Path == "bork/shape" {
		return nil
	}
	bound := map[*TypeParam]Type{}
	if len(call.TypeArgs) == len(fn.TypeParams) {
		for i, argument := range call.TypeArgs {
			if len(argument.Where) == 0 && deriveConcreteType(argument, typeNames) {
				if actual := c.resolveType(argument); actual != Invalid {
					bound[fn.TypeParams[i]] = actual
				}
			}
		}
	}
	signature := &deriveCallSignature{params: make([]Type, len(fn.Params)), names: make([]string, len(fn.Params)), result: independentDeriveType(subst(fn.Result, bound)), effects: fn.Effects}
	for i, parameter := range fn.Params {
		signature.params[i] = deriveArgumentContext(subst(parameter, bound))
		if i < len(fn.Decl.Params) {
			signature.names[i] = fn.Decl.Params[i].Name
		}
	}
	return signature
}

func independentDeriveType(typ Type) Type {
	if typ == nil || typ == Invalid || hasTypeParam(typ) {
		return nil
	}
	return typ
}

func (signature *deriveCallSignature) argument(call *syntax.Call, index int) Type {
	parameter := index
	if index < len(call.Arguments) && call.Arguments[index].Name != "" {
		parameter = -1
		for i, name := range signature.names {
			if name == call.Arguments[index].Name {
				parameter = i
				break
			}
		}
	}
	if parameter < 0 || parameter >= len(signature.params) {
		return nil
	}
	return signature.params[parameter]
}

// A lambda can use a partial declared function context: arity, metadata
// descriptor kinds and an independent result survive unknown input heads.
// Other expressions must not be compared against this dependent function type.
func deriveArgumentContext(typ Type) Type {
	if _, ok := typ.(*FuncType); ok {
		return typ
	}
	return independentDeriveType(typ)
}

func deriveResolvedDescriptor(typ Type) deriveDescriptor {
	record, ok := typ.(*Record)
	if !ok || record.Pkg == nil || record.Pkg.Path != "bork/shape" {
		return 0
	}
	switch record.Name {
	case "Field":
		return deriveField
	case "Variant":
		return deriveVariant
	case "Fact":
		return deriveFact
	case "PackageTag":
		return derivePackageTag
	}
	return 0
}

// Bind only caller-resolved concrete arguments in a fresh annotation tree.
// The helper's lexical package still resolves its named constructors; no
// symbolic field head or dictionary requirement is invented for unknown args.
func (c *checker) deriveKnownHelperType(source *syntax.TypeExpr, actuals map[string]Type) Type {
	var overrides []*syntax.TypeExpr
	defer func() {
		for _, node := range overrides {
			delete(c.info.assemblyTypes, node)
		}
	}()
	var clone func(*syntax.TypeExpr) *syntax.TypeExpr
	clone = func(node *syntax.TypeExpr) *syntax.TypeExpr {
		if node == nil {
			return nil
		}
		copy := *node
		result := &copy
		if actual := actuals[node.Name]; actual != nil && len(node.Args) == 0 {
			c.info.assemblyTypes[result] = actual
			overrides = append(overrides, result)
		}
		children := func(nodes []*syntax.TypeExpr) []*syntax.TypeExpr {
			if nodes == nil {
				return nil
			}
			result := make([]*syntax.TypeExpr, len(nodes))
			for i, node := range nodes {
				result[i] = clone(node)
			}
			return result
		}
		result.Args = children(node.Args)
		result.Tuple = children(node.Tuple)
		result.Union = children(node.Union)
		if node.Func != nil {
			function := *node.Func
			function.Params = children(node.Func.Params)
			function.Result = clone(node.Func.Result)
			result.Func = &function
		}
		return result
	}
	return c.resolveType(clone(source))
}
