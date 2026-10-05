# Variant ergonomics

Ticket: bork-3ic73j. Deliver context patterns first (PR A), then positional
variant payloads and the Option migration (PR B), after tuples PR 1 lands.
These are general sealed-type features; neither introduces global variant
names or `T?` syntax.

## Context patterns

A match arm may omit a sealed variant's owner: `.None` or
`.Some { value: x }`. Explicit `Option.None` and named-field patterns remain
valid. A nested pattern receives the matched field or element's type as its
context, so `.Some { value: .Ready }` resolves each variant independently.

Use the context-literal uniqueness rules on the scrutinee's checked type:
resolve aliases and select exactly one nominal sealed candidate declaring the
variant. In a union, two specializations are two candidates; no candidate or
multiple candidates produces a diagnostic, never an import search or a choice
based on fields. An unknown variant on a single sealed candidate gets the
existing unknown-variant diagnostic and spelling fix. Check visibility after
selection. Unresolved generic candidates must not silently establish uniqueness.
Qualified patterns continue to disambiguate unions. There is no context record
pattern `.{ ... }` in this change.

The checker records the resolved owner and variant for lowering and editor
queries without rewriting the source syntax. Exhaustiveness and unreachable
arm checking use that same resolved identity: qualified and context spellings
have identical coverage. Existing restrictions on guards and nested patterns
continue to apply.

## Positional payloads

Declarations accept `sealed { Some(T), None }` and
`sealed { Pair(A, B), Named { value: A } }`. A positional variant has a fixed,
nonempty ordered list of payload types; no named fields, defaults, spread,
named arguments, or mixed payload forms. A fieldless variant remains `None`,
not `None()`. Each payload slot may itself be a tuple or constrained type.

Construct with `.Some(x)`, `Option.Some(x)`, or `.Pair(a, b)`; match with
`.Some(x)`, `.Some(_)`, or `.Pair(.Some(x), _)`. Parentheses identify a
positional payload, not a named-field literal or an arbitrary function call.
Arity must match exactly. Context constructors use the existing expected-type
selection; explicit constructors retain generic inference. Check each argument
against its instantiated payload type, preserving constraints and effects.
Nested patterns receive the corresponding instantiated type. Named variants
retain braces; a positional variant rejects braces and a named variant rejects
parentheses. A bare positional variant pattern may ignore its payload, following
the existing bare named-variant pattern rule.

Build on tuples' ordered type/pattern checking and projection machinery, while
retaining the distinction between `One((A, B))` (one tuple-valued slot) and
`Pair(A, B)` (two slots). Keep payload metadata explicit in the syntax and
checked variant model; generated storage names are not source-level fields.
Lower to the existing sealed interface and variant struct representation with
stable internal slot names, evaluate constructor arguments once in source order,
and expose positional slots to matching, facts, equality, Show, compile-time
values, describe and editor queries. Debug mappings retain source positions.
Refutable payload patterns do not cover a whole variant; wildcard/binding
payloads participate in the existing nested-pattern exhaustiveness rules.

## Encode and Decode

Derived encoding for ordinary positional variants uses a tagged object with an
ordered `values` array: `Pair(1, "x")` encodes as
`{"type":"Pair","values":[1,"x"]}`. Even a single payload uses an array.
Decode requires an array of exactly the declared arity and reports element
errors at `.values[index]`; it validates payload and completed-value constraints
just as named variants do. Named variants retain their current tagged objects
with named keys, and fieldless variants retain their current representation.
Internal slot names never appear in the wire format.

Option's existing explicit Encode/Decode instances retain their null/value
representation and missing-field behavior. Changing Some's declaration must
not alter JSON, CSV or GoStruct optional-field compatibility. Coordinate the
`values` representation and tuple codec reuse with the encoding and tuples
workers before PR B.

## Migration and validation

PR A adds context patterns without changing existing programs. PR B changes
the prelude to `type Option[T] = sealed { Some(T), None }`, migrates every old
Some construction and destructuring pattern in prelude, std, examples, tests,
templates and documentation, and updates compiler/runtime assumptions about
the former `value` field. The compiler emits an actionable fix for the old
Option named-field construction and pattern, preserving the supplied expression
or nested pattern. This is migration guidance for the changed prelude API,
not a special syntax or typing rule for Option. Preserve `?`, Option methods,
constraints, Go conversions and codecs, with focused regression coverage.

Follow every row in [the syntax-change checklist](../syntax-changes.md):
parser/AST and recovery; checker/facts/exhaustiveness; generation/debug mapping;
formatter; describe; prelude/std and validator regeneration; compiler-backed
completion, semantic tokens, navigation, rename, signatures, inlay hints and
fixes; playground/templates; TextMate, tree-sitter and native editor grammars,
shared query snapshots and grammar pins; grammar/specification, reader pages
and examples. Coordinate shared parser and tuple-pattern work with tuples,
and source migrations with bindings and encoding.

Add positive and failing driver cases for unique/ambiguous/private/unknown
context variants, nested patterns, generic aliases/unions, payload arity and
form errors, constraints, exhaustiveness, evaluation order, codecs and legacy
Option fixes. Run focused package and golden tests locally, inspect every
golden diff, run lint and formatting checks, and let CI run the full suite.
Obtain a fresh cold review for each PR and rebase before reporting it ready.
