# Derivation templates

A class's package can declare how to derive its instances with `derive instance`.
The template has one target parameter and implements the class's methods.
Each request expands the template for its target, then checks the resulting
Bork code with the ordinary type, effect, fact and lifetime rules.

## Your first template

```bork
import "bork/shape"

class Labels[T] {
  fn labels(x: T): List[String]
}

derive instance labels[T]: Labels[T] {
  fn labels(x: T): List[String] {
    [comptime for (field in shape.fields[T]()) comptime if (!field.computed) field.name]
  }
}

type Item = { name: String, count: Int } derive (Labels)
type Box[A] = { value: A }
derive Labels for Box

fn main() {
  println(labels(Item { name: "one", count: 1 }))
  println(labels(Box { value: true }))
}
```

Inline `derive (...)` on a fact alias and standalone `derive Class for Alias`
retain the alias's whole-value facts in the instance head. A derived decoder
checks those facts before returning a value of the alias.

`comptime for` expands one body per descriptor. Each field has its own type,
so `field.Type` can appear in annotations and type arguments. The list form
collects one expression per field, and its optional `comptime if` filters fields.
An empty sequence produces an empty list. Ordinary `for` and `if` retain their
runtime meaning; the prefix states which stage should select or repeat code.
A runtime loop over descriptors reports an error offering to add `comptime`.

## A small record encoder

This class turns a record into `name=value` lines. It delegates each stored
field to `Show`; the compiler infers those dictionary requirements for generic
records. Computed fields are omitted. The staged kind check rejects targets
that are not records.

```bork
import "bork/shape"

class Lines[T] { fn lines(value: T): String }

derive instance lines[T]: Lines[T] {
  fn lines(value: T): String {
    comptime if (shape.kind[T]() == shape.Record) {
      parts: List[String] = [comptime for (field in shape.fields[T]()) comptime if (!field.computed)
        field.name + "=" + toString(field.read(value))]
      parts.join("\n")
    } else {
      shape.fail("Lines requires a record")
    }
  }
}

type Item = { name: String, count: Int } derive (Lines)

fn main() {
  println(lines(Item { name: "tea", count: 2 }))
}
```

The program prints:

```text
name=tea
count=2
```

String formatting comes from `Show`; this is a text format, without JSON
escaping. For structured serialization use [bork/codec](../std/codec.md).
To see the rejection, request the same template for a sealed type:

```bork fails
import "bork/shape"

class Lines[T] { fn lines(value: T): String }

derive instance lines[T]: Lines[T] {
  fn lines(value: T): String {
    comptime if (shape.kind[T]() == shape.Record) {
      parts: List[String] = [comptime for (field in shape.fields[T]()) comptime if (!field.computed)
        field.name + "=" + toString(field.read(value))]
      parts.join("\n")
    } else {
      shape.fail("Lines requires a record")
    }
  }
}

type Choice = sealed { Yes, No } derive (Lines)
```

```text
Lines requires a record
```

## Advanced template reference

The sections below explain how `bork/shape` descriptors participate in expansion
and validation. See the [typed derivation API reference](../std/shape.md)
for descriptor signatures, builders, and validation rules.

### Fields, facts and defaults

