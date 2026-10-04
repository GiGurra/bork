package check

import (
	"go/constant"

	"github.com/GiGurra/bork/internal/syntax"
)

// Known guards use source values, never predicate execution or memo recipes.
func (f *factChecker) knownCondition(x Expr) (bool, bool) {
	value := f.branchConstant(x, 0)
	if value == nil || value.Kind() != constant.Bool {
		return false, false
	}
	return constant.BoolVal(value), true
}

func (f *factChecker) branchConstant(x Expr, depth int) constant.Value {
	if x == nil || depth > maxDepth {
		return nil
	}
	x = debugValue(x)
	switch x := x.(type) {
	case *Const:
		return conditionConstant(x.Value, x.Type())
	case *VarRef:
		if x.Var.Kind == VarLet && x.Var.Let != nil && x.Var.Let.Initializer == nil {
			return conditionConstant(f.branchConstant(x.Var.Let.Value, depth+1), x.Type())
		}
	case *Select:
		if x.Field != nil && x.Field.Lazy {
			return nil
		}
		return conditionConstant(f.branchConstant(f.branchProjection(x.X, x.Name, depth+1), depth+1), x.Type())
	case *Block:
		if len(x.Stmts) == 0 {
			return f.branchConstant(x.Tail, depth+1)
		}
	case *Unary:
		value := f.branchConstant(x.X, depth+1)
		if value == nil {
			return nil
		}
		copy := *x
		copy.X = &Const{expr: expr{typ: x.X.Type()}, Value: value}
		return evalCondition(&copy, nil)
	case *Binary:
		left, right := f.branchConstant(x.X, depth+1), f.branchConstant(x.Y, depth+1)
		if x.Op == syntax.AndAnd || x.Op == syntax.OrOr {
			// Either decisive operand determines the value if evaluation completes.
			decisive := x.Op == syntax.OrOr
			for _, value := range []constant.Value{left, right} {
				if value != nil && value.Kind() == constant.Bool && constant.BoolVal(value) == decisive {
					return constant.MakeBool(decisive)
				}
			}
		}
		if left == nil || right == nil {
			return nil
		}
		copy := *x
		copy.X = &Const{expr: expr{typ: x.X.Type()}, Value: left}
		copy.Y = &Const{expr: expr{typ: x.Y.Type()}, Value: right}
		return evalCondition(&copy, nil)
	}
	return nil
}

// Project independent literal/copy inputs without traversing deferred storage.
func (f *factChecker) branchProjection(x Expr, name string, depth int) Expr {
	if x == nil || depth > maxDepth {
		return nil
	}
	x = debugValue(x)
	switch x := x.(type) {
	case *VarRef:
		if x.Var.Kind == VarLet && x.Var.Let != nil && x.Var.Let.Initializer == nil {
			return f.branchProjection(x.Var.Let.Value, name, depth+1)
		}
	case *RecordLit:
		for _, field := range x.Fields {
			if field.Name == name && !field.Field.Lazy {
				return field.Value
			}
		}
	case *Select:
		if x.Field != nil && x.Field.Lazy {
			return nil
		}
		return f.branchProjection(f.branchProjection(x.X, x.Name, depth+1), name, depth+1)
	case *Copy:
		var updates []*FieldUpdate
		for _, update := range x.Updates {
			if update.Path[0] != name {
				continue
			}
			if len(update.Path) == 1 {
				if !update.Field.Lazy {
					return update.Value
				}
				return nil
			}
			copy := *update
			copy.Path = copy.Path[1:]
			updates = append(updates, &copy)
		}
		original := f.branchProjection(x.X, name, depth+1)
		if len(updates) == 0 || original == nil {
			return original
		}
		return &Copy{expr: expr{typ: original.Type()}, X: original, Updates: updates}
	case *Block:
		if len(x.Stmts) == 0 {
			return f.branchProjection(x.Tail, name, depth+1)
		}
	}
	return nil
}

func (f *factChecker) statementCompletes(statement Stmt) bool {
	switch statement := statement.(type) {
	case *ExprStmt:
		return f.completes(statement.X)
	case *Let:
		return (statement.AsyncScope == nil || f.completes(statement.AsyncScope)) && (statement.Initializer != nil || f.completes(statement.Value))
	}
	return true
}
func (f *factChecker) statementsComplete(statements []Stmt) bool {
	for _, statement := range statements {
		if !f.statementCompletes(statement) {
			return false
		}
	}
	return true
}
