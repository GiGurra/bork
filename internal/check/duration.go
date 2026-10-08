package check

import (
	"go/constant"
	"go/token"
)

// Literal unit constructors are closed values, so timeout facts can be checked
// without asking callers to bind and guard a duration made from a literal.
func durationLiteral(call *Call) *RecordLit {
	if !call.Func.Prelude || call.Func.Decl == nil || !call.Func.Decl.IsMethod || len(call.Args) != 1 || call.Args[0].Type() != Int {
		return nil
	}
	scales := map[string]int64{"nanos": 1, "micros": 1000, "millis": 1000000, "seconds": 1000000000, "minutes": 60000000000, "hours": 3600000000000}
	scale, ok := scales[call.Func.Decl.Name]
	if !ok {
		return nil
	}
	rec, ok := call.Type().(*Record)
	if !ok || !rec.Prelude || rec.Name != "Duration" {
		return nil
	}
	n := constOf(call.Args[0])
	if n == nil || n.Kind() != constant.Int {
		return nil
	}
	n = constant.BinaryOp(n, token.MUL, constant.MakeInt64(scale))
	lo, hi := constant.MakeInt64(-1<<63), constant.MakeInt64(1<<63-1)
	if constant.Compare(n, token.LSS, lo) {
		n = lo
	}
	if constant.Compare(n, token.GTR, hi) {
		n = hi
	}
	field := rec.Field("nanos")
	return &RecordLit{expr: call.expr, Record: rec, Fields: []*FieldValue{{Name: "nanos", Field: field, Value: &Const{expr: expr{pos: call.Pos(), typ: Int}, Value: n}}}}
}
