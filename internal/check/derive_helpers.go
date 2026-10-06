package check

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

func (c *checker) declareDeriveHelpers(files []*syntax.File) {
	for _, file := range files {
		c.inFile(file)
		if c.pkg.deriveHelpers == nil {
			c.pkg.deriveHelpers = map[string]*syntax.FuncDecl{}
		}
		for _, helper := range file.DeriveHelpers {
			if helper.IsGo() || helper.Body == nil || helper.IsMethod || helper.Constructor != nil {
				c.errorf(helper.Pos, "derive helpers require an ordinary Bork function body")
				continue
			}
			if previous := c.pkg.deriveHelpers[helper.Name]; previous != nil {
				c.errorf(helper.Pos, "derive helper %s is already declared at %s", helper.Name, previous.Pos)
				continue
			}
			valid := true
			for _, parameter := range helper.Params {
				if parameter.Default != nil || parameter.In != "" {
					c.errorf(parameter.Pos, "derive helper parameters cannot have defaults or scope annotations")
					valid = false
				}
			}
			if valid {
				c.pkg.deriveHelpers[helper.Name] = helper
			}
		}
	}
}

func (c *checker) deriveHelperNamed(from *Package, name string) (*syntax.FuncDecl, *Package) {
	if alias, member, qualified := strings.Cut(name, "."); qualified {
		pkg := from.imports[alias]
		if pkg != nil && Exported(member) && pkg.deriveHelpers[member] != nil {
			from.used[alias] = true
			return pkg.deriveHelpers[member], pkg
		}
		return nil, nil
	}
	return from.deriveHelpers[name], from
}

func (p *deriveExpansion) descriptorType(name string, owner Type) Type {
	return instantiate(p.c.pkgs["bork/shape"].TypeNamed(name), []Type{owner})
}

func (p *deriveExpansion) metadataType(value any) Type {
	switch value := value.(type) {
	case metadataList:
		if value.element != nil {
			return &List{Elem: value.element}
		}
	case metadataChecked:
		if value.raw {
			return p.c.info.optionPayloads[value.value]
		}
		return p.c.info.types[value.value]
	case bool:
		return Bool
	case int64:
		return Int
	case string:
		return String
	case shapeEnum:
		return p.c.pkgs["bork/shape"].TypeNamed("Kind")
	case shapeField:
		return p.descriptorType("Field", value.owner)
	case shapeVariant:
		return p.descriptorType("Variant", value.variant.Parent)
	case shapeFact:
		return p.descriptorType("Fact", value.owner)
	case shapeSequence:
		if value.element != nil {
			return &List{Elem: value.element}
		}
	}
	return Invalid
}

func (p *deriveExpansion) checkMetadataType(written *syntax.TypeExpr, value any, pos diag.Pos) {
	if written == nil {
		return
	}
	saved := p.c.pkg
	p.c.pkg = p.template.Pkg
	defer func() { p.c.pkg = saved }()
	clone := p.clone(reflect.ValueOf(written)).Interface().(*syntax.TypeExpr)
	want := p.c.resolveType(clone)
	switch value.(type) {
	case metadataRecord, metadataList, metadataVariant:
		if !layoutValue(value, want) {
			p.error(pos, "compile-time value must be %s", want)
		}
		p.c.whereReported(clone)
		return
	}
	actual := p.metadataType(value)
	if actual == Invalid || !identical(actual, want) {
		p.error(pos, "compile-time value must be %s, found %s", want, actual)
	}
	// Descriptor annotations constrain metadata, not runtime values or facts.
	p.c.whereReported(clone)
	if p.c.hasFacts(clone) || len(p.c.info.shapeTypeFacts[clone]) > 0 {
		p.error(pos, "compile-time metadata annotations cannot introduce runtime facts")
	}
}

