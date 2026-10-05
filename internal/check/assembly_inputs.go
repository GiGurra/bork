package check

import (
	"strconv"

	"github.com/GiGurra/bork/internal/syntax"
)

// providerInputs splices exactly one level of checked tuple values. A tuple
// expression is retained as one input, independently of its provider count.
func (c *checker) providerInputs(args []syntax.Expr) []*assemblyProvider {
	var result []*assemblyProvider
	for evaluation, x := range args {
		// Inline declarations retain assembly's ambient-need behavior. Function
		// values inside tuples have already captured their ordinary needs.
		if id, ok := x.(*syntax.Ident); ok && c.lookup(id.Name) == nil && c.packageBindingNamed(id.Name) == nil {
			if _, found := c.funcNamed(id.Name); found {
				result = append(result, &assemblyProvider{x: x, eval: evaluation})
				continue
			}
		}
		typ := c.expr(x)
		c.info.conversionInputs[x] = true
		tuple, ok := typ.(*Record)
		if !ok || !tuple.Tuple {
			fn, _ := typ.(*FuncType)
			result = append(result, &assemblyProvider{x: x, typ: fn, eval: evaluation})
			continue
		}
		for i, field := range tuple.Fields {
			fn, _ := field.Type.(*FuncType)
			source := c.tupleElementSource(x, i, map[syntax.Expr]bool{})
			p := &assemblyProvider{x: x, typ: fn, eval: evaluation, tuple: x, tupleIndex: i, origin: writtenText(x)}
			if source != nil {
				p.elementPos = source.Position()
				if literal, ok := x.(*syntax.TupleLit); ok {
					p.x = literal.Elems[i]
				}
				if inst := c.providerFunction(source, map[syntax.Expr]bool{}); inst != nil {
					p.fn, p.inst = inst.Func, inst
				}
			}
			result = append(result, p)
		}
	}
	return result
}

// Follow only immutable checked aliases; computed tuples need no declaration
// metadata to splice. This never evaluates an initializer or changes captures.
func (c *checker) tupleElementSource(x syntax.Expr, index int, seen map[syntax.Expr]bool) syntax.Expr {
	if seen[x] {
		return nil
	}
	seen[x] = true
	switch x := x.(type) {
	case *syntax.TupleLit:
		if index < len(x.Elems) {
			return x.Elems[index]
		}
	case *syntax.Call:
		if expanded := c.info.conversionCalls[x]; expanded != nil {
			return c.tupleElementSource(expanded.Tail, index, seen)
		}
	case *syntax.Ident:
		if binding, ok := c.info.defs[x].(*syntax.Binding); ok {
			return c.tupleElementSource(binding.Value, index, seen)
		}
		if value := c.info.tupleBindingValues[c.info.defs[x]]; value != nil {
			return c.tupleElementSource(value, index, seen)
		}
	case *syntax.Selector:
		if slot, err := strconv.Atoi(x.Name); err == nil {
			if value := c.tupleElementSource(x.X, slot, map[syntax.Expr]bool{}); value != nil {
				return c.tupleElementSource(value, index, seen)
			}
		}
	}
	return nil
}

func (c *checker) providerFunction(x syntax.Expr, seen map[syntax.Expr]bool) *Instance {
	if seen[x] {
		return nil
	}
	seen[x] = true
	if inst := c.info.funcRefs[x]; inst != nil {
		return inst
	}
	switch x := x.(type) {
	case *syntax.Ident:
		if binding, ok := c.info.defs[x].(*syntax.Binding); ok {
			return c.providerFunction(binding.Value, seen)
		}
		if value := c.info.tupleBindingValues[c.info.defs[x]]; value != nil {
			return c.providerFunction(value, seen)
		}
	case *syntax.Selector:
		if index, err := strconv.Atoi(x.Name); err == nil {
			if source := c.tupleElementSource(x.X, index, map[syntax.Expr]bool{}); source != nil {
				return c.providerFunction(source, seen)
			}
		}
	}
	return nil
}
