package check

import (
	"slices"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"

	"github.com/GiGurra/bork/internal/syntax"
)

// typeEntry is a declared type during resolution.
type typeEntry struct {
	decl      *syntax.TypeDecl
	typ       Type // *Record or *Sealed (created up front), or the alias target
	resolving bool // guards against alias cycles
	resolved  bool
	prelude   bool
	pkg       *Package // the declaring package
	// The where clause of a constrained alias (type Port = Int where ...).
	constraints     []*Constraint
	constraintsDone bool
}

// reservedTypeNames cannot be declared by user code.
var reservedTypeNames = map[string]bool{"Never": true, "List": true, "Map": true, "Seq": true}

func init() {
	for name := range basicTypes {
		reservedTypeNames[name] = true
	}
}

func (c *checker) isTypeName(name string) bool {
	if reservedTypeNames[name] {
		return true
	}
	return c.lookupType(name) != nil
}

// lookupType finds a declared type by name, as the code being checked
// sees it: its package's types, then the prelude's. A qualified name
// ("money.Amount") is looked up in an imported package, which must
// export it.
func (c *checker) lookupType(name string) *typeEntry {
	if pkg, n, ok := c.qualified(name); ok {
		if !Exported(n) {
			return nil
		}
		return pkg.types[n]
	}
	if e := c.pkg.types[name]; e != nil {
		return e
	}
	return c.preludePkg.types[name]
}

// Go tags retain their field-only placement; package groups are checked later.
func (c *checker) checkTagGroupSyntax(groups []*syntax.TagGroup, goFields bool) {
	seen := map[string]bool{}
	for _, group := range groups {
		if seen[group.Name] {
			c.errorf(group.Pos, "tag group %s is declared twice", group.Name)
		}
		seen[group.Name] = true
		if group.Name == "go" && !goFields {
			c.errorf(group.Pos, "Go struct tags can only be written on fields")
		}
	}
}

func (c *checker) declareType(td *syntax.TypeDecl, prelude bool) {
	c.checkTagGroupSyntax(td.TagGroups, false)
	for _, variant := range td.Variants {
		c.checkTagGroupSyntax(variant.TagGroups, false)
	}
	if reservedTypeNames[td.Name] {
		c.errorf(td.Pos, "%s is a built-in type and cannot be redefined", td.Name)
		return
	}
	if prev, ok := c.preludePkg.types[td.Name]; ok && !prelude {
		c.errorf(td.Pos, "%s is a built-in type and cannot be redefined", prev.decl.Name)
		return
	}
	if prev, ok := c.pkg.types[td.Name]; ok {
		c.errorf(td.Pos, "type %s is already declared at %s", td.Name, prev.decl.Pos)
		return
	}
	if _, ok := c.pkg.imports[td.Name]; ok {
		c.errorf(td.Pos, "%s is already the name of an imported package", td.Name)
		return
	}
	if _, ok := builtins[td.Name]; ok {
		c.errorf(td.Pos, "%s is a built-in function", td.Name)
		return
	}
	e := &typeEntry{decl: td, prelude: prelude, pkg: c.pkg}
	var params []*TypeParam
	seen := map[string]bool{}
	for _, d := range td.TypeParams {
		switch {
		case seen[d.Name]:
			c.errorf(d.Pos, "type parameter %s is declared twice", d.Name)
			continue
		case reservedTypeNames[d.Name]:
			c.errorf(d.Pos, "type parameter %s has the name of a type", d.Name)
			continue
		}
		seen[d.Name] = true
		params = append(params, &TypeParam{Name: d.Name, Decl: d})
	}
	switch td.Kind {
	case syntax.RecordType:
		e.typ = &Record{Name: td.Name, Decl: td, Prelude: prelude, Pkg: c.pkg, TypeParams: params, insts: newInstanceSet()}
	case syntax.SealedType:
		e.typ = &Sealed{Name: td.Name, Decl: td, Prelude: prelude, Pkg: c.pkg, TypeParams: params, insts: newInstanceSet()}
	case syntax.AliasType:
		if len(td.TypeParams) > 0 {
			c.errorf(td.Pos, "a type alias cannot have type parameters (yet); declare a record or sealed type")
		}
	case syntax.GoType:
		if len(td.TypeParams) > 0 {
			c.bindErr(td.Pos, "an opaque Go type cannot have type parameters")
		}
	case syntax.ResourceType:
		if len(td.TypeParams) > 0 {
			c.errorf(td.Pos, "a resource type cannot have type parameters")
		}
		if td.GoName == nil {
			e.typ = &Resource{Name: td.Name, Decl: td, Prelude: prelude, Pkg: c.pkg}
		}
	}
	if e.typ != nil {
		c.info.TypeOrder = append(c.info.TypeOrder, e.typ)
	}
	c.pkg.types[td.Name] = e
}

