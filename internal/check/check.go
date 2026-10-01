package check

import (
	"math/big"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Func is a declared function's signature.
type Func struct {
	Decl   *syntax.FuncDecl
	Params []Type
	Result Type
}

// Builtin identifies a function provided by the compiler.
type Builtin int

const (
	BuiltinNone Builtin = iota
	BuiltinPrintln
)

var builtins = map[string]Builtin{
	"println": BuiltinPrintln,
}

// Info is what the checker learned about a package. Later passes (code
// generation) read it instead of re-deriving types.
type Info struct {
	Funcs map[string]*Func
	// Types records the type of every expression.
	Types map[syntax.Expr]Type
	// Calls records which function each call targets.
	CallFuncs    map[*syntax.Call]*Func
	CallBuiltins map[*syntax.Call]Builtin
	// UnusedBindings lists bindings whose value is never read.
	UnusedBindings map[*syntax.Binding]bool
	// IntValues holds the parsed value of every integer literal.
	IntValues map[*syntax.IntLit]int64
}

// Package type-checks the given files as one package.
func Package(files []*syntax.File, diags *diag.List) *Info {
	c := &checker{
		diags: diags,
		info: &Info{
			Funcs:          map[string]*Func{},
			Types:          map[syntax.Expr]Type{},
			CallFuncs:      map[*syntax.Call]*Func{},
			CallBuiltins:   map[*syntax.Call]Builtin{},
			UnusedBindings: map[*syntax.Binding]bool{},
			IntValues:      map[*syntax.IntLit]int64{},
		},
	}
	// Pass 1: collect function signatures, so functions can call each
	// other regardless of declaration order.
	for _, f := range files {
		for _, fd := range f.Funcs {
			c.declareFunc(fd)
		}
	}
	// Pass 2: check bodies.
	for _, f := range files {
		for _, fd := range f.Funcs {
			if fn := c.info.Funcs[fd.Name]; fn != nil && fn.Decl == fd {
				c.checkFunc(fn)
			}
		}
	}
	return c.info
}

type checker struct {
	diags *diag.List
	info  *Info

	// Per-function state.
	fn     *Func
	scopes []map[string]*local
}

type local struct {
	typ     Type
	binding *syntax.Binding // nil for parameters
	used    bool
}

func (c *checker) errorf(pos diag.Pos, format string, args ...any) {
	c.diags.Add(pos, format, args...)
}

func (c *checker) resolveType(t *syntax.TypeExpr) Type {
	if t == nil {
		return Unit
	}
	if typ, ok := namedTypes[t.Name]; ok {
		return typ
	}
	c.errorf(t.Pos, "unknown type %s", t.Name)
	return Invalid
}

func (c *checker) declareFunc(fd *syntax.FuncDecl) {
	if _, ok := builtins[fd.Name]; ok {
		c.errorf(fd.Pos, "%s is a built-in function and cannot be redefined", fd.Name)
		return
	}
	if prev, ok := c.info.Funcs[fd.Name]; ok {
		c.errorf(fd.Pos, "function %s is already declared at %s", fd.Name, prev.Decl.Pos)
		return
	}
	fn := &Func{Decl: fd, Result: c.resolveType(fd.Result)}
	for _, p := range fd.Params {
		fn.Params = append(fn.Params, c.resolveType(p.Type))
	}
	c.info.Funcs[fd.Name] = fn
}

func (c *checker) checkFunc(fn *Func) {
	c.fn = fn
	c.scopes = []map[string]*local{{}}
	for i, p := range fn.Decl.Params {
		if c.nameTaken(p.Name, p.Pos) {
			continue
		}
		if fn.Params[i] == Unit {
			c.errorf(p.Type.Pos, "parameter %s cannot have type Unit", p.Name)
		}
		c.scopes[0][p.Name] = &local{typ: fn.Params[i]}
	}
	if fn.Decl.Name == "main" && (len(fn.Params) != 0 || fn.Result != Unit) {
		c.errorf(fn.Decl.Pos, "main must take no parameters and return no value")
	}
	bodyType := c.block(fn.Decl.Body)
	if fn.Result == Unit && isValue(bodyType) {
		c.errorf(fn.Decl.Body.Tail.Position(), "value of type %s is not used (function %s returns no value)", bodyType, fn.Decl.Name)
	}
	if fn.Result != Unit && !assignable(bodyType, fn.Result) {
		pos := fn.Decl.Body.Pos
		if fn.Decl.Body.Tail != nil {
			pos = fn.Decl.Body.Tail.Position()
		}
		if bodyType == Unit {
			c.errorf(pos, "function %s must return a value of type %s, but its body ends without one", fn.Decl.Name, fn.Result)
		} else {
			c.errorf(pos, "function %s returns %s, but its body produces %s", fn.Decl.Name, fn.Result, bodyType)
		}
	}
	c.fn = nil
}

// nameTaken reports (and diagnoses) an attempt to bind a name that is
// already visible. bork does not allow shadowing.
func (c *checker) nameTaken(name string, pos diag.Pos) bool {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if _, ok := c.scopes[i][name]; ok {
			c.errorf(pos, "%s is already defined in an enclosing scope (bork does not allow shadowing)", name)
			return true
		}
	}
	if _, ok := c.info.Funcs[name]; ok {
		c.errorf(pos, "%s is already the name of a function (bork does not allow shadowing)", name)
		return true
	}
	if _, ok := builtins[name]; ok {
		c.errorf(pos, "%s is a built-in function (bork does not allow shadowing)", name)
		return true
	}
	return false
}

