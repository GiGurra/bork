package check

import (
	"sort"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// LazyDescription reports initializer metadata without evaluating a cell.
type LazyDescription struct {
	Kind     string   `json:"kind"`
	Effects  string   `json:"initializer_effects"`
	Captures []string `json:"captures,omitempty"`
}

// A result boundary at exactly depth; nested lambdas retain their own rules.
type lazyContext struct {
	parent   *lazyContext
	external map[any]bool
	captures map[string]bool
	depth    int
	want     Type
	returns  []Type
}

func (c *checker) lazyInitializer(s *syntax.Binding, want Type) Type {
	saved := c.lazyContext
	c.lambdaDepth++
	ctx := &lazyContext{depth: c.lambdaDepth, want: want, parent: saved, external: map[any]bool{}, captures: map[string]bool{}}
	for _, scope := range c.scopes {
		for _, local := range scope {
			ctx.external[local.decl] = true
		}
	}
	outerEffects := c.used
	c.used = 0
	c.lazyContext = ctx
	defer func() {
		names := []string{}
		for name := range ctx.captures {
			names = append(names, name)
		}
		sort.Strings(names)
		c.info.lazyBindings[s] = &LazyDescription{Kind: "binding", Effects: c.used.String(), Captures: names}
		c.used |= outerEffects
		c.lazyContext = saved
		c.lambdaDepth--
	}()
	t := c.exprWant(s.Value, want)
	if want == nil && len(ctx.returns) > 0 {
		members := append([]Type{}, ctx.returns...)
		if t != Never {
			members = append(members, t)
		}
		t = newUnion(members)
	}
	if t == Never && want != nil {
		t = want
	}
	if containsOwned(t) || containsOwned(want) {
		c.errorf(s.Pos, "a lazy initializer cannot return an OwnedScope; acquire and consume it inside the initializer")
		return Invalid
	}
	return t
}

// Check every normal/early result against the binding's promises, retaining
// outer parameter facts but without collecting this thunk's returns as returns
// of a pure function the caller is currently unfolding.
func (f *factChecker) lazyBinding(s *Let, e env) {
	outer, collect := f.fn, f.collect
	fn := *outer
	decl := *outer.Decl
	decl.Name = "lazy " + s.Var.Name
	fn.Decl, fn.Body, fn.Result = &decl, nil, s.Var.Type
	fn.ResultConstraints = []MemberConstraints{{Type: s.Var.Type, Constraints: s.Constraints}}
	// A thunk changes the result boundary, not the validator's proof context.
	if f.validators != nil {
		f.validators[&fn] = f.validators[outer]
	}
	f.fn, f.collect = &fn, nil
	defer func() {
		f.fn, f.collect = outer, collect
		delete(f.validators, &fn)
	}()
	f.tail(s.Value, e, f.checkResult)
}

// LazyWarnings only reports a narrow, unconditional next-statement read.
// Complex initializers are excluded because removing their function boundary
// could change return/? behavior.
func LazyWarnings(info *Info) *diag.List {
	warnings := &diag.List{}
	for node := range info.types {
		block, ok := node.(*syntax.Block)
		if !ok {
			continue
		}
		for i, stmt := range block.Stmts {
			binding, ok := stmt.(*syntax.Binding)
			if !ok || !binding.Lazy {
				continue
			}
			metadata := info.lazyBindings[binding]
			if metadata == nil || metadata.Effects != "nothing" || info.types[binding.Value] == Never || !simpleLazyInitializer(binding.Value) {
				continue
			}
			var next syntax.Expr
			if i+1 < len(block.Stmts) {
				switch stmt := block.Stmts[i+1].(type) {
				case *syntax.Binding:
					if !stmt.Lazy {
						next = stmt.Value
					}
				case *syntax.ExprStmt:
					next = stmt.X
				}
			} else {
				next = block.Tail
			}
			if call, ok := next.(*syntax.Call); ok {
				if _, direct := call.Fun.(*syntax.Ident); direct && len(call.Args) > 0 {
					next = call.Args[0]
				}
			}
			ref, ok := next.(*syntax.Ident)
			if !ok || info.defs[ref] != binding {
				continue
			}
			end := binding.LazyPos
			end.Col += len("lazy")
			const code = "lazy.immediate-force"
			warnings.Warn(binding.LazyPos, code, "pure lazy initializer is read immediately; consider an ordinary binding")
			warnings.Suggest(binding.LazyPos, code, end, diag.Fix{Message: "remove the lazy modifier", Edits: []diag.TextEdit{{Start: binding.LazyPos, End: end, Replacement: ""}}})
		}
	}
	return warnings
}

func simpleLazyInitializer(x syntax.Expr) bool {
	switch x := x.(type) {
	case *syntax.IntLit, *syntax.FloatLit, *syntax.RuneLit, *syntax.BoolLit, *syntax.StringLit, *syntax.Ident:
		return true
	case *syntax.Call:
		if _, ok := x.Fun.(*syntax.Ident); !ok {
			return false
		}
		for _, arg := range x.Args {
			switch arg.(type) {
			case *syntax.IntLit, *syntax.FloatLit, *syntax.RuneLit, *syntax.BoolLit, *syntax.StringLit, *syntax.Ident:
			default:
				return false
			}
		}
		return true
	}
	return false
}

func (c *checker) noteLazyCapture(decl any, name string) {
	for ctx := c.lazyContext; ctx != nil; ctx = ctx.parent {
		if ctx.external[decl] {
			ctx.captures[name] = true
		}
	}
}
