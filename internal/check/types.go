// Package check type-checks a parsed bork package.
package check

import (
	"fmt"
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// Type is a bork type as the checker models it. Constraints (`where`)
// are not modelled yet; they arrive with facts (milestone M1).
type Type interface {
	String() string
}

// Basic is a built-in type.
type Basic struct {
	name string
	kind numKind
	bits int // size of a numeric type
}

type numKind int

const (
	notNumeric numKind = iota
	signedInt
	unsignedInt
	floatNum
)

func (b *Basic) String() string { return b.name }

var (
	Int    Type = &Basic{name: "Int", kind: signedInt, bits: 64}
	Bool   Type = &Basic{name: "Bool"}
	String Type = &Basic{name: "String"}
	// Unit is the type of expressions that produce no meaningful value.
	Unit Type = &Basic{name: "Unit"}
	// Never is the type of expressions that never finish normally, such
	// as `return`. It can be used wherever any type is expected.
	Never Type = &Basic{name: "Never"}
	// Scope is the type of a scope (`scope s { ... }`): resources are
	// opened in a scope, and closed when it closes.
	Scope Type = &Basic{name: "Scope"}
	// Invalid marks an expression that already failed to type-check, to
	// avoid cascades of follow-up errors.
	Invalid Type = &Basic{name: "invalid"}

	// Sized numeric types. Int is Int64, and Float is Float64.
	Int8    Type = &Basic{name: "Int8", kind: signedInt, bits: 8}
	Int16   Type = &Basic{name: "Int16", kind: signedInt, bits: 16}
	Int32   Type = &Basic{name: "Int32", kind: signedInt, bits: 32}
	Uint8   Type = &Basic{name: "Uint8", kind: unsignedInt, bits: 8}
	Uint16  Type = &Basic{name: "Uint16", kind: unsignedInt, bits: 16}
	Uint32  Type = &Basic{name: "Uint32", kind: unsignedInt, bits: 32}
	Uint64  Type = &Basic{name: "Uint64", kind: unsignedInt, bits: 64}
	Float32 Type = &Basic{name: "Float32", kind: floatNum, bits: 32}
	Float   Type = &Basic{name: "Float", kind: floatNum, bits: 64}
)

// NumericTypes lists every numeric type, each once.
var NumericTypes = []Type{Int8, Int16, Int32, Int, Uint8, Uint16, Uint32, Uint64, Float32, Float}

var basicTypes = map[string]Type{
	"Int":    Int,
	"Bool":   Bool,
	"String": String,
	"Unit":   Unit,
	"Scope":  Scope,

	"Int8": Int8, "Int16": Int16, "Int32": Int32, "Int64": Int,
	"Uint8": Uint8, "Uint16": Uint16, "Uint32": Uint32, "Uint64": Uint64,
	"Byte":    Uint8,
	"Rune":    Int32,
	"Float32": Float32, "Float64": Float, "Float": Float,
}

func numKindOf(t Type) numKind {
	if b, ok := t.(*Basic); ok {
		return b.kind
	}
	return notNumeric
}

// IsNumeric reports whether t is one of the number types.
func IsNumeric(t Type) bool { return numKindOf(t) != notNumeric }

// IsInteger reports whether t is a signed or unsigned integer type.
func IsInteger(t Type) bool { k := numKindOf(t); return k == signedInt || k == unsignedInt }

// IsFloat reports whether t is Float32 or Float.
func IsFloat(t Type) bool { return numKindOf(t) == floatNum }

func isUnsigned(t Type) bool { return numKindOf(t) == unsignedInt }

func bitsOf(t Type) int { return t.(*Basic).bits }

// alwaysFits reports whether every value of the numeric type from can
// be converted to the numeric type to without going out of range.
// Integers always fit in floats (large ones are rounded).
func alwaysFits(from, to Type) bool {
	fk, tk := numKindOf(from), numKindOf(to)
	switch {
	case tk == floatNum:
		return true
	case fk == floatNum:
		return false
	case fk == tk:
		return bitsOf(from) <= bitsOf(to)
	case fk == unsignedInt && tk == signedInt:
		return bitsOf(from) < bitsOf(to)
	}
	return false // signed to unsigned: negative values never fit
}

// Resource is a resource type (`type File = resource`): a handle to
// something outside the program that a scope closes. Its values are
// made by `unsafe go` functions, which register the finalizer.
type Resource struct {
	Name    string
	Decl    *syntax.TypeDecl
	Prelude bool
	Pkg     *Package
}

func (r *Resource) String() string { return r.Name }

// Field is a named, typed field of a record or variant.
type Field struct {
	Name string
	Type Type
	// Constraints is the field's where clause.
	Constraints []*Constraint
}

// Record is a named record type: `type User = { name: String }`.
//
// A generic record (`type Pair[A, B] = { first: A, second: B }`) has
// TypeParams; its instances (`Pair[Int, String]`) have Base set to it,
// Args, and Fields with the arguments filled in.
type Record struct {
	Name    string
	Fields  []*Field
	Decl    *syntax.TypeDecl
	Prelude bool     // declared in prelude.bork
	Pkg     *Package // the declaring package

	TypeParams []*TypeParam
	Base       *Record
	Args       []Type
	insts      *instanceSet
}

func (r *Record) String() string { return r.Name + argsString(r.Args) }

// Instance is the generic record r with the given type arguments.
func (r *Record) Instance(args []Type) *Record {
	if sameParams(r.TypeParams, args) {
		return r
	}
	key := argsKey(args)
	if t, ok := r.insts.byKey[key]; ok {
		return t.(*Record)
	}
	inst := &Record{Name: r.Name, Decl: r.Decl, Prelude: r.Prelude, Pkg: r.Pkg, Base: r, Args: args}
	r.insts.byKey[key] = inst
	r.insts.whenResolved(func() {
		inst.Fields = substFields(r.Fields, bindParams(r.TypeParams, args))
	})
	return inst
}

// instanceSet holds a generic type's instances. Instances can be made
// before the generic type's fields are resolved (by a recursive type,
// or a type declared later); their fields are filled in once they are.
type instanceSet struct {
	byKey    map[string]Type
	resolved bool
	pending  []func()
}

func newInstanceSet() *instanceSet { return &instanceSet{byKey: map[string]Type{}} }

func (s *instanceSet) whenResolved(fill func()) {
	if s.resolved {
		fill()
	} else {
		s.pending = append(s.pending, fill)
	}
}

func (s *instanceSet) markResolved() {
	s.resolved = true
	for len(s.pending) > 0 {
		fill := s.pending[0]
		s.pending = s.pending[1:]
		fill()
	}
}

func argsString(args []Type) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.String()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// argsKey identifies type arguments for the instance cache. Unlike
// their names, it tells apart type parameters of different functions
// that happen to have the same name.
func argsKey(args []Type) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = typeKey(a)
	}
	return strings.Join(parts, ",")
}

