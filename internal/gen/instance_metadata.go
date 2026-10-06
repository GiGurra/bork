package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"strings"

	"github.com/GiGurra/bork/internal/check"
)

func (g *gen) instanceMetadata(instance *check.ClassInstance) ast.Expr {
	var entries []string
	for _, initializer := range instance.Metadata {
		reference := &check.Instance{Func: initializer, Params: initializer.Params, Result: initializer.Result}
		for _, parameter := range instance.TypeParams {
			reference.TypeArgs = append(reference.TypeArgs, parameter)
			for _, class := range parameter.Bounds {
				reference.Dicts = append(reference.Dicts, &check.Dict{Class: class, Type: parameter, Param: parameter})
			}
		}
		call := &ast.CallExpr{Fun: g.funcRef(reference)}
		entries = append(entries, fmt.Sprintf("%q: func() any { return %s }", g.info.MetadataKey(initializer.Result), g.text(call)))
	}
	expression, err := parser.ParseExpr("map[string]func() any{" + strings.Join(entries, ",") + "}")
	if err != nil {
		panic(err)
	}
	return expression
}

func (g *gen) shapeMetadata(call *check.CallBuiltin) ast.Expr {
	optional := call.Type().(*check.Sealed)
	key := check.TypeArgs(optional)[0]
	some, none := optional.Variant("Some"), optional.Variant("None")
	source := fmt.Sprintf("func() %s { if provider := (%s)._metadata[%q]; provider != nil { return %s{%s: provider().(%s)} }; return %s{} }()", g.typeText(optional), g.text(g.dict(call.Dictionary)), g.info.MetadataKey(key), g.text(g.variantType(some)), name(some.Fields[0].Name).Name, g.typeText(key), g.text(g.variantType(none)))
	expression, err := parser.ParseExpr(source)
	if err != nil {
		panic(err)
	}
	return expression
}
