package check

import "github.com/GiGurra/bork/internal/syntax"

type shapeProjection struct {
	variant *Variant
	view    *Record
}
type shapeViewRead struct {
	variant *Variant
	field   *Field
	view    *Record
}

// A payload view is a private nominal record specific to one resolved variant.
// Source code cannot construct or update it; projection stores a proven owner.
func (p *deriveExpansion) variantView(variant *Variant) *Record {
	var parameters []*TypeParam
	if p.instance != nil {
		parameters = p.instance.TypeParams
	}
	if !p.charge(variant.Parent.Decl.Pos, 8+len(parameters)) {
		return nil
	}
	if view := p.c.info.shapeViews[variant]; view != nil {
		return view
	}
	if p.c.info.shapeViews == nil {
		p.c.info.shapeViews = map[*Variant]*Record{}
	}
	pkg := p.c.pkgs["bork/shape"]
	name := p.generatedName("view", pkg)
	decl := &syntax.TypeDecl{Pos: variant.Parent.Decl.Pos, Name: name, Kind: syntax.RecordType, Private: true}
	view := &Record{Name: name, Pkg: pkg, Decl: decl, Fields: []*Field{{Name: "value", Type: variant.Parent, Pkg: pkg}}, insts: &instanceSet{byKey: map[string]Type{}, resolved: true}}
	if p.instance != nil {
		view.TypeParams = parameters
		for _, param := range view.TypeParams {
			view.Args = append(view.Args, param)
		}
	}
	p.c.info.shapeViews[variant] = view
	p.c.info.TypeOrder = append(p.c.info.TypeOrder, view)
	return view
}

func (p *deriveExpansion) variantProject(call *syntax.Call, variant *Variant) syntax.Expr {
	if len(call.Args) != 1 || len(call.TypeArgs) != 0 {
		p.error(call.Pos, "variant.project takes its proven sealed owner value")
		return &syntax.Block{Pos: call.Pos}
	}
	view := p.variantView(variant)
	if view == nil || !p.charge(call.Pos, 14) {
		return &syntax.Block{Pos: call.Pos}
	}
	value := p.expr(call.Args[0])
	result := &syntax.Call{Pos: call.Pos, Start: call.Start, Fun: &syntax.Ident{Pos: call.Pos, Name: "_shapeProject"}, Args: []syntax.Expr{value}}
	if p.c.info.shapeProjects == nil {
		p.c.info.shapeProjects = map[*syntax.Call]*shapeProjection{}
	}
	p.c.info.shapeProjects[result] = &shapeProjection{variant: variant, view: view}
	return result
}

func (l *lowerer) shapeProjection(source *syntax.Call, at expr, project *shapeProjection) Expr {
	owner := project.variant.Parent
	variable := &Var{Name: "_shapeOwner", Label: "proven sealed owner", Pos: source.Pos, Type: owner, Kind: VarLet}
	value := &VarRef{expr: expr{pos: source.Pos, typ: owner}, Var: variable}
	view := &RecordLit{expr: expr{pos: source.Pos, typ: project.view}, Record: project.view, Fields: []*FieldValue{{Name: "value", Field: project.view.Fields[0], Value: value}}}
	option := at.typ.(*Sealed)
	some := option.Variant("Some")
	result := &RecordLit{expr: at, Variant: some, Fields: []*FieldValue{{Name: some.Fields[0].Name, Field: some.Fields[0], Value: view}}}
	dispatch := &Match{expr: at, X: value, Arms: []*MatchArm{
		{Pat: &Pat{Kind: PatVariant, Type: owner, Variant: project.variant}, Body: result},
		{Pat: &Pat{Kind: PatWild, Type: owner}, Body: &VariantValue{expr: at, Variant: option.Variant("None")}},
	}}
	binding := &Let{Pos: source.Pos, Var: variable, Value: l.expr(source.Args[0])}
	variable.Let = binding
	return &Block{expr: at, Stmts: []Stmt{binding}, Tail: dispatch}
}

func (l *lowerer) shapeViewRead(source *syntax.Selector, at expr, read *shapeViewRead) Expr {
	owner := &Select{expr: expr{pos: source.Pos, typ: read.variant.Parent}, X: l.expr(source.X), Name: "value", Field: read.view.Fields[0]}
	variable := &Var{Name: "_shapeField", Label: "proven payload field", Pos: source.Pos, Type: read.field.Type, Kind: VarPattern, Sibling: read.field, Source: &VarSource{Subject: owner, Path: "." + read.field.Name, Field: read.field}}
	pattern := &Pat{Kind: PatVariant, Type: read.variant.Parent, Variant: read.variant, Fields: []*PatField{{Name: read.field.Name, Pat: &Pat{Kind: PatWild, Type: read.field.Type, Bind: variable.Name, BindType: read.field.Type, Var: variable}}}}
	// This view can only come from the matching projection, so the alternative
	// branches are unreachable without explicitly unsafe representation changes.
	return &Match{expr: at, X: owner, Arms: []*MatchArm{{Pat: pattern, Body: &VarRef{expr: at, Var: variable}}}}
}