func (c *checker) resolveDecl(e *typeEntry) Type {
	if e.resolved {
		return e.typ
	}
	if e.resolving {
		c.errorf(e.decl.Pos, "type alias %s refers to itself", e.decl.Name)
		e.typ = Invalid
		return e.typ
	}
	e.resolving = true
	td := e.decl
	// Names in the declaration are those of its own package.
	savedPkg, savedPrelude := c.pkg, c.inPrelude
	c.pkg, c.inPrelude = e.pkg, e.prelude
	defer func() { c.pkg, c.inPrelude = savedPkg, savedPrelude }()
	// A generic type's parameters are visible in its fields.
	savedParams := c.typeParams
	if ps := typeParamsOf(e.typ); len(ps) > 0 {
		c.typeParams = map[string]*TypeParam{}
		for _, p := range ps {
			c.typeParams[p.Name] = p
		}
	}
	defer func() { c.typeParams = savedParams }()
	switch td.Kind {
	case syntax.GoType:
		e.typ = c.resolveGoDecl(e)
	case syntax.ResourceType:
		if td.GoName != nil {
			e.typ = c.resolveGoDecl(e)
		}
	case syntax.RecordType:
		r := e.typ.(*Record)
		r.Fields = c.resolveFields(td.Fields, "record "+td.Name)
	case syntax.SealedType:
		s := e.typ.(*Sealed)
		seen := map[string]bool{}
		for i, vd := range td.Variants {
			if seen[vd.Name] {
				c.errorf(vd.Pos, "variant %s is declared twice in %s", vd.Name, td.Name)
				continue
			}
			seen[vd.Name] = true
			fields := vd.Fields
			if vd.Positional {
				for i, slot := range vd.Slots {
					fields = append(fields, &syntax.FieldDecl{Pos: slot.Pos, Name: strconv.Itoa(i), Type: slot})
				}
			}
			s.Variants = append(s.Variants, &Variant{
				Name:       vd.Name,
				Doc:        vd.Doc,
				Positional: vd.Positional,
				Fields:     c.resolveFields(fields, "variant "+td.Name+"."+vd.Name),
				Parent:     s,
				Index:      i,
			})
		}
		if len(s.Variants) == 0 {
			c.errorf(td.Pos, "sealed type %s needs at least one variant", td.Name)
		}
	case syntax.AliasType:
		if len(td.TypeParams) > 0 {
			e.typ = Invalid // reported when declared
		} else {
			e.typ = c.resolveType(td.Alias)
		}
	}
	e.resolving = false
	e.resolved = true
	c.info.Named[td.Name] = e.typ
	switch t := e.typ.(type) {
	case *Record:
		t.insts.markResolved()
	case *Sealed:
		t.insts.markResolved()
	}
	return e.typ
}

