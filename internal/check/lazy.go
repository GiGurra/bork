package check

import (
	"sort"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// LazyDescription reports initializer metadata without evaluating a cell.
type AsyncDescription struct {
	Scope    string   `json:"scope"`
	Effects  string   `json:"initializer_effects"`
	Captures []string `json:"captures,omitempty"`
}

type LazyDescription struct {
	Kind         string   `json:"kind"`
	Effects      string   `json:"initializer_effects"`
	Captures     []string `json:"captures,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

// A result boundary at exactly depth; nested lambdas retain their own rules.
type initializerContext struct {
	name      string
	parent    *initializerContext
	external  map[any]bool
	captures  map[string]bool
	depth     int
	want      Type
	returns   []Type
	rejectTry bool
}

func (c *checker) deferredInitializer(s *syntax.Binding, want Type) Type {
	kind := "lazy"
	if s.AsyncScope != nil {
		kind = "async"
	}
	return c.valueInitializer(s, want, kind+" initializer")
}

// fieldInitializer checks the recipe with its own return/try boundary.
func (c *checker) fieldInitializer(value syntax.Expr, field *Field) Type {
	if field == nil || !field.Lazy {
		return c.exprWant(value, fieldType(field))
	}
	binding := &syntax.Binding{Lazy: true, LazyPos: value.Position(), Pos: value.Position(), Name: field.Name, Value: value}
	typ := c.deferredInitializer(binding, field.Type)
	metadata := c.info.lazyBindings[binding]
	metadata.Kind = "field"
	c.info.lazyFields[value] = metadata
	delete(c.info.lazyBindings, binding)
	return typ
}
func fieldType(field *Field) Type {
	if field != nil {
		return field.Type
	}
	return nil
}

func (c *checker) valueInitializer(s *syntax.Binding, want Type, boundary string) Type {
	saved := c.initializerContext
	c.lambdaDepth++
	ctx := &initializerContext{name: boundary, depth: c.lambdaDepth, want: want, parent: saved, external: map[any]bool{}, captures: map[string]bool{}}
	ctx.rejectTry = s.Lazy || s.AsyncScope != nil
	for _, scope := range c.scopes {
		for _, local := range scope {
			ctx.external[local.decl] = true
		}
	}
	outerEffects := c.used
	c.used = 0
	c.initializerContext = ctx
	defer func() {
		if s.Lazy || s.Package || s.AsyncScope != nil {
			names := []string{}
			for name := range ctx.captures {
				names = append(names, name)
			}
			sort.Strings(names)
			c.info.lazyBindings[s] = &LazyDescription{Kind: "binding", Effects: c.used.String(), Captures: names}
		}
		c.used |= outerEffects
		c.initializerContext = saved
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
		t = c.zonk(want)
	}
	if containsOwned(t) || containsOwned(want) {
		c.errorf(s.Pos, "a %s cannot return an OwnedScope; acquire and consume it inside the initializer", boundary)
		return Invalid
	}
	return t
}

// Check every normal/early result against the binding's promises, retaining
// outer parameter facts but without collecting this thunk's returns as returns
// of a pure function the caller is currently unfolding.
func (f *factChecker) deferredBinding(s *Let, e env) {
	outer, collect := f.fn, f.collect
	savedFn, savedResult := f.initializerResultFn, f.initializerResult
	fn := *outer
	f.initializerResultFn, f.initializerResult = &fn, nil
	decl := *outer.Decl
	kind := "lazy"
	if s.Deferred == AsyncBinding {
		kind = "async"
	}
	decl.Name = kind + " " + s.Var.Name
	fn.Decl, fn.Body, fn.Result = &decl, nil, s.Var.Type
	fn.ResultConstraints = nil
	f.initializerResult = func(value Expr, facts env) {
		for _, con := range s.Constraints {
			f.oblige(value, con, f.ownParams(), facts, s.Var.displayName()+" must be "+con.String())
		}
	}
	// A thunk changes the result boundary, not the validator's proof context.
	if f.validators != nil {
		f.validators[&fn] = f.validators[outer]
	}
	f.fn, f.collect = &fn, nil
	defer func() {
		f.fn, f.collect = outer, collect
		f.initializerResultFn, f.initializerResult = savedFn, savedResult
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
					if !stmt.Lazy && stmt.AsyncScope == nil {
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

func (c *checker) noteInitializerCapture(decl any, name string) {
	for ctx := c.initializerContext; ctx != nil; ctx = ctx.parent {
		if ctx.external[decl] {
			ctx.captures[name] = true
		}
	}
}

// Retain validator exclusions while changing only the initializer's result boundary.
func (f *factChecker) fieldBoundary(value Expr, typ Type, facts env, result func(Expr, env)) {
	outer, collect := f.fn, f.collect
	savedFn, savedResult := f.initializerResultFn, f.initializerResult
	fn := *outer
	fn.Result, fn.ResultConstraints = typ, nil
	f.fn, f.collect = &fn, nil
	f.initializerResultFn, f.initializerResult = &fn, result
	if f.validators != nil {
		f.validators[&fn] = f.validators[outer]
	}
	defer func() {
		f.fn, f.collect = outer, collect
		f.initializerResultFn, f.initializerResult = savedFn, savedResult
		delete(f.validators, &fn)
	}()
	f.tail(value, facts, f.checkResult)
}

// LazyFieldDescription distinguishes a recipe from a transparent field read.
func (info *Info) LazyFieldDescription(value Expr) *LazyDescription {
	if metadata := info.fieldRecipes[value]; metadata != nil {
		return metadata
	}
	if read, ok := value.(*Select); ok && read.Field != nil && read.Field.Lazy {
		if read.Field.Computed {
			metadata := &LazyDescription{Kind: "computed field", Effects: "nothing"}
			for _, dependency := range read.Field.Dependencies {
				metadata.Dependencies = append(metadata.Dependencies, dependency.Name)
			}
			return metadata
		}
		return &LazyDescription{Kind: "independent field", Effects: "charged at construction"}
	}
	return nil
}
