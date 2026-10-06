package check

import (
	"fmt"
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// Terms describe types without instantiating a runtime type for a target or a
// descriptor projection. Unknown leaves are obligations for typed expansion;
// fixed constructors can already rule out an assignment at definition time.
type deriveTypeTerm struct {
	head      string
	native    Type
	args      []*deriveTypeTerm
	dependent bool
}

type deriveSymbolicTypes struct {
	c        *checker
	metadata *deriveMetadataTypes
	names    map[string]bool
	locals   map[*local]*deriveTypeTerm
}

func deriveTerm(head string, args ...*deriveTypeTerm) *deriveTypeTerm {
	t := &deriveTypeTerm{head: head, args: args}
	for _, arg := range args {
		if arg == nil || arg.dependent {
			t.dependent = true
		}
	}
	return t
}

func (t *deriveTypeTerm) String() string {
	if t == nil {
		return "?"
	}
	if t.native != nil {
		return t.native.String()
	}
	if len(t.args) == 0 {
		return t.head
	}
	parts := make([]string, len(t.args))
	for i, arg := range t.args {
		parts[i] = arg.String()
	}
	return t.head + "[" + strings.Join(parts, ", ") + "]"
}

func deriveNativeTerm(typ Type, bound map[*TypeParam]*deriveTypeTerm) *deriveTypeTerm {
	if typ == nil || typ == Invalid {
		return nil
	}
	if p, ok := typ.(*TypeParam); ok {
		if actual := bound[p]; actual != nil {
			return actual
		}
		return &deriveTypeTerm{head: p.Name, dependent: true}
	}
	var term *deriveTypeTerm
	switch typ := typ.(type) {
	case *List:
		term = deriveTerm("List", deriveNativeTerm(typ.Elem, bound))
	case *Map:
		term = deriveTerm("Map", deriveNativeTerm(typ.Key, bound), deriveNativeTerm(typ.Value, bound))
	case *FuncType:
		var args []*deriveTypeTerm
		for _, p := range typ.Params {
			args = append(args, deriveNativeTerm(p, bound))
		}
		term = deriveTerm("function", append(args, deriveNativeTerm(typ.Result, bound))...)
	case *Union:
		var args []*deriveTypeTerm
		for _, m := range typ.Members {
			args = append(args, deriveNativeTerm(m, bound))
		}
		term = deriveTerm("union", args...)
	case *Record:
		var args []*deriveTypeTerm
		if typ.Tuple {
			for _, f := range typ.Fields {
				args = append(args, deriveNativeTerm(f.Type, bound))
			}
			term = deriveTerm("tuple", args...)
		} else {
			for _, a := range TypeArgs(typ) {
				args = append(args, deriveNativeTerm(a, bound))
			}
			term = deriveTerm(deriveNominalHead(typ), args...)
		}
	case *Sealed:
		var args []*deriveTypeTerm
		for _, a := range TypeArgs(typ) {
			args = append(args, deriveNativeTerm(a, bound))
		}
		term = deriveTerm(deriveNominalHead(typ), args...)
	default:
		term = deriveTerm(typ.String())
	}
	if !term.dependent && !hasTypeParam(typ) {
		term.native = typ
	}
	return term
}

func deriveNominalHead(typ Type) string {
	switch typ := typ.(type) {
	case *Record:
		if typ.Pkg != nil {
			return typ.Pkg.Path + "." + typ.Name
		}
		return typ.Name
	case *Sealed:
		if typ.Pkg != nil {
			return typ.Pkg.Path + "." + typ.Name
		}
		return typ.Name
	}
	return ""
}

func (s *deriveSymbolicTypes) annotation(node *syntax.TypeExpr, names map[string]*deriveTypeTerm) *deriveTypeTerm {
	if node == nil {
		return nil
	}
	if actual, present := names[node.Name]; present {
		return actual
	}
	if s.names[node.Name] {
		return &deriveTypeTerm{head: node.Name, dependent: true}
	}
	if _, member, projection := strings.Cut(node.Name, "."); projection && member == "Type" {
		return &deriveTypeTerm{head: node.Name, dependent: true}
	}
	children := func(nodes []*syntax.TypeExpr) []*deriveTypeTerm {
		result := make([]*deriveTypeTerm, len(nodes))
		for i, node := range nodes {
			result[i] = s.annotation(node, names)
		}
		return result
	}
	if node.Tuple != nil {
		return deriveTerm("tuple", children(node.Tuple)...)
	}
	if node.Union != nil {
		return deriveTerm("union", children(node.Union)...)
	}
	if node.Func != nil {
		return deriveTerm("function", append(children(node.Func.Params), s.annotation(node.Func.Result, names))...)
	}
	switch node.Name {
	case "List", "Map":
		return deriveTerm(node.Name, children(node.Args)...)
	}
	if len(node.Args) > 0 {
		if typ := s.c.typeNamed(node.Name); typ != nil && deriveNominalHead(typ) != "" {
			return deriveTerm(deriveNominalHead(typ), children(node.Args)...)
		}
		return nil
	}
	if deriveConcreteType(node, s.names) {
		return deriveNativeTerm(s.c.resolveType(node), nil)
	}
	return nil
}

// Mismatch means no substitution of dependent leaves can make src assignable
// to dst. An unknown root and union alternatives retain expansion obligations.
func deriveTermMismatch(src, dst *deriveTypeTerm) bool {
	if src == nil || dst == nil {
		return false
	}
	if src.native != nil && dst.native != nil {
		return !assignable(src.native, dst.native)
	}
	if src.native == Never {
		return false
	}
	if src.head == "union" {
		for _, member := range src.args {
			if deriveTermMismatch(member, dst) {
				return true
			}
		}
		return false
	}
	if dst.head == "union" {
		for _, member := range dst.args {
			if !deriveTermMismatch(src, member) {
				return false
			}
		}
		return true
	}
	if src.dependent && len(src.args) == 0 || dst.dependent && len(dst.args) == 0 {
		return false
	}
	if src.head != dst.head || len(src.args) != len(dst.args) {
		return true
	}
	for i, arg := range src.args {
		if deriveTermMismatch(arg, dst.args[i]) {
			return true
		}
	}
	return false
}

func (s *deriveSymbolicTypes) check(expr syntax.Expr, want *deriveTypeTerm) {
	actual := s.expr(expr)
	// The ordinary island checker owns fully concrete comparisons.
	if actual != nil && want != nil && (actual.dependent || want.dependent) && deriveTermMismatch(actual, want) {
		s.c.errorf(expr.Position(), "derive expression must be %s, found %s", want, actual)
	}
}

func (s *deriveSymbolicTypes) expr(expr syntax.Expr) *deriveTypeTerm {
	if expr == nil {
		return nil
	}
	if typ := s.metadata.scalar(expr); typ != nil {
		return deriveNativeTerm(typ, nil)
	}
	switch expr := expr.(type) {
	case *syntax.Ident:
		local := s.c.lookup(expr.Name)
		if term := s.locals[local]; term != nil {
			return term
		}
		if local != nil {
			return deriveNativeTerm(local.typ, nil)
		}
	case *syntax.IntLit, *syntax.FloatLit:
		// Numeric literals require contextual typing, including narrow widths.
		// The ordinary island checker or typed expansion determines their type.
		return nil
	case *syntax.StringLit:
		return deriveNativeTerm(String, nil)
	case *syntax.BoolLit:
		return deriveNativeTerm(Bool, nil)
	case *syntax.RuneLit:
		return deriveNativeTerm(Rune, nil)
	case *syntax.TupleLit:
		var args []*deriveTypeTerm
		for _, elem := range expr.Elems {
			args = append(args, s.expr(elem))
		}
		return deriveTerm("tuple", args...)
	case *syntax.ListLit:
		var elem *deriveTypeTerm
		if len(expr.Elems) > 0 {
			elem = s.expr(expr.Elems[0])
		}
		return deriveTerm("List", elem)
	case *syntax.MapLit:
		var key, value *deriveTypeTerm
		if len(expr.Keys) > 0 {
			key, value = s.expr(expr.Keys[0]), s.expr(expr.Values[0])
		}
		return deriveTerm("Map", key, value)
	case *syntax.Call:
		if selector, ok := expr.Fun.(*syntax.Selector); ok && s.metadata.kind(selector.X) == deriveField && (selector.Name == "read" || selector.Name == "default") {
			return &deriveTypeTerm{head: "field.Type", dependent: true}
		}
		_, result, _ := s.call(expr)
		return result
	}
	return nil
}

func (s *deriveSymbolicTypes) call(call *syntax.Call) ([]*deriveTypeTerm, *deriveTypeTerm, []string) {
	id, ok := call.Fun.(*syntax.Ident)
	if !ok || call.Pipe.File != "" {
		return nil, nil, nil
	}
	if local := s.c.lookup(id.Name); local != nil {
		term := s.expr(id)
		if term != nil && term.head == "function" && len(term.args) > 0 {
			return term.args[:len(term.args)-1], term.args[len(term.args)-1], nil
		}
		return nil, nil, nil
	}
	if helper, pkg := s.c.deriveHelperNamed(s.c.pkg, id.Name); helper != nil {
		bound := map[string]*deriveTypeTerm{}
		for i, p := range helper.TypeParams {
			bound[p.Name] = &deriveTypeTerm{head: p.Name, dependent: true}
			if len(call.TypeArgs) == len(helper.TypeParams) {
				bound[p.Name] = s.annotation(call.TypeArgs[i], nil)
			}
		}
		outer := s.c.pkg
		s.c.pkg = pkg
		defer func() { s.c.pkg = outer }()
		lexical := *s
		lexical.names = nil // Caller formals apply only to the already-resolved actual terms.
		params, names := make([]*deriveTypeTerm, len(helper.Params)), make([]string, len(helper.Params))
		for i, p := range helper.Params {
			params[i], names[i] = lexical.annotation(p.Type, bound), p.Name
		}
		return params, lexical.annotation(helper.Result, bound), names
	}
	fn, found := s.c.funcNamed(id.Name)
	if !found || fn.Pkg != nil && fn.Pkg.Path == "bork/shape" {
		return nil, nil, nil
	}
	bound := map[*TypeParam]*deriveTypeTerm{}
	if len(call.TypeArgs) == len(fn.TypeParams) {
		for i, arg := range call.TypeArgs {
			bound[fn.TypeParams[i]] = s.annotation(arg, nil)
		}
	}
	params, names := make([]*deriveTypeTerm, len(fn.Params)), make([]string, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = deriveNativeTerm(p, bound)
		if i < len(fn.Decl.Params) {
			names[i] = fn.Decl.Params[i].Name
		}
	}
	return params, deriveNativeTerm(fn.Result, bound), names
}

func deriveTermArgument(call *syntax.Call, index int, params []*deriveTypeTerm, names []string) *deriveTypeTerm {
	if index < len(call.Arguments) && call.Arguments[index].Name != "" {
		for i, name := range names {
			if name == call.Arguments[index].Name {
				if i < len(params) {
					return params[i]
				}
				return nil
			}
		}
		return nil
	}
	if index < len(params) {
		return params[index]
	}
	return nil
}

var _ fmt.Stringer = (*deriveTypeTerm)(nil)
