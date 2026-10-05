package syntax

import "github.com/GiGurra/bork/internal/diag"

// File is one parsed .bork source file.
type File struct {
	// ExpressionSpans retains parser ranges, including grouping, for compiler
	// source queries and refactorings. One node can have several grouped spans.
	ExpressionSpans []ExpressionSpan
	// Script marks a single executable file with an implicit main.
	Script bool
	Path   string
	// Source retains the original text for compile-time debug probes.
	Source string
	// Package is the import path of the file's package (set by the
	// driver), and Imports what the file imports.
	Package   string
	Imports   []*Import
	Uses      []*Use
	Bundles   []*Bundle
	Providers []*ProviderBundle
	Rules     []*RuleDecl
	// Classes and Instances; the instances' methods are also in Funcs.
	Classes   []*ClassDecl
	Instances []*InstanceDecl
	// Prelude is set for the compiler's built-in prelude.
	Prelude  bool
	Types    []*TypeDecl
	Ambients []*AmbientDecl
	Funcs    []*FuncDecl
	// Bindings contains immutable package values.
	Bindings []*Binding
	Tests    []*TestDecl
	Comments []Comment
}

type SourceSpan struct{ Start, End diag.Pos }
type ExpressionSpan struct {
	Expr Expr
	SourceSpan
}

// ProviderBundle is a compile-time named provider list.
type ProviderBundle struct {
	Pos     diag.Pos
	NamePos diag.Pos
	Name    string
	Entries []*ProviderEntry
}

type ProviderEntry struct {
	Pos      diag.Pos
	Name     string
	Provider Expr
}

// Import is `import "example.com/shop/money"` or, with a name to use
// instead of the path's last element, `import cash "example.com/shop/money"`.
//
// The parser reads a use of an imported name, `money.add` or
// `money.Amount`, as one qualified name: an Ident (or type, predicate,
// or pattern name) "money.add".
type Import struct {
	Pos  diag.Pos
	End  diag.Pos // after the quoted import path
	Name string   // what the file calls the package
	Path string
}

// TestDecl is a test: `test "adds numbers" { assert(add(1, 2) == 3) }`.
// Tests run with `bork test`, and are left out of programs. A test with
// parameters is a property test, run on generated values of them:
// `test "doubles" (n: Int where small) { assert(double(n) > n) }`.
type TestDecl struct {
	Pos    diag.Pos
	Name   string
	Params []*Param
	Body   *Block
}

// TypeKind says what a type declaration declares.
type TypeKind int

const (
	RecordType   TypeKind = iota // type User = { name: String }
	SealedType                   // type Shape = sealed { Circle { radius: Int }, Empty }
	AliasType                    // type Result = User | NotFound
	ResourceType                 // type File = resource
	GoType                       // type Request = go "*net/http.Request"
)

// TypeDecl is `type Name = ...`.
type TypeDecl struct {
	Pos  diag.Pos
	Name string
	// TypeParams lists a generic type's parameters: `type Pair[A, B]`.
	TypeParams []*TypeParam
	GoName     *GoBind
	Kind       TypeKind
	Private    bool           // record construction belongs to its package
	Fields     []*FieldDecl   // RecordType
	Variants   []*VariantDecl // SealedType
	Alias      *TypeExpr      // AliasType
	Where      []*PredRef     // whole-value invariants on records and sealed types
	// Derive lists the classes to derive instances of:
	// `type User = { ... } derive (Decode, Encode)`.
	Derive    []string
	DerivePos diag.Pos
}

type FieldDecl struct {
	Lazy    bool
	LazyPos diag.Pos
	GoTags  []GoTag
	Default Expr
	Doc     string
	Pos     diag.Pos
	Name    string
	Type    *TypeExpr
}

// GoTag is an ordered Go struct tag on a generated field.
type GoTag struct {
	Pos         diag.Pos
	Name, Value string
}

