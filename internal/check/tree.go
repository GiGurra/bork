package check

import (
	"go/constant"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// The typed tree is what the checker makes of a function's body: every
// expression with its type, names resolved to the variables and
// functions they refer to, and calls resolved to what they call, with
// their arguments as the callee takes them (a method's receiver first,
// and the defaults of parameters the call leaves out). The later passes
// (lifetimes, facts, and code generation) read it instead of the
// syntax, which the checker leaves as written.
//
// The checker builds it once a program checks without errors (see
// lower.go): each function's in Func.Body, and each rule's conditions
// in Rule.Conditions.

// Expr is a typed expression.
type Expr interface {
	// Pos is where the expression starts.
	Pos() diag.Pos
	// TokenPos is the operator, call parenthesis, or selected name that
	// identifies this expression in source queries. Pos remains its start.
	TokenPos() diag.Pos
	Type() Type
	exprNode()
}

// expr holds what every expression has.
type expr struct {
	pos   diag.Pos
	typ   Type
	token diag.Pos
}

func (e *expr) Pos() diag.Pos { return e.pos }
func (e *expr) TokenPos() diag.Pos {
	if e.token.File != "" {
		return e.token
	}
	return e.pos
}
func (e *expr) Type() Type { return e.typ }
func (*expr) exprNode()    {}

// Const is a constant: a number (or arithmetic on numbers), a String,
// or a Bool. A number has the value of its type: an Int64 for an
// integer type, or a float for a float type.
type Const struct {
	expr
	Value constant.Value
	// SourceSpan covers folded arithmetic whose operand nodes are no longer
	// in the typed tree. Source queries select the complete folded value.
	SourceSpan *SourceSpan
}

type SourceSpan struct {
	Start diag.Pos `json:"start"`
	End   diag.Pos `json:"end"`
}

// Interp is an interpolated string: s"Hello $name". Parts holds the
// text around the expressions, one more than Exprs.
type Interp struct {
	expr
	Parts []string
	Exprs []Expr
}

// VarKind says what declared a variable.
type VarKind int

const (
	VarParam       VarKind = iota // a function's parameter, or a rule's variable
	VarLambdaParam                // a lambda's parameter
	VarLet                        // a binding: name = value
	VarPattern                    // a name a match pattern binds
	VarLoop                       // an iteration binding
	VarScope                      // the scope of `scope s { ... }`
	VarAmbient                    // an ambient value a function needs
)

// Var is a variable: a parameter, a binding, a name bound by a pattern,
// or a scope.
type Var struct {
	// Label describes a compiler-generated variable in diagnostics.
	Label string
	Name  string
	// GoName is the variable's name in the generated Go, when it is not
	// Name (an ambient value's binding or hidden parameter).
	GoName string
	Pos    diag.Pos
	Type   Type
	Kind   VarKind
	// Index is a parameter's position among its function's (or
	// lambda's, or rule's) parameters.
	Index int
	// Let is a VarLet's binding.
	Let *Let
	// Source is where a VarPattern's value comes from.
	Source *VarSource
	// Need is the need a VarAmbient reads.
	Need *FuncNeed
	// Ambient is the ambient value a with binding (a VarLet) binds.
	Ambient *Ambient
	// Unused is set for a binding or pattern name that is never read.
	Unused bool
}

func (v *Var) displayName() string {
	if v.Label != "" {
		return v.Label
	}
	return v.Name
}

// VarSource is where a value bound by a match pattern comes from: the
// matched value, Subject. A name bound at the top of an arm's pattern
// is the subject narrowed to Member (or nil, if the pattern did not
// narrow it); one bound inside the pattern (`Option.Some { value: v }`)
// is the part of it at Path (".value"), the field Field.
type VarSource struct {
	Subject Expr
	Member  Type
	Path    string
	Field   *Field
}

// VarRef is a use of a variable.
type VarRef struct {
	expr
	Var *Var
}

// FuncRef is a declared function used as a value: `xs.map(double)`.
type FuncRef struct {
	expr
	Name string // as written
	Inst *Instance
	// Needs is what the reference binds for the function's needs, as
	// Call.Needs.
	Needs []Expr
}

// Unary is `-x` or `!x`.
type Unary struct {
	expr
	Op syntax.Kind
	X  Expr
}

// Binary is `x op y`.
type Binary struct {
	expr
	Op   syntax.Kind
	X, Y Expr
}

// Call is a call of a declared function (or method). Args are the
// arguments as the function takes them: a method's receiver first, then
// the arguments written, then the defaults of those left out.
type Call struct {
	expr
	Func *Func
	Inst *Instance
	Args []Expr
	// Needs holds what the call passes for each of Func's needs (in
	// the order of Func.Needs): a VarRef, Option.Some of one, or
	// Option.None.
	Needs []Expr
	// ArgOrder lists parameter indices in source evaluation order; nil
	// means declaration order. Args remains in declaration order for facts.
	ArgOrder []int
	Labels   []ArgumentLabel
	// Embedded is set for a compile-time bork/embed call.
	Embedded *Embedded
	// ReceiverCall distinguishes x.method(a) from Type.method(x, a).
	ReceiverCall bool
	// TypeArgNames holds, per type parameter, an explicit type argument
	// that is a plain name, as written (Port, not Int); "" for others.
	TypeArgNames []string
}

// ArgumentLabel identifies the parameter selected by a source argument label.
type ArgumentLabel struct {
	Name  string
	Pos   diag.Pos
	Param int
}

// EvaluationArgs gives arguments in source evaluation order. Args itself
// maps parameter identities for contracts and generic specialization.
func (c *Call) EvaluationArgs() []Expr {
	if c.ArgOrder == nil {
		return c.Args
	}
	out := make([]Expr, len(c.ArgOrder))
	for i, param := range c.ArgOrder {
		out[i] = c.Args[param]
	}
	return out
}

// CallBuiltin is a call of a function the compiler provides.
type CallBuiltin struct {
	expr
	Builtin Builtin
	// DebugText is the original argument text for dbg.
	DebugText string
	Name      string
	Args      []Expr
	// Conv describes a numeric conversion of a value that is not a
	// constant; nil for other builtins.
	Conv *Conversion
}

// CallValue is a call of a function value: `f(x)`, `r.handler(x)`.
type CallValue struct {
	expr
	Fun  Expr
	Args []Expr
}

// Lambda is a function value written in place. Its type is a
// *FuncType.
type Lambda struct {
	expr
	Params []*Var
	Body   Expr
}

// ListLit is a list literal: `[1, 2, 3]`.
type ListLit struct {
	expr
	Elems []Expr
}

// MapLit is a map literal: `{"a": 1}`.
type MapLit struct {
	expr
	Keys, Values []Expr
}

// If is `if (cond) { ... } else ...`. Else is nil, a *Block, or an *If.
type If struct {
	expr
	Cond Expr
	Then *Block
	Else Expr
}

// Block is `{ stmts; tail }`. Tail is nil when the block's value is
// Unit (or it never finishes).
type Block struct {
	expr
	Assembly   *Assembly
	Conversion *SourceSpan
	Stmts      []Stmt
	Tail       Expr
	End        diag.Pos // the closing '}'
	// Labels are the bindings of a with's logged or propagated values,
	// which the block publishes in the goroutine's labels once its
	// statements (the with's bindings) have run, until it ends.
	Labels []*Var
}

// ScopeBlock is `scope s { ... }`.
type ScopeBlock struct {
	expr
	Var      *Var
	Policies []Expr
	Body     *Block
}

// Return is `return` or `return value`.
type Return struct {
	expr
	Value Expr
}

// Select reads a record's field: `user.name`.
type Select struct {
	expr
	X     Expr
	Name  string
	Field *Field
}

// ConstructorHead retains written type uses for source queries.
type ConstructorHead struct {
	Start, End diag.Pos
	Uses       []TypeReference
}
type TypeReference struct {
	Pos        diag.Pos
	Name       string
	Type       Type
	Definition *diag.Pos
}

// VariantValue is a variant without fields: `Shape.Empty`. Text is the
// variant as written.
type VariantValue struct {
	expr
	Variant     *Variant
	Text        string
	Head        *ConstructorHead
	Constraints []*Constraint
}

// RecordLit builds a record (Variant nil) or a variant of a sealed
// type: `User { name: "Ada" }`, `Shape.Circle { radius: 3 }`. Fields
// are in the order written.
type RecordLit struct {
	expr
	Record      *Record
	Variant     *Variant
	Fields      []*FieldValue
	Head        *ConstructorHead
	Constraints []*Constraint
}

// FieldValue is a field of a record literal.
type FieldValue struct {
	IsDefault bool
	Name      string
	Field     *Field
	Value     Expr
}

// Copy is `x.copy(a: 1, b.c: 2)`.
type Copy struct {
	expr
	X       Expr
	Updates []*FieldUpdate
}

// FieldUpdate is a (possibly nested) field a copy changes: Field is the
// last one on Path.
type FieldUpdate struct {
	Path  []string
	Field *Field
	Value Expr
}

// Match is `match (x) { pattern => value, ... }`.
type Match struct {
	expr
	X    Expr
	Arms []*MatchArm
}

// MatchArm is an arm of a match: its checked pattern, and its value.
type MatchArm struct {
	Pat  *Pat
	Body Expr
}

// Try is `x?`.
type Try struct {
	expr
	X Expr
	TryInfo
}

// Stmt is a statement of a block.
type Stmt interface{ stmtNode() }

// Let binds a variable: `name = value` or `name: Type = value`. Var's
// name is "_" for a value that is only computed.
type Let struct {
	Pos   diag.Pos
	Var   *Var
	Value Expr
	// Declared is set when the binding's type is written; Constraints
	// are then its where clauses.
	Declared    bool
	Constraints []*Constraint
}

// ExprStmt is an expression evaluated for its effect.
type ExprStmt struct {
	X Expr
}

// Trust is `trust p(x, ...)`: Call is the call, a *Call of a predicate
// (or a *CallValue of a predicate parameter, which the facts pass
// reports). Text is the call as written, and SubjectText its first
// argument (x).
type Trust struct {
	Pos         diag.Pos
	Call        Expr
	Text        string
	SubjectText string
}

// Mock is `mock target(a, b) { ... }` in a test: until the end of the
// block, calls of Target run Func's body instead. Func is the mock's
// body as a function, with Target's signature (see Func.MockOf); Var is
// the handle that records the calls, or nil. Text is the target as
// written, which recorded calls are shown with.
type Mock struct {
	Pos    diag.Pos
	Target *Func
	// Text is the target as written, at TargetPos.
	Text      string
	TargetPos diag.Pos
	Var       *Var
	Func      *Func
}

func (*Mock) stmtNode()     {}
func (*Let) stmtNode()      {}
func (*ExprStmt) stmtNode() {}
func (*Trust) stmtNode()    {}

// Generate constructs a producer without running its body.
type Generate struct {
	expr
	Body        *Block
	Constraints []*Constraint
}
type Yield struct {
	expr
	Value Expr
	Elem  Type
}
type For struct {
	expr
	Var   *Var
	Items Expr
	Body  *Block
}
type LoopControl struct {
	expr
	Continue bool
}

type SeqCall struct {
	expr
	Op      string
	Args    []Expr
	Effects Effects
}
