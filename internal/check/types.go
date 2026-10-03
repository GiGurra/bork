// Package check type-checks a parsed bork package.
package check

import (
	"fmt"
	"go/types"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
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
	Bytes  Type = &Basic{name: "Bytes"}
	// Unit is the type of expressions that produce no meaningful value.
	Unit Type = &Basic{name: "Unit"}
	// Never is the type of expressions that never finish normally, such
	// as `return`. It can be used wherever any type is expected.
	Never Type = &Basic{name: "Never"}
	// Scope is the type of a scope (`scope s { ... }`): resources are
	// opened in a scope, and closed when it closes.
	Scope Type = &Basic{name: "Scope"}
	// OwnedScope is the type of an owned child scope (openScope): the
	// right to end it, which is passed on, never copied (see owners.go).
	OwnedScope Type = &Basic{name: "OwnedScope"}
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
	"Int":        Int,
	"Bool":       Bool,
	"String":     String,
	"Bytes":      Bytes,
	"Unit":       Unit,
	"Scope":      Scope,
	"OwnedScope": OwnedScope,

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

// AlwaysFits reports whether every value of the numeric type from fits
// the numeric type to.
func AlwaysFits(from, to Type) bool { return alwaysFits(from, to) }

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
type Opaque struct {
	Name   string
	Decl   *syntax.TypeDecl
	Pkg    *Package
	GoType types.Type
}

func (o *Opaque) String() string { return TypeText(o, nil) }

// GoTypeOf is the underlying Go type of an opaque value or Go resource.
func GoTypeOf(t Type) types.Type {
	switch t := t.(type) {
	case *Opaque:
		return t.GoType
	case *Resource:
		return t.GoType
	}
	return nil
}

type Resource struct {
	ContextBinding *Func
	GoType         types.Type
	Name           string
	Decl           *syntax.TypeDecl
	Prelude        bool
	Pkg            *Package
}

func (r *Resource) String() string { return TypeText(r, nil) }

// Field is a named, typed field of a record or variant.
type GoField struct {
	Tag  string
	Path []string
	Type types.Type
}

type Field struct {
	GoTags         []syntax.GoTag
	Decl           *syntax.FieldDecl
	Pkg            *Package
	Prelude        bool
	Doc            string
	Default        Expr
	defaultGeneric bool
	defaultState   int
	defaultUse     diag.Pos
	Name           string
	Type           Type
	// Constraints is the field's where clause.
	Constraints []*Constraint
}

// Record is a named record type: `type User = { name: String }`.
//
// A generic record (`type Pair[A, B] = { first: A, second: B }`) has
// TypeParams; its instances (`Pair[Int, String]`) have Base set to it,
// Args, and Fields with the arguments filled in.
type Record struct {
	GoGenerated  bool
	GoStruct     bool
	GoTo, GoFrom bool
	GoMirror     types.Type
	GoFields     []GoField
	Name         string
	Fields       []*Field
	Decl         *syntax.TypeDecl
	Prelude      bool     // declared in prelude
	Pkg          *Package // the declaring package

	TypeParams []*TypeParam
	Base       *Record
	Args       []Type
	insts      *instanceSet
}

func (r *Record) String() string { return TypeText(r, nil) }

// Instance is the generic record r with the given type arguments.
func (r *Record) Instance(args []Type) *Record {
	if sameParams(r.TypeParams, args) {
		return r
	}
	key := argsKey(args)
	if t, ok := r.insts.byKey[key]; ok {
		return t.(*Record)
	}
	inst := &Record{Name: r.Name, Decl: r.Decl, Prelude: r.Prelude, Pkg: r.Pkg, Base: r, Args: args, GoStruct: r.GoStruct, GoGenerated: r.GoGenerated, GoMirror: r.GoMirror, GoFields: r.GoFields, GoTo: r.GoTo, GoFrom: r.GoFrom}
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
	if s == nil {
		return // an instance (an alias names one) has no instances of its own
	}
	s.resolved = true
	for len(s.pending) > 0 {
		fill := s.pending[0]
		s.pending = s.pending[1:]
		fill()
	}
}

// TypeText renders t as code in package from would write it: the
// types of other packages qualified (money.Cents). With from nil, no
// names are qualified.
func TypeText(t Type, from *Package) string {
	named := func(name string, pkg *Package, args []Type) string {
		if from != nil {
			name = qualify(name, pkg, from)
		}
		return name + argsText(args, from)
	}
	switch t := t.(type) {
	case *Record:
		return named(t.Name, t.Pkg, t.Args)
	case *Sealed:
		return named(t.Name, t.Pkg, t.Args)
	case *Resource:
		return named(t.Name, t.Pkg, nil)
	case *Opaque:
		return named(t.Name, t.Pkg, nil)
	case *List:
		return "List[" + innerText(t.Elem, from) + "]"
	case *Map:
		return "Map[" + innerText(t.Key, from) + ", " + innerText(t.Value, from) + "]"
	case *FuncType:
		return funcText(t, from, false, false)
	case *Union:
		parts := make([]string, len(t.Members))
		for i, m := range t.Members {
			parts[i] = innerText(m, from)
			if _, ok := m.(*FuncType); ok {
				parts[i] = "(" + parts[i] + ")"
			}
		}
		return strings.Join(parts, " | ")
	}
	return t.String()
}

// innerText writes a type inside another (a type argument, an element,
// a union's member), where an open function type cannot be written:
// it is shown as using "open".
func innerText(t Type, from *Package) string {
	if ft, ok := t.(*FuncType); ok {
		return funcText(ft, from, false, true)
	}
	return TypeText(t, from)
}

// funcText writes a function type. pureMark writes `uses nothing` for
// a pure one, as a parameter's function type without uses would read as
// open. showOpen writes the effects of an open one ("uses open"), which
// otherwise reads as written, without uses.
func funcText(t *FuncType, from *Package, pureMark, showOpen bool) string {
	params := make([]string, len(t.Params))
	for i, p := range t.Params {
		if pf, ok := p.(*FuncType); ok {
			params[i] = funcText(pf, from, true, false)
		} else {
			params[i] = TypeText(p, from)
		}
	}
	result := TypeText(t.Result, from)
	if _, ok := t.Result.(*Union); ok {
		result = "(" + result + ")"
	}
	uses := ""
	switch {
	case t.Effects&^EffOpen != 0 || showOpen && t.Effects != 0:
		uses = " uses " + t.Effects.String()
	case t.Effects == 0 && pureMark:
		uses = " uses nothing"
	}
	return "(" + strings.Join(params, ", ") + ")" + uses + " => " + result
}

func argsText(args []Type, from *Package) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = innerText(a, from)
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
	case *Opaque, *Resource:
		return fmt.Sprintf("%p", t)
	case *TypeParam:
		return fmt.Sprintf("%s#%p", t.Name, t)
	case *List:
		return "List[" + typeKey(t.Elem) + "]"
	case *Map:
		return "Map[" + typeKey(t.Key) + "," + typeKey(t.Value) + "]"
	case *FuncType:
		return fmt.Sprintf("(%s)%d=>%s", argsKey(t.Params), t.Effects, typeKey(t.Result))
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
		cp := *f
		cp.Type = subst(f.Type, bound)
		cp.Constraints = substConstraints(f.Constraints, bound)
		cp.Default = nil
		cp.defaultState = 0
		out[i] = &cp
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
	Prelude  bool     // declared in prelude
	Pkg      *Package // the declaring package

	TypeParams []*TypeParam
	Base       *Sealed
	Args       []Type
	insts      *instanceSet
}

