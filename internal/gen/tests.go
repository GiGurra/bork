package gen

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"
	"unicode"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/syntax"
)

// Tests generates a Go program that runs the package's tests and
// reports the results. It is built in test mode: facts the compiler
// takes on trust (`trust`, and what `unsafe go` functions promise) are
// checked at runtime, so a wrong one fails the test that reaches it.
// Inference rules, also taken on trust, get property tests that look
// for counterexamples (see ruleTest).
//
// A test with parameters is a property test (see propertyTest), and
// with autoProperties, so is every function whose promises are trusted
// (see autoPropertyCandidate).
func Tests(files []*syntax.File, info *check.Info, autoProperties bool) ([]byte, error) {
	return TestsWith(files, info, TestOptions{AutoProperties: autoProperties})
}

// TestOptions are how Tests builds the test program.
type TestOptions struct {
	// AutoProperties property-tests the functions whose promises are
	// trusted (see autoPropertyCandidate).
	AutoProperties bool
	// Hermetic fails, without running it, every test that can reach a
	// function doing net in Go code with no mock of it in force (see
	// check.Info.Unmocked).
	Hermetic bool
}

// TestsWith is Tests with options.
func TestsWith(files []*syntax.File, info *check.Info, opts TestOptions) ([]byte, error) {
	autoProperties := opts.AutoProperties
	g := newGen(info)
	g.testMode = true
	g.usesTests = true
	// With Hermetic, the tests that could reach the network fail
	// without running, and their bodies (mocks included) are left out.
	notRun := map[*check.Func]string{}
	if opts.Hermetic {
		for _, fn := range info.Tests {
			if paths := info.Unmocked(fn, check.EffNet); len(paths) > 0 {
				notRun[fn] = notHermetic(paths, fn.Pkg)
			}
		}
	}
	g.mockTargets(info, notRun)
	var roots []*check.Func
	list := &ast.CompositeLit{Type: &ast.ArrayType{Elt: ast.NewIdent("_test")}}
	snaps := map[string]bool{}
	failing := func(goName *ast.Ident, msg string) {
		g.extraFuncs = append(g.extraFuncs, &ast.FuncDecl{
			Name: goName,
			Type: &ast.FuncType{Params: &ast.FieldList{}},
			Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{strLit(msg)}}}}},
		})
	}
	for i, fn := range info.Tests {
		goName := ast.NewIdent("_test" + strconv.Itoa(i+1))
		if msg, ok := notRun[fn]; ok {
			failing(goName, msg)
			snap := ""
			if len(fn.Params) == 0 {
				// Named as when it runs, so the other tests' names stay.
				snap = snapshotName(fn.Test.Name, snaps)
			}
			list.Elts = append(list.Elts, &ast.CompositeLit{Elts: []ast.Expr{strLit(fn.Test.Name), strLit(snap), goName}})
			continue
		}
		roots = append(roots, fn.Calls...)
		if len(fn.Params) > 0 {
			// Its generated values cannot be snapshotted.
			body := func() []ast.Stmt { return g.blockInto(fn.Body, sink{}) }
			decl, untried := g.propertyTest(fn.Test.Name, false, fn.Decl.Params, fn.Params, fn.ParamConstraints, body, goName)
			if decl == nil {
				list.Elts = append(list.Elts, &ast.CompositeLit{Elts: []ast.Expr{
					strLit(fmt.Sprintf("%s (no values are generated for %s)", fn.Test.Name, untried)),
					strLit(""),
					ast.NewIdent("nil"),
				}})
				continue
			}
			g.extraFuncs = append(g.extraFuncs, decl)
			list.Elts = append(list.Elts, &ast.CompositeLit{Elts: []ast.Expr{strLit(fn.Test.Name), strLit(""), goName}})
			continue
		}
		g.extraFuncs = append(g.extraFuncs, g.testFunc(fn, goName))
		list.Elts = append(list.Elts, &ast.CompositeLit{Elts: []ast.Expr{
			&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(fn.Test.Name)},
			strLit(snapshotName(fn.Test.Name, snaps)),
			goName,
		}})
	}
	for i, r := range info.Rules {
		if r.Pkg != nil && !r.Pkg.Root {
			continue // an imported package's rules are tested with it
		}
		goName := ast.NewIdent("_rule" + strconv.Itoa(i+1))
		decl, untried := g.ruleTest(r, goName)
		if decl == nil {
			list.Elts = append(list.Elts, &ast.CompositeLit{Elts: []ast.Expr{
				strLit(fmt.Sprintf("rule %s (no values are generated for %s)", r.Decl.Name, untried)),
				strLit(""),
				ast.NewIdent("nil"),
			}})
			continue
		}
		for _, atoms := range [][]*check.RuleAtom{r.Premises, r.Conclusions} {
			for _, a := range atoms {
				roots = append(roots, a.Pred)
			}
		}
		g.extraFuncs = append(g.extraFuncs, decl)
		list.Elts = append(list.Elts, &ast.CompositeLit{Elts: []ast.Expr{
			strLit("rule " + r.Decl.Name),
			strLit(""),
			goName,
		}})
	}
	if autoProperties {
		n := 0
		for _, f := range files {
			for _, fd := range f.Funcs {
				fn := info.FuncOf[fd]
				if fn == nil || !g.autoPropertyCandidate(fn) {
					continue
				}
				n++
				goName := ast.NewIdent("_auto" + strconv.Itoa(n))
				if opts.Hermetic {
					if paths := info.Reaching(fn, check.EffNet); len(paths) > 0 {
						// It is called on generated arguments, with no mocks.
						var up []check.UnmockedPath
						for _, p := range paths {
							up = append(up, check.UnmockedPath{Pos: fn.Decl.Pos, Funcs: p})
						}
						failing(goName, notHermetic(up, fn.Pkg))
						list.Elts = append(list.Elts, &ast.CompositeLit{Elts: []ast.Expr{strLit(autoPropertyName(fn)), strLit(""), goName}})
						continue
					}
				}
				roots = append(roots, fn)
				g.extraFuncs = append(g.extraFuncs, g.autoProperty(fn, goName))
				list.Elts = append(list.Elts, &ast.CompositeLit{Elts: []ast.Expr{strLit(autoPropertyName(fn)), strLit(""), goName}})
			}
		}
	}
	// Keep declaration identities separate from display names (which can include
	// a reason a property test cannot generate values).
	for i, elt := range list.Elts {
		entry := elt.(*ast.CompositeLit)
		selector, file, line := "", "", 0
		if i < len(info.Tests) {
			test := info.Tests[i].Test
			selector, file, line = test.Name, test.Pos.File, test.Pos.Line
		}
		// Generated rule and automatic property tests are not named test
		// declarations; their display labels must not match --filter.
		entry.Elts = append(entry.Elts, strLit(selector), strLit(file), &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(line)}, ast.NewIdent(strconv.FormatBool(i < len(info.Tests))))
	}
	roots = append(roots, g.propRoots...)
	if g.mockErrors.Len() > 0 {
		return nil, &g.mockErrors
	}
	main := &ast.FuncDecl{
		Name: ast.NewIdent("main"),
		Type: &ast.FuncType{Params: &ast.FieldList{}},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("_runTests"), Args: []ast.Expr{list}}}}},
	}
	return generate(g, files, roots, main)
}

