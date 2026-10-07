# bork/shape

`bork/shape` describes types during typed derivation and builds values with checked facts.

```bork
import "bork/shape"

class Build[T] { fn build(input: Int): T | shape.ValidationError }

derive instance build[T]: Build[T] {
  fn build(input: Int): T | shape.ValidationError {
    initial = shape.builder[T]()
    steps: List[(initial.Type) => initial.Type] = [
      comptime for (field in shape.fields[T]())
      comptime if (!field.computed && !field.hasDefault)
      (state: initial.Type) => state.set(field, input)
    ]
    steps.fold(initial, (state, step) => step(state)).finish()
  }
}

pred positive(n: Int) { n > 0 }
type Count = { value: Int where positive } derive (Build)

fn main() {
  println(build[Count](3))
  match (build[Count](0)) {
    error: shape.ValidationError => println(s"rejected field: ${error.path}")
    value: Count => println(value)
  }
}
```

```text
Count { value: 3 }
rejected field: .value
```

The template expands once for each target type. `finish` checks every promised
fact before exposing the completed value. For a tutorial on defining classes
and templates, see [derivation](../language/derivation.md).

## API

Most operations below exist only during derive expansion. They are pure staged
operations; descriptor lists are consumed by `comptime for`, `comptime if`, or
`comptime match`. Their descriptors cannot escape into runtime values.
`metadata` also works in ordinary runtime code.

| Signature or operation | Meaning |
| --- | --- |
| `kind[T](): Kind` | Select `Record`, `Sealed`, or `Other`. |
| `name[T](): String` | Read the target's source name. |
| `typeName[T](): String` | Read its display name with type arguments. |
| `owner[T](): String` | Read its defining package path. |
| `positional[T](): Bool` | Identify a structural tuple's positional slots. |
| `fields[T](): List[Field[T]]` | Read record fields in declaration order. |
| `variants[T](): List[Variant[T]]` | Read sealed alternatives in declaration order. |
| `facts[T](): List[Fact[T]]` | Read invariants and constrained head obligations. |
| `builder[T]()` | Create private immutable record-builder storage. |
| `fail(message: String): Never` | Reject a target at its derive request. |
| `metadata[T, C, M](): Option[M]` | Read M metadata from the selected C instance for T. |
| `exhausted[T](value: T): Never` | Close a certified exhaustive sequence of sealed projections. |

The builder storage type is supplied by expansion and projected as `initial.Type`;
there is no public builder type to import. Inspecting another package's private
representation is rejected. `typeName` is display text, not a metadata key.

## Reject an unsupported target

```bork fails
import "bork/shape"

class Label[T] { fn label(value: T): String }

derive instance label[T]: Label[T] {
  fn label(value: T): String {
    comptime match (shape.kind[T]()) {
      shape.Record => "record"
      _ => shape.fail(shape.name[T]() + ": only records are supported")
    }
  }
}

type Choice = sealed { First, Second } derive (Label)

fn main() {}
```

```text
Choice: only records are supported
```

The compiler reports the error at the derive request, with the template operation
as context. Unsupported staged computations also produce compile-time diagnostics.

## Fields, facts, and tags

| Field descriptor property | Type and meaning |
| --- | --- |
| `name` | `String`: source field name, empty for a positional slot. |
| `positional` | `Bool`: whether this is a positional slot. |
| `index` | `Int`: declaration-order field index. |
| `doc` | `String`: field documentation. |
| `computed` | `Bool`: whether the field is computed. |
| `hasDefault` | `Bool`: whether a stored field declares a default. |
| `tags` | `List[Tag]`: ordered `go` tags; Tag has `name: String`, `value: String`. |
| `tagGroups` | `List[PackageTag[T]]`: checked package tag groups in declaration order; excludes `go` tags. |
| `facts` | `List[Fact[T]]`: ordered resolved obligations. |
| `Type` | Field type retaining independent field facts. |
| `RawType` | Field type without its field facts. |

Package tag descriptors expose `package: String` as the canonical import path,
`Type` as the concrete tag record type, and `value(): tag.Type` as the checked
record value including defaults. They retain lexical references from the tag
declaration. Use `comptime for (tag in field.tagGroups)` to consume them; the
descriptors cannot escape into runtime values. Ordinary generic consumers can
read encoded foreign field tags through `codec.RecordField.tags`.

