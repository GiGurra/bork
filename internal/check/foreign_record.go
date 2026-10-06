package check

import (
	"go/token"
	"go/types"
	"reflect"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// ForeignLayout is a record's Go struct layout, requested by deriving a class
// whose derive template declares shape.ForeignRecord metadata. The template's
// metadata chooses the stored fields, their order, Go names, tags and Option
// policy; the compiler validates it and generates the conversion bridge.
type ForeignLayout struct {
	Class  *Class
	Pos    diag.Pos // the derive request
	Pkg    *Package // the requesting package
	Failed bool
	// Slots lists field indices in Go struct order.
	Slots []int
	// mirror holds a mirror's layout until its Go fields are matched.
	mirror []foreignSlot
}

func isForeignRecordKey(t Type) bool {
	r, ok := t.(*Record)
	return ok && r.Pkg != nil && r.Pkg.Path == "bork/shape" && r.Name == "ForeignRecord"
}

// foreignRecordMetadata finds a template's shape.ForeignRecord declaration,
// which grants the class its foreign record capability.
func (c *checker) foreignRecordMetadata(class *Class) *syntax.InstanceMetadata {
	decl := class.Template.Decl
	plan := &deriveExpansion{c: c, template: class.Template, env: map[string]any{decl.TypeParams[0].Name: class.Param}, budget: &deriveBudget{remaining: 100000}, names: map[string]bool{}}
	saved := c.diags
	c.diags = &diag.List{}
	defer func() { c.diags = saved }()
	for _, source := range decl.Metadata {
		written := plan.clone(reflect.ValueOf(source.Type)).Interface().(*syntax.TypeExpr)
		if isForeignRecordKey(c.resolveType(written)) {
			return source
		}
	}
	return nil
}

// Prepare generated named types before resolving fields, so nested and
// recursive records refer to the same Go type regardless of declaration order.
func (c *checker) resolveForeignRecords(files []*syntax.File) {
	for _, request := range c.derives {
		cl := request.class
		if cl.ForeignRecord == nil {
			continue
		}
		c.pkg, c.inPrelude = request.pkg, request.prelude
		r, ok := request.typ.(*Record)
		switch {
		case !ok:
			request.invalid = true
			c.errorf(request.pos, "%s can only be derived for a record", cl.Name)
			continue
		case r.Decl != nil && r.Decl.Private && r.Pkg != c.pkg:
			request.invalid = true
			c.errorf(request.pos, "cannot derive %s for %s: package %s controls construction of %s; use an instance provided by that package", cl.Name, request.name, r.Pkg.Path, r.Name)
			continue
		case r.Base != nil:
			request.invalid = true
			c.errorf(request.pos, "cannot derive %s for a specialization; derive it for the record declaration", cl.Name)
			continue
		case r.Foreign != nil:
			request.invalid = true
			c.errorf(request.pos, "cannot derive %s for %s: it already has a Go struct layout from %s", cl.Name, r.Name, r.Foreign.Class.Name)
			continue
		}
		r.Foreign = &ForeignLayout{Class: cl, Pos: request.pos, Pkg: request.pkg}
		if r.Decl.GoName == nil {
			r.GoGenerated = true
			r.GoMirror = types.NewNamed(types.NewTypeName(token.NoPos, nil, "_go_"+r.Pkg.GoPrefix+r.Name, nil), nil, nil)
		}
	}
	for _, f := range files {
		c.inFile(f)
		for _, td := range f.Types {
			e := c.pkg.types[td.Name]
			if e == nil || e.decl != td {
				continue
			}
			r, ok := e.typ.(*Record)
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
						if !validTagName(tag.Name) {
							c.errorf(tag.Pos, "invalid Go struct tag name %s", tag.Name)
						}
					}
				}
			}
		}
	}
}

func validTagName(name string) bool {
	return name != "" && !strings.ContainsAny(name, " \t\n\r\"\\:")
}

// foreignSlot is one validated entry of a template's layout.
type foreignSlot struct {
	field   int
	name    string
	tags    []syntax.GoTag
	pointer bool
}

