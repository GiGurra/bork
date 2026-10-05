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
	info    *Info
	sources map[string]*syntax.File
	// vars holds the variable each declaring syntax node introduced (a
	// parameter, binding, pattern, or scope block), for the identifiers
	// that refer to it.
	vars      map[any]*Var
	yieldElem Type
	// withs counts the with bindings, to name them apart.
	tuples               int
	withs                int
	interpolationSources map[syntax.Expr]Expr
	interpolationSites   []*InterpolationSite
	// loops are the carried names of the loops being lowered, innermost
	// last, for the breaks and continues in them.
	loops [][]*Carry
	// invariants are the facts the first binding of a carried name
	// declares, for the variables of its later values.
	invariants map[*Var][]*Constraint
	// roots are the typed trees lowered, for the passes over all of them.
	roots []Expr
}

// nameRebinding keeps immutable identities distinct in generated Go.
func (l *lowerer) nameRebinding(v *Var, node any) {
	if l.info.rebindings[node] != nil {
		v.GoName = fmt.Sprintf("_rebind_%d_%d_%s", v.Pos.Line, v.Pos.Col, v.Name)
	}
}

// needs is what the call or reference x passes for its callee's needs.
func (l *lowerer) needs(x syntax.Expr) []Expr {
	var out []Expr
	for _, s := range l.info.needArgs[x] {
		at := expr{pos: x.Position(), typ: s.need.Type}
		if s.decl == nil {
			opt := s.need.Type.(*Sealed)
			out = append(out, &VariantValue{expr: at, Variant: opt.Variant("None"), Text: "Option.None"})
			continue
		}
		v := l.vars[s.decl]
		var value Expr = &VarRef{expr: expr{pos: x.Position(), typ: v.Type}, Var: v}
		if s.some {
			opt := s.need.Type.(*Sealed)
			some := opt.Variant("Some")
			value = &RecordLit{expr: at, Variant: some, Fields: []*FieldValue{{Name: some.Fields[0].Name, Field: some.Fields[0], Value: value}}}
		}
		out = append(out, value)
	}
	return out
}

// needVarName is the Go name of the hidden parameter holding need n of
// fn. An unsafe go body sees it by the ambient's own name, or for
// another package's, with that package's prefix; elsewhere it is a name
// of its own, which cannot hide a Go name the generated code uses.
func needVarName(fn *Func, n *FuncNeed) string {
	name := n.Ambient.Name
	if n.Ambient.Pkg != fn.Pkg {
		name = n.Ambient.Pkg.GoPrefix + name
	}
	if fn.Decl.IsGo() {
		return name
	}
	return "_need_" + name
}

// lower builds the typed tree of every function body, test, and rule.
func (c *checker) lower(files []*syntax.File) {
	l := &lowerer{info: c.info, vars: map[any]*Var{}, sources: map[string]*syntax.File{}}
	for _, f := range files {
		l.sources[f.Path] = f
	}
	for _, binding := range c.info.PackageBindings {
		binding.Var = &Var{Name: binding.Decl.Name, GoName: "_lazy_pkg_" + binding.Pkg.GoPrefix + binding.Decl.Name, Pos: binding.Decl.Pos, Type: binding.Type, Kind: VarLet, PackageBinding: binding}
		l.vars[binding.Decl] = binding.Var
	}
	for field := range c.info.fieldDefaults {
		for i, param := range field.defaultParams {
			v := &Var{Name: param.Name, Pos: param.Pos, Type: field.siblings[i].Type, Kind: VarDefaultField, Index: i, Sibling: field.siblings[i]}
			l.vars[param] = v
			field.DefaultVars = append(field.DefaultVars, v)
		}
	}
	for _, f := range files {
		for _, fd := range f.Funcs {
			if fn := c.info.FuncOf[fd]; fn != nil {
				l.function(fn)
			}
		}
	}
	for _, fn := range c.info.ExpandedFunctions {
		l.function(fn)
	}
	for _, binding := range c.info.PackageBindings {
		binding.Value = l.stmt(binding.Decl).(*Let)
		l.roots = append(l.roots, binding.Value.Value)
		binding.Boundary.Body = &Block{expr: expr{pos: binding.Decl.Pos, typ: binding.Type}, Stmts: []Stmt{binding.Value}, Tail: &VarRef{expr: expr{pos: binding.Decl.Pos, typ: binding.Type}, Var: binding.Var}}
	}
	for _, cl := range c.info.Classes {
		for _, fn := range cl.Methods {
			l.function(fn)
		}
	}
	for _, fn := range c.info.Tests {
		l.function(fn)
	}
	for field, x := range c.info.fieldDefaults {
		field.Default = l.expr(x)
		l.roots = append(l.roots, field.Default)
		if metadata := c.info.lazyFields[x]; metadata != nil {
			c.info.fieldRecipes[field.Default] = metadata
		}
	}
	for _, fn := range c.info.mocks {
		if fn.MockOf.Requires != nil {
			bound := map[*Var]argVal{}
			for i, p := range fn.MockOf.ParamVars {
				bound[p] = argVal{expr: &VarRef{expr: expr{pos: fn.ParamVars[i].Pos, typ: fn.ParamVars[i].Type}, Var: fn.ParamVars[i]}}
			}
			fn.Requires = substituteExpr(fn.MockOf.Requires, bound)
		}
	}
	for _, r := range c.info.Rules {
		l.rule(r)
	}
	l.interpolationBatches()
	c.checkCarried(l.roots)
}

