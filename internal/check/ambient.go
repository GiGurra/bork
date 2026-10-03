package check

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Ambient values (see "Ambient values" in docs/requirements.md): an
// `ambient traceId: String` declaration names a typed value that a
// function reads by declaring it, `needs traceId`, and that code binds
// for a block with `with (traceId: id) { ... }`.
//
// The checker treats a need as a local the function's body can read,
// and a with binding as a local of its block; both are bound under the
// ambient's key (ambientKey), which no identifier can spell, so only a
// name that resolves to the ambient finds them. A call of a function
// that needs a value is given the local in force at the call (or, for
// an optional need, Option.Some of it, the caller's own Option, or
// Option.None), and the generated Go passes it as a hidden parameter.
// Lambdas read these locals as they read any other, so they capture the
// bindings in force where they are made.

// Ambient is a declared ambient value.
type Ambient struct {
	Decl *syntax.AmbientDecl
	Name string
	Pkg  *Package
	Type Type
	// Constraints are the facts of its type (`ambient id: String where
	// nonEmpty`): a with must prove them, and a function that needs the
	// value knows them.
	Constraints []*Constraint
	// Logged and Header are its markers: with also publishes a marked
	// value in the goroutine's labels, for log lines (logged) and for
	// the header Header of outgoing calls (propagated).
	Logged bool
	Header string
}

// Marked reports whether with publishes the ambient's values in the
// goroutine's labels.
func (a *Ambient) Marked() bool {
	return a.Logged || a.Header != ""
}

// QualifiedName is the ambient's name as code in package from refers to
// it.
func (a *Ambient) QualifiedName(from *Package) string {
	return qualify(a.Name, a.Pkg, from)
}

// FuncNeed is one ambient value a function reads (`needs traceId`, or
// `needs locale?` for an optional one, read as an Option).
type FuncNeed struct {
	Ambient  *Ambient
	Optional bool
	Decl     *syntax.Need
	// Type is the type the function's body reads the value as.
	Type Type
}

// needSource is how a call provides one of its callee's needs: the
// local declared by decl (a *syntax.Need or a *syntax.WithBinding), as
// it is or wrapped in Option.Some; or nothing (decl nil), Option.None.
type needSource struct {
	need *FuncNeed
	decl any
	some bool
}

func ambientKey(a *Ambient) string {
	return "ambient " + a.Pkg.Path + "." + a.Name
}

func (c *checker) optionOf(t Type) Type {
	if s, ok := c.info.Named["Option"].(*Sealed); ok {
		return s.Instance([]Type{t})
	}
	return Invalid
}

// declareAmbients declares the ambient values of every package, once
// the types are resolved.
func (c *checker) declareAmbients(files []*syntax.File) {
	for _, f := range files {
		c.inFile(f)
		for _, ad := range f.Ambients {
			c.declareAmbient(ad)
		}
	}
}

