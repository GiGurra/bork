package check

import (
	"reflect"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Definition references retain lexical identity even before any target requests
// expansion. The earlier spelling sweep distinguishes global names; this pass
// prevents local spellings in another branch from hiding an out-of-scope use.
func (c *checker) checkDeriveScopes(method *syntax.FuncDecl, localNames, typeNames map[string]bool) {
	type scope map[string]diag.Pos
	copyScope := func(outer scope) scope {
		inner := make(scope, len(outer))
		for name, pos := range outer {
			inner[name] = pos
		}
		return inner
	}
	bind := func(env scope, name string, pos diag.Pos) {
		if name == "" || name == "_" {
			return
		}
		_, builtin := builtins[name]
		if _, present := env[name]; present || typeNames[name] || builtin || c.pkg.Funcs[name] != nil || c.pkg.deriveHelpers[name] != nil || c.pkg.imports[name] != nil || c.pkg.bindings[name] != nil || c.pkg.ambients[name] != nil || c.pkg.providers[name] != nil || c.isTypeName(name) {
			c.errorf(pos, "%s is already defined in an enclosing derive scope", name)
		}
		env[name] = pos
	}
	var walk func(reflect.Value, scope)
	var pattern func(syntax.Pattern, scope)
	pattern = func(pat syntax.Pattern, env scope) {
		switch pat := pat.(type) {
		case *syntax.TypePat:
			walk(reflect.ValueOf(pat.Type), env)
			bind(env, pat.Name, pat.Pos)
		case *syntax.ListPat:
			for _, elem := range pat.Elems {
				pattern(elem, env)
			}
			bind(env, pat.Rest, pat.RestPos)
		case *syntax.VariantPat:
			if len(pat.Path) == 1 && !pat.Context && !pat.Braces && len(pat.Fields) == 0 && !c.isTypeName(pat.Path[0]) && !typeNames[pat.Path[0]] {
				bind(env, pat.Path[0], pat.Pos)
			}
			for _, field := range pat.Fields {
				if field.Pattern == nil {
					bind(env, field.Field, field.Pos)
				} else {
					pattern(field.Pattern, env)
				}
			}
		case *syntax.LitPat:
			walk(reflect.ValueOf(pat.Value), env)
		}
	}
	walk = func(v reflect.Value, env scope) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem(), env)
			}
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			switch node := v.Interface().(type) {
			case *syntax.Block:
				inner := copyScope(env)
				for _, stmt := range node.Stmts {
					walk(reflect.ValueOf(stmt), inner)
				}
				walk(reflect.ValueOf(node.Tail), inner)
				return
			case *syntax.Binding:
				walk(reflect.ValueOf(node.Type), env)
				walk(reflect.ValueOf(node.Value), env)
				walk(reflect.ValueOf(node.AsyncScope), env)
				bind(env, node.Name, node.Pos)
				return
			case *syntax.For:
				walk(reflect.ValueOf(node.Items), env)
				inner := copyScope(env)
				bind(inner, node.Name, node.NamePos)
				walk(reflect.ValueOf(node.Body), inner)
				return
			case *syntax.Lambda:
				inner := copyScope(env)
				for _, param := range node.Params {
					walk(reflect.ValueOf(param.Type), env)
					walk(reflect.ValueOf(param.Default), env)
					bind(inner, param.Name, param.Pos)
				}
				walk(reflect.ValueOf(node.Body), inner)
				return
			case *syntax.Match:
				walk(reflect.ValueOf(node.X), env)
				for _, arm := range node.Arms {
					inner := copyScope(env)
					pattern(arm.Pattern, inner)
					walk(reflect.ValueOf(arm.Body), inner)
				}
				return
			case *syntax.ScopeExpr:
				for _, policy := range node.Policies {
					walk(reflect.ValueOf(policy), env)
				}
				inner := copyScope(env)
				bind(inner, node.Name, node.Pos)
				walk(reflect.ValueOf(node.Body), inner)
				return
			case *syntax.Ident:
				if origin, present := env[node.Name]; present {
					c.noteDeriveSource(node.Pos, node.Name, origin, "variable")
				} else if localNames[node.Name] {
					c.errorf(node.Pos, "undefined local in derive definition: %s", node.Name)
				}
			case *syntax.TypeExpr:
				if owner, member, projected := strings.Cut(node.Name, "."); projected && member == "Type" && localNames[owner] {
					if origin, present := env[owner]; present {
						c.noteDeriveSource(node.Pos, owner, origin, "variable")
					} else {
						c.errorf(node.Pos, "undefined local in derive definition: %s", owner)
					}
				}
			}
			walk(v.Elem(), env)
		case reflect.Struct:
			for _, index := range walkableSyntaxFields(v.Type()) {
				walk(v.Field(index), env)
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), env)
			}
		}
	}
	env := scope{}
	for _, param := range method.Params {
		bind(env, param.Name, param.Pos)
	}
	walk(reflect.ValueOf(method.Requires), env)
	walk(reflect.ValueOf(method.Body), env)
}
