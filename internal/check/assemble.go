package check

import (
	"fmt"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Assembly describes the graph resolved at one call site.
type Assembly struct {
	Mode      string             `json:"mode"`
	Target    string             `json:"target"`
	Result    string             `json:"result"`
	Effects   string             `json:"effects"`
	Tree      string             `json:"tree"`
	Providers []AssemblyProvider `json:"providers"`
	Roots     []AssemblyRoot     `json:"roots"`
	Order     []int              `json:"order"`
}

type AssemblyProvider struct {
	Bundle        string               `json:"bundle,omitempty"`
	Entry         string               `json:"entry,omitempty"`
	EntryPosition *diag.Pos            `json:"entry_position,omitempty"`
	ID            int                  `json:"id"`
	Label         string               `json:"label"`
	Position      diag.Pos             `json:"position"`
	Product       string               `json:"product"`
	Dependencies  []AssemblyDependency `json:"dependencies"`
}

type AssemblyDependency struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Provider int    `json:"provider,omitempty"`
	Scope    bool   `json:"scope,omitempty"`
}

type AssemblyRoot struct {
	Name     string `json:"name,omitempty"`
	Type     string `json:"type"`
	Provider int    `json:"provider"`
}

type assemblyProvider struct {
	bundle, entry string
	entryPos      diag.Pos
	expected      Type
	eval          int
	x             syntax.Expr
	fn            *Func
	typ           *FuncType
	product       Type
	errors        []Type
	deps          []int
	names         []string
	state         int
	used          bool
}

func (p *assemblyProvider) label() string {
	if p.bundle != "" {
		return p.bundle + "." + p.entry + " (" + writtenText(p.x) + ")"
	}
	return writtenText(p.x)
}

type assemblyGraph struct {
	c         *checker
	call      *syntax.Call
	providers []*assemblyProvider
	roots     []int
	rootNames []string
	rootTypes []Type
	order     []int
	problems  []assemblyProblem
	ambiguous []Type
	path      []int
	target    Type
	success   Type
}

type assemblyProblem struct {
	code string
	pos  diag.Pos
	text string
}

func assemblyName(name string) bool {
	return name == "assemble" || name == "assembleAll" || name == "assembleRecord"
}

func (g *assemblyGraph) problem(code string, pos diag.Pos, format string, args ...any) {
	g.problems = append(g.problems, assemblyProblem{"assemble." + code, pos, fmt.Sprintf(format, args...)})
}