type VariantDecl struct {
	Pos    diag.Pos
	Name   string
	Fields []*FieldDecl
	Where  []*PredRef
}

// FuncDecl is `fn name(params): Result { body }`. Result is nil when
// the function returns Ok. A function implemented in Go has GoBody
// instead of Body.
type FuncDecl struct {
	// ScriptMain is the synthetic entrypoint of a script.
	ScriptMain bool
	// Constructor names the owning record in `fn New = Config.new`.
	Constructor *TypeExpr
	Pos         diag.Pos
	End         diag.Pos // just after the complete declaration, including a body when present
	Name        string
	// IsPred is set for `pred name(x: T, ...) { ... }`: a function
	// returning Bool that can be used in `where` clauses.
	IsPred bool
	// TypeParams lists the type parameters of a generic function:
	// `fn map[A, B](...)`.
	TypeParams []*TypeParam
	Params     []*Param
	// ParamsEnd is the position of the ')' that ends the parameters.
	ParamsEnd diag.Pos
	// Requires relates function inputs; result constraints remain on Result.
	Requires Expr
	// Uses lists the effects the function may have, `uses io + net`,
	// or is nil when it declares none.
	Uses *Uses
	// Needs lists the ambient values it reads, `needs traceId +
	// locale?`, or is nil when it reads none.
	Needs  *Needs
	Result *TypeExpr
	Body   *Block
	GoBody *GoCode
	// GoBind is set instead of a body for a binding to a Go function:
	// `fn Getenv(key: String): String unsafe go "os.Getenv"`.
	GoBind *GoBind
	// Instance is set for a method of an instance declaration.
	Instance *InstanceDecl
	// IsMethod is set for a method, `fn (xs: List[T]) first[T](): T`:
	// its receiver is its first parameter.
	IsMethod bool
}

// ClassDecl is a type class: `class Show[T] { fn show(x: T): String }`.
// Its methods are signatures, without bodies.
type ClassDecl struct {
	Pos        diag.Pos
	Name       string
	TypeParams []*TypeParam
	Methods    []*FuncDecl
}

// InstanceDecl is a named instance of a class for a type:
// `instance showInt: Show[Int] { fn show(x: Int): String { ... } }`, or
// generic: `instance showList[T: Show]: Show[List[T]] { ... }`.
type InstanceDecl struct {
	Pos        diag.Pos
	Name       string
	TypeParams []*TypeParam
	Class      string // possibly qualified: fmt.Show
	ClassPos   diag.Pos
	Type       *TypeExpr
	Methods    []*FuncDecl
}

// Bundle names a set of instances, which `use` brings into scope at
// once: `instances Json { ItemDecode, ItemEncode, money.* }`. A
// package suggests instances to its importers with one.
type Bundle struct {
	Pos   diag.Pos
	Name  string
	Items []*Use // instances, bundles, and pkg.* of imported packages
}

// Use brings instances of another package into scope: `use money.showAmount`,
// or every exported one: `use money.*`.
type Use struct {
	Pos  diag.Pos
	Name string // "money.showAmount" or "money.*"
}

// GoCode is the body of `unsafe go { ... }`: Go statements, preceded
// by optional `import [alias] "path"` lines.
type GoCode struct {
	Pos     diag.Pos // position of '{'
	Imports []string
	// ImportAliases contains explicit aliases by path.
	ImportAliases map[string]string
	// Body is the text between the braces, with the import lines
	// blanked out, so its lines still match the source.
	Body string
}

// GoBind names the Go function a binding calls, with its import path:
// "os.Getenv", "crypto/sha256.Sum256".
type GoBind struct {
	Pos  diag.Pos // position of the string
	Name string
}

// IsGo reports whether the function is implemented in Go, by an
// `unsafe go` body or a binding: bork trusts its signature.
func (fd *FuncDecl) IsGo() bool { return fd.GoBody != nil || fd.GoBind != nil }

