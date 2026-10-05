package check

import (
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// EditorContextVariantUnique applies context selection to a checked scrutinee.
// Invisible variants still count toward ambiguity, as they do in the checker.
func EditorContextVariantUnique(t Type, name string) bool {
	candidates, unresolved := contextCandidates(name, t)
	if unresolved || len(candidates) != 1 {
		return false
	}
	owner, ok := candidates[0].(*Sealed)
	return ok && owner.Variant(name) != nil
}

// MethodDescription is a method as seen from the querying package. Ambiguous
// names are reported separately from callable methods.
type MethodDescription struct {
	Name       string               `json:"name"`
	Type       string               `json:"type,omitempty"`
	Definition *diag.Pos            `json:"definition,omitempty"`
	Ambiguity  string               `json:"ambiguity,omitempty"`
	Requires   []string             `json:"requires,omitempty"`
	Callable   *CallableDescription `json:"callable,omitempty"`
}

// CallableDescription exposes declaration names, which are intentionally
// absent from function types. Renaming a named parameter breaks named callers.
type CallableDescription struct {
	Requires             []string               `json:"requires,omitempty"`
	NamedArguments       bool                   `json:"named_arguments"`
	ParameterNamesAreAPI bool                   `json:"parameter_names_are_api"`
	Parameters           []ParameterDescription `json:"parameters"`
	// Needs lists the ambient values the function reads, as its
	// signature declares them (locale? for an optional one).
	Needs []string `json:"needs,omitempty"`
}

type ParameterDescription struct {
	Doc      string `json:"doc,omitempty"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Receiver bool   `json:"receiver,omitempty"`
	Default  string `json:"default,omitempty"`
}

func DescribeCallable(fn *Func, params []Type, from *Package, bound bool) *CallableDescription {
	out := &CallableDescription{NamedArguments: true, ParameterNamesAreAPI: true, Parameters: []ParameterDescription{}}
	if fn.Requires != nil {
		out.Requires = []string{requirementText(fn.Requires, from)}
	}
	if constructorInvariant(fn) {
		for _, con := range TypeConstraints(fn.Result) {
			out.Requires = append(out.Requires, "completed "+TypeText(fn.Result, from)+" requires "+con.Text(from))
		}
	}
	skip := 0
	if bound {
		skip = 1
	}
	for i, p := range fn.Decl.Params[skip:] {
		out.Parameters = append(out.Parameters, ParameterDescription{Name: p.Name, Type: TypeText(params[i], from), Receiver: fn.Decl.IsMethod && !bound && i == 0, Default: defaultText(p.Default)})
		if fn.Decl.Constructor != nil {
			record := fn.Result.(*Record)
			out.Parameters[len(out.Parameters)-1].Doc = record.Fields[i].Doc
			for _, con := range fn.ParamConstraints[i] {
				out.Requires = append(out.Requires, p.Name+" requires "+con.Text(from))
			}
		}
	}
	for _, n := range fn.Needs {
		name := n.Ambient.QualifiedName(from)
		if n.Optional {
			name += "?"
		}
		out.Needs = append(out.Needs, name)
	}
	return out
}

// Defaults are closed values; render their source syntax rather than a Go value.
func defaultText(x syntax.Expr) string {
	switch x := x.(type) {
	case nil:
		return ""
	case *syntax.IntLit:
		return x.Text
	case *syntax.FloatLit:
		return x.Text
	case *syntax.RuneLit:
		return x.Text
	case *syntax.StringLit:
		return strconv.Quote(x.Value)
	case *syntax.BoolLit:
		return strconv.FormatBool(x.Value)
	case *syntax.Binary:
		return "(" + defaultText(x.X) + " " + strings.Trim(x.Op.String(), "'") + " " + defaultText(x.Y) + ")"
	case *syntax.Unary:
		return strings.Trim(x.Op.String(), "'") + defaultText(x.X)
	case *syntax.Ident:
		return x.Name
	case *syntax.TypeHead:
		var args []string
		for _, a := range x.Type.Args {
			args = append(args, writtenTypeText(a))
		}
		return x.Type.Name + "[" + strings.Join(args, ", ") + "]"
	case *syntax.ContextName:
		return "." + x.Name
	case *syntax.Selector:
		return defaultText(x.X) + "." + x.Name
	case *syntax.ListLit:
		var parts []string
		for _, e := range x.Elems {
			parts = append(parts, defaultText(e))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *syntax.MapLit:
		if len(x.Keys) == 0 {
			return "{:}"
		}
		var parts []string
		for i, k := range x.Keys {
			parts = append(parts, defaultText(k)+": "+defaultText(x.Values[i]))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	case *syntax.RecordLit:
		var parts []string
		for _, f := range x.Fields {
			parts = append(parts, f.Name+": "+defaultText(f.Value))
		}
		return defaultText(x.Type) + " { " + strings.Join(parts, ", ") + " }"
	}
	return ""
}

// VisibleMethods uses the same precedence and visibility rules as a call.
func VisibleMethods(info *Info, from *Package, t Type) []MethodDescription {
	key, ok := methodKey(t)
	if !ok {
		return nil
	}
	c := queryChecker(info, from)
	names := map[string]bool{}
	for _, fn := range info.FuncOf {
		if fn.Decl.IsMethod {
			if k, ok := methodKey(fn.Params[0]); ok && k == key {
				names[fn.Decl.Name] = true
			}
		}
	}
	var out []MethodDescription
	for name := range names {
		if r, ok := t.(*Record); ok && r.Field(name) != nil {
			continue
		}
		fn, why := c.methodNamed(t, name)
		if fn == nil {
			if strings.Contains(why, "is ambiguous:") {
				prefix, places, _ := strings.Cut(why, ": it is declared in ")
				parts := strings.Split(places, " and ")
				sort.Strings(parts)
				why = prefix + ": it is declared in " + strings.Join(parts, " and ")
				out = append(out, MethodDescription{Name: name, Ambiguity: why})
			}
			continue
		}
		in := newInference(fn)
		in.unify(fn.Params[0], t)
		if !assignable(t, in.subst(fn.Params[0])) {
			continue
		}
		ft := &FuncType{Result: in.subst(fn.Result), Effects: fn.Effects}
		for _, p := range fn.Params[1:] {
			ft.Params = append(ft.Params, in.subst(p))
		}
		var requires []string
		if fn.Requires != nil {
			requires = append(requires, requirementText(fn.Requires, from))
		}
		for i, cons := range fn.ParamConstraints {
			for _, con := range cons {
				requires = append(requires, fn.Decl.Params[i].Name+": "+con.Text(from))
			}
		}
		for _, param := range fn.TypeParams {
			for _, bound := range param.Bounds {
				requires = append(requires, TypeText(in.subst(param), from)+": "+qualify(bound.Name, bound.Pkg, from))
			}
		}
		pos := fn.Decl.Pos
		out = append(out, MethodDescription{Name: name, Type: TypeText(ft, from), Definition: &pos, Requires: requires, Callable: DescribeCallable(fn, ft.Params, from, true)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func queryChecker(info *Info, from *Package) *checker {
	c := &checker{info: info, pkg: from, diags: &diag.List{}}
	for _, fn := range info.FuncOf {
		if fn.Prelude {
			c.preludePkg = fn.Pkg
			break
		}
	}
	c.inPrelude = from == c.preludePkg
	return c
}

// KnownFact is a conservative fact from a guard, declaration or promise.
// Path is empty for the whole value, or a field/element path such as .[].
type KnownFact struct {
	Constraint string `json:"constraint"`
	Path       string `json:"path,omitempty"`
}

// Proof answers an explicit where-clause query using the compiler's prover.
type Proof struct {
	Where  string `json:"where"`
	Proven bool   `json:"proven"`
	Reason string `json:"reason,omitempty"`
}

// DescribeFacts uses the same backward proofs as compilation. It deliberately
// enumerates only declared/guarded facts: there is no finite list of all
// predicates and arguments that could be proven about a value.
func DescribeFacts(info *Info, fn *Func, x Expr, site diag.Pos, where string, eval Evaluator) ([]KnownFact, *Proof, error) {
	if fn == nil || fn.Body == nil {
		if where != "" {
			return nil, nil, fmt.Errorf("fact queries need a value inside a bork function or test")
		}
		return nil, nil, nil
	}
	f := &factChecker{info: info, diags: &diag.List{}, paths: map[*Func][]branch{}, active: map[string]bool{}, params: map[*Var]*VarRef{}, predParams: map[*Var]*Func{}, lambdaArgs: map[*Var]lambdaArg{}}
	f.validators = validationContexts(info)
	var at env
	found := false
	f.observe = func(pos diag.Pos, e env) {
		if pos == site && f.fn == fn && !found {
			at, found = e, true
		}
	}
	if fn.MockIn != nil {
		// A mock's body is walked as part of its test.
		f.function(fn.MockIn)
	} else {
		f.function(fn)
	}
	f.observe = nil
	f.fn = fn
	if !found {
		if where != "" {
			return nil, nil, fmt.Errorf("no value to prove a fact about at %s", x.Pos())
		}
		return nil, nil, nil
	}
	facts := describedFacts(f, x, at, fn.Pkg)
	if where == "" {
		return facts, nil, nil
	}
	c := queryChecker(info, fn.Pkg)
	c.fn = fn
	c.useTypeParams(fn)
	parsed := syntax.Parse("<where query>", []byte("fn query(value: Int where "+where+") {}"), c.diags)
	if c.diags.Len() != 0 {
		return nil, nil, fmt.Errorf("invalid where query: %s", c.diags.Error())
	}
	if len(parsed.Funcs) != 1 || len(parsed.Funcs[0].Params) != 1 || len(parsed.Funcs[0].Params[0].Type.Where) == 0 {
		return nil, nil, fmt.Errorf("expected a where clause, such as positive or between(1, 10)")
	}
	scope := map[string]Type{}
	for i, p := range fn.Decl.Params {
		scope[p.Name] = fn.Params[i]
	}
	var constraints []*Constraint
	for _, ref := range parsed.Funcs[0].Params[0].Type.Where {
		con := c.constraint(ref, x.Type(), scope)
		if c.diags.Len() != 0 || con == nil {
			return nil, nil, fmt.Errorf("invalid where query: %s", c.diags.Error())
		}
		constraints = append(constraints, con)
	}
	proof := &Proof{Where: where, Proven: true}
	for _, con := range constraints {
		ob := f.obligationOf(con, f.ownParams(), "")
		ok, pending := f.prove(x, ob, at, 0)
		if !ok {
			proof.Proven = false
			proof.Reason = fmt.Sprintf("%s is not proven for %s%s", con.Text(fn.Pkg), f.describe(x), f.hint(x, ob))
			break
		}
		if len(pending) > 0 {
			results, err := eval(pending)
			if err != nil {
				return nil, nil, fmt.Errorf("cannot evaluate fact query: %w", err)
			}
			if len(results) != len(pending) {
				return nil, nil, fmt.Errorf("fact evaluator returned %d results for %d queries", len(results), len(pending))
			}
			for i, holds := range results {
				if !holds {
					proof.Proven = false
					proof.Reason = pending[i].Text(fn.Pkg) + " is false"
					break
				}
			}
			if !proof.Proven {
				break
			}
		}
	}
	return facts, proof, nil
}

func describedFacts(f *factChecker, x Expr, at env, from *Package) []KnownFact {
	var facts []KnownFact
	seen := map[KnownFact]bool{}
	for _, k := range f.declared(x, at, 0) {
		text := knownText(k, from)
		if text == "" {
			continue
		}
		fact := KnownFact{Constraint: text, Path: k.path}
		if !seen[fact] {
			facts = append(facts, fact)
			seen[fact] = true
		}
	}
	sort.Slice(facts, func(i, j int) bool {
		if facts[i].Path != facts[j].Path {
			return facts[i].Path < facts[j].Path
		}
		return facts[i].Constraint < facts[j].Constraint
	})
	return facts
}

func knownText(k known, from *Package) string {
	if k.or != nil {
		var parts []string
		for _, alt := range k.or {
			text := knownText(alt, from)
			if text == "" {
				return ""
			}
			parts = append(parts, text)
		}
		return strings.Join(parts, " or ")
	}
	if k.pred == nil {
		return ""
	}
	text := k.pred.QualifiedName(from)
	if len(k.args) > 0 {
		var args []string
		for _, arg := range k.args {
			args = append(args, arg.text)
		}
		text += "(" + strings.Join(args, ", ") + ")"
	}
	return text
}

// Reference gives a variable declaration a typed reference for code queries.
func Reference(v *Var) *VarRef {
	return &VarRef{expr: expr{pos: v.Pos, typ: v.Type}, Var: v}
}

// MockTargetRef is a mock's target as a function value, for code
// queries on its name.
func MockTargetRef(m *Mock) *FuncRef {
	t := &FuncType{Params: m.Target.Params, Result: m.Target.Result, Effects: m.Target.Effects}
	inst := &Instance{Func: m.Target, Params: m.Target.Params, Result: m.Target.Result}
	return &FuncRef{expr: expr{pos: m.TargetPos, typ: t, token: m.TargetPos}, Name: m.Text, Inst: inst}
}

func writtenTypeText(t *syntax.TypeExpr) string {
	if t == nil {
		return "Ok"
	}
	out := writtenTypeAtomText(t)
	if t.Uses != nil {
		var names []string
		for _, e := range t.Uses.Effects {
			names = append(names, e.Name)
		}
		if len(names) == 0 {
			names = []string{"nothing"}
		}
		out += " uses " + strings.Join(names, " + ")
	}
	if len(t.Where) > 0 {
		var parts []string
		for _, ref := range t.Where {
			text := writtenPredText(ref)
			if len(ref.Or) > 0 && len(t.Where) > 1 {
				text = "(" + text + ")"
			}
			parts = append(parts, text)
		}
		out += " where " + strings.Join(parts, " and ")
	}
	return out
}
func writtenPredText(ref *syntax.PredRef) string {
	text := ref.Name
	if len(ref.Args) > 0 {
		var args []string
		for _, a := range ref.Args {
			args = append(args, defaultText(a))
		}
		text += "(" + strings.Join(args, ", ") + ")"
	}
	for _, alt := range ref.Or {
		text += " or " + writtenPredText(alt)
	}
	return text
}
func writtenTypeAtomText(t *syntax.TypeExpr) string {
	if t == nil {
		return "Ok"
	}
	if t.Union != nil {
		var parts []string
		for _, m := range t.Union {
			text := writtenTypeText(m)
			if m.Func != nil {
				text = "(" + text + ")"
			}
			parts = append(parts, text)
		}
		return strings.Join(parts, " | ")
	}
	if t.Func != nil {
		var ps []string
		for _, p := range t.Func.Params {
			ps = append(ps, writtenTypeText(p))
		}
		effects := ""
		if t.Func.Uses != nil {
			var names []string
			for _, e := range t.Func.Uses.Effects {
				names = append(names, e.Name)
			}
			if len(names) == 0 {
				names = []string{"nothing"}
			}
			effects = " uses " + strings.Join(names, " + ")
		}
		return "(" + strings.Join(ps, ", ") + ")" + effects + " => " + writtenTypeText(t.Func.Result)
	}
	if len(t.Args) == 0 {
		return t.Name
	}
	var parts []string
	for _, a := range t.Args {
		parts = append(parts, writtenTypeText(a))
	}
	return t.Name + "[" + strings.Join(parts, ", ") + "]"
}

// EditorType resolves written type syntax with the compiler's normal package,
// alias and generic rules. Invalid or inaccessible types return Invalid.
func EditorType(info *Info, from *Package, typ *syntax.TypeExpr) Type {
	c := editorTypeQueryChecker(info, from)
	t := c.resolveType(typ)
	if c.diags.Len() != 0 {
		return Invalid
	}
	return t
}

// EditorCallable resolves a declaration with ordinary package lookup rules.
func EditorCallable(info *Info, from *Package, name string) *CallableDescription {
	fn, ok := queryChecker(info, from).funcNamed(name)
	if !ok {
		return nil
	}
	return DescribeCallable(fn, fn.Params, from, false)
}

// EditorVariantFields uses the same variant lookup and visibility as a
// specialized constructor. It returns declaration fields for an unresolved
// generic head and substituted fields for an explicitly specialized one.
func EditorVariantFields(info *Info, from *Package, owner *syntax.TypeExpr, name string) []*Field {
	c := editorTypeQueryChecker(info, from)
	var typ Type
	if len(owner.Args) == 0 {
		typ = c.typeNamed(owner.Name)
	} else {
		typ = c.resolveType(owner)
	}
	sealed, ok := typ.(*Sealed)
	if !ok {
		return nil
	}
	variant := c.specializedVariant(owner.Pos, sealed, name)
	if variant == nil || c.diags.Len() != 0 {
		return nil
	}
	return variant.Fields
}

// EditorVisibleTypes includes only names available without a package qualifier.
func EditorVisibleTypes(info *Info, from *Package) map[string]Type {
	c := queryChecker(info, from)
	out := maps.Clone(basicTypes)
	for _, pkg := range []*Package{c.preludePkg, from} {
		if pkg == nil {
			continue
		}
		for name, entry := range pkg.types {
			out[name] = c.resolveDecl(entry)
		}
	}
	return out
}

// EditorVisibleVariants applies the compiler's constructor/pattern visibility.
func EditorVisibleVariants(info *Info, from *Package, sealed *Sealed) []*Variant {
	c := queryChecker(info, from)
	var out []*Variant
	for _, v := range sealed.Variants {
		if c.visibleVariant(diag.Pos{}, sealed, v.Name) {
			out = append(out, v)
		}
	}
	return out
}

func editorTypeQueryChecker(info *Info, from *Package) *checker {
	copy := *info
	copy.writtenTypes = maps.Clone(info.writtenTypes)
	copy.typeUses = maps.Clone(info.typeUses)
	copy.sourceDefinitions = maps.Clone(info.sourceDefinitions)
	copy.sourceNames = maps.Clone(info.sourceNames)
	c := queryChecker(&copy, from)
	c.appliedWhere = map[*syntax.TypeExpr]bool{}
	return c
}

// EditorRecordFields also exposes a generic declaration before its literal's
// fields have supplied enough information to infer type arguments.
func EditorRecordFields(info *Info, from *Package, head *syntax.TypeExpr) []*Field {
	c := editorTypeQueryChecker(info, from)
	var typ Type
	if len(head.Args) == 0 {
		typ = c.typeNamed(head.Name)
	} else {
		typ = c.resolveType(head)
	}
	if record, ok := typ.(*Record); ok && c.diags.Len() == 0 {
		return record.Fields
	}
	return nil
}

// EditorRemainingParameters follows the same name/position assignment as calls.
func EditorRemainingParameters(callable *CallableDescription, args []syntax.Argument) []ParameterDescription {
	var names []string
	for _, p := range callable.Parameters {
		names = append(names, p.Name)
	}
	used := map[int]bool{}
	next := 0
	for _, arg := range args {
		index := argumentIndex(names, arg.Name, next)
		if arg.Name == "" {
			next++
		}
		used[index] = true
	}
	var out []ParameterDescription
	for i, p := range callable.Parameters {
		if !used[i] && !p.Receiver {
			out = append(out, p)
		}
	}
	return out
}
