package check

import (
	"fmt"
	"go/constant"
	"go/token"
	"strconv"
	"strings"
	"unicode/utf8"

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
	Funcs        map[string]*Func
	bindings     map[string]*PackageBinding
	scriptLocals map[string]diag.Pos
	// methods holds the package's methods, by receiver type (see
	// methodKey) and name.
	methods       map[string]map[string]*Func
	types         map[string]*typeEntry
	imports       map[string]*Package // by the name files use for them
	used          map[string]bool     // the imports that are used
	classes       map[string]*Class
	deriveHelpers map[string]*syntax.FuncDecl
	// instances holds the package's own class instances, and inScope
	// those its code can use: its own, the prelude's, and those it uses.
	instances []*ClassInstance
	inScope   []*ClassInstance
	// bundles holds the package's named sets of instances.
	bundles map[string]*bundle
	// ambients holds the package's ambient values by name.
	ambients map[string]*Ambient
}

// TypeNamed is the record, sealed, or resource type the package
// declares with the given name, or nil.
func (p *Package) TypeNamed(name string) Type {
	if e := p.types[name]; e != nil && e.decl.Kind != syntax.AliasType {
		return e.typ
	}
	return nil
}

// Imported resolves the Bork package alias visible to this package.
func (p *Package) Imported(alias string) *Package { return p.imports[alias] }

// ClassNamed returns a class declared by this package.
func (p *Package) ClassNamed(name string) *Class { return p.classes[name] }

