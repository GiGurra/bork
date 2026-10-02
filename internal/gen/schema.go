package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
)

// decodeSchema exposes derived record fields without changing Decode's bork methods.
func (g *gen) decodeSchema(ci *check.ClassInstance, record *check.Record) string {
	var b strings.Builder
	b.WriteString("func() []_borkDecodeField { return []_borkDecodeField{\n")
	for i, field := range record.Fields {
		dict := g.text(g.dict(ci.Methods[0].Derived.FieldDicts[0][i]))
		var constraints []string
		for _, con := range field.Constraints {
			constraints = append(constraints, strconv.Quote(con.String()))
		}
		fmt.Fprintf(&b, "{Name: %q, Type: %q, Constraints: []string{%s}, Kind: (%s).kind, Optional: (%s).optional, Decode: func(value Json) any {\n", field.Name, field.Type.String(), strings.Join(constraints, ", "), dict, dict)
		b.WriteString("_result := func() any {\n_obj := Json_Object{fields: []JsonField{{name: " + fmt.Sprintf("%q", field.Name) + ", value: value}}}\n")
		b.WriteString(g.decodeFields([]*check.Field{field}, []*check.Dict{ci.Methods[0].Derived.FieldDicts[0][i]}, g.typeText(record)))
		fmt.Fprintf(&b, "}()\nif err, ok := _result.(DecodeError); ok { return err }\nreturn _result.(%s).%s\n}},\n", g.typeText(record), name(field.Name).Name)
	}
	b.WriteString("} }")
	return b.String()
}

// decodeKind uses the actual decoder dictionary for generic field types.
func (g *gen) decodeKind(typ check.Type) string {
	if check.IsOption(typ) {
		return g.decodeKind(check.TypeArgs(typ)[0])
	}
	if param, ok := typ.(*check.TypeParam); ok {
		for _, bound := range param.Bounds {
			if bound.Prelude && bound.Name == "Decode" {
				return dictParam(param, bound).Name + ".kind"
			}
		}
		return strconv.Quote("json")
	}
	switch typ {
	case check.String:
		return strconv.Quote("string")
	case check.Int, check.Int8, check.Int16, check.Int32, check.Uint8, check.Uint16, check.Uint32, check.Uint64, check.Float32, check.Float:
		return strconv.Quote("number")
	case check.Bool:
		return strconv.Quote("bool")
	}
	return strconv.Quote("json")
}
