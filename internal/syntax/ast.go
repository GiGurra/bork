package syntax

import "github.com/GiGurra/bork/internal/diag"

// File is one parsed .bork source file.
type File struct {
	Path string
	// Prelude is set for the compiler's built-in prelude.bork.
	Prelude  bool
	Types    []*TypeDecl
	Funcs    []*FuncDecl
	Comments []Comment
}

// TypeKind says what a type declaration declares.
type TypeKind int

const (
	RecordType TypeKind = iota // type User = { name: String }
	SealedType                 // type Shape = sealed { Circle { radius: Int }, Empty }
	AliasType                  // type Result = User | NotFound
)

// TypeDecl is `type Name = ...`.
type TypeDecl struct {
	Pos      diag.Pos
	Name     string
	Kind     TypeKind
	Fields   []*FieldDecl   // RecordType
	Variants []*VariantDecl // SealedType
	Alias    *TypeExpr      // AliasType
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
	Pos    diag.Pos
	Name   string
	Params []*Param
	Result *TypeExpr
	Body   *Block
	GoBody *GoCode
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

type Param struct {
	Pos  diag.Pos
	Name string
	Type *TypeExpr
}

// TypeExpr is a written type: a name with optional type arguments
// (`Option[Int]`), or a union of types (`User | NotFound`).
type TypeExpr struct {
	Pos   diag.Pos
	Name  string
	Args  []*TypeExpr
	Union []*TypeExpr // non-nil for a union; Name and Args are then unused
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

func (*Binding) stmtNode()  {}
func (*ExprStmt) stmtNode() {}

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
	Fun  Expr
	Args []Expr
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

// VariantPat is `Shape.Circle { radius }` or `Option.None`, optionally
// binding fields: `{ radius }` binds radius, `{ radius: r }` binds r.
type VariantPat struct {
	Pos    diag.Pos
	Path   []string
	Fields []*FieldPat
}

type FieldPat struct {
	Pos   diag.Pos
	Field string
	Bind  string
}

// LitPat is a literal pattern: a number, String, or Bool literal.
type LitPat struct {
	Pos   diag.Pos
	Value Expr
}

func (*WildcardPat) patternNode() {}
func (*TypePat) patternNode()     {}
func (*VariantPat) patternNode()  {}
func (*LitPat) patternNode()      {}

func (p *WildcardPat) Position() diag.Pos { return p.Pos }
func (p *TypePat) Position() diag.Pos     { return p.Pos }
func (p *VariantPat) Position() diag.Pos  { return p.Pos }
func (p *LitPat) Position() diag.Pos      { return p.Pos }

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
