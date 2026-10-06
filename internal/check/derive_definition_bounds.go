package check

import "github.com/GiGurra/bork/internal/syntax"

// Closed arguments have dictionary obligations even when another argument of
// the same call depends on a target. Selection waits for resolved constraints
// and established template bounds; no runtime value or predicate is evaluated.
type deriveDefinitionBound struct {
	call    *syntax.Call
	pkg     *Package
	class   *Class
	written *syntax.TypeExpr
	actual  Type
}

func (c *checker) collectDeriveDefinitionBounds(call *syntax.Call, typeNames map[string]bool) {
	id, named := call.Fun.(*syntax.Ident)
	if !named || call.Pipe.File != "" {
		return
	}
	var bounds [][]*Class
	if helper, pkg := c.deriveHelperNamed(c.pkg, id.Name); helper != nil {
		for _, parameter := range helper.TypeParams {
			var classes []*Class
			for _, name := range parameter.Bounds {
				outer := c.pkg
				c.pkg = pkg
				class := c.lookupClass(name)
				c.pkg = outer
				if class != nil {
					classes = append(classes, class)
				}
			}
			bounds = append(bounds, classes)
		}
	} else if function, found := c.funcNamed(id.Name); found {
		for _, parameter := range function.TypeParams {
			classes := append([]*Class(nil), parameter.Bounds...)
			if function.Class != nil && len(bounds) == 0 {
				classes = append(classes, function.Class)
			}
			bounds = append(bounds, classes)
		}
	}
	if len(bounds) != len(call.TypeArgs) {
		return
	}
	for i, argument := range call.TypeArgs {
		if len(bounds[i]) == 0 || !deriveConcreteType(argument, typeNames) || !deriveClosedBoundType(argument) {
			continue
		}
		actual := c.resolveType(argument)
		if actual == Invalid || hasTypeParam(actual) {
			continue
		}
		for _, class := range bounds[i] {
			c.deriveDefinitionBounds = append(c.deriveDefinitionBounds, deriveDefinitionBound{call: call, pkg: c.pkg, class: class, written: argument, actual: actual})
		}
	}
}

func deriveClosedBoundType(written *syntax.TypeExpr) bool {
	if written == nil || len(written.Where) != 0 {
		return false
	}
	for _, group := range [][]*syntax.TypeExpr{written.Args, written.Tuple, written.Union} {
		for _, argument := range group {
			if !deriveClosedBoundType(argument) {
				return false
			}
		}
	}
	if written.Func != nil {
		for _, parameter := range written.Func.Params {
			if !deriveClosedBoundType(parameter) {
				return false
			}
		}
		return deriveClosedBoundType(written.Func.Result)
	}
	return true
}

func (c *checker) checkDeriveDefinitionBounds() {
	pkg, function, parameters, facts := c.pkg, c.fn, c.typeParams, c.have
	defer func() { c.pkg, c.fn, c.typeParams, c.have = pkg, function, parameters, facts }()
	for _, obligation := range c.deriveDefinitionBounds {
		// Concrete expression islands already ran the ordinary call checker,
		// including failed dictionary selection. Do not report that failure twice.
		if _, checked := c.info.types[obligation.call]; checked {
			continue
		}
		c.pkg, c.fn, c.typeParams = obligation.pkg, nil, nil
		c.have = c.constraintsOf(obligation.written, obligation.actual, nil)
		// A constrained candidate may need facts from an argument whose type
		// still depends on the target. Keep that selection as an expansion
		// obligation instead of rejecting a possibly valid specialization.
		dependentFacts := false
		for _, instance := range c.pkg.inScope {
			if instance.Class == obligation.class && len(instance.Constraints) != 0 {
				if _, matches := matchHead(instance, obligation.actual); matches {
					dependentFacts = true
					break
				}
			}
		}
		if dependentFacts && len(c.have) == 0 {
			continue
		}
		c.dict(obligation.class, obligation.actual, obligation.written.Pos, 0)
	}
}
