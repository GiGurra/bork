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

// Derived instances (`derive (Decode, Encode)`) are written by the
// compiler, as Go.
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
	g.goType(g.info.Named["Json"])
	g.goType(g.info.Named["JsonField"])
	var sig bytes.Buffer
	_ = printer.Fprint(&sig, token.NewFileSet(), g.signature(fn.Decl))
	var body string
	switch fn.Of.Class.Name {
	case "Decode":
		body = g.deriveDecode(fn)
	case "Encode":
		body = g.deriveEncode(fn)
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
	return fmt.Sprintf("return %s{path: %s, message: %s}\n", g.typeText(g.info.Named["DecodeError"]), path, message)
}

func (g *gen) deriveDecode(fn *check.Func) string {
	var b strings.Builder
	jsonVariant := func(name string) string {
		return g.text(g.variantType(g.info.Named["Json"].(*check.Sealed).Variant(name)))
	}
	fmt.Fprintf(&b, "_obj, _isObj := json.(%s)\n", jsonVariant("Object"))
	switch t := fn.Of.Type.(type) {
	case *check.Record:
		if len(t.Fields) == 0 {
			b.WriteString("_ = _obj\n")
		}
		b.WriteString("if !_isObj {\n" + g.decodeError(`""`, `"expected an object, found " + _jsonKind(json)`) + "}\n")
		b.WriteString(g.decodeFields(t.Fields, fn.Derived.FieldDicts[0], g.typeText(t), decodeInvariant{t, append(append([]*check.Constraint{}, t.Constraints...), fn.Of.Constraints...)}))
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
			b.WriteString(g.decodeFields(v.Fields, fn.Derived.FieldDicts[i], g.text(g.variantType(v)), decodeInvariant{t, cons}))
		}
		b.WriteString("}\n")
		g.imports["strconv"] = true
		b.WriteString(g.decodeError(`".type"`, strconv.Quote("unknown variant ")+" + strconv.Quote(_tag) + "+strconv.Quote(" of "+t.Name)))
	}
	return b.String()
}

// decodeFields decodes the fields of a record (or variant) from _obj,
// and returns the value built from them.
type decodeInvariant struct {
	typ         check.Type
	constraints []*check.Constraint
}

func (g *gen) decodeFields(fields []*check.Field, dicts []*check.Dict, goType string, invariants ...decodeInvariant) string {
	var b strings.Builder
	var inits []string
	for i, f := range fields {
		if f.Computed {
			fmt.Fprintf(&b, "if _, present := _jsonField(_obj, %q); present {\n%s}\n", f.Name, g.decodeError(strconv.Quote("."+f.Name), strconv.Quote("computed field is read-only")))
			continue
		}
		v := fmt.Sprintf("_f%d", i)
		path := strconv.Quote("." + f.Name)
		fmt.Fprintf(&b, "var %s %s\n{\n", v, g.typeText(f.Type))
		fmt.Fprintf(&b, "_v, _present := _jsonField(_obj, %q)\n", f.Name)
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
		fmt.Fprintf(&b, "if _e, _isErr := _r.(%s); _isErr {\n", g.typeText(g.info.Named["DecodeError"]))
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
	phases := 1
	if hasComputedFields(fields) {
		phases = 2
	}
	for phase := 0; phase < phases; phase++ {
		if phase == 1 {
			for _, stmt := range g.computedCells(ast.NewIdent("_out"), fields, owner) {
				b.WriteString(g.text(stmt) + "\n")
			}
		}
		for _, f := range fields {
			for _, con := range f.Constraints {
				early := !f.Computed && !con.HasSiblingArgs()
				if phases == 2 && (phase == 0) != early {
					continue
				}
				stmts := g.atFailurePath(g.fieldRead(ast.NewIdent("_out"), f), f.Type, splitPath(con.Path), stringLit("."+f.Name), func(x ast.Expr, t check.Type, path ast.Expr) []ast.Stmt {
					runtime := fieldConstraint(con, func(n string) string {
						for _, sibling := range fields {
							if sibling.Name == n {
								return g.fieldReadText("_out", sibling)
							}
						}
						return name(n).Name
					})
					cond := g.constraintCond(runtime, x, t)
					if cond == nil {
						return nil
					}
					setup, failurePath, message := g.constraintFailure(runtime, x, t, path, con)
					ret := &ast.ReturnStmt{Results: []ast.Expr{&ast.CompositeLit{Type: g.goType(g.info.Named["DecodeError"]), Elts: []ast.Expr{
						&ast.KeyValueExpr{Key: ast.NewIdent("path"), Value: failurePath},
						&ast.KeyValueExpr{Key: ast.NewIdent("message"), Value: message},
					}}}}
					return []ast.Stmt{&ast.IfStmt{Cond: &ast.UnaryExpr{Op: token.NOT, X: paren(cond)}, Body: &ast.BlockStmt{List: append(setup, ret)}}}
				})
				for _, s := range stmts {
					b.WriteString(g.text(s) + "\n")
				}
			}
		}
	}

	for _, inv := range invariants {
		for _, con := range inv.constraints {
			if cond := g.constraintCond(con, ast.NewIdent("_out"), inv.typ); cond != nil {
				setup, path, message := g.constraintFailure(con, ast.NewIdent("_out"), inv.typ, stringLit(""))
				fmt.Fprintf(&b, "if !(%s) {\n", g.text(cond))
				for _, stmt := range setup {
					b.WriteString(g.text(stmt) + "\n")
				}
				b.WriteString(g.decodeError(g.text(path), g.text(message)) + "}\n")
			}
		}
	}
	b.WriteString("return _out\n")
	return b.String()
}

func (g *gen) deriveEncode(fn *check.Func) string {
	var b strings.Builder
	obj := g.text(g.variantType(g.info.Named["Json"].(*check.Sealed).Variant("Object")))
	str := g.text(g.variantType(g.info.Named["Json"].(*check.Sealed).Variant("String")))
	field := g.typeText(g.info.Named["JsonField"])
	fields := func(x string, fs []*check.Field, dicts []*check.Dict, tag string) string {
		var parts []string
		if tag != "" {
			parts = append(parts, fmt.Sprintf("{name: \"type\", value: %s{value: %q}}", str, tag))
		}
		for i, f := range fs {
			if f.Computed {
				continue
			}
			fun, ds := g.dictMethod(dicts[i], "encode")
			call := g.text(&ast.CallExpr{Fun: fun, Args: append(ds, g.fieldRead(ast.NewIdent(x), f))})
			parts = append(parts, fmt.Sprintf("{name: %q, value: %s}", f.Name, call))
		}
		return fmt.Sprintf("return %s{fields: []%s{%s}}\n", obj, field, strings.Join(parts, ", "))
	}
	switch t := fn.Of.Type.(type) {
	case *check.Record:
		b.WriteString(fields("x", t.Fields, fn.Derived.FieldDicts[0], ""))
	case *check.Sealed:
		b.WriteString("switch _v := x.(type) {\n")
		for i, v := range t.Variants {
			fmt.Fprintf(&b, "case %s:\n", g.text(g.variantType(v)))
			if len(v.Fields) == 0 {
				b.WriteString("_ = _v\n")
			}
			b.WriteString(fields("_v", v.Fields, fn.Derived.FieldDicts[i], v.Name))
		}
		b.WriteString("}\npanic(\"bork: unreachable\")\n")
	}
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
