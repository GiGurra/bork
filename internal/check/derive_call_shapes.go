package check

import (
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// Callable arity and argument labels do not depend on a target's shape. Check
// those declarations before expansion; dictionary and dependent type selection
// continue through the ordinary checker on the expanded call.
func (c *checker) checkDeriveCallShape(call *syntax.Call, localNames map[string]bool) {
	id, named := call.Fun.(*syntax.Ident)
	if !named || localNames[id.Name] || call.Pipe.File != "" {
		return
	}
	var fn *Func
	var genericCount int
	var helper bool
	if decl, pkg := c.deriveHelperNamed(c.pkg, id.Name); decl != nil {
		helper = true
		genericCount = len(decl.TypeParams)
		fn = &Func{Decl: decl, Pkg: pkg, Params: make([]Type, len(decl.Params)), defaultsChecked: true}
		typeNames := map[string]bool{}
		for _, parameter := range decl.TypeParams {
			typeNames[parameter.Name] = true
		}
		saved := c.pkg
		c.pkg = pkg
		for i := range fn.Params {
			fn.Params[i] = Invalid
			if deriveConcreteType(decl.Params[i].Type, typeNames) {
				fn.Params[i] = c.resolveType(decl.Params[i].Type)
			}
		}
		c.pkg = saved
	} else {
		if alias, member, qualified := strings.Cut(id.Name, "."); qualified {
			if pkg := c.pkg.imports[alias]; pkg != nil && pkg.Path == "bork/shape" {
				fn = pkg.Funcs[member]
				helper = true // Intrinsic queries also need explicit type handles.
			}
		}
		if fn == nil {
			fn, _ = c.funcNamed(id.Name)
		}
		if fn == nil {
			return
		}
		genericCount = len(fn.TypeParams)
	}
	if helper && len(call.TypeArgs) != genericCount || !helper && len(call.TypeArgs) != 0 && len(call.TypeArgs) != genericCount {
		c.errorf(call.Pos, "derive definition call to %s requires %d type arguments", id.Name, genericCount)
	}
	if hasNamedArgs(call) {
		// Replace defaults only in the temporary mapping signature. Their
		// runtime expressions are checked later in their own lexical context.
		proxy := *fn
		decl := *fn.Decl
		decl.Params = make([]*syntax.Param, len(fn.Decl.Params))
		for i, param := range fn.Decl.Params {
			copy := *param
			if param.Default != nil {
				copy.Default = &syntax.BoolLit{Pos: param.Default.Position()}
			}
			decl.Params[i] = &copy
		}
		proxy.Decl = &decl
		proxy.defaultsChecked = true
		c.namedArgs(call, id.Name, &proxy, call.Args)
		c.checkDeriveLiteralArguments(call, fn)
		return
	}
	required := len(fn.Params)
	for required > 0 && fn.Decl.Params[required-1].Default != nil {
		required--
	}
	if len(call.Args) < required || len(call.Args) > len(fn.Params) {
		c.errorf(call.Pos, "derive definition call to %s takes %d to %d arguments, found %d", id.Name, required, len(fn.Params), len(call.Args))
	}
	c.checkDeriveLiteralArguments(call, fn)
}

// Only literal source arguments with a resolved, target-independent parameter
// type are checked here. Dependent arguments remain expansion obligations.
func (c *checker) checkDeriveLiteralArguments(call *syntax.Call, fn *Func) {
	for i, arg := range call.Args {
		parameter := i
		if i < len(call.Arguments) && call.Arguments[i].Name != "" {
			parameter = -1
			for j, param := range fn.Decl.Params {
				if param.Name == call.Arguments[i].Name {
					parameter = j
					break
				}
			}
		}
		if parameter < 0 || parameter >= len(fn.Params) || !deriveLiteralExpression(arg) {
			continue
		}
		want := fn.Params[parameter]
		if want == nil || want == Invalid || hasTypeParam(want) || c.open(want) {
			continue
		}
		if actual := c.exprWant(arg, want); actual != Invalid && !assignable(actual, want) {
			c.errorf(arg.Position(), "derive call argument %s must be %s, found %s", fn.Decl.Params[parameter].Name, want, actual)
		}
	}
}