func (c *checker) assemble(call *syntax.Call, mode string) Type {
	outerEffects := c.used
	c.used = 0
	defer func() { c.used |= outerEffects }()
	c.rejectNamedArgs(call, "assembly takes a positional scope and provider list")
	if len(call.TypeArgs) != 1 || len(call.Args) < 2 {
		c.diags.AddCode(call.Pos, "assemble.target", "%s requires one target type, a Scope, and at least one provider", mode)
		return Invalid
	}
	target := c.resolveType(call.TypeArgs[0])
	if target == Invalid {
		return Invalid
	}
	if _, union := target.(*Union); union || target == Scope || assemblyOwner(target) || target == Ok || !isValue(target) || c.open(target) || hasTypeParam(target) {
		c.diags.AddCode(call.Pos, "assemble.target", "%s requires a concrete non-union product type, found %s", mode, target)
		return Invalid
	}
	scope := c.expr(call.Args[0])
	if scope == Invalid {
		return Invalid
	}
	if scope != Scope {
		c.diags.AddCode(call.Args[0].Position(), "assemble.target", "assembly requires a Scope, found %s", scope)
		return Invalid
	}
	g := &assemblyGraph{c: c, call: call, target: target, success: target}
	for i, p := range c.providerInputs(call.Args[1:]) {
		x := p.x
		// Direct references retain declaration facts and become direct calls.
		if id, ok := x.(*syntax.Ident); ok && p.fn == nil && c.lookup(id.Name) == nil {
			if fn, ok := c.funcNamed(id.Name); ok {
				if len(fn.TypeParams) > 0 {
					g.problem("provider", x.Position(), "provider #%d %s is generic; use a monomorphic adapter", i+1, id.Name)
					g.providers = append(g.providers, p)
					continue
				}
				p.fn, p.typ = fn, fn.funcType()
			}
		}
		if p.typ == nil {
			p.typ, _ = c.expr(x).(*FuncType)
		}
		if p.typ == nil {
			g.problem("provider", x.Position(), "provider #%d must be a function with a concrete signature", i+1)
			g.providers = append(g.providers, p)
			continue
		}
		p.product = p.typ.Result
		if u, ok := p.product.(*Union); ok {
			p.product, p.errors = u.Members[0], u.Members[1:]
		}
		if p.expected != nil && !identical(p.product, p.expected) {
			g.problem("bundle", x.Position(), "replacement for %s.%s must produce %s, found %s", p.bundle, p.entry, TypeText(p.expected, c.pkg), TypeText(p.product, c.pkg))
		}
		if p.product == Scope || assemblyOwner(p.product) || p.product == Ok || p.product == Never || p.product == Invalid || !isValue(p.product) || c.open(p.typ) || hasTypeParam(p.typ) || hasOpenEffects(p.typ) {
			g.problem("provider", x.Position(), "provider #%d has unsupported signature %s", i+1, p.typ)
			p.product = nil
		}
		for j, t := range p.typ.Params {
			name := fmt.Sprintf("argument %d", j+1)
			if p.fn != nil {
				name = p.fn.Decl.Params[j].Name
			}
			p.names = append(p.names, name)
			p.deps = append(p.deps, -1)
			if assemblyOwner(t) {
				g.problem("provider", x.Position(), "provider #%d parameter %s is an OwnedScope; assembly only borrows Scope values", i+1, name)
			}
			if _, ok := t.(*Union); ok {
				g.problem("provider", x.Position(), "provider #%d parameter %s has union type %s; use a wrapper product", i+1, name, t)
			}
		}
		g.providers = append(g.providers, p)
	}
	for _, p := range g.providers {
		if p.typ == nil {
			continue
		}
		for j, t := range p.typ.Params {
			if t == Scope {
				continue
			}
			candidates := g.candidates(t)
			if len(candidates) == 1 {
				p.deps[j] = candidates[0]
			}
		}
	}
	switch mode {
	case "assembleAll":
		g.success = &List{Elem: target}
		for i, p := range g.providers {
			if p.product != nil && identical(p.product, target) {
				g.roots = append(g.roots, i)
				g.rootNames = append(g.rootNames, "")
				g.rootTypes = append(g.rootTypes, target)
			}
		}
		if len(g.roots) == 0 {
			g.roots = append(g.roots, -1)
			g.rootNames = append(g.rootNames, "")
			g.rootTypes = append(g.rootTypes, target)
			g.problem("missing", call.Pos, "no provider produces collection element %s", target)
		}
	case "assembleRecord":
		record, ok := target.(*Record)
		if !ok {
			g.problem("target", call.Pos, "assembleRecord requires a record target, found %s", target)
		} else {
			for _, field := range record.Fields {
				g.rootNames = append(g.rootNames, field.Name)
				g.rootTypes = append(g.rootTypes, field.Type)
				g.roots = append(g.roots, g.find(field.Type, call.Pos, "field "+field.Name))
			}
		}
	default:
		g.roots = append(g.roots, g.find(target, call.Pos, "target"))
		g.rootNames = append(g.rootNames, "")
		g.rootTypes = append(g.rootTypes, target)
	}
	for _, root := range g.roots {
		g.visit(root)
	}
	for i, p := range g.providers {
		if p.product == nil || g.isAmbiguous(p.product) {
			continue
		}
		for j := 0; j < i; j++ {
			other := g.providers[j]
			if other.product != nil && identical(p.product, other.product) && (mode != "assembleAll" || !identical(p.product, target)) {
				g.problem("duplicate", p.x.Position(), "duplicate providers #%d and #%d for %s", j+1, i+1, p.product)
			}
		}
	}
	// When a root or edge is ambiguous, its candidates have been marked used.
	for i, p := range g.providers {
		if p.product != nil && !p.used {
			g.problem("unused", p.x.Position(), "unused provider #%d %s produces %s", i+1, p.label(), p.product)
		}
	}
	resultTypes := []Type{g.success}
	for _, i := range g.order {
		p := g.providers[i]
		for _, failure := range p.errors {
			for _, other := range g.providers {
				if other.product != nil && identical(failure, other.product) {
					g.problem("failure", p.x.Position(), "provider #%d failure %s is also a graph product; use a distinct failure type", i+1, failure)
					break
				}
			}
			if identical(failure, g.success) {
				g.problem("failure", p.x.Position(), "provider #%d failure %s is also the assembly success type", i+1, failure)
			}
			resultTypes = append(resultTypes, failure)
		}
	}
	result := newUnion(resultTypes)
	description := g.describe(mode, result)
	if len(g.problems) > 0 {
		for _, problem := range g.problems {
			c.diags.AddCode(problem.pos, problem.code, "%s\n%s", problem.text, description.Tree)
		}
		return Invalid
	}
	body := g.expand()
	t := c.exprWant(body, result)
	description.Effects = c.used.String()
	c.info.assemblyCalls[call] = &assemblyExpansion{body: body, description: description}
	return t
}