func typeKey(t Type) string {
	switch t := t.(type) {
	case *TypeParam:
		return fmt.Sprintf("%s#%p", t.Name, t)
	case *List:
		return "List[" + typeKey(t.Elem) + "]"
	case *FuncType:
		return "(" + argsKey(t.Params) + ")=>" + typeKey(t.Result)
	case *Union:
		return "(" + strings.Join(strings.Split(argsKey(t.Members), ","), "|") + ")"
	case *Record, *Sealed:
		return fmt.Sprintf("%p[%s]", genericBaseOrSelf(t), argsKey(TypeArgs(t)))
	}
	return t.String()
}

func genericBaseOrSelf(t Type) Type {
	if b := genericBase(t); b != nil {
		return b
	}
	return t
}

func sameParams(params []*TypeParam, args []Type) bool {
	if len(params) != len(args) {
		return false
	}
	for i, p := range params {
		if args[i] != Type(p) {
			return false
		}
	}
	return true
}

func bindParams(params []*TypeParam, args []Type) map[*TypeParam]Type {
	bound := map[*TypeParam]Type{}
	for i, p := range params {
		bound[p] = args[i]
	}
	return bound
}

func substFields(fields []*Field, bound map[*TypeParam]Type) []*Field {
	out := make([]*Field, len(fields))
	for i, f := range fields {
		out[i] = &Field{Name: f.Name, Type: subst(f.Type, bound), Constraints: f.Constraints}
	}
	return out
}