| Field operation | Result and guarantee |
| --- | --- |
| `field.read(value: T): field.Type` | Checked field read from its owner; retains independent facts. |
| `field.default(): field.Type` | Declared stored default with checked type and lexical references. |
| `field.validate(value: field.RawType): field.Type \| ValidationError` | Checked field value or `ValidationError`. |
| `field.check(value: field.RawType): Ok \| ValidationError` | `Ok \| ValidationError`; also handles a field head equal to the failure type. |

Test `hasDefault` before calling `default`, and exclude computed fields. Reading
metadata does not execute a default; calling its provider runs at runtime.
Computed fields require the complete owner and are read through `read`.
`validate` requires a field head disjoint from `ValidationError`. Heads that
overlap it, including unconstrained root type parameters, are rejected; use
`check` for those fields.

Sibling-dependent constraints require a complete owner; a field-level check
cannot establish them independently.

A `Fact[T]` has `text: String`, `path: String`, and `independent: Bool`.
`independent` is true only for stored-field obligations that do not depend on
siblings; computed and whole-value obligations need the complete value. Handles
retain resolved predicate and argument identities. Display strings are metadata,
not predicates to parse or proof that a value satisfies them.

```bork
import "bork/shape"

class Labels[T] { fn labels(value: T): List[String] }

derive instance labels[T]: Labels[T] {
  fn labels(value: T): List[String] {
    [comptime for (field in shape.fields[T]()) comptime if (!field.computed) field.name]
  }
}

type Item = { name: String, count: Int } derive (Labels)

fn main() {
  println(labels(Item { name: "one", count: 1 }))
}
```

```text
["name", "count"]
```

See the [labels example](../../examples/derive_labels/README.md) for generic targets.

## Sealed alternatives and projections

| Variant property or operation | Meaning |
| --- | --- |
| `name: String`, `index: Int` | Source name and declaration-order index. |
| `doc: String` | Immediately preceding line comments joined with newlines, or empty text. |
| `positional: Bool` | Whether the payload is positional. |
| `fields: List[Field[T]]`, `facts: List[Fact[T]]` | Payload fields and resolved obligations. |
| `variant.Type` | Read-only payload view type supplied by expansion. |
| `variant.project(value: T): Option[variant.Type]` | Check the tag and return an Option payload view. |
| `variant.builder()` | Create private immutable storage for this payload. |

Trailing comments and comments separated from a variant by a blank line do not
document it.

Each projection evaluates its input once. A payload field's `read` requires the
matching variant view, not the complete sealed value or another variant's view.
Views cannot be constructed or updated in source. Positional payload fields have
an empty name and a slot index; numeric internal labels are never object keys.
Computed fields read through the proven owner.

```bork
import "bork/shape"

class VariantName[T] { fn variantName(value: T): String }

derive instance variantNames[T]: VariantName[T] {
  fn variantName(value: T): String {
    comptime for (variant in shape.variants[T]()) {
      match (variant.project(value)) {
        Option.Some(payload) => { _ = payload; return variant.name }
        Option.None => {}
      }
    }
    shape.exhausted[T](value)
  }
}

type Choice = sealed { One { n: Int }, Empty } derive (VariantName)

fn main() {
  println(variantName(Choice.One { n: 1 }))
  println(variantName(Choice.Empty))
}
```

```text
One
Empty
```

`exhausted` accepts an immutable owner parameter only after an unconditional
projection for every declared variant, with each Some arm unable to continue.
Projections under runtime conditions or of other values do not count. Its Never
result allows a complete encoder without an invented fallback.

```bork
import "bork/shape"

class VariantDocs[T] { fn variantDocs(value: T): List[String] }

derive instance variantDocs[T]: VariantDocs[T] {
  fn variantDocs(value: T): List[String] {
    [comptime for (variant in shape.variants[T]()) variant.name + ": " + variant.doc]
  }
}

type Outcome = sealed {
  // A completed operation.
  Done
  // An operation to retry.
  Retry { after: Int }
} derive (VariantDocs)

fn main() {
  println(variantDocs(Outcome.Done))
}
```

