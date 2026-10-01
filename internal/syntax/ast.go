package syntax

import "github.com/GiGurra/bork/internal/diag"

// File is one parsed .bork source file.
type File struct {
	Path     string
	Funcs    []*FuncDecl
	Comments []Comment
}

// FuncDecl is `fn name(params): Result { body }`. Result is nil when
// the function returns Unit.
type FuncDecl struct {
	Pos    diag.Pos
	Name   string
	Params []*Param
	Result *TypeExpr
	Body   *Block
}

type Param struct {
	Pos  diag.Pos
	Name string
	Type *TypeExpr
}

// TypeExpr is a written type. In this first slice, only named types.
type TypeExpr struct {
	Pos  diag.Pos
	Name string
}

// Stmt is a statement inside a block.
type Stmt interface{ stmtNode() }

// Binding is `name = value`. Bindings are immutable.
type Binding struct {
	Pos   diag.Pos
	Name  string
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

func (*IntLit) exprNode()    {}
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
func (e *StringLit) Position() diag.Pos { return e.Pos }
func (e *BoolLit) Position() diag.Pos   { return e.Pos }
func (e *Ident) Position() diag.Pos     { return e.Pos }
func (e *Unary) Position() diag.Pos     { return e.Pos }
func (e *Binary) Position() diag.Pos    { return e.X.Position() }
func (e *Call) Position() diag.Pos      { return e.Fun.Position() }
func (e *If) Position() diag.Pos        { return e.Pos }
func (e *Block) Position() diag.Pos     { return e.Pos }
func (e *Return) Position() diag.Pos    { return e.Pos }
