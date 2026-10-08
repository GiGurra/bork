# Lazy bindings and record fields (bork-9zpf2t)

> **Status:** Implemented: local lazy bindings, lazy/computed record fields and pure package bindings. Current docs: [lazy bindings](../language/scopes.md#lazy-and-async-bindings) and [record types](../language/types.md).
> Bork blocks below are design sketches; the linked current docs contain checked examples.

Bork evaluates ordinary expressions eagerly. An explicit `lazy` modifier defers
one initializer until its first read and shares that result with every later
read. The static type remains the initializer's type. This is memoization of a
single value, distinct from a Seq's repeatable producer and from a task that
starts immediately.

The inspiration is [q's lazy values](https://gigurra.github.io/q/api/lazy/),
including once-only evaluation, concurrent readers and cached failures. Bork
uses transparent binding/field reads instead of a wrapper accessor, immutable
captures instead of mutable Go closure capture, and checked scope lifetimes.
This document specifies the complete feature. Local bindings, independent record
fields, computed sibling defaults and pure package bindings are implemented.

## Bindings and demand

```bork fragment
fn answer(): Int {
  lazy result: Int = expensiveComputation()
  if (needAnswer()) { result } else { 0 }
}
```

`lazy name = expr` and `lazy name: T = expr` follow ordinary binding inference,
expected types, facts, visibility and unused-binding rules. The modifier is a
contextual keyword only at binding and record-field declaration heads; existing
names `lazy` elsewhere remain legal. Destructuring and `_` lazy bindings are
rejected: they obscure which read forces the shared initializer. Lazy parameters,
call-by-name arguments and assignment to an existing binding are out of scope.
A binding's own name is unavailable in its initializer, as for an eager binding.

Reaching the declaration creates a memo cell and captures its immutable lexical
inputs; it does not evaluate the initializer or any of its argument expressions.
Those expressions evaluate in ordinary source order on the first read. A read
in an argument, condition, interpolation, pattern subject or ordinary alias
`alias = result` forces it. Capturing `result` in a closure does not force it;
reading it in that closure does. Nested lazy initializers can capture earlier
lazy bindings without forcing them. An unused cell performs no work, including
when its enclosing function or scope exits. Generated unused-variable handling
must never read it merely to satisfy Go's compiler.

Each dynamic execution of the declaration creates a fresh cell. Reads through
closures share that execution's cell. Loop iterations have separate cells.
There is no public force-state API: whether the result is cached is an
implementation detail, not a predicate or an input to program behavior.

The initializer is a function boundary returning T. A block initializer can
use explicit `return` under that result type; it returns from the initializer.
`?` is rejected in lazy initializers, including lazy field recipes. Use `match`
to handle each outcome explicitly, as in a lambda.
`break`, `continue` and `yield` cannot cross this boundary. Scopes and
owners created inside it obey ordinary unwinding. Returning a handle belonging
to an initializer-local scope is rejected, as with an ordinary function.

## Effects, context and failures

The initializer's inferred effects are charged at the declaration/construction
site even if the value is never read. Missing `uses` declarations are errors
there. This conservative effect contract allows reads to keep type T with no
latent-effect qualifier. It differs intentionally from Seq traversal effects.
Capturing an open-effect callback follows ordinary closure checks; an unresolved
callback effect cannot be hidden by a lazy initializer or exported field.

Ambient `needs` are captured from the declaration's lexical context, as for
closures. A reader's ambient needs cannot change the recipe after it is declared.
The compiler must retain those capture requirements in construction/factory
checking, including lazy supplied fields; reading a passed record needs no
second copy of the constructor's context. Dynamic mocks and logged/propagated
labels follow the forcing goroutine under their existing rules: data and
closures do not carry mocks. The first force therefore determines the cached
result under the mocks in force for that reader, while later readers only see
that cached result.
Captured capabilities are ordinary immutable values, not snapshots of external
state: delaying an I/O call can change its result and the order of its effects.

Like Task.wait, a memo read observes work whose effects belong to its creation
site. This is an explicit exception to "pure means no outside action": its first
read can perform the charged initializer effects. It does not make that original
effectful expression a repeatable pure computation. Compiler predicate/constant
evaluation must never force a runtime cell, even through a pure accessor,
comparison, Show instance or whole-value validator. A compile-time proof may
reason about a checked initializer statically, or evaluate a wholly known pure
candidate with no runtime cells. Fresh pure cells created inside that known-input
compile-time candidate preserve demand and caching; they are distinct from
cells created by the running program. Otherwise use existing boundary validation or
report an unproved obligation. No optimizer may hoist or eagerly force a runtime
cell based only on the accessor's pure signature.

The first reader runs the initializer synchronously on its own goroutine. Other
readers wait for the same completion; the result is published atomically. A
success, any alternative of a failure union, or a panic is cached exactly once.
A cached panic is re-raised on every read, with its original failure information;
it must not become a zero value after the first panic. Retrying requires a new
cell. There is no automatic task, scope, retry, background work or cancellation
policy. Waiting for a cell does not create a new cancellation exception; the
initializer uses its captured scopes' ordinary cooperative checkpoints. A
nonterminating initializer can block its readers just as a synchronous call can.

Reject statically visible initializer dependency cycles with a chain showing
all declarations, including computed field and package dependencies. Local
forward/self references already fail ordinary name checking. A cycle hidden
through callbacks or foreign code must fail loudly when runtime re-entry can
be identified, rather than returning uninitialized data. Cross-goroutine cycles
that are not statically visible retain the ordinary blocking-call limitation;
the feature does not promise whole-program dynamic deadlock detection. Lowering
must distinguish empty, evaluating, value and panic states and account for
re-entrant access; blindly using a once primitive that deadlocks on direct
re-entry is insufficient.

## Record fields and a passable lazy value

```bork fragment
type Lazy[T] = { lazy value: T }

fn deferred(): Lazy[Int] {
  Lazy[Int] { value: expensiveComputation() }
}

// The field access has type Int and forces once.
fn read(value: Lazy[Int]): Int { value.value }

type Person = {
  first: String,
  last: String,
  lazy full: String = s"${first} ${last}",
}
```

`lazy field: T` is an independent supplied field. It is required unless an
ordinary admissible closed default is present. `lazy field: T = expr` whose
initializer refers to sibling fields is a computed field: its pure default may
read only siblings and pure operations on them, with no external captures or
ambient needs. A sibling-independent default remains an independent field.
Classification is fixed by the declaration, visible in tooling, and does not
vary per instance. A computed field cannot be supplied explicitly or overridden
by `copy`; change its dependencies instead. This keeps equality and codecs
consistent across all instances. A computed field with no sibling reference can
be expressed as an independent lazy default; no dependency inference from runtime
branch selection is performed.

A supplied expression captures the constructor's environment and is checked
against T immediately, but evaluates only when the field is read. Other fields
and constructor arguments keep their ordinary evaluation order; evaluation of a
lazy expression is skipped at its position. Independent defaults obey existing
default admissibility and specialization rules. Computed defaults extend those
rules only for pure sibling expressions; they capture the completed record's
fields, not a partially initialized construction order. Sibling references may
point forward; a dependency graph must be acyclic. This applies to records and
record-bearing sealed variants, explicit/specialized/context literals, assembly
and nested constructors. Private construction rules remain in force.

Every holder of a record shares its cells. Whole-record binding/return/storage
and a bare type test do not force fields. Projection, field-binding destructuring
and a pattern comparing a field force only the fields actually inspected, in
ordinary pattern evaluation order. A nested record stored in an eager field
retains its lazy cells. Go interop must not erase them or expose a mutable thunk.

Copy shares each unchanged independent cell, even if not yet forced. Replacing
an independent lazy field creates a fresh cell from the new expression. Computed
cells are shared only when none of their declared dependencies was updated;
otherwise they are recreated against the completed copy. Dependencies include
transitive computed fields and nested projection paths; replacing a parent
invalidates its dependent projections. Initially a whole-sibling dependency is
allowed as a conservative approximation of paths. An explicit update invalidates
even when the replacement compares equal; copy must not evaluate values to choose
which memo to share. Copying one changed field does not force unrelated fields.

Default structural Eq and Hash include independent lazy fields as data and force them when
the corresponding eager operation would inspect that field. Computed fields
are excluded from Eq, Hash, Encode and Show: their values are determined by the
independent data. Derived Encode and default Show force independent fields in declaration
order; Eq retains normal short-circuit order and Hash its ordinary field order.
Effectful initializers may consequently run while inspecting a record whose
construction already accounted for those effects. These operations retain
ordinary purity declarations; stable cached observations do not become evidence
that repeating the original effectful call has the same result. User-defined
instances can explicitly inspect computed fields and thus force them.

## Facts, validation and lifetimes

Lazy annotations and field constraints create the same proof obligations as
eager initializers. Statically prove those obligations on the checked initializer
before permitting its declared facts at a read. Do not assume an annotation to
prove itself. Laziness supplies no implicit runtime check that converts a failed
constraint into a successful T. A field predicate or whole-value invariant can
read lazy fields only under the existing pure predicate rules; independent
initializer effects were charged at construction. Checking proofs must not run
an initializer. Stable read identities belong to the memo, so unchanged shared
cells preserve proven facts; invalidated computed cells and changed copies must
reprove their obligations against the completed candidate. Nominal invariants
remain unavailable until construction validation is established, avoiding
circular proofs through defaults or a lazy projection.

Decode and checked Go-to-bork conversions validate their completed values before
returning success, as today. Decoded independent fields are already-resolved
cells containing validated data, not deferred parsing errors. Computed fields
are omitted from the serialized/Go data representation and reconstructed from
their defaults; unknown input-field rules remain unchanged. Runtime validators
may force computed fields that their predicates inspect; document this demand,
and validate before publishing the nominal value. Defaults on missing independent
fields must also be forced when runtime validation needs them. A validation
failure remains DecodeError/GoValueError, never a later field-read surprise.
Schema-driven constructors (environment, CSV, CLI and dictionary/config paths)
expose independent fields with their existing decoder and default metadata.
Computed fields are excluded from writable schemas; an explicit computed-field
input receives the ordinary unknown/read-only-field diagnostic instead of
becoming an override. These paths reconstruct computed cells and validate the
completed candidate under the same rules as Decode. Property generators and
shrinkers generate/shrink independent data only, rebuild invalidated computed
cells, and enforce all field/whole-value invariants before publishing a case.
They never synthesize a computed-field override.
Bork-to-Go conversion forces independent fields needed by the target representation.
Unsupported opaque/unsafe representations receive a diagnostic rather than
silently losing memo, validation or lifetime metadata.

A lazy cell retains every captured scope/resource/closure lifetime until it can
no longer be read, even if T itself is scalar. Such a dependency follows records,
generics, lists, returns and tasks; wrapping the cell in `Lazy[Int]` cannot erase
it. An eager read yielding a scope-independent Int can escape as that Int, while
the lazy record or a closure still holding its cell cannot. Capture dependencies
are conservative and remain after a cell has been forced; no path-sensitive
"already forced" lifetime escape is introduced. A read after closing or handing
away an owner invalidating a capture is rejected.

`lazy db = openDb(s)` opens and registers cleanup with s only on first read. An
unread binding registers no cleanup, and scope exit never forces it. First force
and scope close obey the resource API's existing synchronization and cancellation
contract. The cell cannot open a resource after its captured scope has closed.
A lazy initializer cannot capture/consume an OwnedScope or return one: unread
initializers must not hide mandatory owner consumption. It may capture an owner's
borrowed scope under the usual closure restrictions. Owners created and consumed
entirely inside the initializer still follow ordinary affine rules.

## Package bindings, tooling and implementation

Package `Name = expr`, `Name: T = expr`, and `lazy Name: T = expr` are runtime
memos with ordinary export visibility. Reads within `comptime` evaluate only the
needed pure package values and bake their data, with dependencies prepared first
in one evaluator batch. Ordinary reads remain lazy at runtime. Names may refer to later
package declarations, and exported names can be read through an import. Direct
and helper-induced dependency cycles receive a diagnostic with the declaration
chain, including implicit rendering and decoder validation calls. The first phase for
package bindings permits only pure, closed initializers with no scope-dependent
captures or ambient needs. Dependencies on other pure package bindings are
allowed and cycle-checked across imports. Effectful global initializers require
a separately specified application context/lifecycle contract and are rejected
in this phase. Pure known-input initializers can receive a future suggestion to
use explicit comptime (bork-pvx43y); lazy and comptime remain distinct.

`bork describe` reports type T, lazy/independent/computed classification,
initializer effects, captures and known field dependencies without executing
anything. `dbg` is a value observation: it forces a lazy binding and any independent
lazy fields it prints. Its diagnostic/source annotation says that it forces;
computed fields remain omitted under normal Show. Formatting preserves the
modifier and initializer; grammar, editor syntax, README, structured diagnostics
and describe coverage ship with each phase. A conservative warning can suggest
removing `lazy` when the very next statement unconditionally reads it before
any branch/capture; it must not promise whole-program demand analysis.

Checker metadata must separate source type T from storage strategy, effects,
captures, field dependencies and stable observation identity. Lower binding and
field reads through a compiler-owned generic memo accessor while capturing cells
rather than their forced values in closures. Retain explicit eager/lazy boundaries
in the typed tree for tooling and control-flow lowering. This transparent access
machinery is also intended for bork-mais5u (`async(s) name = expr`), whose task
starts immediately; async scheduling, joining and failure rules are a separate
design. Lazy providers in assembly are future work, not a prerequisite for this
feature.

Acceptance covers unused/read-once/repeated reads, alias and closure demand,
conditional branches, iteration cells, generics and constrained values, source
order, errors and repeated panic reads, concurrent readers under the race detector,
visible cycles and runtime direct re-entry. Negative tests cover missing effects,
ambient/capture erasure, cross-initializer control transfer, scope escape and
OwnedScope capture/result. Resource fixtures count acquisition and cleanup for
unused, forced, panic and scope-close paths. Field cases cover classification,
forbidden computed overrides, forward/transitive/nested dependencies, copy memo
sharing/invalidation, sealed/private/context/specialized constructors, structural
equality/hash and default Show/derived Encode order, Decode and both Go conversion
directions, field facts
and whole-value invariant validation without circular proofs. Package fixtures
cover imported shared reads, global cycles, prohibited effects/context and no
compile-time execution. Tooling fixtures prove describe never forces and dbg does.