func (c *checker) declareAmbient(ad *syntax.AmbientDecl) {
	if _, ok := c.pkg.ambients[ad.Name]; ok {
		c.errorf(ad.Pos, "ambient %s is already declared at %s", ad.Name, c.pkg.ambients[ad.Name].Decl.Pos)
		return
	}
	if _, ok := c.pkg.imports[ad.Name]; ok {
		c.errorf(ad.Pos, "%s is already the name of an imported package", ad.Name)
		return
	}
	if c.isTypeName(ad.Name) {
		c.errorf(ad.Pos, "%s is already the name of a type", ad.Name)
		return
	}
	if _, ok := builtins[ad.Name]; ok {
		c.errorf(ad.Pos, "%s is a built-in function", ad.Name)
		return
	}
	t := c.resolveType(ad.Type)
	if t == Unit || t == Never {
		c.errorf(ad.Type.Pos, "ambient %s must hold values, not %s", ad.Name, t)
		t = Invalid
	} else if t != Invalid && (&lifeChecker{carries: map[Type]bool{}}).carriesLife(t) {
		c.errorf(ad.Type.Pos, "ambient %s cannot hold %s: ambient values are data, so pass scopes, resources, tasks, atoms, channels and functions as parameters", ad.Name, t)
		t = Invalid
	}
	a := &Ambient{Decl: ad, Name: ad.Name, Pkg: c.pkg, Type: t, Logged: ad.Logged != nil}
	if ad.Propagated != nil {
		a.Header = ad.Propagated.Header
		c.propagatedHeader(a)
	}
	if a.Marked() && t != Invalid && !markable(t) {
		marker := "logged"
		if ad.Logged == nil {
			marker = "propagated"
		}
		c.errorf(ad.Type.Pos, "%s ambient %s must hold a String, Int, Float or Bool (facts allowed), found %s: logs and headers carry it as text", marker, ad.Name, t)
	}
	if c.pkg.ambients == nil {
		c.pkg.ambients = map[string]*Ambient{}
	}
	c.pkg.ambients[ad.Name] = a
	c.info.Ambients = append(c.info.Ambients, a)
}

// markable reports whether values of t can be logged and propagated:
// String, Int, Float or Bool, which logs and headers carry as text.
func markable(t Type) bool {
	return t == String || t == Int || t == Float || t == Bool
}

// propagatedHeader checks the header a propagated ambient is sent
// under: an HTTP header name (a token), used by no other declaration
// of the program (header names ignore case).
func (c *checker) propagatedHeader(a *Ambient) {
	pr := a.Decl.Propagated
	if !httpToken(a.Header) {
		c.errorf(pr.HeaderPos, "propagated(%q): a header name is letters, digits and !#$%%&'*+-.^_`|~, and not empty", a.Header)
		a.Header = ""
		return
	}
	key := strings.ToLower(a.Header)
	if other := c.info.propagatedHeaders[key]; other != nil {
		c.errorf(pr.HeaderPos, "header %s already carries %s (declared at %s)", a.Header, other.QualifiedName(c.pkg), other.Decl.Pos)
		a.Header = ""
		return
	}
	if c.info.propagatedHeaders == nil {
		c.info.propagatedHeaders = map[string]*Ambient{}
	}
	c.info.propagatedHeaders[key] = a
}

// httpToken reports whether s is an HTTP token (RFC 9110), as header
// names are.
func httpToken(s string) bool {
	for _, r := range s {
		letter := 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z'
		if !letter && (r < '0' || r > '9') && !strings.ContainsRune("!#$%&'*+-.^_`|~", r) {
			return false
		}
	}
	return s != ""
}

// ambientConstraints resolves the facts of the ambient values' types,
// once the predicates are declared.
func (c *checker) ambientConstraints(files []*syntax.File) {
	for _, f := range files {
		c.inFile(f)
		for _, ad := range f.Ambients {
			if a := c.pkg.ambients[ad.Name]; a != nil && a.Decl == ad && a.Type != Invalid {
				a.Constraints = c.constraintsOf(ad.Type, a.Type, nil)
			}
		}
	}
}

// ambientNamed is the ambient value a name (traceId, or trace.Id)
// refers to, or nil.
func (c *checker) ambientNamed(name string) *Ambient {
	if c.inPrelude {
		return nil
	}
	if pkg, n, ok := c.qualified(name); ok {
		if a := pkg.ambients[n]; a != nil && Exported(n) {
			return a
		}
		return nil
	}
	return c.pkg.ambients[name]
}

// notAmbient explains why name is not an ambient value the code can
// use: what, or that its package does not export it.
func (c *checker) notAmbient(name, what string) string {
	if pkg, n, ok := c.qualified(name); ok && pkg.ambients[n] != nil {
		return fmt.Sprintf("%s is not exported by %s", name, pkg.Path)
	}
	return what
}