// notHermetic explains how a test reaches the network unmocked: per
// path, the call in the test, the functions on the way, and the last of
// them the test can mock.
func notHermetic(paths []check.UnmockedPath, from *check.Package) string {
	var b strings.Builder
	b.WriteString("not hermetic (bork test --hermetic): it can reach the network with no mock in force:")
	for _, p := range paths {
		var names []string
		mockable := ""
		for _, f := range p.Funcs {
			n := f.QualifiedName(from)
			names = append(names, n)
			if f.Mockable(from) {
				mockable = n
			}
		}
		fmt.Fprintf(&b, "\n%s: %s", p.Pos, strings.Join(names, " -> "))
		switch {
		case p.InMock != nil && p.Funcs[0] == p.InMock:
			fmt.Fprintf(&b, " (in the mock of %s, its name means the function before the mock)", p.InMock.QualifiedName(from))
		case mockable == "":
			b.WriteString(" (no function on the way can be mocked)")
		default:
			fmt.Fprintf(&b, "; mock %s", mockable)
		}
	}
	b.WriteString("\nA mock covers a call written in its block after it; a call in a lambda, or a function passed as a value, may run elsewhere, and only mocks at the start of the test cover it.")
	return b.String()
}

// snapshotName is the base name of a test's snapshot files: its name
// with every run of characters other than ASCII letters and digits
// replaced by _, and a number added if another test already has it
// (ignoring case).
func snapshotName(test string, taken map[string]bool) string {
	var b strings.Builder
	gap := false
	for _, r := range test {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			if gap && b.Len() > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
			gap = false
		} else {
			gap = true
		}
	}
	base := b.String()
	if base == "" {
		base = "test"
	}
	// Names that differ only in case are the same file on some
	// file systems.
	name := base
	for n := 2; taken[strings.ToLower(name)]; n++ {
		name = base + "_" + strconv.Itoa(n)
	}
	taken[strings.ToLower(name)] = true
	return name
}

