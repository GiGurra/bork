package gen

import (
	"fmt"
	"go/ast"

	"github.com/GiGurra/bork/internal/check"
)

func shapeCaptures(fn *check.Func) []check.DeriveCapture {
	if fn.TemplateScope != nil {
		return fn.TemplateScope.Captures
	}
	return nil
}

func shapeCaptureParameter(index int) *ast.Ident {
	return ast.NewIdent(fmt.Sprintf("_shapeCapture%d", index))
}

func (g *gen) shapeCaptureArgument(original string) ast.Expr {
	if g.shapeScope != nil {
		for i, capture := range g.shapeScope.Captures {
			if capture.Name == original {
				return shapeCaptureParameter(i)
			}
		}
	}
	return name(original)
}

func (g *gen) shapeCaptureArguments(instance *check.ClassInstance) []ast.Expr {
	var arguments []ast.Expr
	if instance != nil {
		for _, capture := range instance.Captures {
			arguments = append(arguments, g.shapeCaptureArgument(capture.Name))
		}
	}
	return arguments
}

func (g *gen) shapeDictionaryParameters(instance *check.ClassInstance) []*ast.Field {
	parameters := g.dictParams(instance.TypeParams)
	for i, capture := range instance.Captures {
		parameters = append(parameters, &ast.Field{Names: []*ast.Ident{shapeCaptureParameter(i)}, Type: g.goType(capture.Type)})
	}
	return parameters
}

func (g *gen) dictionaryCaptureArguments(dictionary *check.Dict) []ast.Expr {
	if len(dictionary.Captures) != 0 {
		_, captures := g.values(dictionary.Captures)
		return captures
	}
	return g.shapeCaptureArguments(dictionary.Inst)
}
