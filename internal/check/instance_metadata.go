package check

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
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
		if !c.metadataKeyFits(key, instance.Type, source.Pos) {
			continue
		}
		c.info.registerMetadataKey(key)
		duplicate := false
		for _, previous := range instance.Metadata {
			if sameMetadataKey(previous.Result, key) {
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
	if !c.metadataKeyFits(key, target, call.TypeArgs[2].Pos) {
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
			if sameMetadataKey(subst(initializer.Result, bindParams(dictionary.Inst.TypeParams, dictionary.TypeArgs)), key) {
				c.fn.Calls = append(c.fn.Calls, initializer)
			}
		}
	}
	return instantiate(c.preludePkg.TypeNamed("Option"), []Type{key}), true
}

// metadataKeyFits reports whether key can identify metadata of target's
// selected dictionary: a closed type, or a family applied to the target.
func (c *checker) metadataKeyFits(key, target Type, pos diag.Pos) bool {
	if family, argument, ok := metadataFamily(key); ok {
		if argument == nil || !identical(argument, target) {
			c.errorf(pos, "metadata family %s is indexed by the target: write %s[%s], found %s", family.Name, family.Name, target, key)
			return false
		}
		return true
	}
	if !closedMetadataKey(key) {
		c.errorf(pos, "metadata requires a closed resolved type key, found %s", key)
		return false
	}
	return true
}

// metadataFamily finds the family a key applies. A `metadata type K[T]`
// family is one key for every target: the selected dictionary for X holds
// K[X], so a generic query K[U] and a concrete one find the same entry.
func metadataFamily(key Type) (family *syntax.TypeDecl, argument Type, ok bool) {
	switch key := key.(type) {
	case *Record:
		family = key.Decl
	case *Sealed:
		family = key.Decl
	}
	if family == nil || !family.MetadataFamily {
		return nil, nil, false
	}
	if arguments := TypeArgs(key); len(arguments) == 1 {
		argument = arguments[0]
	}
	return family, argument, true
}

// sameMetadataKey compares keys by family, or else by checked type identity.
func sameMetadataKey(left, right Type) bool {
	leftFamily, _, leftOk := metadataFamily(left)
	rightFamily, _, rightOk := metadataFamily(right)
	if leftOk || rightOk {
		return leftFamily == rightFamily
	}
	return identical(left, right)
}

// checkMetadataFamily accepts a record or sealed family whose parameter is
// only a whole callback parameter type. A constrained target (Int where
// small) can select its base type's instance, so a family value may consume
// target values but must not produce them.
func (c *checker) checkMetadataFamily(td *syntax.TypeDecl, typ Type) {
	params := typeParamsOf(typ)
	if td.Kind != syntax.RecordType && td.Kind != syntax.SealedType || len(params) != 1 {
		c.errorf(td.Pos, "metadata family %s must be a record or sealed type with one type parameter", td.Name)
		td.MetadataFamily = false // Its uses are then ordinary keys.
		return
	}
	var fields []*Field
	switch typ := typ.(type) {
	case *Record:
		fields = typ.Fields
	case *Sealed:
		for _, variant := range typ.Variants {
			fields = append(fields, variant.Fields...)
		}
	}
	for _, field := range fields {
		if !consumesOnly(field.Type, params[0]) {
			c.errorf(field.Decl.Type.Pos, "metadata family %s may use %s only as a callback parameter type, found %s", td.Name, params[0].Name, field.Type)
			td.MetadataFamily = false
		}
	}
}

func consumesOnly(typ Type, param *TypeParam) bool {
	switch typ := typ.(type) {
	case *FuncType:
		for _, parameter := range typ.Params {
			if parameter != Type(param) && mentionsParam(parameter, param) {
				return false
			}
		}
		return consumesOnly(typ.Result, param)
	case *List:
		return consumesOnly(typ.Elem, param)
	case *Seq:
		return consumesOnly(typ.Elem, param)
	case *Map:
		return !mentionsParam(typ.Key, param) && consumesOnly(typ.Value, param)
	case *Union:
		for _, member := range typ.Members {
			if !consumesOnly(member, param) {
				return false
			}
		}
		return true
	case *Record:
		if typ.Tuple {
			for _, field := range typ.Fields {
				if !consumesOnly(field.Type, param) {
					return false
				}
			}
			return true
		}
	}
	return !mentionsParam(typ, param)
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
		if sameMetadataKey(existing, typ) {
			return
		}
	}
	info.metadataKeyTypes = append(info.metadataKeyTypes, typ)
}

// MetadataKey uses checked semantic type identity, including callback effects
// and structural tuple equivalence, and one key per metadata family. Keys are deterministic within this program;
// runtime Go reflection and erased Go function types do not define identity.
func (info *Info) MetadataKey(typ Type) string {
	for i, existing := range info.metadataKeyTypes {
		if sameMetadataKey(existing, typ) {
			return fmt.Sprintf("metadata%d", i)
		}
	}
	panic("metadata type key was not checked")
}
