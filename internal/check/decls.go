package check

import (
	"github.com/GiGurra/bork/internal/syntax"
)

// typeEntry is a declared type during resolution.
type typeEntry struct {
	decl      *syntax.TypeDecl
	typ       Type // *Record or *Sealed (created up front), or the alias target
	resolving bool // guards against alias cycles
	resolved  bool
	prelude   bool
	// The where clause of a constrained alias (type Port = Int where ...).
	constraints     []*Constraint
	constraintsDone bool
}

// reservedTypeNames cannot be declared by user code.
var reservedTypeNames = map[string]bool{"Never": true, "Option": true, "List": true}

func init() {
	for name := range basicTypes {
		reservedTypeNames[name] = true
	}
}

func (c *checker) isTypeName(name string) bool {
	if reservedTypeNames[name] {
		return true
	}
	_, ok := c.decls[name]
	return ok
}

func (c *checker) declareType(td *syntax.TypeDecl, prelude bool) {
	if reservedTypeNames[td.Name] {
		c.errorf(td.Pos, "%s is a built-in type and cannot be redefined", td.Name)
		return
	}
	if prev, ok := c.decls[td.Name]; ok && prev.prelude {
		c.errorf(td.Pos, "%s is a built-in type and cannot be redefined", td.Name)
		return
	} else if ok {
		c.errorf(td.Pos, "type %s is already declared at %s", td.Name, prev.decl.Pos)
		return
	}
	if _, ok := builtins[td.Name]; ok {
		c.errorf(td.Pos, "%s is a built-in function", td.Name)
		return
	}
	e := &typeEntry{decl: td, prelude: prelude}
	switch td.Kind {
	case syntax.RecordType:
		e.typ = &Record{Name: td.Name, Decl: td, Prelude: prelude}
	case syntax.SealedType:
		e.typ = &Sealed{Name: td.Name, Decl: td, Prelude: prelude}
	}
	if e.typ != nil {
		c.info.TypeOrder = append(c.info.TypeOrder, e.typ)
	}
	c.decls[td.Name] = e
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
	switch td.Kind {
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
			s.Variants = append(s.Variants, &Variant{
				Name:   vd.Name,
				Fields: c.resolveFields(vd.Fields, "variant "+td.Name+"."+vd.Name),
				Parent: s,
				Index:  i,
			})
		}
		if len(s.Variants) == 0 {
			c.errorf(td.Pos, "sealed type %s needs at least one variant", td.Name)
		}
	case syntax.AliasType:
		e.typ = c.resolveType(td.Alias)
	}
	e.resolving = false
	e.resolved = true
	c.info.Named[td.Name] = e.typ
	return e.typ
}

func (c *checker) resolveFields(decls []*syntax.FieldDecl, owner string) []*Field {
	var fields []*Field
	seen := map[string]bool{}
	for _, fd := range decls {
		if seen[fd.Name] {
			c.errorf(fd.Pos, "field %s is declared twice in %s", fd.Name, owner)
			continue
		}
		seen[fd.Name] = true
		t := c.resolveType(fd.Type)
		if t == Unit {
			c.errorf(fd.Type.Pos, "field %s cannot have type Unit", fd.Name)
			t = Invalid
		}
		fields = append(fields, &Field{Name: fd.Name, Type: t})
	}
	return fields
}

// resolveType turns a written type into a Type. A nil type means Unit
// (a function without a declared result).
func (c *checker) resolveType(t *syntax.TypeExpr) Type {
	if t == nil {
		return Unit
	}
	if t.Union != nil {
		members := make([]Type, 0, len(t.Union))
		for _, m := range t.Union {
			mt := c.resolveType(m)
			switch {
			case mt == Invalid:
				return Invalid
			case !isValue(mt):
				c.errorf(m.Pos, "%s cannot be part of a union", mt)
				return Invalid
			}
			members = append(members, mt)
		}
		return newUnion(members)
	}
	if t.Func != nil {
		ft := &FuncType{Result: c.resolveType(t.Func.Result)}
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
		if hasWhere(t.Func.Result) || hasWhereIn(t.Func.Params) {
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
		return tp
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
	if t.Name == "Option" {
		if len(t.Args) != 1 {
			c.errorf(t.Pos, "Option needs exactly one type argument, as in Option[Int]")
			return Invalid
		}
		elem := c.resolveType(t.Args[0])
		if elem == Invalid {
			return Invalid
		}
		if !isValue(elem) {
			c.errorf(t.Args[0].Pos, "Option[%s] is not allowed", elem)
			return Invalid
		}
		return Option(elem)
	}
	if len(t.Args) > 0 {
		c.errorf(t.Pos, "%s does not take type arguments", t.Name)
		return Invalid
	}
	if typ, ok := basicTypes[t.Name]; ok {
		return typ
	}
	if e, ok := c.decls[t.Name]; ok {
		if e.decl.Kind == syntax.AliasType {
			return c.resolveDecl(e)
		}
		return e.typ
	}
	c.errorf(t.Pos, "unknown type %s", t.Name)
	return Invalid
}

func hasWhere(t *syntax.TypeExpr) bool {
	if t == nil {
		return false
	}
	return len(t.Where) > 0 || hasWhereIn(t.Union) || hasWhereIn(t.Args) ||
		(t.Func != nil && (hasWhere(t.Func.Result) || hasWhereIn(t.Func.Params)))
}

func hasWhereIn(ts []*syntax.TypeExpr) bool {
	for _, t := range ts {
		if hasWhere(t) {
			return true
		}
	}
	return false
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
