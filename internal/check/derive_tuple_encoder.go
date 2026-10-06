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
// template, shared by arity and requesting scope; no tuple codec is emitted
// by the Go backend.
func (c *checker) sourceTupleCodec(dictionary *Dict, tuple *Record, at diag.Pos) *Dict {
	callerParams, callerFunction := c.typeParams, c.fn
	defer func() { c.typeParams, c.fn = callerParams, callerFunction }()
	scope := c.pkg
	if c.fn != nil && c.fn.TemplateScope != nil {
		scope = c.fn.TemplateScope.Pkg
	}
	class := dictionary.Class
	key := fmt.Sprintf("%s\x00%s\x00%d", scope.Path, classIdentity(class), len(tuple.Fields))
	if IsCodec(class, "Decode") {
		// Decoding validates actual slot obligations. An arity-only erased
		// target cannot certify tuples carrying field facts.
		key += "\x00" + typeKey(tuple) + "\x00" + deriveObligationsKey(tupleConstraints(tuple))
	}
	if c.info.deriveTupleCodecs == nil {
		c.info.deriveTupleCodecs = map[string]*ClassInstance{}
	}
	instance := c.info.deriveTupleCodecs[key]
	if instance == nil {
		template := class.Template
		plan := &deriveExpansion{c: c, template: template, scope: scope, budget: &deriveBudget{remaining: 100000}, env: map[string]any{}, names: map[string]bool{}, origins: map[string]diag.Pos{}, typeFacts: map[string][]*Constraint{}, active: map[*syntax.FuncDecl]bool{}}
		digest := sha256.Sum256([]byte(key))
		name := fmt.Sprintf("_derive_tuple_%d_%x", len(tuple.Fields), digest[:8])
		instance = &ClassInstance{Name: name, Pkg: scope, Class: class, Decl: &syntax.InstanceDecl{Pos: at}}
		var head *Record
		if IsCodec(class, "Decode") {
			head = tuple
			instance.Name = fmt.Sprintf("_derive_tuple_decode_%d_%d", len(tuple.Fields), len(c.info.deriveTupleCodecs))
			instance.Constraints = tupleConstraints(tuple)
			seen := map[*TypeParam]bool{}
			mentionsWhere(tuple, func(parameter *TypeParam) bool {
				if !seen[parameter] {
					seen[parameter] = true
					instance.TypeParams = append(instance.TypeParams, parameter)
				}
				return false
			})
		} else {
			var elements []Type
			for i := range tuple.Fields {
				parameter := &TypeParam{Name: fmt.Sprintf("Tuple%d", i), Bounds: []*Class{class}}
				instance.TypeParams = append(instance.TypeParams, parameter)
				elements = append(elements, parameter)
			}
			head = tupleType(elements)
		}
		instance.Type = head
		plan.target, plan.instance = head, instance
		plan.env[template.Decl.TypeParams[0].Name] = head
		c.info.deriveTupleCodecs[key] = instance
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
		c.declareInstanceMetadata(instance, template.Decl.Metadata, plan)
		c.pkg = saved
	}
	c.typeParams, c.fn = callerParams, callerFunction
	result := &Dict{Class: class, Type: tuple, Inst: instance}
	if IsCodec(class, "Decode") {
		for _, parameter := range instance.TypeParams {
			result.TypeArgs = append(result.TypeArgs, parameter)
			for _, bound := range parameter.Bounds {
				result.Args = append(result.Args, c.dict(bound, parameter, at, 0))
			}
		}
	} else {
		result.Args = dictionary.Args
		for _, field := range tuple.Fields {
			result.TypeArgs = append(result.TypeArgs, field.Type)
		}
	}
	return result
}

// Runtime obligations still require a local adapter so they retain the caller's
// values. Keep the existing closure decoder until source captures are lowered.
func tupleRuntimeCaptures(tuple *Record) bool {
	var captures func(*Constraint) bool
	captures = func(constraint *Constraint) bool {
		if constraint.PredParam != "" {
			return true
		}
		for _, argument := range constraint.Args {
			if argument.Const == nil && !argument.Sibling {
				return true
			}
		}
		for _, alternative := range constraint.Or {
			if captures(alternative) {
				return true
			}
		}
		return false
	}
	for _, constraint := range tupleConstraints(tuple) {
		if captures(constraint) {
			return true
		}
	}
	return false
}