// needsOf resolves a function's `needs` clause, sorted by package and
// name: the order of its hidden parameters.
func (c *checker) needsOf(fn *Func) {
	fd := fn.Decl
	if fd.Needs == nil {
		return
	}
	switch {
	case fd.Name == "main" && c.pkg.Root:
		c.errorf(fd.Needs.Pos, "main cannot declare needs; bind the values instead: with (%s: ...) { ... }", fd.Needs.Items[0].Name)
		return
	case fd.IsPred:
		c.errorf(fd.Needs.Pos, "pred %s cannot declare needs: predicates cannot read ambient values, or their facts would depend on what is bound", fd.Name)
		return
	case fd.GoBind != nil:
		c.errorf(fd.Needs.Pos, "%s binds a Go function, which cannot read ambient values", fd.Name)
		return
	}
	seen := map[*Ambient]bool{}
	for _, n := range fd.Needs.Items {
		a := c.ambientNamed(n.Name)
		if a == nil {
			c.errorf(n.Pos, "%s", c.notAmbient(n.Name, n.Name+" is not an ambient value"))
			continue
		}
		if seen[a] {
			c.errorf(n.Pos, "%s is needed twice", n.Name)
			continue
		}
		seen[a] = true
		if Exported(fd.Name) && !Exported(a.Name) && !c.pkg.Root {
			c.errorf(n.Pos, "%s is exported, but needs %s, which other packages cannot bind; export the ambient value, or the function's callers there cannot call it", fd.Name, n.Name)
		}
		if fd.IsGo() && a.Pkg == c.pkg {
			for _, tp := range fd.TypeParams {
				if tp.Name == a.Name {
					c.errorf(n.Pos, "%s's unsafe go body sees %s by its name, which type parameter %s already has", fd.Name, n.Name, tp.Name)
				}
			}
		}
		t := a.Type
		if n.Optional {
			t = c.optionOf(t)
		}
		fn.Needs = append(fn.Needs, &FuncNeed{Ambient: a, Optional: n.Optional, Decl: n, Type: t})
	}
	sort.SliceStable(fn.Needs, func(i, j int) bool {
		a, b := fn.Needs[i].Ambient, fn.Needs[j].Ambient
		if a.Pkg.Path != b.Pkg.Path {
			return a.Pkg.Path < b.Pkg.Path
		}
		return a.Name < b.Name
	})
}

// needsPlacement reports needs where they cannot be declared: on the
// methods of classes and instances, whose signatures the class fixes.
func (c *checker) needsPlacement(files []*syntax.File) {
	for _, f := range files {
		for _, cd := range f.Classes {
			for _, m := range cd.Methods {
				if m.Needs != nil {
					c.errorf(m.Needs.Pos, "class methods cannot declare needs")
				}
			}
		}
		for _, fd := range f.Funcs {
			if fd.Instance != nil && fd.Needs != nil {
				c.errorf(fd.Needs.Pos, "instance methods cannot declare needs: the class fixes their signature")
			}
		}
	}
}

// bindNeeds makes a function's needs readable in its body.
func (c *checker) bindNeeds(fn *Func) {
	for _, n := range fn.Needs {
		c.scopes[0][ambientKey(n.Ambient)] = &local{typ: n.Type, decl: n.Decl}
	}
}

// unusedNeeds reports the needs an unexported function never reads.
func (c *checker) unusedNeeds(fn *Func) {
	if Exported(fn.Decl.Name) || fn.Decl.IsGo() {
		return
	}
	for _, n := range fn.Needs {
		if l := c.scopes[0][ambientKey(n.Ambient)]; l != nil && !l.used {
			name := n.Decl.Name
			if n.Optional {
				name += "?"
			}
			c.errorf(n.Decl.Pos, "%s declares needs %s, but never reads it", fn.Decl.Name, name)
		}
	}
}