func (l *lowerer) function(fn *Func) {
	for i, p := range fn.Decl.Params {
		v := &Var{Name: p.Name, Pos: p.Pos, Type: fn.Params[i], Kind: VarParam, Index: i}
		if fn.Decl.Constructor != nil {
			v.GoName = "_ctorArg" + strconv.Itoa(i)
		}
		l.vars[p] = v
		if i < len(fn.ParamConstraints) {
			l.setInvariant(v, fn.ParamConstraints[i])
		}
		fn.ParamVars = append(fn.ParamVars, v)
	}
	fn.NeedVars = nil
	for i, n := range fn.Needs {
		v := &Var{Name: n.Decl.Name, GoName: needVarName(fn, n), Pos: n.Decl.Pos, Type: n.Type, Kind: VarAmbient, Index: i, Need: n}
		l.vars[n.Decl] = v
		fn.NeedVars = append(fn.NeedVars, v)
	}
	if fn.Decl.Requires != nil {
		fn.Requires = l.expr(fn.Decl.Requires)
	}
	if fn.Decl.Body != nil {
		fn.Body = l.block(fn.Decl.Body)
		l.roots = append(l.roots, fn.Body)
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
		if tuple, ok := s.(*syntax.TupleBinding); ok {
			out.Stmts = append(out.Stmts, l.tupleBinding(tuple)...)
			continue
		}
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
		if s.AsyncScope != nil && l.info.types[s.AsyncScope] == Never {
			return &ExprStmt{X: l.expr(s.AsyncScope)}
		}
		let := &Let{Pos: s.Pos, Value: l.expr(s.Value), Declared: s.Type != nil, Constraints: l.info.bindingConstraints[s], Carried: l.info.carriedBindings[s] != nil}
		let.Var = &Var{Label: l.info.assemblyNames[s], Name: s.Name, Pos: s.Pos, Type: l.info.bindings[s], Kind: VarLet, Let: let, Unused: l.info.unused[s]}
		l.nameRebinding(let.Var, s)
		if let.Carried {
			l.setInvariant(let.Var, l.invariantOf(l.vars[l.info.rebindings[s]]))
		}
		if global := l.vars[s]; global != nil && global.PackageBinding != nil {
			global.Let = let
			let.Var = global
		}
		if s.Lazy || s.Package || s.AsyncScope != nil {
			let.Deferred = LazyBinding
			pos := s.LazyPos
			if s.Package && !s.Lazy {
				pos = s.Pos
			}
			metadata := l.info.lazyBindings[s]
			if s.AsyncScope != nil {
				let.Deferred = AsyncBinding
				pos = s.AsyncPos
				let.AsyncScope = l.expr(s.AsyncScope)
				let.Async = &AsyncDescription{Scope: writtenText(s.AsyncScope), Effects: metadata.Effects, Captures: metadata.Captures}
			} else {
				let.Lazy = metadata
			}
			let.Initializer = &Lambda{expr: expr{pos: pos, typ: &FuncType{Result: let.Var.Type}}, Body: let.Value}
		}
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
	case *syntax.MockStmt:
		fn := l.info.mocks[s]
		m := &Mock{Pos: s.MockPos, Target: fn.MockOf, Text: writtenText(s.Target), TargetPos: mockTargetPos(s.Target), Func: fn}
		l.function(fn)
		if s.Name != "" {
			m.Var = &Var{Name: s.Name, Pos: s.Pos, Type: l.info.mockHandles[s], Kind: VarMockHandle, Unused: l.info.unused[s]}
			l.nameRebinding(m.Var, s)
			l.vars[s] = m.Var
		}
		return m
	}
	panic(fmt.Sprintf("unhandled statement %T", s))
}