func (c *checker) lookup(name string) *local {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if l, ok := c.scopes[i][name]; ok {
			return l
		}
	}
	return nil
}

func (c *checker) record(e syntax.Expr, t Type) Type {
	c.info.Types[e] = t
	return t
}

func (c *checker) block(b *syntax.Block) Type {
	c.scopes = append(c.scopes, map[string]*local{})
	defer func() {
		scope := c.scopes[len(c.scopes)-1]
		for _, l := range scope {
			if l.binding != nil && !l.used {
				c.info.UnusedBindings[l.binding] = true
			}
		}
		c.scopes = c.scopes[:len(c.scopes)-1]
	}()

	// Statements after one that never finishes (e.g. `return`) are
	// unreachable. Only the first one is reported.
	diverged, reported := false, false
	for _, s := range b.Stmts {
		if diverged {
			c.errorf(stmtPos(s), "unreachable code")
			reported = true
			break
		}
		if c.stmt(s) == Never {
			diverged = true
		}
	}
	t := Unit
	if b.Tail != nil {
		if !diverged {
			t = c.expr(b.Tail)
		} else if !reported {
			c.errorf(b.Tail.Position(), "unreachable code")
		}
	}
	if diverged {
		t = Never
	}
	return c.record(b, t)
}

func stmtPos(s syntax.Stmt) diag.Pos {
	switch s := s.(type) {
	case *syntax.Binding:
		return s.Pos
	case *syntax.ExprStmt:
		return s.X.Position()
	}
	return diag.Pos{}
}

// stmt checks a statement and returns Never if it never finishes.
func (c *checker) stmt(s syntax.Stmt) Type {
	switch s := s.(type) {
	case *syntax.Binding:
		t := c.expr(s.Value)
		switch t {
		case Unit:
			c.errorf(s.Value.Position(), "cannot bind %s: the expression produces no value (Unit)", s.Name)
			t = Invalid
		case Never:
			c.errorf(s.Value.Position(), "cannot bind %s: the expression never produces a value", s.Name)
			return Never
		}
		if c.nameTaken(s.Name, s.Pos) {
			// Bind anyway, as invalid, so later uses don't cause follow-up errors.
			t = Invalid
		}
		c.scopes[len(c.scopes)-1][s.Name] = &local{typ: t, binding: s}
		return Unit
	case *syntax.ExprStmt:
		t := c.expr(s.X)
		if isValue(t) {
			c.errorf(s.X.Position(), "value of type %s is not used", t)
		}
		return t
	}
	return Unit
}