// TypeParam is a declared type parameter.
type TypeParam struct {
	Pos  diag.Pos
	Name string
	// Bounds lists the classes the type must have instances of:
	// `T: Show + Eq`.
	Bounds []string
}

// Param is a function's or a lambda's parameter. A lambda's parameter
// may leave out its type (Type is nil).
type Param struct {
	Pos  diag.Pos
	Name string
	Type *TypeExpr
	// Default is the value of a function parameter that a call may
	// leave out (`attrs: Map[String, Int] = {:}`), or nil.
	Default Expr
	// In names the parameter (a Scope or an OwnedScope) whose scope this
	// one belongs to, in `conn: Conn in prev`; "" if none.
	In    string
	InPos diag.Pos
}

// TypeExpr is a written type: a name with optional type arguments
// (`Option[Int]`), or a union of types (`User | NotFound`).
type TypeExpr struct {
	Uses *Uses // latent effects on Seq[T]

	Pos   diag.Pos
	Name  string
	Args  []*TypeExpr
	Tuple []*TypeExpr // non-nil for a positional tuple type
	Union []*TypeExpr // non-nil for a union; Name and Args are then unused
	// Func is set for a function type `(A, B) => C`; Name and Args are
	// then unused.
	Func *FuncTypeExpr
	// Where lists the clauses of `T where p and (q(1) or r)`, all of
	// which must hold.
	Where []*PredRef
}

// FuncTypeExpr is a function type: `(A, B) => C`, or with effects,
// `(A) uses io => C`.
type FuncTypeExpr struct {
	Params []*TypeExpr
	Uses   *Uses
	Result *TypeExpr
}

// Uses is a declaration of effects: `uses io + net`, or `uses nothing`
// (Effects is then empty).
type Uses struct {
	Pos     diag.Pos
	End     diag.Pos // just after the last effect (or nothing)
	Effects []Effect
}

// AmbientDecl declares an ambient value: `ambient traceId: String`,
// optionally marked `logged` (its value is added to every log line
// written while a with binds it) or `propagated("header")` (sent
// across process boundaries under that name).
type AmbientDecl struct {
	Pos  diag.Pos
	Name string
	Type *TypeExpr
	// Logged is the position of the logged marker, if present.
	Logged *diag.Pos
	// Propagated is the propagated marker, if present.
	Propagated *Propagated
}

// Propagated is the `propagated("traceparent")` marker of an ambient
// declaration: the name of the header (or message metadata) that
// carries the value.
type Propagated struct {
	Pos       diag.Pos
	Header    string
	HeaderPos diag.Pos
}

// Needs is a declaration of the ambient values a function reads:
// `needs traceId + locale?`.
type Needs struct {
	Pos   diag.Pos
	End   diag.Pos // just after the last need
	Items []*Need
}

// Need is one ambient value named in a `needs` declaration; Optional is
// set for `locale?`, which is read as an Option.
type Need struct {
	Pos      diag.Pos
	Name     string // as written: traceId, or trace.Id
	Optional bool
}

// WithExpr binds ambient values for a block:
// `with (traceId: id, principal: p) { ... }`.
type WithExpr struct {
	Pos      diag.Pos
	Bindings []*WithBinding
	Body     *Block
}

// WithBinding is one `name: value` of a with.
type WithBinding struct {
	Pos   diag.Pos
	Name  string // as written: traceId, or trace.Id
	Value Expr
}

// Effect is one effect named in a `uses` declaration.
type Effect struct {
	Pos  diag.Pos
	Name string
}

// PredRef is one predicate in a where clause: `positive`, or
// `between(1, 65535)`, whose arguments are constants or parameter
// names. The constrained value itself is the predicate's first argument
// and is not written. A clause with alternatives, `p or q`, has the
// first in Name and Args and the rest in Or.
type PredRef struct {
	Pos  diag.Pos
	Name string
	Args []Expr
	Or   []*PredRef
}