// TypeArgs is a generic type's type arguments; for the generic type
// itself (inside its own declaration), its parameters.
func TypeArgs(t Type) []Type {
	params := func(ps []*TypeParam) []Type {
		out := make([]Type, len(ps))
		for i, p := range ps {
			out[i] = p
		}
		return out
	}
	switch t := t.(type) {
	case *Record:
		if t.Base != nil {
			return t.Args
		}
		return params(t.TypeParams)
	case *Sealed:
		if t.Base != nil {
			return t.Args
		}
		return params(t.TypeParams)
	}
	return nil
}

// genericBase is the generic type t is an instance of (t itself for the
// generic type), or nil.
func genericBase(t Type) Type {
	switch t := t.(type) {
	case *Record:
		if t.Base != nil {
			return t.Base
		}
		if len(t.TypeParams) > 0 {
			return t
		}
	case *Sealed:
		if t.Base != nil {
			return t.Base
		}
		if len(t.TypeParams) > 0 {
			return t
		}
	}
	return nil
}

// instantiate makes an instance of the generic type base.
func instantiate(base Type, args []Type) Type {
	switch b := base.(type) {
	case *Record:
		return b.Instance(args)
	case *Sealed:
		return b.Instance(args)
	}
	return base
}

func typeParamsOf(t Type) []*TypeParam {
	switch t := t.(type) {
	case *Record:
		return t.TypeParams
	case *Sealed:
		return t.TypeParams
	}
	return nil
}

func (r *Record) Field(name string) *Field { return findField(r.Fields, name) }

// Sealed is a type with a closed set of variants:
// `type Shape = sealed { Circle { radius: Int }, Empty }`.
//
// Like records, sealed types can be generic: the prelude's
// `type Option[T] = sealed { Some { value: T }, None }` is one.
type Sealed struct {
	Name     string
	Variants []*Variant
	Decl     *syntax.TypeDecl
	Prelude  bool     // declared in prelude.bork
	Pkg      *Package // the declaring package

	TypeParams []*TypeParam
	Base       *Sealed
	Args       []Type
	insts      *instanceSet
}

func (s *Sealed) String() string { return s.Name + argsString(s.Args) }

// Instance is the generic sealed type s with the given type arguments.
func (s *Sealed) Instance(args []Type) *Sealed {
	if sameParams(s.TypeParams, args) {
		return s
	}
	key := argsKey(args)
	if t, ok := s.insts.byKey[key]; ok {
		return t.(*Sealed)
	}
	inst := &Sealed{Name: s.Name, Decl: s.Decl, Prelude: s.Prelude, Pkg: s.Pkg, Base: s, Args: args}
	s.insts.byKey[key] = inst
	s.insts.whenResolved(func() {
		bound := bindParams(s.TypeParams, args)
		for _, v := range s.Variants {
			inst.Variants = append(inst.Variants, &Variant{Name: v.Name, Fields: substFields(v.Fields, bound), Parent: inst, Index: v.Index})
		}
	})
	return inst
}

func (s *Sealed) Variant(name string) *Variant {
	for _, v := range s.Variants {
		if v.Name == name {
			return v
		}
	}
	return nil
}

// Variant is one variant of a sealed type. It is not a type of its own:
// constructing a variant produces a value of the sealed type.
type Variant struct {
	Name   string
	Fields []*Field
	Parent *Sealed
	Index  int
}

func (v *Variant) Field(name string) *Field { return findField(v.Fields, name) }

