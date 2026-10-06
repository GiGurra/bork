package check

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// Initializers are ordinary checked functions. Their type keys carry no value
// proofs, and querying a dictionary does not inspect a target representation.
func (c *checker) declareInstanceMetadata(instance *ClassInstance, declarations []*syntax.InstanceMetadata, expansion *deriveExpansion) {
	for i, source := range declarations {
		written, value := source.Type, source.Value
		if expansion != nil {
			written = expansion.clone(reflect.ValueOf(written)).Interface().(*syntax.TypeExpr)
			value = expansion.expr(value)
		}
		key := c.resolveType(written)
		if key == Invalid {
			continue
		}
		if !closedMetadataKey(key) {
			c.errorf(source.Pos, "metadata requires a closed resolved type key, found %s", key)
			continue
		}
		c.info.registerMetadataKey(key)
		duplicate := false
		for _, previous := range instance.Metadata {
			if identical(previous.Result, key) {
				c.errorf(source.Pos, "metadata for %s is declared twice in instance %s", key, instance.Name)
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		decl := &syntax.FuncDecl{Pos: source.Pos, Name: fmt.Sprintf("_metadata_%s_%d", instance.Name, i), TypeParams: instance.Decl.TypeParams, Result: written, Body: &syntax.Block{Pos: source.Pos, Tail: value}}
		fn := &Func{Decl: decl, Pkg: instance.Pkg, TemplatePkg: instance.Pkg, Prelude: instance.Prelude, TypeParams: instance.TypeParams, Result: key}
		if expansion != nil {
			fn.TemplatePkg, fn.TemplateScope = expansion.template.Pkg, instance
		}
		instance.Metadata = append(instance.Metadata, fn)
		c.info.FuncOf[decl] = fn
		c.info.ExpandedFunctions = append(c.info.ExpandedFunctions, fn)
	}
}

func (c *checker) shapeMetadataCall(call *syntax.Call) (Type, bool) {
	id, ok := call.Fun.(*syntax.Ident)
	if !ok {
		return nil, false
	}
	alias, member, qualified := strings.Cut(id.Name, ".")
	pkg := c.pkg.imports[alias]
	if !qualified || member != "metadata" || pkg == nil || pkg.Path != "bork/shape" {
		return nil, false
	}
	c.pkg.used[alias] = true
	if len(call.TypeArgs) != 3 || len(call.Args) != 0 {
		c.errorf(call.Pos, "shape.metadata takes target, class and metadata type arguments and no value arguments")
		return Invalid, true
	}
	target := c.resolveType(call.TypeArgs[0])
	class := c.lookupClass(call.TypeArgs[1].Name)
	if class == nil || len(call.TypeArgs[1].Args) != 0 || len(call.TypeArgs[1].Where) != 0 {
		c.errorf(call.TypeArgs[1].Pos, "shape.metadata requires a class as its second type argument")
		return Invalid, true
	}
	key := c.resolveType(call.TypeArgs[2])
	if target == Invalid || key == Invalid {
		return Invalid, true
	}
	if !closedMetadataKey(key) {
		c.errorf(call.TypeArgs[2].Pos, "metadata requires a closed resolved type key, found %s", key)
		return Invalid, true
	}
	c.info.registerMetadataKey(key)
	saved := c.have
	c.have = c.constraintsOf(call.TypeArgs[0], target, c.paramScope())
	c.have = append(c.have, c.info.shapeRawHeads[call.TypeArgs[0]]...)
	dictionary := c.dict(class, target, call.Pos, 0)
	c.have = saved
	if dictionary == nil {
		return Invalid, true
	}
	if c.info.shapeMetadataCalls == nil {
		c.info.shapeMetadataCalls = map[*syntax.Call]*Dict{}
	}
	c.info.shapeMetadataCalls[call] = dictionary
	if c.fn != nil && dictionary.Inst != nil {
		for _, initializer := range dictionary.Inst.Metadata {
			if identical(subst(initializer.Result, bindParams(dictionary.Inst.TypeParams, dictionary.TypeArgs)), key) {
				c.fn.Calls = append(c.fn.Calls, initializer)
			}
		}
	}
	return instantiate(c.preludePkg.TypeNamed("Option"), []Type{key}), true
}

func closedMetadataKey(typ Type) bool {
	if hasTypeParam(typ) {
		return false
	}
	switch typ := typ.(type) {
	case *Record:
		for _, argument := range TypeArgs(typ) {
			if !closedMetadataKey(argument) {
				return false
			}
		}
		if typ.Tuple {
			for _, field := range typ.Fields {
				if !closedMetadataKey(field.Type) {
					return false
				}
			}
		}
	case *Seq:
		return typ.Effects != EffOpen && closedMetadataKey(typ.Elem)
	case *List:
		return closedMetadataKey(typ.Elem)
	case *Map:
		return closedMetadataKey(typ.Key) && closedMetadataKey(typ.Value)
	case *FuncType:
		if typ.Effects == EffOpen || !closedMetadataKey(typ.Result) {
			return false
		}
		for _, parameter := range typ.Params {
			if !closedMetadataKey(parameter) {
				return false
			}
		}
	case *Union:
		for _, member := range typ.Members {
			if !closedMetadataKey(member) {
				return false
			}
		}
	case *Sealed:
		for _, argument := range TypeArgs(typ) {
			if !closedMetadataKey(argument) {
				return false
			}
		}
	}
	return true
}

func (info *Info) registerMetadataKey(typ Type) {
	for _, existing := range info.metadataKeyTypes {
		if identical(existing, typ) {
			return
		}
	}
	info.metadataKeyTypes = append(info.metadataKeyTypes, typ)
}

// MetadataKey uses checked semantic type identity, including callback effects
// and structural tuple equivalence. Keys are deterministic within this program;
// runtime Go reflection and erased Go function types do not define identity.
func (info *Info) MetadataKey(typ Type) string {
	for i, existing := range info.metadataKeyTypes {
		if identical(existing, typ) {
			return fmt.Sprintf("metadata%d", i)
		}
	}
	panic("metadata type key was not checked")
}