func (c *checker) expr(e syntax.Expr) Type {
	switch e := e.(type) {
	case *syntax.IntLit:
		v, ok := new(big.Int).SetString(strings.ReplaceAll(e.Text, "_", ""), 10)
		if !ok || strings.HasPrefix(e.Text, "_") || strings.HasSuffix(e.Text, "_") || strings.Contains(e.Text, "__") {
			c.errorf(e.Pos, "invalid integer literal %s", e.Text)
			return c.record(e, Invalid)
		}
		if !v.IsInt64() {
			c.errorf(e.Pos, "integer literal %s does not fit in Int (64-bit)", e.Text)
			return c.record(e, Invalid)
		}
		c.info.IntValues[e] = v.Int64()
		return c.record(e, Int)
	case *syntax.StringLit:
		return c.record(e, String)
	case *syntax.BoolLit:
		return c.record(e, Bool)
	case *syntax.Ident:
		if l := c.lookup(e.Name); l != nil {
			l.used = true
			return c.record(e, l.typ)
		}
		if _, ok := c.info.Funcs[e.Name]; ok {
			c.errorf(e.Pos, "function %s must be called (functions as values are not supported yet)", e.Name)
			return c.record(e, Invalid)
		}
		if _, ok := builtins[e.Name]; ok {
			c.errorf(e.Pos, "built-in %s must be called", e.Name)
			return c.record(e, Invalid)
		}
		c.errorf(e.Pos, "undefined: %s", e.Name)
		return c.record(e, Invalid)
	case *syntax.Unary:
		return c.record(e, c.unary(e))
	case *syntax.Binary:
		return c.record(e, c.binary(e))
	case *syntax.Call:
		return c.record(e, c.call(e))
	case *syntax.If:
		return c.record(e, c.ifExpr(e))
	case *syntax.Block:
		return c.block(e)
	case *syntax.Return:
		c.returnExpr(e)
		return c.record(e, Never)
	}
	panic("unhandled expression")
}

func (c *checker) unary(e *syntax.Unary) Type {
	t := c.expr(e.X)
	if t == Invalid {
		return Invalid
	}
	switch e.Op {
	case syntax.Minus:
		if t != Int {
			c.errorf(e.Pos, "operator - needs an Int, found %s", t)
			return Invalid
		}
		return Int
	case syntax.Not:
		if t != Bool {
			c.errorf(e.Pos, "operator ! needs a Bool, found %s", t)
			return Invalid
		}
		return Bool
	}
	return Invalid
}

func opSymbol(k syntax.Kind) string {
	return strings.Trim(k.String(), "'")
}

func (c *checker) binary(e *syntax.Binary) Type {
	x, y := c.expr(e.X), c.expr(e.Y)
	if x == Invalid || y == Invalid {
		return Invalid
	}
	op := opSymbol(e.Op)
	switch e.Op {
	case syntax.AndAnd, syntax.OrOr:
		if x != Bool || y != Bool {
			c.errorf(e.Pos, "operator %s needs Bool operands, found %s and %s", op, x, y)
			return Invalid
		}
		return Bool
	case syntax.Plus:
		if x == Int && y == Int {
			return Int
		}
		if x == String && y == String {
			return String
		}
		c.errorf(e.Pos, "operator + needs two Ints or two Strings, found %s and %s", x, y)
		return Invalid
	case syntax.Minus, syntax.Star, syntax.Slash, syntax.Pct:
		if x != Int || y != Int {
			c.errorf(e.Pos, "operator %s needs Int operands, found %s and %s", op, x, y)
			return Invalid
		}
		return Int
	case syntax.Lt, syntax.LtEq, syntax.Gt, syntax.GtEq:
		if (x == Int && y == Int) || (x == String && y == String) {
			return Bool
		}
		c.errorf(e.Pos, "operator %s needs two Ints or two Strings, found %s and %s", op, x, y)
		return Invalid
	case syntax.Eq, syntax.NotEq:
		if x != y {
			c.errorf(e.Pos, "cannot compare %s with %s: the types are incompatible", x, y)
			return Invalid
		}
		if !isValue(x) {
			c.errorf(e.Pos, "cannot compare values of type %s", x)
			return Invalid
		}
		return Bool
	}
	return Invalid
}

