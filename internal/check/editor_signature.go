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
	for call, instance := range info.instances {
		if call.Pos != open || instance == nil {
			continue
		}
		bound := false
		if selector, ok := call.Fun.(*syntax.Selector); ok && instance.Func.Decl.IsMethod {
			bound = info.types[selector.X] != nil
		}
		signature := editorSignature(instance.Func, instance.Params, instance.Result, from, bound)
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
	var fn *Func
	if receiver != nil {
		fn, _ = c.methodNamed(receiver, name)
	} else {
		fn, _ = c.funcNamed(name)
		if fn == nil {
			if split := strings.LastIndexByte(name, '.'); split > 0 {
				selector := &syntax.Selector{X: &syntax.Ident{Name: name[:split]}, Name: name[split+1:]}
				fn, _, _ = c.methodReference(selector)
			}
		}
	}
	if fn == nil {
		return nil
	}
	in := newInference(fn)
	if receiver != nil {
		in.unify(fn.Params[0], receiver)
		if !assignable(receiver, in.subst(fn.Params[0])) {
			return nil
		}
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
