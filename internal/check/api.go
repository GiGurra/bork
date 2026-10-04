package check

import (
	"sort"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/doccomment"
	"github.com/GiGurra/bork/internal/syntax"
)

// PackageAPI is an owned view of the declarations an importer can use.
type PackageAPI struct {
	Path, Documentation string
	Unsafe              bool
	Declarations        []APIDeclaration
}
type APIDeclaration struct {
	Kind, Name, Receiver, Signature, Documentation string
	Position                                       diag.Pos
}

func API(info *Info, files []*syntax.File, path string) *PackageAPI {
	var pkg *Package
	for _, p := range info.Packages {
		if p.Path == path {
			pkg = p
			break
		}
	}
	if pkg == nil {
		return nil
	}
	out := &PackageAPI{Path: path}
	var sources []*syntax.File
	for _, f := range files {
		if !f.Prelude && f.Package == path {
			sources = append(sources, f)
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })
	var summaries []string
	for _, f := range sources {
		if text := doccomment.Package(f); text != "" {
			summaries = append(summaries, text)
		}
		for _, fn := range f.Funcs {
			out.Unsafe = out.Unsafe || fn.IsGo()
		}
	}
	out.Documentation = strings.Join(summaries, "\n\n")
	add := func(kind, name, receiver, signature string, pos diag.Pos) {
		doc := apiComment(sources, pos)
		out.Declarations = append(out.Declarations, APIDeclaration{kind, name, receiver, signature, doc, pos})
	}
	for name, e := range pkg.types {
		if Exported(name) {
			add("type", name, "", apiType(e.decl, sources), e.decl.Pos)
		}
	}
	for name, fn := range pkg.Funcs {
		if fn.Decl == nil || fn.Of != nil || fn.Synthetic || fn.Class != nil || !Exported(name) {
			continue
		}
		kind := "function"
		if fn.Decl.IsPred {
			kind = "predicate"
		}
		if fn.Decl.Constructor != nil {
			kind = "constructor"
		}
		add(kind, name, "", apiFunction(fn), fn.Decl.Pos)
	}
	for _, methods := range pkg.methods {
		for name, fn := range methods {
			if Exported(name) {
				add("method", name, writtenTypeText(fn.Decl.Params[0].Type), apiFunction(fn), fn.Decl.Pos)
			}
		}
	}
	for name, class := range pkg.classes {
		if !Exported(name) {
			continue
		}
		var signatures []string
		for _, fn := range class.Methods {
			signature := apiFunction(fn)
			if doc := apiComment(sources, fn.Decl.Pos); doc != "" {
				signature = "// " + strings.ReplaceAll(doc, "\n", "\n// ") + "\n" + signature
			}
			signatures = append(signatures, "  "+strings.ReplaceAll(signature, "\n", "\n  "))
		}
		add("class", name, "", "class "+name+apiParams(class.Decl.TypeParams)+" {\n"+strings.Join(signatures, "\n")+"\n}", class.Decl.Pos)
	}
	for _, instance := range pkg.instances {
		if Exported(instance.Name) && instance.Decl != nil {
			sig := "instance " + instance.Name + apiParams(instance.Decl.TypeParams) + ": " + qualify(instance.Class.Name, instance.Class.Pkg, pkg) + "[" + writtenTypeText(instance.Decl.Type) + "]"
			add("instance", instance.Name, "", sig, instance.Decl.Pos)
		}
	}
	for name, bundle := range pkg.bundles {
		if Exported(name) {
			var names []string
			for _, item := range bundle.decl.Items {
				names = append(names, item.Name)
			}
			add("instances", name, "", "instances "+name+" { "+strings.Join(names, ", ")+" }", bundle.decl.Pos)
		}
	}
	for name, bundle := range pkg.providers {
		if Exported(name) {
			var entries []string
			for _, e := range bundle.Entries {
				signature := apiFunction(e.Func)
				signature = strings.Replace(signature, "fn "+e.Func.Decl.Name, e.Decl.Name, 1)
				entries = append(entries, signature)
			}
			add("providers", name, "", "providers "+name+" {\n  "+strings.Join(entries, "\n  ")+"\n}", bundle.Decl.Pos)
		}
	}
	for name, a := range pkg.ambients {
		if Exported(name) {
			sig := "ambient " + name + ": " + writtenTypeText(a.Decl.Type)
			if a.Logged {
				sig = "logged " + sig
			}
			if a.Header != "" {
				sig = "propagated(" + strconv.Quote(a.Header) + ") " + sig
			}
			add("ambient", name, "", sig, a.Decl.Pos)
		}
	}
	for name, b := range pkg.bindings {
		if Exported(name) {
			typ := TypeText(b.Type, pkg)
			if b.Decl.Type != nil {
				typ = writtenTypeText(b.Decl.Type)
			}
			sig := name + ": " + typ
			if b.Decl.Lazy {
				sig = "lazy " + sig
			}
			add("value", name, "", sig, b.Decl.Pos)
		}
	}
	sort.Slice(out.Declarations, func(i, j int) bool {
		a, b := out.Declarations[i], out.Declarations[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Receiver != b.Receiver {
			return a.Receiver < b.Receiver
		}
		return a.Name < b.Name
	})
	return out
}

func apiComment(sources []*syntax.File, pos diag.Pos) string {
	for _, source := range sources {
		if source.Path == pos.File {
			return doccomment.At(source, pos)
		}
	}
	return ""
}

func apiParams(params []*syntax.TypeParam) string {
	if len(params) == 0 {
		return ""
	}
	var parts []string
	for _, p := range params {
		text := p.Name
		if len(p.Bounds) > 0 {
			text += ": " + strings.Join(p.Bounds, " + ")
		}
		parts = append(parts, text)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func apiFunction(fn *Func) string {
	fd := fn.Decl
	keyword := "fn"
	if fd.IsPred {
		keyword = "pred"
	}
	text := keyword + " "
	param := func(p *syntax.Param) string {
		value := p.Name + ": " + writtenTypeText(p.Type)
		if p.In != "" {
			value += " in " + p.In
		}
		if p.Default != nil {
			value += " = " + apiDefault(p.Default)
		}
		return value
	}
	start := 0
	if fd.IsMethod {
		text += "(" + param(fd.Params[0]) + ") "
		start = 1
	}
	var params []string
	for _, p := range fd.Params[start:] {
		params = append(params, param(p))
	}
	text += fd.Name + apiParams(fd.TypeParams) + "(" + strings.Join(params, ", ") + ")"
	if fn.Requires != nil {
		text += " where " + requirementText(fn.Requires, fn.Pkg)
	}
	if fd.Uses != nil || fn.Effects != 0 {
		text += " uses " + fn.Effects.String()
	}
	callable := DescribeCallable(fn, fn.Params, fn.Pkg, false)
	if len(callable.Needs) > 0 {
		text += " needs " + strings.Join(callable.Needs, " + ")
	}
	result := writtenTypeText(fd.Result)
	if fd.Result == nil {
		result = TypeText(fn.Result, fn.Pkg)
	}
	if !fd.IsPred {
		text += ": " + result
	}
	if fd.IsGo() {
		text += " unsafe go"
	}
	if fd.Constructor != nil && constructorInvariant(fn) {
		text += "\n// " + strings.Join(callable.Requires, "; ")
	}
	return text
}

// Computed defaults expose their presence, without rendering implementation code.
func apiDefault(expr syntax.Expr) string {
	if text := defaultText(expr); apiDefaultSupported(expr) && text != "" {
		return text
	}
	return "<computed>"
}

func apiDefaultSupported(expr syntax.Expr) bool {
	switch x := expr.(type) {
	case *syntax.IntLit, *syntax.FloatLit, *syntax.RuneLit, *syntax.StringLit, *syntax.BoolLit, *syntax.Ident, *syntax.TypeHead, *syntax.ContextName:
		return true
	case *syntax.Unary:
		return apiDefaultSupported(x.X)
	case *syntax.Selector:
		return apiDefaultSupported(x.X)
	case *syntax.ListLit:
		for _, e := range x.Elems {
			if !apiDefaultSupported(e) {
				return false
			}
		}
		return true
	case *syntax.MapLit:
		for i, key := range x.Keys {
			if !apiDefaultSupported(key) || !apiDefaultSupported(x.Values[i]) {
				return false
			}
		}
		return true
	case *syntax.RecordLit:
		if !apiDefaultSupported(x.Type) {
			return false
		}
		for _, field := range x.Fields {
			if !apiDefaultSupported(field.Value) {
				return false
			}
		}
		return true
	}
	return false
}

func apiFields(fields []*syntax.FieldDecl, sources []*syntax.File) string {
	var parts []string
	for _, f := range fields {
		text := f.Name + ": " + writtenTypeText(f.Type)
		if f.Lazy {
			text = "lazy " + text
		}
		if f.Default != nil {
			text += " = " + apiDefault(f.Default)
		}
		if doc := apiComment(sources, f.Pos); doc != "" {
			text = "// " + strings.ReplaceAll(doc, "\n", "\n  // ") + "\n  " + text
		}
		parts = append(parts, "  "+text)
	}
	return "{\n" + strings.Join(parts, ",\n") + "\n}"
}
func apiType(td *syntax.TypeDecl, sources []*syntax.File) string {
	text := "type " + td.Name + apiParams(td.TypeParams) + " = "
	if td.Private {
		text += "private "
	}
	switch td.Kind {
	case syntax.RecordType:
		text += apiFields(td.Fields, sources)
	case syntax.SealedType:
		var variants []string
		private := false
		for _, v := range td.Variants {
			if !Exported(v.Name) {
				private = true
				continue
			}
			value := v.Name
			if doc := apiComment(sources, v.Pos); doc != "" {
				value = "// " + strings.ReplaceAll(doc, "\n", "\n  // ") + "\n  " + value
			}
			if len(v.Fields) > 0 {
				value += " " + apiFields(v.Fields, sources)
			}
			if len(v.Where) > 0 {
				var clauses []string
				for _, q := range v.Where {
					clauses = append(clauses, writtenPredText(q))
				}
				value += " where " + strings.Join(clauses, " and ")
			}
			variants = append(variants, value)
		}
		if private {
			variants = append(variants, "// additional variants are private")
		}
		text += "sealed {\n  " + strings.Join(variants, ",\n  ") + "\n}"
	case syntax.AliasType:
		text += writtenTypeText(td.Alias)
	case syntax.ResourceType:
		text += "resource"
	case syntax.GoType:
		text += "go " + strconv.Quote(td.GoName.Name)
	}
	if len(td.Where) > 0 {
		var clauses []string
		for _, q := range td.Where {
			clauses = append(clauses, writtenPredText(q))
		}
		text += " where " + strings.Join(clauses, " and ")
	}
	if len(td.Derive) > 0 {
		text += " derive (" + strings.Join(td.Derive, ", ") + ")"
	}
	return text
}