// Assembly never transfers closing rights, even when a provider could accept one.
func assemblyOwner(t Type) bool {
	return t == OwnedScope
}

func hasOpenEffects(t Type) bool {
	switch t := t.(type) {
	case *FuncType:
		if t.Effects&EffOpen != 0 || hasOpenEffects(t.Result) {
			return true
		}
		for _, p := range t.Params {
			if hasOpenEffects(p) {
				return true
			}
		}
	}
	return false
}

func (g *assemblyGraph) candidates(t Type) []int {
	var found []int
	for i, p := range g.providers {
		if p.product != nil && identical(p.product, t) {
			found = append(found, i)
		}
	}
	return found
}

func (g *assemblyGraph) find(t Type, pos diag.Pos, label string) int {
	found := g.candidates(t)
	if len(found) == 1 {
		return found[0]
	}
	if len(found) == 0 {
		g.problem("missing", pos, "missing provider for %s, needed by %s", t, label)
	} else {
		var labels []string
		for _, i := range found {
			g.providers[i].used = true
			labels = append(labels, fmt.Sprintf("#%d %s", i+1, g.providers[i].label()))
		}
		if !g.isAmbiguous(t) {
			g.ambiguous = append(g.ambiguous, t)
			g.problem("duplicate", pos, "ambiguous %s needed by %s: %s", t, label, strings.Join(labels, ", "))
		}
		// Invalid graphs still expose every candidate's dependency problems.
		for _, candidate := range found {
			g.visit(candidate)
		}
	}
	return -1
}

func (g *assemblyGraph) isAmbiguous(t Type) bool {
	for _, other := range g.ambiguous {
		if identical(t, other) {
			return true
		}
	}
	return false
}

func (g *assemblyGraph) visit(i int) {
	if i < 0 {
		return
	}
	p := g.providers[i]
	p.used = true
	if p.state == 2 {
		return
	}
	if p.state == 1 {
		var path []string
		cycle := g.path
		for j, n := range cycle {
			if n == i {
				cycle = cycle[j:]
				break
			}
		}
		for _, n := range append(append([]int(nil), cycle...), i) {
			path = append(path, fmt.Sprintf("%s (#%d %s)", g.providers[n].product, n+1, g.providers[n].label()))
		}
		g.problem("cycle", p.x.Position(), "dependency cycle: %s", strings.Join(path, " -> "))
		return
	}
	p.state = 1
	g.path = append(g.path, i)
	for j, t := range p.typ.Params {
		if t == Scope {
			continue
		}
		p.deps[j] = g.find(t, p.x.Position(), fmt.Sprintf("#%d %s parameter %s", i+1, p.label(), p.names[j]))
		g.visit(p.deps[j])
	}
	g.path = g.path[:len(g.path)-1]
	p.state = 2
	g.order = append(g.order, i)
}

