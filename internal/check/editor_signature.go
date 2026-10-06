package check

import (
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
	"strings"
)

// EditorSignature describes a callable without evaluating its facts.
type EditorSignature struct {
	Name, Result, Effects string
	Callable              *CallableDescription
	Definition            *diag.Pos
	ImplicitParameters    int
}

func editorSignature(fn *Func, params []Type, result Type, from *Package, bound bool) *EditorSignature {
	if bound {
		params = params[1:]
	}
	pos := fn.Decl.Pos
	return &EditorSignature{Name: fn.Decl.Name, Result: TypeText(result, from), Effects: fn.Effects.String(), Callable: DescribeCallable(fn, params, from, bound), Definition: &pos}
}

// EditorCheckedSignature returns the exact instantiation at a checked call.
func EditorCheckedSignature(info *Info, from *Package, open diag.Pos) *EditorSignature {
	// Predicate guards synthesized by an assertion share its source position.
	// Prefer the actual assertion identity over those internal calls.
	for call, literal := range info.variantCalls {
		if call.Pos != open {
			continue
		}
		variant, ok := info.recordTargets[literal].(*Variant)
		if !ok {
			continue
		}
		return editorVariantSignature(variant, from)
	}
	for call, assertion := range info.patternAssertions {
		if call.Pos == open {
			instance := info.instances[call]
			signature := editorSignature(instance.Func, instance.Params, instance.Result, from, false)
			signature.Result = assertion.Expected
			if call.Pipe.File != "" {
				signature.ImplicitParameters = 1
			}
			return signature
		}
	}
	for call, instance := range info.instances {
		if call.Pos != open || instance == nil {
			continue
		}
		bound := false
		if selector, ok := call.Fun.(*syntax.Selector); ok && instance.Func.Decl.IsMethod {
			bound = info.types[selector.X] != nil
		}
		if pkg := instance.Func.TemplatePkg; pkg != nil && instance.Func.Decl.Instance == nil {
			for _, helper := range pkg.deriveHelpers {
				if helper.Pos == instance.Func.Decl.Pos {
					return editorDeriveSignature(helper)
				}
			}
		}
		signature := editorSignature(instance.Func, instance.Params, instance.Result, from, bound)
		if assertion := info.patternAssertions[call]; assertion != nil {
			signature.Result = assertion.Expected
		}
		if call.Pipe.File != "" {
			signature.ImplicitParameters = 1
		}
		return signature
	}
	for call := range info.types {
		if call, ok := call.(*syntax.Call); ok && call.Pos == open {
			if typ, ok := info.types[call.Fun].(*FuncType); ok {
				return EditorFunctionSignature("", typ, from)
			}
		}
	}
	return nil
}

// EditorNamedSignature resolves unfinished calls with normal package/method
// lookup and compiler substitution. Unsolved generic parameters remain named.
func EditorNamedSignature(info *Info, from *Package, name string, receiver Type, typeArgs []*syntax.TypeExpr, site diag.Pos) *EditorSignature {
	c := editorTypeQueryChecker(info, from)
	for _, context := range info.FuncOf {
		body := context.Decl.Body
		if body != nil && body.Pos.File == site.File && (body.Pos.Line < site.Line || body.Pos.Line == site.Line && body.Pos.Col <= site.Col) && (site.Line < body.End.Line || site.Line == body.End.Line && site.Col < body.End.Col) {
			c.useTypeParams(context)
			break
		}
	}
	if receiver == nil && len(typeArgs) == 0 {
		if signature := c.editorVariantSignatureNamed(name, from, site); signature != nil {
			return signature
		}
	}
	var fn *Func
	if receiver != nil {
		fn, _ = c.methodNamed(receiver, name)
	} else {
		if helper, _ := c.deriveHelperNamed(from, name); helper != nil {
			return editorDeriveSignature(helper)
		}
		fn, _ = c.funcNamed(name)
		if fn == nil {
			if split := strings.LastIndexByte(name, '.'); split > 0 {
				selector := &syntax.Selector{X: &syntax.Ident{Name: name[:split]}, Name: name[split+1:]}
				fn, _, _ = c.methodReference(selector)
			}
		}
	}
	if fn == nil {
		if receiver == nil {
			if helper, _ := c.deriveHelperNamed(from, name); helper != nil {
				return editorDeriveSignature(helper)
			}
		}
		return nil
	}
	in := newInference(fn)
	if receiver != nil {
		in.unify(fn.Params[0], receiver)
		if !assignable(receiver, in.subst(fn.Params[0])) {
			return nil
		}
	}
	if assertIsIntrinsic(fn) && len(typeArgs) == 1 {
		typeArgs = append(append([]*syntax.TypeExpr(nil), typeArgs...), nil)
	}
	if len(typeArgs) > 0 {
		if receiver != nil {
			var ok bool
			typeArgs, ok = c.methodTypeArgs(&syntax.Call{TypeArgs: typeArgs}, &syntax.Selector{Name: name}, fn)
			if !ok {
				return nil
			}
		}
		if len(typeArgs) != len(fn.TypeParams) {
			return nil
		}
		for i, arg := range typeArgs {
			if arg == nil {
				continue
			}
			typ := c.resolveType(arg)
			if typ == Invalid {
				return nil
			}
			in.bound[fn.TypeParams[i]] = typ
		}
	}
	params := make([]Type, len(fn.Params))
	for i, param := range fn.Params {
		params[i] = in.subst(param)
	}
	if receiver != nil && !assignable(receiver, params[0]) {
		return nil
	}
	return editorSignature(fn, params, in.subst(fn.Result), from, receiver != nil)
}

