package check

import (
	"reflect"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Check global references even when no target requests a template or a staged
// branch is not selected. Field-dependent operations and full lexical typing
// remain obligations of the typed expansion. Local spellings are collected
// first because their types may only become available after unrolling.
func (c *checker) checkDeriveDefinitions(files []*syntax.File) {
	for _, file := range files {
		c.inFile(file)
		var methods []*syntax.FuncDecl
		targets := map[*syntax.FuncDecl][]*syntax.TypeParam{}
		for _, template := range file.Templates {
			methods = append(methods, template.Methods...)
			for _, method := range template.Methods {
				targets[method] = template.TypeParams
			}
		}
		for _, helper := range file.DeriveHelpers {
			_, builtin := builtins[helper.Name]
			if c.pkg.Funcs[helper.Name] != nil || c.pkg.imports[helper.Name] != nil || c.pkg.bindings[helper.Name] != nil || c.pkg.ambients[helper.Name] != nil || c.isTypeName(helper.Name) || builtin {
				c.errorf(helper.Pos, "derive helper %s conflicts with an existing name", helper.Name)
			}
		}
		methods = append(methods, file.DeriveHelpers...)
		for _, method := range methods {
			locals := map[string]bool{}
			typeNames := map[string]bool{}
			typeDeclarations := map[string]*syntax.TypeParam{}
			for _, parameter := range append(append([]*syntax.TypeParam(nil), targets[method]...), method.TypeParams...) {
				if typeNames[parameter.Name] {
					c.errorf(parameter.Pos, "derive type parameter %s is declared twice", parameter.Name)
				}
				typeNames[parameter.Name] = true
				typeDeclarations[parameter.Name] = parameter
				for _, bound := range parameter.Bounds {
					if c.lookupClass(bound) == nil {
						c.errorf(parameter.Pos, "unknown class %s", bound)
					}
				}
			}
			parameterNames := map[string]bool{}
			for _, parameter := range method.Params {
				if parameterNames[parameter.Name] {
					c.errorf(parameter.Pos, "derive parameter %s is declared twice", parameter.Name)
				}
				parameterNames[parameter.Name] = true
			}
			var walk func(reflect.Value, func(any))
			walk = func(v reflect.Value, visit func(any)) {
				if !v.IsValid() {
					return
				}
				switch v.Kind() {
				case reflect.Interface:
					if !v.IsNil() {
						walk(v.Elem(), visit)
					}
				case reflect.Pointer:
					if v.IsNil() {
						return
					}
					visit(v.Interface())
					walk(v.Elem(), visit)
				case reflect.Struct:
					for _, index := range walkableSyntaxFields(v.Type()) {
						walk(v.Field(index), visit)
					}
				case reflect.Slice:
					for i := 0; i < v.Len(); i++ {
						walk(v.Index(i), visit)
					}
				}
			}
			// Walk the body separately from the FuncDecl's cyclic Instance link.
			collect := func(node any) {
				switch node := node.(type) {
				case *syntax.Param:
					locals[node.Name] = true
				case *syntax.Binding:
					locals[node.Name] = true
				case *syntax.For:
					locals[node.Name] = true
				case *syntax.ScopeExpr:
					locals[node.Name] = true
				case *syntax.TypePat:
					locals[node.Name] = true
				case *syntax.ListPat:
					locals[node.Rest] = true
				case *syntax.FieldPat:
					if node.Pattern == nil {
						locals[node.Field] = true
					}
				case *syntax.VariantPat:
					if len(node.Path) == 1 && !node.Context && !node.Braces && len(node.Fields) == 0 && !c.isTypeName(node.Path[0]) && !typeNames[node.Path[0]] {
						locals[node.Path[0]] = true
					}
				}
			}
			walk(reflect.ValueOf(method.Params), collect)
			walk(reflect.ValueOf(method.Body), collect)
			references := func(node any) {
				if pattern, ok := node.(*syntax.VariantPat); ok && len(pattern.Path) == 1 && typeNames[pattern.Path[0]] {
					if declaration := typeDeclarations[pattern.Path[0]]; declaration != nil {
						c.noteDeriveSource(pattern.Pos, pattern.Path[0], declaration.Pos, "typeParameter")
					}
				}
				if call, ok := node.(*syntax.Call); ok {
					c.checkDeriveCallShape(call, locals)
				}
				if written, ok := node.(*syntax.TypeExpr); ok && written.Name != "" {
					projected := false
					if owner, member, qualified := strings.Cut(written.Name, "."); qualified && member == "Type" && locals[owner] {
						projected = true
					}
					if typeNames[written.Name] || projected {
						if parameter := typeDeclarations[written.Name]; parameter != nil {
							c.noteDeriveSource(written.Pos, written.Name, parameter.Pos, "typeParameter")
						}
						if len(written.Args) != 0 {
							c.errorf(written.Pos, "a derive target reference cannot have type arguments")
						}
					} else if !c.isTypeName(written.Name) {
						c.errorf(written.Pos, "undefined type in derive definition: %s", written.Name)
					}
				}
				identifier, ok := node.(*syntax.Ident)
				if !ok || locals[identifier.Name] {
					return
				}
				if _, ok := builtins[identifier.Name]; ok {
					return
				}
				if identifier.Name == "Ok" || c.isTypeName(identifier.Name) {
					return
				}
				if _, ok := c.funcNamed(identifier.Name); ok {
					return
				}
				if helper, _ := c.deriveHelperNamed(c.pkg, identifier.Name); helper != nil {
					c.noteDeriveSource(identifier.Pos, identifier.Name, helper.Pos, "function")
					return
				}
				if c.packageBindingNamed(identifier.Name) != nil || c.ambientNamed(identifier.Name) != nil {
					return
				}
				if alias, member, qualified := strings.Cut(identifier.Name, "."); qualified {
					if pkg := c.pkg.imports[alias]; pkg != nil && pkg.Path == "bork/shape" && (member == "Record" || member == "Sealed" || member == "Other" || member == "fields" || member == "variants" || member == "kind" || member == "name" || member == "owner" || member == "facts" || member == "builder" || member == "fail") {
						return
					}
				}
				c.errorf(identifier.Pos, "undefined name in derive definition: %s", identifier.Name)
			}
			walk(reflect.ValueOf(method.Params), references)
			walk(reflect.ValueOf(method.Result), references)
			walk(reflect.ValueOf(method.Requires), references)
			walk(reflect.ValueOf(method.Body), references)
			c.checkDeriveScopes(method, locals, typeNames)
			c.checkDeriveLiteralTypes(method, typeNames)
		}
	}
}

func (c *checker) noteDeriveSource(pos diag.Pos, name string, definition diag.Pos, kind string) {
	c.info.sourceNames[pos] = name
	c.info.sourceDefinitions[pos] = definition
	if c.info.deriveSourceKinds == nil {
		c.info.deriveSourceKinds = map[diag.Pos]string{}
	}
	c.info.deriveSourceKinds[pos] = kind
}
