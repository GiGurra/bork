package check

import (
	"fmt"
	"go/constant"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// lowerer builds the typed tree (see tree.go) from the syntax and what
// the checker recorded about it.
type lowerer struct {
	info *Info
	// vars holds the variable each declaring syntax node introduced (a
	// parameter, binding, pattern, or scope block), for the identifiers
	// that refer to it.
	vars map[any]*Var
}

// lower builds the typed tree of every function body, test, and rule.
func (c *checker) lower(files []*syntax.File) {
	l := &lowerer{info: c.info, vars: map[any]*Var{}}
	for _, f := range files {
		for _, fd := range f.Funcs {
			if fn := c.info.FuncOf[fd]; fn != nil {
				l.function(fn)
			}
		}
	}
	for _, fn := range c.info.Tests {
		l.function(fn)
	}
	for field, x := range c.info.fieldDefaults {
		field.Default = l.expr(x)
	}
	for _, r := range c.info.Rules {
		l.rule(r)
	}
}

func (l *lowerer) function(fn *Func) {
	for i, p := range fn.Decl.Params {
		v := &Var{Name: p.Name, Pos: p.Pos, Type: fn.Params[i], Kind: VarParam, Index: i}
		l.vars[p] = v
		fn.ParamVars = append(fn.ParamVars, v)
	}
	if fn.Decl.Body != nil {
		fn.Body = l.block(fn.Decl.Body)
	}
}

func (l *lowerer) rule(r *Rule) {
	for i, p := range r.Decl.Params {
		v := &Var{Name: p.Name, Pos: p.Pos, Type: r.VarTypes[i], Kind: VarParam, Index: i}
		l.vars[p] = v
		r.Vars = append(r.Vars, v)
	}
	lowered := map[syntax.Expr]Expr{}
	for _, x := range r.Decl.Premises {
		lowered[x] = l.expr(x)
		r.PremiseExprs = append(r.PremiseExprs, lowered[x])
	}
	for _, x := range r.conditions {
		r.Conditions = append(r.Conditions, lowered[x])
	}
	for _, x := range r.Decl.Conclusions {
		r.ConclusionExprs = append(r.ConclusionExprs, l.expr(x))
		r.ConclusionTexts = append(r.ConclusionTexts, writtenText(x))
	}
}

func (l *lowerer) exprs(xs []syntax.Expr) []Expr {
	out := make([]Expr, len(xs))
	for i, x := range xs {
		out[i] = l.expr(x)
	}
	return out
}

func (l *lowerer) block(b *syntax.Block) *Block {
	out := &Block{expr: expr{pos: b.Pos, typ: l.info.types[b]}, End: b.End}
	for _, s := range b.Stmts {
		out.Stmts = append(out.Stmts, l.stmt(s))
	}
	if b.Tail != nil {
		out.Tail = l.expr(b.Tail)
	}
	return out
}

func (l *lowerer) stmt(s syntax.Stmt) Stmt {
	switch s := s.(type) {
	case *syntax.Binding:
		let := &Let{Pos: s.Pos, Value: l.expr(s.Value), Declared: s.Type != nil, Constraints: l.info.bindingConstraints[s]}
		let.Var = &Var{Name: s.Name, Pos: s.Pos, Type: l.info.bindings[s], Kind: VarLet, Let: let, Unused: l.info.unused[s]}
		l.vars[s] = let.Var
		return let
	case *syntax.TrustStmt:
		t := &Trust{Pos: s.Pos, Call: l.expr(s.Call), Text: writtenText(s.Call)}
		if args := l.info.args(s.Call); len(args) > 0 {
			t.SubjectText = writtenText(args[0])
		}
		return t
	case *syntax.ExprStmt:
		return &ExprStmt{X: l.expr(s.X)}
	}
	panic(fmt.Sprintf("unhandled statement %T", s))
}

func (l *lowerer) expr(x syntax.Expr) Expr {
	at := expr{pos: x.Position(), typ: l.info.types[x], token: sourceTokenPos(x)}
	if inst := l.info.funcRefs[x]; inst != nil {
		at.token = x.Position()
		return &FuncRef{expr: at, Name: writtenText(x), Inst: inst}
	}
	if v := l.info.constantOf(x); v != nil {
		var span *SourceSpan
		switch x.(type) {
		case *syntax.Unary, *syntax.Binary:
			start, end := constantSpan(x)
			span = &SourceSpan{Start: start, End: end}
		}
		return &Const{expr: at, Value: v, SourceSpan: span}
	}
	switch x := x.(type) {
	case *syntax.Interp:
		return &Interp{expr: at, Parts: x.Parts, Exprs: l.exprs(x.Exprs)}
	case *syntax.Ident:
		v := l.vars[l.info.defs[x]]
		if v == nil {
			panic(fmt.Sprintf("%s: %s refers to no variable (compiler bug)", x.Pos, x.Name))
		}
		return &VarRef{expr: at, Var: v}
	case *syntax.Unary:
		return &Unary{expr: at, Op: x.Op, X: l.expr(x.X)}
	case *syntax.Binary:
		return &Binary{expr: at, Op: x.Op, X: l.expr(x.X), Y: l.expr(x.Y)}
	case *syntax.Call:
		if fn := l.info.callFuncs[x]; fn != nil {
			call := &Call{expr: at, Func: fn, Inst: l.info.instances[x], Args: l.exprs(l.info.args(x)), ArgOrder: l.info.callOrder[x], Embedded: l.info.embedCalls[x]}
			if sel, ok := x.Fun.(*syntax.Selector); ok {
				args := l.info.args(x)
				call.ReceiverCall = len(args) > 0 && args[0] == sel.X
			}
			offset := 0
			if call.ReceiverCall {
				offset = 1
			}
			for i, arg := range x.Arguments {
				if arg.Name != "" {
					call.Labels = append(call.Labels, ArgumentLabel{Name: arg.Name, Pos: arg.Pos, Param: call.ArgOrder[i+offset]})
				}
			}
			for _, ta := range l.info.callTypeArgs[x] {
				name := ""
				if ta != nil && ta.Name != "" && len(ta.Args) == 0 && len(ta.Where) == 0 {
					name = ta.Name
				}
				call.TypeArgNames = append(call.TypeArgNames, name)
			}
			return call
		}
		if b := l.info.callBuiltins[x]; b != BuiltinNone {
			return &CallBuiltin{expr: at, Builtin: b, Name: x.Fun.(*syntax.Ident).Name, Args: l.exprs(x.Args), Conv: l.info.conversions[x]}
		}
		at.token = x.Pos
		return &CallValue{expr: at, Fun: l.expr(x.Fun), Args: l.exprs(x.Args)}
	case *syntax.Lambda:
		ft := at.typ.(*FuncType)
		out := &Lambda{expr: at}
		for i, p := range x.Params {
			v := &Var{Name: p.Name, Pos: p.Pos, Type: ft.Params[i], Kind: VarLambdaParam, Index: i}
			l.vars[p] = v
			out.Params = append(out.Params, v)
		}
		out.Body = l.expr(x.Body)
		return out
	case *syntax.ListLit:
		return &ListLit{expr: at, Elems: l.exprs(x.Elems)}
	case *syntax.MapLit:
		return &MapLit{expr: at, Keys: l.exprs(x.Keys), Values: l.exprs(x.Values)}
	case *syntax.If:
		out := &If{expr: at, Cond: l.expr(x.Cond), Then: l.block(x.Then)}
		if x.Else != nil {
			out.Else = l.expr(x.Else)
		}
		return out
	case *syntax.Block:
		return l.block(x)
	case *syntax.ScopeExpr:
		v := &Var{Name: x.Name, Pos: x.Pos, Type: Scope, Kind: VarScope}
		l.vars[x] = v
		return &ScopeBlock{expr: at, Var: v, Policies: l.exprs(x.Policies), Body: l.block(x.Body)}
	case *syntax.Return:
		out := &Return{expr: at}
		if x.Value != nil {
			out.Value = l.expr(x.Value)
		}
		return out
	case *syntax.Selector:
		if v := l.info.selectorVariants[x]; v != nil {
			return &VariantValue{expr: at, Variant: v, Text: writtenText(x)}
		}
		out := &Select{expr: at, X: l.expr(x.X), Name: x.Name}
		if rec, ok := out.X.Type().(*Record); ok {
			out.Field = rec.Field(x.Name)
		}
		return out
	case *syntax.RecordLit:
		out := &RecordLit{expr: at}
		var fields []*Field
		switch t := l.info.recordTargets[x].(type) {
		case *Record:
			out.Record, fields = t, t.Fields
		case *Variant:
			out.Variant, fields = t, t.Fields
		}
		inits := l.info.recordInits[x]
		if inits == nil {
			inits = x.Fields
		}
		given := map[string]bool{}
		for _, fi := range x.Fields {
			given[fi.Name] = true
		}
		for _, fi := range inits {
			out.Fields = append(out.Fields, &FieldValue{IsDefault: !given[fi.Name], Name: fi.Name, Field: findField(fields, fi.Name), Value: l.expr(fi.Value)})
		}
		return out
	case *syntax.Copy:
		out := &Copy{expr: at, X: l.expr(x.X)}
		rec, _ := out.X.Type().(*Record)
		for _, u := range x.Updates {
			fu := &FieldUpdate{Path: u.Path, Value: l.expr(u.Value)}
			cur := rec
			for _, name := range u.Path {
				if cur == nil {
					fu.Field = nil
					break
				}
				if fu.Field = cur.Field(name); fu.Field == nil {
					break
				}
				cur, _ = fu.Field.Type.(*Record)
			}
			out.Updates = append(out.Updates, fu)
		}
		return out
	case *syntax.Match:
		out := &Match{expr: at, X: l.expr(x.X)}
		for _, arm := range x.Arms {
			pat := l.info.armPats[arm]
			l.patVars(pat, out.X)
			out.Arms = append(out.Arms, &MatchArm{Pat: pat, Body: l.expr(arm.Body)})
		}
		return out
	case *syntax.Try:
		out := &Try{expr: at, X: l.expr(x.X)}
		if info := l.info.tries[x]; info != nil {
			out.TryInfo = *info
		}
		return out
	}
	panic(fmt.Sprintf("unhandled expression %T", x))
}

func sourceTokenPos(x syntax.Expr) diag.Pos {
	switch x := x.(type) {
	case *syntax.Unary:
		return x.Pos
	case *syntax.Binary:
		return x.Pos
	case *syntax.Call:
		if sel, ok := x.Fun.(*syntax.Selector); ok {
			return sel.Pos
		}
		return x.Pos
	case *syntax.Selector:
		return x.Pos
	case *syntax.RecordLit:
		return sourceTokenPos(x.Type)
	case *syntax.Copy:
		return x.Pos
	case *syntax.Try:
		return x.Pos
	}
	return x.Position()
}

func constantSpan(x syntax.Expr) (diag.Pos, diag.Pos) {
	start, end := x.Position(), x.Position()
	switch x := x.(type) {
	case *syntax.IntLit:
		end.Col += len(x.Text)
	case *syntax.FloatLit:
		end.Col += len(x.Text)
	case *syntax.RuneLit:
		end.Col += len(x.Text)
	case *syntax.Unary:
		_, end = constantSpan(x.X)
	case *syntax.Binary:
		start, _ = constantSpan(x.X)
		_, end = constantSpan(x.Y)
	}
	return start, end
}

// patVars makes variables of the names pat binds, from the value
// subject it matches.
func (l *lowerer) patVars(p *Pat, subject Expr) {
	if p == nil {
		return
	}
	if p.Bind != "" {
		v := &Var{Name: p.Bind, Pos: bindPos(p.bindNode), Type: p.BindType, Kind: VarPattern, Unused: l.info.unused[p.bindNode]}
		if src := l.info.patSources[p.bindNode]; src != nil {
			v.Source = &VarSource{Subject: subject, Member: src.Member, Path: src.Path, Field: src.Field}
		}
		l.vars[p.bindNode] = v
		p.Var = v
	}
	for _, f := range p.Fields {
		l.patVars(f.Pat, subject)
	}
	for _, e := range p.Elems {
		l.patVars(e, subject)
	}
	l.patVars(p.Rest, subject)
	l.patVars(p.Sub, subject)
	if p.guard != nil {
		p.Guard = l.expr(p.guard)
	}
}

// bindPos is where the pattern node that binds a name names it.
func bindPos(node any) diag.Pos {
	switch n := node.(type) {
	case *syntax.ListPat:
		return n.RestPos
	case *syntax.FieldPat:
		return n.Pos
	case syntax.Pattern:
		return n.Position()
	}
	return diag.Pos{}
}

// writtenText shows a simple expression as written, for messages.
func writtenText(x syntax.Expr) string {
	switch x := x.(type) {
	case *syntax.Ident:
		return x.Name
	case *syntax.Selector:
		return writtenText(x.X) + "." + x.Name
	case *syntax.Call:
		args := make([]string, len(x.Args))
		for i, a := range x.Args {
			args[i] = writtenText(a)
		}
		return writtenText(x.Fun) + "(" + strings.Join(args, ", ") + ")"
	case *syntax.StringLit:
		return strconv.Quote(x.Value)
	case *syntax.IntLit:
		return x.Text
	}
	return "..."
}

// constantOf is the value of a constant expression, or nil.
func (info *Info) constantOf(x syntax.Expr) constant.Value {
	if v, ok := info.consts[x]; ok {
		return v
	}
	switch x := x.(type) {
	case *syntax.StringLit:
		return constant.MakeString(x.Value)
	case *syntax.BoolLit:
		return constant.MakeBool(x.Value)
	}
	return nil
}
