package check

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

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
	NamedArguments       bool                   `json:"named_arguments"`
	ParameterNamesAreAPI bool                   `json:"parameter_names_are_api"`
	Parameters           []ParameterDescription `json:"parameters"`
}

type ParameterDescription struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Receiver bool   `json:"receiver,omitempty"`
	Default  string `json:"default,omitempty"`
}

func DescribeCallable(fn *Func, params []Type, from *Package, bound bool) *CallableDescription {
	out := &CallableDescription{NamedArguments: true, ParameterNamesAreAPI: true, Parameters: []ParameterDescription{}}
	skip := 0
	if bound {
		skip = 1
	}
	for i, p := range fn.Decl.Params[skip:] {
		out.Parameters = append(out.Parameters, ParameterDescription{Name: p.Name, Type: TypeText(params[i], from), Receiver: fn.Decl.IsMethod && !bound && i == 0, Default: defaultText(p.Default)})
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
	case *syntax.Unary:
		return strings.Trim(x.Op.String(), "'") + defaultText(x.X)
	case *syntax.Ident:
		return x.Name
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
	var facts []KnownFact
	seen := map[KnownFact]bool{}
	for _, k := range f.declared(x, at, 0) {
		text := knownText(k, fn.Pkg)
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