func (p *deriveExpansion) helperPlan(call *syntax.Call, helper *syntax.FuncDecl, pkg *Package) (*deriveExpansion, bool) {
	if len(call.TypeArgs) != len(helper.TypeParams) || len(call.Args) != len(helper.Params) {
		p.error(call.Pos, "derive helper %s requires %d explicit type arguments and %d arguments", helper.Name, len(helper.TypeParams), len(helper.Params))
		return nil, false
	}
	child := &deriveExpansion{c: p.c, template: &DeriveTemplate{Pkg: pkg}, scope: p.scope, instance: p.instance, env: map[string]any{}, origins: map[string]diag.Pos{}, typeFacts: map[string][]*Constraint{}, names: map[string]bool{}, active: p.active, budget: p.budget, layout: p.layout}
	fn := &Func{Decl: helper, Pkg: pkg, Params: make([]Type, len(helper.Params)), defaultsChecked: true}
	for i := range fn.Params {
		fn.Params[i] = Invalid
	}
	args, valid := p.c.namedArgs(call, helper.Name, fn, call.Args)
	if !valid {
		p.failed = true
		return nil, false
	}
	child.helperArgs = args
	child.helperOrder = p.c.info.callOrder[call]
	if len(child.helperOrder) == 0 {
		for i := range args {
			child.helperOrder = append(child.helperOrder, i)
		}
	}
	for i, parameter := range helper.TypeParams {
		typ, facts := p.projectedTypeArg(call.TypeArgs[i])
		child.env[parameter.Name] = typ
		child.typeFacts[parameter.Name] = facts
	}
	return child, true
}

func (p *deriveExpansion) evalHelper(call *syntax.Call, helper *syntax.FuncDecl, pkg *Package) (any, bool) {
	if p.active == nil {
		p.active = map[*syntax.FuncDecl]bool{}
	}
	if p.active[helper] {
		return nil, false
	}
	child, valid := p.helperPlan(call, helper, pkg)
	if !valid {
		return nil, false
	}
	for i, parameter := range helper.Params {
		value, known := p.eval(child.helperArgs[i])
		if !known {
			return nil, false
		}
		child.env[parameter.Name] = value
		child.origins[parameter.Name] = parameter.Pos
	}
	if helper.Result == nil || helper.Uses != nil || helper.Needs != nil || helper.Requires != nil || helper.Result != nil && len(helper.Result.Where) > 0 {
		return nil, false
	}
	for _, parameter := range helper.TypeParams {
		if len(parameter.Bounds) > 0 {
			return nil, false
		}
	}
	p.active[helper] = true
	defer delete(p.active, helper)
	saved := p.c.pkg
	p.c.pkg = pkg
	defer func() { p.c.pkg = saved }()
	for _, parameter := range helper.Params {
		child.checkMetadataType(parameter.Type, child.env[parameter.Name], parameter.Pos)
	}
	value, known := child.evalBlock(helper.Body)
	p.failed = p.failed || child.failed
	if known {
		child.checkMetadataType(helper.Result, value, helper.Pos)
	}
	return value, known && !child.failed
}

func (p *deriveExpansion) evalBlock(block *syntax.Block) (any, bool) {
	savedEnv, savedOrigins := p.env, p.origins
	p.env = make(map[string]any, len(savedEnv))
	for name, value := range savedEnv {
		p.env[name] = value
	}
	p.origins = make(map[string]diag.Pos, len(savedOrigins))
	for name, origin := range savedOrigins {
		p.origins[name] = origin
	}
	defer func() { p.env, p.origins = savedEnv, savedOrigins }()
	for _, statement := range block.Stmts {
		switch statement := statement.(type) {
		case *syntax.Binding:
			value, known := p.eval(statement.Value)
			if !known || statement.Lazy || statement.AsyncScope != nil {
				return nil, false
			}
			p.checkMetadataType(statement.Type, value, statement.Pos)
			if _, exists := p.env[statement.Name]; exists {
				p.error(statement.Pos, "%s is already defined in an enclosing compile-time scope", statement.Name)
			}
			p.env[statement.Name] = value
			p.origins[statement.Name] = statement.Pos
		case *syntax.ExprStmt:
			if returned, yes := statement.X.(*syntax.Return); yes && returned.Value != nil {
				return p.eval(returned.Value)
			}
			return nil, false
		default:
			return nil, false
		}
	}
	if block.Tail == nil {
		return nil, false
	}
	if returned, yes := block.Tail.(*syntax.Return); yes && returned.Value != nil {
		return p.eval(returned.Value)
	}
	return p.eval(block.Tail)
}

