package check

import "github.com/GiGurra/bork/internal/syntax"

// ShapeFieldValidation holds independent resolved obligations. It cannot
// establish any fact about an incomplete owner or unavailable sibling input.
type ShapeFieldValidation struct {
	Owner       Type
	Variant     *Variant
	Field       *Field
	Constraints []*Constraint
	ReturnValue bool
}

type shapeValidationCall struct {
	layout    *ShapeFieldValidation
	expansion *deriveExpansion
	charged   bool
}

func (p *deriveExpansion) fieldValidation(call *syntax.Call, selector *syntax.Selector) syntax.Expr {
	if selector.Name != "validate" && selector.Name != "check" {
		return nil
	}
	value, known := p.eval(selector.X)
	descriptor, ok := value.(shapeField)
	if !known || !ok {
		return nil
	}
	if len(call.Args) != 1 || len(call.TypeArgs) != 0 {
		p.error(call.Pos, "field.%s takes one argument and no type arguments", selector.Name)
		return &syntax.Block{Pos: call.Pos}
	}
	layout := &ShapeFieldValidation{Owner: descriptor.owner, Variant: descriptor.variant, Field: descriptor.field, ReturnValue: selector.Name == "validate"}
	for _, constraint := range descriptor.field.Constraints {
		if !constraint.HasSiblingArgs() {
			layout.Constraints = append(layout.Constraints, constraint)
		}
	}
	result := &syntax.Call{Pos: call.Pos, Fun: &syntax.Ident{Pos: call.Pos, Name: "_shapeValidate"}, Args: []syntax.Expr{p.expr(call.Args[0])}}
	if p.c.info.shapeValidations == nil {
		p.c.info.shapeValidations = map[*syntax.Call]*shapeValidationCall{}
	}
	p.c.info.shapeValidations[result] = &shapeValidationCall{layout: layout, expansion: p}
	for _, frame := range p.budget.frames {
		frame.deferred = true
	}
	return result
}

func (c *checker) shapeValidateCall(call *syntax.Call, operation *shapeValidationCall) Type {
	layout := operation.layout
	if layout.ReturnValue && shapeValidationErrorOverlap(layout.Field.Type, c.pkgs["bork/shape"].TypeNamed("ValidationError")) {
		c.errorf(call.Pos, "field.validate requires a value type distinct from shape.ValidationError, found %s", layout.Field.Type)
		return Invalid
	}
	value := c.exprWant(call.Args[0], layout.Field.Type)
	if value != Invalid && !assignable(value, layout.Field.Type) {
		c.errorf(call.Args[0].Position(), "field.%s requires %s, found %s", layout.operationName(), layout.Field.Type, value)
		return Invalid
	}
	if !operation.charged {
		operation.charged = true
		for _, constraint := range layout.Constraints {
			if !operation.expansion.chargeConstructionConstraint(call, constraint) {
				return Invalid
			}
		}
	}
	if c.fn != nil {
		for _, constraint := range layout.Constraints {
			c.shapeConstraintCalls(constraint)
		}
	}
	success := Type(Ok)
	if layout.ReturnValue {
		success = layout.Field.Type
	}
	return newUnion([]Type{success, c.pkgs["bork/shape"].TypeNamed("ValidationError")})
}

func (l *lowerer) shapeValidate(call *syntax.Call, at expr, operation *shapeValidationCall) Expr {
	return &CallBuiltin{expr: at, Builtin: BuiltinShapeValidate, Name: "field." + operation.layout.operationName(), Args: []Expr{l.expr(call.Args[0])}, Validation: operation.layout}
}

// An erased union cannot distinguish a successful failure-typed value from a
// failed check. Unknown root parameters also need a disjoint concrete head.
func shapeValidationErrorOverlap(value, failure Type) bool {
	if identical(value, failure) {
		return true
	}
	switch value := value.(type) {
	case *TypeParam:
		return true
	case *Union:
		for _, member := range value.Members {
			if shapeValidationErrorOverlap(member, failure) {
				return true
			}
		}
	}
	return false
}

func (layout *ShapeFieldValidation) operationName() string {
	if layout.ReturnValue {
		return "validate"
	}
	return "check"
}