// Exported reports whether a name is visible to other packages: it
// starts with an upper-case letter, as in Go.
func Exported(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

// Func is a declared function's signature.
type Func struct {
	TemplatePkg         *Package
	TemplateScope       *ClassInstance
	RuntimePackageReads bool

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
	// Effects is what the function declares it may do (`uses io`).
	Effects Effects
	// Needs lists the ambient values it reads (`needs traceId`), in
	// the order of its hidden parameters, and NeedVars their variables.
	Needs    []*FuncNeed
	NeedVars []*Var
	// Prelude is set for the built-in functions of prelude.
	Prelude bool
	// TrackCaller adds a hidden source location for internal helper diagnostics.
	TrackCaller bool
	// Synthetic is set for a predicate that stands for a function
	// parameter (see facts.go); it has no body to run.
	Synthetic bool
	// Test is set for the function checking a test's body.
	Test *syntax.TestDecl
	// MockOf is set for the body of a `mock` statement in a test, checked
	// as a function with the signature of the function it mocks. Its
	// Decl.Params are the names the mock gives the parameters, and
	// MockIn is the test it is in.
	MockOf, MockIn *Func
	// TailRec is set when the function declares `uses tailrec`, at
	// TailRecPos: its recursive calls must compile as jumps.
	TailRec    bool
	TailRecPos diag.Pos
	// Calls lists the functions this function's body calls.
	Calls []*Func
	// ParamVars are the variables of the parameters, and Body the typed
	// tree of the body (nil for a function implemented in Go).
	ParamVars []*Var
	Body      *Block
	Requires  Expr
	// ParamIn holds, per parameter, the index of the parameter (a Scope
	// or an OwnedScope) it is declared to belong to (`conn: Conn in
	// prev`), or -1.
	ParamIn []int
	// ParamConstraints holds each parameter's where clause, and
	// ResultConstraints what the result promises (per union member).
	// defaultsChecked is set once the parameters' defaults are checked.
	defaultsChecked   bool
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
	BuiltinDbg
	BuiltinTodo
	BuiltinCallerLocation
	BuiltinAssert         // assert(cond)
	BuiltinAssertEqual    // assertEqual(actual, expected)
	BuiltinAssertSnapshot // assertSnapshot(x)
	BuiltinAssemble
	BuiltinAssertIsFailure
)

var builtins = map[string]Builtin{
	"println":                BuiltinPrintln,
	"toString":               BuiltinToString,
	"panic":                  BuiltinPanic,
	"dbg":                    BuiltinDbg,
	"todo":                   BuiltinTodo,
	"compilerCallerLocation": BuiltinCallerLocation,
	"assert":                 BuiltinAssert,
	"assertEqual":            BuiltinAssertEqual,
	"assertSnapshot":         BuiltinAssertSnapshot,
	"assemble":               BuiltinAssemble,
	"assembleAll":            BuiltinAssemble,
	"assembleRecord":         BuiltinAssemble,
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
// and the packages it imports. Later passes (lifetimes, facts, and code
// generation) read it, and the typed tree of each function body
// (Func.Body), instead of re-deriving types.
// InterpolationSource retains source expressions hidden by named interpolation lowering.
// Holes refer to the expressions already lowered for execution.
type InterpolationSource struct {
	Prefix  SourceSpan
	Factory Expr
	Holes   []Expr
}

type Info struct {
	// predicateRefs retains checked where-clause identities for editor queries.
	predicateRefs map[diag.Pos]*Constraint

	Interpolations       map[Expr]*InterpolationSource
	InterpolationBatches []*InterpolationBatch

	PackageBindings []*PackageBinding
	packageGraph    map[*Func]packageDependencies

	// GoImportNames resolves unsafe Go imports without mutating source syntax.
	GoImportNames map[*syntax.GoCode]map[string]string

	// Packages lists the program's packages.
	Packages []*Package
	// Ambients lists the program's ambient declarations, in order.
	Ambients []*Ambient
	// Classes and ClassInstances list every class and instance.
	Classes        []*Class
	ClassInstances []*ClassInstance
	// Funcs holds the functions visible to the root package by name: its
	// own, and the prelude's that it does not replace.
	Funcs map[string]*Func
	// FuncOf holds every declared function, including prelude functions
	// the package replaced (the prelude still uses its own).
	FuncOf map[*syntax.FuncDecl]*Func
	// ExpandedFunctions holds typed derive methods and helpers without changing parsed declarations.
	ExpandedFunctions []*Func
	// Named holds every declared type: *Record, *Sealed, or (for an
	// alias) the aliased type.
	Named map[string]Type
	// TypeOrder lists declared records and sealed types in source order.
	TypeOrder []Type
	// OutOfRange is the prelude's OutOfRange record.
	OutOfRange Type
	// Tests holds the root package's tests, each checked as a function
	// without parameters.
	Tests []*Func
	// Lifetimes holds the scopes each expression's value belongs to, and
	// VarLifetimes each variable's (see lifetimes.go), for queries.
	Lifetimes    map[Expr][]string
	VarLifetimes map[*Var][]string
	// Ownership says, for each use of a resource variable and each one
	// bound (VarOwnership), whether it was acquired here and can be
	// moved, is borrowed, is kept by a task or a channel, or was moved
	// (see moves.go), for queries.
	Ownership    map[Expr]string
	VarOwnership map[*Var]string
	// Mocks holds the bodies of the tests' `mock` statements (see
	// Func.MockOf), and mocks them by statement.
	Mocks []*Func
	mocks map[*syntax.MockStmt]*Func
	// TailCalls says how each call of a function by itself is
	// compiled; tailJumps holds the functions with a call compiled as
	// a jump (see CheckTailCalls).
	TailCalls map[*Call]*TailCall
	tailJumps map[*Func]bool
	// MockCalls are the records of calls of the functions tests mock
	// with a handle (see callRecord), declared only in test builds.
	MockCalls     map[*Func]*Record
	MockCallOrder []*Record
	mockHandles   map[*syntax.MockStmt]Type
	testSites     map[*Func][]testSite
	// Rules holds the inference rules of every package.
	Rules []*Rule
	// GoBindings holds every checked binding to a Go function
	// (`unsafe go "os.Getenv"`).
	GoBindings map[*Func]*GoBinding
	// Comptimes lists explicit build-time computations in lowering order.
	Comptimes            []*Comptime
	comptimeSyntax       []*syntax.Comptime
	comptimeCaptureDecls map[*syntax.Comptime][]any
	// Embeds lists compile-time asset requests in source order.
	Embeds        []*Embedded
	embedCalls    map[*syntax.Call]*Embedded
	BuildReads    []*BuildRead
	buildCalls    map[*syntax.Call]*BuildRead
	assemblyCalls map[*syntax.Call]*assemblyExpansion
	// selects holds the blocks select expressions are lowered to.
	selects map[*syntax.Select]*syntax.Block
	// selectMatches are the matches selects are lowered to.
	selectMatches          map[*syntax.Match]bool
	assemblyTypes          map[*syntax.TypeExpr]Type
	deriveSourceKinds      map[diag.Pos]string
	shapeReadOwners        map[*syntax.Selector]Type
	shapeTypeFacts         map[*syntax.TypeExpr][]*Constraint
	shapeDefaults          map[*syntax.Call]*Field
	assemblyNames          map[any]string
	interpolatorCalls      map[*syntax.Interp]*syntax.Call
	interpolatorValidators map[*syntax.Interp]*Dict
	interpolatorFactories  map[*syntax.Interp]*syntax.Call
	conversionCalls        map[*syntax.Call]*syntax.Block
	conversionRecords      map[*syntax.RecordLit]*Record
	conversionInputs       map[syntax.Expr]bool
	assemblyValueCalls     map[*syntax.Call]*Instance
	tupleBindingValues     map[any]syntax.Expr

	// What the checker records about the syntax as it checks it, which
	// the typed tree is built from (see lower.go).

	// types records the resulting type of every expression. optionPayloads
	// retains the checked payload type when an implicit Some is inserted.
	types          map[syntax.Expr]Type
	optionPayloads map[syntax.Expr]Type
	// callFuncs and callBuiltins record which function each call
	// targets.
	callFuncs           map[*syntax.Call]*Func
	callBuiltins        map[*syntax.Call]Builtin
	seqCalls            map[*syntax.Call]*seqCallInfo
	generateConstraints map[*syntax.Generate][]*Constraint
	// callArgs holds the arguments of every call of a declared function
	// in parameter order, including a method's receiver and the defaults
	// of unfilled parameters. The call's syntax is left as written.
	callArgs map[*syntax.Call][]syntax.Expr
	// callOrder maps source evaluation order to parameter indices for
	// calls with labels, including receiver and inserted defaults.
	callOrder map[*syntax.Call][]int
	// callTypeArgs holds a call's explicit type arguments as written, one
	// per type parameter (nil for those a method's receiver decides).
	callTypeArgs map[*syntax.Call][]*syntax.TypeExpr
	// instances describes every call of a declared function, and
	// funcRefs every function used as a value, with type arguments for
	// generic functions.
	instances map[*syntax.Call]*Instance
	funcRefs  map[syntax.Expr]*Instance
	// needArgs records what each call or reference of a function that
	// needs ambient values passes for them.
	needArgs map[syntax.Expr][]needSource
	// withAmbients holds the ambient value each with binding binds.
	withAmbients map[*syntax.WithBinding]*Ambient
	// propagatedHeaders maps the header names propagated ambients are
	// sent under (lower case) to their declarations.
	propagatedHeaders map[string]*Ambient
	// recordTargets records what each record literal builds: a *Record
	// or a *Variant.
	sourceDefinitions      map[diag.Pos]diag.Pos
	sourceNames            map[diag.Pos]string
	writtenTypes           map[*syntax.TypeExpr]Type
	constructorConstraints map[syntax.Expr][]*Constraint
	variantCalls           map[*syntax.Call]*syntax.RecordLit
	recordTargets          map[*syntax.RecordLit]any
	recordInits            map[*syntax.RecordLit][]*syntax.FieldInit
	fieldDefaults          map[*Field]syntax.Expr
	typeUses               map[Type]diag.Pos
	exprOwners             map[syntax.Expr]*Func
	// selectorVariants records selectors that name a field-less variant
	// (`Shape.Empty`); other selectors are field accesses.
	selectorVariants map[*syntax.Selector]*Variant
	// ownerScopes holds each `b.scope` of an owned scope b, which is
	// scopeOf(b).
	ownerScopes     map[*syntax.Selector]*Func
	contextVariants map[*syntax.ContextName]*Variant
	// armPats holds the checked pattern of every match arm.
	armPats            map[*syntax.Arm]*Pat
	tuplePats          map[*syntax.TupleBinding]*Pat
	patternTests       map[*syntax.Is]*Pat
	patternAssertions  map[*syntax.Call]*assertIsInfo
	patternCertainties map[*syntax.Is]bool
	// tries describes every `?`.
	tries map[*syntax.Try]*TryInfo
	// unused holds bindings whose value is never read: *syntax.Binding,
	// or the pattern node that bound the name.
	unused map[any]bool
	// rebindings records the previous declaration replaced by a sequential binding.
	rebindings map[any]any
	// Carried rebinding (see carried.go): the names each loop carries,
	// the values a break or continue carries out of an iteration, the
	// names joined after an if, match or block statement in a loop body,
	// and the bindings that give a carried name its next value.
	loopCarries     map[*syntax.For]*carryLoop
	loopEdges       map[*syntax.LoopControl][]any
	joins           map[syntax.Expr][]*joinCarry
	carriedBindings map[*syntax.Binding]*carrySlot
	// consts holds the value of every constant expression (number
	// literals and arithmetic on them), already converted to the type
	// recorded in types (or optionPayloads for a promoted value).
	consts map[syntax.Expr]constant.Value
	// bindings records the type of every binding, and
	// bindingConstraints the where clauses of typed ones.
	lazyBindings       map[*syntax.Binding]*LazyDescription
	lazyFields         map[syntax.Expr]*LazyDescription
	fieldRecipes       map[Expr]*LazyDescription
	bindings           map[*syntax.Binding]Type
	bindingConstraints map[*syntax.Binding][]*Constraint
	// conversions describes numeric conversions of non-constant values.
	conversions map[*syntax.Call]*Conversion
	// defs records what each identifier refers to: a *syntax.Param, a
	// *syntax.Binding, a *syntax.ScopeExpr, or the pattern node that
	// bound it.
	defs map[*syntax.Ident]any
	// patSources records, for a name bound by a match arm's pattern,
	// where its value comes from (see patSource).
	patSources map[any]*patSource
}

// args is a call's arguments: for a call of a declared function, as the
// function takes them (see callArgs); otherwise as written.
func (info *Info) args(call *syntax.Call) []syntax.Expr {
	if args, ok := info.callArgs[call]; ok {
		return args
	}
	return call.Args
}

// patSource is where a value bound by a match pattern came from.
//
// A name bound inside the pattern (`Option.Some(v)`) has the
// field path to it from the subject (".value"), and the Field.
type patSource struct {
	Subject syntax.Expr
	Member  Type
	Path    string
	Field   *Field
}

// Program type-checks a program: the root package (whose import path is
// root), and the packages it imports. files holds the files of all of
// them, and the prelude's.
//
// goTypes loads the Go packages that bindings name; it may be nil for a
// program without bindings.
func Program(files []*syntax.File, root string, diags *diag.List, goTypes GoTypes) *Info {
	return ProgramObserved(files, root, diags, goTypes, nil)
}

// ProgramObserved is Program with optional phase notifications for benchmarks.
// observe is called before lowering and contract checks; checking starts in the caller.
func ProgramObserved(files []*syntax.File, root string, diags *diag.List, goTypes GoTypes, observe func(string)) *Info {
	if templateFiles(files) {
		return programWithDeriveRequirements(files, root, diags, goTypes, observe)
	}
	return programObserved(files, root, diags, goTypes, observe, nil, nil)
}

func programObserved(files []*syntax.File, root string, diags *diag.List, goTypes GoTypes, observe func(string), bounds deriveBounds, discovery *deriveDiscovery) *Info {
	c := &checker{
		recordPredicateRefs: true,
		deriveBounds:        bounds,
		deriveDiscovery:     discovery,
		files:               files,
		bindingFiles:        map[string]*syntax.File{},
		diags:               diags,
		info: &Info{
			GoBindings:             map[*Func]*GoBinding{},
			GoImportNames:          checkGoImports(files, diags, goTypes),
			assemblyCalls:          map[*syntax.Call]*assemblyExpansion{},
			selects:                map[*syntax.Select]*syntax.Block{},
			selectMatches:          map[*syntax.Match]bool{},
			interpolatorCalls:      map[*syntax.Interp]*syntax.Call{},
			interpolatorValidators: map[*syntax.Interp]*Dict{},
			interpolatorFactories:  map[*syntax.Interp]*syntax.Call{},
			conversionCalls:        map[*syntax.Call]*syntax.Block{},
			conversionRecords:      map[*syntax.RecordLit]*Record{},
			conversionInputs:       map[syntax.Expr]bool{},
			assemblyTypes:          map[*syntax.TypeExpr]Type{},
			assemblyNames:          map[any]string{},
			Funcs:                  map[string]*Func{},
			FuncOf:                 map[*syntax.FuncDecl]*Func{},
			Named:                  map[string]Type{},
			types:                  map[syntax.Expr]Type{},
			optionPayloads:         map[syntax.Expr]Type{},
			callFuncs:              map[*syntax.Call]*Func{},
			callBuiltins:           map[*syntax.Call]Builtin{},
			seqCalls:               map[*syntax.Call]*seqCallInfo{},
			generateConstraints:    map[*syntax.Generate][]*Constraint{},
			callArgs:               map[*syntax.Call][]syntax.Expr{},
			callOrder:              map[*syntax.Call][]int{},
			callTypeArgs:           map[*syntax.Call][]*syntax.TypeExpr{},
			sourceDefinitions:      map[diag.Pos]diag.Pos{},
			sourceNames:            map[diag.Pos]string{},
			writtenTypes:           map[*syntax.TypeExpr]Type{},
			constructorConstraints: map[syntax.Expr][]*Constraint{},
			variantCalls:           map[*syntax.Call]*syntax.RecordLit{},
			recordTargets:          map[*syntax.RecordLit]any{},
			recordInits:            map[*syntax.RecordLit][]*syntax.FieldInit{},
			fieldDefaults:          map[*Field]syntax.Expr{},
			typeUses:               map[Type]diag.Pos{},
			exprOwners:             map[syntax.Expr]*Func{},
			selectorVariants:       map[*syntax.Selector]*Variant{},
			ownerScopes:            map[*syntax.Selector]*Func{},
			contextVariants:        map[*syntax.ContextName]*Variant{},
			armPats:                map[*syntax.Arm]*Pat{},
			tuplePats:              map[*syntax.TupleBinding]*Pat{},
			patternAssertions:      map[*syntax.Call]*assertIsInfo{},
			patternCertainties:     map[*syntax.Is]bool{},
			patternTests:           map[*syntax.Is]*Pat{},
			tries:                  map[*syntax.Try]*TryInfo{},
			unused:                 map[any]bool{},
			rebindings:             map[any]any{},
			loopCarries:            map[*syntax.For]*carryLoop{},
			loopEdges:              map[*syntax.LoopControl][]any{},
			joins:                  map[syntax.Expr][]*joinCarry{},
			carriedBindings:        map[*syntax.Binding]*carrySlot{},
			consts:                 map[syntax.Expr]constant.Value{},
			lazyBindings:           map[*syntax.Binding]*LazyDescription{},
			lazyFields:             map[syntax.Expr]*LazyDescription{},
			fieldRecipes:           map[Expr]*LazyDescription{},
			bindings:               map[*syntax.Binding]Type{},
			conversions:            map[*syntax.Call]*Conversion{},
			defs:                   map[*syntax.Ident]any{},

			bindingConstraints: map[*syntax.Binding][]*Constraint{},
			patSources:         map[any]*patSource{},
			instances:          map[*syntax.Call]*Instance{},
			funcRefs:           map[syntax.Expr]*Instance{},
			mocks:              map[*syntax.MockStmt]*Func{},
			mockHandles:        map[*syntax.MockStmt]Type{},
			testSites:          map[*Func][]testSite{},
			MockCalls:          map[*Func]*Record{},
		},
	}
	c.appliedWhere = map[*syntax.TypeExpr]bool{}
	c.declarePackages(files, root)
	c.goOpaque = map[string]Type{}
	c.goTypes = loadGoTypes(files, goTypes)
	// Pass 1: declare types, then resolve their bodies, so types can
	// refer to each other regardless of declaration order.
	for _, f := range files {
		c.bindingFiles[f.Path] = f
		c.inFile(f)
		for _, td := range f.Types {
			c.declareType(td, f.Prelude)
		}
	}
	for _, f := range files {
		c.inFile(f)
		for _, td := range f.Types {
			if td.GoName != nil && td.Kind != syntax.RecordType {
				if e := c.pkg.types[td.Name]; e != nil && e.decl == td {
					c.resolveDecl(e)
				}
			}
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
	c.declareClasses(files)
	c.declareClassMethods()
	c.declareDeriveHelpers(files)
	c.declareDeriveTemplates(files)
	c.collectDerived(files)
	c.resolveGoStructs(files)
	c.resolveGoMirrors(files, false)
	c.checkRecordCycles()
	c.info.OutOfRange = c.info.Named["OutOfRange"]
	c.declareAmbients(files)
	c.needsPlacement(files)
	// Pass 2: collect function signatures, so functions can call each
	// other regardless of declaration order.
	for _, f := range files {
		c.inFile(f)
		for _, fd := range f.Funcs {
			if fd.Instance == nil {
				decl := fd
				if fd.Constructor != nil {
					decl = c.expandConstructor(fd)
					if decl == nil {
						continue
					}
				}
				c.declareFunc(decl, f.Prelude)
				if fn := c.info.FuncOf[decl]; decl != fd && fn != nil {
					c.info.FuncOf[fd] = fn
					delete(c.info.FuncOf, decl)
				}
			}
		}
	}
	c.declarePackageBindings(files)
	c.declareInstances(files)
	c.declareDerived()
	c.resolveUses(files)
	c.checkDeriveDefinitions(files)
	for name, fn := range c.preludePkg.Funcs {
		c.info.Funcs[name] = fn
	}
	for name, fn := range c.rootPkg.Funcs {
		c.info.Funcs[name] = fn
	}
	// Where clauses refer to predicates, so they are resolved once all
	// functions are declared.
	for _, f := range files {
		c.inFile(f)
		for _, fd := range f.Funcs {
			if fn := c.info.FuncOf[fd]; fn != nil && fd.IsPred {
				for i, pt := range fn.Params {
					if containsOpaque(pt, map[Type]bool{}) {
						c.bindErr(fd.Params[i].Pos, "predicate %s takes %s, which holds a Go value that can change, so its facts could go stale", fd.Name, pt)
					}
				}
			}
		}
	}
	c.resolveConstraints(files)
	c.ambientConstraints(files)
	c.instanceConstraints()
	c.checkDerivedDuplicates()
	c.ensureAllFieldDefaults()
	for _, checkKey := range c.mapKeyChecks {
		checkKey()
	}
	c.mapKeyChecks = nil
	c.finishGoStructs()
	c.resolveGoMirrors(files, true)
	c.resolveDerived()
	c.checkRules(files)
	for _, t := range c.info.TypeOrder {
		if r, ok := t.(*Record); ok && r.GoMirror != nil {
			r.GoTo = c.toGo(r, r.GoMirror)
			r.GoFrom = c.fromGo(r.GoMirror, r).ok
			for _, t := range r.insts.byKey {
				inst := t.(*Record)
				inst.GoStruct, inst.GoGenerated, inst.GoMirror, inst.GoFields, inst.GoTo, inst.GoFrom = r.GoStruct, r.GoGenerated, r.GoMirror, r.GoFields, r.GoTo, r.GoFrom
			}
			if r.GoStruct && (!r.GoTo || !r.GoFrom) {
				c.errorf(r.GoStructPos, "cannot derive GoStruct for %s: its fields must convert both to and from Go", r.Name)
			}
		}
	}
	c.ensureAllFieldDefaults()
	c.checkBindings(files, c.goTypes)
	c.checkFunctionRequirements()
	for _, binding := range c.info.PackageBindings {
		c.ensurePackageBinding(binding)
	}
	c.expandDeriveBodies()
	// Establish template bounds before ordinary callers select their dictionaries.
	for _, fn := range c.info.ExpandedFunctions {
		c.checkFunc(fn)
	}
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
	c.materializeDefaultUses()
	c.ensureAllFieldDefaults()
	c.unappliedWheres(files)
	c.markUnsafeGoImports(files)
	for _, f := range files {
		if f.Prelude {
			continue
		}
		pkg := c.pkgs[f.Package]
		for _, imp := range f.Imports {
			if pkg.imports[imp.Name] != nil && !pkg.used[imp.Name] {
				c.diags.AddCode(imp.Pos, "import.unused", "%s is imported but not used", imp.Path)
				c.diags.Suggest(imp.Pos, "import.unused", imp.End, diag.Fix{Message: "Remove unused import", Edits: []diag.TextEdit{{Start: imp.Pos, End: imp.End}}})
			}
		}
	}
	// The later passes read the typed tree, which is built once the
	// program checks.
	if c.diags.Len() == 0 && c.deriveDiscovery == nil {
		c.zonkInfo()
		c.checkInterpolationValidators()
		c.checkOpaqueFields()
		c.checkOpaqueGenericUses(files)
		if c.diags.Len() == 0 {
			c.checkEmbeds()
			c.checkBuildReads()
			if c.diags.Len() == 0 {
				if observe != nil {
					observe("lower")
				}
				c.lower(files)
				c.packageDependencyGraph()
				if observe != nil {
					observe("contracts")
				}
				c.checkRequirementContracts()
				c.checkComptimeTypes()
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
	fn := &Func{Decl: &syntax.FuncDecl{Pos: td.Pos, Name: "test", Params: td.Params, Body: td.Body}, Pkg: c.pkg, Result: Ok, Test: td}
	// A property test's parameters are generated, with their facts.
	scope := map[string]Type{}
	for _, p := range td.Params {
		t := c.resolveType(p.Type)
		fn.Params = append(fn.Params, t)
		scope[p.Name] = t
	}
	fn.ParamConstraints = make([][]*Constraint, len(td.Params))
	for i, p := range td.Params {
		fn.ParamConstraints[i] = c.constraintsOf(p.Type, fn.Params[i], scope)
	}
	c.info.Tests = append(c.info.Tests, fn)
	c.checkFunc(fn)
}

// declarePackages sets up the packages the files belong to, and their
// imports.
func (c *checker) declarePackages(files []*syntax.File, root string) {
	c.pkgs = map[string]*Package{}
	c.preludePkg = &Package{Funcs: map[string]*Func{}, types: map[string]*typeEntry{}, imports: map[string]*Package{}, used: map[string]bool{}, classes: map[string]*Class{}}
	prefixes := map[string]bool{}
	for _, f := range files {
		if f.Prelude || c.pkgs[f.Package] != nil {
			continue
		}
		name := f.Package[strings.LastIndex(f.Package, "/")+1:]
		pkg := &Package{Path: f.Package, Name: name, Root: f.Package == root,
			Funcs: map[string]*Func{}, types: map[string]*typeEntry{}, imports: map[string]*Package{}, used: map[string]bool{}, classes: map[string]*Class{}}
		if !pkg.Root {
			stem := strings.Map(func(r rune) rune {
				if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
					return r
				}
				return '_'
			}, name)
			pkg.GoPrefix = "_" + stem + "_"
			for n := 2; prefixes[pkg.GoPrefix]; n++ {
				pkg.GoPrefix = "_" + stem + strconv.Itoa(n) + "_"
			}
			prefixes[pkg.GoPrefix] = true
		}
		c.pkgs[f.Package] = pkg
		c.info.Packages = append(c.info.Packages, pkg)
	}
	for _, file := range files {
		if !file.Script {
			continue
		}
		pkg := c.pkgs[file.Package]
		if pkg == nil {
			continue
		}
		if pkg.scriptLocals == nil {
			pkg.scriptLocals = map[string]diag.Pos{}
		}
		for _, fn := range file.Funcs {
			if !fn.ScriptMain {
				continue
			}
			for _, stmt := range fn.Body.Stmts {
				if binding, ok := stmt.(*syntax.Binding); ok && binding.Name != "_" {
					pkg.scriptLocals[binding.Name] = binding.Pos
				}
			}
		}
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
	files                 []*syntax.File
	tupleDerives          []*ClassInstance
	tupleBindingMode      bool
	patternTest           bool
	derives               []*deriveRequest
	deriveCalls           map[*syntax.Call]*Func
	deriveHelperSerial    int
	deriveSpecializations map[string]*Func
	deriveBounds          deriveBounds
	deriveDiscovery       *deriveDiscovery
	// Only compilation retains source identities; read-only queries do not.
	recordPredicateRefs bool
	bindingFiles        map[string]*syntax.File
	packagePath         []*PackageBinding

	mapKeyChecks []func()

	// needsFix is the needs clause the function being checked is
	// missing, offered as one fix (see reportUnprovided).
	needsFix *pendingNeeds
	// assemblyFuncs resolves generated references to bundled declarations.
	assemblyFuncs map[string]*Func
	// appliedWhere holds the written types whose where clauses
	// constraints were made from (see unappliedWheres).
	appliedWhere map[*syntax.TypeExpr]bool
	fieldWhere   bool // resolving field constraints in their sibling scope

	goTypes  GoTypes
	goOpaque map[string]Type
	// have holds the facts known of the value an instance is looked up
	// for (a field's where clause), which constrained instances need.
	have []*Constraint

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
	comptimeContext    *comptimeContext
	initializerContext *initializerContext
	lambdaDepth        int
	assemblySerial     int
	selectSerial       int
	producer           *producerContext
	loops              []*loopContext
	// postClause is one more than the lambda depth of the loop post
	// clause being checked, or 0: it cannot leave the loop.
	postClause int
	// loopCond is the same for a loop condition: loop control in it
	// would mean different loops to the checker and the generated Go.
	loopCond int
	// carryFrames are the loop bodies and statement branches being
	// checked that may give carried names new values (see carried.go);
	// nextTransparent marks the next scope pushed as one of them, and
	// stmtPos tells the if, match or block about to be checked that it
	// is a statement there.
	carryFrames     []*carryFrame
	nextTransparent bool
	stmtPos         bool
	// headers are loops' header bindings, which bind new names.
	headers map[*syntax.Binding]bool

	conversionSerial int
	// inForce lists the mocks in force at the current point of a test:
	// their targets, and how many scopes were open when each started.
	inForce []mockInForce
	// used collects the effects of the function or lambda being
	// checked: of the calls in its body (see effects.go).
	used Effects
	// sharedDefaults are the parameter defaults that calls share (see
	// defaults.go): checked once, where they were declared.
	sharedDefaults map[syntax.Expr]bool
	// session is the inference of the calls being checked, and solved
	// holds the solutions of all unknowns (see infer.go).
	session *session
	solved  map[*TypeParam]Type
}

type local struct {
	typ  Type
	node any // the binding's syntax node; nil for parameters
	decl any // what an identifier refers to (see Info.defs)
	used bool
	// carry is the loop that carries this value of the name to its next
	// iteration, if any (see carried.go).
	carry *carryLoop
}

// funcNamed looks up a function by name, as the code being checked
// sees it: its package's functions, then the prelude's (prelude code
// sees only the prelude). A qualified name ("money.add") is looked up in
// an imported package, which must export it.
func (c *checker) funcNamed(name string) (*Func, bool) {
	if fn := c.assemblyFuncs[name]; fn != nil {
		return fn, true
	}
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
	// The prelude's select helpers are for the select expression only.
	if strings.HasPrefix(name, "compilerSelect") {
		return nil, false
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
	if (isFunc || isType || pkg.bindings[n] != nil) && !Exported(n) {
		return fmt.Sprintf("%s is not exported by package %s (only names starting with an upper-case letter are)", n, pkg.Path)
	}
	return fmt.Sprintf("package %s has no %s", pkg.Path, n)
}

// errorf reports an error. Types in args are shown as code in the
// current package would write them (money.Cents).
func (c *checker) errorf(pos diag.Pos, format string, args ...any) {
	if strings.Contains(format, "must be %s") && strings.Contains(format, ", found %s") || strings.Contains(format, "but its body produces %s") || strings.Contains(format, "but this returns %s") {
		format += effectsNote(args)
		format += c.optionPromotionNote(args)
	}
	for i, a := range args {
		if t, ok := a.(Type); ok && t != nil {
			args[i] = TypeText(t, c.pkg)
		}
	}
	c.diags.AddCode(pos, "type.error", format, args...)
}

func (c *checker) declareFunc(fd *syntax.FuncDecl, prelude bool) {
	if fd.IsMethod {
		c.declareMethod(fd, prelude)
		return
	}
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
	if _, ok := c.pkg.ambients[fd.Name]; ok {
		c.errorf(fd.Pos, "%s is already the name of an ambient value", fd.Name)
		return
	}
	fn := &Func{Decl: fd, Pkg: c.pkg, Prelude: prelude}
	fn.TypeParams = c.declareTypeParams(fd, prelude)
	fn.Effects = c.declEffects(fn)
	c.needsOf(fn)
	if fd.IsPred && fd.Uses != nil {
		c.diags.AddCode(fd.Uses.Pos, "effect.pred", "pred %s cannot declare effects: predicates must be pure, or their facts could go stale", fd.Name)
	}
	if r := fd.Result; r != nil && r.Name == "Never" && len(r.Args) == 0 && r.Func == nil && len(r.Union) == 0 {
		// A function that never returns, such as exit.
		fn.Result = Never
	} else {
		fn.Result = c.resolveType(fd.Result)
	}
	for _, p := range fd.Params {
		fn.Params = append(fn.Params, c.resolveType(p.Type))
	}
	c.ownerSignature(fn)
	if fd.Constructor == nil {
		c.openSignature(fn)
	}
	c.typeParams = nil
	c.pkg.Funcs[fd.Name] = fn
	c.info.FuncOf[fd] = fn
}

func (c *checker) checkFunc(fn *Func) {
	c.fn = fn
	c.inForce = nil
	c.pkg = fn.Pkg
	if fn.TemplatePkg != nil {
		c.pkg = fn.TemplatePkg
	}
	c.inPrelude = fn.Prelude
	defer func() { c.inPrelude = false }()
	c.useTypeParams(fn)
	defer c.useTypeParams(nil)
	c.scopes = []map[string]*local{{}}
	defer c.popScope()
	for i, p := range fn.Decl.Params {
		// The prelude's parameter names do not depend on user code.
		if !fn.Prelude && fn.Decl.Constructor == nil && c.nameTaken(p.Name, p.Pos) {
			continue
		}
		if fn.Params[i] == Ok {
			c.errorf(p.Type.Pos, "parameter %s cannot have type Ok", p.Name)
		}
		c.scopes[0][p.Name] = &local{typ: fn.Params[i], decl: p}
	}
	c.ensureDefaults(fn)
	c.bindNeeds(fn)
	if fn.Decl.Name == "main" && (len(fn.Params) != 0 || fn.Result != Ok) {
		c.errorf(fn.Decl.Pos, "main must take no parameters and return no value")
	}
	if fn.Decl.IsGo() {
		// The Go code is checked by the Go compiler.
		c.fn = nil
		return
	}
	var want Type
	if fn.Result != Ok {
		want = fn.Result
	}
	c.used = 0
	bodyType := c.blockInScope(fn.Decl.Body, want)
	c.unusedNeeds(fn)
	c.suggestNeeds(fn)
	if isOpen(fn.Result) && c.used&EffOpen != 0 {
		c.diags.AddCode(fn.Decl.Pos, "effect.open-result", "%s returns an open function, so it cannot call its open parameters itself (its callers are not charged for them); give them effects, or only return them", fn.Decl.Name)
	}
	if fn.Result == Ok && isValue(bodyType) && !isOkTask(bodyType) {
		if fn.Test != nil {
			c.errorf(fn.Decl.Body.Tail.Position(), "value of type %s is not used (a test returns no value)", bodyType)
		} else {
			c.errorf(fn.Decl.Body.Tail.Position(), "value of type %s is not used (function %s returns no value)", bodyType, fn.Decl.Name)
		}
	}
	if fn.Result != Ok && !assignable(bodyType, fn.Result) {
		pos := fn.Decl.Body.Pos
		if fn.Decl.Body.Tail != nil {
			pos = fn.Decl.Body.Tail.Position()
		}
		if bodyType == Ok {
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
	if c.pkg.bindings[name] != nil {
		c.diags.AddCode(pos, "binding.shadow", "%s is already the name of a package value (bork does not allow shadowing)", name)
		return true
	}
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if _, ok := c.scopes[i][name]; ok {
			c.diags.AddCode(pos, "binding.shadow", "%s is already defined in an enclosing scope (bork does not allow shadowing)", name)
			return true
		}
	}
	// Prelude functions may be shadowed: their names (count, find,
	// last, ...) are too useful to take away from locals.
	if _, ok := c.pkg.Funcs[name]; ok && c.pkg != c.preludePkg {
		c.diags.AddCode(pos, "binding.shadow", "%s is already the name of a function (bork does not allow shadowing)", name)
		return true
	}
	if _, ok := c.pkg.imports[name]; ok {
		c.diags.AddCode(pos, "binding.shadow", "%s is already the name of an imported package (bork does not allow shadowing)", name)
		return true
	}
	if _, ok := builtins[name]; ok {
		c.diags.AddCode(pos, "binding.shadow", "%s is a built-in function (bork does not allow shadowing)", name)
		return true
	}

	if c.isTypeName(name) {
		c.diags.AddCode(pos, "binding.shadow", "%s is already the name of a type", name)
		return true
	}
	if _, ok := c.pkg.ambients[name]; ok {
		c.diags.AddCode(pos, "binding.shadow", "%s is already the name of an ambient value (bork does not allow shadowing)", name)
		return true
	}
	return false
}

// bind adds a local to the innermost scope. A name that is already
// taken is still bound, as Invalid, so later uses don't cause follow-up
// errors.
func (c *checker) bind(name string, pos diag.Pos, t Type, node any) {
	if name != "_" && c.nameTaken(name, pos) {
		t = Invalid
	}
	c.scopes[len(c.scopes)-1][name] = &local{typ: t, node: node, decl: node, used: name == "_"}
}

func (c *checker) lookup(name string) *local {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if l, ok := c.scopes[i][name]; ok {
			return l
		}
	}
	return nil
}

func (c *checker) pushScope() {
	scope := map[string]*local{}
	if c.nextTransparent {
		scope[transparentKey] = &local{used: true}
		c.nextTransparent = false
	}
	c.scopes = append(c.scopes, scope)
}

func (c *checker) popScope() {
	for _, l := range c.scopes[len(c.scopes)-1] {
		if l.node != nil && !l.used {
			c.unusedLocal(l)
		}
	}
	c.scopes = c.scopes[:len(c.scopes)-1]
	for len(c.inForce) > 0 && c.inForce[len(c.inForce)-1].depth > len(c.scopes) {
		c.inForce = c.inForce[:len(c.inForce)-1]
	}
}

func (c *checker) record(e syntax.Expr, t Type) Type {
	if c.session != nil {
		t = c.zonk(t)
	}
	c.info.types[e] = t
	c.info.exprOwners[e] = c.fn
	c.noteDefaultTypeUse(t, e.Position())
	return t
}

// block checks a block. want is the type the block's value is expected
// to have, or nil if there is no expectation.
func (c *checker) block(b *syntax.Block, want Type) Type {
	c.pushScope()
	defer c.popScope()
	return c.blockInScope(b, want)
}

// blockInScope shares a function body with its parameters.
func (c *checker) blockInScope(b *syntax.Block, want Type) Type {

	// Statements after one that never finishes (e.g. `return`) are
	// unreachable. Only the first one is reported.
	diverged, reported := false, false
	// In a loop body, an if, match or block statement may give the
	// names the loop carries new values (see carried.go).
	carrying := c.transparent(len(c.scopes) - 1)
	for _, s := range b.Stmts {
		if diverged {
			c.errorf(stmtPos(s), "unreachable code")
			reported = true
			break
		}
		if es, ok := s.(*syntax.ExprStmt); ok && carrying {
			c.stmtPos = compound(es.X)
		}
		t := c.stmt(s)
		c.stmtPos = false
		if t == Never {
			diverged = true
		}
	}
	t := Ok
	if b.Tail != nil {
		if !diverged {
			c.stmtPos = carrying && compound(b.Tail)
			t = c.exprWant(b.Tail, want)
			c.stmtPos = false
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
	// The policies, computed before the scope opens.
	for _, p := range e.Policies {
		policy := c.preludePkg.TypeNamed("ScopePolicy")
		if t := c.exprWant(p, policy); t != Invalid && !assignable(t, policy) {
			c.errorf(p.Position(), "a scope's policy must be a ScopePolicy (such as taskTimeout(100), cleanupTimeout(500), or logFailures()), found %s", t)
		}
		if c.fn != nil {
			c.fn.Calls = append(c.fn.Calls, c.preludePkg.Funcs["setScopePolicy"])
		}
	}
	c.pushScope()
	defer c.popScope()
	if !c.nameTaken(e.Name, e.Pos) {
		c.scopes[len(c.scopes)-1][e.Name] = &local{typ: Scope, decl: e, used: true}
	}
	return c.block(e.Body, want)
}

func stmtPos(s syntax.Stmt) diag.Pos {
	switch s := s.(type) {
	case *syntax.TupleBinding:
		return s.Pos
	case *syntax.Binding:
		return s.Pos
	case *syntax.ExprStmt:
		return s.X.Position()
	case *syntax.TrustStmt:
		return s.Pos
	case *syntax.MockStmt:
		return s.Pos
	}
	return diag.Pos{}
}

// stmt checks a statement and returns Never if it never finishes.
func (c *checker) stmt(s syntax.Stmt) Type {
	switch s := s.(type) {
	case *syntax.TupleBinding:
		return c.tupleBinding(s)
	case *syntax.Binding:
		var declared Type
		if s.Type != nil {
			declared = c.resolveType(s.Type)
		}
		var t Type
		if s.AsyncScope != nil {
			scope := c.expr(s.AsyncScope)
			if scope == Never {
				return Never
			}
			if scope != Scope && scope != Invalid {
				c.errorf(s.AsyncScope.Position(), "async requires a Scope, found %s", scope)
			}
		}
		if s.Lazy || s.AsyncScope != nil {
			t = c.deferredInitializer(s, declared)
		} else if slot := c.carriedHere(s); declared == nil && slot != nil {
			// A carried name keeps its type, which guides the new value.
			t = c.exprWant(s.Value, slot.typ)
		} else {
			t = c.exprWant(s.Value, declared)
		}
		if s.Type == nil {
			var code, annotation string
			switch e := s.Value.(type) {
			case *syntax.ListLit:
				if len(e.Elems) == 0 {
					code, annotation = "type.empty-list", ": List[Element]"
				}
			case *syntax.MapLit:
				if len(e.Keys) == 0 {
					code, annotation = "type.empty-map", ": Map[Key, Value]"
				}
			}
			if code != "" {
				at := s.Pos
				at.Col += len(s.Name)
				c.diags.Suggest(s.Value.Position(), code, s.Value.Position(), diag.Fix{
					Message:       "annotate the binding (replace the type placeholders)",
					RequiresInput: true,
					Edits:         []diag.TextEdit{{Start: at, End: at, Replacement: annotation}},
				})
			}
		}
		switch t {
		case Ok:
			c.errorf(s.Value.Position(), "cannot bind %s: the expression produces no value (Ok)", s.Name)
			t = Invalid
		case Never:
			c.errorf(s.Value.Position(), "cannot bind %s: the expression never produces a value", s.Name)
			return Never
		}
		if declared != nil {
			// A declared type holds even when the value has errors, so
			// that they do not spread to its uses.
			if t, declared := c.settle(t, declared); t != Invalid && declared != Invalid && !assignable(t, declared) {
				c.errorf(s.Value.Position(), "%s must be %s, found %s", s.Name, declared, t)
			}
			t = declared
		}
		c.info.bindings[s] = t
		if s.Type != nil && t != Invalid {
			c.info.bindingConstraints[s] = c.constraintsOf(s.Type, t, c.paramScope())
		}
		if s.Name != "_" {
			c.bindRebinding(s.Name, s.Pos, t, s)
		}
		return Ok
	case *syntax.TrustStmt:
		c.expr(s.Call)
		if fn := c.info.callFuncs[s.Call]; fn != nil && !fn.Decl.IsPred {
			c.errorf(s.Call.Position(), "trust needs a predicate call, but %s is a function", fn.Decl.Name)
		}
		return Ok
	case *syntax.ExprStmt:
		t := c.expr(s.X)
		if isValue(t) && !isOkTask(t) {
			c.errorf(s.X.Position(), "value of type %s is not used", t)
		}
		return t
	case *syntax.MockStmt:
		c.mockStmt(s)
	}
	return Ok
}

func (c *checker) expr(e syntax.Expr) Type { return c.exprWant(e, nil) }

// exprWant checks an expression. want is the type the context expects,
// or nil. It guides branches that produce different members of a union,
// and values like `Option.None` whose type comes from the context. It
// does not report mismatches; the caller does.
func (c *checker) exprWantRaw(e syntax.Expr, want Type) Type {
	// Conversion expands already-checked inputs into bindings. Keep their
	// original contextual typing and lexical bindings when checking the expansion.
	if c.info.conversionInputs[e] {
		return c.info.types[e]
	}
	if c.open(want) && !c.needsContext(e) {
		// A type not fully known yet (see infer.go) guides only what
		// takes its type from the context; the caller unifies the rest.
		list, openList := want.(*List)
		if !openList || !mentionsOpen(list.Elem) {
			want = nil
		}
	}
	if v := constValue(e); v != nil {
		return c.constant(e, v, want)
	}
	switch e := e.(type) {
	case *syntax.Comptime:
		return c.record(e, c.comptimeInitializer(e, want))
	case *syntax.Generate:
		return c.record(e, c.generate(e))
	case *syntax.Yield:
		return c.record(e, c.yieldExpr(e))
	case *syntax.For:
		return c.record(e, c.forExpr(e))
	case *syntax.LoopControl:
		word := "break"
		if e.Continue {
			word = "continue"
		}
		if c.postClause == c.lambdaDepth+1 {
			c.inPostClause(e.Pos, word)
		} else if c.loopCond == c.lambdaDepth+1 {
			c.errorf(e.Pos, "a loop's condition cannot use %s; test in the body instead", word)
		} else if len(c.loops) == 0 || c.loops[len(c.loops)-1].depth != c.lambdaDepth {
			c.errorf(e.Pos, "break and continue require a loop in the same function or producer")
		} else {
			loop := c.loops[len(c.loops)-1]
			if !e.Continue {
				loop.broken = true
			}
			c.loopEdge(e, loop)
		}
		return c.record(e, Never)
	case *syntax.IntLit:
		c.errorf(e.Pos, "invalid integer literal %s", e.Text)
		return c.record(e, Invalid)
	case *syntax.FloatLit:
		c.errorf(e.Pos, "invalid float literal %s", e.Text)
		return c.record(e, Invalid)
	case *syntax.RuneLit:
		if len(e.Text) >= 2 {
			r, _, tail, err := strconv.UnquoteChar(e.Text[1:len(e.Text)-1], '\'')
			if err == nil && tail == "" && utf8.ValidRune(r) {
				c.info.consts[e] = constant.MakeInt64(int64(r))
				return c.record(e, Rune)
			}
		}
		c.errorf(e.Pos, "invalid rune literal %s", e.Text)
		return c.record(e, Invalid)
	case *syntax.StringLit:
		return c.record(e, String)
	case *syntax.StaticPartsLit:
		return c.record(e, c.preludePkg.types["StaticParts"].typ)
	case *syntax.Interp:
		if e.Prefix != nil {
			return c.record(e, c.interpolator(e, want))
		}
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
		return c.record(e, c.unary(e, want))
	case *syntax.Binary:
		return c.record(e, c.binary(e, want))
	case *syntax.Call:
		return c.record(e, c.call(e, want))
	case *syntax.Lambda:
		return c.record(e, c.lambda(e, want))
	case *syntax.TupleLit:
		return c.record(e, c.tupleLit(e, want))
	case *syntax.ListLit:
		return c.record(e, c.listLit(e, want))
	case *syntax.MapLit:
		return c.record(e, c.mapLit(e, want))
	case *syntax.If:
		return c.record(e, c.ifExpr(e, want))
	case *syntax.Block:
		if j := c.startJoin(); j != nil {
			t := c.joinBranch(j, 0, func() Type { return c.branchExpr(j, e, want) })
			c.endJoin(j, e, 1)
			return t
		}
		return c.block(e, want)
	case *syntax.ScopeExpr:
		return c.record(e, c.scopeExpr(e, want))
	case *syntax.WithExpr:
		return c.withExpr(e, want)
	case *syntax.Return:
		c.inPostClause(e.Pos, "return")
		c.returnExpr(e)
		return c.record(e, Never)
	case *syntax.Selector:
		return c.record(e, c.selector(e, want))
	case *syntax.TypeHead:
		c.errorf(e.Position(), "a specialized type needs record braces or a sealed variant; it is not a value")
		return c.record(e, Invalid)
	case *syntax.ContextName:
		return c.record(e, c.contextVariant(e, want))
	case *syntax.RecordLit:
		return c.record(e, c.recordLit(e, want))
	case *syntax.Copy:
		return c.record(e, c.copyExpr(e))
	case *syntax.Is:
		return c.record(e, c.isExpr(e))
	case *syntax.Match:
		return c.record(e, c.match(e, want))
	case *syntax.Select:
		return c.record(e, c.selectExpr(e, want))
	case *syntax.Try:
		c.inPostClause(e.Pos, "?")
		return c.record(e, c.try(e))
	}
	panic("unhandled expression")
}

// inPostClause reports what cannot leave a loop's post clause, which
// runs between iterations.
func (c *checker) inPostClause(pos diag.Pos, what string) {
	if c.postClause == c.lambdaDepth+1 {
		c.errorf(pos, "a loop's post clause cannot use %s; compute the value in the body instead", what)
	}
}

func (c *checker) ident(e *syntax.Ident, want Type) Type {
	if l := c.lookup(e.Name); l != nil {
		l.used = true
		c.noteInitializerCapture(l.decl, e.Name)
		c.noteComptimeCapture(e, l.decl, l.typ)
		c.info.defs[e] = l.decl
		return l.typ
	}
	if binding := c.packageBindingNamed(e.Name); binding != nil {
		return c.packageBindingRead(e, binding)
	}
	if a := c.ambientNamed(e.Name); a != nil {
		return c.ambientIdent(e, a)
	}

	if fn, ok := c.funcNamed(e.Name); ok {
		if swapIntrinsic(fn) {
			c.diags.AddCode(e.Pos, "test.swap", "test.%s must be called directly", fn.Decl.Name)
			return Invalid
		}
		return c.funcValue(e, e.Name, fn, want)
	}
	if _, ok := builtins[e.Name]; ok {
		c.errorf(e.Pos, "built-in %s must be called", e.Name)
		return Invalid
	}
	if e.Name == "Ok" {
		return Ok
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
	if c.scriptLocalError(e.Pos, e.Name) {
		return Invalid
	}
	c.errorf(e.Pos, "undefined: %s", e.Name)
	return Invalid
}

func (c *checker) scriptLocalError(pos diag.Pos, name string) bool {
	declaration, ok := c.pkg.scriptLocals[name]
	if !ok || c.fn == nil || c.fn.Decl.ScriptMain {
		return false
	}
	c.errorf(pos, "%s is a script local declared at %s; functions cannot capture script locals (use lazy %s = ... for a pure package value)", name, declaration, name)
	return true
}

func (c *checker) unary(e *syntax.Unary, want Type) Type {
	t := c.exprWant(e.X, want)
	if t == Invalid {
		return Invalid
	}
	switch e.Op {
	case syntax.Caret:
		if !IsInteger(t) {
			c.errorf(e.Pos, "operator ^ needs an integer, found %s", t)
			return Invalid
		}
		if v := c.info.consts[e.X]; v != nil {
			precision := uint(0)
			if isUnsigned(t) {
				precision = uint(bitsOf(t))
			}
			c.info.consts[e] = constant.UnaryOp(token.XOR, v, precision)
		}
		return t
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
	if e.Op == syntax.Shl || e.Op == syntax.Shr {
		x = c.exprWant(e.X, want)
		y = c.expr(e.Y)
	} else if constValue(e.X) != nil && constValue(e.Y) == nil {
		y = c.exprWant(e.Y, xWant)
		x = c.exprWant(e.X, y)
	} else {
		x = c.exprWant(e.X, xWant)
		y = c.exprWant(e.Y, x)
	}
	if x == Invalid || y == Invalid {
		return Invalid
	}
	if c.session != nil && (c.open(x) || c.open(y)) {
		// An operand whose type is not fully known yet (in a lambda
		// given to a call being inferred) has the other's type, or Bool.
		switch e.Op {
		case syntax.AndAnd, syntax.OrOr:
			c.solve(x, Bool)
			c.solve(y, Bool)
		default:
			c.solve(x, y)
		}
		x, y = c.zonk(x), c.zonk(y)
	}
	op := opSymbol(e.Op)
	sameNumbers := IsNumeric(x) && identical(x, y)
	if e.Op == syntax.Slash || e.Op == syntax.Pct {
		if v := c.info.consts[e.Y]; v != nil && constant.Sign(v) == 0 {
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
	case syntax.Shl, syntax.Shr:
		if !IsInteger(x) || !IsInteger(y) {
			c.errorf(e.Pos, "operator %s needs integer operands, found %s and %s", op, x, y)
			return Invalid
		}
		if a, b := c.info.consts[e.X], c.info.consts[e.Y]; a != nil && b != nil && constant.Sign(b) >= 0 {
			v := shiftConstant(a, b, e.Op)
			if v == nil {
				c.errorf(e.Pos, "constant shift result does not fit %s", x)
				return Invalid
			}
			v, ok := c.fits(e.Pos, v, x)
			if !ok {
				return Invalid
			}
			c.info.consts[e] = v
		}
		return x
	case syntax.Amp, syntax.Pipe, syntax.Caret:
		if !sameNumbers || !IsInteger(x) {
			if x == Bool && y == Bool {
				replacement := map[syntax.Kind]string{syntax.Amp: "&&", syntax.Pipe: "||", syntax.Caret: "!="}[e.Op]
				c.errorf(e.Pos, "operator %s needs integers; use %s for Bool operands", op, replacement)
				end := e.Pos
				end.Col += len(op)
				c.diags.Suggest(e.Pos, "type.error", end, diag.Fix{Message: "replace " + op + " with " + replacement, Edits: []diag.TextEdit{{Start: e.Pos, End: end, Replacement: replacement}}})
			} else {
				c.errorf(e.Pos, "operator %s needs two integers of the same type, found %s and %s", op, x, y)
			}
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
		if sameNumbers || (x == String && y == String) || (x == Rune && y == Rune) {
			return Bool
		}
		c.errorf(e.Pos, "operator %s needs two numbers of the same type or two Strings, found %s and %s", op, x, y)
		return Invalid
	case syntax.Eq, syntax.NotEq:
		// A union compares with a value of one of its members.
		switch {
		case identical(x, y), assignable(y, x):
		case assignable(x, y):
			x = y
		default:
			c.errorf(e.Pos, "cannot compare %s with %s: the types are incompatible", x, y)
			return Invalid
		}
		if !isValue(x) {
			c.errorf(e.Pos, "cannot compare values of type %s", x)
			return Invalid
		}
		if c.session != nil && c.open(x) {
			// Whether it has == is told once the session decides it.
			pos, t := e.Pos, x
			c.session.finish = append(c.session.finish, func() {
				if t := c.zonk(t); !c.open(t) && !comparable(t) {
					c.errorf(pos, "cannot compare values of type %s with %s (functions, scopes, and resources have no ==)", t, op)
				}
			})
			return Bool
		}
		if tp, ok := x.(*TypeParam); ok && !comparable(x) {
			c.errorf(e.Pos, "cannot compare values of type parameter %s with %s; require it: [%s: Eq]", tp.Name, op, tp.Name)
			return Invalid
		}
		if !comparable(x) && containsOpaque(x, map[Type]bool{}) {
			c.bindErr(e.Pos, "cannot compare values of type %s with %s: opaque Go values can change", x, op)
			return Invalid
		}
		if !comparable(x) {
			c.errorf(e.Pos, "cannot compare values of type %s with %s (functions, scopes, and resources have no ==)", x, op)
			return Invalid
		}
		return Bool
	}
	return Invalid
}

func (c *checker) call(e *syntax.Call, want Type) Type {
	if t, ok := c.positionalVariantCall(e, want); ok {
		return t
	}
	if field := c.info.shapeDefaults[e]; field != nil {
		return field.Type
	}
	if fn := c.deriveCalls[e]; fn != nil {
		return c.callFunc(e, fn.Decl.Name, fn, e.Args, nil, e.TypeArgs, want)
	}
	if id, ok := e.Fun.(*syntax.Ident); ok {
		if helper, _ := c.deriveHelperNamed(c.pkg, id.Name); helper != nil {
			c.errorf(e.Pos, "derive helper %s is available only during template expansion", id.Name)
			return Invalid
		}
		alias, _, qualified := strings.Cut(id.Name, ".")
		if pkg := c.pkg.imports[alias]; qualified && pkg != nil && pkg.Path == "bork/shape" {
			c.pkg.used[alias] = true
			c.errorf(e.Pos, "shape operations are available only during derive template expansion")
			return Invalid
		}
	}
	if sel, ok := e.Fun.(*syntax.Selector); ok && sel.Name == "into" {
		return c.into(e, sel)
	}
	if id, ok := e.Fun.(*syntax.Ident); ok && assemblyName(id.Name) && c.lookup(id.Name) == nil {
		return c.assemble(e, id.Name)
	}
	if id, ok := e.Fun.(*syntax.Ident); ok && c.lookup(id.Name) == nil && c.packageBindingNamed(id.Name) == nil {
		if fn, found := c.funcNamed(id.Name); found && swapIntrinsic(fn) {
			return c.tupleSwap(e, fn)
		}
	}
	if t, ok := c.seqStatic(e); ok {
		return t
	}

	if t, ok := c.methodCallOf(e, want); ok {
		return t
	}
	id, ok := e.Fun.(*syntax.Ident)
	if len(e.TypeArgs) > 0 {
		builtin := false
		if ok {
			_, builtin = builtins[id.Name]
		}
		if !ok || c.lookup(id.Name) != nil || c.packageBindingNamed(id.Name) != nil || builtin {
			c.errorf(e.Pos, "only a declared generic function can be given type arguments")
		}
	}
	if !ok || c.lookup(id.Name) != nil || c.packageBindingNamed(id.Name) != nil {
		return c.callValue(e)
	}
	if b, ok := builtins[id.Name]; ok && c.lookup(id.Name) == nil {
		c.info.callBuiltins[e] = b
		return c.builtinCall(e, id.Name, b, want)
	}
	fn, ok := c.funcNamed(id.Name)
	if !ok && c.removedTaskCall(id) {
		fn, _ = c.funcNamed("fork")
		return c.callFunc(e, fn.Decl.Name, fn, e.Args, nil, e.TypeArgs, want)
	}
	if !ok {
		if c.removedChannelCall(e, id) {
			for _, arg := range e.Args {
				c.exprWant(arg, nil)
			}
			return Invalid
		}
		if c.removedBytesCall(e, id) {
			for _, arg := range e.Args {
				var context Type
				if id.Name == "bytes" {
					context = &List{Elem: Uint8}
				}
				c.exprWant(arg, context)
			}
			return Invalid
		}
		checked := 0
		if e.Pipe.File != "" && len(e.Args) > 0 {
			recv := c.expr(e.Args[0])
			checked = 1
			if c.pipeMethodError(e, id, recv, want) {
				return Invalid
			}
		}
		switch {
		case c.isTypeName(id.Name):
			c.errorf(id.Pos, "%s is a type; build a record with %s { field: value, ... }", id.Name, id.Name)
		case c.notFound(id.Name) != "":
			c.errorf(id.Pos, "%s", c.notFound(id.Name))
		default:
			if !c.scriptLocalError(id.Pos, id.Name) {
				c.errorf(id.Pos, "undefined function: %s", id.Name)
			}
		}
		for _, a := range e.Args[checked:] {
			c.expr(a)
		}
		return Invalid
	}
	name := id.Name
	if c.assemblyFuncs[name] != nil {
		name = fn.Decl.Name
	}
	return c.callFunc(e, name, fn, e.Args, nil, e.TypeArgs, want)
}

func (c *checker) builtinCall(e *syntax.Call, fname string, b Builtin, want Type) Type {
	c.rejectNamedArgs(e, "compiler built-ins have no declared parameter names")
	if b == BuiltinCallerLocation {
		if len(e.TypeArgs) != 0 {
			c.errorf(e.Pos, "compilerCallerLocation takes no type arguments")
		}
		if len(e.Args) != 0 {
			c.errorf(e.Pos, "compilerCallerLocation takes no arguments")
			for _, arg := range e.Args {
				c.expr(arg)
			}
		}
		if c.fn == nil || c.fn.Decl.IsPred || c.fn.Class != nil || c.fn.Of != nil || c.fn.MockOf != nil || !c.inPrelude && !strings.HasPrefix(c.pkg.Path, "bork/") {
			c.errorf(e.Pos, "compilerCallerLocation is only available in prelude and standard-library helpers")
			return Invalid
		}
		c.fn.TrackCaller = true
		return String
	}
	if b == BuiltinTodo {
		if len(e.Args) > 1 {
			c.errorf(e.Pos, "todo takes 0 or 1 arguments, but %d were given", len(e.Args))
		}
		for _, a := range e.Args {
			if t := c.exprWant(a, String); t != String && t != Invalid && t != Never {
				c.errorf(a.Position(), "todo needs a String message, found %s", t)
			}
		}
		return Never
	}
	if b == BuiltinAssertEqual {
		if len(e.Args) != 2 {
			c.errorf(e.Pos, "assertEqual takes 2 arguments (actual, expected), but %d were given", len(e.Args))
			for _, a := range e.Args {
				c.expr(a)
			}
			return Ok
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
		return Ok
	}
	if b != BuiltinPrintln && len(e.Args) != 1 {
		c.errorf(e.Pos, "%s takes 1 argument, but %d were given", fname, len(e.Args))
		for _, a := range e.Args {
			c.expr(a)
		}
		return Invalid
	}
	if b == BuiltinPrintln || b == BuiltinAssertSnapshot {
		c.used |= EffIO
	}
	switch b {
	case BuiltinDbg:
		t := c.exprWant(e.Args[0], want)
		if t != Invalid && t != Never && !isValue(t) {
			c.errorf(e.Args[0].Position(), "dbg needs a value, found %s", t)
		}
		return t
	case BuiltinPanic:
		if t := c.exprWant(e.Args[0], String); t != String && t != Invalid {
			c.errorf(e.Args[0].Position(), "panic needs a String message, found %s", t)
		}
		return Never
	case BuiltinAssert:
		if t := c.expr(e.Args[0]); t != Bool && t != Invalid {
			c.errorf(e.Args[0].Position(), "assert needs a Bool, found %s", t)
		}
		return Ok
	case BuiltinToString:
		t := c.expr(e.Args[0])
		if t != Invalid && !isValue(t) {
			c.errorf(e.Args[0].Position(), "toString needs a value, found %s", t)
		}
		return String
	case BuiltinAssertSnapshot:
		t := c.expr(e.Args[0])
		if t != Invalid && !isValue(t) {
			c.errorf(e.Args[0].Position(), "assertSnapshot needs a value, found %s", t)
		}
		return Ok
	case BuiltinConvert:
		return c.conversion(e, fname)
	case BuiltinPrintln:
		for _, a := range e.Args {
			t := c.expr(a)
			if t != Invalid && !isValue(t) {
				c.errorf(a.Position(), "println cannot print a value of type %s", t)
			}
		}
		return Ok
	}
	return Invalid
}

func (c *checker) ifExpr(e *syntax.If, want Type) Type {
	// As a statement in a loop body, the branches may give the names the
	// loop carries new values (see carried.go).
	j := c.startJoin()
	defer c.endJoin(j, e, 2)
	cond := c.expr(e.Cond)
	if cond != Bool && cond != Invalid && cond != Never {
		c.errorf(e.Cond.Position(), "if-condition must be Bool, found %s", cond)
	}
	if e.Else != nil && (want == nil || c.unbound(want)) && c.branchNeedsContext(e.Then) && !c.branchNeedsContext(e.Else) {
		// The then-branch's type comes from the else-branch: `[]`.
		elseT := c.joinBranch(j, 1, func() Type { return c.branchExpr(j, e.Else, nil) })
		thenT := c.joinBranch(j, 0, func() Type { return c.branchBlock(j, e.Then, elseT) })
		return c.unify(e.Pos, "if-branches have", []Type{thenT, elseT}, nil)
	}
	thenT := c.joinBranch(j, 0, func() Type { return c.branchBlock(j, e.Then, want) })
	if e.Else == nil {
		// Without else, the if is only run for its effect.
		if isValue(thenT) && !isOkTask(thenT) {
			c.errorf(e.Then.Pos, "if without else cannot produce a value (found %s); add an else branch or drop the value", thenT)
		}
		if j != nil {
			// Not taking the branch keeps the values.
			j.reaches[1] = true
		}
		return Ok
	}
	if (want == nil || c.unbound(want)) && c.branchNeedsContext(e.Else) {
		want = thenT
	}
	elseT := c.joinBranch(j, 1, func() Type { return c.branchExpr(j, e.Else, want) })
	return c.unify(e.Pos, "if-branches have", []Type{thenT, elseT}, want)
}

// branchNeedsContext reports whether a branch's value (a block's tail)
// gets its type from the context, as `[]` does.
func (c *checker) branchNeedsContext(x syntax.Expr) bool {
	if w, ok := x.(*syntax.WithExpr); ok {
		return c.branchNeedsContext(w.Body)
	}
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
	if c.session != nil {
		// Branches whose types are not fully known yet decide each
		// other's (see infer.go).
		var first Type
		for _, t := range ts {
			switch {
			case t == Never || t == Invalid:
			case first == nil:
				first = t
			default:
				c.solve(first, t)
			}
		}
		want = c.zonk(want)
	}
	for _, t := range ts {
		t = c.zonk(t)
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
	// The type of one branch, if it holds the values of all the others
	// (a union, and some of its members).
	for _, wide := range vals {
		fits := true
		for _, t := range vals {
			if !assignable(t, wide) {
				fits = false
			}
		}
		if fits {
			return wide
		}
	}
	// A Task[Ok] joins Ok where no value is wanted: the branch's task is
	// dropped like an Ok. (A union result, Ok | E, would return the task.)
	okJoin := false
	for _, t := range vals {
		if want != nil && want != Ok {
			break
		}
		if t != Ok && !isOkTask(t) {
			okJoin = false
			break
		}
		okJoin = okJoin || t == Ok
	}
	if okJoin {
		return Ok
	}
	// Functions that differ only in their effects join: the value may
	// use what any of them uses.
	if joined, ok := vals[0].(*FuncType); ok {
		for _, t := range vals[1:] {
			if tf, ok := t.(*FuncType); ok && sameSignature(joined, tf) {
				joined = &FuncType{Params: joined.Params, Result: joined.Result, Effects: joined.Effects | tf.Effects}
			} else {
				joined = nil
				break
			}
		}
		if joined != nil {
			return joined
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
	if ctx := c.initializerContext; ctx != nil && ctx.depth == c.lambdaDepth {
		if e.Value == nil {
			c.errorf(e.Pos, "a %s must return a value", ctx.name)
			return
		}
		t := c.exprWant(e.Value, ctx.want)
		if ctx.want != nil {
			t, ctx.want = c.settle(t, ctx.want)
			if !assignable(t, ctx.want) {
				c.errorf(e.Value.Position(), "%s must return %s, found %s", ctx.name, ctx.want, t)
			}

		}
		ctx.returns = append(ctx.returns, t)
		return
	}
	if c.producer != nil && c.producer.depth == c.lambdaDepth {
		if e.Value != nil {
			c.expr(e.Value)
			c.errorf(e.Pos, "a generator can only use bare return; yield a value instead")
		}
		return
	}
	if c.lambdaDepth > 0 {
		c.errorf(e.Pos, "return cannot be used in a lambda; a lambda's value is its body's value")
		if e.Value != nil {
			c.expr(e.Value)
		}
		return
	}
	want := c.fn.Result
	if e.Value == nil {
		if want != Ok {
			c.errorf(e.Pos, "function %s must return a value of type %s", c.fn.Decl.Name, want)
		}
		return
	}
	if want == Ok {
		if t := c.expr(e.Value); isValue(t) && !isOkTask(t) {
			c.errorf(e.Value.Position(), "function %s does not return a value", c.fn.Decl.Name)
		}
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
		c.info.conversions[e] = &Conversion{From: from, To: to}
		return to
	}
	c.info.conversions[e] = &Conversion{From: from, To: to, Checked: true}
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

// PackageNamed resolves a loaded package by its full import path.
func (info *Info) PackageNamed(path string) *Package {
	for _, pkg := range info.Packages {
		if pkg.Path == path {
			return pkg
		}
	}
	return nil
}