```text
["Done: A completed operation.", "Retry: An operation to retry."]
```

## Builder storage and validation

| Builder operation | Meaning |
| --- | --- |
| `state.Type` | Ordinary private storage type for annotations and helper arguments. |
| `state.set(field, value)` | Return a new state containing one typed input. |
| `state.finish(): T \| ValidationError` | Return the complete owner or `ValidationError`. |

Storage holds optional typed inputs; it is not a completed owner. `set` checks
owner and payload identity, rejects erased field types and computed fields, and
evaluates its runtime arguments once. The original storage remains usable.

`finish` detects duplicate inputs, fills missing stored defaults, and rejects
other missing inputs. It validates stored facts, initializes computed cells,
checks computed and sibling-dependent facts, then variant and owner invariants,
including constrained type arguments. Only success exposes the promised type.
Positional paths use `[0]`; codec libraries add format-specific envelopes.

| Diagnostic record | Fields |
| --- | --- |
| `ValidationError` | `path: String`, `message: String`, `obligation: Option[Obligation] = .None` |
| `Obligation` (private) | Readable `owner: String`, `variant: String`, `field: String`, `index: Int`, `source: String` |

Missing and duplicate inputs have no source obligation. Predicate failures retain
its resolved owner and source group index; whole-owner facts have an empty field.
This provenance neither establishes a value proof nor permits unchecked construction.

## Instance metadata

```bork
import "bork/shape"

type Description = { text: String }
class Label[T] { fn label(value: T): String }

instance integerLabel: Label[Int] {
  metadata Description = Description { text: "a whole number" }
  fn label(value: Int): String { toString(value) }
}

fn description[T: Label](): Option[Description] {
  shape.metadata[T, Label, Description]()
}

fn main() {
  println(description[Int]())
}
```

```text
Option.Some(Description { text: "a whole number" })
```

`metadata` selects the same instance as ordinary class calls in its scope. An
absent key returns None. Keys must be closed resolved types, or a
[metadata family](../language/derivation.md#instance-metadata) (`metadata type
Show[T]`) applied to the target; the target may remain generic. Callback effects are part of key identity. Duplicate keys and
wrongly typed initializers are rejected, including equivalent aliases. An
initializer runs when queried, not merely when its dictionary is constructed.

## Foreign record layouts

A template declaring `metadata shape.ForeignRecord` asks the compiler to generate
a Go struct with the requested layout and checked conversions. The expression
uses compile-time layout records, lists, descriptor properties and String case
operations. See [Go interop](../language/go-interop.md) and
[generated Go structs](../std-go.md#generated-go-structs).

| Type | Fields or variants |
| --- | --- |
| `ForeignRecord` | `fields: List[ForeignField]` |
| `ForeignField` | `slot: Int`, `name: String`, `tags: List[Tag]`, `option: ForeignOption` |
| `ForeignOption` | `Pointer \| Reject` |

`slot` selects a stored field index; `name` supplies its exported Go field name.
A layout must include every stored field exactly once and use distinct exported
Go identifiers; the compiler rejects missing/duplicate slots and invalid names.
Pointer maps `Option[A]` to `*A`, or to A when its Go type is already nillable.
Reject refuses fields containing Option, including list elements and map entries.
```bork
import "bork/shape"

class Row[T] {}

derive instance row[T]: Row[T] {
  metadata shape.ForeignRecord = shape.ForeignRecord {
    fields: [comptime for (field in shape.fields[T]()) comptime if (!field.computed)
      shape.ForeignField {
        slot: field.index,
        name: field.name.capitalize(),
        tags: [shape.Tag { name: "db", value: field.name }],
        option: shape.ForeignOption.Reject
      }
    ]
  }
}

type User = { name: String, age: Int = 36 } derive (Row)

fn main() {
  layout = shape.metadata[User, Row, shape.ForeignRecord]()
  println(layout.map(value => value.fields.map(field => field.name)))
}
```

```text
Option.Some(["Name", "Age"])
```

See the [derivation design](../design/derive.md) for rationale and expansion limits.

Run `bork doc bork/shape` for the generated reference.

[All standard packages](README.md)