`shape.fields[T]()` returns record fields in declaration order. A field exposes
`name`, `positional`, `index`, `doc`, `computed`, `hasDefault`, and `tags`, the
field's declared `go { name: "value" }` tags in order as a `List[shape.Tag]`.
Package tag records are checked separately: `field.tagged[M]()` and
`variant.tagged[M]()` return `Option[M]`, while `shape.tagged[T, M]()` reads a type
group. `field.tagGroups` enumerates checked package groups during expansion, with
canonical package paths, concrete `Type` and `value()`. See
[typed package tags](types.md#typed-package-tags) and the
[shape API](../std/shape.md#fields-facts-and-tags).

`field.read(value)` expands
into a checked field read; the value must have the descriptor's owner type.
The projected type retains independent field facts. Sibling constraints remain
on the descriptor and complete owner proof, since their arguments require that
owner value. Descriptor values stay within
template expansion and cannot escape into runtime results.

`field.facts`, `variant.facts`, and `shape.facts[T]()` expose ordered obligation
descriptors. A fact has display `text`, a nested traversal `path`, and an
`independent` flag. The flag is true only for a stored-field obligation that
does not depend on siblings; computed-field and whole-value obligations require
the complete value. Fact handles retain resolved predicate and argument
identities. Their display strings do not validate values or establish proofs.
The target sequence includes declared invariants and constrained head facts.

`field.default()` supplies the declared stored-field default with its checked
type and lexical references. The provider runs at runtime when called;
inspecting `hasDefault` does not call it. Test `hasDefault` before using the
provider, and exclude computed fields: those require the complete owner and
are available through `field.read(value)`.

### Sealed variants

`shape.variants[T]()` describes sealed alternatives, with `name`, `doc`, `index`,
and `fields`, plus a `positional` payload flag. Variant `doc` joins consecutive
`//` lines immediately above the variant with newlines; it is empty when no
documentation is present. Positional fields expose their slot
`index`, `positional: true`, and an empty `name`; internal numeric labels are
never object keys. `variant.project(value)` checks the tag and returns an `Option`
containing a read-only payload view. It evaluates the input once per projection.
`variant.Type` supplies that view's type for annotations and helper arguments.
A payload field's `read` requires the matching variant's view; the complete
sealed value and other variants' views do not suffice. Source code cannot
construct or update these views. Computed fields read through the proven owner.

### Validated builders

`shape.builder[T]()` creates private immutable storage for a record;
`variant.builder()` does the same for one sealed payload. A builder's `.Type`
projection supplies its ordinary private storage type for annotations and helpers.
The storage contains optional typed inputs and cannot be used as the completed
owner. `state.set(field, value)` returns a new state, rejects mismatched owners,
payloads and erased field types, and evaluates both runtime arguments once.
Computed fields cannot be supplied. The original state remains usable.

For example, a template can build a sequence of typed updates and fold them:

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
type Point = { x: Int, y: Int } derive (Build)
fn main() { println(build[Point](3)) }
```

Each selected field must accept `input`'s type. The template may branch on
metadata to supply different typed expressions for different fields.
`finish()` returns the owner or `shape.ValidationError`. It reports duplicate
inputs, supplies declared defaults for missing stored inputs, and rejects other
missing inputs. Positional slots use paths such as `[0]`; format-specific
envelopes are added by codec libraries. It validates stored facts, initializes computed cells, checks
computed and sibling-dependent facts, then checks variant and owner invariants,
including constrained type arguments. Only success exposes the promised owner.

A validation error contains `path`, `message`, and an optional `obligation`.
Predicate failures retain an opaque source obligation with readable `owner`,
`variant`, `field`, `index`, and `source` metadata. The index identifies the
fact's position in its source group; whole-owner facts have an empty field.
Missing and duplicate inputs have no predicate obligation. This diagnostic
metadata cannot establish a proof or construct an unchecked owner.

### Staged selection and helpers

`shape.kind[T]()` selects `shape.Record`, `shape.Sealed`, or
`shape.Other` in `comptime if` or `comptime match`. `shape.name[T]()` and
`shape.owner[T]()` expose the target's source name and defining package path.
`shape.typeName[T]()` retains type arguments in a display name such as
`Option[List[String]]`; it does not identify a metadata key.
Inspecting a foreign private representation remains an error.
Use `shape.fail("explanation")` in a selected branch to reject an unsupported
target. Its diagnostic points to the derive request and identifies the template
operation.

Template code can call ordinary functions. Dictionary requirements inferred
from those calls become bounds on a generic derived instance; unused fields
create no bounds. A class's template does not automatically require that same
class for every field.

Declare reusable expansion helpers with `derive fn`. They resolve names in
their author's package and can receive descriptors as parameters. Helpers with
runtime arguments specialize into checked ordinary functions; pure metadata
helpers can provide compile-time conditions or scalar values. Declared class
bounds and facts still apply. Helpers cannot contain unsafe Go or be called
from runtime source. The metadata evaluator supports String, Bool and Int literals, bindings, descriptor
properties, sequence `length()` and `isEmpty()` queries, scalar equality,
String concatenation, Boolean operations, staged conditions and matches, and calls to
helpers using these operations. It also folds `toUpper()`, `toLower()` and
`capitalize()` on a compile-time String. Other computations report an unsupported
compile-time value when a staged control needs their result. Expansion has a
shared work and depth limits and does not execute
the native evaluator used by ordinary `comptime { ... }` blocks.

A standalone request follows the [ownership and instance import rules](types.md):
it belongs to the type's or class's package, and other packages import the
resulting instance with `use`. The class name itself is not an instance import.

The [derive_labels example](../../examples/derive_labels/README.md) is runnable.

### Instance metadata

Instances and templates can associate typed metadata with the selected dictionary.
Declare `metadata Type = expression` alongside the methods. The expression is
checked Bork code and runs when queried; constructing the dictionary or calling
an ordinary method does not invoke it. `metadata` remains a contextual identifier.

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
fn main() { println(description[Int]()) }
```

`shape.metadata[T, Class, MetadataType]()` selects the same instance as ordinary
class calls in its scope and returns `None` when that instance has no metadata
for the requested key. It can be called from ordinary runtime code as well as
templates. Keys must be closed resolved types; targets may remain generic.
Callback effects remain part of key identity. Duplicate keys and wrongly typed
initializers are rejected, including keys declared through equivalent aliases.

The standard `codec.Encode` derivation is a source template in `bork/codec`. It omits computed fields, retains named-field
object order, and uses the tagged `values` array for positional payloads.
Tuple encoding uses the same template's array branch; `shape.positional[T]()`
identifies those ordered slots. Implicit tuple dictionaries retain their normal
per-slot instance selection. `codec.Decode` also uses source templates and
validated builders for records, variants and tuples. Runtime tuple predicates
retain caller arguments and predicate callbacks as typed dictionary captures;
the captures participate in lifetime and compile-time dependency checking.
Predicate callbacks must be pure: write `(Int) uses nothing => Bool` for a
callback on an Int slot. Encode does not retain tuple predicate arguments.

## How templates are checked

Each expansion is checked as ordinary bork code. The definition itself is also
checked even when no target requests it: predicate names and arity, concrete
expression types, known effects, and closed dictionary requirements. Effects follow the ordinary rules. A call of a known function or helper
uses what it declares, plus what its arguments to open callback parameters use.
Callback parameters and results written without `uses` are open, in helpers
and template methods alike. A callback must fit a function type written with
`uses`, and one returned as an open result may use only what the open
parameters use. A `comptime if` or `comptime match` branch is not selected for
every target, and a `comptime for` body may expand to no copies, so calls in
them are charged by expansion; callbacks in them must still fit their contexts.
A `comptime { ... }` block may use only `build`. Calls whose callee depends on
the target are left to expansion. Checking an unused definition never runs
`comptime` code or captures `build` or `embed` files.
Known parameter facts participate in dictionary selection. Requirements that
depend on a target's type or predicate callback remain checks of its expansion.

Facts are checked at the definition too, for values that do not depend on the
target. A requirement that fails for every target is reported once, at the
definition, even when no type requests it:

```bork fails
pred positive(n: Int) { n > 0 }
fn need(n: Int where positive): Int { n }
derive fn scaled[T](value: T, n: Int): Int { need(n) }
```

```text
need requires n to be positive, but that is not proven for n (check it first with if (positive(n)) { ... }, or require it: n: Int where positive)
```

Writing `n: Int where positive`, or checking `if (n > 0)` first, fixes it, as in
ordinary code. Code inside a `comptime for` body or a `comptime` branch is
checked as one copy. Anything that could differ between targets is left to the
expansion: values computed from descriptors, field reads or the target, values
chosen by a `comptime` branch, facts that follow a test of such a value, and
facts after a `comptime` copy that may return. Proving a requirement of a
constant, such as `need(0)`, also waits for an expansion, because it runs the
predicate.

## Foreign records

A template that declares `metadata shape.ForeignRecord` gives its class a
generated Go struct and checked conversions, as `GoStruct` does. The compiler
evaluates the metadata expression for each derived record and validates it as
that record's Go layout. The template chooses the order, Go names, tags and
`Option` policy of the stored fields; nothing in the compiler depends on the
class's name. Evaluation supports the layout records `shape.ForeignRecord`,
`shape.ForeignField` and `shape.Tag`, `comptime for` lists, descriptor properties,
literals and String case operations, so a query of the same metadata at runtime
returns the same layout.

```bork
import "bork/shape"

class Row[T] {}
derive instance row[T]: Row[T] {
  metadata shape.ForeignRecord = shape.ForeignRecord {
    fields: [comptime for (field in shape.fields[T]()) comptime if (!field.computed)
      shape.ForeignField {
        slot: field.index,
        name: "Col" + field.name.capitalize(),
        tags: [shape.Tag { name: "db", value: field.name }],
        option: shape.ForeignOption.Reject
    }]
  }
}
type User = { name: String, age: Int = 36 } derive (Row)
fn main() { println(shape.metadata[User, Row, shape.ForeignRecord]()) }
```

`slot` is a field index. The layout must place each stored field once, under a
unique exported Go name. `ForeignOption.Pointer` maps `Option[A]` to `*A` (or to
`A` when its Go type is already nillable); `ForeignOption.Reject` refuses fields
containing an `Option`, including list elements and map keys or values. Class
methods cannot be named `New`, `FromGo`, `ToGo` or `Fields`, since the generated
dictionary uses those names. Derive helpers can build layout values, and
String case operations fold only when they resolve to the prelude's methods. A record has at most one Go layout, and the existing GoStruct
rules apply: no tuples, specializations, resources or fields that depend on
type parameters, and instances must be derived. A mirror keeps its Go struct:
the layout must use its field names, ignoring case, and cannot add tags.
Unsafe Go code reaches the conversions through the class dictionary's `New`,
`FromGo`, `ToGo` and `Fields` members, described in
[Generated Go structs](../std-go.md#generated-go-structs).

## Exhaustive projections

`shape.exhausted[T](value)` closes a staged sequence of sealed projections.
The compiler requires an immutable owner parameter and a preceding unconditional
projection for every declared variant whose `Some` arm cannot continue. A
projection under a runtime condition, or of another value, does not count. The
operation returns `Never`, so complete sealed encoders need no fallback value.

---

Previous: [Typed interpolation](interpolators.md) · Next: [Calling Go](go-interop.md) · [All pages](../README.md#the-language)
