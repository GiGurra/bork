package gen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
)

// Checked source templates implement structural codecs. The construction
// helpers below remain shared by foreign Go conversion and captured tuples.

func (g *gen) text(x ast.Node) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, token.NewFileSet(), x)
	return buf.String()
}

func (g *gen) typeText(t check.Type) string { return g.text(g.goType(t)) }

// decodeError is Go code returning a DecodeError.
func (g *gen) decodeError(path, message string) string {
	return g.constructionError(g.codecType("DecodeError"), path, message)
}

// decodeFields decodes the fields of a record (or variant) from _obj,
// and returns the value built from them.
type constructionInvariant struct {
	fieldPath   func(*check.Field) string
	positional  bool
	typ         check.Type
	constraints []*check.Constraint
}

func (g *gen) decodeFields(fields []*check.Field, dicts []*check.Dict, goType string, invariants ...constructionInvariant) string {
	var b strings.Builder
	var inits []string
	positional := len(invariants) > 0 && invariants[0].positional
	if positional {
		array := g.text(g.variantType(g.codecType("Value").(*check.Sealed).Variant("Array")))
		b.WriteString("if !_isObj {\n" + g.decodeError(`".values"`, `"expected an array of payload values"`) + "}\n")
		b.WriteString("_values, _present := _jsonField(_obj, \"values\")\n")
		fmt.Fprintf(&b, "_array, _isArray := _values.(%s)\n", array)
		b.WriteString("if !_present || !_isArray {\n" + g.decodeError(`".values"`, `"expected an array of payload values"`) + "}\n")
		fmt.Fprintf(&b, "if len(_array.items) != %d {\n%s}\n", len(fields), g.decodeError(`".values"`, strconv.Quote(fmt.Sprintf("expected an array of length %d", len(fields)))))
	}
	fieldPath := func(f *check.Field) string {
		if positional {
			return ".values[" + f.Name + "]"
		}
		return "." + f.Name
	}
	for i, f := range fields {
		if f.Computed {
			fmt.Fprintf(&b, "if _, present := _jsonField(_obj, %q); present {\n%s}\n", f.Name, g.decodeError(strconv.Quote("."+f.Name), strconv.Quote("computed field is read-only")))
			continue
		}
		v := fmt.Sprintf("_f%d", i)
		path := strconv.Quote(fieldPath(f))
		fmt.Fprintf(&b, "var %s %s\n{\n", v, g.typeText(f.Type))
		if positional {
			fmt.Fprintf(&b, "_v, _present := _array.items[%d], true\n", i)
		} else {
			fmt.Fprintf(&b, "_v, _present := _jsonField(_obj, %q)\n", f.Name)
		}
		if f.Default != nil {
			fmt.Fprintf(&b, "if !_present { %s = %s } else {\n", v, g.fieldDefault(f))
		} else if !check.IsOption(f.Type) {
			b.WriteString("if !_present {\n" + g.decodeError(path, `"is missing"`) + "}\n")
		} else {
			b.WriteString("_ = _present\n")
		}
		fun, ds := g.dictMethod(dicts[i], "decode")
		args := append(ds, ast.NewIdent("_v"))
		fmt.Fprintf(&b, "_r := %s\n", g.text(&ast.CallExpr{Fun: fun, Args: args}))
		fmt.Fprintf(&b, "if _e, _isErr := _r.(%s); _isErr {\n", g.typeText(g.codecType("DecodeError")))
		b.WriteString(g.decodeError(path+" + _e.path", "_e.message") + "}\n")
		fmt.Fprintf(&b, "%s = _r.(%s)\n", v, g.typeText(f.Type))

		if f.Default != nil {
			b.WriteString("}\n")
		}
		b.WriteString("}\n")
		inits = append(inits, fmt.Sprintf("%s: %s", name(f.Name).Name, g.text(g.fieldResolved(ast.NewIdent(v), f))))
	}
	fmt.Fprintf(&b, "_out := %s{%s}\n", goType, strings.Join(inits, ", "))
	owner := check.Invalid
	if len(invariants) > 0 {
		owner = invariants[0].typ
	}
	b.WriteString(g.constructionChecks(fields, owner, invariants, g.codecType("DecodeError")))
	b.WriteString("return _out\n")
	return b.String()
}

const deriveRuntime = `package main

// _jsonField finds a field of a JSON object.
func _jsonField(obj Json_Object, name string) (Json, bool) {
	for _, f := range obj.fields {
		if f.name == name {
			return f.value, true
		}
	}
	return Json_Null{}, false
}

// _jsonKind names the kind of a JSON value, for messages.
func _jsonKind(j Json) string {
	switch j.(type) {
	case Json_Null:
		return "null"
	case Json_Bool:
		return "a boolean"
	case Json_Number:
		return "a number"
	case Json_String:
		return "a string"
	case Json_Array:
		return "an array"
	}
	return "an object"
}
`
