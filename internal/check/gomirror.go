package check

import (
	"go/ast"
	"go/types"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/GiGurra/bork/internal/syntax"
)

// Mirrors keep bork record identity and representation. The Go type only
// describes conversions at the boundary.
func (c *checker) resolveGoMirrors(files []*syntax.File) {
	for _, f := range files {
		c.inFile(f)
		for _, td := range f.Types {
			if td.Kind != syntax.RecordType || td.GoName == nil {
				continue
			}
			e := c.pkg.types[td.Name]
			if e == nil || e.decl != td {
				continue
			}
			r, ok := e.typ.(*Record)
			if !ok {
				continue
			}
			if len(r.TypeParams) > 0 {
				c.bindErr(td.Pos, "a Go mirror cannot have type parameters")
				continue
			}
			gt := c.namedGoType(td.GoName.Name, td.GoName)
			if gt == nil {
				continue
			}
			if _, pointer := gt.(*types.Pointer); pointer {
				c.bindErr(td.Pos, "a Go mirror names a struct, not a pointer to one")
				continue
			}
			if _, ok := gt.Underlying().(*types.Struct); !ok {
				c.bindErr(td.Pos, "%s is not a Go struct, so it cannot be mirrored", td.GoName.Name)
				continue
			}
			r.GoMirror = gt
			for i, field := range r.Fields {
				if containsResource(field.Type, map[Type]bool{}) {
					c.bindErr(td.Fields[i].Pos, "mirror field %s contains a resource, but _borkFromGo has no ownership Scope; keep the outer Go object opaque or use an unsafe resource wrapper", field.Name)
					r.GoFields = append(r.GoFields, GoField{})
					continue
				}
				names := []string{}
				ch, n := utf8.DecodeRuneInString(field.Name)
				exact := string(unicode.ToUpper(ch)) + field.Name[n:]
				obj, index, _ := types.LookupFieldOrMethod(gt, false, nil, exact)
				if obj != nil {
					names = append(names, exact)
				} else {
					candidates := map[string]bool{}
					collectGoFields(gt, candidates, map[types.Type]bool{})
					for name := range candidates {
						if strings.EqualFold(name, field.Name) {
							if found, _, _ := types.LookupFieldOrMethod(gt, false, nil, name); found != nil {
								names = append(names, name)
							}
						}
					}
					sort.Strings(names)
				}
				if len(names) != 1 {
					c.bindErr(td.Fields[i].Pos, "%s mirrors %s, but field %s has %d matching exported Go fields", r.Name, td.GoName.Name, field.Name, len(names))
					r.GoFields = append(r.GoFields, GoField{})
					continue
				}
				obj, index, _ = types.LookupFieldOrMethod(gt, false, nil, names[0])
				v, ok := obj.(*types.Var)
				if !ok || !v.IsField() || !v.Exported() {
					c.bindErr(td.Fields[i].Pos, "%s is not an exported Go struct field", names[0])
					r.GoFields = append(r.GoFields, GoField{})
					continue
				}
				current := gt
				gf := GoField{Type: v.Type()}
				valid := true
				for _, j := range index {
					if _, pointer := current.Underlying().(*types.Pointer); pointer {
						valid = false
						break
					}
					sf := current.Underlying().(*types.Struct).Field(j)
					if !sf.Exported() {
						valid = false
						break
					}
					gf.Path = append(gf.Path, sf.Name())
					current = sf.Type()
				}
				if !valid {
					c.bindErr(td.Fields[i].Pos, "%s is promoted through a pointer or unexported embedded field, so it cannot be mirrored", names[0])
					gf = GoField{}
				}
				r.GoFields = append(r.GoFields, gf)
			}
		}
	}
}

func collectGoFields(t types.Type, names map[string]bool, seen map[types.Type]bool) {
	if seen[t] {
		return
	}
	seen[t] = true
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	if s, ok := t.Underlying().(*types.Struct); ok {
		for i := 0; i < s.NumFields(); i++ {
			f := s.Field(i)
			if ast.IsExported(f.Name()) {
				names[f.Name()] = true
			}
			if f.Embedded() {
				collectGoFields(f.Type(), names, seen)
			}
		}
	}
}

func containsResource(t Type, seen map[Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t := t.(type) {
	case *Resource:
		return true
	case *List:
		return containsResource(t.Elem, seen)
	case *Map:
		return containsResource(t.Value, seen)
	case *Record:
		for _, f := range t.Fields {
			if containsResource(f.Type, seen) {
				return true
			}
		}
	case *Sealed:
		for _, v := range t.Variants {
			for _, f := range v.Fields {
				if containsResource(f.Type, seen) {
					return true
				}
			}
		}
	case *Union:
		for _, m := range t.Members {
			if containsResource(m, seen) {
				return true
			}
		}
	}
	return false
}
