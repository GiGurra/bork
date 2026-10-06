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

// Structural Decode still uses the compiler backend while its source template
// and schema companions are being ported. Encode uses the codec source template.
//
// JSON objects map to records by field name. A sealed type's value is an
// object whose "type" field names the variant ({"type": "Circle",
// "radius": 2}); a variant without fields may also be just its name
// ("Empty").
//
// A derived decoder checks each field's where clause, so the record it
// returns is proven: the compiler trusts it like Go code, and it is
// right by construction.

// derivedFunc generates a derived instance method.
func (g *gen) derivedFunc(fn *check.Func) string {
	g.tmp = 0
	g.fnResult = fn.Result
	g.usesDerive = true
	g.goType(g.codecType("Value"))
	g.goType(g.codecType("Field"))
	var sig bytes.Buffer
	_ = printer.Fprint(&sig, token.NewFileSet(), g.signature(fn.Decl))
	var body string
	switch fn.Of.Class.Name {
	case "Decode":
		body = g.deriveDecode(fn)
	}
	return sig.String() + " {\n" + body + "}\n"
}

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

func (g *gen) deriveDecode(fn *check.Func) string {
	var b strings.Builder
	jsonVariant := func(name string) string {
		return g.text(g.variantType(g.codecType("Value").(*check.Sealed).Variant(name)))
	}
	fmt.Fprintf(&b, "_obj, _isObj := json.(%s)\n", jsonVariant("Object"))
	switch t := fn.Of.Type.(type) {
	case *check.Record:
		if len(t.Fields) == 0 {
			b.WriteString("_ = _obj\n")
		}
		b.WriteString("if !_isObj {\n" + g.decodeError(`""`, `"expected an object, found " + _jsonKind(json)`) + "}\n")
		b.WriteString(g.decodeFields(t.Fields, fn.Derived.FieldDicts[0], g.typeText(t), constructionInvariant{typ: t, constraints: append(append([]*check.Constraint{}, t.Constraints...), fn.Of.Constraints...)}))
	case *check.Sealed:
		b.WriteString("var _tag string\n")
		fmt.Fprintf(&b, "if _s, _isStr := json.(%s); _isStr {\n_tag = _s.value\n} else if _isObj {\n", jsonVariant("String"))
		b.WriteString("_t, _present := _jsonField(_obj, \"type\")\n")
		fmt.Fprintf(&b, "_ts, _isStr := _t.(%s)\n", jsonVariant("String"))
		b.WriteString("if !_present || !_isStr {\n" + g.decodeError(`".type"`, `"expected the variant's name, as a string"`) + "}\n")
		b.WriteString("_tag = _ts.value\n} else {\n")
		b.WriteString(g.decodeError(`""`, `"expected an object, found " + _jsonKind(json)`) + "}\n")
		b.WriteString("switch _tag {\n")
		for i, v := range t.Variants {
			fmt.Fprintf(&b, "case %q:\n", v.Name)
			cons := append(append([]*check.Constraint{}, t.Constraints...), v.Constraints...)
			cons = append(cons, fn.Of.Constraints...)
			b.WriteString(g.decodeFields(v.Fields, fn.Derived.FieldDicts[i], g.text(g.variantType(v)), constructionInvariant{typ: t, constraints: cons, positional: v.Positional}))
		}
		b.WriteString("}\n")
		g.imports["strconv"] = true
		b.WriteString(g.decodeError(`".type"`, strconv.Quote("unknown variant ")+" + strconv.Quote(_tag) + "+strconv.Quote(" of "+t.Name)))
	}
	return b.String()
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
