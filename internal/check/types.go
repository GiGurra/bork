// Package check type-checks a parsed bork package.
package check

import (
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

// Field is a named, typed field of a record or variant.
type Field struct {
	Name string
	Type Type
	// Constraints is the field's where clause.
	Constraints []*Constraint
}

// Record is a named record type: `type User = { name: String }`.
type Record struct {
	Name    string
	Fields  []*Field
	Decl    *syntax.TypeDecl
	Prelude bool // declared in prelude.bork
}

func (r *Record) String() string { return r.Name }

func (r *Record) Field(name string) *Field { return findField(r.Fields, name) }

// Sealed is a type with a closed set of variants:
// `type Shape = sealed { Circle { radius: Int }, Empty }`.
// Option[T] is a built-in sealed type with one type argument.
type Sealed struct {
	Name     string
	Args     []Type // type arguments, for Option[T]
	Variants []*Variant
	Decl     *syntax.TypeDecl // nil for built-in types
	Prelude  bool             // declared in prelude.bork
}

func (s *Sealed) String() string {
	if len(s.Args) == 0 {
		return s.Name
	}
	args := make([]string, len(s.Args))
	for i, a := range s.Args {
		args[i] = a.String()
	}
	return s.Name + "[" + strings.Join(args, ", ") + "]"
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
	case *Sealed:
		b, ok := b.(*Sealed)
		if !ok || a.Name != b.Name || a.Decl != b.Decl || len(a.Args) != len(b.Args) {
			return false
		}
		for i := range a.Args {
			if !identical(a.Args[i], b.Args[i]) {
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

// Option returns the built-in sealed type Option[T]:
// `sealed { Some { value: T }, None }`. Instances are compared
// structurally by identical, so each call may return a fresh value.
func Option(elem Type) *Sealed {
	o := &Sealed{Name: "Option", Args: []Type{elem}}
	o.Variants = []*Variant{
		{Name: "Some", Fields: []*Field{{Name: "value", Type: elem}}, Parent: o, Index: 0},
		{Name: "None", Parent: o, Index: 1},
	}
	return o
}

// IsOption reports whether t is an Option[T].
func IsOption(t Type) bool {
	s, ok := t.(*Sealed)
	return ok && s.Decl == nil && s.Name == "Option"
}