// ambientIdent reads an ambient value by name, where it is needed or
// bound.
func (c *checker) ambientIdent(e *syntax.Ident, a *Ambient) Type {
	if l := c.lookup(ambientKey(a)); l != nil {
		l.used = true
		c.noteLazyCapture(l.decl, e.Name)
		c.info.defs[e] = l.decl
		return l.typ
	}
	c.reportUnprovided(e.Pos, []string{e.Name}, "")
	return Invalid
}

// reportUnprovided reports a read of the ambient values names (directly,
// or by calling callee) that nothing provides, with the needs clause
// that would provide them as a fix where one can be declared.
func (c *checker) reportUnprovided(pos diag.Pos, names []string, callee string) {
	c.diags.AddCode(pos, "ambient.missing", "%s", c.unprovided(names, callee))
	if !c.mayNeed() {
		return
	}
	if c.needsFix == nil || c.needsFix.fn != c.fn {
		c.needsFix = &pendingNeeds{fn: c.fn, pos: pos}
	}
	for _, n := range names {
		if !slices.Contains(c.needsFix.names, n) {
			c.needsFix.names = append(c.needsFix.names, n)
		}
	}
}

// pendingNeeds collects the needs a function is missing, which its first
// missing-need diagnostic offers to declare, all at once.
type pendingNeeds struct {
	fn    *Func
	pos   diag.Pos
	names []string
}

// suggestNeeds attaches the fix that declares fn's missing needs.
func (c *checker) suggestNeeds(fn *Func) {
	if c.needsFix == nil || c.needsFix.fn != fn {
		return
	}
	c.diags.Suggest(c.needsFix.pos, "ambient.missing", c.needsFix.pos, needsFix(fn.Decl, strings.Join(c.needsFix.names, " + ")))
	c.needsFix = nil
}

// mayNeed reports whether the function being checked could declare
// needs.
func (c *checker) mayNeed() bool {
	fn := c.fn
	if fn == nil || fn.Decl.Name == "main" && fn.Pkg.Root {
		return false
	}
	return fn.Test == nil && !fn.Decl.IsPred && fn.Of == nil && fn.MockOf == nil && fn.Decl.GoBind == nil
}

// needsFix is the edit that makes fd also need name.
func needsFix(fd *syntax.FuncDecl, name string) diag.Fix {
	fix := diag.Fix{Message: "declare needs " + name}
	switch {
	case fd.Needs != nil:
		fix.Edits = []diag.TextEdit{{Start: fd.Needs.End, End: fd.Needs.End, Replacement: " + " + name}}
	case fd.Uses != nil:
		fix.Edits = []diag.TextEdit{{Start: fd.Uses.End, End: fd.Uses.End, Replacement: " needs " + name}}
	default:
		at := fd.ParamsEnd
		at.Col++ // just after the ')'
		fix.Edits = []diag.TextEdit{{Start: at, End: at, Replacement: " needs " + name}}
	}
	return fix
}

// unprovided explains that the code being checked reads the ambient
// values names, directly or by calling callee, without needing or
// binding them.
func (c *checker) unprovided(names []string, callee string) string {
	why := ""
	if callee != "" {
		why = " (it calls " + callee + ")"
	}
	name := strings.Join(names, " + ")
	it := "it"
	if len(names) > 1 {
		it = "them"
	}
	bind := "with (" + strings.Join(names, ": ..., ") + ": ...) { ... }"
	switch {
	case c.fn == nil:
		return fmt.Sprintf("%s is not bound here%s; bind %s: %s", name, why, it, bind)
	case c.fn.Test != nil:
		return fmt.Sprintf("the test reads %s%s, but does not bind %s: %s", name, why, it, bind)
	case c.fn.Decl.Name == "main" && c.fn.Pkg.Root:
		return fmt.Sprintf("main reads %s%s, but does not bind %s: %s", name, why, it, bind)
	case c.fn.Decl.IsPred:
		return fmt.Sprintf("%s reads %s%s, but predicates cannot read ambient values, or their facts would depend on what is bound; bind %s inside: %s", c.fn.Decl.Name, name, why, it, bind)
	case c.fn.MockOf != nil:
		return fmt.Sprintf("the mock of %s reads %s%s, which %s does not need; bind %s: %s", c.fn.Decl.Name, name, why, c.fn.Decl.Name, it, bind)
	case c.fn.Of != nil:
		return fmt.Sprintf("%s reads %s%s, but instance methods cannot declare needs; bind %s: %s", c.fn.Decl.Name, name, why, it, bind)
	}
	return fmt.Sprintf("%s reads %s%s, but its signature does not provide %s; declare %s: needs %s, or bind %s: %s", c.fn.Decl.Name, name, why, it, it, name, it, bind)
}