// EditorFunctionSignature exposes unnamed function-value parameters honestly.
func EditorFunctionSignature(name string, typ Type, from *Package) *EditorSignature {
	ft, ok := typ.(*FuncType)
	if !ok {
		return nil
	}
	callable := &CallableDescription{Parameters: []ParameterDescription{}}
	for _, param := range ft.Params {
		callable.Parameters = append(callable.Parameters, ParameterDescription{Type: TypeText(param, from)})
	}
	return &EditorSignature{Name: name, Result: TypeText(ft.Result, from), Effects: ft.Effects.String(), Callable: callable}
}

// EditorActiveParameter uses the same positional/named assignment as checking.
// A new argument after named ones selects the first parameter not supplied.
func EditorActiveParameter(callable *CallableDescription, previous []syntax.Argument, currentName string) *int {
	names := make([]string, len(callable.Parameters))
	for i, param := range callable.Parameters {
		names[i] = param.Name
	}
	used := map[int]bool{}
	next := 0
	named := false
	for _, arg := range previous {
		index := argumentIndex(names, arg.Name, next)
		used[index] = true
		if arg.Name == "" {
			next++
		} else {
			named = true
		}
	}
	index := argumentIndex(names, currentName, next)
	if currentName == "" && named {
		index = 0
		for index < len(names) && used[index] {
			index++
		}
	}
	if index < 0 || index >= len(names) {
		return nil
	}
	return &index
}

func editorVariantSignature(variant *Variant, from *Package) *EditorSignature {
	pos := variant.Parent.Decl.Variants[variant.Index].Pos
	return &EditorSignature{Name: variant.Parent.Name + "." + variant.Name, Result: TypeText(variant.Parent, from), Effects: "nothing", Callable: DescribeVariantCallable(variant, from), Definition: &pos}
}

func (c *checker) editorVariantSignatureNamed(name string, from *Package, site diag.Pos) *EditorSignature {
	if !strings.Contains(name, ".") {
		return nil
	}
	saved := c.diags
	c.diags = &diag.List{}
	defer func() { c.diags = saved }()
	parsed := syntax.Parse("signature-query.bork", []byte("fn SignatureQuery(){"+name+"(0)}"), c.diags)
	if len(parsed.Funcs) == 0 || parsed.Funcs[0].Body == nil {
		return nil
	}
	call, ok := parsed.Funcs[0].Body.Tail.(*syntax.Call)
	if !ok {
		return nil
	}
	head, ok := call.Fun.(*syntax.Selector)
	if !ok {
		return nil
	}
	var owner Type
	switch head := head.X.(type) {
	case *syntax.Ident:
		owner = c.typeNamed(head.Name)
	case *syntax.TypeHead:
		owner = c.resolveType(head.Type)
	}
	sealed, ok := owner.(*Sealed)
	if !ok {
		return nil
	}
	variant := c.specializedVariant(site, sealed, head.Name)
	if variant == nil || !variant.Positional || c.diags.Len() != 0 {
		return nil
	}
	return editorVariantSignature(variant, from)
}

// A source helper can have descriptors erased from runtime specializations.
// Signature help shows its complete declaration rather than a generated name.
func editorDeriveSignature(helper *syntax.FuncDecl) *EditorSignature {
	callable := &CallableDescription{NamedArguments: true, ParameterNamesAreAPI: true}
	for _, param := range helper.Params {
		callable.Parameters = append(callable.Parameters, ParameterDescription{Name: param.Name, Type: writtenTypeText(param.Type)})
	}
	if helper.Requires != nil {
		callable.Requires = []string{deriveSignatureExpr(helper.Requires)}
	}
	if helper.Needs != nil {
		for _, need := range helper.Needs.Items {
			name := need.Name
			if need.Optional {
				name += "?"
			}
			callable.Needs = append(callable.Needs, name)
		}
	}
	var effects []string
	if helper.Uses != nil {
		for _, effect := range helper.Uses.Effects {
			effects = append(effects, effect.Name)
		}
	}
	if len(effects) == 0 {
		effects = append(effects, "nothing")
	}
	result := "Ok"
	if helper.Result != nil {
		result = writtenTypeText(helper.Result)
	}
	pos := helper.Pos
	return &EditorSignature{Name: helper.Name, Result: result, Effects: strings.Join(effects, " + "), Callable: callable, Definition: &pos}
}
