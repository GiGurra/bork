package check

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Mocks (see "Mocking in tests" in docs/requirements.md): a `mock`
// statement in a test replaces a function with effects until the end
// of its block. Its body is checked as a function with the target's
// signature (Func.MockOf), so it keeps the target's promises: the
// parameters' and the result's types and facts, and the target's
// effects (plus state, which the effect check allows).

func (c *checker) mockErr(pos diag.Pos, format string, args ...any) {
	c.diags.AddCode(pos, "mock.error", format, args...)
}

// mockStmt checks `[m =] mock target(a, b) { ... }`.
func (c *checker) mockStmt(s *syntax.MockStmt) {
	test := c.fn
	switch {
	case test != nil && test.MockOf != nil:
		c.mockErr(s.MockPos, "mock can only be used in a test body, not in the body of another mock")
	case test == nil || test.Test == nil:
		c.mockErr(s.MockPos, "mock can only be used in a test body")
	case c.lambdaDepth > 0:
		c.mockErr(s.MockPos, "mock can only be used in a test body, not in a lambda (which may run later, or on another task)")
	}
	target := c.mockTarget(s.Target)
	if target != nil && len(s.Params) != len(target.Params) {
		var names []string
		for _, p := range target.Decl.Params {
			names = append(names, p.Name)
		}
		list := strings.Join(names, ", ")
		text := writtenText(s.Target)
		start := s.ParamsStart
		start.Col++
		c.diags.AddCode(s.ParamsStart, "mock.parameters", "%s takes %s (%s), but the mock names %d; write: mock %s(%s)", text, plural(len(target.Params), "parameter"), list, len(s.Params), text, list)
		c.diags.Suggest(s.ParamsStart, "mock.parameters", s.ParamsStart, diag.Fix{
			Message: "name the parameters of " + text,
			Edits:   []diag.TextEdit{{Start: start, End: s.ParamsEnd, Replacement: list}},
		})
		target = nil
	}
	if target != nil {
		// One mock of a function per block, as one binding of a name.
		key := "mock " + target.QualifiedName(nil) + fmt.Sprintf(" %p", target)
		top := c.scopes[len(c.scopes)-1]
		if prev, ok := top[key]; ok {
			c.mockErr(s.MockPos, "%s is already mocked in this block (at %s); mock it again in a nested block", writtenText(s.Target), prev.decl.(*syntax.MockStmt).MockPos)
		} else {
			top[key] = &local{typ: Invalid, decl: s, used: true}
		}
		c.mockBody(s, target, test)
	}
	if s.Name != "" {
		t := Type(Invalid)
		if mt := c.preludePkg.TypeNamed("Mock"); mt != nil && target != nil {
			t = instantiate(mt, []Type{c.callRecord(target)})
		}
		c.info.mockHandles[s] = t
		c.bind(s.Name, s.Pos, t, s)
	}
}

// callRecord is the record of a call of target, which a mock's handle
// gives its calls' arguments as (Mock[A]): a field per parameter, named
// as the target names them. It is made once per target, named after it
// (FetchCall, StoreSaveCall, HttpGetCall), and declared only in test
// builds.
func (c *checker) callRecord(target *Func) *Record {
	if r := c.info.MockCalls[target]; r != nil {
		return r
	}
	var b strings.Builder
	if target.Pkg != nil && target.Pkg != c.pkg {
		parts := strings.Split(target.Pkg.Path, "/")
		b.WriteString(identWord(parts[len(parts)-1]))
	}
	if target.Decl.IsMethod && len(target.Params) > 0 {
		recv := target.Params[0]
		switch t := recv.(type) {
		case *Record:
			b.WriteString(identWord(t.Name))
		case *Sealed:
			b.WriteString(identWord(t.Name))
		default:
			b.WriteString(identWord(TypeText(recv, c.pkg)))
		}
	}
	b.WriteString(identWord(target.Decl.Name))
	base := b.String() + "Call"
	name := base
	for n := 2; c.isTypeName(name) || c.pkg.Funcs[name] != nil || c.preludePkg.Funcs[name] != nil || c.mockCallTaken(name); n++ {
		name = fmt.Sprintf("%s%d", base, n)
	}
	r := &Record{Name: name, Pkg: c.pkg, MockCall: true}
	// A value that can belong to a scope (a scope, a resource, a Go
	// value, a function, or what holds one) would outlive it in the
	// handle, which the test keeps: such parameters get no field (the
	// call's text still shows them).
	life := &lifeChecker{carries: map[Type]bool{}}
	for i, p := range target.Params {
		if life.carriesLife(p) {
			continue
		}
		field := fmt.Sprintf("p%d", i)
		if i < len(target.Decl.Params) {
			field = target.Decl.Params[i].Name
		}
		r.Fields = append(r.Fields, &Field{Name: field, Type: p, Pkg: c.pkg})
	}
	c.info.MockCalls[target] = r
	c.info.MockCallOrder = append(c.info.MockCallOrder, r)
	return r
}