// RuleDecl is an inference rule:
//
//	rule weaken(x: Int, a: Int, b: Int) {
//	  atLeast(x, a) and a >= b => atLeast(x, b)
//	}
//
// Premises are predicate calls (facts to find) or other Bool
// expressions (conditions, computed when their variables are constants).
// Conclusions are predicate calls.
type RuleDecl struct {
	Pos         diag.Pos
	Name        string
	Params      []*Param
	Premises    []Expr
	Conclusions []Expr
}

// TrustStmt is `trust p(x, ...)`: from here on, p(x, ...) is taken as
// a fact without proof.
type TrustStmt struct {
	Pos  diag.Pos
	Call *Call
}

// MockStmt is `mock target(a, b) { ... }`, or `m = mock target(...)
// { ... }` with a handle m that records the calls: in a test, it
// replaces the function target until the end of the enclosing block.
// Target is an *Ident (`fetch`, `payments.Charge`) or a *Selector
// naming a method (`Store.save`, `model.Point.Value`). Params holds the
// parameters' names only (no types); `_` ignores one.
type MockStmt struct {
	Pos     diag.Pos // the binding's name, or 'mock'
	Name    string   // the handle's name, or ""
	MockPos diag.Pos // 'mock'
	Target  Expr
	Params  []*Param
	// ParamsStart and ParamsEnd are the positions of the '(' and ')'
	// around the parameters.
	ParamsStart, ParamsEnd diag.Pos
	// Requires relates function inputs; result constraints remain on Result.
	Requires Expr
	Body     *Block
}

// Stmt is a statement inside a block.
type Stmt interface{ stmtNode() }

// Binding is `name = value`, or `name: Type = value`. Bindings are
// immutable.
type Binding struct {
	// Package values use process-lifetime memo cells, including without lazy.
	Package    bool
	AsyncScope Expr
	AsyncPos   diag.Pos
	Lazy       bool
	LazyPos    diag.Pos
	Pos        diag.Pos
	Name       string
	Type       *TypeExpr // nil if not written
	Value      Expr
}

// ExprStmt is an expression evaluated for its effect.
type TupleBinding struct {
	Pos     diag.Pos
	Pattern *TuplePat
	Value   Expr
}

func (*TupleBinding) stmtNode() {}

type ExprStmt struct {
	X Expr
}

func (*Binding) stmtNode()   {}
func (*TrustStmt) stmtNode() {}
func (*ExprStmt) stmtNode()  {}
func (*MockStmt) stmtNode()  {}

// Expr is an expression. Everything that produces a value is an
// expression, including if, blocks, and return.
type Expr interface {
	exprNode()
	Position() diag.Pos
}

// Comptime is an explicit build-time value computation.
type Comptime struct {
	Pos  diag.Pos
	Body *Block
}

func (*Comptime) exprNode()            {}
func (e *Comptime) Position() diag.Pos { return e.Pos }

type Generate struct {
	Pos  diag.Pos
	Elem *TypeExpr
	Body *Block
}
type Yield struct {
	Pos   diag.Pos
	Value Expr
}
type For struct {
	Pos     diag.Pos
	Name    string
	NamePos diag.Pos
	Items   Expr
	Body    *Block
}
type LoopControl struct {
	Pos      diag.Pos
	Continue bool
}

func (*Generate) exprNode()               {}
func (*Yield) exprNode()                  {}
func (*For) exprNode()                    {}
func (*LoopControl) exprNode()            {}
func (e *Generate) Position() diag.Pos    { return e.Pos }
func (e *Yield) Position() diag.Pos       { return e.Pos }
func (e *For) Position() diag.Pos         { return e.Pos }
func (e *LoopControl) Position() diag.Pos { return e.Pos }

type TupleLit struct {
	Pos, End diag.Pos
	Elems    []Expr
}

func (*TupleLit) exprNode()            {}
func (e *TupleLit) Position() diag.Pos { return e.Pos }

