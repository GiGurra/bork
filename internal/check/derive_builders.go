package check

import (
	"fmt"
	"go/constant"
	"reflect"

	"github.com/GiGurra/bork/internal/syntax"
)

// ShapeConstruction retains resolved field and obligation identities. Its
// private storage contains optional erased field values, never an unchecked T.
type ShapeConstruction struct {
	Owner       Type
	Variant     *Variant
	Fields      []*Field
	Storage     *Record
	Constraints []*Constraint
}

// FieldPath names semantic slots without treating numeric storage labels as
// object keys. Format libraries add their own envelope path during mapping.
func (layout *ShapeConstruction) FieldPath(field *Field) string {
	positional := layout.Variant != nil && layout.Variant.Positional
	if record, ok := layout.Owner.(*Record); ok {
		positional = record.Tuple
	}
	if positional {
		for i, candidate := range layout.Fields {
			if candidate == field {
				return fmt.Sprintf("[%d]", i)
			}
		}
	}
	return "." + field.Name
}

type shapeRuntimeType struct{ typ Type }

type shapeBuildCall struct {
	operation string
	field     *Field
	variant   *Variant
	owner     Type
	layout    *ShapeConstruction
	expansion *deriveExpansion
	charged   bool
}

func (p *deriveExpansion) builderCreation(call *syntax.Call, variant *Variant, headFacts ...[]*Constraint) syntax.Expr {
	var target Type
	var constraints []*Constraint
	var fields []*Field
	if variant != nil {
		if len(call.TypeArgs) != 0 || len(call.Args) != 0 {
			p.error(call.Pos, "variant.builder takes no arguments")
			return &syntax.Block{Pos: call.Pos}
		}
		target, fields = variant.Parent, variant.Fields
		if len(headFacts) > 0 {
			constraints = headFacts[0]
		}
	} else {
		if len(call.TypeArgs) != 1 || len(call.Args) != 0 {
			p.error(call.Pos, "shape.builder takes one target type and no arguments")
			return &syntax.Block{Pos: call.Pos}
		}
		target, constraints = p.projectedTypeArg(call.TypeArgs[0])
		record, ok := target.(*Record)
		if !ok {
			p.error(call.Pos, "shape.builder requires a record; sealed payloads use variant.builder")
			return &syntax.Block{Pos: call.Pos}
		}
		fields = record.Fields
	}
	if !p.accessible(target, call.Pos) || !p.charge(call.Pos, 12+8*len(fields)) {
		return &syntax.Block{Pos: call.Pos}
	}
	// Layouts carry ordered source obligations as well as typed slots.
	key := typeKey(target) + "\x00" + deriveObligationsKey(constraints)
	if variant != nil {
		key += fmt.Sprintf("\x00variant:%d", variant.Index)
	}
	if p.instance != nil {
		key += "\x00" + deriveBoundKey(p.instance.Pkg, p.instance.Name, "")
	}
	if layout := p.c.info.shapeBuilderPlans[key]; layout != nil {
		result := &syntax.Call{Pos: call.Pos, Fun: &syntax.Ident{Pos: call.Pos, Name: "_shapeBuilder"}}
		p.recordBuildCall(result, &shapeBuildCall{operation: "create", layout: layout})
		return result
	}
	pkg := p.c.pkgs["bork/shape"]
	name := p.generatedName("builder", pkg)
	decl := &syntax.TypeDecl{Pos: call.Pos, Name: name, Kind: syntax.RecordType, Private: true}
	storage := &Record{Name: name, Pkg: pkg, Decl: decl, insts: &instanceSet{byKey: map[string]Type{}, resolved: true}}
	for i, field := range fields {
		if !field.Computed {
			optional := instantiate(p.c.preludePkg.TypeNamed("Option"), []Type{field.Type})
			storage.Fields = append(storage.Fields, &Field{Name: fmt.Sprintf("slot%d", i), Type: optional, Pkg: pkg})
		}
	}
	storage.Fields = append(storage.Fields, &Field{Name: "duplicate", Type: String, Pkg: pkg})
	if p.instance != nil {
		storage.TypeParams = p.instance.TypeParams
		for _, parameter := range storage.TypeParams {
			storage.Args = append(storage.Args, parameter)
		}
	}
	layout := &ShapeConstruction{Owner: target, Variant: variant, Fields: fields, Storage: storage, Constraints: constraints}
	if p.c.info.shapeBuilderLayouts == nil {
		p.c.info.shapeBuilderLayouts = map[*Record]*ShapeConstruction{}
	}
	p.c.info.shapeBuilderLayouts[storage] = layout
	if p.c.info.shapeBuilderPlans == nil {
		p.c.info.shapeBuilderPlans = map[string]*ShapeConstruction{}
	}
	p.c.info.shapeBuilderPlans[key] = layout
	p.c.info.TypeOrder = append(p.c.info.TypeOrder, storage)
	result := &syntax.Call{Pos: call.Pos, Fun: &syntax.Ident{Pos: call.Pos, Name: "_shapeBuilder"}}
	p.recordBuildCall(result, &shapeBuildCall{operation: "create", layout: layout})
	return result
}