func (c *checker) call(e *syntax.Call) Type {
	id, ok := e.Fun.(*syntax.Ident)
	if !ok {
		c.errorf(e.Fun.Position(), "only named functions can be called")
		for _, a := range e.Args {
			c.expr(a)
		}
		return Invalid
	}
	if b, ok := builtins[id.Name]; ok && c.lookup(id.Name) == nil {
		c.info.CallBuiltins[e] = b
		return c.builtinCall(e, b)
	}
	fn, ok := c.info.Funcs[id.Name]
	if !ok {
		if c.lookup(id.Name) != nil {
			c.errorf(id.Pos, "%s is a value, not a function", id.Name)
		} else {
			c.errorf(id.Pos, "undefined function: %s", id.Name)
		}
		for _, a := range e.Args {
			c.expr(a)
		}
		return Invalid
	}
	c.info.CallFuncs[e] = fn
	if len(e.Args) != len(fn.Params) {
		c.errorf(e.Pos, "%s takes %d argument(s), but %d were given", id.Name, len(fn.Params), len(e.Args))
	}
	for i, a := range e.Args {
		t := c.expr(a)
		if i < len(fn.Params) && !assignable(t, fn.Params[i]) {
			c.errorf(a.Position(), "argument %d to %s must be %s, found %s", i+1, id.Name, fn.Params[i], t)
		}
	}
	return fn.Result
}

func (c *checker) builtinCall(e *syntax.Call, b Builtin) Type {
	switch b {
	case BuiltinPrintln:
		for _, a := range e.Args {
			t := c.expr(a)
			if t != Invalid && !isValue(t) {
				c.errorf(a.Position(), "println cannot print a value of type %s", t)
			}
		}
		return Unit
	}
	return Invalid
}

func (c *checker) ifExpr(e *syntax.If) Type {
	cond := c.expr(e.Cond)
	if cond != Bool && cond != Invalid && cond != Never {
		c.errorf(e.Cond.Position(), "if-condition must be Bool, found %s", cond)
	}
	thenT := c.block(e.Then)
	if e.Else == nil {
		// Without else, the if is only run for its effect.
		if isValue(thenT) {
			c.errorf(e.Then.Pos, "if without else cannot produce a value (found %s); add an else branch or drop the value", thenT)
		}
		return Unit
	}
	elseT := c.expr(e.Else)
	switch {
	case thenT == Invalid || elseT == Invalid:
		return Invalid
	case thenT == Never:
		return elseT
	case elseT == Never:
		return thenT
	case thenT == elseT:
		return thenT
	}
	c.errorf(e.Pos, "if-branches have different types: %s and %s", thenT, elseT)
	return Invalid
}

func (c *checker) returnExpr(e *syntax.Return) {
	want := c.fn.Result
	if e.Value == nil {
		if want != Unit {
			c.errorf(e.Pos, "function %s must return a value of type %s", c.fn.Decl.Name, want)
		}
		return
	}
	t := c.expr(e.Value)
	if want == Unit {
		c.errorf(e.Value.Position(), "function %s does not return a value", c.fn.Decl.Name)
		return
	}
	if !assignable(t, want) {
		c.errorf(e.Value.Position(), "function %s returns %s, but this returns %s", c.fn.Decl.Name, want, t)
	}
}
