package check

import (
	"reflect"

	"github.com/GiGurra/bork/internal/syntax"
)

// Capture closure is checked by declaration identity, including nested blocks.
type comptimeContext struct {
	parent   *comptimeContext
	external map[any]bool
	captures []any
	seen     map[any]bool
}

func (c *checker) comptimeInitializer(node *syntax.Comptime, want Type) Type {
	ctx := &comptimeContext{parent: c.comptimeContext, external: map[any]bool{}, seen: map[any]bool{}}
	for _, scope := range c.scopes {
		for _, local := range scope {
			ctx.external[local.decl] = true
		}
	}
	c.comptimeContext = ctx
	savedEffects := c.used
	c.used = 0
	binding := &syntax.Binding{Pos: node.Pos, Value: node.Body}
	t := c.valueInitializer(binding, want, "comptime block")
	effects := c.used
	c.used = savedEffects
	c.comptimeContext = ctx.parent
	c.info.comptimeSyntax = append(c.info.comptimeSyntax, node)
	if c.info.comptimeCaptureDecls == nil {
		c.info.comptimeCaptureDecls = map[*syntax.Comptime][]any{}
	}
	c.info.comptimeCaptureDecls[node] = ctx.captures
	if effects&^EffBuild != 0 {
		c.diags.AddCode(node.Pos, "comptime.effects", "comptime requires pure code, found uses %s", effects)
	}
	return t
}

func (c *checker) noteComptimeCapture(node *syntax.Ident, decl any, typ Type) {
	for ctx := c.comptimeContext; ctx != nil; ctx = ctx.parent {
		if ctx.external[decl] && (hasTypeParam(typ) || !c.closedComptimeDeclaration(decl, map[any]bool{})) {
			c.diags.AddCode(node.Pos, "comptime.capture", "comptime cannot capture runtime value %s", node.Name)
			return
		}
		if ctx.external[decl] && !ctx.seen[decl] {
			ctx.seen[decl] = true
			ctx.captures = append(ctx.captures, decl)
		}
	}
}

func (c *checker) closedComptimeDeclaration(decl any, seen map[any]bool) bool {
	if seen[decl] {
		return false
	}
	seen[decl] = true
	defer delete(seen, decl)
	binding, ok := decl.(*syntax.Binding)
	return ok && !binding.Lazy && binding.AsyncScope == nil && c.closedComptimeSyntax(binding.Value, seen)
}

func (c *checker) closedComptimeSyntax(x syntax.Expr, seen map[any]bool) bool {
	if c.info.constantOf(x) != nil {
		return true
	}
	switch x := x.(type) {
	case *syntax.Comptime:
		return true
	case *syntax.Selector:
		return c.info.selectorVariants[x] != nil || c.closedComptimeSyntax(x.X, seen)
	case *syntax.ContextName:
		return c.info.contextVariants[x] != nil
	case *syntax.Ident:
		return c.closedComptimeDeclaration(c.info.defs[x], seen)
	case *syntax.ListLit:
		for _, value := range x.Elems {
			if !c.closedComptimeSyntax(value, seen) {
				return false
			}
		}
		return true
	case *syntax.MapLit:
		for i, key := range x.Keys {
			if !c.closedComptimeSyntax(key, seen) || !c.closedComptimeSyntax(x.Values[i], seen) {
				return false
			}
		}
		return true
	case *syntax.RecordLit:
		// A closed recipe still creates runtime memo storage. Capturing that
		// record would carry its cells into the compile-time computation.
		var fields []*Field
		switch target := c.info.recordTargets[x].(type) {
		case *Record:
			fields = target.Fields
		case *Variant:
			fields = target.Fields
		}
		for _, field := range fields {
			if field.Lazy {
				return false
			}
		}
		// Include inserted defaults, not only fields written at the capture site.
		for _, field := range c.info.recordInits[x] {
			if !c.closedComptimeSyntax(field.Value, seen) {
				return false
			}
		}
		return true
	}
	return false
}

