package check

import (
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// shape.exhausted closes a sequence of complete, dominating projections of
// one immutable owner parameter. It grants no value facts or construction.
func (c *checker) shapeExhaustionCall(call *syntax.Call) (Type, bool) {
	id, ok := call.Fun.(*syntax.Ident)
	if !ok {
		return nil, false
	}
	alias, member, qualified := strings.Cut(id.Name, ".")
	pkg := c.pkg.imports[alias]
	if !qualified || member != "exhausted" || pkg == nil || pkg.Path != "bork/shape" {
		return nil, false
	}
	c.pkg.used[alias] = true
	if c.fn == nil || c.fn.TemplateScope == nil || len(call.TypeArgs) != 1 || len(call.Args) != 1 {
		c.errorf(call.Pos, "shape.exhausted requires a sealed owner parameter inside a derive template")
		return Invalid, true
	}
	owner, ok := c.resolveType(call.TypeArgs[0]).(*Sealed)
	if !ok {
		c.errorf(call.Pos, "shape.exhausted requires a sealed target")
		return Invalid, true
	}
	subject, ok := call.Args[0].(*syntax.Ident)
	actual := c.expr(call.Args[0])
	if !ok || !identical(actual, owner) {
		c.errorf(call.Pos, "shape.exhausted requires its sealed owner parameter")
		return Invalid, true
	}
	definition := c.info.defs[subject]
	immutable := false
	for _, parameter := range c.fn.Decl.Params {
		if definition == parameter {
			immutable = true
		}
	}
	if !immutable {
		c.errorf(call.Pos, "shape.exhausted requires an immutable function parameter")
		return Invalid, true
	}
	handled := map[*Variant]bool{}
	if !c.exhaustionPath(c.fn.Decl.Body, call, definition, handled) {
		c.errorf(call.Pos, "shape.exhausted requires a direct continuation after variant projections")
		return Invalid, true
	}
	for _, variant := range owner.Variants {
		if !handled[variant] {
			c.errorf(call.Pos, "shape.exhausted has not handled variant %s", variant.Name)
			return Invalid, true
		}
	}
	if c.info.shapeExhaustions == nil {
		c.info.shapeExhaustions = map[*syntax.Call]bool{}
	}
	c.info.shapeExhaustions[call] = true
	return Never, true
}

// Only unconditional blocks are transparent. Projections under an if, loop,
// lambda or runtime match cannot certify the enclosing continuation.
func (c *checker) exhaustionPath(expression syntax.Expr, finish *syntax.Call, subject any, handled map[*Variant]bool) bool {
	if expression == finish {
		return true
	}
	switch expression := expression.(type) {
	case *syntax.Block:
		for _, statement := range expression.Stmts {
			if statement, ok := statement.(*syntax.ExprStmt); ok && c.exhaustionPath(statement.X, finish, subject, handled) {
				return true
			}
		}
		if expression.Tail != nil {
			return c.exhaustionPath(expression.Tail, finish, subject, handled)
		}
	case *syntax.Return:
		if expression.Value != nil {
			return c.exhaustionPath(expression.Value, finish, subject, handled)
		}
	case *syntax.Match:
		call, ok := expression.X.(*syntax.Call)
		if !ok {
			return false
		}
		project := c.info.shapeProjects[call]
		if project == nil || len(call.Args) != 1 {
			return false
		}
		value, ok := call.Args[0].(*syntax.Ident)
		if !ok || c.info.defs[value] != subject {
			return false
		}
		for _, arm := range expression.Arms {
			pattern := c.info.armPats[arm]
			if pattern == nil {
				return false
			}
			if pattern.Kind == PatNever || pattern.Kind == PatVariant && pattern.Variant.Name == "None" {
				continue
			}
			// An earlier guarded/refutable Some (or wildcard) arm can
			// intercept the successful projection. Every such arm must
			// escape before a later complete arm can certify coverage.
			if c.info.types[arm.Body] != Never {
				return false
			}
			if pattern.Kind != PatVariant || pattern.HasGuard() || pattern.Variant.Name != "Some" {
				continue
			}
			complete := true
			for _, field := range pattern.Fields {
				if field.Pat.Kind != PatWild {
					complete = false
				}
			}
			if complete {
				handled[project.variant] = true
				break
			}
		}
	}
	return false
}