// testFunc generates a test's body as a function.
func (g *gen) testFunc(fn *check.Func, goName *ast.Ident) ast.Decl {
	g.tmp = 0
	g.fnResult = check.Ok
	return &ast.FuncDecl{
		Name: goName,
		Type: &ast.FuncType{Params: &ast.FieldList{}},
		Body: &ast.BlockStmt{List: g.guardLabels(func() []ast.Stmt { return g.blockInto(fn.Body, sink{}) })},
	}
}

// at is a position as a Go string literal, for messages.
func at(pos fmt.Stringer) ast.Expr {
	return &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(pos.String())}
}

// trustCheck checks a `trust p(x)` in test mode.
func (g *gen) trustCheck(s *check.Trust) []ast.Stmt {
	stmts, cond := g.value(s.Call)
	if cond == nil {
		return stmts
	}
	msg := fmt.Sprintf("%s: trusted fact does not hold: ", s.Pos)
	g.usesShow = true
	text := &ast.BinaryExpr{X: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(msg + s.Text)}, Op: token.ADD, Y: g.argsShown(s)}
	return append(stmts, &ast.IfStmt{
		Cond: &ast.UnaryExpr{Op: token.NOT, X: paren(cond)},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{text}}}}},
	})
}

// argsShown renders the first argument of a trusted call for the
// message: " (x = 0)".
func (g *gen) argsShown(s *check.Trust) ast.Expr {
	var args []check.Expr
	switch c := s.Call.(type) {
	case *check.Call:
		args = c.Args
	case *check.CallValue:
		args = c.Args
	}
	if len(args) == 0 {
		return &ast.BasicLit{Kind: token.STRING, Value: `""`}
	}
	_, x := g.value(args[0])
	if x == nil {
		return &ast.BasicLit{Kind: token.STRING, Value: `""`}
	}
	show := &ast.CallExpr{Fun: ast.NewIdent("_show"), Args: []ast.Expr{g.typed(x, args[0].Type())}}
	return &ast.BinaryExpr{
		X:  &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(" (" + s.SubjectText + " = ")},
		Op: token.ADD,
		Y:  &ast.BinaryExpr{X: show, Op: token.ADD, Y: &ast.BasicLit{Kind: token.STRING, Value: `")"`}},
	}
}

// checkedWrapper generates, in test mode, the function fn (implemented
// in unsafe go, now named _unchecked_<name>) as a wrapper that checks
// what fn's signature promises about its result.
func (g *gen) checkedWrapper(fn *check.Func) ast.Decl {
	decl := g.signature(fn.Decl)
	var args []ast.Expr
	for _, tp := range fn.TypeParams {
		for _, b := range tp.Bounds {
			args = append(args, dictParam(tp, b))
		}
	}
	for _, p := range fn.Decl.Params {
		args = append(args, name(p.Name))
	}
	for _, v := range fn.NeedVars {
		args = append(args, varIdent(v))
	}
	var impl ast.Expr = ast.NewIdent("_unchecked_" + g.funcName(fn).Name)
	if len(fn.TypeParams) > 0 {
		idx := &ast.IndexListExpr{X: impl}
		for _, tp := range fn.TypeParams {
			idx.Indices = append(idx.Indices, name(tp.Name))
		}
		impl = idx
	}
	r := ast.NewIdent("_r")
	body := []ast.Stmt{define(r, &ast.CallExpr{Fun: impl, Args: args})}
	for _, mc := range fn.ResultConstraints {
		var checks []ast.Stmt
		v := ast.Expr(r)
		if !check.Identical(mc.Type, fn.Result) {
			v = g.newTmp()
		}
		for _, con := range mc.Constraints {
			checks = append(checks, g.atPath(v, mc.Type, splitPath(con.Path), func(x ast.Expr, t check.Type) []ast.Stmt {
				return g.contractCheck(fn, con, x, t)
			})...)
		}
		if check.Identical(mc.Type, fn.Result) {
			body = append(body, checks...)
			continue
		}
		// One member of a union result.
		ok := g.newTmp()
		body = append(body, &ast.IfStmt{
			Init: &ast.AssignStmt{Lhs: []ast.Expr{v, ok}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.TypeAssertExpr{X: r, Type: g.goType(mc.Type)}}},
			Cond: ok,
			Body: &ast.BlockStmt{List: checks},
		})
	}
	// Records it builds must keep their fields' where clauses.
	body = append(body, g.invariantChecks(fn, r, fn.Result, map[check.Type]bool{})...)
	decl.Body = &ast.BlockStmt{List: append(body, &ast.ReturnStmt{Results: []ast.Expr{r}})}
	return decl
}

