package check

import (
	"crypto/sha256"
	"fmt"
	"go/constant"
	"reflect"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

const ExecutionAuditVersion = 1
const ExecutionIntrinsicVersion = 1
const executionAuditNodeLimit = 100000
const executionAuditDepthLimit = 256

// ExecutionAudit is owned static evidence only. It does not certify generated
// support/import initialization, the Go build closure, or native/policy inputs.
// Those independent inventories remain mandatory before any execution hit.
type ExecutionAudit struct {
	Decline      string
	Declarations []ExecutionDeclaration
	Intrinsics   []ExecutionIntrinsic
	Priors       []ExecutionPriorSite
}
type ExecutionDeclaration struct {
	Package, Name, Signature string
	Position                 diag.Pos
}
type ExecutionIntrinsic struct {
	Declaration    ExecutionDeclaration
	Contract       string
	Implementation [sha256.Size]byte
}
type ExecutionPriorSite struct {
	Position diag.Pos
	Type     string
}

type executionAuditor struct {
	info         *Info
	report       ExecutionAudit
	nodes        int
	functions    map[*Func]bool
	types        map[Type]bool
	activeTypes  map[Type]bool
	expressions  map[Expr]bool
	dictionaries map[*Dict]bool
	priors       map[*Comptime]bool
}

func newExecutionAuditor(info *Info) *executionAuditor {
	return &executionAuditor{info: info, functions: map[*Func]bool{}, types: map[Type]bool{}, activeTypes: map[Type]bool{}, expressions: map[Expr]bool{}, dictionaries: map[*Dict]bool{}, priors: map[*Comptime]bool{}}
}
func (a *executionAuditor) reject(reason string) {
	if a.report.Decline == "" {
		a.report.Decline = reason
	}
}
func (a *executionAuditor) step(depth int) bool {
	if a.report.Decline != "" {
		return false
	}
	a.nodes++
	if a.nodes > executionAuditNodeLimit || depth > executionAuditDepthLimit {
		a.reject("execution audit budget exceeded")
		return false
	}
	return true
}

// AuditExecutionQueries inventories every actual query, including And/Or leaves
// and typed argument/setup expressions. Query.Text and Func.Calls are not closure
// evidence. Unknown nodes or targets produce an ordinary eligibility decline.
func AuditExecutionQueries(info *Info, queries []Query) ExecutionAudit {
	a := newExecutionAuditor(info)
	if info == nil || len(queries) == 0 {
		a.reject("missing execution selection")
		return a.report
	}
	for _, query := range queries {
		a.query(query, 0)
	}
	return a.report
}
func AuditComptimeExecution(info *Info, node *Comptime) ExecutionAudit {
	a := newExecutionAuditor(info)
	if info == nil || node == nil || node.Body == nil {
		a.reject("missing execution selection")
		return a.report
	}
	a.typ(node.Type(), 0)
	for _, capture := range node.Captures {
		if capture == nil || capture.Let == nil || capture.PackageBinding != nil || capture.Let.Deferred != EagerBinding {
			a.reject("unresolved or deferred capture")
			break
		}
		a.closed(capture.Let.Value, 0)
	}
	a.expr(node.Body, 0, false)
	return a.report
}
func (a *executionAuditor) query(q Query, depth int) {
	if !a.step(depth) {
		return
	}
	if q.Or != nil || q.And != nil {
		if q.Pred != nil || (q.Or != nil && q.And != nil) || q.Subject != nil || len(q.Values) > 0 || len(q.Args) > 0 || len(q.Dicts) > 0 {
			a.reject("ambiguous query shape")
			return
		}
		parts := q.Or
		if q.And != nil {
			parts = q.And
		}
		if len(parts) == 0 {
			a.reject("empty query combination")
			return
		}
		for _, part := range parts {
			a.query(part, depth+1)
		}
		return
	}
	if q.Pred == nil || q.Pred.Decl == nil || !q.Pred.Decl.IsPred || q.Pred.Result != Bool || len(q.TypeArgs) > 0 {
		a.reject("unresolved query target or generic selection")
		return
	}
	params := q.Params
	if params == nil {
		params = q.Pred.Params
	}
	count := len(q.Values) + len(q.Args)
	if q.Subject != nil {
		count++
	}
	if count != len(params) {
		a.reject("incomplete query arguments")
		return
	}
	for _, typ := range params {
		a.typ(typ, depth+1)
	}
	for _, value := range q.Values {
		a.closed(value, depth+1)
	}
	if q.Subject != nil {
		a.closed(q.Subject, depth+1)
	}
	for _, value := range q.Args {
		if value == nil || value.Kind() == constant.Unknown {
			a.reject("unknown query constant")
		}
	}
	for _, dict := range q.Dicts {
		a.dict(dict, depth+1)
	}
	for _, facts := range q.ArgFacts {
		a.constraints(facts, depth+1)
	}
	a.function(q.Pred, depth+1)
}

// typeText bounds expanded display work, including repeated DAG edges, before
// invoking the shared formatter. Static reports are not canonical type receipts.
func (a *executionAuditor) typeText(typ Type) string {
	budget := 1 << 20
	var visit func(Type, int)
	visit = func(t Type, depth int) {
		if !a.step(depth) {
			return
		}
		budget -= 64
		var children []Type
		switch t := t.(type) {
		case *Basic:
			budget -= len(t.String())
		case *List:
			children = []Type{t.Elem}
		case *Map:
			children = []Type{t.Key, t.Value}
		case *Record:
			budget -= len(t.Name)
			children = t.Args
		case *Sealed:
			budget -= len(t.Name)
			children = t.Args
		case *Union:
			children = t.Members
		case *FuncType:
			for _, p := range t.Params {
				visit(p, depth+1)
			}
			children = []Type{t.Result}
		default:
			a.reject("unresolved display type")
			return
		}
		if budget < 0 {
			a.reject("execution type rendering budget exceeded")
			return
		}
		for _, child := range children {
			visit(child, depth+1)
		}
	}
	visit(typ, 0)
	if a.report.Decline != "" {
		return ""
	}
	return TypeText(typ, nil)
}

func (a *executionAuditor) declaration(fn *Func) ExecutionDeclaration {
	pkg := ""
	if fn.Pkg != nil {
		pkg = fn.Pkg.Path
	}
	params := make([]string, len(fn.Params))
	for i, typ := range fn.Params {
		params[i] = a.typeText(typ)
	}
	return ExecutionDeclaration{Package: pkg, Name: fn.Decl.Name, Signature: "(" + strings.Join(params, ",") + ")=>" + a.typeText(fn.Result), Position: fn.Decl.Pos}
}
func (a *executionAuditor) function(fn *Func, depth int) {
	if !a.step(depth) {
		return
	}
	if fn == nil || fn.Decl == nil || a.info.FuncOf[fn.Decl] != fn {
		a.reject("unresolved declaration identity")
		return
	}
	if a.functions[fn] {
		return
	}
	a.functions[fn] = true
	if fn.Effects != 0 || len(fn.Needs) > 0 || fn.RuntimePackageReads {
		a.reject("runtime effect, ambient need or package memo")
		return
	}
	if len(fn.TypeParams) > 0 {
		a.reject("generic implementation not yet audited")
		return
	}
	if fn.Class != nil || fn.Derived != nil || fn.Synthetic || fn.MockOf != nil || fn.Test != nil {
		a.reject("unresolved or generated implementation")
		return
	}
	for _, typ := range fn.Params {
		if _, ok := typ.(*FuncType); ok {
			a.reject("callback parameter not yet resolved")
			return
		}
		a.typ(typ, depth+1)
	}
	a.typ(fn.Result, depth+1)
	if a.report.Decline != "" {
		return
	}
	declaration := a.declaration(fn)
	a.report.Declarations = append(a.report.Declarations, declaration)
	for _, facts := range fn.ParamConstraints {
		a.constraints(facts, depth+1)
	}
	for _, facts := range fn.ResultConstraints {
		a.typ(facts.Type, depth+1)
		a.constraints(facts.Constraints, depth+1)
	}
	a.expr(fn.Requires, depth+1, true)
	if fn.Decl.IsGo() {
		if !a.byteLength(fn) {
			a.reject("unaudited unsafe implementation")
			return
		}
		a.report.Intrinsics = append(a.report.Intrinsics, ExecutionIntrinsic{Declaration: declaration, Contract: "string-byte-length-v1", Implementation: sha256.Sum256([]byte(fn.Decl.GoBody.Body))})
		return
	}
	if fn.Body == nil {
		a.reject("missing implementation body")
		return
	}
	a.expr(fn.Body, depth+1, false)
}

// Resolve exactly the checked prelude declaration and pin its implementation.
// A user function with the same name or Go len text is never an intrinsic.
func (a *executionAuditor) byteLength(fn *Func) bool {
	return fn.Prelude && fn.Pkg != nil && fn.Pkg.Path == "" && fn.Decl.Name == "byteLength" && fn.Decl.IsMethod && len(fn.Params) == 1 && fn.Params[0] == String && fn.Result == Int && fn.Decl.GoBind == nil && fn.Decl.GoBody != nil && len(fn.Decl.GoBody.Imports) == 0 && len(fn.Decl.GoBody.ImportAliases) == 0 && len(fn.Calls) == 0 && len(fn.Decl.Params) == 1 && fn.Decl.Params[0].Name == "s" && strings.TrimSpace(fn.Decl.GoBody.Body) == "return int64(len(s))"
}
func (a *executionAuditor) instance(inst *Instance, depth int) {
	if !a.step(depth) {
		return
	}
	if inst == nil || inst.Func == nil || len(inst.TypeArgs) > 0 {
		a.reject("unresolved or generic invocation")
		return
	}
	for _, typ := range inst.Params {
		a.typ(typ, depth+1)
	}
	a.typ(inst.Result, depth+1)
	a.function(inst.Func, depth+1)
	if a.report.Decline != "" {
		return
	}
	if len(inst.Params) != len(inst.Func.Params) || !identical(inst.Result, inst.Func.Result) {
		a.reject("mismatched invocation signature")
		return
	}
	for i, typ := range inst.Params {
		if !identical(typ, inst.Func.Params[i]) {
			a.reject("mismatched invocation signature")
			return
		}
	}
	for _, dict := range inst.Dicts {
		a.dict(dict, depth+1)
	}
	for _, facts := range inst.ArgFacts {
		a.constraints(facts, depth+1)
	}
	a.function(inst.Func, depth+1)
}
func (a *executionAuditor) dict(dict *Dict, depth int) {
	if !a.step(depth) {
		return
	}
	if dict == nil || dict.Class == nil || dict.Param != nil || !slices.Contains(a.info.Classes, dict.Class) {
		a.reject("unresolved dictionary")
		return
	}
	if a.dictionaries[dict] {
		return
	}
	a.dictionaries[dict] = true
	a.typ(dict.Type, depth+1)
	if dict.Builtin {
		if !IsEq(dict.Class) || dict.Inst != nil || len(dict.Args) > 0 {
			a.reject("unaudited builtin dictionary")
		}
		return
	}
	if dict.Inst == nil || !slices.Contains(a.info.ClassInstances, dict.Inst) || dict.Inst.Class != dict.Class || len(dict.TypeArgs) > 0 || len(dict.Inst.TypeParams) > 0 {
		a.reject("unresolved or generic dictionary")
		return
	}
	for _, arg := range dict.Args {
		a.dict(arg, depth+1)
	}
	a.constraints(dict.Inst.Constraints, depth+1)
	// These are slices in declared method/field order, not maps.
	for _, method := range dict.Inst.Methods {
		a.function(method, depth+1)
	}
	for _, decoder := range dict.Inst.GoFieldDecoders {
		a.dict(decoder, depth+1)
	}

}
func (a *executionAuditor) constraints(constraints []*Constraint, depth int) {
	for _, constraint := range constraints {
		if !a.step(depth) {
			return
		}
		if constraint == nil || constraint.PredParam != "" {
			a.reject("unresolved predicate constraint")
			return
		}
		if constraint.Or != nil {
			a.constraints(constraint.Or, depth+1)
			continue
		}
		a.function(constraint.Pred, depth+1)
		for _, arg := range constraint.Args {
			if arg.Type != nil {
				a.typ(arg.Type, depth+1)
			}
		}
	}
}
func (a *executionAuditor) typ(typ Type, depth int) {
	if !a.step(depth) {
		return
	}
	if typ == nil || (reflect.ValueOf(typ).Kind() == reflect.Pointer && reflect.ValueOf(typ).IsNil()) || !reflect.TypeOf(typ).Comparable() {
		a.reject("unresolved execution type")
		return
	}
	if a.activeTypes[typ] {
		switch typ.(type) {
		case *Record, *Sealed:
		default:
			a.reject("cyclic structural execution type")
		}
		return
	}
	if a.types[typ] {
		return
	}
	a.activeTypes[typ] = true
	defer delete(a.activeTypes, typ)
	a.types[typ] = true
	switch typ := typ.(type) {
	case *Basic:
		if typ != Bool && typ != String && typ != Rune && typ != Ok && typ != Never && !IsNumeric(typ) {
			a.reject("runtime or unknown basic type")
		}
	case *List:
		a.typ(typ.Elem, depth+1)
	case *Map:
		a.typ(typ.Key, depth+1)
		a.typ(typ.Value, depth+1)
	case *Record:
		if typ.Base == nil && len(typ.TypeParams) > 0 {
			a.reject("unresolved generic record")
			return
		}
		for _, arg := range typ.Args {
			a.typ(arg, depth+1)
		}
		if typ.GoMirror != nil || typ.GoGenerated || typ.MockCall {
			a.reject("foreign or generated record")
			return
		}
		a.fields(typ.Fields, depth+1)
		a.constraints(typ.Constraints, depth+1)
	case *Sealed:
		if typ.Base == nil && len(typ.TypeParams) > 0 {
			a.reject("unresolved generic sealed type")
			return
		}
		for _, arg := range typ.Args {
			a.typ(arg, depth+1)
		}
		a.constraints(typ.Constraints, depth+1)
		for _, variant := range typ.Variants {
			if variant == nil {
				a.reject("unknown variant")
				return
			}
			a.fields(variant.Fields, depth+1)
			a.constraints(variant.Constraints, depth+1)
		}
	case *Union:
		for _, member := range typ.Members {
			a.typ(member, depth+1)
		}
	case *FuncType:
		if typ.Effects != 0 {
			a.reject("effectful function value")
			return
		}
		for _, param := range typ.Params {
			a.typ(param, depth+1)
		}
		a.typ(typ.Result, depth+1)
	default:
		a.reject("unknown or runtime execution type")
	}
}
func (a *executionAuditor) fields(fields []*Field, depth int) {
	for _, field := range fields {
		if !a.step(depth) {
			return
		}
		if field == nil || field.Lazy || field.RuntimePackageReads {
			a.reject("deferred or unresolved field")
			return
		}
		a.typ(field.Type, depth+1)
		a.constraints(field.Constraints, depth+1)
		a.expr(field.Default, depth+1, true)
		for _, dependency := range field.Dependencies {
			if dependency == nil {
				a.reject("unresolved default dependency")
				return
			}
			a.typ(dependency.Type, depth+1)
			a.expr(dependency.Default, depth+1, true)
		}
		for _, fn := range field.DefaultCalls {
			a.function(fn, depth+1)
		}
	}
}

func nilExecutionExpr(x Expr) bool {
	return x == nil || (reflect.ValueOf(x).Kind() == reflect.Pointer && reflect.ValueOf(x).IsNil())
}
func (a *executionAuditor) closed(x Expr, depth int) {
	if !a.step(depth) {
		return
	}
	if nilExecutionExpr(x) {
		a.reject("missing closed argument")
		return
	}
	switch x := x.(type) {
	case *Const, *FloatBits, *VariantValue:
	case *Comptime:
		if x.Value == nil {
			a.reject("unvalidated prior comptime")
			return
		}
		a.closed(x.Value, depth+1)
	case *ListLit:
		for _, item := range x.Elems {
			a.closed(item, depth+1)
		}
	case *MapLit:
		for _, item := range x.Keys {
			a.closed(item, depth+1)
		}
		for _, item := range x.Values {
			a.closed(item, depth+1)
		}
	case *RecordLit:
		for _, field := range x.Fields {
			if field == nil || field.Thunk != nil || field.Lazy != nil {
				a.reject("deferred closed field")
				return
			}
			a.closed(field.Value, depth+1)
		}
	case *Block:
		if len(x.Stmts) > 0 || x.Tail != nil || x.Type() != Ok {
			a.reject("nonliteral closed block")
			return
		}
	case *VarRef:
		if x.Var == nil || x.Var.PackageBinding != nil || x.Var.Let == nil || x.Var.Let.Deferred != EagerBinding {
			a.reject("runtime closed argument")
			return
		}
		// A reference cycle is not a closed value. Apply the depth budget rather
		// than the expression visited set used for ordinary local references.
		a.closed(x.Var.Let.Value, depth+1)
	default:
		a.reject("argument is not canonical closed data")
		return
	}
	a.expr(x, depth+1, false)
}
func (a *executionAuditor) expr(x Expr, depth int, optional bool) {
	if !a.step(depth) {
		return
	}
	if nilExecutionExpr(x) {
		if !optional {
			a.reject("missing typed expression")
		}
		return
	}
	if !reflect.TypeOf(x).Comparable() {
		a.reject("unknown non-comparable typed expression")
		return
	}
	if a.expressions[x] {
		return
	}
	a.expressions[x] = true
	a.typ(x.Type(), depth+1)
	walk := func(child Expr) { a.expr(child, depth+1, false) }
	maybe := func(child Expr) { a.expr(child, depth+1, true) }
	many := func(children []Expr) {
		for _, child := range children {
			walk(child)
		}
	}
	switch x := x.(type) {
	case *Const:
		if x.Value == nil || x.Value.Kind() == constant.Unknown {
			a.reject("unknown constant")
		}
	case *FloatBits:
	case *Interp:
		for _, item := range x.Exprs {
			if nilExecutionExpr(item) {
				a.reject("missing interpolation expression")
				return
			}
			switch item.Type().(type) {
			case *Basic:
			default:
				a.reject("unaudited interpolation rendering")
			}
			walk(item)
		}
	case *VarRef:
		if x.Var == nil || x.Var.PackageBinding != nil || x.Var.Ambient != nil {
			a.reject("runtime or unresolved variable")
			return
		}
		switch x.Var.Kind {
		case VarParam, VarLambdaParam, VarPattern, VarLoop:
		case VarLet:
			if x.Var.Let == nil {
				a.reject("missing variable binding")
				return
			}
		case VarDefaultField:
			if x.Var.Sibling == nil {
				a.reject("missing default sibling")
				return
			}
			a.fields([]*Field{x.Var.Sibling}, depth+1)
		default:
			a.reject("runtime or unknown variable kind")
			return
		}
		if x.Var.Let != nil {
			a.let(x.Var.Let, depth+1)
		}
		if x.Var.Source != nil {
			walk(x.Var.Source.Subject)
		}
	case *FuncRef:
		if len(x.Needs) > 0 {
			a.reject("ambient function reference")
			return
		}
		a.instance(x.Inst, depth+1)
	case *Unary:
		if x.Op != syntax.Minus && x.Op != syntax.Not {
			a.reject("unknown unary operator")
			return
		}
		walk(x.X)
	case *Binary:
		switch x.Op {
		case syntax.Plus, syntax.Minus, syntax.Star, syntax.Slash, syntax.Pct, syntax.AndAnd, syntax.OrOr, syntax.Eq, syntax.NotEq, syntax.Lt, syntax.LtEq, syntax.Gt, syntax.GtEq:
		default:
			a.reject("unknown binary operator")
			return
		}
		walk(x.X)
		walk(x.Y)
	case *Call:
		if x.BuildRead != nil || x.Embedded != nil || len(x.Needs) > 0 {
			a.reject("build, asset or ambient input")
			return
		}
		if x.Inst == nil || x.Inst.Func != x.Func {
			a.reject("unresolved call target")
			return
		}
		many(x.Args)
		a.instance(x.Inst, depth+1)
	case *CallBuiltin:
		switch x.Builtin {
		case BuiltinPanic, BuiltinConvert, BuiltinCallerLocation:
		default:
			a.reject("unaudited compiler builtin")
			return
		}
		many(x.Args)
	case *CallValue:
		walk(x.Fun)
		if a.report.Decline != "" {
			return
		}
		many(x.Args)
		target := x.Fun
		for budget := 0; budget < executionAuditDepthLimit; budget++ {
			ref, ok := target.(*VarRef)
			if !ok {
				break
			}
			if ref.Var == nil || ref.Var.Let == nil || ref.Var.PackageBinding != nil || ref.Var.Let.Deferred != EagerBinding {
				a.reject("unresolved callback target")
				return
			}
			target = ref.Var.Let.Value
		}
		switch target.(type) {
		case *FuncRef, *Lambda:
			walk(target)
		default:
			a.reject("unresolved callback target")
		}
	case *Lambda:
		for _, param := range x.Params {
			if param == nil {
				a.reject("unknown lambda parameter")
				return
			}
			a.typ(param.Type, depth+1)
		}
		walk(x.Body)
	case *ListLit:
		many(x.Elems)
	case *MapLit:
		if len(x.Keys) != len(x.Values) {
			a.reject("malformed map")
		}
		many(x.Keys)
		many(x.Values)
	case *If:
		walk(x.Cond)
		walk(x.Then)
		maybe(x.Else)
	case *Comptime:
		if x.Value == nil {
			a.reject("unvalidated prior comptime")
			return
		}
		if !a.priors[x] {
			a.priors[x] = true
			a.report.Priors = append(a.report.Priors, ExecutionPriorSite{Position: x.Pos(), Type: a.typeText(x.Type())})
		}
		a.closed(x.Value, depth+1)
	case *Block:
		if x.Assembly != nil || len(x.Labels) > 0 {
			a.reject("unaudited provider assembly or labels")
			return
		}
		for _, stmt := range x.Stmts {
			a.stmt(stmt, depth+1)
		}
		maybe(x.Tail)
	case *ScopeBlock:
		a.reject("runtime scope")
	case *Return:
		maybe(x.Value)
	case *Select:
		if x.Field == nil || x.Field.Lazy || x.Field.RuntimePackageReads {
			a.reject("deferred or unresolved selection")
			return
		}
		walk(x.X)
		a.fields([]*Field{x.Field}, depth+1)
	case *VariantValue:
		if x.Variant == nil {
			a.reject("unknown variant value")
			return
		}
		maybe(x.ProofSource)
		a.constraints(x.Constraints, depth+1)
	case *RecordLit:
		if x.Record == nil && x.Variant == nil {
			a.reject("unresolved constructor")
			return
		}
		for _, field := range x.Fields {
			if field == nil || field.Thunk != nil || field.Lazy != nil {
				a.reject("deferred field construction")
				return
			}
			walk(field.Value)
			a.fields([]*Field{field.Field}, depth+1)
		}
		a.constraints(x.Constraints, depth+1)
	case *Copy:
		walk(x.X)
		for _, update := range x.Updates {
			if update == nil || update.Thunk != nil || update.Lazy != nil {
				a.reject("deferred copy update")
				return
			}
			walk(update.Value)
			a.fields([]*Field{update.Field}, depth+1)
		}
	case *Match:
		walk(x.X)
		for _, arm := range x.Arms {
			if arm == nil {
				a.reject("unknown match arm")
				return
			}
			a.pattern(arm.Pat, depth+1)
			walk(arm.Body)
		}
	case *Try:
		walk(x.X)
	case *Generate, *Yield, *SeqCall:
		a.reject("unaudited producer execution")
	case *For:
		walk(x.Items)
		walk(x.Body)
	case *LoopControl:
	default:
		a.reject(fmt.Sprintf("unknown typed expression %T", x))
	}
}
func (a *executionAuditor) let(binding *Let, depth int) {
	if !a.step(depth) {
		return
	}
	if binding == nil || binding.Deferred != EagerBinding || binding.Initializer != nil || binding.AsyncScope != nil || binding.Async != nil || binding.Lazy != nil {
		a.reject("deferred or unknown binding")
		return
	}
	a.expr(binding.Value, depth+1, false)
	a.constraints(binding.Constraints, depth+1)
}
func (a *executionAuditor) stmt(stmt Stmt, depth int) {
	if !a.step(depth) {
		return
	}
	switch stmt := stmt.(type) {
	case *Let:
		a.let(stmt, depth+1)
	case *ExprStmt:
		if stmt == nil {
			a.reject("missing expression statement")
			return
		}
		a.expr(stmt.X, depth+1, false)
	case *Trust:
		a.reject("unaudited trust statement")
	case *Mock:
		a.reject("test mock overlay")
	default:
		a.reject(fmt.Sprintf("unknown typed statement %T", stmt))
	}
}
func (a *executionAuditor) pattern(pattern *Pat, depth int) {
	if !a.step(depth) {
		return
	}
	if pattern == nil {
		a.reject("missing pattern")
		return
	}
	switch pattern.Kind {
	case PatWild, PatLit, PatVariant, PatRecord, PatType, PatList:
	default:
		a.reject("unknown pattern kind")
		return
	}
	a.typ(pattern.Type, depth+1)
	if pattern.BindType != nil {
		a.typ(pattern.BindType, depth+1)
	}
	for _, typ := range pattern.Members {
		a.typ(typ, depth+1)
	}
	for _, field := range pattern.Fields {
		if field == nil {
			a.reject("missing pattern field")
			return
		}
		a.pattern(field.Pat, depth+1)
	}
	for _, elem := range pattern.Elems {
		a.pattern(elem, depth+1)
	}
	if pattern.Sub != nil {
		a.pattern(pattern.Sub, depth+1)
	}
	if pattern.Rest != nil {
		a.pattern(pattern.Rest, depth+1)
	}
	a.expr(pattern.Guard, depth+1, true)
}
