package check

import (
	"go/ast"
	"go/types"
	"sort"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
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
	if iface, ok := named.Underlying().(*types.Interface); ok && !iface.IsMethodSet() {
		c.bindErr(pos.Pos, "%s is a constraint interface, so it cannot hold Go values", name)
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
	case *Seq:
		return containsOpaque(t.Elem, seen)
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

// A generic field may acquire an opaque type only after substitution.
func (c *checker) checkOpaqueFields() {
	seen := map[string]bool{}
	check := func(fields, base []*Field, decls []*syntax.FieldDecl) {
		for i, f := range fields {
			var arguments func(*Constraint)
			arguments = func(con *Constraint) {
				for _, arg := range con.Args {
					if !arg.Sibling || arg.Type == nil {
						continue
					}
					typ := c.zonk(arg.Type)
					if !containsOpaque(typ, map[Type]bool{}) {
						continue
					}
					key := decls[i].Pos.String() + arg.Param + typeKey(typ)
					if !seen[key] {
						seen[key] = true
						c.bindErr(decls[i].Pos, "facts on instantiated field %s cannot refer to sibling %s of type %s, which holds a Go value that can change", f.Name, arg.Param, typ)
					}
				}
				for _, alt := range con.Or {
					arguments(alt)
				}
			}
			for _, con := range f.Constraints {
				arguments(con)
			}
			if len(base[i].Constraints) == 0 || !containsOpaque(c.zonk(f.Type), map[Type]bool{}) {
				continue
			}
			key := decls[i].Pos.String() + typeKey(c.zonk(f.Type))
			if !seen[key] {
				seen[key] = true
				c.bindErr(decls[i].Pos, "facts cannot apply to instantiated field %s of type %s, which holds a Go value that can change", f.Name, c.zonk(f.Type))
			}
		}
	}
	for _, t := range c.info.TypeOrder {
		switch t := t.(type) {
		case *Record:
			for _, key := range sortedInstanceKeys(t.insts) {
				i := t.insts.byKey[key].(*Record)
				if len(i.Constraints) > 0 && containsOpaque(i, map[Type]bool{}) {
					c.bindErr(t.Decl.Pos, "facts cannot apply to instantiated type %s, which holds a Go value that can change", i)
				}
				check(i.Fields, t.Fields, t.Decl.Fields)
			}
		case *Sealed:
			for _, key := range sortedInstanceKeys(t.insts) {
				i := t.insts.byKey[key].(*Sealed)
				constrained := len(i.Constraints) > 0
				for _, v := range i.Variants {
					constrained = constrained || len(v.Constraints) > 0
				}
				if constrained && containsOpaque(i, map[Type]bool{}) {
					c.bindErr(t.Decl.Pos, "facts cannot apply to instantiated type %s, which holds a Go value that can change", i)
				}
				for j, v := range i.Variants {
					check(v.Fields, t.Variants[j].Fields, t.Decl.Variants[v.Index].Fields)
				}
			}
		}
	}
}
func sortedInstanceKeys(s *instanceSet) []string {
	var keys []string
	if s != nil {
		for k := range s.byKey {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// Function references specialize the same promises as direct calls.
func (c *checker) checkOpaqueInstance(inst *Instance, pos diag.Pos, args []syntax.Expr) bool {
	fn := inst.Func
	for i, pt := range inst.Params {
		at := pos
		if i < len(args) {
			at = args[i].Position()
		}
		if (requirementMentionsParam(fn.Decl.Requires, fn.Decl.Params[i].Name) || i < len(fn.ParamConstraints) && len(fn.ParamConstraints[i]) > 0) && containsOpaque(pt, map[Type]bool{}) {
			c.bindErr(at, "facts cannot apply to parameter %s of type %s, which holds a Go value that can change", fn.Decl.Params[i].Name, pt)
			return false
		}
		if fn.Decl.IsPred && containsOpaque(pt, map[Type]bool{}) {
			c.bindErr(at, "predicate %s cannot take %s, which holds a Go value that can change", fn.Decl.Name, pt)
			return false
		}
	}
	for _, mc := range fn.ResultConstraints {
		t := subst(mc.Type, bindParams(fn.TypeParams, inst.TypeArgs))
		if containsOpaque(t, map[Type]bool{}) {
			c.bindErr(pos, "facts cannot apply to result %s, which holds a Go value that can change", t)
			return false
		}
	}
	if fn.Prelude && (fn.Decl.Name == "attach" || fn.Decl.Name == "move") && len(inst.TypeArgs) == 1 {
		_, ok := inst.TypeArgs[0].(*Resource)
		if !ok {
			c.errorf(pos, "%s takes a resource (a value of a resource type, such as File), found %s", fn.Decl.Name, inst.TypeArgs[0])
			return false
		}
	}
	return true
}

// Generic bodies are checked once, with their parameters still abstract.
// Propagate uses that require immutable bork data through specialization,
// including calls through generic wrappers and constrained record fields.
func (c *checker) checkOpaqueGenericUses(files []*syntax.File) {
	originalPackage := c.pkg
	defer func() { c.pkg = originalPackage }()
	sourcePackages := map[string]*Package{}
	for _, file := range files {
		pkg := c.pkgs[file.Package]
		if file.Prelude {
			pkg = c.preludePkg
		}
		sourcePackages[file.Path] = pkg
	}
	restricted := map[*TypeParam]string{}
	var mark func(Type, string, map[Type]bool)
	mark = func(t Type, why string, seen map[Type]bool) {
		if seen[t] {
			return
		}
		seen[t] = true
		switch t := t.(type) {
		case *TypeParam:
			if !t.unknown && restricted[t] == "" {
				restricted[t] = why
			}
		case *List:
			mark(t.Elem, why, seen)
		case *Map:
			mark(t.Key, why, seen)
			mark(t.Value, why, seen)
		case *Record:
			for _, f := range t.Fields {
				mark(f.Type, why, seen)
			}
		case *Sealed:
			for _, v := range t.Variants {
				for _, f := range v.Fields {
					mark(f.Type, why, seen)
				}
			}
		case *Union:
			for _, m := range t.Members {
				mark(m, why, seen)
			}
		}
	}
	markType := func(t Type, why string) { mark(t, why, map[Type]bool{}) }
	var constraints func([]*Constraint, *Func)
	constraints = func(cs []*Constraint, fn *Func) {
		for _, con := range cs {
			constraints(con.Or, fn)
			for _, a := range con.Args {
				for i, p := range fn.Decl.Params {
					if a.Param == p.Name {
						markType(fn.Params[i], "facts")
					}
				}
			}
		}
	}
	for _, fn := range c.info.FuncOf {
		if fn.Decl.IsPred {
			for _, p := range fn.Params {
				markType(p, "predicate "+fn.Decl.Name)
			}
		}
		if IsCodec(fn.Class, "Encode") || IsCodec(fn.Class, "Decode") {
			for _, tp := range fn.TypeParams {
				markType(tp, fn.Class.Name)
			}
		}
		if fn.Of != nil && (IsCodec(fn.Of.Class, "Encode") || IsCodec(fn.Of.Class, "Decode")) {
			markType(fn.Of.Type, fn.Of.Class.Name)
		}
		if fn.Decl.Requires != nil {
			for i, pt := range fn.Params {
				if requirementMentionsParam(fn.Decl.Requires, fn.Decl.Params[i].Name) {
					markType(pt, "facts")
				}
			}
		}
		for i, cs := range fn.ParamConstraints {
			if len(cs) > 0 {
				markType(fn.Params[i], "facts")
				constraints(cs, fn)
			}
		}
		for _, mc := range fn.ResultConstraints {
			markType(mc.Type, "facts")
			constraints(mc.Constraints, fn)
		}
	}
	var allTypes []Type
	for _, t := range c.info.types {
		allTypes = append(allTypes, t)
	}
	for _, t := range c.info.TypeOrder {
		allTypes = append(allTypes, t)
		switch t := t.(type) {
		case *Record:
			for _, f := range t.Fields {
				if len(f.Constraints) > 0 {
					markType(f.Type, "facts")
				}
			}
			for _, i := range t.insts.byKey {
				allTypes = append(allTypes, i)
			}
		case *Sealed:
			for _, v := range t.Variants {
				for _, f := range v.Fields {
					if len(f.Constraints) > 0 {
						markType(f.Type, "facts")
					}
				}
			}
			for _, i := range t.insts.byKey {
				allTypes = append(allTypes, i)
			}
		}
	}
	type use struct {
		inst *Instance
		pos  diag.Pos
	}
	var uses []use
	for e, i := range c.info.instances {
		uses = append(uses, use{i, e.Position()})
	}
	for e, i := range c.info.funcRefs {
		uses = append(uses, use{i, e.Position()})
	}
	// Class implementations specialize through dictionaries rather than calls.
	// Include their methods and recursively selected bounds in the same graph.
	var addDict func(*Dict, diag.Pos, map[*Dict]bool)
	addDict = func(d *Dict, pos diag.Pos, seen map[*Dict]bool) {
		if d == nil || seen[d] {
			return
		}
		seen[d] = true
		if d.Inst != nil {
			for _, fn := range d.Inst.Methods {
				uses = append(uses, use{&Instance{Func: fn, TypeArgs: d.TypeArgs}, pos})
			}
		}
		for _, arg := range d.Args {
			addDict(arg, pos, seen)
		}
	}
	for _, u := range append([]use(nil), uses...) {
		seen := map[*Dict]bool{}
		for _, d := range u.inst.Dicts {
			addDict(d, u.pos, seen)
		}
	}
	sort.Slice(uses, func(i, j int) bool {
		a, b := uses[i].pos, uses[j].pos
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
	for {
		before := len(restricted)
		for _, u := range uses {
			for i, tp := range u.inst.Func.TypeParams {
				if why := restricted[tp]; why != "" {
					markType(u.inst.TypeArgs[i], why)
				}
			}
		}
		for _, t := range allTypes {
			for i, a := range TypeArgs(t) {
				ps := typeParamsOf(genericBaseOrSelf(t))
				if i < len(ps) {
					if why := restricted[ps[i]]; why != "" {
						markType(a, why)
					}
				}
			}
		}
		if len(restricted) == before {
			break
		}
	}
	for _, u := range uses {
		c.pkg = sourcePackages[u.pos.File]
		for i, tp := range u.inst.Func.TypeParams {
			if why := restricted[tp]; why != "" && containsOpaque(u.inst.TypeArgs[i], map[Type]bool{}) {
				c.bindErr(u.pos, "%s of %s cannot be %s: its generic implementation uses %s, which cannot depend on opaque Go values", tp.Name, u.inst.Func.Decl.Name, u.inst.TypeArgs[i], why)
			}
		}
	}
}

func containsGoContext(t types.Type, seen map[types.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	if isGoContext(t) {
		return true
	}
	switch t := t.Underlying().(type) {
	case *types.Pointer:
		return containsGoContext(t.Elem(), seen)
	case *types.Slice:
		return containsGoContext(t.Elem(), seen)
	case *types.Array:
		return containsGoContext(t.Elem(), seen)
	case *types.Map:
		return containsGoContext(t.Key(), seen) || containsGoContext(t.Elem(), seen)
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			if containsGoContext(t.Field(i).Type(), seen) {
				return true
			}
		}
	}
	return false
}