func (g *assemblyGraph) describe(mode string, result Type) *Assembly {
	a := &Assembly{Mode: mode, Target: TypeText(g.target, g.c.pkg), Result: TypeText(result, g.c.pkg), Providers: []AssemblyProvider{}, Roots: []AssemblyRoot{}, Order: []int{}}
	var effects Effects
	for i, p := range g.providers {
		product := "invalid"
		if p.product != nil {
			product = TypeText(p.product, g.c.pkg)
		}
		out := AssemblyProvider{ID: i + 1, Label: p.label(), Position: p.x.Position(), Product: product, Dependencies: []AssemblyDependency{}}
		if p.bundle != "" {
			out.Bundle, out.Entry, out.EntryPosition = p.bundle, p.entry, &p.entryPos
		}
		if p.typ != nil {
			for j, t := range p.typ.Params {
				out.Dependencies = append(out.Dependencies, AssemblyDependency{Name: p.names[j], Type: TypeText(t, g.c.pkg), Provider: p.deps[j] + 1, Scope: t == Scope})
			}
			if p.used {
				effects |= p.typ.Effects
			}
		}
		a.Providers = append(a.Providers, out)
	}
	for j, i := range g.roots {
		a.Roots = append(a.Roots, AssemblyRoot{Name: g.rootNames[j], Type: TypeText(g.rootTypes[j], g.c.pkg), Provider: i + 1})
	}
	for _, i := range g.order {
		a.Order = append(a.Order, i+1)
	}
	a.Effects = effects.String()
	var tree strings.Builder
	tree.WriteString("assembly " + mode + "[" + a.Target + "]:\n")
	seen := map[int]bool{}
	var node func(int, Type, string, string)
	node = func(i int, requested Type, label, indent string) {
		tree.WriteString(indent + label)
		if i < 0 {
			candidates := g.candidates(requested)
			if len(candidates) == 0 {
				tree.WriteString(" ?? missing provider\n")
			} else {
				tree.WriteString(" ?? ambiguous providers\n")
				for _, candidate := range candidates {
					node(candidate, requested, "candidate", indent+"  ")
				}
			}
			return
		}
		p := g.providers[i]
		fmt.Fprintf(&tree, " <- #%d %s (%s)", i+1, p.label(), p.x.Position())
		if seen[i] {
			tree.WriteString(" [shared or cycle; see above]\n")
			return
		}
		seen[i] = true
		tree.WriteByte('\n')
		for j, t := range p.typ.Params {
			label := p.names[j] + ": " + TypeText(t, g.c.pkg)
			if t == Scope {
				tree.WriteString(indent + "  " + label + " <- target scope\n")
			} else {
				node(p.deps[j], t, label, indent+"  ")
			}
		}
	}
	for j, i := range g.roots {
		label := TypeText(g.rootTypes[j], g.c.pkg)
		if g.rootNames[j] != "" {
			label = g.rootNames[j] + ": " + label
		}
		node(i, g.rootTypes[j], label, "  ")
	}
	tree.WriteString("supplied:\n")
	for i, p := range a.Providers {
		fmt.Fprintf(&tree, "  #%d %s -> %s", p.ID, p.Label, p.Product)
		if !g.providers[i].used {
			tree.WriteString(" (unused)")
		}
		tree.WriteByte('\n')
	}
	a.Tree = strings.TrimSuffix(tree.String(), "\n")
	return a
}

type assemblyExpansion struct {
	body        *syntax.Block
	description *Assembly
}

func (g *assemblyGraph) typeExpr(t Type, pos diag.Pos) *syntax.TypeExpr {
	te := &syntax.TypeExpr{Pos: pos}
	g.c.info.assemblyTypes[te] = t
	return te
}

