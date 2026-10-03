package check

import (
	"fmt"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// ProviderBundle retains declaration identity across package boundaries.
type ProviderBundle struct {
	Decl    *syntax.ProviderBundle
	Pkg     *Package
	Entries []*ProviderBundleEntry
}

type ProviderBundleEntry struct {
	Decl *syntax.ProviderEntry
	Func *Func
}

func (c *checker) bundleError(pos diag.Pos, format string, args ...any) {
	c.diags.AddCode(pos, "assemble.bundle", format, args...)
}

func (c *checker) providerBundleNamed(name string) *ProviderBundle {
	if pkg, n, ok := c.qualified(name); ok {
		if Exported(n) {
			return pkg.providers[n]
		}
		return nil
	}
	return c.pkg.providers[name]
}

func providerProduct(fn *FuncType) Type {
	if u, ok := fn.Result.(*Union); ok {
		return u.Members[0]
	}
	return fn.Result
}

func validBundleProvider(fn *Func) bool {
	t := fn.funcType()
	product := providerProduct(t)
	if len(fn.TypeParams) > 0 || fn.Decl.IsPred || fn.Class != nil || fn.Prelude && builtins[fn.Decl.Name] != BuiltinNone || product == Scope || product == OwnedScope || product == Unit || product == Never || product == Invalid || !isValue(product) || hasTypeParam(t) || hasOpenEffects(t) {
		return false
	}
	for _, p := range t.Params {
		if _, union := p.(*Union); union || p == OwnedScope {
			return false
		}
	}
	return true
}

func (c *checker) declareProviderBundles(files []*syntax.File) {
	for _, f := range files {
		c.inFile(f)
		if c.pkg.providers == nil {
			c.pkg.providers = map[string]*ProviderBundle{}
		}
		for _, decl := range f.Providers {
			name := decl.Name
			taken := false
			for _, instance := range c.pkg.instances {
				taken = taken || instance.Name == name
			}
			for _, file := range files {
				if file.Package != f.Package {
					continue
				}
				for _, rule := range file.Rules {
					taken = taken || rule.Name == name
				}
			}
			if taken || c.pkg.providers[name] != nil || c.pkg.Funcs[name] != nil || c.isTypeName(name) || c.pkg.classes[name] != nil || c.pkg.bundles[name] != nil || c.pkg.imports[name] != nil {
				c.bundleError(decl.NamePos, "provider bundle %s conflicts with an existing declaration or imported package", name)
				continue
			}
			if _, builtin := builtins[name]; builtin {
				c.bundleError(decl.NamePos, "provider bundle %s conflicts with a compiler built-in", name)
				continue
			}
			bundle := &ProviderBundle{Decl: decl, Pkg: c.pkg}
			c.pkg.providers[name] = bundle
			c.info.ProviderBundles = append(c.info.ProviderBundles, bundle)
		}
	}
}

func (c *checker) resolveProviderBundles() {
	for _, bundle := range c.info.ProviderBundles {
		c.pkg = bundle.Pkg
		if len(bundle.Decl.Entries) == 0 {
			c.bundleError(bundle.Decl.Pos, "provider bundle %s must contain at least one entry", bundle.Decl.Name)
		}
		names := map[string]bool{}
		for _, entry := range bundle.Decl.Entries {
			out := &ProviderBundleEntry{Decl: entry}
			bundle.Entries = append(bundle.Entries, out)
			if names[entry.Name] {
				c.bundleError(entry.Pos, "provider bundle %s has duplicate entry %s", bundle.Decl.Name, entry.Name)
			}
			names[entry.Name] = true
			id, ok := entry.Provider.(*syntax.Ident)
			if !ok {
				c.bundleError(entry.Provider.Position(), "provider bundle entry %s must name a monomorphic declared function; use an adapter or a call-site replacement", entry.Name)
				continue
			}
			fn, ok := c.funcNamed(id.Name)
			if !ok {
				why := c.notFound(id.Name)
				if why == "" {
					why = fmt.Sprintf("%s is not a declared function", id.Name)
				}
				c.bundleError(id.Pos, "provider bundle entry %s: %s", entry.Name, why)
				continue
			}
			if !validBundleProvider(fn) || c.open(fn.funcType()) {
				c.bundleError(id.Pos, "provider bundle entry %s has unsupported signature %s; use a monomorphic assembly provider", entry.Name, fn.funcType())
				continue
			}
			out.Func = fn
		}
	}
}

// providerInputs flattens static slots while retaining expression evaluation order.
func (c *checker) providerInputs(args []syntax.Expr) []*assemblyProvider {
	var result []*assemblyProvider
	evaluation := 0
	for _, x := range args {
		id, direct := x.(*syntax.Ident)
		specialization, call := x.(*syntax.Call)
		if call {
			id, direct = specialization.Fun.(*syntax.Ident)
		}
		var bundle *ProviderBundle
		if direct && c.lookup(id.Name) == nil {
			bundle = c.providerBundleNamed(id.Name)
		}
		if bundle == nil {
			result = append(result, &assemblyProvider{x: x, eval: evaluation})
			evaluation++
			continue
		}
		replacements := map[string]*assemblyProvider{}
		if call {
			if len(specialization.TypeArgs) != 0 {
				c.bundleError(specialization.Pos, "provider bundle %s does not take type arguments", id.Name)
			}
			for i, expr := range specialization.Args {
				name := ""
				if i < len(specialization.Arguments) {
					name = specialization.Arguments[i].Name
				}
				if name == "" {
					c.bundleError(expr.Position(), "provider bundle %s takes only named entry replacements", id.Name)
					continue
				}
				var entry *ProviderBundleEntry
				for _, candidate := range bundle.Entries {
					if candidate.Decl.Name == name {
						entry = candidate
						break
					}
				}
				if entry == nil {
					c.bundleError(expr.Position(), "provider bundle %s has no entry %s", id.Name, name)
					continue
				}
				if replacements[name] != nil {
					c.bundleError(expr.Position(), "provider bundle entry %s.%s is replaced more than once", id.Name, name)
					continue
				}
				replacements[name] = &assemblyProvider{x: expr, eval: evaluation}
				evaluation++
			}
		}
		use := &providerBundleUse{name: id.Name, pos: id.Pos, bundle: bundle, pkg: c.pkg}
		if call {
			use.paren = specialization.Pos
		}
		c.info.providerBundleUses = append(c.info.providerBundleUses, use)
		for _, entry := range bundle.Entries {
			p := replacements[entry.Decl.Name]
			if p == nil {
				p = &assemblyProvider{x: entry.Decl.Provider, fn: entry.Func, eval: -1}
			}
			p.bundle, p.entry, p.entryPos = id.Name, entry.Decl.Name, entry.Decl.Pos
			if p.fn != nil {
				p.typ = p.fn.funcType()
			}
			if replacements[entry.Decl.Name] != nil && entry.Func != nil {
				p.expected = providerProduct(entry.Func.funcType())
			}
			result = append(result, p)
			use.entries = append(use.entries, p)
		}
	}
	return result
}

// ProviderBundleDescription is static wiring metadata, not a value type.
type ProviderBundleDescription struct {
	Name       string                     `json:"name"`
	Definition diag.Pos                   `json:"definition"`
	Entries    []ProviderEntryDescription `json:"entries"`
}

type ProviderEntryDescription struct {
	Name         string   `json:"name"`
	Function     string   `json:"function"`
	Position     diag.Pos `json:"position"`
	Product      string   `json:"product"`
	Dependencies []string `json:"dependencies"`
	Effects      string   `json:"effects"`
	Failures     []string `json:"failures"`
	Replaced     bool     `json:"replaced,omitempty"`
}

type providerBundleUse struct {
	name       string
	pos, paren diag.Pos
	bundle     *ProviderBundle
	pkg        *Package
	entries    []*assemblyProvider
}

func describeProviderBundle(name string, bundle *ProviderBundle, entries []*assemblyProvider, pkg *Package) *ProviderBundleDescription {
	out := &ProviderBundleDescription{Name: name, Definition: bundle.Decl.NamePos, Entries: []ProviderEntryDescription{}}
	for i, p := range entries {
		e := ProviderEntryDescription{Name: bundle.Entries[i].Decl.Name, Function: writtenText(p.x), Position: p.x.Position(), Dependencies: []string{}, Failures: []string{}, Replaced: p.expected != nil}
		if p.typ != nil {
			e.Product, e.Effects = TypeText(providerProduct(p.typ), pkg), p.typ.Effects.String()
			for _, t := range p.typ.Params {
				e.Dependencies = append(e.Dependencies, TypeText(t, pkg))
			}
			if union, ok := p.typ.Result.(*Union); ok {
				for _, t := range union.Members[1:] {
					e.Failures = append(e.Failures, TypeText(t, pkg))
				}
			}
		}
		out.Entries = append(out.Entries, e)
	}
	return out
}

// ProviderBundleAt returns metadata for a declaration or an assembly-use token.
func (info *Info) ProviderBundleAt(pos diag.Pos) (*ProviderBundleDescription, *Package) {
	contains := func(start diag.Pos, width int) bool {
		return start.File == pos.File && start.Line == pos.Line && start.Col <= pos.Col && pos.Col < start.Col+width
	}
	for _, use := range info.providerBundleUses {
		if contains(use.pos, len(use.name)) || pos == use.paren {
			return describeProviderBundle(use.name, use.bundle, use.entries, use.pkg), use.pkg
		}
	}
	for _, bundle := range info.ProviderBundles {
		if !contains(bundle.Decl.NamePos, len(bundle.Decl.Name)) {
			continue
		}
		var entries []*assemblyProvider
		for _, entry := range bundle.Entries {
			p := &assemblyProvider{x: entry.Decl.Provider, fn: entry.Func}
			if entry.Func != nil {
				p.typ = entry.Func.funcType()
			}
			entries = append(entries, p)
		}
		return describeProviderBundle(bundle.Decl.Name, bundle, entries, bundle.Pkg), bundle.Pkg
	}
	return nil, nil
}
