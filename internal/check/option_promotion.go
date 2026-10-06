package check

import (
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// Only a closed, exact prelude Option supplies promotion context. In
// particular, finding an Option somewhere inside a union is insufficient.
func (c *checker) promotionTarget(want Type) *Sealed {
	if p, ok := want.(*TypeParam); ok && p.unknown {
		want = c.zonk(want)
	}
	if !IsOption(want) {
		return nil
	}
	want = c.zonk(want)
	if !c.open(want) {
		return want.(*Sealed)
	}
	return nil
}

func ordinaryPayload(t Type) bool {
	if t == nil || t == Invalid || t == Never || IsOption(t) {
		return false
	}
	switch t := t.(type) {
	case *TypeParam:
		return false // even a rigid parameter can specialize to Option
	case *Union:
		for _, m := range t.Members {
			if !ordinaryPayload(m) {
				return false
			}
		}
	}
	return true
}

// Declared optional and bare generic results retain outer context. Other
// result heads can use payload context without speculative checking.
func (c *checker) optionResultWant(want, result Type) Type {
	if opt := c.promotionTarget(want); opt != nil && ordinaryPayload(result) {
		return opt.Args[0]
	}
	return want
}

func (c *checker) optionalConstructor(x syntax.Expr) bool {
	switch x := x.(type) {
	case *syntax.Call:
		return c.optionalConstructor(x.Fun)
	case *syntax.RecordLit:
		return c.optionalConstructor(x.Type)
	case *syntax.ContextName:
		return x.Name == "Some" || x.Name == "None"
	case *syntax.Ident:
		t := c.typeNamed(x.Name)
		s, ok := t.(*Sealed)
		return ok && s.Prelude && s.Name == "Option"
	case *syntax.TypeHead:
		return c.optionalConstructor(&syntax.Ident{Name: x.Type.Name})
	case *syntax.Selector:
		return c.optionalConstructor(x.X)
	}
	return false
}

func (c *checker) promotionContext(x syntax.Expr, want Type) Type {
	opt := c.promotionTarget(want)
	if opt == nil || c.optionalConstructor(x) {
		return want
	}
	switch x.(type) {
	case *syntax.Block, *syntax.If, *syntax.Match, *syntax.ScopeExpr, *syntax.WithExpr, *syntax.Call:
		// Structural expressions forward the optional expectation. Calls
		// select result context from their declaration, in inferCall.
		return want
	}
	return opt.Args[0]
}

func (c *checker) exprWant(e syntax.Expr, want Type) Type {
	opt := c.promotionTarget(want)
	t := c.exprWantRaw(e, c.promotionContext(e, want))
	if opt == nil {
		return t
	}
	t = c.zonk(t)
	if ordinaryPayload(t) && assignable(t, opt.Args[0]) {
		c.info.optionPayloads[e] = t
		return c.record(e, opt)
	}
	return t
}

// An already optional argument can still supply ordinary generic inference.
// This inspection uses existing declarations and never checks the value twice.
func (c *checker) knownOptionalSource(x syntax.Expr) bool {
	return IsOption(c.peekValueType(x))
}

func (c *checker) peekValueType(x syntax.Expr) Type {
	return c.peekValueTypeIn(x, nil)
}

func (c *checker) peekValueTypeIn(x syntax.Expr, locals map[string]Type) Type {
	x = debugSyntaxValue(x)
	switch x := x.(type) {
	case *syntax.ContextName:
		if x.Name == "Some" || x.Name == "None" {
			if base, ok := c.preludePkg.TypeNamed("Option").(*Sealed); ok {
				return instantiate(base, []Type{&TypeParam{Name: "T"}})
			}
		}
	case *syntax.IntLit:
		return Int
	case *syntax.FloatLit:
		return Float
	case *syntax.StringLit:
		return String
	case *syntax.BoolLit:
		return Bool
	case *syntax.Return, *syntax.LoopControl:
		return Never
	case *syntax.Ident:
		if t := locals[x.Name]; t != nil {
			return t
		}
		if v := c.lookup(x.Name); v != nil {
			return c.zonk(v.typ)
		}
		if a := c.ambientNamed(x.Name); a != nil {
			return a.Type
		}
		if fn, ok := c.funcNamed(x.Name); ok {
			return fn.funcType()
		}
	case *syntax.TypeHead:
		return c.peekType(x.Type)
	case *syntax.RecordLit:
		base := c.peekValueTypeIn(x.Type, locals)
		if base == nil {
			if id, ok := x.Type.(*syntax.Ident); ok {
				base = c.typeNamed(id.Name)
			}
		}
		if base != nil && len(typeParamsOf(base)) > 0 {
			in := typeInference(typeParamsOf(base))
			var fields []*Field
			switch b := base.(type) {
			case *Record:
				fields = b.Fields
			case *Sealed:
				if sel, ok := x.Type.(*syntax.Selector); ok {
					if v := b.Variant(sel.Name); v != nil {
						fields = v.Fields
					}
				}
			}
			for _, fi := range x.Fields {
				if f := findField(fields, fi.Name); f != nil {
					in.unify(f.Type, c.peekValueTypeIn(fi.Value, locals))
				}
			}
			args := in.args()
			for i, a := range args {
				if a == nil {
					args[i] = in.params[i]
				}
			}
			return instantiate(base, args)
		}
		return base
	case *syntax.Selector:
		if head, ok := x.X.(*syntax.Ident); ok {
			if s, ok := c.typeNamed(head.Name).(*Sealed); ok {
				return s
			}
		}
		if head, ok := x.X.(*syntax.TypeHead); ok {
			return c.peekType(head.Type)
		}
		if rec, ok := c.peekValueTypeIn(x.X, locals).(*Record); ok {
			if field := rec.Field(x.Name); field != nil {
				return field.Type
			}
		}
	case *syntax.Copy:
		return c.peekValueTypeIn(x.X, locals)
	case *syntax.Try:
		if opt, ok := c.peekValueTypeIn(x.X, locals).(*Sealed); ok && IsOption(opt) {
			return opt.Args[0]
		}
	case *syntax.Call:
		if head, ok := x.Fun.(*syntax.Selector); ok {
			if owner, ok := c.peekValueTypeIn(head, locals).(*Sealed); ok {
				if variant := owner.Variant(head.Name); variant != nil && variant.Positional {
					literal := &syntax.RecordLit{Type: head, Positional: true}
					for i, arg := range x.Args {
						literal.Fields = append(literal.Fields, &syntax.FieldInit{Name: strconv.Itoa(i), Value: arg})
					}
					return c.peekValueTypeIn(literal, locals)
				}
			}
		}
		if id, ok := x.Fun.(*syntax.Ident); ok && c.lookup(id.Name) == nil && locals[id.Name] == nil {
			if b, ok := builtins[id.Name]; ok && (b == BuiltinPanic || b == BuiltinTodo) {
				return Never
			}
		}
		if id, ok := x.Fun.(*syntax.Ident); ok && locals[id.Name] == nil && c.lookup(id.Name) == nil {
			if fn, ok := c.funcNamed(id.Name); ok {
				in := newInference(fn)
				if len(x.TypeArgs) == len(fn.TypeParams) {
					for i, ta := range x.TypeArgs {
						if t := c.peekType(ta); t != nil {
							in.bound[fn.TypeParams[i]] = t
						}
					}
				}
				for i, arg := range x.Args {
					if i < len(fn.Params) {
						index := i
						if fn.Decl != nil && len(x.Arguments) == len(x.Args) && x.Arguments[i].Name != "" {
							index = -1
							for j, p := range fn.Decl.Params {
								if p.Name == x.Arguments[i].Name {
									index = j
									break
								}
							}
						}
						if index >= 0 {
							in.unify(fn.Params[index], c.peekValueTypeIn(arg, locals))
						}
					}
				}
				return in.subst(fn.Result)
			}
		}
		if ft, ok := c.peekValueTypeIn(x.Fun, locals).(*FuncType); ok {
			return ft.Result
		}
		if sel, ok := x.Fun.(*syntax.Selector); ok {
			if receiver := c.peekValueTypeIn(sel.X, locals); receiver != nil {
				if seq, ok := receiver.(*Seq); ok && sel.Name == "first" {
					return instantiate(c.preludePkg.TypeNamed("Option"), []Type{seq.Elem})
				}
				if fn, _ := c.methodNamed(receiver, sel.Name); fn != nil {
					in := newInference(fn)
					if len(fn.Params) > 0 {
						in.unify(fn.Params[0], receiver)
					}
					for i, arg := range x.Args {
						j := i + 1
						if fn.Decl != nil && len(x.Arguments) == len(x.Args) && x.Arguments[i].Name != "" {
							j = -1
							for k, p := range fn.Decl.Params {
								if p.Name == x.Arguments[i].Name {
									j = k
									break
								}
							}
						}
						if j >= 0 && j < len(fn.Params) {
							in.unify(fn.Params[j], c.peekValueTypeIn(arg, locals))
						}
					}
					for i, ta := range x.TypeArgs {
						j := len(fn.TypeParams) - len(x.TypeArgs) + i
						if j >= 0 {
							if t := c.peekType(ta); t != nil {
								in.bound[fn.TypeParams[j]] = t
							}
						}
					}
					return in.subst(fn.Result)
				}
			}
		}
	case *syntax.Block:
		inner := map[string]Type{}
		for name, t := range locals {
			inner[name] = t
		}
		for _, stmt := range x.Stmts {
			if b, ok := stmt.(*syntax.Binding); ok {
				if b.Type != nil {
					inner[b.Name] = c.peekType(b.Type)
				} else {
					inner[b.Name] = c.peekValueTypeIn(b.Value, inner)
				}
			}
		}
		return c.peekValueTypeIn(x.Tail, inner)
	case *syntax.ScopeExpr:
		return c.peekValueTypeIn(x.Body, locals)
	case *syntax.WithExpr:
		return c.peekValueTypeIn(x.Body, locals)
	case *syntax.Match:
		var known Type
		for _, arm := range x.Arms {
			inner := map[string]Type{}
			for name, t := range locals {
				inner[name] = t
			}
			c.peekPattern(arm.Pattern, c.peekValueTypeIn(x.X, locals), inner)
			t := c.peekValueTypeIn(arm.Body, inner)
			if t == Never {
				continue
			}
			if t == nil || known != nil && (!IsOption(known) || !IsOption(t)) && !identical(known, t) {
				return nil
			}
			known = t
		}
		return known
	case *syntax.If:
		a, b := c.peekValueTypeIn(x.Then, locals), c.peekValueTypeIn(x.Else, locals)
		if a == Never {
			return b
		}
		if b == Never {
			return a
		}
		if a != nil && b != nil && (identical(a, b) || IsOption(a) && IsOption(b)) {
			return a
		}
	}
	return nil
}

func (c *checker) optionPromotionNote(args []any) string {
	var ts []Type
	for _, a := range args {
		if t, ok := a.(Type); ok {
			ts = append(ts, t)
		}
	}
	if len(ts) < 2 {
		return ""
	}
	want, found := ts[len(ts)-2], ts[len(ts)-1]
	if IsOption(want) {
		payload := want.(*Sealed).Args[0]
		if found != Invalid && found != Never && (c.open(payload) || assignable(found, payload)) {
			return "; write .Some(...) to make the optional layer or type inference explicit"
		}
	}
	return ""
}

// peekType reads constructor spelling without producing diagnostics or
// recording a second type-argument check.
func (c *checker) peekType(x *syntax.TypeExpr) Type {
	if x == nil {
		return nil
	}
	if len(x.Union) > 0 {
		out := &Union{}
		for _, member := range x.Union {
			t := c.peekType(member)
			if t == nil {
				return nil
			}
			out.Members = append(out.Members, t)
		}
		return out
	}
	if x.Func != nil {
		out := &FuncType{Result: c.peekType(x.Func.Result)}
		if out.Result == nil {
			return nil
		}
		for _, param := range x.Func.Params {
			t := c.peekType(param)
			if t == nil {
				return nil
			}
			out.Params = append(out.Params, t)
		}
		return out
	}
	args := make([]Type, len(x.Args))
	for i, a := range x.Args {
		args[i] = c.peekType(a)
		if args[i] == nil {
			return nil
		}
	}
	switch x.Name {
	case "List":
		if len(args) == 1 {
			return &List{Elem: args[0]}
		}
	case "Map":
		if len(args) == 2 {
			return &Map{Key: args[0], Value: args[1]}
		}
	case "Seq":
		if len(args) == 1 {
			return &Seq{Elem: args[0]}
		}
	}
	t := c.typeNamed(x.Name)
	if t == nil && c.typeParams != nil {
		if tp := c.typeParams[x.Name]; tp != nil {
			t = tp
		}
	}
	if len(x.Args) == 0 {
		return t
	}
	if t == nil || len(typeParamsOf(t)) != len(x.Args) {
		return nil
	}
	return instantiate(t, args)
}

func (c *checker) peekPattern(p syntax.Pattern, t Type, locals map[string]Type) {
	switch p := p.(type) {
	case *syntax.TypePat:
		locals[p.Name] = c.peekType(p.Type)
	case *syntax.VariantPat:
		if !p.Context && len(p.Path) == 1 && !p.Braces && c.typeNamed(p.Path[0]) == nil {
			locals[p.Path[0]] = t
			return
		}
		var fields []*Field
		switch s := t.(type) {
		case *Record:
			fields = s.Fields
		case *Sealed:
			if len(p.Path) > 0 {
				if v := s.Variant(p.Path[len(p.Path)-1]); v != nil {
					fields = v.Fields
				}
			}
		case *Union:
			if p.Context {
				owners, unresolved := c.contextCandidates(p.Path[0], s)
				if len(owners) == 1 && !unresolved {
					if owner, ok := owners[0].(*Sealed); ok {
						if v := owner.Variant(p.Path[0]); v != nil {
							fields = v.Fields
						}
					}
				}
				break
			}
			if base := c.typeNamed(strings.Join(p.Path, ".")); base != nil {
				if r, ok := instanceIn(s, base).(*Record); ok {
					fields = r.Fields
				}
			}
		}
		for i, elem := range p.Elems {
			if i < len(fields) {
				c.peekPattern(elem, fields[i].Type, locals)
			}
		}
		for _, fp := range p.Fields {
			if f := findField(fields, fp.Field); f != nil {
				if fp.Pattern == nil {
					locals[fp.Field] = f.Type
				} else {
					c.peekPattern(fp.Pattern, f.Type, locals)
				}
			}
		}
	case *syntax.ListPat:
		if xs, ok := t.(*List); ok {
			for _, item := range p.Elems {
				c.peekPattern(item, xs.Elem, locals)
			}
			if p.Rest != "" {
				locals[p.Rest] = t
			}
		}
	}
}
