package check

import (
	"go/ast"
	"go/types"
	"sort"
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// Load all named packages together, so Go type identities agree across
// declarations, method receivers, and function signatures.
type loadedGoTypes struct {
	pkgs map[string]*types.Package
	errs map[string]error
}

func (l loadedGoTypes) Load([]string) (map[string]*types.Package, map[string]error) {
	return l.pkgs, l.errs
}

func loadGoTypes(files []*syntax.File, loader GoTypes) GoTypes {
	if loader == nil {
		return nil
	}
	paths := map[string]bool{}
	for _, f := range files {
		for _, td := range f.Types {
			if td.GoName != nil {
				if p, _, ok := splitGoName(strings.TrimPrefix(td.GoName.Name, "*")); ok {
					paths[p] = true
				}
			}
		}
		for _, fd := range f.Funcs {
			if fd.GoBind != nil {
				p, _, ok := splitGoName(fd.GoBind.Name)
				if ok {
					paths[p] = true
				}
			}
		}
	}
	if len(paths) == 0 {
		return nil
	}
	var list []string
	for p := range paths {
		list = append(list, p)
	}
	sort.Strings(list)
	pkgs, errs := loader.Load(list)
	return loadedGoTypes{pkgs, errs}
}

func forbiddenGoPath(path string) bool {
	return path == "internal" || strings.HasPrefix(path, "internal/") || strings.Contains(path, "/internal/") || strings.HasSuffix(path, "/internal") || path == "vendor" || strings.HasPrefix(path, "vendor/") || strings.Contains(path, "/vendor/")
}

func (c *checker) namedGoType(name string, pos *syntax.GoBind) types.Type {
	pointer := strings.HasPrefix(name, "*")
	path, n, ok := splitGoName(strings.TrimPrefix(name, "*"))
	if !ok || !ast.IsExported(n) || strings.ContainsAny(n, "[]()*") || forbiddenGoPath(path) {
		c.bindErr(pos.Pos, "%q is not an exported named Go type or a pointer to one", name)
		return nil
	}
	if c.goTypes == nil {
		c.bindErr(pos.Pos, "checking Go types needs Go's type information, which is not available here")
		return nil
	}
	pkgs, errs := c.goTypes.Load(nil)
	if err := errs[path]; err != nil {
		c.bindErr(pos.Pos, "Go package %q cannot be loaded: %v", path, err)
		return nil
	}
	pkg := pkgs[path]
	if pkg == nil {
		c.bindErr(pos.Pos, "Go package %q not found", path)
		return nil
	}
	obj, ok := pkg.Scope().Lookup(n).(*types.TypeName)
	if !ok {
		c.bindErr(pos.Pos, "%s has no named Go type %s", path, n)
		return nil
	}
	t := types.Unalias(obj.Type())
	named, ok := t.(*types.Named)
	if !ok || named.TypeParams().Len() > 0 || !goTypeVisible(t) {
		c.bindErr(pos.Pos, "%s is not a non-generic exported named Go type", name)
		return nil
	}
	if pointer {
		t = types.NewPointer(t)
	}
	return t
}

func (c *checker) resolveGoDecl(e *typeEntry) Type {
	td := e.decl
	if len(td.TypeParams) > 0 {
		return Invalid
	}
	gt := c.namedGoType(td.GoName.Name, td.GoName)
	if gt == nil {
		return Invalid
	}
	key := types.TypeString(gt, func(p *types.Package) string { return p.Path() })
	resource := td.Kind == syntax.ResourceType
	if prev := c.goOpaque[key]; prev != nil {
		_, wasResource := prev.(*Resource)
		if resource != wasResource {
			var decl *syntax.TypeDecl
			switch p := prev.(type) {
			case *Resource:
				decl = p.Decl
			case *Opaque:
				decl = p.Decl
			}
			c.bindErr(td.Pos, "Go type %s is declared both resource and non-resource: %s at %s and %s at %s", key, decl.Name, decl.Pos, td.Name, td.Pos)
			return Invalid
		}
		return prev
	}
	var t Type
	if resource {
		if !hasGoClose(gt) {
			c.bindErr(td.Pos, "Go resource %s needs a Close() or Close() error method", key)
			return Invalid
		}
		t = &Resource{Name: td.Name, Decl: td, Pkg: e.pkg, Prelude: e.prelude, GoType: gt}
	} else {
		t = &Opaque{Name: td.Name, Decl: td, Pkg: e.pkg, GoType: gt}
	}
	c.goOpaque[key] = t
	c.info.TypeOrder = append(c.info.TypeOrder, t)
	return t
}

func hasGoClose(t types.Type) bool {
	selection := types.NewMethodSet(t).Lookup(nil, "Close")
	if selection == nil {
		return false
	}
	sig := selection.Obj().Type().(*types.Signature)
	return sig.Params().Len() == 0 && (sig.Results().Len() == 0 || sig.Results().Len() == 1 && isGoError(sig.Results().At(0).Type()))
}

// Opaque values can mutate behind bork's back, so predicates and facts
// cannot depend on them, including through a container.
func containsOpaque(t Type, seen map[Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	if GoTypeOf(t) != nil {
		return true
	}
	switch t := t.(type) {
	case *List:
		return containsOpaque(t.Elem, seen)
	case *Map:
		return containsOpaque(t.Key, seen) || containsOpaque(t.Value, seen)
	case *Record:
		for _, f := range t.Fields {
			if containsOpaque(f.Type, seen) {
				return true
			}
		}
	case *Sealed:
		for _, v := range t.Variants {
			for _, f := range v.Fields {
				if containsOpaque(f.Type, seen) {
					return true
				}
			}
		}
	case *Union:
		for _, m := range t.Members {
			if containsOpaque(m, seen) {
				return true
			}
		}
	}
	return false
}

func isGoContext(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "context" && n.Obj().Name() == "Context"
}

func containsGoResource(t Type, seen map[Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t := t.(type) {
	case *Resource:
		return t.GoType != nil
	case *List:
		return containsGoResource(t.Elem, seen)
	case *Map:
		return containsGoResource(t.Value, seen)
	case *Sealed:
		for _, v := range t.Variants {
			for _, f := range v.Fields {
				if containsGoResource(f.Type, seen) {
					return true
				}
			}
		}
	case *Union:
		for _, m := range t.Members {
			if containsGoResource(m, seen) {
				return true
			}
		}
	}
	return false
}
