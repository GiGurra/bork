package check

import (
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// A fallback's String is never a canonical name or alias. Generate the same
// transparent predicate obligation as a written field where clause, so both
// ordinary construction and checked builders enforce the wire invariant.
func (c *checker) checkCodecFallbacks() {
	codec := c.pkgs["bork/codec"]
	if codec == nil {
		return
	}
	savedPkg := c.pkg
	defer func() { c.pkg = savedPkg }()
	for _, typ := range c.info.TypeOrder {
		sealed, ok := typ.(*Sealed)
		if !ok || sealed.Decl == nil {
			continue
		}
		plan := &deriveExpansion{c: c, template: &DeriveTemplate{Pkg: codec}, scope: sealed.Pkg, env: map[string]any{"T": sealed}, typeFacts: map[string][]*Constraint{}, names: map[string]bool{}, budget: &deriveBudget{remaining: 100000}}
		var fallback *Variant
		marked := map[*Variant]bool{}
		valid := true
		for _, variant := range sealed.Variants {
			value, known := codecFallbackHelper(plan, sealed.Decl.Pos, "VariantFallback", variant)
			if !known || value != true {
				continue
			}
			marked[variant] = true
			decl := sealed.Decl.Variants[variant.Index]
			if fallback != nil {
				c.errorf(decl.Pos, "a sealed type can have at most one codec fallback variant")
				valid = false
				continue
			}
			fallback = variant
			if !variant.Positional || len(variant.Fields) != 1 || !identical(variant.Fields[0].Type, String) || len(variant.Fields[0].Constraints) != 0 || len(decl.Where) != 0 {
				c.errorf(decl.Pos, "codec fallback requires one positional String without facts, defaults, or a variant where clause")
				valid = false
			}
			tags := plan.c.info.tagGroups
			for _, group := range decl.TagGroups {
				literal := tags[group]
				if literal == nil || !identical(plan.c.info.types[literal], codec.TypeNamed("VariantTags")) {
					continue
				}
				name, _ := plan.checkedTagProperty(metadataChecked{value: literal}, "name")
				aliases, _ := plan.checkedTagProperty(metadataChecked{value: literal}, "aliases")
				named, hasName := name.(metadataVariant)
				list, _ := aliases.(metadataList)
				if hasName && named.name != "None" || len(list.items) != 0 {
					c.errorf(group.Pos, "codec fallback cannot declare a wire name or aliases")
					valid = false
				}
			}
		}
		if fallback == nil {
			continue
		}
		var names []string
		knownVariants := 0
		for _, variant := range sealed.Variants {
			if marked[variant] {
				continue
			}
			knownVariants++
			if len(variant.Fields) != 0 {
				c.errorf(sealed.Decl.Variants[variant.Index].Pos, "codec fallback is only supported with fieldless known variants")
				valid = false
				continue
			}
			value, known := codecFallbackHelper(plan, sealed.Decl.Pos, "VariantNames", variant)
			list, ok := value.(metadataList)
			if !known || !ok {
				valid = false
				continue
			}
			for _, item := range list.items {
				if name, ok := item.(string); ok {
					names = append(names, name)
				}
			}
		}
		if valid && knownVariants == 0 {
			c.errorf(sealed.Decl.Variants[fallback.Index].Pos, "an enum with a fallback needs at least one known variant")
			valid = false
		}
		if !valid {
			continue
		}
		pos := sealed.Decl.Variants[fallback.Index].Pos
		var body syntax.Expr = &syntax.BoolLit{Pos: pos, Value: true}
		for _, name := range names {
			comparison := &syntax.Binary{Pos: pos, Op: syntax.NotEq, X: &syntax.Ident{Pos: pos, Name: "_codecInput"}, Y: &syntax.StringLit{Pos: pos, Value: name}}
			body = &syntax.Binary{Pos: pos, Op: syntax.AndAnd, X: body, Y: comparison}
		}
		declaration := &syntax.FuncDecl{Pos: pos, Name: plan.generatedName("codecUnknown_"+sealed.Name+"_"+fallback.Name, sealed.Pkg), IsPred: true, Params: []*syntax.Param{{Pos: pos, Name: "_codecInput", Type: &syntax.TypeExpr{Pos: pos, Name: "String"}}}, Result: &syntax.TypeExpr{Pos: pos, Name: "Bool"}, Body: &syntax.Block{Pos: pos, Tail: body}}
		c.pkg = sealed.Pkg
		c.declareFunc(declaration, sealed.Prelude)
		predicate := c.info.FuncOf[declaration]
		if predicate == nil {
			continue
		}
		predicate.TemplatePkg = sealed.Pkg
		c.info.ExpandedFunctions = append(c.info.ExpandedFunctions, predicate)
		constraint := &Constraint{Pred: predicate, Pos: pos, Pkg: sealed.Pkg}
		fallback.Fields[0].Constraints = append(fallback.Fields[0].Constraints, constraint)
		for _, typ := range sealed.insts.byKey {
			instance := typ.(*Sealed)
			if fallback.Index < len(instance.Variants) {
				instance.Variants[fallback.Index].Fields[0].Constraints = append(instance.Variants[fallback.Index].Fields[0].Constraints, constraint)
			}
		}
	}
}

func codecFallbackHelper(plan *deriveExpansion, pos diag.Pos, name string, variant *Variant) (any, bool) {
	plan.env["variant"] = shapeVariant{variant: variant}
	return plan.eval(&syntax.Call{Pos: pos, Fun: &syntax.Ident{Pos: pos, Name: name}, TypeArgs: []*syntax.TypeExpr{{Pos: pos, Name: "T"}}, Args: []syntax.Expr{&syntax.Ident{Pos: pos, Name: "variant"}}})
}