type TuplePat struct {
	Pos, End diag.Pos
	Elems    []Pattern
}

func (*TuplePat) patternNode()         {}
func (p *TuplePat) Position() diag.Pos { return p.Pos }

type IntLit struct {
	Pos  diag.Pos
	Text string
}

// FloatLit is a floating-point literal: `1.5`, `2e10`.
type FloatLit struct {
	Pos  diag.Pos
	Text string
}

// RuneLit is a rune (Unicode code point) literal: 'a', '\n'.
type RuneLit struct {
	Pos  diag.Pos
	Text string
}

// Interp is an interpolated string: s"Hello $name, ${age + 1}". Parts
// holds the (unquoted) text around the expressions, so it has one more
// element than Exprs.
type Interp struct {
	Prefix    Expr     // nil for the built-in s prefix
	PrefixEnd diag.Pos // opening quote position for a named prefix
	Pos       diag.Pos
	Parts     []string
	Exprs     []Expr
}

// StaticPartsLit is created only by the checker for a named interpolation.
// It has no source syntax: runtime Strings cannot acquire literal provenance.
type StaticPartsLit struct {
	Pos   diag.Pos
	Parts []string
}

type StringLit struct {
	Pos   diag.Pos
	Value string // unquoted
}

type BoolLit struct {
	Pos   diag.Pos
	Value bool
}

type Ident struct {
	Pos  diag.Pos
	Name string
}

type Unary struct {
	Pos diag.Pos
	Op  Kind
	X   Expr
}

type Binary struct {
	Pos  diag.Pos // position of the operator
	Op   Kind
	X, Y Expr
}

type Call struct {
	Start  diag.Pos // start of the callee, including grouping parentheses
	Pos    diag.Pos // position of '('
	End    diag.Pos // position after the closing ')'
	Fun    Expr
	FunEnd diag.Pos // position after the callee, before type arguments
	Args   []Expr
	// Arguments retains labels and source ranges, parallel to Args. A
	// synthesized pipeline input has no label.
	Arguments []Argument
	// TypeArgs are explicit type arguments: `empty[Int]()`.
	TypeArgs []*TypeExpr
	// Pipe retains the source of a desugared pipeline for diagnostics.
	Pipe               diag.Pos
	PipeStart, PipeEnd diag.Pos // range of the receiver expression
	PipeTargetEnd      diag.Pos // end of the original pipeline target
	PipeWrap           bool     // receiver needs parentheses before a selector
	PipeBare           bool     // target was written without call parentheses
}

// Argument describes a call argument as written; Name is empty for a
// positional argument. End is after its expression, NameEnd after its label.
type Argument struct {
	Name              string
	Pos, NameEnd, End diag.Pos
	ValueStart        diag.Pos
	// RemovalStart includes the preceding comma, when no comment would
	// be removed with it. Zero for the first argument or commented spans.
	RemovalStart diag.Pos
}

// If is `if (cond) { ... } else { ... }`. Else is nil, a *Block, or an
// *If (for `else if`).
type If struct {
	Pos  diag.Pos
	Cond Expr
	Then *Block
	Else Expr
}

// Block is `{ stmts; tail }`. Tail is the block's value; nil means the
// block's value is Ok (or Never, if it ends by returning).
type Block struct {
	Pos   diag.Pos
	Stmts []Stmt
	Tail  Expr
	End   diag.Pos // position of the closing '}'
}

// Return is `return` or `return value`. Its own type is Never.
type Return struct {
	Pos   diag.Pos
	Value Expr
}

// Selector is `x.name`: a field access, or a qualified variant
// (`Shape.Empty`).
type Selector struct {
	Pos  diag.Pos // position of the name
	X    Expr
	Name string
}

// ContextName omits a constructor's owner: .{ ... } or .Variant.
// Name is empty for a record, and End is after the dot or variant name.
type ContextName struct {
	Pos, End diag.Pos
	NamePos  diag.Pos
	Name     string
}