// invariantPreds lists the predicates that invariantChecks may call for
// values of type t.
func invariantPreds(t check.Type, seen map[check.Type]bool) []*check.Func {
	if seen[t] {
		return nil
	}
	seen[t] = true
	var out []*check.Func
	for _, con := range check.TypeConstraints(t) {
		out = append(out, constraintPreds(con)...)
	}
	fields := func(fs []*check.Field) {
		for _, f := range fs {
			for _, con := range f.Constraints {
				out = append(out, constraintPreds(con)...)
			}
			out = append(out, invariantPreds(f.Type, seen)...)
		}
	}
	switch t := t.(type) {
	case *check.Record:
		fields(t.Fields)
	case *check.Sealed:
		for _, v := range t.Variants {
			for _, con := range v.Constraints {
				out = append(out, constraintPreds(con)...)
			}
			fields(v.Fields)
		}
	case *check.Union:
		for _, m := range t.Members {
			out = append(out, invariantPreds(m, seen)...)
		}
	case *check.List:
		out = append(out, invariantPreds(t.Elem, seen)...)
	case *check.Map:
		out = append(out, invariantPreds(t.Key, seen)...)
		out = append(out, invariantPreds(t.Value, seen)...)
	}
	return out
}

func constraintPreds(con *check.Constraint) []*check.Func {
	var out []*check.Func
	for _, alt := range con.Or {
		out = append(out, constraintPreds(alt)...)
	}
	if con.Pred != nil {
		out = append(out, con.Pred)
	}
	return out
}

// hasInvariants reports whether values of type t can hold records whose
// fields have where clauses.
func (g *gen) hasInvariants(t check.Type, seen map[check.Type]bool) bool {
	if len(check.TypeConstraints(t)) > 0 {
		return true
	}
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t := t.(type) {
	case *check.Record:
		for _, f := range t.Fields {
			if len(f.Constraints) > 0 || g.hasInvariants(f.Type, seen) {
				return true
			}
		}
	case *check.Sealed:
		for _, v := range t.Variants {
			if len(v.Constraints) > 0 {
				return true
			}
			for _, f := range v.Fields {
				if len(f.Constraints) > 0 || g.hasInvariants(f.Type, seen) {
					return true
				}
			}
		}
	case *check.Union:
		for _, m := range t.Members {
			if g.hasInvariants(m, seen) {
				return true
			}
		}
	case *check.List:
		return g.hasInvariants(t.Elem, seen)
	case *check.Map:
		return g.hasInvariants(t.Key, seen) || g.hasInvariants(t.Value, seen)
	}
	return false
}

