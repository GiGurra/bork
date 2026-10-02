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
		kind := "json"
		typ := field.Type
		if check.IsOption(typ) {
			typ = check.TypeArgs(typ)[0]
		}
		switch typ {
		case check.String:
			kind = "string"
		case check.Int, check.Int8, check.Int16, check.Int32, check.Uint8, check.Uint16, check.Uint32, check.Uint64, check.Float32, check.Float:
			kind = "number"
		case check.Bool:
			kind = "bool"
		}
		var constraints []string
		for _, con := range field.Constraints {
			constraints = append(constraints, strconv.Quote(con.String()))
		}
		fmt.Fprintf(&b, "{Name: %q, Type: %q, Constraints: []string{%s}, Kind: %q, Optional: %t, Decode: func(value Json) any {\n", field.Name, field.Type.String(), strings.Join(constraints, ", "), kind, check.IsOption(field.Type))
		b.WriteString("_result := func() any {\n_obj := Json_Object{fields: []JsonField{{name: " + fmt.Sprintf("%q", field.Name) + ", value: value}}}\n")
		b.WriteString(g.decodeFields([]*check.Field{field}, []*check.Dict{ci.Methods[0].Derived.FieldDicts[0][i]}, g.typeText(record)))
		fmt.Fprintf(&b, "}()\nif err, ok := _result.(DecodeError); ok { return err }\nreturn _result.(%s).%s\n}},\n", g.typeText(record), name(field.Name).Name)
	}
	b.WriteString("} }")
	return b.String()
}
