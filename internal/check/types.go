// Package check type-checks a parsed bork package.
package check

// Type is a bork type as the checker models it. Constraints (`where`)
// are not modelled yet; they arrive with facts (milestone M1).
type Type interface {
	String() string
}

// Basic is a built-in type.
type Basic struct{ name string }

func (b *Basic) String() string { return b.name }

var (
	Int    Type = &Basic{"Int"}
	Bool   Type = &Basic{"Bool"}
	String Type = &Basic{"String"}
	// Unit is the type of expressions that produce no meaningful value.
	Unit Type = &Basic{"Unit"}
	// Never is the type of expressions that never finish normally, such
	// as `return`. It can be used wherever any type is expected.
	Never Type = &Basic{"Never"}
	// Invalid marks an expression that already failed to type-check, to
	// avoid cascades of follow-up errors.
	Invalid Type = &Basic{"invalid"}
)

var namedTypes = map[string]Type{
	"Int":    Int,
	"Bool":   Bool,
	"String": String,
	"Unit":   Unit,
}

// assignable reports whether a value of type src can be used where dst
// is expected.
func assignable(src, dst Type) bool {
	return src == dst || src == Never || src == Invalid || dst == Invalid
}

// isValue reports whether t is a type of real values (not Unit, Never,
// or Invalid).
func isValue(t Type) bool {
	return t != Unit && t != Never && t != Invalid
}