// expand builds ordinary checked blocks and matches; failure arms skip all
// remaining calls. No generated lambda changes returns, facts or lifetimes.
func (g *assemblyGraph) expand() *syntax.Block {
	c, pos := g.c, g.call.Pos
	c.assemblySerial++
	// Leading underscores are reserved, so these names cannot shadow user code.
	prefix := fmt.Sprintf("__assembly%d_", c.assemblySerial)
	id := func(name string) *syntax.Ident { return &syntax.Ident{Pos: pos, Name: prefix + name} }
	block := &syntax.Block{Pos: pos, End: g.call.End}
	scopeBinding := &syntax.Binding{Pos: pos, Name: prefix + "scope", Value: g.call.Args[0]}
	c.info.assemblyNames[scopeBinding] = writtenText(g.call.Args[0])
	block.Stmts = append(block.Stmts, scopeBinding)
	indices := make([]int, len(g.providers))
	for i := range indices {
		indices[i] = i
	}
	slices.SortFunc(indices, func(a, b int) int { return g.providers[a].eval - g.providers[b].eval })
	for _, i := range indices {
		p := g.providers[i]
		if p.fn == nil {
			block.Stmts = append(block.Stmts, &syntax.Binding{Pos: p.x.Position(), Name: prefix + fmt.Sprintf("provider%d", i), Value: p.x})
		}
	}
	product := func(i int) syntax.Expr { return id(fmt.Sprintf("value%d", i)) }
	var tail syntax.Expr
	switch g.call.Fun.(*syntax.Ident).Name {
	case "assembleAll":
		list := &syntax.ListLit{Pos: pos}
		for _, i := range g.roots {
			list.Elems = append(list.Elems, product(i))
		}
		tail = list
	case "assembleRecord":
		record := &syntax.RecordLit{Type: &syntax.Ident{Pos: pos, Name: g.call.TypeArgs[0].Name}, End: g.call.End}
		for j, i := range g.roots {
			record.Fields = append(record.Fields, &syntax.FieldInit{Pos: pos, Name: g.rootNames[j], Value: product(i)})
		}
		tail = record
	default:
		tail = product(g.roots[0])
	}
	// The annotated binding checks target facts, including constrained aliases.
	targetExpr := g.call.TypeArgs[0]
	if g.call.Fun.(*syntax.Ident).Name == "assembleAll" {
		targetExpr = &syntax.TypeExpr{Pos: pos, Name: "List", Args: []*syntax.TypeExpr{targetExpr}}
	}
	resultBinding := &syntax.Binding{Pos: pos, Name: prefix + "result", Type: targetExpr, Value: tail}
	c.info.assemblyNames[resultBinding] = "assembly result"
	tail = &syntax.Block{Pos: pos, End: g.call.End, Stmts: []syntax.Stmt{resultBinding}, Tail: id("result")}
	for n := len(g.order) - 1; n >= 0; n-- {
		i := g.order[n]
		p := g.providers[i]
		fun := p.x
		if p.fn != nil && p.bundle != "" {
			ref := id(fmt.Sprintf("function%d", i))
			ref.Pos = p.x.Position()
			if c.assemblyFuncs == nil {
				c.assemblyFuncs = map[string]*Func{}
			}
			c.assemblyFuncs[ref.Name] = p.fn
			fun = ref
		}
		if p.fn == nil {
			fun = id(fmt.Sprintf("provider%d", i))
		}
		call := &syntax.Call{Pos: p.x.Position(), End: g.call.End, Fun: fun}
		for j, t := range p.typ.Params {
			if t == Scope {
				call.Args = append(call.Args, id("scope"))
			} else {
				call.Args = append(call.Args, product(p.deps[j]))
			}
		}
		valueName := prefix + fmt.Sprintf("value%d", i)
		if len(p.errors) == 0 {
			binding := &syntax.Binding{Pos: pos, Name: valueName, Value: call}
			c.info.assemblyNames[binding] = "result of " + p.label()
			tail = &syntax.Block{Pos: pos, End: g.call.End, Stmts: []syntax.Stmt{binding}, Tail: tail}
			continue
		}
		m := &syntax.Match{Pos: pos, X: call}
		success := &syntax.TypePat{Pos: pos, Name: valueName, Type: g.typeExpr(p.product, pos)}
		c.info.assemblyNames[success] = "result of " + p.label()
		m.Arms = append(m.Arms, &syntax.Arm{Pattern: success, Body: tail})
		for j, failure := range p.errors {
			name := fmt.Sprintf("failure%d_%d", i, j)
			m.Arms = append(m.Arms, &syntax.Arm{Pattern: &syntax.TypePat{Pos: pos, Name: prefix + name, Type: g.typeExpr(failure, pos)}, Body: id(name)})
		}
		tail = m
	}
	block.Tail = tail
	return block
}