func (c *checker) checkComptimeTypes() {
	for _, node := range c.info.comptimeSyntax {
		c.checkComptimeRecipe(reflect.ValueOf(node.Body))
	}
	nodes := append([]*Comptime(nil), c.info.Comptimes...)
	checkedPackages := map[*PackageBinding]bool{}
	for _, batch := range c.info.InterpolationBatches {
		nodes = append(nodes, batch.Recipe)
	}
	for _, node := range nodes {
		var checkPackage func(*PackageBinding)
		checkPackage = func(binding *PackageBinding) {
			if checkedPackages[binding] {
				return
			}
			checkedPackages[binding] = true
			if path := unsupportedComptimeType(binding.Type, map[Type]bool{}); path != "" {
				c.diags.AddCode(node.Pos(), "comptime.result", "comptime cannot bake package value %s of type %s: %s", binding.Decl.Name, binding.Type, path)
			}
			for _, dependency := range binding.Dependencies {
				checkPackage(dependency)
			}
		}
		for _, binding := range ComptimePackageBindings(c.info, node) {
			checkPackage(binding)
		}
		if path := unsupportedComptimeType(node.Type(), map[Type]bool{}); path != "" {
			c.diags.AddCode(node.Pos(), "comptime.result", "comptime cannot bake result type %s: %s", node.Type(), path)
		}
	}
}

func unsupportedComptimeType(t Type, seen map[Type]bool) string {
	if hasTypeParam(t) {
		return "unresolved generic " + t.String()
	}
	if seen[t] {
		return ""
	}
	seen[t] = true
	switch t := t.(type) {
	case *Basic:
		if t == Bool || t == String || t == Rune || t == Ok || IsNumeric(t) {
			return ""
		}
	case *List:
		return unsupportedComptimeType(t.Elem, seen)
	case *Map:
		if path := unsupportedComptimeType(t.Key, seen); path != "" {
			return "Map key: " + path
		}
		return unsupportedComptimeType(t.Value, seen)
	case *Record:
		for _, field := range t.Fields {
			if field.Lazy {
				return "field " + field.Name + ": lazy cell"
			}
			if path := unsupportedComptimeType(field.Type, seen); path != "" {
				return "field " + field.Name + ": " + path
			}
		}
		return ""
	case *Sealed:
		for _, variant := range t.Variants {
			for _, field := range variant.Fields {
				if field.Lazy {
					return "variant " + variant.Name + " field " + field.Name + ": lazy cell"
				}
				if path := unsupportedComptimeType(field.Type, seen); path != "" {
					return "variant " + variant.Name + " field " + field.Name + ": " + path
				}
			}
		}
		return ""
	case *Union:
		for _, member := range t.Members {
			if path := unsupportedComptimeType(member, seen); path != "" {
				return path
			}
		}
		return ""
	}
	return "unsupported " + t.String()
}

// Inspect inferred, zonked expression/instance types as well as written types.
// A phantom type parameter need not appear in the result's fields or syntax.
func (c *checker) checkComptimeRecipe(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		if typ, ok := v.Interface().(*syntax.TypeExpr); ok {
			if resolved := c.info.writtenTypes[typ]; resolved != nil && hasTypeParam(resolved) {
				c.diags.AddCode(typ.Pos, "comptime.type", "comptime requires concrete types, found %s", resolved)
			}
		}
		if node, ok := v.Interface().(syntax.Expr); ok {
			if typ := c.info.types[node]; typ != nil && hasTypeParam(typ) {
				c.diags.AddCode(node.Position(), "comptime.type", "comptime requires concrete types, found %s", typ)
			}
			if call, ok := node.(*syntax.Call); ok {
				if instance := c.info.instances[call]; instance != nil {
					for _, typ := range instance.TypeArgs {
						if hasTypeParam(typ) {
							c.diags.AddCode(call.Position(), "comptime.type", "comptime requires concrete types, found type argument %s", typ)
						}
					}
				}
			}
		}
		c.checkComptimeRecipe(v.Elem())
	case reflect.Interface:
		if !v.IsNil() {
			c.checkComptimeRecipe(v.Elem())
		}
	case reflect.Struct:
		for _, i := range walkableSyntaxFields(v.Type()) {
			c.checkComptimeRecipe(v.Field(i))
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			c.checkComptimeRecipe(v.Index(i))
		}
	}
}