// invariantChecks checks that the records in x (of type t), which fn
// returned, keep their fields' where clauses. Types already being
// checked are not entered again, so recursive types are checked only to
// a fixed depth.
func (g *gen) invariantChecks(fn *check.Func, x ast.Expr, t check.Type, seen map[check.Type]bool) []ast.Stmt {
	if seen[t] || !g.hasInvariants(t, map[check.Type]bool{}) {
		return nil
	}
	seen[t] = true
	defer delete(seen, t)
	fields := func(owner string, x ast.Expr, fs []*check.Field) []ast.Stmt {
		var out []ast.Stmt
		for _, f := range fs {
			v := g.fieldRead(x, f)
			for _, con := range f.Constraints {
				what := fmt.Sprintf("%s whose %s is not %s", owner, f.Name, con)
				out = append(out, g.atPath(v, f.Type, splitPath(con.Path), func(y ast.Expr, yt check.Type) []ast.Stmt {
					return g.invariantCheck(fn, fieldConstraint(con, func(n string) string {
						for _, sibling := range fs {
							if sibling.Name == n {
								return g.text(g.fieldRead(x, sibling))
							}
						}
						panic("missing sibling")
					}), y, yt, what)
				})...)
			}
			out = append(out, g.invariantChecks(fn, v, f.Type, seen)...)
		}
		return out
	}
	nominal := func(cons []*check.Constraint, x ast.Expr, typ check.Type) []ast.Stmt {
		var out []ast.Stmt
		for _, con := range cons {
			out = append(out, g.invariantCheck(fn, con, x, typ, fmt.Sprintf("%s that is not %s", typ, con))...)
		}
		return out
	}
	switch t := t.(type) {
	case *check.Record:
		return append(fields(t.Name, x, t.Fields), nominal(t.Constraints, x, t)...)
	case *check.Sealed:
		out := nominal(t.Constraints, x, t)
		for _, v := range t.Variants {
			if !g.hasInvariants(&check.Record{Fields: v.Fields, Constraints: v.Constraints}, map[check.Type]bool{}) {
				continue
			}
			val, ok := g.newTmp(), g.newTmp()
			body := fields(t.Name+"."+v.Name, val, v.Fields)
			body = append(body, nominal(v.Constraints, val, t)...)
			out = append(out, &ast.IfStmt{
				Init: &ast.AssignStmt{Lhs: []ast.Expr{val, ok}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.TypeAssertExpr{X: x, Type: g.variantType(v)}}},
				Cond: ok,
				Body: &ast.BlockStmt{List: body},
			})
		}
		return out
	case *check.Union:
		var out []ast.Stmt
		for _, m := range t.Members {
			if !g.hasInvariants(m, map[check.Type]bool{}) {
				continue
			}
			val, ok := g.newTmp(), g.newTmp()
			out = append(out, &ast.IfStmt{
				Init: &ast.AssignStmt{Lhs: []ast.Expr{val, ok}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.TypeAssertExpr{X: x, Type: g.goType(m)}}},
				Cond: ok,
				Body: &ast.BlockStmt{List: g.invariantChecks(fn, val, m, seen)},
			})
		}
		return out
	case *check.List:
		el := g.newTmp()
		return []ast.Stmt{&ast.RangeStmt{
			Key: ast.NewIdent("_"), Value: el, Tok: token.DEFINE, X: x,
			Body: &ast.BlockStmt{List: g.invariantChecks(fn, el, t.Elem, seen)},
		}}
	case *check.Map:
		key, value := g.newTmp(), g.newTmp()
		body := g.invariantChecks(fn, key, t.Key, seen)
		body = append(body, g.invariantChecks(fn, value, t.Value, seen)...)
		body = append(body, &ast.ReturnStmt{Results: []ast.Expr{ast.NewIdent("true")}})
		callback := &ast.FuncLit{Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{
				{Names: []*ast.Ident{key}, Type: g.goType(t.Key)},
				{Names: []*ast.Ident{value}, Type: g.goType(t.Value)},
			}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("bool")}}},
		}, Body: &ast.BlockStmt{List: body}}
		return []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: x, Sel: ast.NewIdent("each")}, Args: []ast.Expr{callback}}}}
	}
	return nil
}

// invariantCheck panics if con does not hold for x, a field value of a
// record fn returned.
func (g *gen) invariantCheck(fn *check.Func, con *check.Constraint, x ast.Expr, t check.Type, what string) []ast.Stmt {
	cond := g.constraintCond(con, x, t)
	if cond == nil {
		return nil
	}
	g.usesShow = true
	msg := fmt.Sprintf("%s: %s returned %s %s: ", fn.Decl.Pos, fn.Decl.Name, article(what), what)
	text := &ast.BinaryExpr{X: strLit(msg), Op: token.ADD, Y: &ast.CallExpr{Fun: ast.NewIdent("_show"), Args: []ast.Expr{x}}}
	return []ast.Stmt{&ast.IfStmt{
		Cond: &ast.UnaryExpr{Op: token.NOT, X: paren(cond)},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{text}}}}},
	}}
}

func article(w string) string {
	if w != "" && strings.ContainsRune("AEIOUaeiou", rune(w[0])) {
		return "an"
	}
	return "a"
}

func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	return strings.Split(path[1:], ".")
}

