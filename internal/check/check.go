package check

import (
	"go/constant"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Func is a declared function's signature.
type Func struct {
	Decl   *syntax.FuncDecl
	Params []Type
	Result Type
	// Prelude is set for the built-in functions of prelude.bork.
	Prelude bool
	// Calls lists the functions this function's body calls.
	Calls []*Func
}

// Builtin identifies a function provided by the compiler.
type Builtin int

const (
	BuiltinNone Builtin = iota
	BuiltinPrintln
	BuiltinToString
	BuiltinConvert // toInt8(x), toFloat(x), ...
	BuiltinPanic
)

var builtins = map[string]Builtin{
	"println":  BuiltinPrintln,
	"toString": BuiltinToString,
	"panic":    BuiltinPanic,
}

// conversions maps each conversion function to its target type.
var conversions = map[string]Type{}

func init() {
	for name, t := range basicTypes {
		if IsNumeric(t) {
			conversions["to"+name] = t
			builtins["to"+name] = BuiltinConvert
		}
	}
}

// Conversion describes a numeric conversion of a non-constant value.
// Checked conversions may go out of range, and produce `To | OutOfRange`.
type Conversion struct {
	From, To Type
	Checked  bool
}

// TryInfo describes a `?` expression.
type TryInfo struct {
	// Kept is the type `?` produces.
	Kept Type
	// Rest lists the union members returned from the function (for a
	// union operand).
	Rest []Type
	// Option is the operand's type when it is an Option, and NoneOf the
	// Option type that `None` is returned as.
	Option *Sealed
	NoneOf *Sealed
}

// Info is what the checker learned about a package. Later passes (code
// generation) read it instead of re-deriving types.
type Info struct {
	Funcs map[string]*Func
	// Named holds every declared type: *Record, *Sealed, or (for an
	// alias) the aliased type.
	Named map[string]Type
	// TypeOrder lists declared records and sealed types in source order.
	TypeOrder []Type
	// Types records the type of every expression.
	Types map[syntax.Expr]Type
	// Calls records which function each call targets.
	CallFuncs    map[*syntax.Call]*Func
	CallBuiltins map[*syntax.Call]Builtin
	// RecordTargets records what each record literal builds: a *Record
	// or a *Variant.
	RecordTargets map[*syntax.RecordLit]any
	// SelectorVariants records selectors that name a field-less variant
	// (`Shape.Empty`); other selectors are field accesses.
	SelectorVariants map[*syntax.Selector]*Variant
	// ArmPats holds the checked pattern of every match arm.
	ArmPats map[*syntax.Arm]*Pat
	// Tries describes every `?`.
	Tries map[*syntax.Try]*TryInfo
	// Unused holds bindings whose value is never read: *syntax.Binding,
	// or the pattern node that bound the name.
	Unused map[any]bool
	// Consts holds the value of every constant expression (number
	// literals and arithmetic on them), already converted to the type
	// recorded in Types.
	Consts map[syntax.Expr]constant.Value
	// Bindings records the type of every binding.
	Bindings map[*syntax.Binding]Type
	// Conversions describes numeric conversions of non-constant values.
	Conversions map[*syntax.Call]*Conversion
	// OutOfRange is the prelude's OutOfRange record.
	OutOfRange Type
}

// Package type-checks the given files as one package.
func Package(files []*syntax.File, diags *diag.List) *Info {
	c := &checker{
		diags: diags,
		decls: map[string]*typeEntry{},
		info: &Info{
			Funcs:            map[string]*Func{},
			Named:            map[string]Type{},
			Types:            map[syntax.Expr]Type{},
			CallFuncs:        map[*syntax.Call]*Func{},
			CallBuiltins:     map[*syntax.Call]Builtin{},
			RecordTargets:    map[*syntax.RecordLit]any{},
			SelectorVariants: map[*syntax.Selector]*Variant{},
			ArmPats:          map[*syntax.Arm]*Pat{},
			Tries:            map[*syntax.Try]*TryInfo{},
			Unused:           map[any]bool{},
			Consts:           map[syntax.Expr]constant.Value{},
			Bindings:         map[*syntax.Binding]Type{},
			Conversions:      map[*syntax.Call]*Conversion{},
		},
	}
	// Pass 1: declare types, then resolve their bodies, so types can
	// refer to each other regardless of declaration order.
	for _, f := range files {
		for _, td := range f.Types {
			c.declareType(td, f.Prelude)
		}
	}
	for _, f := range files {
		for _, td := range f.Types {
			if e := c.decls[td.Name]; e != nil && e.decl == td {
				c.resolveDecl(e)
			}
		}
	}
	c.checkRecordCycles()
	c.info.OutOfRange = c.info.Named["OutOfRange"]
	// Pass 2: collect function signatures, so functions can call each
	// other regardless of declaration order.
	for _, f := range files {
		for _, fd := range f.Funcs {
			c.declareFunc(fd, f.Prelude)
		}
	}
	// Pass 3: check bodies.
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
	decls map[string]*typeEntry

	// Per-function state.
	fn     *Func
	scopes []map[string]*local
}

type local struct {
	typ  Type
	node any // the binding's syntax node; nil for parameters
	used bool
}

func (c *checker) errorf(pos diag.Pos, format string, args ...any) {
	c.diags.Add(pos, format, args...)
}

func (c *checker) declareFunc(fd *syntax.FuncDecl, prelude bool) {
	if _, ok := builtins[fd.Name]; ok {
		c.errorf(fd.Pos, "%s is a built-in function and cannot be redefined", fd.Name)
		return
	}
	if prev, ok := c.info.Funcs[fd.Name]; ok && prev.Prelude {
		c.errorf(fd.Pos, "%s is a built-in function and cannot be redefined", fd.Name)
		return
	} else if ok {
		c.errorf(fd.Pos, "function %s is already declared at %s", fd.Name, prev.Decl.Pos)
		return
	}
	if c.isTypeName(fd.Name) {
		c.errorf(fd.Pos, "%s is already the name of a type", fd.Name)
		return
	}
	fn := &Func{Decl: fd, Result: c.resolveType(fd.Result), Prelude: prelude}
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
	if fn.Decl.GoBody != nil {
		// The Go code is checked by the Go compiler.
		c.fn = nil
		return
	}
	var want Type
	if fn.Result != Unit {
		want = fn.Result
	}
	bodyType := c.block(fn.Decl.Body, want)
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
	if fn, ok := c.info.Funcs[name]; ok {
		if fn.Prelude {
			c.errorf(pos, "%s is a built-in function (bork does not allow shadowing)", name)
		} else {
			c.errorf(pos, "%s is already the name of a function (bork does not allow shadowing)", name)
		}
		return true
	}
	if _, ok := builtins[name]; ok {
		c.errorf(pos, "%s is a built-in function (bork does not allow shadowing)", name)
		return true
	}
	if c.isTypeName(name) {
		c.errorf(pos, "%s is already the name of a type", name)
		return true
	}
	return false
}

// bind adds a local to the innermost scope. A name that is already
// taken is still bound, as Invalid, so later uses don't cause follow-up
// errors.
func (c *checker) bind(name string, pos diag.Pos, t Type, node any) {
	if c.nameTaken(name, pos) {
		t = Invalid
	}
	c.scopes[len(c.scopes)-1][name] = &local{typ: t, node: node}
}

func (c *checker) lookup(name string) *local {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if l, ok := c.scopes[i][name]; ok {
			return l
		}
	}
	return nil
}

