package check

import "github.com/GiGurra/bork/internal/syntax"

// expandConstructor reuses ordinary function signatures and literal lowering.
// Only the record owner can deliberately export this construction API.
func (c *checker) expandConstructor(source *syntax.FuncDecl) *syntax.FuncDecl {
	decl := *source
	fd := &decl
	target := fd.Constructor
	entry := c.lookupType(target.Name)
	if entry == nil || entry.decl.Kind != syntax.RecordType {
		c.diags.AddCode(target.Pos, "construction.constructor_target", "generated constructor %s requires a declared record, found %s", fd.Name, target.Name)
		return nil
	}
	if entry.pkg != c.pkg {
		c.diags.AddCode(target.Pos, "construction.constructor_owner", "only package %s can declare a generated constructor for %s", entry.pkg.Path, target.Name)
		return nil
	}
	td := entry.decl
	fd.TypeParams = td.TypeParams
	fd.Params = nil
	fd.Result = &syntax.TypeExpr{Pos: fd.Pos, Name: td.Name}
	for _, param := range td.TypeParams {
		fd.Result.Args = append(fd.Result.Args, &syntax.TypeExpr{Pos: fd.Pos, Name: param.Name})
	}
	lit := &syntax.RecordLit{Type: &syntax.TypeHead{Type: fd.Result, End: fd.Pos}, End: fd.Pos}
	for _, field := range td.Fields {
		fd.Params = append(fd.Params, &syntax.Param{Pos: fd.Pos, Name: field.Name, Type: constructorType(field.Type), Default: field.Default})
		lit.Fields = append(lit.Fields, &syntax.FieldInit{Pos: fd.Pos, Name: field.Name, Value: &syntax.Ident{Pos: fd.Pos, Name: field.Name}})
	}
	fd.Body = &syntax.Block{Pos: fd.Pos, End: fd.Pos, Tail: lit}
	return fd
}

// Written types are keyed by syntax identity. Generated parameters need their
// own copies so generic function resolution cannot replace the record's types.
func constructorType(t *syntax.TypeExpr) *syntax.TypeExpr {
	if t == nil {
		return nil
	}
	out := *t
	out.Args = nil
	for _, arg := range t.Args {
		out.Args = append(out.Args, constructorType(arg))
	}
	if t.Union != nil {
		out.Union = make([]*syntax.TypeExpr, len(t.Union))
		for i, member := range t.Union {
			out.Union[i] = constructorType(member)
		}
	}
	if t.Func != nil {
		fn := *t.Func
		fn.Params = nil
		for _, param := range t.Func.Params {
			fn.Params = append(fn.Params, constructorType(param))
		}
		fn.Result = constructorType(fn.Result)
		out.Func = &fn
	}
	return &out
}

func constructorInvariant(fn *Func) bool {
	return fn.Decl.Constructor != nil && len(TypeConstraints(fn.Result)) != 0
}

func (f *factChecker) constructorObligations(call *Call, e env) {
	if call.Func.Decl.Constructor == nil {
		return
	}
	record, ok := call.Type().(*Record)
	if !ok || len(call.Args) != len(record.Fields) {
		return
	}
	candidate := &RecordLit{expr: expr{pos: call.Pos(), typ: record}, Record: record}
	for i, field := range record.Fields {
		candidate.Fields = append(candidate.Fields, &FieldValue{Name: field.Name, Field: field, Value: call.Args[i]})
	}
	f.nominalObligationsFor(candidate, nil, e, call.Func.QualifiedName(f.from())+" requires its completed "+TypeText(record, f.from())+" to be ")
}