func (s *Sealed) String() string { return TypeText(s, nil) }

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
	// Bounds lists the classes the type must have instances of.
	Bounds []*Class
	// unknown is set for a type not known yet in the middle of checking
	// a call, which unification decides (see infer.go).
	unknown bool
}

func (t *TypeParam) String() string { return t.Name }

// FuncType is the type of a function value: `(A, B) => C`.
type FuncType struct {
	Params []Type
	Result Type
	// Effects is what calling the function may do: `(A) uses io => C`.
	Effects Effects
}

func (f *FuncType) String() string { return TypeText(f, nil) }

// List is the built-in immutable list type List[T].
type List struct {
	Elem Type
}

func (l *List) String() string { return TypeText(l, nil) }

// Map is the built-in immutable map type Map[K, V]. It keeps its keys
// in the order they were first added.
type Map struct {
	Key, Value Type
}

func (m *Map) String() string { return TypeText(m, nil) }

// Union is `A | B | ...`. Members are kept in written order, because
// `?` keeps the leftmost member. Members are never unions themselves
// (nested unions are flattened) and never repeat.
type Union struct {
	Members []Type
}

func (u *Union) String() string { return TypeText(u, nil) }

// newUnion flattens and de-duplicates members. A single remaining
// member is returned as itself.
func newUnion(members []Type) Type {
	var flat []Type
	add := func(t Type) {
		for i, m := range flat {
			if identical(m, t) {
				return
			}
			// Function types that differ only in their effects are one
			// member, which may use either's.
			if mf, ok := m.(*FuncType); ok {
				if tf, ok := t.(*FuncType); ok && sameSignature(mf, tf) {
					flat[i] = &FuncType{Params: mf.Params, Result: mf.Result, Effects: mf.Effects | tf.Effects}
					return
				}
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
	case *Map:
		b, ok := b.(*Map)
		return ok && identical(a.Key, b.Key) && identical(a.Value, b.Value)
	case *FuncType:
		b, ok := b.(*FuncType)
		return ok && a.Effects == b.Effects && sameSignature(a, b)
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

// sameSignature reports whether two function types take and give the
// same types, whatever their effects.
func sameSignature(a, b *FuncType) bool {
	if len(a.Params) != len(b.Params) || !identical(a.Result, b.Result) {
		return false
	}
	for i := range a.Params {
		if !identical(a.Params[i], b.Params[i]) {
			return false
		}
	}
	return true
}

// fitsFunc reports whether a function of type src can be used as a
// dst: it takes the same parameters, gives a result that fits, and uses
// no more than dst allows.
func fitsFunc(src, dst *FuncType) bool {
	if src.Effects&^dst.Effects != 0 || len(src.Params) != len(dst.Params) || !fitsResult(src.Result, dst.Result) {
		return false
	}
	for i := range src.Params {
		if !identical(src.Params[i], dst.Params[i]) {
			return false
		}
	}
	return true
}

// fitsResult reports whether a function's result of type src fits one
// of type dst: the same type, or a function that uses less. (Go's
// function types are invariant, so nothing else may differ.)
func fitsResult(src, dst Type) bool {
	if identical(src, dst) {
		return true
	}
	sf, ok1 := src.(*FuncType)
	df, ok2 := dst.(*FuncType)
	return ok1 && ok2 && fitsFunc(sf, df)
}

// Identical is identical for other packages.
func Identical(a, b Type) bool { return identical(a, b) }

// Assignable reports whether a value of type src can be used as a dst.
func Assignable(src, dst Type) bool { return assignable(src, dst) }

// assignable reports whether a value of type src can be used where dst
// is expected: the same type, Never, a member of a union, a union
// whose members all fit, or a function that uses no more than dst
// allows.
func assignable(src, dst Type) bool {
	if src == Never || src == Invalid || dst == Invalid || identical(src, dst) {
		return true
	}
	if sf, ok := src.(*FuncType); ok {
		switch dst := dst.(type) {
		case *FuncType:
			return fitsFunc(sf, dst)
		case *Union:
			for _, m := range dst.Members {
				if mf, ok := m.(*FuncType); ok && fitsFunc(sf, mf) {
					return true
				}
			}
		}
	}
	du, ok := dst.(*Union)
	if !ok {
		return false
	}
	if su, ok := src.(*Union); ok {
		for _, m := range su.Members {
			if !assignable(m, du) {
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
	if t == Scope || t == OwnedScope {
		return false
	}
	if seen[t] {
		return true
	}
	seen[t] = true
	switch t := t.(type) {
	case *List:
		return comparableIn(t.Elem, seen)
	case *Map:
		return comparableIn(t.Key, seen) && comparableIn(t.Value, seen)
	case *TypeParam:
		for _, b := range t.Bounds {
			if IsEq(b) {
				return true
			}
		}
		return false
	case *FuncType, *Resource, *Opaque:
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
