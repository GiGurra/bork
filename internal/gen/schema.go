package gen

import (
	"go/ast"
	"strconv"

	"github.com/GiGurra/bork/internal/check"
)

// decodeKind uses the actual decoder dictionary for generic field types.
func (g *gen) decodeKind(typ check.Type) string {
	if list, ok := typ.(*check.List); ok {
		return strconv.Quote("list:") + " + " + g.decodeKind(list.Elem)
	}
	if check.IsOption(typ) {
		return g.decodeKind(check.TypeArgs(typ)[0])
	}
	if param, ok := typ.(*check.TypeParam); ok {
		for _, bound := range param.Bounds {
			if check.IsCodec(bound, "Decode") {
				return dictParam(param, bound).Name + ".kind"
			}
		}
		return strconv.Quote("json")
	}
	switch typ {
	case check.String, check.Rune:
		return strconv.Quote("string")
	case check.Int, check.Int8, check.Int16, check.Int32, check.Uint8, check.Uint16, check.Uint32, check.Uint64, check.Float32, check.Float:
		return strconv.Quote("number")
	case check.Bool:
		return strconv.Quote("bool")
	}
	return strconv.Quote("json")
}

func (g *gen) fieldDefault(field *check.Field) string {
	setup, value := g.value(field.Default)
	value = g.typed(value, field.Type)
	if len(setup) == 0 {
		return g.text(value)
	}
	setup = append(setup, &ast.ReturnStmt{Results: []ast.Expr{value}})
	return g.text(&ast.CallExpr{Fun: &ast.FuncLit{Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: g.goType(field.Type)}}}}, Body: &ast.BlockStmt{List: setup}}})
}

// A schema field decoder has only one field; relational checks belong to the
// complete record decoder, once its sibling values have been decoded.
func independentField(f *check.Field) *check.Field {
	cp := *f
	cp.Constraints = nil
	for _, con := range f.Constraints {
		if !con.HasSiblingArgs() {
			cp.Constraints = append(cp.Constraints, con)
		}
	}
	return &cp
}
