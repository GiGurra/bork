package check

import (
	"crypto/sha256"
	"fmt"
	"slices"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

type deriveRequest struct {
	pos     diag.Pos
	name    string
	typ     Type
	written *syntax.TypeExpr
	class   *Class
	pkg     *Package
	prelude bool
	invalid bool
}

// Collect both spellings before preparing Go layouts or declaring instances.
func (c *checker) collectDerived(files []*syntax.File) {
	for _, file := range files {
		c.inFile(file)
		start := len(c.derives)
		for _, td := range file.Types {
			entry := c.pkg.types[td.Name]
			if entry == nil || entry.decl != td {
				continue
			}
			var written *syntax.TypeExpr
			if td.Kind == syntax.AliasType {
				written = &syntax.TypeExpr{Pos: td.Pos, Name: td.Name}
			}
			for _, class := range td.Derive {
				c.collectDerive(td.DerivePos, td.Name, entry.typ, written, class, file.Prelude, false)
			}
		}
		for _, decl := range file.Derives {
			entry := c.lookupType(decl.Type.Name)
			var typ Type
			if entry != nil && len(decl.Type.Args) == 0 && len(typeParamsOf(entry.typ)) > 0 {
				typ = entry.typ
				c.info.writtenTypes[decl.Type] = typ
				c.noteSourceType(decl.Type.Pos, decl.Type.Name)
			} else {
				typ = c.resolveType(decl.Type)
			}
			if typ == Invalid {
				continue
			}
			name := decl.Type.Name
			for i := len(name) - 1; i >= 0; i-- {
				if name[i] == '.' {
					name = name[i+1:]
					break
				}
			}
			class := c.lookupClass(decl.Class)
			if class != nil {
				c.info.sourceNames[decl.ClassPos] = decl.Class
				c.info.sourceDefinitions[decl.ClassPos] = class.Decl.Pos
			}
			c.collectDerive(decl.Pos, name, typ, decl.Type, decl.Class, file.Prelude, true)
		}
		slices.SortFunc(c.derives[start:], func(a, b *deriveRequest) int {
			if a.pos.Line != b.pos.Line {
				return a.pos.Line - b.pos.Line
			}
			return a.pos.Col - b.pos.Col
		})
	}
}

func (c *checker) collectDerive(pos diag.Pos, name string, typ Type, written *syntax.TypeExpr, class string, prelude, standalone bool) {
	var owner *Package
	switch t := typ.(type) {
	case *Record:
		owner = t.Pkg
	case *Sealed:
		owner = t.Pkg
	default:
		c.errorf(pos, "only records and sealed types can derive instances")
		return
	}
	cl := c.lookupClass(class)
	switch {
	case cl == nil && class == "Show":
		c.errorf(pos, "Show is not needed: toString shows every value")
		return
	case cl == nil:
		c.errorf(pos, "unknown class %s", class)
		return
	case IsEq(cl):
		c.errorf(pos, "Eq is built in: every type whose values can be compared has it, with no derive needed")
		return
	case !derivable(cl):
		c.errorf(pos, "%s cannot be derived; only Decode, Encode and GoStruct can (yet)", class)
		return
	}
	if tuple, ok := typ.(*Record); ok && tuple.Tuple {
		if standalone {
			c.errorf(pos, "tuples have implicit codec instances; standalone derive requires a named record or sealed target")
		} else if cl.ForeignRecord != nil {
			c.errorf(pos, "%s cannot be derived for a tuple", cl.Name)
		} else if cl.Template != nil && !IsCodec(cl, "Encode") && !IsCodec(cl, "Decode") {
			c.errorf(pos, "tuple aliases cannot derive custom classes; derive templates currently require a named record or sealed target")
		} else {
			c.tupleDerives = append(c.tupleDerives, &ClassInstance{Type: tuple, Class: cl, Pkg: c.pkg, Decl: &syntax.InstanceDecl{Pos: pos}})
		}
		return
	}
	if standalone && c.pkg != owner && c.pkg != cl.Pkg {
		c.errorf(pos, "cannot derive %s for %s: declare it in the type's package %s or the class's package %s", class, name, deriveOwner(owner), deriveOwner(cl.Pkg))
		return
	}
	c.derives = append(c.derives, &deriveRequest{pos: pos, name: name, typ: typ, written: written, class: cl, pkg: c.pkg, prelude: prelude})
}

func (c *checker) declareDerived() {
	for _, request := range c.derives {
		c.pkg, c.inPrelude = request.pkg, request.prelude
		if request.invalid {
			continue
		}
		c.deriveInstance(request)
	}
}

func (c *checker) deriveInstance(request *deriveRequest) {
	t, cl := request.typ, request.class
	var tps []*TypeParam
	var args []Type
	for _, p := range typeParamsOf(t) {
		tp := &TypeParam{Name: p.Name, Decl: p.Decl}
		if cl.Template == nil {
			tp.Bounds = []*Class{cl}
		}
		tps = append(tps, tp)
		args = append(args, tp)
	}
	head := t
	if len(tps) > 0 {
		head = instantiate(t, args)
	}
	name := request.name + cl.Name
	// Specialized heads need stable exported names, independent of traversal order.
	if request.written != nil && len(request.written.Args) > 0 {
		digest := sha256.Sum256([]byte(TypeText(t, nil)))
		name += fmt.Sprintf("_%x", digest[:4])
	}
	if cl.Template != nil {
		for _, tp := range tps {
			tp.Bounds = c.seededDeriveBounds(name, tp)
		}
	}
	for _, ci := range c.pkg.instances {
		if ci.Name == name {
			c.errorf(request.pos, "instance %s is already declared at %s", name, ci.Decl.Pos)
			return
		}
	}
	decl := &syntax.InstanceDecl{Pos: request.pos, Name: name, Type: request.written}
	for _, tp := range tps {
		param := &syntax.TypeParam{Name: tp.Name}
		for _, bound := range tp.Bounds {
			param.Bounds = append(param.Bounds, qualify(bound.Name, bound.Pkg, c.pkg))
		}
		decl.TypeParams = append(decl.TypeParams, param)
	}
	ci := &ClassInstance{Derived: request.name, Name: name, Decl: decl, Pkg: c.pkg, Prelude: request.prelude, Class: cl, TypeParams: tps, Type: head}
	bound := map[*TypeParam]Type{cl.Param: head}
	for _, m := range cl.Methods {
		fd := &syntax.FuncDecl{Pos: request.pos, Name: m.Decl.Name, Params: m.Decl.Params}
		fn := &Func{Decl: fd, Pkg: c.pkg, Prelude: request.prelude, Of: ci, TypeParams: tps, Result: subst(m.Result, bound), Effects: m.Effects, Derived: &Derived{}}
		for _, p := range m.Params {
			fn.Params = append(fn.Params, subst(p, bound))
		}
		if cl.Template != nil {
			fn.Derived = nil
			fn.TemplatePkg = cl.Template.Pkg
			fn.TemplateScope = ci
			fn.Decl = c.expandDeriveMethod(request, ci, fn, head)
			fd = fn.Decl
			c.info.ExpandedFunctions = append(c.info.ExpandedFunctions, fn)
		}
		fn.ParamConstraints = make([][]*Constraint, len(fn.Params))
		ci.Methods = append(ci.Methods, fn)
		c.info.FuncOf[fd] = fn
	}
	c.pkg.instances = append(c.pkg.instances, ci)
	c.info.ClassInstances = append(c.info.ClassInstances, ci)
}

func (c *checker) checkDerivedDuplicates() {
	for i, ci := range c.info.ClassInstances {
		if ci.Derived == "" {
			continue
		}
		for _, previous := range c.info.ClassInstances[:i] {
			if previous.Derived == "" || previous.Pkg != ci.Pkg || previous.Class != ci.Class || len(previous.TypeParams) != len(ci.TypeParams) {
				continue
			}
			if _, ok := matchHead(previous, ci.Type); !ok {
				continue
			}
			if len(missingConstraints(previous.Constraints, ci.Constraints)) != 0 || len(missingConstraints(ci.Constraints, previous.Constraints)) != 0 {
				continue
			}
			c.errorf(ci.Decl.Pos, "instance %s is already derived at %s", ci.Name, previous.Decl.Pos)
		}
	}
}

func deriveOwner(pkg *Package) string {
	if pkg.Path == "" {
		return "the prelude"
	}
	return pkg.Path
}
