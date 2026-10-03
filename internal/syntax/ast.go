package syntax

import "github.com/GiGurra/bork/internal/diag"

// File is one parsed .bork source file.
type File struct {
	Path string
	// Package is the import path of the file's package (set by the
	// driver), and Imports what the file imports.
	Package string
	Imports []*Import
	Uses    []*Use
	Bundles []*Bundle
	Rules   []*RuleDecl
	// Classes and Instances; the instances' methods are also in Funcs.
	Classes   []*ClassDecl
	Instances []*InstanceDecl
	// Prelude is set for the compiler's built-in prelude.bork.
	Prelude  bool
	Types    []*TypeDecl
	Funcs    []*FuncDecl
	Tests    []*TestDecl
	Comments []Comment
}

// Import is `import "example.com/shop/money"` or, with a name to use
// instead of the path's last element, `import cash "example.com/shop/money"`.
//
// The parser reads a use of an imported name, `money.add` or
// `money.Amount`, as one qualified name: an Ident (or type, predicate,
// or pattern name) "money.add".
type Import struct {
	Pos  diag.Pos
	Name string // what the file calls the package
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
)

// TypeDecl is `type Name = ...`.
type TypeDecl struct {
	Pos  diag.Pos
	Name string
	// TypeParams lists a generic type's parameters: `type Pair[A, B]`.
	TypeParams []*TypeParam
	Kind       TypeKind
	Fields     []*FieldDecl   // RecordType
	Variants   []*VariantDecl // SealedType
	Alias      *TypeExpr      // AliasType
	// Derive lists the classes to derive instances of:
	// `type User = { ... } derive (Decode, Encode)`.
	Derive    []string
	DerivePos diag.Pos
}

type FieldDecl struct {
	Pos  diag.Pos
	Name string
	Type *TypeExpr
}

type VariantDecl struct {
	Pos    diag.Pos
	Name   string
	Fields []*FieldDecl
}

// FuncDecl is `fn name(params): Result { body }`. Result is nil when
// the function returns Unit. A function implemented in Go has GoBody
// instead of Body.
type FuncDecl struct {
	Pos  diag.Pos
	Name string
	// IsPred is set for `pred name(x: T, ...) { ... }`: a function
	// returning Bool that can be used in `where` clauses.
	IsPred bool
	// TypeParams lists the type parameters of a generic function:
	// `fn map[A, B](...)`.
	TypeParams []*TypeParam
	Params     []*Param
	// ParamsEnd is the position of the ')' that ends the parameters.
	ParamsEnd diag.Pos
	// Uses lists the effects the function may have, `uses io + net`,
	// or is nil when it declares none.
	Uses   *Uses
	Result *TypeExpr
	Body   *Block
	GoBody *GoCode
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
// by optional `import "path"` lines.
type GoCode struct {
	Pos     diag.Pos // position of '{'
	Imports []string
	// Body is the text between the braces, with the import lines
	// blanked out, so its lines still match the source.
	Body string
}

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
}