func (p *deriveExpansion) recordBuildCall(call *syntax.Call, operation *shapeBuildCall) {
	operation.expansion = p
	if p.c.info.shapeBuildCalls == nil {
		p.c.info.shapeBuildCalls = map[*syntax.Call]*shapeBuildCall{}
	}
	p.c.info.shapeBuildCalls[call] = operation
}

func (p *deriveExpansion) builderOperation(call *syntax.Call, selector *syntax.Selector) syntax.Expr {
	if selector.Name == "finish" {
		if len(call.Args) != 0 || len(call.TypeArgs) != 0 {
			return nil // The ordinary checker diagnoses non-builder methods.
		}
		result := &syntax.Call{Pos: call.Pos, Fun: &syntax.Selector{Pos: selector.Pos, X: p.expr(selector.X), Name: "finish"}}
		p.recordBuildCall(result, &shapeBuildCall{operation: "finish"})
		return result
	}
	if selector.Name != "set" || len(call.Args) != 2 || len(call.TypeArgs) != 0 {
		return nil
	}
	descriptor, known := p.eval(call.Args[0])
	field, ok := descriptor.(shapeField)
	if !known || !ok {
		return nil
	}
	if field.field.Computed {
		p.error(call.Pos, "builder.set cannot supply computed field %s", field.field.Name)
		return &syntax.Block{Pos: call.Pos}
	}
	if !p.charge(call.Pos, 16) {
		return &syntax.Block{Pos: call.Pos}
	}
	result := &syntax.Call{Pos: call.Pos, Fun: &syntax.Selector{Pos: selector.Pos, X: p.expr(selector.X), Name: "set"}, Args: []syntax.Expr{p.expr(call.Args[1])}}
	p.recordBuildCall(result, &shapeBuildCall{operation: "set", field: field.field, owner: field.owner, variant: field.variant})
	return result
}

func (c *checker) shapeBuildCall(call *syntax.Call, operation *shapeBuildCall) (Type, bool) {
	if operation.operation == "create" {
		return operation.layout.Storage, true
	}
	selector := call.Fun.(*syntax.Selector)
	receiver := c.expr(selector.X)
	record, ok := genericBaseOrSelf(receiver).(*Record)
	if !ok {
		return nil, false
	}
	layout := c.info.shapeBuilderLayouts[record]
	if layout == nil {
		return nil, false
	}
	operation.layout = layout
	if operation.expansion.budget.remaining <= 0 {
		return Invalid, true
	}
	if !operation.charged {
		operation.charged = true
		if !operation.expansion.charge(call.Pos, 40+6*len(layout.Storage.Fields)) {
			return Invalid, true
		}
		if operation.operation == "finish" {
			for _, field := range layout.Fields {
				if source := c.info.fieldDefaults[field]; source != nil {
					if !operation.expansion.chargeDefaultSyntax(call.Pos, reflect.ValueOf(source)) {
						return Invalid, true
					}
				}
				for _, constraint := range field.Constraints {
					if !operation.expansion.chargeConstructionConstraint(call, constraint) {
						return Invalid, true
					}
				}
			}
			for _, constraint := range append(append(append([]*Constraint(nil), TypeConstraints(layout.Owner)...), layout.Constraints...), variantConstraints(layout.Variant)...) {
				if !operation.expansion.chargeConstructionConstraint(call, constraint) {
					return Invalid, true
				}
			}
		}
	}
	if operation.operation == "finish" {
		if c.fn != nil {
			for _, field := range layout.Fields {
				c.fn.Calls = append(c.fn.Calls, field.DefaultCalls...)
				for _, constraint := range field.Constraints {
					c.shapeConstraintCalls(constraint)
				}
			}
			for _, constraint := range append(append(append([]*Constraint(nil), TypeConstraints(layout.Owner)...), layout.Constraints...), variantConstraints(layout.Variant)...) {
				c.shapeConstraintCalls(constraint)
			}
		}
		return newUnion([]Type{layout.Owner, c.pkgs["bork/shape"].TypeNamed("ValidationError")}), true
	}
	// Structurally equal tuple spellings have fresh field objects. Their slot
	// ordinals identify the same owner member, unlike nominal field identities.
	if tuple, ok := layout.Owner.(*Record); ok && tuple.Tuple && identical(operation.owner, layout.Owner) {
		operation.field = findField(layout.Fields, operation.field.Name)
	}
	value := c.exprWant(call.Args[0], operation.field.Type)
	if !identical(operation.owner, layout.Owner) || operation.variant != layout.Variant {
		c.errorf(call.Pos, "builder.set requires a field from its exact owner and payload")
		return Invalid, true
	}
	if value != Invalid && !assignable(value, operation.field.Type) {
		c.errorf(call.Args[0].Position(), "builder.set field %s must be %s, found %s", operation.field.Name, operation.field.Type, value)
		return Invalid, true
	}
	return receiver, true
}