// provideNeeds finds what a call (or a reference) of fn at e passes for
// each of fn's needs, and records it.
func (c *checker) provideNeeds(e syntax.Expr, fn *Func, callee string) {
	if len(fn.Needs) == 0 {
		return
	}
	var sources []needSource
	var missing, maybe []string
	for _, n := range fn.Needs {
		name := n.Ambient.QualifiedName(c.pkg)
		l := c.lookup(ambientKey(n.Ambient))
		optionalHere := false
		if l != nil {
			if d, ok := l.decl.(*syntax.Need); ok && d.Optional {
				optionalHere = true
			}
			l.used = true
			c.noteLazyCapture(l.decl, name)
		}
		switch {
		case n.Optional && l == nil:
			sources = append(sources, needSource{need: n})
		case n.Optional:
			sources = append(sources, needSource{need: n, decl: l.decl, some: !optionalHere})
		case optionalHere:
			maybe = append(maybe, name)
		case l == nil:
			missing = append(missing, name)
		default:
			sources = append(sources, needSource{need: n, decl: l.decl})
		}
	}
	for _, name := range maybe {
		c.diags.AddCode(e.Position(), "ambient.missing", "%s needs %s, which may be unbound here (needs %s?); bind it: with (%s: ...) { ... }", callee, name, name, name)
	}
	if len(missing) > 0 {
		c.reportUnprovided(e.Position(), missing, callee)
	}
	if len(missing) > 0 || len(maybe) > 0 {
		return
	}
	if c.info.needArgs == nil {
		c.info.needArgs = map[syntax.Expr][]needSource{}
	}
	c.info.needArgs[e] = sources
}

// withExpr checks `with (name: value, ...) { ... }`: the values are
// checked where the with starts, then bound for its block.
func (c *checker) withExpr(e *syntax.WithExpr, want Type) Type {
	type bound struct {
		a *Ambient
		b *syntax.WithBinding
	}
	var binds []bound
	seen := map[*Ambient]bool{}
	for _, b := range e.Bindings {
		a := c.ambientNamed(b.Name)
		if a == nil {
			c.errorf(b.Pos, "%s", c.notAmbient(b.Name, "with binds ambient values, and "+b.Name+" is not one"))
			c.expr(b.Value)
			continue
		}
		t := c.exprWant(b.Value, a.Type)
		if t == Never {
			c.errorf(b.Value.Position(), "cannot bind %s: the expression never produces a value", b.Name)
		}
		if t, tt := c.settle(t, a.Type); t != Invalid && tt != Invalid && !assignable(t, tt) {
			c.errorf(b.Value.Position(), "%s must be %s, found %s", b.Name, tt, t)
		}
		if seen[a] {
			c.errorf(b.Pos, "%s is bound twice in one with", b.Name)
			continue
		}
		seen[a] = true
		binds = append(binds, bound{a, b})
	}
	c.pushScope()
	defer c.popScope()
	if c.info.withAmbients == nil {
		c.info.withAmbients = map[*syntax.WithBinding]*Ambient{}
	}
	for _, x := range binds {
		c.info.withAmbients[x.b] = x.a
		c.scopes[len(c.scopes)-1][ambientKey(x.a)] = &local{typ: x.a.Type, node: x.b, decl: x.b}
	}
	return c.record(e, c.block(e.Body, want))
}
