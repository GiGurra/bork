package check

import (
	"strconv"
	"sync"

	"github.com/GiGurra/bork/internal/syntax"
)

// Tuple bases are immutable and shared by arity. Instances use the ordinary
// record traversals for generics, facts, equality and lifetime analysis.
var tupleBases sync.Map

// SubstituteType applies a checked generic signature's arguments for lowering.
func SubstituteType(t Type, params []*TypeParam, args []Type) Type {
	return subst(t, bindParams(params, args))
}

func tupleType(elems []Type) *Record {
	arity := len(elems)
	base, found := tupleBases.Load(arity)
	if !found {
		r := &Record{Tuple: true}
		for i := range elems {
			r.TypeParams = append(r.TypeParams, &TypeParam{Name: "T" + strconv.Itoa(i)})
		}
		base, _ = tupleBases.LoadOrStore(arity, r)
	}
	r := &Record{Tuple: true, Base: base.(*Record), Args: append([]Type(nil), elems...)}
	for i, elem := range elems {
		r.Fields = append(r.Fields, &Field{Name: strconv.Itoa(i), Type: elem})
	}
	return r
}

func (c *checker) tupleLit(e *syntax.TupleLit, want Type) Type {
	expected, ok := want.(*Record)
	if ok && expected.Tuple && len(expected.Fields) != len(e.Elems) {
		c.errorf(e.Pos, "tuple has %d elements, expected %d", len(e.Elems), len(expected.Fields))
		return Invalid
	}
	elems := make([]Type, len(e.Elems))
	check := func(i int, elem syntax.Expr) {
		var elemWant Type
		if ok && expected.Tuple {
			elemWant = c.zonk(expected.Fields[i].Type)
		}
		elems[i] = c.exprWant(elem, elemWant)
		if elemWant != nil {
			c.solve(elemWant, elems[i])
		}
	}
	// As with call arguments, infer ordinary elements before lambdas that
	// need context. Another position may determine a shared type parameter.
	for i, elem := range e.Elems {
		if !c.needsContext(elem) {
			check(i, elem)
		}
	}
	for i, elem := range e.Elems {
		if c.needsContext(elem) {
			check(i, elem)
		}
		if elems[i] == Invalid {
			return Invalid
		}
		if !isValue(elems[i]) {
			c.errorf(elem.Position(), "a tuple cannot hold %s", elems[i])
			return Invalid
		}
		if ok && expected.Tuple {
			actual, target := c.settle(elems[i], expected.Fields[i].Type)
			if assignable(actual, target) {
				elems[i] = target
			}
		}
	}
	return tupleType(elems)
}

func (c *checker) tupleBinding(s *syntax.TupleBinding) Type {
	t := c.expr(s.Value)
	if t == Never {
		return Never
	}
	if t == Invalid {
		return Ok
	}
	names := map[string]bool{}
	var valid func(syntax.Pattern, Type) bool
	valid = func(p syntax.Pattern, t Type) bool {
		switch p := p.(type) {
		case *syntax.WildcardPat:
			return true
		case *syntax.TuplePat:
			rec, ok := t.(*Record)
			if !ok || !rec.Tuple {
				c.errorf(p.Pos, "tuple binding requires a tuple value, found %s", t)
				return false
			}
			if len(rec.Fields) != len(p.Elems) {
				// The pattern checker reports the arity mismatch.
				return true
			}
			for i, elem := range p.Elems {
				if !valid(elem, rec.Fields[i].Type) {
					return false
				}
			}
			return true
		case *syntax.VariantPat:
			if len(p.Path) == 1 && !p.Context && !p.Braces && c.typeNamed(p.Path[0]) == nil {
				name := p.Path[0]
				if names[name] {
					c.errorf(p.Pos, "tuple binding repeats name %s", name)
					return false
				}
				names[name] = true
				return true
			}
		}
		c.errorf(p.Position(), "tuple binding requires names, wildcards or nested tuple patterns")
		return false
	}
	if !valid(s.Pattern, t) {
		return Ok
	}
	saved := c.tupleBindingMode
	c.tupleBindingMode = true
	pat := c.pattern(s.Pattern, t)
	c.tupleBindingMode = saved
	c.info.tuplePats[s] = pat
	return Ok
}

func (l *lowerer) tupleBinding(s *syntax.TupleBinding) []Stmt {
	value := l.expr(s.Value)
	l.tuples++
	temp := &Var{Name: "_tuple", GoName: "_tuple" + strconv.Itoa(l.tuples), Pos: s.Pos, Type: value.Type(), Kind: VarLet}
	let := &Let{Pos: s.Pos, Var: temp, Value: value}
	temp.Let = let
	var out []Stmt
	out = append(out, let)
	var bind func(*Pat, Expr)
	bind = func(p *Pat, value Expr) {
		if p.Bind != "" {
			v := &Var{Name: p.Bind, Pos: bindPos(p.bindNode), Type: p.BindType, Kind: VarLet, Unused: l.info.unused[p.bindNode]}
			l.nameRebinding(v, p.bindNode)
			binding := &Let{Pos: v.Pos, Var: v, Value: value}
			v.Let = binding
			p.Var = v
			l.vars[p.bindNode] = v
			out = append(out, binding)
		}
		for _, field := range p.Fields {
			rec := value.Type().(*Record)
			f := rec.Field(field.Name)
			bind(field.Pat, &Select{expr: expr{pos: s.Pos, typ: f.Type}, X: value, Name: f.Name, Field: f})
		}
	}
	bind(l.info.tuplePats[s], &VarRef{expr: expr{pos: s.Pos, typ: temp.Type}, Var: temp})
	if len(out) == 1 {
		temp.Unused = true
	}
	return out
}

func tupleConstraints(tuple *Record) []*Constraint {
	var out []*Constraint
	for _, field := range tuple.Fields {
		for _, con := range field.Constraints {
			copy := *con
			copy.Path = "." + field.Name + con.Path
			out = append(out, &copy)
		}
	}
	return out
}