// Local typed-function memoization preserves logical expansion work and peak
// depth. A provisional function can serve recursive references, but only a
// successful complete expansion receives a reusable cost receipt. Helpers
// with deferred builder validation (including callers of those helpers) remain
// uncached until their complete validation cost can be represented in a plan.
type deriveSpecializationCost struct{ work, depth int }

func (p *deriveExpansion) runtimeHelper(call *syntax.Call, helper *syntax.FuncDecl, pkg *Package) syntax.Expr {
	child, valid := p.helperPlan(call, helper, pkg)
	if !valid || p.instance == nil {
		return &syntax.Block{Pos: call.Pos}
	}
	// Ordinary runtime arguments are retained; descriptor arguments specialize
	// source code and never appear in the resulting function signature.
	var runtimeArgs []syntax.Expr
	var runtimeParameters []*syntax.Param
	runtimeIndices := map[int]int{}
	key := fmt.Sprintf("%s\x00%s\x00%s", pkg.Path, helper.Name, deriveBoundKey(p.instance.Pkg, p.instance.Name, ""))
	for _, parameter := range helper.TypeParams {
		key += "\x00" + typeKey(child.env[parameter.Name].(Type)) + "\x00" + deriveObligationsKey(child.typeFacts[parameter.Name])
	}
	for i, parameter := range helper.Params {
		if value, known := p.eval(child.helperArgs[i]); known && metadataValue(value) {
			child.env[parameter.Name] = value
			child.origins[parameter.Name] = parameter.Pos
			child.checkMetadataType(parameter.Type, value, parameter.Pos)
			key += fmt.Sprintf("\x00%#v", value)
		} else {
			runtimeIndices[i] = len(runtimeParameters)
			runtimeParameters = append(runtimeParameters, parameter)
			runtimeArgs = append(runtimeArgs, p.expr(child.helperArgs[i]))
			child.names[parameter.Name] = true
		}
	}
	if p.c.deriveSpecializations == nil {
		p.c.deriveSpecializations = map[string]*Func{}
	}
	fn := p.c.deriveSpecializations[key]
	if receipt, hit := p.c.deriveSpecializationCosts[key]; hit {
		if !p.charge(call.Pos, receipt.work) {
			return &syntax.Block{Pos: call.Pos}
		}
		depth := p.budget.depth + receipt.depth
		if depth > 256 {
			p.error(call.Pos, "derive template expansion exceeds its compile-time depth limit")
			return &syntax.Block{Pos: call.Pos}
		}
		p.budget.observeDepth(depth)
	}
	if fn == nil {
		before, errors := p.budget.remaining, p.c.diags.Len()
		frame := &deriveBudgetFrame{start: p.budget.depth}
		p.budget.frames = append(p.budget.frames, frame)
		fd := &syntax.FuncDecl{Pos: helper.Pos, End: helper.End, Name: p.generatedName("helper", p.instance.Pkg), Uses: helper.Uses, Needs: helper.Needs, Requires: nil}
		fn = &Func{Decl: fd, Pkg: p.instance.Pkg, TemplatePkg: pkg, TemplateScope: p.instance, TypeParams: p.instance.TypeParams, Result: Ok}
		p.c.deriveSpecializations[key] = fn
		p.c.info.FuncOf[fd] = fn
		saved := p.c.pkg
		p.c.pkg = pkg
		fd.Params = child.clone(reflect.ValueOf(runtimeParameters)).Interface().([]*syntax.Param)
		for _, parameter := range fd.Params {
			fn.Params = append(fn.Params, p.c.resolveType(parameter.Type))
		}
		if helper.Result != nil {
			fd.Result = child.clone(reflect.ValueOf(helper.Result)).Interface().(*syntax.TypeExpr)
			fn.Result = p.c.resolveType(fd.Result)
		}
		fn.Effects = p.c.effectsOf(fd.Uses)
		p.c.openSignature(fn)
		p.c.needsOf(fn)
		if helper.Requires != nil {
			fd.Requires = child.expr(helper.Requires)
		}
		fd.Body = child.expr(helper.Body).(*syntax.Block)
		p.c.resolveFunctionConstraints(fn)
		p.c.checkFunctionRequirement(fn)
		fd.Body.Stmts = append(child.helperBoundChecks(helper), fd.Body.Stmts...)
		p.c.pkg = saved
		p.c.info.ExpandedFunctions = append(p.c.info.ExpandedFunctions, fn)
		p.failed = p.failed || child.failed
		p.budget.frames = p.budget.frames[:len(p.budget.frames)-1]
		if !child.failed && p.c.diags.Len() == errors && !frame.deferred {
			if p.c.deriveSpecializationCosts == nil {
				p.c.deriveSpecializationCosts = map[string]deriveSpecializationCost{}
			}
			p.c.deriveSpecializationCosts[key] = deriveSpecializationCost{work: before - p.budget.remaining, depth: frame.peak}
		} else {
			delete(p.c.deriveSpecializations, key)
		}
	}
	generated := &syntax.Call{Start: call.Start, Pos: call.Pos, End: call.End, Fun: &syntax.Ident{Pos: call.Start, Name: fn.Decl.Name}, Args: runtimeArgs}
	for _, parameter := range fn.TypeParams {
		written := &syntax.TypeExpr{Pos: call.Pos, Name: parameter.Name}
		p.c.info.assemblyTypes[written] = parameter
		generated.TypeArgs = append(generated.TypeArgs, written)
	}
	for _, sourceIndex := range child.helperOrder {
		if runtimeIndex, present := runtimeIndices[sourceIndex]; present {
			p.c.info.callOrder[generated] = append(p.c.info.callOrder[generated], runtimeIndex)
		}
	}
	p.registerCall(generated, fn)
	return generated
}