// Field shapes follow computed-default classification.
func (c *checker) finishForeignRecords() {
	for _, t := range c.info.TypeOrder {
		r, ok := t.(*Record)
		if !ok || r.Foreign == nil || r.Base != nil {
			continue
		}
		slots := c.foreignLayout(r)
		if slots == nil {
			r.Foreign.Failed = true
			if r.GoGenerated {
				r.GoMirror.(*types.Named).SetUnderlying(types.NewStruct(nil, nil))
			}
			continue
		}
		for _, slot := range slots {
			r.Foreign.Slots = append(r.Foreign.Slots, slot.field)
		}
		if r.GoGenerated {
			c.generatedLayout(r, slots)
		} else {
			r.Foreign.Slots = nil
			r.Foreign.mirror = slots
		}
	}
	for _, t := range c.info.TypeOrder {
		if r, ok := t.(*Record); ok && r.GoGenerated {
			for _, t := range r.insts.byKey {
				inst := t.(*Record)
				inst.Foreign, inst.GoGenerated, inst.GoMirror, inst.GoFields = r.Foreign, r.GoGenerated, r.GoMirror, r.GoFields
			}
		}
	}
}

// foreignLayout evaluates the class template's ForeignRecord metadata for r
// and validates it against r's stored fields. It returns nil after an error.
func (c *checker) foreignLayout(r *Record) []foreignSlot {
	layout := r.Foreign
	cl := layout.Class
	source := cl.ForeignRecord
	fail := func(format string, args ...any) []foreignSlot {
		c.errorf(layout.Pos, "cannot derive %s for %s: "+format, append([]any{cl.Name, r.Name}, args...)...)
		return nil
	}
	start := c.diags.Len()
	saved := c.pkg
	c.pkg = cl.Template.Pkg
	plan := &deriveExpansion{c: c, template: cl.Template, target: r, layout: true, scope: layout.Pkg, active: map[*syntax.FuncDecl]bool{}, env: map[string]any{cl.Template.Decl.TypeParams[0].Name: r}, budget: &deriveBudget{remaining: 100000}, names: map[string]bool{}}
	value, known := plan.eval(source.Value)
	c.pkg = saved
	c.diags.DeriveContext(start, layout.Pos)
	if plan.failed {
		return nil
	}
	record, ok := value.(metadataRecord)
	if !known || !ok || record.typ.Name != "ForeignRecord" {
		return fail("its template's ForeignRecord metadata at %s must be a compile-time shape.ForeignRecord literal (layout records and lists, descriptor properties, literals and String case operations)", source.Pos)
	}
	var slots []foreignSlot
	placed := map[int]bool{}
	names := map[string]bool{}
	for _, entry := range record.fields["fields"].(metadataList).items {
		entry := entry.(metadataRecord)
		index := int(entry.fields["slot"].(int64))
		if index < 0 || index >= len(r.Fields) || r.Fields[index].Computed {
			return fail("its ForeignRecord layout places slot %d, which is not a stored field", index)
		}
		f := r.Fields[index]
		if placed[index] {
			return fail("its ForeignRecord layout places field %s twice", f.Name)
		}
		placed[index] = true
		slot := foreignSlot{field: index, name: entry.fields["name"].(string), pointer: entry.fields["option"].(metadataVariant).name == "Pointer"}
		for _, tag := range entry.fields["tags"].(metadataList).items {
			tag := tag.(metadataRecord)
			slot.tags = append(slot.tags, syntax.GoTag{Pos: f.Decl.Pos, Name: tag.fields["name"].(string), Value: tag.fields["value"].(string)})
		}
		switch {
		case !token.IsIdentifier(slot.name):
			c.errorf(f.Decl.Pos, "Go field name %q for %s is not an identifier", slot.name, f.Name)
		case !token.IsExported(slot.name):
			c.errorf(f.Decl.Pos, "Go field %s is not exported", slot.name)
		case names[slot.name]:
			c.errorf(f.Decl.Pos, "more than one field maps to Go field %s", slot.name)
		}
		names[slot.name] = true
		if !sameTags(slot.tags, f.GoTags) {
			// Declared tags were checked with their own positions.
			seen := map[string]bool{}
			for _, tag := range slot.tags {
				if seen[tag.Name] {
					c.errorf(f.Decl.Pos, "Go struct tag %s is repeated", tag.Name)
				} else if !validTagName(tag.Name) {
					c.errorf(f.Decl.Pos, "invalid Go struct tag name %s", tag.Name)
				}
				seen[tag.Name] = true
			}
		}
		if !slot.pointer && IsOption(f.Type) {
			c.errorf(f.Decl.Pos, "cannot derive %s for %s: field %s is an Option, which its ForeignRecord layout rejects", cl.Name, r.Name, f.Name)
		}
		slots = append(slots, slot)
	}
	for i, f := range r.Fields {
		if !f.Computed && !placed[i] {
			return fail("its ForeignRecord layout omits stored field %s", f.Name)
		}
	}
	if c.diags.Len() != start {
		return nil
	}
	return slots
}

