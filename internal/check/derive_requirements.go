package check

import (
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Requirements are keyed by stable declaration identities. Discovery uses
// fresh checker state on every round, so provisional dictionaries, defaults,
// and inference caches can never enter the checked program.
type deriveBounds map[string]map[string]bool

type deriveToken struct{ parameter, class string }
type deriveClause struct {
	premises []deriveToken
	result   deriveToken
}
type deriveDiscovery struct {
	clauses []deriveClause
	origins []deriveToken
}

// Derive bounds are the least closure of grounded obligations. A recursive
// dictionary may depend on its own bound; that edge alone must never invent or
// preserve a requirement after the grounded use disappears.
func (d *deriveDiscovery) solve() deriveBounds {
	known := map[deriveToken]bool{}
	for changed := true; changed; {
		changed = false
		for _, clause := range d.clauses {
			ready := true
			for _, premise := range clause.premises {
				if !known[premise] {
					ready = false
					break
				}
			}
			if ready && !known[clause.result] {
				known[clause.result] = true
				changed = true
			}
		}
	}
	result := deriveBounds{}
	for token := range known {
		if result[token.parameter] == nil {
			result[token.parameter] = map[string]bool{}
		}
		result[token.parameter][token.class] = true
	}
	return result
}

func deriveBoundKey(pkg *Package, instance, parameter string) string {
	return pkg.Path + "\x00" + instance + "\x00" + parameter
}

func classIdentity(class *Class) string { return class.Pkg.Path + "\x00" + class.Name }

func (c *checker) discoverBound(tp *TypeParam, class *Class) bool {
	if c.deriveDiscovery == nil || c.fn == nil || c.fn.TemplatePkg == nil || c.fn.TemplateScope == nil {
		return false
	}
	for _, parameter := range c.fn.TypeParams {
		if parameter != tp {
			continue
		}
		key := deriveBoundKey(c.fn.TemplateScope.Pkg, c.fn.TemplateScope.Name, tp.Name)
		c.deriveDiscovery.clauses = append(c.deriveDiscovery.clauses, deriveClause{premises: append([]deriveToken(nil), c.deriveDiscovery.origins...), result: deriveToken{key, classIdentity(class)}})
		return true
	}
	return false
}

func (c *checker) seededDeriveBounds(instance string, parameter *TypeParam) []*Class {
	if c.deriveBounds == nil {
		return nil
	}
	identities := slices.Sorted(maps.Keys(c.deriveBounds[deriveBoundKey(c.pkg, instance, parameter.Name)]))
	var result []*Class
	for _, identity := range identities {
		path, name, _ := strings.Cut(identity, "\x00")
		if pkg := c.pkgs[path]; pkg != nil {
			if class := pkg.classes[name]; class != nil {
				result = append(result, class)
			}
		}
	}
	return result
}

func templateFiles(files []*syntax.File) bool {
	hasTemplate, hasRequest := false, false
	for _, file := range files {
		hasTemplate = hasTemplate || len(file.Templates) > 0
		hasRequest = hasRequest || len(file.Derives) > 0
		for _, decl := range file.Types {
			hasRequest = hasRequest || len(decl.Derive) > 0
		}
	}
	return hasTemplate && hasRequest
}

// Parsed declarations may have cyclic instance/method links. Preserve those
// identities within the copy, while sharing no mutable nodes with the input.
func cloneDeriveFiles(files []*syntax.File) []*syntax.File { return cloneSyntax(files) }

func cloneSyntax[T any](source T) T {
	seen := map[reflect.Value]reflect.Value{}
	var clone func(reflect.Value) reflect.Value
	clone = func(value reflect.Value) reflect.Value {
		switch value.Kind() {
		case reflect.Pointer:
			if value.IsNil() {
				return value
			}
			if copied, ok := seen[value]; ok {
				return copied
			}
			out := reflect.New(value.Type().Elem())
			seen[value] = out
			out.Elem().Set(clone(value.Elem()))
			return out
		case reflect.Interface:
			if value.IsNil() {
				return value
			}
			out := reflect.New(value.Type()).Elem()
			out.Set(clone(value.Elem()))
			return out
		case reflect.Struct:
			out := reflect.New(value.Type()).Elem()
			out.Set(value)
			for _, index := range walkableSyntaxFields(value.Type()) {
				out.Field(index).Set(clone(value.Field(index)))
			}
			return out
		case reflect.Slice:
			if value.IsNil() {
				return value
			}
			out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
			for i := range value.Len() {
				out.Index(i).Set(clone(value.Index(i)))
			}
			return out
		}
		return value
	}
	return clone(reflect.ValueOf(source)).Interface().(T)
}

func programWithDeriveRequirements(files []*syntax.File, root string, diags *diag.List, goTypes GoTypes, observe func(string)) *Info {
	seed := deriveBounds{}
	var previous []deriveBounds
	for round := 0; round < 64; round++ {
		discovery := &deriveDiscovery{}
		programObserved(cloneDeriveFiles(files), root, &diag.List{}, goTypes, nil, seed, discovery)
		next := discovery.solve()
		if reflect.DeepEqual(seed, next) {
			return programObserved(files, root, diags, goTypes, observe, seed, nil)
		}
		for _, state := range previous {
			if reflect.DeepEqual(state, next) {
				diags.AddCode(diag.Pos{}, "derive.requirements", "recursive instance selection has no stable derive requirements")
				return programObserved(files, root, diags, goTypes, observe, next, nil)
			}
		}
		previous = append(previous, seed)
		seed = next
	}
	diags.AddCode(diag.Pos{}, "derive.requirements", "derive template requirements exceed the compile-time work limit")
	return programObserved(files, root, diags, goTypes, observe, seed, nil)
}