func findField(fields []*Field, name string) *Field {
	for _, f := range fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// TypeParam is a type parameter of a generic function: `T` in
// `fn first[T](xs: List[T]): Option[T]`. Inside the function it stands
// for any type, and callers' arguments decide which.
type TypeParam struct {
	Name string
	Decl *syntax.TypeParam
}

func (t *TypeParam) String() string { return t.Name }

// FuncType is the type of a function value: `(A, B) => C`.
type FuncType struct {
	Params []Type
	Result Type
}

func (f *FuncType) String() string {
	params := make([]string, len(f.Params))
	for i, p := range f.Params {
		params[i] = p.String()
	}
	result := f.Result.String()
	if _, ok := f.Result.(*Union); ok {
		result = "(" + result + ")"
	}
	return "(" + strings.Join(params, ", ") + ") => " + result
}

// List is the built-in immutable list type List[T].
type List struct {
	Elem Type
}

func (l *List) String() string { return "List[" + l.Elem.String() + "]" }

// Union is `A | B | ...`. Members are kept in written order, because
// `?` keeps the leftmost member. Members are never unions themselves
// (nested unions are flattened) and never repeat.
type Union struct {
	Members []Type
}

func (u *Union) String() string {
	parts := make([]string, len(u.Members))
	for i, m := range u.Members {
		parts[i] = m.String()
		if _, ok := m.(*FuncType); ok {
			parts[i] = "(" + parts[i] + ")"
		}
	}
	return strings.Join(parts, " | ")
}

// newUnion flattens and de-duplicates members. A single remaining
// member is returned as itself.
func newUnion(members []Type) Type {
	var flat []Type
	add := func(t Type) {
		for _, m := range flat {
			if identical(m, t) {
				return
			}
		}
		flat = append(flat, t)
	}
	for _, m := range members {
		if u, ok := m.(*Union); ok {
			for _, mm := range u.Members {
				add(mm)
			}
		} else {
			add(m)
		}
	}
	if len(flat) == 1 {
		return flat[0]
	}
	return &Union{Members: flat}
}

// identical reports whether two types are the same type. Unions are
// compared as sets: `A | B` is the same type as `B | A`.
func identical(a, b Type) bool {
	if a == b {
		return true
	}
	switch a := a.(type) {
	case *List:
		b, ok := b.(*List)
		return ok && identical(a.Elem, b.Elem)
	case *FuncType:
		b, ok := b.(*FuncType)
		if !ok || len(a.Params) != len(b.Params) || !identical(a.Result, b.Result) {
			return false
		}
		for i := range a.Params {
			if !identical(a.Params[i], b.Params[i]) {
				return false
			}
		}
		return true
	case *Record, *Sealed:
		base := genericBase(a)
		if base == nil || base != genericBase(b) {
			return false
		}
		aa, ba := TypeArgs(a), TypeArgs(b)
		for i := range aa {
			if !identical(aa[i], ba[i]) {
				return false
			}
		}
		return true
	case *Union:
		b, ok := b.(*Union)
		if !ok || len(a.Members) != len(b.Members) {
			return false
		}
		for _, m := range a.Members {
			if !containsMember(b, m) {
				return false
			}
		}
		return true
	}
	return false
}

func containsMember(u *Union, t Type) bool {
	for _, m := range u.Members {
		if identical(m, t) {
			return true
		}
	}
	return false
}

// Identical is identical for other packages.
func Identical(a, b Type) bool { return identical(a, b) }

// assignable reports whether a value of type src can be used where dst
// is expected: the same type, Never, a member of a union, or a union
// whose members all fit.
func assignable(src, dst Type) bool {
	if src == Never || src == Invalid || dst == Invalid || identical(src, dst) {
		return true
	}
	du, ok := dst.(*Union)
	if !ok {
		return false
	}
	if su, ok := src.(*Union); ok {
		for _, m := range su.Members {
			if !containsMember(du, m) {
				return false
			}
		}
		return true
	}
	return containsMember(du, src)
}

// isValue reports whether t is a type of real values (not Unit, Never,
// or Invalid).
func isValue(t Type) bool {
	return t != Unit && t != Never && t != Invalid
}

// comparable reports whether values of type t can be compared with ==.
// Lists, functions, and values of a type parameter cannot (nor records,
// variants, or unions that may hold them).
func comparable(t Type) bool {
	return comparableIn(t, map[Type]bool{})
}

func comparableIn(t Type, seen map[Type]bool) bool {
	if t == Scope {
		return false
	}
	if seen[t] {
		return true
	}
	seen[t] = true
	switch t := t.(type) {
	case *List, *FuncType, *TypeParam, *Resource:
		return false
	case *Record:
		for _, f := range t.Fields {
			if !comparableIn(f.Type, seen) {
				return false
			}
		}
	case *Sealed:
		for _, a := range t.Args {
			if !comparableIn(a, seen) {
				return false
			}
		}
		for _, v := range t.Variants {
			for _, f := range v.Fields {
				if !comparableIn(f.Type, seen) {
					return false
				}
			}
		}
	case *Union:
		for _, m := range t.Members {
			if !comparableIn(m, seen) {
				return false
			}
		}
	}
	return true
}

// IsOption reports whether t is an Option[T] (the prelude's).
func IsOption(t Type) bool {
	s, ok := t.(*Sealed)
	return ok && s.Prelude && s.Name == "Option" && s.Base != nil
}