func (c *checker) pushScope() { c.scopes = append(c.scopes, map[string]*local{}) }

func (c *checker) popScope() {
	for _, l := range c.scopes[len(c.scopes)-1] {
		if l.node != nil && !l.used {
			c.info.Unused[l.node] = true
		}
	}
	c.scopes = c.scopes[:len(c.scopes)-1]
}

func (c *checker) record(e syntax.Expr, t Type) Type {
	c.info.Types[e] = t
	return t
}

// block checks a block. want is the type the block's value is expected
// to have, or nil if there is no expectation.
func (c *checker) block(b *syntax.Block, want Type) Type {
	c.pushScope()
	defer c.popScope()

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
			t = c.exprWant(b.Tail, want)
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
		var declared Type
		if s.Type != nil {
			declared = c.resolveType(s.Type)
		}
		t := c.exprWant(s.Value, declared)
		switch t {
		case Unit:
			c.errorf(s.Value.Position(), "cannot bind %s: the expression produces no value (Unit)", s.Name)
			t = Invalid
		case Never:
			c.errorf(s.Value.Position(), "cannot bind %s: the expression never produces a value", s.Name)
			return Never
		}
		if declared != nil && t != Invalid {
			if declared != Invalid && !assignable(t, declared) {
				c.errorf(s.Value.Position(), "%s must be %s, found %s", s.Name, declared, t)
			}
			t = declared
		}
		c.info.Bindings[s] = t
		c.bind(s.Name, s.Pos, t, s)
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

func (c *checker) expr(e syntax.Expr) Type { return c.exprWant(e, nil) }

// exprWant checks an expression. want is the type the context expects,
// or nil. It guides branches that produce different members of a union,
// and values like `Option.None` whose type comes from the context. It
// does not report mismatches; the caller does.
func (c *checker) exprWant(e syntax.Expr, want Type) Type {
	if v := constValue(e); v != nil {
		return c.constant(e, v, want)
	}
	switch e := e.(type) {
	case *syntax.IntLit:
		c.errorf(e.Pos, "invalid integer literal %s", e.Text)
		return c.record(e, Invalid)
	case *syntax.FloatLit:
		c.errorf(e.Pos, "invalid float literal %s", e.Text)
		return c.record(e, Invalid)
	case *syntax.RuneLit:
		c.errorf(e.Pos, "invalid rune literal %s", e.Text)
		return c.record(e, Invalid)
	case *syntax.StringLit:
		return c.record(e, String)
	case *syntax.Interp:
		for _, x := range e.Exprs {
			if t := c.expr(x); t != Invalid && !isValue(t) {
				c.errorf(x.Position(), "cannot put a value of type %s in a string", t)
			}
		}
		return c.record(e, String)
	case *syntax.BoolLit:
		return c.record(e, Bool)
	case *syntax.Ident:
		return c.record(e, c.ident(e))
	case *syntax.Unary:
		return c.record(e, c.unary(e))
	case *syntax.Binary:
		return c.record(e, c.binary(e, want))
	case *syntax.Call:
		return c.record(e, c.call(e))
	case *syntax.If:
		return c.record(e, c.ifExpr(e, want))
	case *syntax.Block:
		return c.block(e, want)
	case *syntax.Return:
		c.returnExpr(e)
		return c.record(e, Never)
	case *syntax.Selector:
		return c.record(e, c.selector(e, want))
	case *syntax.RecordLit:
		return c.record(e, c.recordLit(e, want))
	case *syntax.Copy:
		return c.record(e, c.copyExpr(e))
	case *syntax.Match:
		return c.record(e, c.match(e, want))
	case *syntax.Try:
		return c.record(e, c.try(e))
	}
	panic("unhandled expression")
}

func (c *checker) ident(e *syntax.Ident) Type {
	if l := c.lookup(e.Name); l != nil {
		l.used = true
		return l.typ
	}
	if _, ok := c.info.Funcs[e.Name]; ok {
		c.errorf(e.Pos, "function %s must be called (functions as values are not supported yet)", e.Name)
		return Invalid
	}
	if _, ok := builtins[e.Name]; ok {
		c.errorf(e.Pos, "built-in %s must be called", e.Name)
		return Invalid
	}
	if c.isTypeName(e.Name) {
		c.errorf(e.Pos, "%s is a type, not a value", e.Name)
		return Invalid
	}
	c.errorf(e.Pos, "undefined: %s", e.Name)
	return Invalid
}

func (c *checker) unary(e *syntax.Unary) Type {
	t := c.expr(e.X)
	if t == Invalid {
		return Invalid
	}
	switch e.Op {
	case syntax.Minus:
		if isUnsigned(t) {
			c.errorf(e.Pos, "operator - cannot be used on the unsigned type %s", t)
			return Invalid
		}
		if !IsNumeric(t) {
			c.errorf(e.Pos, "operator - needs a number, found %s", t)
			return Invalid
		}
		return t
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

func (c *checker) binary(e *syntax.Binary, want Type) Type {
	// A constant operand takes its type from the other operand
	// (`x + 1`, `1 + x`), and the right side may take its type from the
	// left (`o == Option.None`).
	var xWant Type
	if _, arith := constOps[e.Op]; arith {
		xWant = want
	}
	var x, y Type
	if constValue(e.X) != nil && constValue(e.Y) == nil {
		y = c.exprWant(e.Y, xWant)
		x = c.exprWant(e.X, y)
	} else {
		x = c.exprWant(e.X, xWant)
		y = c.exprWant(e.Y, x)
	}
	if x == Invalid || y == Invalid {
		return Invalid
	}
	op := opSymbol(e.Op)
	sameNumbers := IsNumeric(x) && identical(x, y)
	if e.Op == syntax.Slash || e.Op == syntax.Pct {
		if v := c.info.Consts[e.Y]; v != nil && constant.Sign(v) == 0 {
			c.errorf(e.Y.Position(), "division by zero")
			return Invalid
		}
	}
	switch e.Op {
	case syntax.AndAnd, syntax.OrOr:
		if x != Bool || y != Bool {
			c.errorf(e.Pos, "operator %s needs Bool operands, found %s and %s", op, x, y)
			return Invalid
		}
		return Bool
	case syntax.Plus:
		if sameNumbers || (x == String && y == String) {
			return x
		}
		c.errorf(e.Pos, "operator + needs two numbers of the same type or two Strings, found %s and %s", x, y)
		return Invalid
	case syntax.Minus, syntax.Star, syntax.Slash:
		if !sameNumbers {
			c.errorf(e.Pos, "operator %s needs two numbers of the same type, found %s and %s", op, x, y)
			return Invalid
		}
		return x
	case syntax.Pct:
		if !sameNumbers || !IsInteger(x) {
			c.errorf(e.Pos, "operator %% needs two integers of the same type, found %s and %s", x, y)
			return Invalid
		}
		return x
	case syntax.Lt, syntax.LtEq, syntax.Gt, syntax.GtEq:
		if sameNumbers || (x == String && y == String) {
			return Bool
		}
		c.errorf(e.Pos, "operator %s needs two numbers of the same type or two Strings, found %s and %s", op, x, y)
		return Invalid
	case syntax.Eq, syntax.NotEq:
		if !identical(x, y) {
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
		return c.builtinCall(e, id.Name, b)
	}
	fn, ok := c.info.Funcs[id.Name]
	if !ok {
		switch {
		case c.lookup(id.Name) != nil:
			c.errorf(id.Pos, "%s is a value, not a function", id.Name)
		case c.isTypeName(id.Name):
			c.errorf(id.Pos, "%s is a type; build a record with %s { field: value, ... }", id.Name, id.Name)
		default:
			c.errorf(id.Pos, "undefined function: %s", id.Name)
		}
		for _, a := range e.Args {
			c.expr(a)
		}
		return Invalid
	}
	c.info.CallFuncs[e] = fn
	if c.fn != nil {
		c.fn.Calls = append(c.fn.Calls, fn)
	}
	if len(e.Args) != len(fn.Params) {
		c.errorf(e.Pos, "%s takes %d argument(s), but %d were given", id.Name, len(fn.Params), len(e.Args))
	}
	for i, a := range e.Args {
		var want Type
		if i < len(fn.Params) {
			want = fn.Params[i]
		}
		t := c.exprWant(a, want)
		if want != nil && !assignable(t, want) {
			c.errorf(a.Position(), "argument %d to %s must be %s, found %s", i+1, id.Name, want, t)
		}
	}
	return fn.Result
}

func (c *checker) builtinCall(e *syntax.Call, fname string, b Builtin) Type {
	if b != BuiltinPrintln && len(e.Args) != 1 {
		c.errorf(e.Pos, "%s takes 1 argument, but %d were given", fname, len(e.Args))
		for _, a := range e.Args {
			c.expr(a)
		}
		return Invalid
	}
	switch b {
	case BuiltinPanic:
		if t := c.exprWant(e.Args[0], String); t != String && t != Invalid {
			c.errorf(e.Args[0].Position(), "panic needs a String message, found %s", t)
		}
		return Never
	case BuiltinToString:
		t := c.expr(e.Args[0])
		if t != Invalid && !isValue(t) {
			c.errorf(e.Args[0].Position(), "toString needs a value, found %s", t)
		}
		return String
	case BuiltinConvert:
		return c.conversion(e, fname)
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

func (c *checker) ifExpr(e *syntax.If, want Type) Type {
	cond := c.expr(e.Cond)
	if cond != Bool && cond != Invalid && cond != Never {
		c.errorf(e.Cond.Position(), "if-condition must be Bool, found %s", cond)
	}
	thenT := c.block(e.Then, want)
	if e.Else == nil {
		// Without else, the if is only run for its effect.
		if isValue(thenT) {
			c.errorf(e.Then.Pos, "if without else cannot produce a value (found %s); add an else branch or drop the value", thenT)
		}
		return Unit
	}
	elseT := c.exprWant(e.Else, want)
	return c.unify(e.Pos, "if-branches have", []Type{thenT, elseT}, want)
}

// unify computes the type of a value that comes from one of several
// branches. Branches that never finish (Never) are ignored. If the
// context expects a type that every branch fits, that is the result;
// otherwise all branches must have the same type.
func (c *checker) unify(pos diag.Pos, what string, ts []Type, want Type) Type {
	var vals []Type
	for _, t := range ts {
		if t == Invalid {
			return Invalid
		}
		if t != Never {
			vals = append(vals, t)
		}
	}
	if len(vals) == 0 {
		return Never
	}
	if want != nil && isValue(want) {
		fits := true
		for _, t := range vals {
			if !assignable(t, want) {
				fits = false
			}
		}
		if fits {
			return want
		}
	}
	for _, t := range vals[1:] {
		if !identical(t, vals[0]) {
			c.errorf(pos, "%s different types: %s and %s", what, vals[0], t)
			return Invalid
		}
	}
	return vals[0]
}

func (c *checker) returnExpr(e *syntax.Return) {
	want := c.fn.Result
	if e.Value == nil {
		if want != Unit {
			c.errorf(e.Pos, "function %s must return a value of type %s", c.fn.Decl.Name, want)
		}
		return
	}
	if want == Unit {
		c.expr(e.Value)
		c.errorf(e.Value.Position(), "function %s does not return a value", c.fn.Decl.Name)
		return
	}
	t := c.exprWant(e.Value, want)
	if !assignable(t, want) {
		c.errorf(e.Value.Position(), "function %s returns %s, but this returns %s", c.fn.Decl.Name, want, t)
	}
}

// conversion checks a numeric conversion such as `toInt8(x)`. A
// constant is converted at compile time and must fit. Otherwise the
// result is the target type if every value of x's type fits, and
// `Target | OutOfRange` if some may not.
func (c *checker) conversion(e *syntax.Call, fname string) Type {
	to := conversions[fname]
	arg := e.Args[0]
	if constValue(arg) != nil {
		if c.exprWant(arg, to) == Invalid {
			return Invalid
		}
		return to
	}
	from := c.expr(arg)
	if from == Invalid {
		return Invalid
	}
	if !IsNumeric(from) {
		c.errorf(arg.Position(), "%s needs a number, found %s", fname, from)
		return Invalid
	}
	if alwaysFits(from, to) {
		c.info.Conversions[e] = &Conversion{From: from, To: to}
		return to
	}
	c.info.Conversions[e] = &Conversion{From: from, To: to, Checked: true}
	return newUnion([]Type{to, c.info.OutOfRange})
}