// TypeExpr is a written type: a name with optional type arguments
// (`Option[Int]`), or a union of types (`User | NotFound`).
type TypeExpr struct {
	Pos   diag.Pos
	Name  string
	Args  []*TypeExpr
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

// Stmt is a statement inside a block.
type Stmt interface{ stmtNode() }

// Binding is `name = value`, or `name: Type = value`. Bindings are
// immutable.
type Binding struct {
	Pos   diag.Pos
	Name  string
	Type  *TypeExpr // nil if not written
	Value Expr
}

// ExprStmt is an expression evaluated for its effect.
type ExprStmt struct {
	X Expr
}

func (*Binding) stmtNode()   {}
func (*TrustStmt) stmtNode() {}
func (*ExprStmt) stmtNode()  {}

// Expr is an expression. Everything that produces a value is an
// expression, including if, blocks, and return.
type Expr interface {
	exprNode()
	Position() diag.Pos
}

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
	Pos   diag.Pos
	Parts []string
	Exprs []Expr
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
	Pos  diag.Pos // position of '('
	End  diag.Pos // position after the closing ')'
	Fun  Expr
	Args []Expr
	// TypeArgs are explicit type arguments: `empty[Int]()`.
	TypeArgs []*TypeExpr
	// Pipe retains the source of a desugared pipeline for diagnostics.
	Pipe               diag.Pos
	PipeStart, PipeEnd diag.Pos // range of the receiver expression
	PipeTargetEnd      diag.Pos // end of the original pipeline target
	PipeWrap           bool     // receiver needs parentheses before a selector
	PipeBare           bool     // target was written without call parentheses
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
// block's value is Unit (or Never, if it ends by returning).
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

// RecordLit is `User { name: "Ada", age: 36 }` or
// `Shape.Circle { radius: 3 }`.
type RecordLit struct {
	Type   Expr // *Ident or *Selector
	Fields []*FieldInit
	End    diag.Pos
}

type FieldInit struct {
	Pos   diag.Pos
	Name  string
	Value Expr
}

// Copy is `x.copy(age = 37, address.city = "Oslo")`: a new record equal
// to x except for the given (possibly nested) fields.
type Copy struct {
	Pos     diag.Pos // position of 'copy'
	X       Expr
	Updates []*CopyUpdate
}

type CopyUpdate struct {
	Pos   diag.Pos
	Path  []string
	Value Expr
}

// Match is `match (x) { pattern => value, ... }`.
type Match struct {
	Pos  diag.Pos
	X    Expr
	Arms []*Arm
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
	Pos    diag.Pos
	Path   []string
	Fields []*FieldPat
	Braces bool // written with { ... }, possibly empty
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
func (*Lambda) exprNode()               {}
func (*ListLit) exprNode()              {}
func (e *Lambda) Position() diag.Pos    { return e.Pos }
func (e *ListLit) Position() diag.Pos   { return e.Pos }

func (*Selector) exprNode()  {}
func (*RecordLit) exprNode() {}
func (*Copy) exprNode()      {}
func (*Match) exprNode()     {}
func (*Try) exprNode()       {}

func (e *Selector) Position() diag.Pos  { return e.X.Position() }
func (e *RecordLit) Position() diag.Pos { return e.Type.Position() }
func (e *Copy) Position() diag.Pos      { return e.X.Position() }
func (e *Match) Position() diag.Pos     { return e.Pos }
func (e *Try) Position() diag.Pos       { return e.X.Position() }

func (*IntLit) exprNode()    {}
func (*FloatLit) exprNode()  {}
func (*Interp) exprNode()    {}
func (*RuneLit) exprNode()   {}
func (*StringLit) exprNode() {}
func (*BoolLit) exprNode()   {}
func (*Ident) exprNode()     {}
func (*Unary) exprNode()     {}
func (*Binary) exprNode()    {}
func (*Call) exprNode()      {}
func (*If) exprNode()        {}
func (*Block) exprNode()     {}
func (*Return) exprNode()    {}

func (e *IntLit) Position() diag.Pos    { return e.Pos }
func (e *FloatLit) Position() diag.Pos  { return e.Pos }
func (e *Interp) Position() diag.Pos    { return e.Pos }
func (e *RuneLit) Position() diag.Pos   { return e.Pos }
func (e *StringLit) Position() diag.Pos { return e.Pos }
func (e *BoolLit) Position() diag.Pos   { return e.Pos }
func (e *Ident) Position() diag.Pos     { return e.Pos }
func (e *Unary) Position() diag.Pos     { return e.Pos }
func (e *Binary) Position() diag.Pos    { return e.X.Position() }
func (e *Call) Position() diag.Pos      { return e.Fun.Position() }
func (e *If) Position() diag.Pos        { return e.Pos }
func (e *Block) Position() diag.Pos     { return e.Pos }
func (e *Return) Position() diag.Pos    { return e.Pos }
