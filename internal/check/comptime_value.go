package check

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"go/constant"
	"io"
	"strconv"

	"github.com/GiGurra/bork/internal/diag"
)

type comptimeValue struct {
	Kind  string
	Text  string
	Items []comptimeValue
	Nil   bool
}

// DecodeComptime validates compiler transport against the concrete checked type.
func DecodeComptime(node *Comptime, data []byte) (Expr, error) {
	if len(data) > 16<<20 {
		return nil, fmt.Errorf("result exceeds 16 MiB")
	}
	var envelope struct {
		Version int
		Value   comptimeValue
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing result data")
	}
	if envelope.Version != 1 {
		return nil, fmt.Errorf("unsupported result schema %d", envelope.Version)
	}
	budget := 1000000
	return decodeComptimeValue(envelope.Value, node.Type(), node.Pos(), 0, &budget)
}

func decodeComptimeValue(v comptimeValue, t Type, pos diag.Pos, depth int, budget *int) (Expr, error) {
	*budget--
	if depth > 256 || *budget < 0 {
		return nil, fmt.Errorf("result exceeds depth or node limit")
	}
	if v.Kind != TypeText(t, nil) {
		return nil, fmt.Errorf("expected result type %s, got %q", t, v.Kind)
	}
	at := expr{pos: pos, typ: t}
	decode := func(v comptimeValue, t Type) (Expr, error) { return decodeComptimeValue(v, t, pos, depth+1, budget) }
	switch t := t.(type) {
	case *Basic:
		if len(v.Items) != 0 || v.Nil {
			return nil, fmt.Errorf("malformed scalar %s", t)
		}
		var value constant.Value
		switch {
		case t == Ok:
			if v.Text != "" {
				return nil, fmt.Errorf("malformed Ok value")
			}
			// Ok is represented by the ordinary no-field value at generation time.
			return &Block{expr: at}, nil
		case t == String:
			data, err := base64.StdEncoding.DecodeString(v.Text)
			if err != nil {
				return nil, err
			}
			value = constant.MakeString(string(data))
		case t == Bool:
			b, err := strconv.ParseBool(v.Text)
			if err != nil {
				return nil, err
			}
			value = constant.MakeBool(b)
		case IsFloat(t):
			bits, err := strconv.ParseUint(v.Text, 16, 64)
			if err != nil {
				return nil, err
			}
			if t == Float32 && bits > 0xffffffff {
				return nil, fmt.Errorf("Float32 bits out of range")
			}
			return &FloatBits{expr: at, Bits: bits}, nil
		case IsNumeric(t):
			if numKindOf(t) == unsignedInt {
				n, err := strconv.ParseUint(v.Text, 10, t.bits)
				if err != nil {
					return nil, err
				}
				value = constant.MakeUint64(n)
			} else {
				n, err := strconv.ParseInt(v.Text, 10, t.bits)
				if err != nil {
					return nil, err
				}
				value = constant.MakeInt64(n)
			}
		default:
			return nil, fmt.Errorf("unsupported scalar %s", t)
		}
		return &Const{expr: at, Value: value}, nil
	case *List:
		if v.Text != "" || v.Nil && len(v.Items) != 0 {
			return nil, fmt.Errorf("malformed list")
		}
		out := &ListLit{expr: at, Nil: v.Nil}
		for _, item := range v.Items {
			value, err := decode(item, t.Elem)
			if err != nil {
				return nil, err
			}
			out.Elems = append(out.Elems, value)
		}
		return out, nil
	case *Record:
		if v.Text != "" || v.Nil || len(v.Items) != len(t.Fields) {
			return nil, fmt.Errorf("malformed record %s", t)
		}
		out := &RecordLit{expr: at, Record: t}
		for i, field := range t.Fields {
			value, err := decode(v.Items[i], field.Type)
			if err != nil {
				return nil, fmt.Errorf("field %s: %w", field.Name, err)
			}
			out.Fields = append(out.Fields, &FieldValue{Name: field.Name, Field: field, Value: value})
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported result type %s", t)
}