// A source helper's declared bounds remain ordinary dictionary obligations,
// even when its implementation happens not to call a class method. A checked
// empty generic function is a witness; Go removes its runtime call.
func (p *deriveExpansion) helperBoundChecks(helper *syntax.FuncDecl) []syntax.Stmt {
	var statements []syntax.Stmt
	for _, parameter := range helper.TypeParams {
		for _, name := range parameter.Bounds {
			class := p.c.lookupClass(name)
			if class == nil {
				p.error(parameter.Pos, "unknown class %s", name)
				continue
			}
			declared := &syntax.FuncDecl{Pos: parameter.Pos, Name: p.generatedName("bound", p.scope), Body: &syntax.Block{Pos: parameter.Pos}}
			typeParameter := &TypeParam{Name: "Bound", Bounds: []*Class{class}}
			witness := &Func{Decl: declared, Pkg: p.scope, TypeParams: []*TypeParam{typeParameter}, Result: Ok, Synthetic: true}
			p.c.info.FuncOf[declared] = witness
			p.c.info.ExpandedFunctions = append(p.c.info.ExpandedFunctions, witness)
			written := &syntax.TypeExpr{Pos: parameter.Pos, Name: parameter.Name}
			p.c.info.assemblyTypes[written] = p.env[parameter.Name].(Type)
			if len(p.typeFacts[parameter.Name]) != 0 {
				if p.c.info.shapeTypeFacts == nil {
					p.c.info.shapeTypeFacts = map[*syntax.TypeExpr][]*Constraint{}
				}
				p.c.info.shapeTypeFacts[written] = append([]*Constraint(nil), p.typeFacts[parameter.Name]...)
			}
			call := &syntax.Call{Pos: parameter.Pos, Start: parameter.Pos, Fun: &syntax.Ident{Pos: parameter.Pos, Name: declared.Name}, TypeArgs: []*syntax.TypeExpr{written}}
			p.registerCall(call, witness)
			statements = append(statements, &syntax.ExprStmt{X: call})
		}
	}
	return statements
}

// Compiler references carry an AST identity; source spelling never grants
// access to a specialized helper or overrides an ordinary source function.
func (p *deriveExpansion) registerCall(call *syntax.Call, fn *Func) {
	if p.c.deriveCalls == nil {
		p.c.deriveCalls = map[*syntax.Call]*Func{}
	}
	p.c.deriveCalls[call] = fn
}

func (p *deriveExpansion) generatedName(kind string, pkg *Package) string {
	for {
		p.c.deriveHelperSerial++
		name := fmt.Sprintf("_derive_%s_%d", kind, p.c.deriveHelperSerial)
		if pkg.Funcs[name] == nil && p.c.preludePkg.Funcs[name] == nil {
			return name
		}
	}
}
