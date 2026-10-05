package check

import (
	"fmt"
	"go/constant"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

func swapIntrinsic(fn *Func) bool {
	return fn.Pkg.Path == "bork/test" && !fn.Decl.IsMethod && (fn.Decl.Name == "Swap" || fn.Decl.Name == "SwapAt")
}

func (c *checker) tupleSwap(call *syntax.Call, fn *Func) Type {
	name := "test." + fn.Decl.Name
	fail := func(pos diag.Pos, format string, args ...any) { c.diags.AddCode(pos, "test.swap", format, args...) }
	c.rejectNamedArgs(call, "tuple replacement helpers require positional arguments")
	if len(call.TypeArgs) != 0 {
		fail(call.Pos, "%s takes no explicit type arguments", name)
	}
	arity := 2
	if fn.Decl.Name == "SwapAt" {
		arity = 3
	}
	if len(call.Args) != arity {
		fail(call.Pos, "%s takes %d arguments, but %d were given", name, arity, len(call.Args))
		for _, arg := range call.Args {
			c.expr(arg)
		}
		return Invalid
	}
	typ := c.expr(call.Args[0])
	tuple, ok := typ.(*Record)
	if !ok || !tuple.Tuple || len(tuple.Fields) == 0 {
		if typ != Invalid {
			fail(call.Args[0].Position(), "%s requires a nonempty tuple, found %s", name, typ)
		}
		for _, arg := range call.Args[1:] {
			c.expr(arg)
		}
		return Invalid
	}
	index := -1
	var want Type
	if arity == 3 {
		saved := c.used
		c.used = 0
		indexType := c.exprWant(call.Args[1], Int)
		effects := c.used
		c.used |= saved
		value := c.swapIndexConstant(call.Args[1], map[syntax.Expr]bool{})
		if indexType != Int || value == nil || value.Kind() != constant.Int || effects != 0 {
			fail(call.Args[1].Position(), "test.SwapAt index must be a pure compile-time Int constant")
			c.expr(call.Args[2])
			return Invalid
		}
		n, exact := constant.Int64Val(value)
		if !exact || n < 0 || n >= int64(len(tuple.Fields)) {
			fail(call.Args[1].Position(), "test.SwapAt index must be in [0, %d), found %s", len(tuple.Fields), value)
			c.expr(call.Args[2])
			return Invalid
		}
		index = int(n)
		want = tuple.Fields[index].Type
	}
	replacement := call.Args[arity-1]
	replacementType := c.exprWantRaw(replacement, want)
	if replacementType == Invalid {
		return Invalid
	}
	if arity == 2 {
		var indices []int
		for i, field := range tuple.Fields {
			if identical(replacementType, field.Type) {
				indices = append(indices, i)
			}
		}
		if len(indices) == 0 {
			fail(replacement.Position(), "test.Swap has no element of exact type %s in %s; build a tuple literal to change element types", replacementType, tuple)
			return Invalid
		}
		if len(indices) > 1 {
			fail(replacement.Position(), "test.Swap matches indices %v; use test.SwapAt to select one position", indices)
			return Invalid
		}
		index = indices[0]
	} else if !identical(replacementType, want) {
		fail(replacement.Position(), "test.SwapAt slot %d requires exact type %s, found %s", index, want, replacementType)
		return Invalid
	}
	// Reuse the conversion expansion machinery: checked source expressions are
	// evaluated once, retain their lexical identities and feed ordinary facts,
	// lifetime checking and code generation.
	c.info.conversionInputs[call.Args[0]] = true
	c.info.conversionInputs[replacement] = true
	c.conversionSerial++
	prefix := fmt.Sprintf("_swap%d_", c.conversionSerial)
	id := func(name string) *syntax.Ident { return &syntax.Ident{Pos: call.Pos, Name: prefix + name} }
	sourceName := prefix + "tuple"
	if len(tuple.Fields) == 1 {
		sourceName = "_"
	}
	source := &syntax.Binding{Pos: call.Pos, Name: sourceName, Value: call.Args[0]}
	value := &syntax.Binding{Pos: replacement.Position(), Name: prefix + "replacement", Value: replacement}
	c.info.assemblyNames[source], c.info.assemblyNames[value] = "swap tuple", "swap replacement"
	result := &syntax.TupleLit{Pos: call.Pos, End: call.End}
	for i := range tuple.Fields {
		var elem syntax.Expr = &syntax.Selector{Pos: call.Pos, X: id("tuple"), Name: fmt.Sprint(i)}
		if i == index {
			elem = id("replacement")
		}
		result.Elems = append(result.Elems, elem)
	}
	statements := []syntax.Stmt{source}
	if arity == 3 {
		// Retain the pure index expression's reads in lowering. Otherwise a local
		// constant used only for shape selection becomes an unused Go variable.
		c.info.conversionInputs[call.Args[1]] = true
		statements = append(statements, &syntax.Binding{Pos: call.Args[1].Position(), Name: "_", Value: call.Args[1]})
	}
	statements = append(statements, value)
	block := &syntax.Block{Pos: call.Pos, End: call.End, Stmts: statements, Tail: result}
	checked := c.exprWant(block, tuple)
	c.info.conversionCalls[call] = block
	c.info.callFuncs[call] = fn
	c.info.callArgs[call] = call.Args
	params := []Type{tuple, replacementType}
	if arity == 3 {
		params = []Type{tuple, Int, replacementType}
	}
	c.info.instances[call] = &Instance{Func: fn, TypeArgs: []Type{tuple, replacementType}, Params: params, Result: tuple}
	if checked == Invalid {
		return Invalid
	}
	c.record(result, tuple)
	return c.record(block, tuple)
}

// Shape selection precedes general comptime execution. Fold checked constants
// and immutable aliases here; a comptime block qualifies only when every part
// is already constant, so no recipe or effectful code is skipped.
func (c *checker) swapIndexConstant(x syntax.Expr, seen map[syntax.Expr]bool) (result constant.Value) {
	defer func() {
		// Alias expressions execute with Int width. Reject overflowing constant
		// intermediates rather than select a slot using unlimited arithmetic.
		if result != nil && result.Kind() == constant.Int {
			if _, exact := constant.Int64Val(result); !exact {
				result = nil
			}
		}
	}()
	if seen[x] {
		return nil
	}
	seen[x] = true
	defer delete(seen, x)
	if value := c.info.constantOf(x); value != nil {
		return value
	}
	literal := func(x syntax.Expr) syntax.Expr {
		value := c.swapIndexConstant(x, seen)
		if value == nil || value.Kind() != constant.Int {
			return nil
		}
		return &syntax.IntLit{Pos: x.Position(), Text: value.ExactString()}
	}
	switch x := x.(type) {
	case *syntax.Ident:
		if binding, ok := c.info.defs[x].(*syntax.Binding); ok && !binding.Lazy && binding.AsyncScope == nil {
			return c.swapIndexConstant(binding.Value, seen)
		}
	case *syntax.Unary:
		if value := literal(x.X); value != nil {
			return constValue(&syntax.Unary{Pos: x.Pos, Op: x.Op, X: value})
		}
	case *syntax.Binary:
		left, right := literal(x.X), literal(x.Y)
		if left != nil && right != nil {
			return constValue(&syntax.Binary{Pos: x.Pos, Op: x.Op, X: left, Y: right})
		}
	case *syntax.Comptime:
		for _, statement := range x.Body.Stmts {
			binding, ok := statement.(*syntax.Binding)
			if !ok || binding.Lazy || binding.AsyncScope != nil || c.swapIndexConstant(binding.Value, seen) == nil {
				return nil
			}
		}
		if x.Body.Tail != nil {
			return c.swapIndexConstant(x.Body.Tail, seen)
		}
	}
	return nil
}
