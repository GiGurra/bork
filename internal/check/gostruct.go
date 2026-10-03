package check

import (
	"go/token"
	"go/types"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

func IsGoStruct(cl *Class) bool { return cl != nil && cl.Prelude && cl.Name == "GoStruct" }

// Prepare generated named types before resolving fields, so nested and
// recursive records refer to the same Go type regardless of declaration order.
func (c *checker) resolveGoStructs(files []*syntax.File) {
	for _, f := range files {
		c.inFile(f)
		for _, td := range f.Types {
			e := c.pkg.types[td.Name]
			if e == nil || e.decl != td {
				continue
			}
			r, ok := e.typ.(*Record)
			for _, name := range td.Derive {
				if !IsGoStruct(c.lookupClass(name)) {
					continue
				}
				if !ok {
					c.errorf(td.DerivePos, "GoStruct can only be derived for a record")
					continue
				}
				if r.Decl != nil && r.Decl.Private && r.Pkg != c.pkg {
					c.errorf(td.DerivePos, "cannot derive GoStruct for %s: package %s controls construction of %s; use an instance provided by that package", td.Name, r.Pkg.Path, r.Name)
					continue
				}
				r.GoStruct = true
				if td.GoName == nil {
					r.GoGenerated = true
					r.GoMirror = types.NewNamed(types.NewTypeName(token.NoPos, nil, "_go_"+r.Pkg.GoPrefix+r.Name, nil), nil, nil)
				}
			}
			var groups [][]*syntax.FieldDecl
			if td.Kind == syntax.RecordType {
				groups = [][]*syntax.FieldDecl{td.Fields}
			}
			for _, v := range td.Variants {
				groups = append(groups, v.Fields)
			}
			for _, fields := range groups {
				for _, field := range fields {
					if len(field.GoTags) > 0 && (!ok || !r.GoGenerated) {
						c.errorf(field.GoTags[0].Pos, "Go struct tags require a generated record with derive (GoStruct); mirrors cannot add tags")
					}
					seen := map[string]bool{}
					for _, tag := range field.GoTags {
						if seen[tag.Name] {
							c.errorf(tag.Pos, "Go struct tag %s is repeated", tag.Name)
						}
						seen[tag.Name] = true
						if strings.ContainsAny(tag.Name, " \t\n\r\"\\:") {
							c.errorf(tag.Pos, "invalid Go struct tag name %s", tag.Name)
						}
					}
				}
			}
		}
	}
	for _, t := range c.info.TypeOrder {
		r, ok := t.(*Record)
		if !ok || !r.GoGenerated {
			continue
		}
		var fields []*types.Var
		var tags []string
		seen := map[string]bool{}
		for _, f := range r.Fields {
			gt := c.generatedGoType(f.Type)
			if gt == nil || containsResource(f.Type, map[Type]bool{}) {
				why := ""
				if hasTypeParam(f.Type) {
					why = "; use a concrete record for fields depending on type parameters"
				}
				c.errorf(f.Decl.Pos, "cannot derive GoStruct for %s: field %s has type %s, which cannot convert to a Go struct field%s", r.Name, f.Name, f.Type, why)
				r.GoStruct = false
				gt = types.Typ[types.Invalid]
			}
			ch, n := utf8.DecodeRuneInString(f.Name)
			name := string(unicode.ToUpper(ch)) + f.Name[n:]
			if !unicode.IsUpper([]rune(name)[0]) {
				c.errorf(f.Decl.Pos, "Go field %s is not exported", name)
			}
			if seen[name] {
				c.errorf(f.Decl.Pos, "more than one field maps to Go field %s", name)
				name = "_invalid" + strconv.Itoa(len(fields))
			}
			seen[name] = true
			fields = append(fields, types.NewField(token.NoPos, nil, name, gt, false))
			var parts []string
			for _, tag := range f.GoTags {
				parts = append(parts, tag.Name+":"+strconv.Quote(tag.Value))
			}
			tags = append(tags, strings.Join(parts, " "))
			r.GoFields = append(r.GoFields, GoField{Path: []string{name}, Type: gt})
		}
		r.GoMirror.(*types.Named).SetUnderlying(types.NewStruct(fields, tags))
	}
	for _, t := range c.info.TypeOrder {
		if r, ok := t.(*Record); ok && r.GoGenerated {
			for _, t := range r.insts.byKey {
				inst := t.(*Record)
				inst.GoStruct, inst.GoGenerated, inst.GoMirror, inst.GoFields = r.GoStruct, r.GoGenerated, r.GoMirror, r.GoFields
			}
		}
	}
}

func (c *checker) generatedGoType(t Type) types.Type {
	if g := GoTypeOf(t); g != nil {
		return g
	}
	if IsOption(t) {
		elem := TypeArgs(t)[0]
		g := c.generatedGoType(elem)
		if g == nil {
			return nil
		}
		if GoTypeOf(elem) != nil && isGoNillable(g) {
			return g
		}
		return types.NewPointer(g)
	}
	switch t := t.(type) {
	case *Basic:
		switch t.name {
		case "Bool":
			return types.Typ[types.Bool]
		case "String":
			return types.Typ[types.String]
		case "Bytes":
			return types.NewSlice(types.Typ[types.Uint8])
		case "Int":
			return types.Typ[types.Int64]
		case "Float":
			return types.Typ[types.Float64]
		default:
			for _, k := range []types.BasicKind{types.Int8, types.Int16, types.Int32, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Float32} {
				if strings.EqualFold(types.Typ[k].Name(), t.name) {
					return types.Typ[k]
				}
			}
		}
	case *List:
		if elem := c.generatedGoType(t.Elem); elem != nil {
			return types.NewSlice(elem)
		}
	case *Map:
		k, v := c.generatedGoType(t.Key), c.generatedGoType(t.Value)
		if isKeyType(t.Key) && k != nil && v != nil {
			return types.NewMap(k, v)
		}
	case *Record:
		if t.Base != nil {
			t = t.Base
		}
		if t.GoMirror != nil {
			return t.GoMirror
		}
		if t.Decl.GoName != nil {
			return c.namedGoType(t.Decl.GoName.Name, t.Decl.GoName)
		}
	}
	return nil
}

// Decoders are optional: opaque Go fields cannot have one.
func (c *checker) resolveGoStructDecoders(ci *ClassInstance) {
	r, ok := ci.Type.(*Record)
	if !ok {
		return
	}
	c.pkg, c.inPrelude = ci.Pkg, ci.Prelude
	c.typeParams = map[string]*TypeParam{}
	for _, p := range ci.TypeParams {
		c.typeParams[p.Name] = p
	}
	for _, f := range r.Fields {
		saved := c.diags
		c.diags = &diag.List{}
		c.have = nil
		for _, con := range f.Constraints {
			if con.Path == "" {
				c.have = append(c.have, con)
			}
		}
		d := c.dict(c.preludePkg.classes["Decode"], f.Type, ci.Decl.Pos, 0)
		c.diags = saved
		c.have = nil
		ci.GoFieldDecoders = append(ci.GoFieldDecoders, d)
	}
}
