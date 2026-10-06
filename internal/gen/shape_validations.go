package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/GiGurra/bork/internal/check"
)

func (g *gen) shapeValidate(call *check.CallBuiltin, argument ast.Expr) ast.Expr {
	layout := call.Validation
	field := *layout.Field
	field.Computed = false
	field.Constraints = layout.Constraints
	errorType := g.info.PackageNamed("bork/shape").TypeNamed("ValidationError")
	var fields []*check.Field
	if layout.Variant != nil {
		fields = layout.Variant.Fields
	} else if owner, ok := layout.Owner.(*check.Record); ok {
		fields = owner.Fields
	}
	provenance := g.shapeObligation(&check.ShapeConstruction{Owner: layout.Owner, Variant: layout.Variant, Fields: fields})
	var body strings.Builder
	fmt.Fprintf(&body, "_out := struct { %s %s }{%s: _input}\n_ = _out\n", name(field.Name).Name, g.typeText(field.Type), name(field.Name).Name)
	body.WriteString(g.constructionChecks([]*check.Field{&field}, layout.Owner, []constructionInvariant{{fieldPath: func(*check.Field) string { return "" }}}, errorType, func(_ *check.Field, constraint *check.Constraint) []ast.Expr {
		return provenance(layout.Field, constraint)
	}))
	if layout.ReturnValue {
		body.WriteString("return _input\n")
	} else {
		fmt.Fprintf(&body, "return %s{}\n", g.typeText(check.Ok))
	}
	source := fmt.Sprintf("func(_input %s) %s {\n%s\n}", g.typeText(field.Type), g.typeText(call.Type()), body.String())
	function, err := parser.ParseExprFrom(token.NewFileSet(), "", source, 0)
	if err != nil {
		panic(err)
	}
	return &ast.CallExpr{Fun: function, Args: []ast.Expr{argument}}
}