// TypeHead specializes a constructor owner, as in Box[Int] or Option[Int].None.
type TypeHead struct {
	Type *TypeExpr
	End  diag.Pos
}

// RecordLit is `User { name: "Ada", age: 36 }` or
// `Shape.Circle { radius: 3 }`.
type RecordLit struct {
	Type   Expr // *Ident, *TypeHead, *Selector or *ContextName
	Fields []*FieldInit
	End    diag.Pos
}

type FieldInit struct {
	Pos   diag.Pos
	Name  string
	Value Expr
}

// Copy is `x.copy(age: 37, address.city: "Oslo")`: a new record equal
// to x except for the given (possibly nested) fields.
type Copy struct {
	Pos     diag.Pos // position of 'copy'
	X       Expr
	Updates []*CopyUpdate
}

type CopyUpdate struct {
	PathEnd              diag.Pos
	ValueStart, ValueEnd diag.Pos
	Pos                  diag.Pos
	Path                 []string
	Value                Expr
}

// Match is `match (x) { pattern => value, ... }`.
type Match struct {
	Pos               diag.Pos
	Close             diag.Pos // the closing brace, before which new arms can be inserted
	TrailingSeparator bool     // the final arm already has a comma or newline
	X                 Expr
	Arms              []*Arm
}

type Arm struct {
	Pattern Pattern
	Body    Expr
}

// Try is `x?`: keep the leftmost member of x's type, and return every
// other member from the enclosing function.
type Try struct {
	Pos diag.Pos // position of '?'
	X   Expr
}

// Pattern is a match pattern.
type Pattern interface {
	patternNode()
	Position() diag.Pos
}

// WildcardPat is `_`.
type WildcardPat struct{ Pos diag.Pos }

// TypePat is `name: Type`; it matches values of that type and binds them.
type TypePat struct {
	Pos  diag.Pos
	Name string
	Type *TypeExpr
}

// VariantPat is a name pattern: a variant (`Shape.Circle { radius }`,
// `Option.None`), a type (`NotFound`), a record destructure
// (`User { name }`), or, for a single name that is not a type, a
// binding of the whole value (`n`).
// ListPat matches a list: `[]`, `[a, b]`, or `[first, ...rest]` (Rest
// binds the remaining elements; `[first, ...]` ignores them).
type ListPat struct {
	Pos     diag.Pos
	Elems   []Pattern
	HasRest bool
	Rest    string // "" when ignored
	RestPos diag.Pos
}

type VariantPat struct {
	Pos     diag.Pos
	NamePos diag.Pos // written context variant identifier, after whitespace/comments
	End     diag.Pos // end of a context variant name, for diagnostics and fixes
	Context bool     // owner omitted with .Variant
	Path    []string
	Fields  []*FieldPat
	Braces  bool // written with { ... }, possibly empty
}

// FieldPat is one field of a destructuring pattern: `{ radius }` binds
// the field to its own name, and `{ radius: p }` matches it against the
// pattern p (a name to bind, a literal, a nested pattern, ...).
type FieldPat struct {
	Pos     diag.Pos
	Field   string
	Pattern Pattern // nil for the `{ radius }` shorthand
}

// LitPat is a literal pattern: a number, String, or Bool literal.
type LitPat struct {
	Pos   diag.Pos
	Value Expr
}

func (*WildcardPat) patternNode() {}
func (*TypePat) patternNode()     {}
func (*VariantPat) patternNode()  {}
func (*ListPat) patternNode()     {}
func (*LitPat) patternNode()      {}

func (p *WildcardPat) Position() diag.Pos { return p.Pos }
func (p *TypePat) Position() diag.Pos     { return p.Pos }
func (p *VariantPat) Position() diag.Pos  { return p.Pos }
func (p *ListPat) Position() diag.Pos     { return p.Pos }
func (p *LitPat) Position() diag.Pos      { return p.Pos }

