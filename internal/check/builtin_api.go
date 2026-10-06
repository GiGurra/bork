package check

import (
	"sort"
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// PreludeVisible reports which prelude names belong to the user-facing API.
// Compiler helpers remain available to lowered expressions and prelude code.
func PreludeVisible(name string) bool {
	return name != "" && !strings.HasPrefix(name, "_") && !strings.HasPrefix(name, "compiler") && name != "SelectHandle" && name != "TaskHandle" && name != "ChannelHandle"
}

// BuiltinAPI includes both checked prelude declarations and compiler primitives.
func BuiltinAPI(info *Info, files []*syntax.File) *PackageAPI {
	pkg := queryChecker(info, nil).preludePkg
	if pkg == nil {
		return nil
	}
	out := packageAPI(pkg, files, "builtin", true)
	out.Unsafe = false
	out.Documentation = "Built-ins are available in every Bork package without an import. Compiler-provided signatures use value for any value type and values... for zero or more independently typed arguments. Conversion results and assembly effects depend on their inputs."
	for name, typ := range basicTypes {
		text := "type " + name
		doc := "A compiler-provided type."
		if name != typ.String() {
			text += " = " + typ.String()
			doc = "An alias for " + typ.String() + "."
		}
		out.Declarations = append(out.Declarations, APIDeclaration{Kind: "type", Name: name, Signature: text, Documentation: doc})
	}
	out.Declarations = append(out.Declarations, []APIDeclaration{
		{Kind: "type", Name: "List", Signature: "type List[T]", Documentation: "An immutable ordered collection; its methods return new lists."},
		{Kind: "type", Name: "Map", Signature: "type Map[K, V]", Documentation: "An immutable map from keys to values; its methods return new maps."},
		{Kind: "type", Name: "Seq", Signature: "type Seq[T] uses effects", Documentation: "A lazy sequence. Iteration charges its declared effects; omitted effects are inferred from context."},
		{Kind: "type", Name: "Never", Signature: "type Never", Documentation: "The result type of an expression that does not return."},
	}...)
	out.Declarations = append(out.Declarations, CompilerBuiltinDeclarations()...)
	sort.Slice(out.Declarations, func(i, j int) bool {
		a, b := out.Declarations[i], out.Declarations[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Receiver != b.Receiver {
			return a.Receiver < b.Receiver
		}
		return a.Name < b.Name
	})
	return out
}

// CompilerBuiltinDeclarations describes functions whose signatures cannot be
// written as ordinary prelude declarations. "value" means any value type;
// "values..." permits zero or more independently typed value arguments.
func CompilerBuiltinDeclarations() []APIDeclaration {
	var out []APIDeclaration
	for name, kind := range builtins {
		if !PreludeVisible(name) {
			continue
		}
		var signature, doc string
		switch kind {
		case BuiltinPrintln, BuiltinEprintln:
			signature = "fn " + name + "(values...) uses io: Ok"
			destination := "standard output"
			if kind == BuiltinEprintln {
				destination = "standard error"
			}
			doc = "Prints values separated by spaces and followed by a newline to " + destination + ". Uses each value's Show instance when available."
		case BuiltinToString:
			signature, doc = "fn toString(value): String", "Renders a value, using its Show instance when available."
		case BuiltinConvert:
			signature = "fn " + name + "(value): " + TypeText(conversions[name], nil) + " | OutOfRange"
			doc = "Converts a numeric value. Returns only the target type when every source value fits; otherwise returns the target type or OutOfRange. Numeric literals must fit the target type at compile time."
		case BuiltinPanic:
			signature, doc = "fn panic(message: String): Never", "Stops execution with a panic message."
		case BuiltinTodo:
			signature, doc = "fn todo([message: String]): Never", "Marks unfinished code and panics if executed; the message is optional."
		case BuiltinDbg:
			signature, doc = "fn dbg(value): same type as value", "Prints the source location, expression and value to standard error, and returns the value."
		case BuiltinAssert:
			signature, doc = "fn assert(condition: Bool): Ok", "Fails the test or panics when the condition is false. A successful assertion establishes the condition's facts."
		case BuiltinAssertEqual:
			signature, doc = "fn assertEqual(actual, expected): Ok", "Compares compatible values and fails when they differ."
		case BuiltinAssertSnapshot:
			signature, doc = "fn assertSnapshot(value) uses io: Ok", "Compares a rendered value with the test's stored snapshot."
		case BuiltinAssemble:
			result := "T"
			if name == "assembleAll" {
				result = "List[T]"
			}
			signature = "fn " + name + "[T](scope: Scope, providers...): " + result + " | provider errors"
			doc = "T must be a concrete, non-union type. Requires a Scope and one or more provider functions or instances bundles. Effects, ambient requirements and error alternatives follow the selected providers. assembleAll collects every provider of T; assembleRecord assembles each field of T."
		}
		out = append(out, APIDeclaration{Kind: "function", Name: name, Signature: signature, Documentation: doc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
