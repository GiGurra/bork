package check

import (
	"crypto/sha256"
	"fmt"
	"reflect"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// The implicit tuple dictionary retains the existing per-slot instance
// selection. Its implementation is an ordinary specialization of the library
// template, shared by arity and requesting scope; no tuple encoder is emitted
// by the Go backend.
func (c *checker) sourceTupleEncoder(dictionary *Dict, tuple *Record, at diag.Pos) *Dict {
	scope := c.pkg
	if c.fn != nil && c.fn.TemplateScope != nil {
		scope = c.fn.TemplateScope.Pkg
	}
	class := dictionary.Class
	key := fmt.Sprintf("%s\x00%s\x00%d", scope.Path, classIdentity(class), len(tuple.Fields))
	if c.info.deriveTupleEncoders == nil {
		c.info.deriveTupleEncoders = map[string]*ClassInstance{}
	}
	instance := c.info.deriveTupleEncoders[key]
	if instance == nil {
		template := class.Template
		plan := &deriveExpansion{c: c, template: template, scope: scope, budget: &deriveBudget{remaining: 100000}, env: map[string]any{}, names: map[string]bool{}, origins: map[string]diag.Pos{}, typeFacts: map[string][]*Constraint{}, active: map[*syntax.FuncDecl]bool{}}
		digest := sha256.Sum256([]byte(key))
		name := fmt.Sprintf("_derive_tuple_%d_%x", len(tuple.Fields), digest[:8])
		instance = &ClassInstance{Name: name, Pkg: scope, Class: class, Decl: &syntax.InstanceDecl{Pos: at}}
		var elements []Type
		for i := range tuple.Fields {
			parameter := &TypeParam{Name: fmt.Sprintf("Tuple%d", i), Bounds: []*Class{class}}
			instance.TypeParams = append(instance.TypeParams, parameter)
			elements = append(elements, parameter)
		}
		head := tupleType(elements)
		instance.Type = head
		plan.target, plan.instance = head, instance
		plan.env[template.Decl.TypeParams[0].Name] = head
		c.info.deriveTupleEncoders[key] = instance
		c.info.ClassInstances = append(c.info.ClassInstances, instance)
		saved := c.pkg
		c.pkg = template.Pkg
		for _, source := range template.Decl.Methods {
			method := class.Method(source.Name)
			decl := &syntax.FuncDecl{Pos: source.Pos, End: source.End, Name: source.Name, Instance: instance.Decl, Uses: source.Uses}
			decl.Params = plan.clone(reflect.ValueOf(source.Params)).Interface().([]*syntax.Param)
			if source.Result != nil {
				decl.Result = plan.clone(reflect.ValueOf(source.Result)).Interface().(*syntax.TypeExpr)
			}
			fn := &Func{Decl: decl, Pkg: scope, Of: instance, TemplatePkg: template.Pkg, TemplateScope: instance, TypeParams: instance.TypeParams, Effects: method.Effects, Result: subst(method.Result, map[*TypeParam]Type{class.Param: head})}
			for _, param := range method.Params {
				fn.Params = append(fn.Params, subst(param, map[*TypeParam]Type{class.Param: head}))
			}
			fn.ParamConstraints = make([][]*Constraint, len(fn.Params))
			for _, param := range decl.Params {
				plan.names[param.Name] = true
			}
			start := c.diags.Len()
			decl.Body = plan.expr(source.Body).(*syntax.Block)
			c.diags.DeriveContext(start, at)
			instance.Methods = append(instance.Methods, fn)
			c.info.FuncOf[decl] = fn
			c.info.ExpandedFunctions = append(c.info.ExpandedFunctions, fn)
		}
		c.pkg = saved
	}
	result := &Dict{Class: class, Type: tuple, Inst: instance, Args: dictionary.Args}
	for _, field := range tuple.Fields {
		result.TypeArgs = append(result.TypeArgs, field.Type)
	}
	return result
}