func (l *lowerer) expr(x syntax.Expr) (result Expr) {
	if capture := l.interpolationSources; capture != nil {
		if _, wanted := capture[x]; wanted {
			defer func() { capture[x] = result }()
		}
	}
	if payload := l.info.optionPayloads[x]; payload != nil {
		value := l.exprRaw(x, payload)
		opt := l.info.types[x].(*Sealed)
		some := opt.Variant("Some")
		return &RecordLit{expr: expr{pos: x.Position(), typ: opt, token: sourceTokenPos(x)}, Variant: some, Promoted: true, Fields: []*FieldValue{{Name: some.Fields[0].Name, Field: some.Fields[0], Value: value}}}
	}
	return l.exprRaw(x, l.info.types[x])
}

func (l *lowerer) exprRaw(x syntax.Expr, typ Type) Expr {
	if sel, ok := x.(*syntax.Select); ok {
		out := l.block(l.info.selects[sel])
		out.token = sel.Pos
		return out
	}
	if call, ok := x.(*syntax.Call); ok {
		if expansion := l.info.conversionCalls[call]; expansion != nil {
			out := l.block(expansion)
			end := call.Pos
			end.Col++
			out.Conversion = &SourceSpan{Start: call.Fun.Position(), End: end}
			out.token = call.Fun.Position()
			return out
		}
		if expansion := l.info.assemblyCalls[call]; expansion != nil {
			out := l.block(expansion.body)
			out.Assembly = expansion.description
			out.token = call.Fun.Position()
			return out
		}
	}
	at := expr{pos: x.Position(), typ: typ, token: sourceTokenPos(x)}
	if inst := l.info.funcRefs[x]; inst != nil {
		at.token = x.Position()
		return &FuncRef{expr: at, Name: writtenText(x), Inst: inst, Needs: l.needs(x)}
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
	case *syntax.Comptime:
		node := &Comptime{expr: at, Body: l.block(x.Body), Owner: l.info.exprOwners[x]}
		for _, decl := range l.info.comptimeCaptureDecls[x] {
			node.Captures = append(node.Captures, l.vars[decl])
		}
		l.info.Comptimes = append(l.info.Comptimes, node)
		return node
	case *syntax.Generate:
		saved := l.yieldElem
		l.yieldElem = at.typ.(*Seq).Elem
		body := l.block(x.Body)
		l.yieldElem = saved
		return &Generate{expr: at, Body: body, Constraints: l.info.generateConstraints[x]}
	case *syntax.Yield:
		return &Yield{expr: at, Value: l.expr(x.Value), Elem: l.yieldElem}
	case *syntax.For:
		if x.Items == nil {
			return l.loop(x, &For{expr: at})
		}
		items := l.expr(x.Items)
		var elem Type
		switch t := items.Type().(type) {
		case *List:
			elem = t.Elem
		case *Seq:
			elem = t.Elem
		}
		v := &Var{Name: x.Name, Pos: x.NamePos, Type: elem, Kind: VarLoop, Source: &VarSource{Subject: items, Path: ".[]"}}
		if x.Name == "_" {
			v.GoName = fmt.Sprintf("_discard_loop_%d_%d", x.NamePos.Line, x.NamePos.Col)
		}
		l.vars[x] = v
		return l.loop(x, &For{expr: at, Var: v, Items: items})
	case *syntax.LoopControl:
		return l.loopControl(x, at)
	case *syntax.StaticPartsLit:
		rec := typ.(*Record)
		list := &ListLit{expr: expr{pos: x.Pos, typ: &List{Elem: String}}}
		for _, part := range x.Parts {
			list.Elems = append(list.Elems, &Const{expr: expr{pos: x.Pos, typ: String}, Value: constant.MakeString(part)})
		}
		return &RecordLit{expr: at, Record: rec, Fields: []*FieldValue{{Name: "values", Field: rec.Fields[0], Value: list}}}
	case *syntax.Interp:
		if call := l.info.interpolatorCalls[x]; call != nil {
			factory := call
			for {
				selector, ok := factory.Fun.(*syntax.Selector)
				if !ok {
					break
				}
				factory = selector.X.(*syntax.Call)
			}
			capture := map[syntax.Expr]Expr{factory: nil}
			for _, hole := range x.Exprs {
				capture[hole] = nil
			}
			outer := l.interpolationSources
			l.interpolationSources = capture
			result := l.expr(call)
			l.interpolationSources = outer
			source := &InterpolationSource{Prefix: SourceSpan{Start: x.Prefix.Position(), End: x.PrefixEnd}, Factory: capture[factory]}
			for _, hole := range x.Exprs {
				source.Holes = append(source.Holes, capture[hole])
			}
			if l.info.Interpolations == nil {
				l.info.Interpolations = map[Expr]*InterpolationSource{}
			}
			l.info.Interpolations[result] = source
			if dict := l.info.interpolatorValidators[x]; dict != nil {
				var parts Expr
				switch call := capture[factory].(type) {
				case *Call:
					parts = call.Args[0]
				case *CallValue:
					parts = call.Args[0]
				}
				l.interpolationSite(x, dict, parts)
			}
			return result
		}
		return &Interp{expr: at, Parts: x.Parts, Exprs: l.exprs(x.Exprs)}
	case *syntax.Ident:
		if at.typ == Ok && l.info.defs[x] == nil {
			return &Block{expr: at}
		}
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
		if literal := l.info.variantCalls[x]; literal != nil {
			out := l.expr(literal).(*RecordLit)
			out.SourceCall = x
			return out
		}
		if project := l.info.shapeProjects[x]; project != nil {
			return l.shapeProjection(x, at, project)
		}
		if assertion := l.info.patternAssertions[x]; assertion != nil {
			return l.assertIs(x, at, assertion)
		}
		if field := l.info.shapeDefaults[x]; field != nil {
			// Reuse the default's checked lexical identities and contextual
			// types. Each call evaluates this expression at runtime.
			return &Block{expr: at, Tail: l.expr(l.info.fieldDefaults[field])}
		}
		if call := l.info.seqCalls[x]; call != nil {
			args := l.exprs(call.args)
			if len(args) > 1 {
				index := 1
				param := 0
				if call.op == "fold" {
					index = 2
					param = 1
				}
				if index < len(args) {
					if callback, ok := args[index].(*Lambda); ok && param < len(callback.Params) {
						callback.Params[param].Source = &VarSource{Subject: args[0], Path: ".[]"}
					}
				}
			}
			return &SeqCall{expr: at, Op: call.op, Args: args, Effects: call.effects}
		}
		if fn := l.info.callFuncs[x]; fn != nil {
			call := &Call{expr: at, Func: fn, Inst: l.info.instances[x], Args: l.exprs(l.info.args(x)), ArgOrder: l.info.callOrder[x], Embedded: l.info.embedCalls[x], BuildRead: l.info.buildCalls[x], Needs: l.needs(x)}
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
			call := &CallBuiltin{expr: at, Builtin: b, Name: x.Fun.(*syntax.Ident).Name, Args: l.exprs(x.Args), Conv: l.info.conversions[x]}
			if b == BuiltinDbg && x.Pipe.File != "" {
				call.DebugText = sourceText(l.sources[x.PipeStart.File], x.PipeStart, x.PipeEnd)
			} else if b == BuiltinDbg && len(x.Arguments) == 1 {
				arg := x.Arguments[0]
				call.DebugText = sourceText(l.sources[arg.Pos.File], arg.Pos, arg.End)
			}
			return call
		}
		at.token = x.Pos
		return &CallValue{expr: at, Fun: l.expr(x.Fun), Args: l.exprs(x.Args), Provider: l.info.assemblyValueCalls[x]}
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
	case *syntax.TupleLit:
		rec := at.typ.(*Record)
		out := &RecordLit{expr: at, Record: rec}
		for i, elem := range x.Elems {
			field := rec.Fields[i]
			out.Fields = append(out.Fields, &FieldValue{Name: field.Name, Field: field, Value: l.expr(elem)})
		}
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
		out.Joins = l.joins(x, func(i int) *Block {
			if i == 0 {
				return out.Then
			}
			b := branchBlock(out.Else)
			out.Else = b
			return b
		})
		return out
	case *syntax.Block:
		out := l.block(x)
		out.Joins = l.joins(x, func(int) *Block { return out })
		return out
	case *syntax.WithExpr:
		// The bindings are lets of a block around the body, under names
		// of their own: an inner with may bind the same value again.
		out := &Block{expr: at, End: x.Body.End}
		for _, b := range x.Bindings {
			l.withs++
			a := l.info.withAmbients[b]
			if a == nil {
				continue
			}
			// The ambient's facts are the binding's: the value must
			// have them.
			let := &Let{Pos: b.Pos, Value: l.expr(b.Value), Declared: true, Constraints: a.Constraints}
			let.Var = &Var{Name: b.Name, GoName: fmt.Sprintf("_with%d_%s", l.withs, strings.ReplaceAll(b.Name, ".", "_")), Pos: b.Pos, Type: a.Type, Kind: VarLet, Let: let, Ambient: a, Unused: l.info.unused[b]}
			l.vars[b] = let.Var
			out.Stmts = append(out.Stmts, let)
			if a.Marked() {
				out.Labels = append(out.Labels, let.Var)
			}
		}
		out.Tail = l.block(x.Body)
		return out
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
		if read := l.info.shapeViewReads[x]; read != nil {
			return l.shapeViewRead(x, at, read)
		}
		if v := l.info.selectorVariants[x]; v != nil {
			return &VariantValue{expr: at, Variant: v, Text: writtenText(x), Head: l.constructorHead(x.X), Constraints: l.info.constructorConstraints[x]}
		}
		if fn := l.info.ownerScopes[x]; fn != nil {
			return &Call{expr: at, Func: fn, Inst: &Instance{Func: fn, Params: fn.Params, Result: fn.Result}, Args: []Expr{l.expr(x.X)}, ReceiverCall: true}
		}
		out := &Select{expr: at, X: l.expr(x.X), Name: x.Name}
		if rec, ok := out.X.Type().(*Record); ok {
			out.Field = rec.Field(x.Name)
		}
		return out
	case *syntax.ContextName:
		v := l.info.contextVariants[x]
		return &VariantValue{expr: at, Variant: v, Text: "." + x.Name}
	case *syntax.RecordLit:
		out := &RecordLit{expr: at, Head: l.constructorHead(x.Type), Constraints: l.info.constructorConstraints[x]}
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
			field := findField(fields, fi.Name)
			value := &FieldValue{IsDefault: !given[fi.Name], Name: fi.Name, Field: field, Value: l.expr(fi.Value)}
			if field != nil && field.Lazy {
				value.Thunk = &Lambda{expr: expr{pos: fi.Value.Position(), typ: &FuncType{Result: field.Type}}, Body: value.Value}
				value.Lazy = l.info.lazyFields[fi.Value]
				l.info.fieldRecipes[value.Value] = value.Lazy
			}
			out.Fields = append(out.Fields, value)
		}
		for _, value := range out.Fields {
			if !value.Field.Computed {
				continue
			}
			if out.Candidate == nil {
				out.Candidate = completedCandidate(out)
			}
			root := &VarRef{expr: at, Var: out.Candidate}
			value.Value = computedRecipe(l.info, value.Field, value.Value, root)
			value.Thunk.Body = value.Value
			l.info.fieldRecipes[value.Value] = value.Lazy
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
			if fu.Field != nil && fu.Field.Lazy {
				fu.Thunk = &Lambda{expr: expr{pos: u.Value.Position(), typ: &FuncType{Result: fu.Field.Type}}, Body: fu.Value}
				fu.Lazy = l.info.lazyFields[u.Value]
				l.info.fieldRecipes[fu.Value] = fu.Lazy
			}
			out.Updates = append(out.Updates, fu)
		}
		l.refreshComputedCopy(out)
		return out
	case *syntax.Is:
		subject := l.expr(x.X)
		pat := l.info.patternTests[x]
		l.patVars(pat, subject)
		var validation []Expr
		if pat.Kind == PatNever {
			validation = pat.Guards()
			pat = &Pat{Kind: PatNever, Type: subject.Type()}
		}
		return &Match{expr: at, PatternTest: true, ValidationGuards: validation, X: subject, Arms: []*MatchArm{
			{Pat: pat, Body: &Const{expr: expr{pos: x.Pos, typ: Bool}, Value: constant.MakeBool(true)}},
			{Pat: &Pat{Kind: PatWild, Type: subject.Type()}, Body: &Const{expr: expr{pos: x.Pos, typ: Bool}, Value: constant.MakeBool(false)}},
		}}
	case *syntax.Match:
		out := &Match{expr: at, X: l.expr(x.X)}
		for _, arm := range x.Arms {
			pat := l.info.armPats[arm]
			l.patVars(pat, out.X)
			out.Arms = append(out.Arms, &MatchArm{Pat: pat, Body: l.expr(arm.Body)})
		}
		out.Joins = l.joins(x, func(i int) *Block {
			b := branchBlock(out.Arms[i].Body)
			out.Arms[i].Body = b
			return b
		})
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
		v := &Var{Label: l.info.assemblyNames[p.bindNode], Name: p.Bind, Pos: bindPos(p.bindNode), Type: p.BindType, Kind: VarPattern, Unused: l.info.unused[p.bindNode] || p.Bind == "_"}
		if src := l.info.patSources[p.bindNode]; src != nil {
			v.Source = &VarSource{Subject: subject, Member: src.Member, Path: src.Path, Field: src.Field}
		}
		if p.Bind == "_" {
			v.GoName = fmt.Sprintf("_discard_%d_%d", v.Pos.Line, v.Pos.Col)
		}
		l.nameRebinding(v, p.bindNode)
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
	case *syntax.TypeHead:
		return defaultText(x)
	case *syntax.Selector:
		return writtenText(x.X) + "." + x.Name
	case *syntax.TupleLit:
		elems := make([]string, len(x.Elems))
		for i, elem := range x.Elems {
			elems[i] = writtenText(elem)
		}
		text := strings.Join(elems, ", ")
		if len(elems) == 1 {
			text += ","
		}
		return "(" + text + ")"
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

// mockTargetPos is where a mock's target starts: its first name.
func mockTargetPos(x syntax.Expr) diag.Pos {
	if sel, ok := x.(*syntax.Selector); ok {
		return mockTargetPos(sel.X)
	}
	return x.Position()
}

func (l *lowerer) constructorHead(x syntax.Expr) *ConstructorHead {
	if sel, ok := x.(*syntax.Selector); ok {
		x = sel.X
	}
	h, ok := x.(*syntax.TypeHead)
	if !ok {
		return nil
	}
	out := &ConstructorHead{Start: h.Position(), End: h.End}
	var visit func(*syntax.TypeExpr)
	visit = func(t *syntax.TypeExpr) {
		typ := l.info.writtenTypes[t]
		if t.Name != "" {
			var pos diag.Pos
			switch v := typ.(type) {
			case *Record:
				pos = v.Decl.Pos
			case *Sealed:
				pos = v.Decl.Pos
			case *Opaque:
				if v.Decl != nil {
					pos = v.Decl.Pos
				}
			case *Resource:
				if v.Decl != nil {
					pos = v.Decl.Pos
				}
			case *TypeParam:
				if v.Decl != nil {
					pos = v.Decl.Pos
				}
			}
			ref := TypeReference{Pos: t.Pos, Name: t.Name, Type: typ}
			if pos.File != "" {
				ref.Definition = &pos
			}
			out.Uses = append(out.Uses, ref)
		}
		for _, a := range t.Args {
			visit(a)
		}
		for _, a := range t.Union {
			visit(a)
		}
		if t.Func != nil {
			for _, a := range t.Func.Params {
				visit(a)
			}
			if t.Func.Result != nil {
				visit(t.Func.Result)
			}
		}
	}
	visit(h.Type)
	return out
}

func (l *lowerer) assertIs(source *syntax.Call, at expr, assertion *assertIsInfo) Expr {
	subject := l.expr(l.info.callArgs[source][0])
	pat := assertion.Pattern
	actual := &Var{Name: "_assertActual", Type: subject.Type(), Kind: VarPattern, Pos: source.Pos}
	failure := &CallBuiltin{expr: expr{pos: source.Pos, typ: Never}, Builtin: BuiltinAssertIsFailure, Name: "test.AssertIs", Expected: assertion.Expected, Args: []Expr{&VarRef{expr: expr{pos: source.Pos, typ: subject.Type()}, Var: actual}}}
	fallback := &MatchArm{Pat: &Pat{Kind: PatWild, Type: subject.Type(), Bind: actual.Name, BindType: actual.Type, Var: actual}, Body: failure}
	result := &Match{expr: at, PatternTest: true, X: subject, Assertion: l.info.instances[source], SourceCall: source}
	l.patVars(pat, subject)
	if pat.Kind == PatNever {
		result.ValidationGuards = pat.Guards()
	} else {
		result.Arms = append(result.Arms, &MatchArm{Pat: pat, Body: &VarRef{expr: expr{pos: source.Pos, typ: assertion.Target}, Var: pat.Var}})
	}
	result.Arms = append(result.Arms, fallback)
	return result
}

// loop lowers `for { }`, `for (cond) { }`, and `for (init; cond; post)
// { }`. A header name is one variable for every iteration, without the
// initial value's identity: later iterations have other values.
