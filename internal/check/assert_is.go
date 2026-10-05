package check

import (
	"github.com/GiGurra/bork/internal/syntax"
	"strings"
)

type assertIsInfo struct {
	Pattern  *Pat
	Target   Type
	Expected string
}

func assertIsIntrinsic(fn *Func) bool {
	return fn.Pkg != nil && fn.Pkg.Path == "bork/test" && fn.Decl.Name == "AssertIs" && !fn.Decl.IsMethod
}

func (c *checker) assertIs(call *syntax.Call, fn *Func, args []syntax.Expr, typeArgs []*syntax.TypeExpr) Type {
	if len(typeArgs) != 1 || len(args) != 1 {
		c.errorf(call.Pos, "test.AssertIs needs one explicit target type and one value")
		for _, a := range args {
			c.expr(a)
		}
		return Invalid
	}
	c.rejectNamedArgs(call, "test.AssertIs takes its value positionally")
	source := c.expr(args[0])
	target := c.resolveType(typeArgs[0])
	if source == Invalid || target == Invalid {
		return Invalid
	}
	if !isValue(source) || !isValue(target) {
		c.errorf(call.Pos, "test.AssertIs needs value types, found %s and %s", source, target)
		return Invalid
	}
	c.pushScope()
	saved := c.patternTest
	c.patternTest = true
	pat := c.pattern(&syntax.TypePat{Pos: call.Pos, Type: typeArgs[0]}, source)
	c.patternTest = saved
	if pat != nil {
		c.patSources(pat, args[0], "", nil, nil)
		c.patternTestLabels(pat, writtenText(args[0]))
	}
	c.popScope()
	if pat == nil {
		return Invalid
	}
	expected := target.String()
	cons := c.constraintsOf(typeArgs[0], target, c.paramScope())
	if len(cons) > 0 {
		var text []string
		for _, con := range cons {
			text = append(text, con.Text(c.pkg))
		}
		expected += " where " + strings.Join(text, " and ")
	}
	c.info.patternAssertions[call] = &assertIsInfo{Pattern: pat, Target: target, Expected: expected}
	c.info.callFuncs[call] = fn
	c.info.callArgs[call] = args
	c.info.instances[call] = &Instance{Func: fn, Params: []Type{source}, Result: target, TypeArgs: []Type{target, source}, ArgFacts: [][]*Constraint{cons, nil}}
	c.info.callTypeArgs[call] = typeArgs
	return target
}