func (c *checker) resolveFields(decls []*syntax.FieldDecl, owner string) []*Field {
	var fields []*Field
	seen := map[string]bool{}
	for _, fd := range decls {
		c.checkTagGroupSyntax(fd.TagGroups, true)
		if seen[fd.Name] {
			c.errorf(fd.Pos, "field %s is declared twice in %s", fd.Name, owner)
			continue
		}
		seen[fd.Name] = true
		t := c.resolveType(fd.Type)
		if t == Ok {
			c.errorf(fd.Type.Pos, "field %s cannot have type Ok", fd.Name)
			t = Invalid
		}
		fields = append(fields, &Field{Lazy: fd.Lazy, Name: fd.Name, Type: t, Decl: fd, Pkg: c.pkg, Prelude: c.inPrelude, Doc: fd.Doc, GoTags: fd.GoTags(), defaultGeneric: hasTypeParam(t)})
	}
	for _, field := range fields {
		field.siblings = fields
		field.defaultTypes = c.typeParams
	}
	return fields
}

// resolveType turns a written type into a Type. A nil type means Ok
// (a function without a declared result).
func (c *checker) resolveType(t *syntax.TypeExpr) Type {
	if known := c.info.assemblyTypes[t]; known != nil {
		return known
	}
	out := c.resolveTypeInner(t)
	if t != nil {
		c.noteDefaultTypeUse(out, t.Pos)
		c.info.writtenTypes[t] = out
		c.noteSourceType(t.Pos, t.Name)
	}
	return out
}
func (c *checker) resolveTypeInner(t *syntax.TypeExpr) Type {
	if t == nil {
		return Ok
	}
	if t.Tuple != nil {
		elems := make([]Type, len(t.Tuple))
		for i, elem := range t.Tuple {
			elems[i] = c.resolveType(elem)
			if elems[i] == Invalid {
				return Invalid
			}
			if !isValue(elems[i]) {
				c.errorf(elem.Pos, "a tuple cannot hold %s", elems[i])
				return Invalid
			}
		}
		return tupleType(elems)
	}
	if t.Union != nil {
		members := make([]Type, 0, len(t.Union))
		for _, m := range t.Union {
			mt := c.resolveType(m)
			switch {
			case mt == Invalid:
				return Invalid
			case !isValue(mt) && mt != Ok:
				// Ok can: `Ok | IoError` is a result that may fail.
				c.errorf(m.Pos, "%s cannot be part of a union", mt)
				return Invalid
			}
			members = append(members, mt)
		}
		return newUnion(members)
	}
	if t.Func != nil {
		ft := &FuncType{Result: c.resolveType(t.Func.Result), Effects: c.effectsOf(t.Func.Uses)}
		if ft.Result == Invalid {
			return Invalid
		}
		for _, p := range t.Func.Params {
			pt := c.resolveType(p)
			switch {
			case pt == Invalid:
				return Invalid
			case !isValue(pt):
				c.errorf(p.Pos, "a function cannot take a parameter of type %s", pt)
				return Invalid
			}
			ft.Params = append(ft.Params, pt)
		}
		if c.hasFacts(t.Func.Result) || slices.ContainsFunc(t.Func.Params, c.hasFacts) {
			c.whereReported(t)
			c.errorf(t.Pos, "where clauses inside function types are not supported yet")
			return Invalid
		}
		return ft
	}
	if tp := c.typeParams[t.Name]; tp != nil {
		if len(t.Args) > 0 {
			c.errorf(t.Pos, "type parameter %s does not take type arguments", t.Name)
			return Invalid
		}
		if len(t.Where) == 0 {
			// This name is a parameter, not an alias in the package.
			c.appliedWhere[t] = true
		}
		return tp
	}
	if t.Name == "Seq" {
		if len(t.Args) != 1 {
			c.errorf(t.Pos, "Seq needs exactly one type argument, as in Seq[Int]")
			return Invalid
		}
		elem := c.resolveType(t.Args[0])
		if elem == Invalid {
			return Invalid
		}
		if !isValue(elem) {
			c.errorf(t.Args[0].Pos, "Seq[%s] is not allowed", elem)
			return Invalid
		}
		return &Seq{Elem: elem, Effects: c.effectsOf(t.Uses)}
	}
	if t.Name == "List" {
		if len(t.Args) != 1 {
			c.errorf(t.Pos, "List needs exactly one type argument, as in List[Int]")
			return Invalid
		}
		elem := c.resolveType(t.Args[0])
		if elem == Invalid {
			return Invalid
		}
		if !isValue(elem) {
			c.errorf(t.Args[0].Pos, "List[%s] is not allowed", elem)
			return Invalid
		}
		return &List{Elem: elem}
	}
	if t.Name == "Map" {
		if len(t.Args) != 2 {
			c.errorf(t.Pos, "Map needs exactly two type arguments, as in Map[String, Int]")
			return Invalid
		}
		key, value := c.resolveType(t.Args[0]), c.resolveType(t.Args[1])
		if key == Invalid || value == Invalid {
			return Invalid
		}
		for i, a := range []Type{key, value} {
			if !isValue(a) {
				c.errorf(t.Args[i].Pos, "Map[%s, %s] is not allowed", key, value)
				return Invalid
			}
		}
		checkKey := func() {
			if !comparable(key) {
				c.errorf(t.Args[0].Pos, "a Map's keys must be comparable with ==, and %s is not (for a type parameter, bound it: [K: Eq])", key)
			}
		}
		if !comparable(key) && unresolvedLazyFields(key, map[Type]bool{}) {
			// Signatures precede default checking, which determines which
			// fields participate in structural equality and hashing.
			c.mapKeyChecks = append(c.mapKeyChecks, checkKey)
		} else if !comparable(key) {
			checkKey()
			return Invalid
		}
		return &Map{Key: key, Value: value}
	}
	if e := c.lookupType(t.Name); e != nil && e.decl.Kind != syntax.AliasType {
		params := typeParamsOf(e.typ)
		if len(params) == 0 {
			if len(t.Args) > 0 {
				c.errorf(t.Pos, "%s does not take type arguments", t.Name)
				return Invalid
			}
			return e.typ
		}
		if len(t.Args) != len(params) {
			c.errorf(t.Pos, "%s needs %d type argument(s), as in %s[%s]", t.Name, len(params), t.Name, paramNames(params))
			return Invalid
		}
		args := make([]Type, len(t.Args))
		for i, a := range t.Args {
			args[i] = c.resolveType(a)
			switch {
			case args[i] == Invalid:
				return Invalid
			case args[i] == Ok && isTask(e.typ):
			case !isValue(args[i]):
				c.errorf(a.Pos, "%s[%s] is not allowed", t.Name, args[i])
				return Invalid
			}
		}
		return instantiate(e.typ, args)
	}
	if len(t.Args) > 0 {
		c.errorf(t.Pos, "%s does not take type arguments", t.Name)
		return Invalid
	}
	if typ, ok := basicTypes[t.Name]; ok {
		return typ
	}
	if e := c.lookupType(t.Name); e != nil {
		if e.decl.Kind == syntax.AliasType {
			return c.resolveDecl(e)
		}
		return e.typ
	}
	c.unknownType(t.Pos, t.Name)
	return Invalid
}

// unknownType reports a type name that does not resolve.
func (c *checker) unknownType(pos diag.Pos, name string) {
	if why := c.notFound(name); why != "" {
		c.errorf(pos, "%s", why)
		return
	}
	c.errorf(pos, "unknown type %s", name)
}

func paramNames(ps []*TypeParam) string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}

// checkRecordCycles reports records that contain themselves directly
// (through other record fields). Such a value would be infinitely
// large; recursion must go through a sealed type or an Option.
func (c *checker) checkRecordCycles() {
	for _, t := range c.info.TypeOrder {
		r, ok := t.(*Record)
		if !ok {
			continue
		}
		seen := map[*Record]bool{}
		var reaches func(from *Record) bool
		reaches = func(from *Record) bool {
			for _, f := range from.Fields {
				fr, ok := f.Type.(*Record)
				if !ok {
					continue
				}
				if fr == r {
					return true
				}
				if !seen[fr] {
					seen[fr] = true
					if reaches(fr) {
						return true
					}
				}
			}
			return false
		}
		if reaches(r) {
			c.errorf(r.Decl.Pos, "record %s contains itself; recursive data must go through a sealed type or an Option", r.Name)
		}
	}
}
