package check

import (
	"fmt"
	"go/constant"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Package is a bork package: a directory of .bork files.
type Package struct {
	Path string // the import path; "" for the prelude
	Name string // the last element of the path
	// Root is set for the package being built or tested; the others are
	// the packages it imports, directly or not.
	Root bool
	// GoPrefix starts the Go names of the package's functions and types
	// ("" for the root package and the prelude).
	GoPrefix string
	// Funcs holds the package's own functions by name.
	Funcs   map[string]*Func
	types   map[string]*typeEntry
	imports map[string]*Package // by the name files use for them
	used    map[string]bool     // the imports that are used
	classes map[string]*Class
	// instances holds the package's own class instances, and inScope
	// those its code can use: its own, the prelude's, and those it uses.
	instances []*ClassInstance
	inScope   []*ClassInstance
	// bundles holds the package's named sets of instances.
	bundles map[string]*bundle
}

// TypeNamed is the record, sealed, or resource type the package
// declares with the given name, or nil.
func (p *Package) TypeNamed(name string) Type {
	if e := p.types[name]; e != nil && e.decl.Kind != syntax.AliasType {
		return e.typ
	}
	return nil
}

// Exported reports whether a name is visible to other packages: it
// starts with an upper-case letter, as in Go.
func Exported(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

// Func is a declared function's signature.
type Func struct {
	Decl *syntax.FuncDecl
	Pkg  *Package
	// Class is set for a class's method (which has no body), and Of for
	// the implementation of a method in an instance.
	Class *Class
	Of    *ClassInstance
	// Derived is set for an instance method the compiler writes.
	Derived *Derived
	// TypeParams lists a generic function's type parameters.
	TypeParams []*TypeParam
	Params     []Type
	Result     Type
	// Prelude is set for the built-in functions of prelude.bork.
	Prelude bool
	// Synthetic is set for a predicate that stands for a function
	// parameter (see facts.go); it has no body to run.
	Synthetic bool
	// Test is set for the function checking a test's body.
	Test *syntax.TestDecl
	// Calls lists the functions this function's body calls.
	Calls []*Func
	// ParamConstraints holds each parameter's where clause, and
	// ResultConstraints what the result promises (per union member).
	ParamConstraints  [][]*Constraint
	ResultConstraints []MemberConstraints
}

// QualifiedName is the function's name as code in package from refers
// to it: "money.add" for a function of a package from imports.
func (fn *Func) QualifiedName(from *Package) string {
	if fn.Prelude {
		return fn.Decl.Name
	}
	return qualify(fn.Decl.Name, fn.Pkg, from)
}

// qualify is name, declared in package pkg, as code in package from
// refers to it.
func qualify(name string, pkg, from *Package) string {
	if from == nil || pkg == nil || pkg == from || pkg.Path == "" {
		return name
	}
	for alias, p := range from.imports {
		if p == pkg {
			return alias + "." + name
		}
	}
	return pkg.Name + "." + name
}

// Builtin identifies a function provided by the compiler.
type Builtin int

const (
	BuiltinNone Builtin = iota
	BuiltinPrintln
	BuiltinToString
	BuiltinConvert // toInt8(x), toFloat(x), ...
	BuiltinPanic
	BuiltinAssert      // assert(cond)
	BuiltinAssertEqual // assertEqual(actual, expected)
)

var builtins = map[string]Builtin{
	"println":     BuiltinPrintln,
	"toString":    BuiltinToString,
	"panic":       BuiltinPanic,
	"assert":      BuiltinAssert,
	"assertEqual": BuiltinAssertEqual,
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

// Info is what the checker learned about a program: the root package,
// and the packages it imports. Later passes (code generation) read it
// instead of re-deriving types.
type Info struct {
	// Packages lists the program's packages.
	Packages []*Package
	// Classes and ClassInstances list every class and instance.
	Classes        []*Class
	ClassInstances []*ClassInstance
	// Funcs holds the functions visible to the root package by name: its
	// own, and the prelude's that it does not replace.
	Funcs map[string]*Func
	// FuncOf holds every declared function, including prelude functions
	// the package replaced (the prelude still uses its own).
	FuncOf map[*syntax.FuncDecl]*Func
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
	// Defs records what each identifier refers to: a *syntax.Param, a
	// *syntax.Binding, or the pattern node that bound it.
	Defs map[*syntax.Ident]any
	// BindingConstraints holds the where clauses of typed bindings.
	BindingConstraints map[*syntax.Binding][]*Constraint
	// Tests holds the root package's tests, each checked as a function
	// without parameters.
	Tests []*Func
	// Rules holds the inference rules of every package.
	Rules []*Rule
	// Instances describes every call of a declared function, and
	// FuncRefs every function used as a value, with type arguments for
	// generic functions.
	Instances map[*syntax.Call]*Instance
	FuncRefs  map[*syntax.Ident]*Instance
	// LambdaParams holds the parameters of lambdas.
	LambdaParams map[*syntax.Param]bool
	// PatSources records, for a name bound by the top-level pattern of a
	// match arm, the matched expression and the union member the pattern
	// narrowed it to (nil if it did not narrow).
	PatSources map[any]*PatSource
}

// PatSource is where a value bound by a match pattern came from.
//
// A name bound inside the pattern (`Option.Some { value: v }`) has the
// field path to it from the subject (".value"), and the Field.
type PatSource struct {
	Subject syntax.Expr
	Member  Type
	Path    string
	Field   *Field
}

// Program type-checks a program: the root package (whose import path is
// root), and the packages it imports. files holds the files of all of
// them, and the prelude's.
func Program(files []*syntax.File, root string, diags *diag.List) *Info {
	c := &checker{
		diags: diags,
		info: &Info{
			Funcs:            map[string]*Func{},
			FuncOf:           map[*syntax.FuncDecl]*Func{},
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
			Defs:             map[*syntax.Ident]any{},

			BindingConstraints: map[*syntax.Binding][]*Constraint{},
			PatSources:         map[any]*PatSource{},
			Instances:          map[*syntax.Call]*Instance{},
			FuncRefs:           map[*syntax.Ident]*Instance{},
			LambdaParams:       map[*syntax.Param]bool{},
		},
	}
	c.declarePackages(files, root)
	// Pass 1: declare types, then resolve their bodies, so types can
	// refer to each other regardless of declaration order.
	for _, f := range files {
		c.inFile(f)
		for _, td := range f.Types {
			c.declareType(td, f.Prelude)
		}
	}
	for _, f := range files {
		c.inFile(f)
		for _, td := range f.Types {
			if e := c.pkg.types[td.Name]; e != nil && e.decl == td {
				c.resolveDecl(e)
			}
		}
	}
	c.checkRecordCycles()
	c.info.OutOfRange = c.info.Named["OutOfRange"]
	c.declareClasses(files)
	c.declareClassMethods()
	// Pass 2: collect function signatures, so functions can call each
	// other regardless of declaration order.
	for _, f := range files {
		c.inFile(f)
		for _, fd := range f.Funcs {
			if fd.Instance == nil {
				c.declareFunc(fd, f.Prelude)
			}
		}
	}
	c.declareInstances(files)
	c.declareDerived(files)
	c.resolveUses(files)
	c.resolveDerived()
	for name, fn := range c.preludePkg.Funcs {
		c.info.Funcs[name] = fn
	}
	for name, fn := range c.rootPkg.Funcs {
		c.info.Funcs[name] = fn
	}
	// Where clauses refer to predicates, so they are resolved once all
	// functions are declared.
	c.resolveConstraints(files)
	c.checkRules(files)
	// Pass 3: check bodies.
	for _, f := range files {
		for _, fd := range f.Funcs {
			if fn := c.info.FuncOf[fd]; fn != nil {
				c.checkFunc(fn)
			}
		}
	}
	names := map[string]diag.Pos{}
	for _, f := range files {
		if f.Prelude || f.Package != root {
			continue
		}
		c.inFile(f)
		for _, td := range f.Tests {
			c.checkTest(td, names)
		}
	}
	for _, f := range files {
		if f.Prelude {
			continue
		}
		pkg := c.pkgs[f.Package]
		for _, imp := range f.Imports {
			if pkg.imports[imp.Name] != nil && !pkg.used[imp.Name] {
				c.errorf(imp.Pos, "%s is imported but not used", imp.Path)
			}
		}
	}
	return c.info
}

// checkTest checks a test's body as a function without parameters.
func (c *checker) checkTest(td *syntax.TestDecl, names map[string]diag.Pos) {
	if prev, ok := names[td.Name]; ok {
		c.errorf(td.Pos, "test %q is already declared at %s", td.Name, prev)
	}
	names[td.Name] = td.Pos
	fn := &Func{Decl: &syntax.FuncDecl{Pos: td.Pos, Name: "test", Body: td.Body}, Pkg: c.pkg, Result: Unit, Test: td}
	c.info.Tests = append(c.info.Tests, fn)
	c.checkFunc(fn)
}

// declarePackages sets up the packages the files belong to, and their
// imports.
func (c *checker) declarePackages(files []*syntax.File, root string) {
	c.pkgs = map[string]*Package{}
	c.preludePkg = &Package{Funcs: map[string]*Func{}, types: map[string]*typeEntry{}, imports: map[string]*Package{}, used: map[string]bool{}, classes: map[string]*Class{}}
	byName := map[string]int{}
	for _, f := range files {
		if f.Prelude || c.pkgs[f.Package] != nil {
			continue
		}
		name := f.Package[strings.LastIndex(f.Package, "/")+1:]
		pkg := &Package{Path: f.Package, Name: name, Root: f.Package == root,
			Funcs: map[string]*Func{}, types: map[string]*typeEntry{}, imports: map[string]*Package{}, used: map[string]bool{}, classes: map[string]*Class{}}
		if !pkg.Root {
			byName[name]++
			pkg.GoPrefix = "_" + name + "_"
			if n := byName[name]; n > 1 {
				pkg.GoPrefix = "_" + name + strconv.Itoa(n) + "_"
			}
		}
		c.pkgs[f.Package] = pkg
		c.info.Packages = append(c.info.Packages, pkg)
	}
	c.rootPkg = c.pkgs[root]
	if c.rootPkg == nil {
		c.rootPkg = &Package{Path: root, Root: true, Funcs: map[string]*Func{}, types: map[string]*typeEntry{}, imports: map[string]*Package{}, used: map[string]bool{}, classes: map[string]*Class{}}
	}
	for _, f := range files {
		if f.Prelude {
			continue
		}
		pkg := c.pkgs[f.Package]
		for _, imp := range f.Imports {
			target := c.pkgs[imp.Path]
			switch {
			case target == nil:
				c.errorf(imp.Pos, "package %s is not loaded (compiler bug)", imp.Path)
			case target == pkg:
				c.errorf(imp.Pos, "a package cannot import itself")
			case pkg.imports[imp.Name] != nil && pkg.imports[imp.Name] != target:
				c.errorf(imp.Pos, "%s already names the imported package %s; import this one with another name: import other %q", imp.Name, pkg.imports[imp.Name].Path, imp.Path)
			default:
				pkg.imports[imp.Name] = target
			}
		}
	}
}

// inFile makes declarations of the file's package the ones in view.
func (c *checker) inFile(f *syntax.File) {
	if f.Prelude {
		c.pkg = c.preludePkg
	} else {
		c.pkg = c.pkgs[f.Package]
	}
}

type checker struct {
	diags *diag.List
	info  *Info
	// The program's packages by import path; the prelude; the root
	// package; and the package whose code is being checked.
	pkgs       map[string]*Package
	preludePkg *Package
	rootPkg    *Package
	pkg        *Package

	// Per-function state.
	fn     *Func
	scopes []map[string]*local
	// inPrelude is set while prelude code is checked, which sees only
	// the prelude.
	inPrelude bool
	// typeParams holds the type parameters in scope.
	typeParams map[string]*TypeParam
	// lambdaDepth counts the lambdas being checked around the current
	// expression.
	lambdaDepth int
}

type local struct {
	typ  Type
	node any // the binding's syntax node; nil for parameters
	decl any // what an identifier refers to (see Info.Defs)
	used bool
}

// funcNamed looks up a function by name, as the code being checked
// sees it: its package's functions, then the prelude's (prelude code
// sees only the prelude). A qualified name ("money.add") is looked up in
// an imported package, which must export it.
func (c *checker) funcNamed(name string) (*Func, bool) {
	if c.inPrelude {
		fn, ok := c.preludePkg.Funcs[name]
		return fn, ok
	}
	if pkg, n, ok := c.qualified(name); ok {
		fn, ok := pkg.Funcs[n]
		// The methods of an exported class are visible with it.
		return fn, ok && (Exported(n) || fn.Class != nil && Exported(fn.Class.Name))
	}
	if fn, ok := c.pkg.Funcs[name]; ok {
		return fn, true
	}
	fn, ok := c.preludePkg.Funcs[name]
	return fn, ok
}

// qualified splits a qualified name "money.add" into the imported
// package and the name in it.
func (c *checker) qualified(name string) (*Package, string, bool) {
	i := strings.IndexByte(name, '.')
	if i < 0 {
		return nil, "", false
	}
	pkg := c.pkg.imports[name[:i]]
	if pkg != nil {
		c.pkg.used[name[:i]] = true
	}
	return pkg, name[i+1:], pkg != nil
}

// notFound explains why a qualified name was not found: it is not
// exported, or does not exist. It returns "" for other names.
func (c *checker) notFound(name string) string {
	pkg, n, ok := c.qualified(name)
	if !ok {
		return ""
	}
	fn, isFunc := pkg.Funcs[n]
	_, isType := pkg.types[n]
	if isFunc && fn.Class != nil && !Exported(fn.Class.Name) {
		return fmt.Sprintf("%s is a method of class %s, which package %s does not export", n, fn.Class.Name, pkg.Path)
	}
	if (isFunc || isType) && !Exported(n) {
		return fmt.Sprintf("%s is not exported by package %s (only names starting with an upper-case letter are)", n, pkg.Path)
	}
	return fmt.Sprintf("package %s has no %s", pkg.Path, n)
}

func (c *checker) errorf(pos diag.Pos, format string, args ...any) {
	c.diags.Add(pos, format, args...)
}

func (c *checker) declareFunc(fd *syntax.FuncDecl, prelude bool) {
	if _, ok := builtins[fd.Name]; ok {
		c.errorf(fd.Pos, "%s is a built-in function and cannot be redefined", fd.Name)
		return
	}
	// A function of the package replaces a prelude function of the
	// same name, so new prelude functions never break programs.
	if prev, ok := c.pkg.Funcs[fd.Name]; ok {
		c.errorf(fd.Pos, "function %s is already declared at %s", fd.Name, prev.Decl.Pos)
		return
	}
	if _, ok := c.pkg.imports[fd.Name]; ok {
		c.errorf(fd.Pos, "%s is already the name of an imported package", fd.Name)
		return
	}
	if c.isTypeName(fd.Name) {
		c.errorf(fd.Pos, "%s is already the name of a type", fd.Name)
		return
	}
	if fd.IsPred && len(fd.Params) == 0 {
		c.errorf(fd.Pos, "pred %s needs a parameter: the value it is about", fd.Name)
		return
	}
	fn := &Func{Decl: fd, Pkg: c.pkg, Prelude: prelude}
	fn.TypeParams = c.declareTypeParams(fd, prelude)
	if r := fd.Result; r != nil && r.Name == "Never" && len(r.Args) == 0 && r.Func == nil && len(r.Union) == 0 {
		// A function that never returns, such as exit.
		fn.Result = Never
	} else {
		fn.Result = c.resolveType(fd.Result)
	}
	for _, p := range fd.Params {
		fn.Params = append(fn.Params, c.resolveType(p.Type))
	}
	c.typeParams = nil
	c.pkg.Funcs[fd.Name] = fn
	c.info.FuncOf[fd] = fn
}

func (c *checker) checkFunc(fn *Func) {
	c.fn = fn
	c.pkg = fn.Pkg
	c.inPrelude = fn.Prelude
	defer func() { c.inPrelude = false }()
	c.useTypeParams(fn)
	defer c.useTypeParams(nil)
	c.scopes = []map[string]*local{{}}
	for i, p := range fn.Decl.Params {
		// The prelude's parameter names do not depend on user code.
		if !fn.Prelude && c.nameTaken(p.Name, p.Pos) {
			continue
		}
		if fn.Params[i] == Unit {
			c.errorf(p.Type.Pos, "parameter %s cannot have type Unit", p.Name)
		}
		c.scopes[0][p.Name] = &local{typ: fn.Params[i], decl: p}
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
		if fn.Test != nil {
			c.errorf(fn.Decl.Body.Tail.Position(), "value of type %s is not used (a test returns no value)", bodyType)
		} else {
			c.errorf(fn.Decl.Body.Tail.Position(), "value of type %s is not used (function %s returns no value)", bodyType, fn.Decl.Name)
		}
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
	// Prelude functions may be shadowed: their names (count, find,
	// last, ...) are too useful to take away from locals.
	if _, ok := c.pkg.Funcs[name]; ok && c.pkg != c.preludePkg {
		c.errorf(pos, "%s is already the name of a function (bork does not allow shadowing)", name)
		return true
	}
	if _, ok := c.pkg.imports[name]; ok {
		c.errorf(pos, "%s is already the name of an imported package (bork does not allow shadowing)", name)
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
	c.scopes[len(c.scopes)-1][name] = &local{typ: t, node: node, decl: node}
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

// scopeExpr checks `scope s { ... }`: its body is a block that sees s,
// of type Scope. Whether resources outlive their scopes is checked
// later (see lifetimes.go).
func (c *checker) scopeExpr(e *syntax.ScopeExpr, want Type) Type {
	c.pushScope()
	defer c.popScope()
	if !c.nameTaken(e.Name, e.Pos) {
		c.scopes[len(c.scopes)-1][e.Name] = &local{typ: Scope, decl: e, used: true}
	}
	return c.block(e.Body, want)
}

func stmtPos(s syntax.Stmt) diag.Pos {
	switch s := s.(type) {
	case *syntax.Binding:
		return s.Pos
	case *syntax.ExprStmt:
		return s.X.Position()
	case *syntax.TrustStmt:
		return s.Pos
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
		if declared != nil {
			// A declared type holds even when the value has errors, so
			// that they do not spread to its uses.
			if t != Invalid && declared != Invalid && !assignable(t, declared) {
				c.errorf(s.Value.Position(), "%s must be %s, found %s", s.Name, declared, t)
			}
			t = declared
		}
		c.info.Bindings[s] = t
		if s.Type != nil && t != Invalid {
			c.info.BindingConstraints[s] = c.constraintsOf(s.Type, t, c.paramScope())
		}
		c.bind(s.Name, s.Pos, t, s)
		return Unit
	case *syntax.TrustStmt:
		c.expr(s.Call)
		if fn := c.info.CallFuncs[s.Call]; fn != nil && !fn.Decl.IsPred {
			c.errorf(s.Call.Position(), "trust needs a predicate call, but %s is a function", fn.Decl.Name)
		}
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
		return c.record(e, c.ident(e, want))
	case *syntax.Unary:
		return c.record(e, c.unary(e))
	case *syntax.Binary:
		return c.record(e, c.binary(e, want))
	case *syntax.Call:
		return c.record(e, c.call(e, want))
	case *syntax.Lambda:
		return c.record(e, c.lambda(e, want, nil))
	case *syntax.ListLit:
		return c.record(e, c.listLit(e, want))
	case *syntax.If:
		return c.record(e, c.ifExpr(e, want))
	case *syntax.Block:
		return c.block(e, want)
	case *syntax.ScopeExpr:
		return c.record(e, c.scopeExpr(e, want))
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

func (c *checker) ident(e *syntax.Ident, want Type) Type {
	if l := c.lookup(e.Name); l != nil {
		l.used = true
		c.info.Defs[e] = l.decl
		return l.typ
	}
	if fn, ok := c.funcNamed(e.Name); ok {
		return c.funcValue(e, fn, want)
	}
	if _, ok := builtins[e.Name]; ok {
		c.errorf(e.Pos, "built-in %s must be called", e.Name)
		return Invalid
	}
	if c.isTypeName(e.Name) {
		c.errorf(e.Pos, "%s is a type, not a value", e.Name)
		return Invalid
	}
	if why := c.notFound(e.Name); why != "" {
		c.errorf(e.Pos, "%s", why)
		return Invalid
	}
	if _, ok := c.pkg.imports[e.Name]; ok {
		c.errorf(e.Pos, "%s is a package; use one of its names, as in %s.Name", e.Name, e.Name)
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
		if !comparable(x) {
			c.errorf(e.Pos, "cannot compare values of type %s with %s (lists, functions, and values of type parameters have no ==)", x, op)
			return Invalid
		}
		return Bool
	}
	return Invalid
}

func (c *checker) call(e *syntax.Call, want Type) Type {
	id, ok := e.Fun.(*syntax.Ident)
	if len(e.TypeArgs) > 0 {
		builtin := false
		if ok {
			_, builtin = builtins[id.Name]
		}
		if !ok || c.lookup(id.Name) != nil || builtin {
			c.errorf(e.Pos, "only a declared generic function can be given type arguments")
		}
	}
	if !ok || c.lookup(id.Name) != nil {
		return c.callValue(e)
	}
	if b, ok := builtins[id.Name]; ok && c.lookup(id.Name) == nil {
		c.info.CallBuiltins[e] = b
		return c.builtinCall(e, id.Name, b)
	}
	fn, ok := c.funcNamed(id.Name)
	if !ok {
		switch {
		case c.isTypeName(id.Name):
			c.errorf(id.Pos, "%s is a type; build a record with %s { field: value, ... }", id.Name, id.Name)
		case c.notFound(id.Name) != "":
			c.errorf(id.Pos, "%s", c.notFound(id.Name))
		default:
			c.errorf(id.Pos, "undefined function: %s", id.Name)
		}
		for _, a := range e.Args {
			c.expr(a)
		}
		return Invalid
	}
	return c.callFunc(e, id, fn, want)
}

func (c *checker) builtinCall(e *syntax.Call, fname string, b Builtin) Type {
	if b == BuiltinAssertEqual {
		if len(e.Args) != 2 {
			c.errorf(e.Pos, "assertEqual takes 2 arguments (actual, expected), but %d were given", len(e.Args))
			for _, a := range e.Args {
				c.expr(a)
			}
			return Unit
		}
		actual := c.expr(e.Args[0])
		expected := c.exprWant(e.Args[1], actual)
		switch {
		case actual == Invalid || expected == Invalid:
		case !identical(actual, expected) && !assignable(expected, actual):
			c.errorf(e.Args[1].Position(), "cannot compare %s with %s", actual, expected)
		case !isValue(actual) || !comparable(actual):
			c.errorf(e.Pos, "cannot compare values of type %s", actual)
		}
		return Unit
	}
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
	case BuiltinAssert:
		if t := c.expr(e.Args[0]); t != Bool && t != Invalid {
			c.errorf(e.Args[0].Position(), "assert needs a Bool, found %s", t)
		}
		return Unit
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
	if e.Else != nil && want == nil && c.branchNeedsContext(e.Then) && !c.branchNeedsContext(e.Else) {
		// The then-branch's type comes from the else-branch: `[]`.
		elseT := c.exprWant(e.Else, nil)
		thenT := c.block(e.Then, elseT)
		return c.unify(e.Pos, "if-branches have", []Type{thenT, elseT}, nil)
	}
	thenT := c.block(e.Then, want)
	if e.Else == nil {
		// Without else, the if is only run for its effect.
		if isValue(thenT) {
			c.errorf(e.Then.Pos, "if without else cannot produce a value (found %s); add an else branch or drop the value", thenT)
		}
		return Unit
	}
	if want == nil && c.branchNeedsContext(e.Else) {
		want = thenT
	}
	elseT := c.exprWant(e.Else, want)
	return c.unify(e.Pos, "if-branches have", []Type{thenT, elseT}, want)
}

// branchNeedsContext reports whether a branch's value (a block's tail)
// gets its type from the context, as `[]` does.
func (c *checker) branchNeedsContext(x syntax.Expr) bool {
	if b, ok := x.(*syntax.Block); ok {
		if b.Tail == nil {
			return false
		}
		return c.branchNeedsContext(b.Tail)
	}
	_, isLambda := x.(*syntax.Lambda)
	return !isLambda && c.needsContext(x)
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
	if c.lambdaDepth > 0 {
		c.errorf(e.Pos, "return cannot be used in a lambda; a lambda's value is its body's value")
		if e.Value != nil {
			c.expr(e.Value)
		}
		return
	}
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

// paramScope maps the current function's parameters to their types.
func (c *checker) paramScope() map[string]Type {
	scope := map[string]Type{}
	if c.fn != nil {
		for i, p := range c.fn.Decl.Params {
			scope[p.Name] = c.fn.Params[i]
		}
	}
	return scope
}