func sameTags(a, b []syntax.GoTag) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Value != b[i].Value {
			return false
		}
	}
	return true
}

// generatedLayout completes the reserved Go struct in the layout's order.
func (c *checker) generatedLayout(r *Record, slots []foreignSlot) {
	c.pkg, c.inPrelude = r.Pkg, r.Prelude
	r.GoFields = make([]GoField, len(r.Fields))
	var fields []*types.Var
	var tags []string
	for _, slot := range slots {
		f := r.Fields[slot.field]
		gt := c.generatedGoType(f.Type)
		if gt == nil || containsResource(f.Type, map[Type]bool{}) {
			why := ""
			if hasTypeParam(f.Type) {
				why = "; use a concrete record for fields depending on type parameters"
			}
			c.errorf(f.Decl.Pos, "cannot derive %s for %s: field %s has type %s, which cannot convert to a Go struct field%s", r.Foreign.Class.Name, r.Name, f.Name, TypeText(f.Type, nil), why)
			r.Foreign.Failed = true
			gt = types.Typ[types.Invalid]
		}
		fields = append(fields, types.NewField(token.NoPos, nil, slot.name, gt, false))
		var parts []string
		for _, tag := range slot.tags {
			parts = append(parts, tag.Name+":"+strconv.Quote(tag.Value))
		}
		tags = append(tags, strings.Join(parts, " "))
		r.GoFields[slot.field] = GoField{Path: []string{slot.name}, Type: gt, Tag: tags[len(tags)-1]}
	}
	r.GoMirror.(*types.Named).SetUnderlying(types.NewStruct(fields, tags))
}

// checkMirrorLayout keeps a mirror's declared Go layout: the template must
// select its stored fields under their mirrored names and add no tags.
func (c *checker) checkMirrorLayout(r *Record) {
	if r.Foreign == nil || r.Foreign.Failed || r.GoGenerated {
		return
	}
	slots := r.Foreign.mirror
	for _, slot := range slots {
		f := r.Fields[slot.field]
		mirrored := strings.Join(r.GoFields[slot.field].Path, ".")
		if !strings.EqualFold(slot.name, mirrored) {
			c.errorf(f.Decl.Pos, "cannot derive %s for %s: the mirror keeps its Go layout, which maps field %s to %s, not %s", r.Foreign.Class.Name, r.Name, f.Name, mirrored, slot.name)
		}
		// Declared tags on a mirror are already rejected where they appear.
		if len(slot.tags) > 0 && !sameTags(slot.tags, f.GoTags) {
			c.errorf(f.Decl.Pos, "cannot derive %s for %s: the mirror keeps its Go struct tags, so the layout cannot add tags to field %s", r.Foreign.Class.Name, r.Name, f.Name)
		}
		r.Foreign.Slots = append(r.Foreign.Slots, slot.field)
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
		case "Rune":
			return types.Typ[types.Int32]
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
		if t.Tuple {
			return nil
		}
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

// Decoders are optional: opaque Go fields cannot have one. They are found by
// the bork/codec package identity, never by the requesting class's name.
func (c *checker) resolveForeignDecoders(ci *ClassInstance) {
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
		if f.Computed {
			ci.ForeignDecoders = append(ci.ForeignDecoders, nil)
			continue
		}
		saved := c.diags
		c.diags = &diag.List{}
		c.have = nil
		for _, con := range f.Constraints {
			if con.Path == "" {
				c.have = append(c.have, con)
			}
		}
		var d *Dict
		if codec := c.info.PackageNamed("bork/codec"); codec != nil {
			d = c.dict(codec.ClassNamed("Decode"), f.Type, ci.Decl.Pos, 0)
		}
		c.diags = saved
		c.have = nil
		ci.ForeignDecoders = append(ci.ForeignDecoders, d)
	}
}