// mockCallLeftOut explains, if r is a call record that left out the
// target's parameter name, why.
func (info *Info) mockCallLeftOut(r *Record, name string) string {
	if !r.MockCall {
		return ""
	}
	for target, rec := range info.MockCalls {
		if rec != r {
			continue
		}
		for i, p := range target.Decl.Params {
			if p.Name == name && i < len(target.Params) {
				return fmt.Sprintf("%s has no field %s: a call record leaves out parameters whose values can belong to a scope (%s), since the test keeps the records after their scopes end; check %s inside the mock's body instead", r.Name, name, target.Params[i], name)
			}
		}
	}
	return ""
}

func (c *checker) mockCallTaken(name string) bool {
	for _, r := range c.info.MockCallOrder {
		if r.Name == name {
			return true
		}
	}
	return false
}

// identWord is s capitalized, with what an identifier cannot hold left
// out: "http" is "Http", "List[Int]" is "ListInt".
func identWord(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
		}
	}
	w := b.String()
	if w == "" {
		return w
	}
	return strings.ToUpper(w[:1]) + w[1:]
}

// mockBody checks a mock's body as the body of a function with
// target's signature, seeing the test's values around it.
func (c *checker) mockBody(s *syntax.MockStmt, target, test *Func) {
	fd := target.Decl
	// The target's where clauses, in the names the mock gives its
	// parameters.
	names := map[string]string{}
	for i, p := range s.Params {
		if p.Name != "_" && i < len(fd.Params) {
			names[fd.Params[i].Name] = p.Name
		}
	}
	paramCons := make([][]*Constraint, len(target.ParamConstraints))
	for i, cons := range target.ParamConstraints {
		paramCons[i] = renameConstraints(cons, names)
	}
	var resultCons []MemberConstraints
	for _, mc := range target.ResultConstraints {
		resultCons = append(resultCons, MemberConstraints{Type: mc.Type, Constraints: renameConstraints(mc.Constraints, names)})
	}
	fn := &Func{
		Decl:              &syntax.FuncDecl{Pos: s.MockPos, Name: fd.Name, Params: s.Params, ParamsEnd: s.ParamsEnd, Uses: fd.Uses, Result: fd.Result, Body: s.Body},
		Pkg:               c.pkg,
		Params:            target.Params,
		Result:            target.Result,
		Effects:           target.Effects | EffState,
		ParamConstraints:  paramCons,
		ResultConstraints: resultCons,
		MockOf:            target,
		MockIn:            test,
	}
	savedFn, savedUsed, savedDepth := c.fn, c.used, c.lambdaDepth
	c.fn, c.used, c.lambdaDepth = fn, 0, 0
	c.pushScope()
	for i, p := range s.Params {
		if p.Name == "_" || c.nameTaken(p.Name, p.Pos) {
			continue
		}
		c.scopes[len(c.scopes)-1][p.Name] = &local{typ: target.Params[i], decl: p}
	}
	var want Type
	if fn.Result != Unit {
		want = fn.Result
	}
	bodyType := c.block(s.Body, want)
	c.popScope()
	c.fn, c.used, c.lambdaDepth = savedFn, savedUsed, savedDepth
	name := writtenText(s.Target)
	switch {
	case fn.Result == Unit && isValue(bodyType):
		c.errorf(s.Body.Tail.Position(), "value of type %s is not used (%s returns no value)", bodyType, name)
	case fn.Result != Unit && bodyType == Unit:
		c.errorf(s.Body.Pos, "the mock of %s must give a value of type %s, but its body ends without one", name, fn.Result)
	case fn.Result != Unit && !assignable(bodyType, fn.Result):
		pos := s.Body.Pos
		if s.Body.Tail != nil {
			pos = s.Body.Tail.Position()
		}
		c.errorf(pos, "the mock of %s must give %s, but its body produces %s", name, fn.Result, bodyType)
	}
	if test != nil {
		// What the mock calls is part of the test program.
		test.Calls = append(test.Calls, fn.Calls...)
	}
	c.info.mocks[s] = fn
	c.info.Mocks = append(c.info.Mocks, fn)
}