// atPath runs leaf on the parts of x (of bork type t) that path leads
// to: every element of a list, a field of a record, or a field of
// whichever variant has it.
func (g *gen) atPath(x ast.Expr, t check.Type, steps []string, leaf func(ast.Expr, check.Type) []ast.Stmt) []ast.Stmt {
	if len(steps) == 0 {
		return leaf(x, t)
	}
	step, rest := steps[0], steps[1:]
	switch t := t.(type) {
	case *check.List:
		el := g.newTmp()
		return []ast.Stmt{&ast.RangeStmt{
			Key: ast.NewIdent("_"), Value: el, Tok: token.DEFINE, X: x,
			Body: &ast.BlockStmt{List: g.atPath(el, t.Elem, rest, leaf)},
		}}
	case *check.Record:
		if f := t.Field(step); f != nil {
			return g.atPath(g.fieldRead(x, f), f.Type, rest, leaf)
		}
	case *check.Sealed:
		var out []ast.Stmt
		for _, v := range t.Variants {
			f := v.Field(step)
			if f == nil {
				continue
			}
			val, ok := g.newTmp(), g.newTmp()
			out = append(out, &ast.IfStmt{
				Init: &ast.AssignStmt{Lhs: []ast.Expr{val, ok}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.TypeAssertExpr{X: x, Type: g.variantType(v)}}},
				Cond: ok,
				Body: &ast.BlockStmt{List: g.atPath(g.fieldRead(val, f), f.Type, rest, leaf)},
			})
		}
		return out
	}
	return nil
}

// contractCheck checks con on the value x (of bork type t), which fn
// returned, and panics if it does not hold.
func (g *gen) contractCheck(fn *check.Func, con *check.Constraint, x ast.Expr, t check.Type) []ast.Stmt {
	cond := g.constraintCond(con, x, t)
	if cond == nil {
		return nil
	}
	g.usesShow = true
	what := "a result that is " + con.String()
	if con.Path != "" {
		what = "results whose parts are " + con.String()
	}
	msg := fmt.Sprintf("%s: %s promised %s, but returned ", fn.Decl.Pos, fn.Decl.Name, what)
	text := &ast.BinaryExpr{
		X:  &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(msg)},
		Op: token.ADD,
		Y:  &ast.CallExpr{Fun: ast.NewIdent("_show"), Args: []ast.Expr{x}},
	}
	return []ast.Stmt{&ast.IfStmt{
		Cond: &ast.UnaryExpr{Op: token.NOT, X: paren(cond)},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{text}}}}},
	}}
}

// constraintCond is the Go condition that con holds for x, of type t.
func (g *gen) constraintCond(con *check.Constraint, x ast.Expr, t check.Type) ast.Expr {
	if con.Or != nil {
		var out ast.Expr
		for _, alt := range con.Or {
			c := g.constraintCond(alt, x, t)
			if c == nil {
				return nil
			}
			if out == nil {
				out = c
			} else {
				out = &ast.BinaryExpr{X: paren(out), Op: token.LOR, Y: paren(c)}
			}
		}
		return out
	}
	if con.PredParam != "" {
		// The function value the caller passed.
		return &ast.CallExpr{Fun: g.shapeCaptureArgument(con.PredParam), Args: []ast.Expr{x}}
	}
	inst := con.InstanceFor(t)
	if inst == nil || !g.info.PredicateDicts(con.Pkg, inst) {
		return nil
	}
	var args []ast.Expr
	for _, dict := range inst.Dicts {
		args = append(args, g.dict(dict))
	}
	args = append(args, x)
	for i, a := range con.Args {
		if a.Const != nil {
			args = append(args, g.constant(a.Const, inst.Params[i+1]))
		} else {
			args = append(args, g.shapeCaptureArgument(a.Param))
		}
	}
	args = append(args, g.membershipArgs(inst.Func.TypeParams, inst.TypeArgs)...)
	return &ast.CallExpr{Fun: g.instance(inst), Args: args}
}

// fieldConstraint substitutes sibling fields with their completed Go values.
func fieldConstraint(con *check.Constraint, value func(string) string) *check.Constraint {
	cp := *con
	cp.Args = append([]check.CArg(nil), con.Args...)
	for i, a := range cp.Args {
		if a.Sibling {
			cp.Args[i].Param = value(a.Param)
			cp.Args[i].Sibling = false
		}
	}
	cp.Or = nil
	for _, alt := range con.Or {
		cp.Or = append(cp.Or, fieldConstraint(alt, value))
	}
	return &cp
}