func variantConstraints(variant *Variant) []*Constraint {
	if variant == nil {
		return nil
	}
	return variant.Constraints
}

func (c *checker) shapeConstraintCalls(constraint *Constraint) {
	if constraint.Pred != nil {
		c.fn.Calls = append(c.fn.Calls, constraint.Pred)
	}
	for _, alternative := range constraint.Or {
		c.shapeConstraintCalls(alternative)
	}
}

func (l *lowerer) shapeBuild(source *syntax.Call, at expr, operation *shapeBuildCall) Expr {
	layout := operation.layout
	if operation.operation == "finish" {
		return &CallBuiltin{expr: at, Builtin: BuiltinShapeFinish, Name: "builder.finish", Args: []Expr{l.expr(source.Fun.(*syntax.Selector).X)}, Construction: layout}
	}
	literal := &RecordLit{expr: at, Record: layout.Storage}
	if operation.operation == "create" {
		for _, field := range layout.Storage.Fields {
			var value Expr = &Const{expr: expr{pos: source.Pos, typ: String}, Value: constant.MakeString("")}
			if optional, ok := field.Type.(*Sealed); ok {
				value = &VariantValue{expr: expr{pos: source.Pos, typ: optional}, Variant: optional.Variant("None")}
			}
			literal.Fields = append(literal.Fields, &FieldValue{Name: field.Name, Field: field, Value: value})
		}
		return literal
	}
	// Capture both arguments once before reading the old immutable state.
	receiver := l.expr(source.Fun.(*syntax.Selector).X)
	value := l.expr(source.Args[0])
	old := &Var{Name: "_shapeState", Label: "construction state", Pos: source.Pos, Type: receiver.Type(), Kind: VarLet}
	item := &Var{Name: "_shapeInput", Label: "stored input", Pos: source.Pos, Type: value.Type(), Kind: VarLet}
	old.Let = &Let{Pos: source.Pos, Var: old, Value: receiver}
	item.Let = &Let{Pos: source.Pos, Var: item, Value: value}
	oldRef := &VarRef{expr: expr{pos: source.Pos, typ: old.Type}, Var: old}
	itemRef := &VarRef{expr: expr{pos: source.Pos, typ: item.Type}, Var: item}
	index := 0
	var previous Expr
	for _, field := range layout.Fields {
		if field.Computed {
			continue
		}
		slot := layout.Storage.Fields[index]
		index++
		selected := &Select{expr: expr{pos: source.Pos, typ: slot.Type}, X: oldRef, Name: slot.Name, Field: slot}
		var next Expr = selected
		if field == operation.field {
			previous = selected
			some := slot.Type.(*Sealed).Variant("Some")
			next = &RecordLit{expr: selected.expr, Variant: some, Fields: []*FieldValue{{Name: some.Fields[0].Name, Field: some.Fields[0], Value: itemRef}}}
		}
		literal.Fields = append(literal.Fields, &FieldValue{Name: slot.Name, Field: slot, Value: next})
	}
	duplicate := layout.Storage.Fields[index]
	selected := &Select{expr: expr{pos: source.Pos, typ: String}, X: oldRef, Name: duplicate.Name, Field: duplicate}
	empty := &Const{expr: selected.expr, Value: constant.MakeString("")}
	path := &Const{expr: selected.expr, Value: constant.MakeString(layout.FieldPath(operation.field))}
	first := &If{expr: selected.expr, Cond: &Binary{expr: expr{pos: source.Pos, typ: Bool}, Op: syntax.Eq, X: selected, Y: empty}, Then: &Block{expr: selected.expr, Tail: path}, Else: selected}
	optional := previous.Type().(*Sealed)
	dups := &Match{expr: selected.expr, X: previous, Arms: []*MatchArm{
		{Pat: &Pat{Kind: PatVariant, Type: optional, Variant: optional.Variant("Some")}, Body: first},
		{Pat: &Pat{Kind: PatWild, Type: optional}, Body: selected},
	}}
	literal.Fields = append(literal.Fields, &FieldValue{Name: duplicate.Name, Field: duplicate, Value: dups})
	return &Block{expr: at, Stmts: []Stmt{old.Let, item.Let}, Tail: literal}
}

func (p *deriveExpansion) chargeConstructionConstraint(call *syntax.Call, constraint *Constraint) bool {
	if !p.charge(call.Pos, 16+len(constraint.Path)+8*len(constraint.Args)) {
		return false
	}
	for _, alternative := range constraint.Or {
		if !p.chargeConstructionConstraint(call, alternative) {
			return false
		}
	}
	return true
}
