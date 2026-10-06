package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
)

// shapeFinish is the checked construction boundary. Its input holds typed
// optional slots, and only the shared validation sequence can expose an owner.
func (g *gen) shapeFinish(call *check.CallBuiltin, argument ast.Expr) ast.Expr {
	layout := call.Construction
	errorType := g.info.PackageNamed("bork/shape").TypeNamed("ValidationError")
	var body strings.Builder
	duplicate := layout.Storage.Fields[len(layout.Storage.Fields)-1]
	fmt.Fprintf(&body, "if _state.%s != \"\" {\n%s}\n", name(duplicate.Name).Name, g.constructionError(errorType, "_state."+name(duplicate.Name).Name, strconv.Quote("field is supplied more than once")))
	index := 0
	var initializers []string
	for i, field := range layout.Fields {
		if field.Computed {
			continue
		}
		slot := layout.Storage.Fields[index]
		index++
		optional := slot.Type.(*check.Sealed)
		some := optional.Variant("Some")
		variable := fmt.Sprintf("_field%d", i)
		fmt.Fprintf(&body, "var %s %s\n", variable, g.typeText(field.Type))
		fmt.Fprintf(&body, "if _some%d, _present := _state.%s.(%s); _present {\n%s = %s\n} else {\n", i, name(slot.Name).Name, g.text(g.variantType(some)), variable, g.fieldReadText(fmt.Sprintf("_some%d", i), some.Fields[0]))
		if field.Default != nil {
			fmt.Fprintf(&body, "%s = %s\n", variable, g.fieldDefault(field))
		} else {
			body.WriteString(g.constructionError(errorType, strconv.Quote(layout.FieldPath(field)), strconv.Quote("is missing")))
		}
		body.WriteString("}\n")
		initializers = append(initializers, fmt.Sprintf("%s: %s", name(field.Name).Name, g.text(g.fieldResolved(ast.NewIdent(variable), field))))
	}
	ownerType := g.typeText(layout.Owner)
	if layout.Variant != nil {
		ownerType = g.text(g.variantType(layout.Variant))
	}
	fmt.Fprintf(&body, "_out := %s{%s}\n", ownerType, strings.Join(initializers, ","))
	constraints := append([]*check.Constraint(nil), check.TypeConstraints(layout.Owner)...)
	if layout.Variant != nil {
		constraints = append(constraints, layout.Variant.Constraints...)
	}
	constraints = append(constraints, layout.Constraints...)
	body.WriteString(g.constructionChecks(layout.Fields, layout.Owner, []constructionInvariant{{typ: layout.Owner, constraints: constraints, fieldPath: layout.FieldPath}}, errorType, g.shapeObligation(layout)))
	body.WriteString("return _out\n")
	source := fmt.Sprintf("func(_state %s) %s {\n%s\n}", g.typeText(call.Args[0].Type()), g.typeText(call.Type()), body.String())
	function, err := parser.ParseExprFrom(token.NewFileSet(), "", source, 0)
	if err != nil {
		panic(err)
	}
	return &ast.CallExpr{Fun: function, Args: []ast.Expr{argument}}
}

// shapeObligation freezes the resolved owner/variant/field identity and the
// ordinal of the source obligation. Display predicate text is not an identity.
func (g *gen) shapeObligation(layout *check.ShapeConstruction) func(*check.Field, *check.Constraint) []ast.Expr {
	return func(field *check.Field, constraint *check.Constraint) []ast.Expr {
		fieldName, variantName := "", ""
		constraints := append([]*check.Constraint(nil), check.TypeConstraints(layout.Owner)...)
		if layout.Variant != nil {
			variantName = layout.Variant.Name
			constraints = append(constraints, layout.Variant.Constraints...)
		}
		constraints = append(constraints, layout.Constraints...)
		if field != nil {
			fieldName = field.Name
			if strings.HasPrefix(layout.FieldPath(field), "[") {
				fieldName = layout.FieldPath(field)
			}
			constraints = field.Constraints
		}
		index := -1
		for i, candidate := range constraints {
			if candidate == constraint {
				index = i
				break
			}
		}
		owner := layout.Owner.String()
		switch target := layout.Owner.(type) {
		case *check.Record:
			if target.Pkg != nil {
				owner = target.Pkg.Path + ":" + owner
			}
		case *check.Sealed:
			if target.Pkg != nil {
				owner = target.Pkg.Path + ":" + owner
			}
		}
		obligation := g.info.PackageNamed("bork/shape").TypeNamed("Obligation")
		value := &ast.CompositeLit{Type: g.goType(obligation), Elts: []ast.Expr{
			&ast.KeyValueExpr{Key: ast.NewIdent("owner"), Value: stringLit(owner)},
			&ast.KeyValueExpr{Key: ast.NewIdent("variant"), Value: stringLit(variantName)},
			&ast.KeyValueExpr{Key: ast.NewIdent("field"), Value: stringLit(fieldName)},
			&ast.KeyValueExpr{Key: ast.NewIdent("index"), Value: &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(index)}},
			&ast.KeyValueExpr{Key: ast.NewIdent("source"), Value: stringLit(constraint.Pos.String())},
		}}
		optional := g.info.PackageNamed("bork/shape").TypeNamed("ValidationError").(*check.Record).Fields[2].Type.(*check.Sealed)
		some := optional.Variant("Some")
		return []ast.Expr{&ast.KeyValueExpr{Key: ast.NewIdent("obligation"), Value: &ast.CompositeLit{Type: g.variantType(some), Elts: []ast.Expr{
			&ast.KeyValueExpr{Key: name(some.Fields[0].Name), Value: value},
		}}}}
	}
}