// Lambda is a function value: `x => x + 1`, `(a, b) => a + b`, or
// `(x: Int) => { ... }`.
type Lambda struct {
	Pos    diag.Pos
	Params []*Param
	Body   Expr
}

// ScopeExpr is `scope s { ... }`: a block with a scope named s, which
// closes (running its finalizers) when the block ends.
type ScopeExpr struct {
	Pos  diag.Pos
	Name string
	// Policies are those of `scope s with taskTimeout(100),
	// cleanupTimeout(500) { ... }`; none for the defaults.
	Policies []Expr
	Body     *Block
}

// ListLit is a list literal: `[1, 2, 3]`.
type ListLit struct {
	Pos   diag.Pos
	Elems []Expr
}

// MapLit is a map literal: `{"a": 1, "b": 2}`, or `{:}` for the empty
// map.
type MapLit struct {
	Pos    diag.Pos
	Keys   []Expr
	Values []Expr
}

func (*MapLit) exprNode()               {}
func (e *MapLit) Position() diag.Pos    { return e.Pos }
func (*ScopeExpr) exprNode()            {}
func (e *ScopeExpr) Position() diag.Pos { return e.Pos }
func (*WithExpr) exprNode()             {}
func (e *WithExpr) Position() diag.Pos  { return e.Pos }
func (*Lambda) exprNode()               {}
func (*ListLit) exprNode()              {}
func (e *Lambda) Position() diag.Pos    { return e.Pos }
func (e *ListLit) Position() diag.Pos   { return e.Pos }

func (*Selector) exprNode()    {}
func (*ContextName) exprNode() {}
func (*TypeHead) exprNode()    {}
func (*RecordLit) exprNode()   {}
func (*Copy) exprNode()        {}
func (*Match) exprNode()       {}
func (*Try) exprNode()         {}

func (e *Selector) Position() diag.Pos    { return e.X.Position() }
func (e *ContextName) Position() diag.Pos { return e.Pos }
func (e *TypeHead) Position() diag.Pos    { return e.Type.Pos }
func (e *RecordLit) Position() diag.Pos   { return e.Type.Position() }
func (e *Copy) Position() diag.Pos        { return e.X.Position() }
func (e *Match) Position() diag.Pos       { return e.Pos }
func (e *Try) Position() diag.Pos         { return e.X.Position() }

func (*IntLit) exprNode()         {}
func (*FloatLit) exprNode()       {}
func (*Interp) exprNode()         {}
func (*StaticPartsLit) exprNode() {}
func (*RuneLit) exprNode()        {}
func (*StringLit) exprNode()      {}
func (*BoolLit) exprNode()        {}
func (*Ident) exprNode()          {}
func (*Unary) exprNode()          {}
func (*Binary) exprNode()         {}
func (*Call) exprNode()           {}
func (*If) exprNode()             {}
func (*Block) exprNode()          {}
func (*Return) exprNode()         {}

func (e *IntLit) Position() diag.Pos         { return e.Pos }
func (e *FloatLit) Position() diag.Pos       { return e.Pos }
func (e *Interp) Position() diag.Pos         { return e.Pos }
func (e *StaticPartsLit) Position() diag.Pos { return e.Pos }
func (e *RuneLit) Position() diag.Pos        { return e.Pos }
func (e *StringLit) Position() diag.Pos      { return e.Pos }
func (e *BoolLit) Position() diag.Pos        { return e.Pos }
func (e *Ident) Position() diag.Pos          { return e.Pos }
func (e *Unary) Position() diag.Pos          { return e.Pos }
func (e *Binary) Position() diag.Pos         { return e.X.Position() }
func (e *Call) Position() diag.Pos           { return e.Fun.Position() }
func (e *If) Position() diag.Pos             { return e.Pos }
func (e *Block) Position() diag.Pos          { return e.Pos }
func (e *Return) Position() diag.Pos         { return e.Pos }