// mockTarget finds the function a mock names, and reports why it
// cannot be mocked if it cannot (returning nil).
func (c *checker) mockTarget(x syntax.Expr) *Func {
	text := writtenText(x)
	var fn *Func
	switch x := x.(type) {
	case *syntax.Ident:
		if _, ok := builtins[x.Name]; ok {
			c.mockErr(x.Pos, "%s is built into the compiler and cannot be mocked; mock the function that calls it", text)
			return nil
		}
		if pkg, n, ok := c.qualified(x.Name); ok {
			if f := pkg.Funcs[n]; f != nil && !Exported(n) && f.Class == nil {
				c.mockErr(x.Pos, "%s is not exported by package %s, so a test of another package cannot mock it; mock the exported function that calls it", text, pkg.Path)
				return nil
			}
		}
		f, ok := c.funcNamed(x.Name)
		if !ok || f == nil {
			if why := c.notFound(x.Name); why != "" {
				c.mockErr(x.Pos, "cannot mock %s: %s", text, why)
			} else if c.lookup(x.Name) != nil {
				c.mockErr(x.Pos, "%s is a value, not a declared function; only declared functions can be mocked (pass a different value instead)", text)
			} else {
				c.mockErr(x.Pos, "cannot mock %s: there is no function named %s", text, text)
			}
			return nil
		}
		fn = f
	case *syntax.Selector:
		f, why, isMethod := c.methodReference(x)
		if !isMethod {
			c.mockErr(x.Pos, "cannot mock %s: it names no function or method (write a function, pkg.Function, or Type.method)", text)
			return nil
		}
		if f == nil {
			c.mockErr(x.Pos, "cannot mock %s: %s", text, why)
			return nil
		}
		fn = f
	default:
		c.mockErr(x.Position(), "cannot mock %s: it names no function", text)
		return nil
	}
	pos := x.Position()
	switch {
	case fn.Class != nil || fn.Of != nil || fn.Derived != nil:
		c.mockErr(pos, "%s is a class method, and class methods and instances cannot be mocked; mock the function that uses them", text)
	case fn.Prelude:
		c.mockErr(pos, "%s is part of the prelude and cannot be mocked; mock the function that calls it", text)
	case fn.Decl.IsPred:
		c.mockErr(pos, "%s is a predicate, and only functions with effects can be mocked: the compiler relies on predicates' answers to prove facts", text)
	case fn.Decl.Name == "main" && fn.Pkg != nil && fn.Pkg.Root:
		c.mockErr(pos, "main cannot be mocked: it cannot be called")
	case fn.Effects&^EffOpen == 0:
		c.mockErr(pos, "%s is pure, and only functions with effects can be mocked: the compiler relies on a pure function giving the same answer for the same arguments (facts named after it, and predicates calling it, would go stale); test it as it is, or have the code under test take it as a function value", text)
	case len(fn.TypeParams) > 0:
		c.mockErr(pos, "%s is generic, and generic functions cannot be mocked yet; mock a non-generic function that calls it", text)
	default:
		return fn
	}
	return nil
}

// plural is "1 parameter" or "2 parameters".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Mocked reports whether a test mocks fn. Such a function promises its
// callers only what its signature says, as another package's does,
// since a mock keeps only those promises.
func (info *Info) Mocked(fn *Func) bool {
	for _, m := range info.Mocks {
		if m.MockOf == fn {
			return true
		}
	}
	return false
}

// renameConstraints copies where clauses with the parameters they name
// renamed (a parameter a mock ignores with _ keeps its name, which then
// names nothing the mock's body can prove anything about).
func renameConstraints(cons []*Constraint, names map[string]string) []*Constraint {
	var out []*Constraint
	for _, con := range cons {
		c := *con
		if n, ok := names[c.PredParam]; ok {
			c.PredParam = n
		}
		c.Args = append([]CArg(nil), con.Args...)
		for i, a := range c.Args {
			if n, ok := names[a.Param]; ok && a.Const == nil && !a.Sibling {
				c.Args[i].Param = n
			}
		}
		c.Or = renameConstraints(con.Or, names)
		out = append(out, &c)
	}
	return out
}
