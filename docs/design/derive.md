# Open derivation (bork-7zscao)

> **Status:** Implemented: open derivation, standalone declarations and library templates. Current docs: [derivation](../language/derivation.md). The original implementation review and staged delivery plan below preserve the design rationale; definition-time lifetime checks and some target-dependent facts remain deferred.

Design rationale for open derivation: codecs live in `bork/codec`,
standalone derive declarations are available, and library authors write templates
in Bork. The baseline review and delivery stages below describe the original
implementation plan, rather than the current implementation boundary.

## Original implementation and boundaries

This section records the pre-open-derivation baseline; these internal paths and
codec ownership rules are historical.

`internal/check/classes.go` admits only the prelude's Encode, Decode and
GoStruct. It synthesizes instances and resolves one dictionary per stored
field. Generic derived codecs originally add the same class bound to every
type parameter. `internal/gen/derive.go` writes codec bodies directly as Go,
using `Info.Named["Json"]`, `JsonField` and `DecodeError`. The runtime helpers
also assume these global names. Checking leaves the parsed syntax unchanged;
expansion must preserve that property.

`internal/prelude/json.bork` owns the data tree, errors, codec classes and
primitive/container instances. `bork/json` parses and renders text.
`bork/encoding` supplies CSV, while cli, env, sql and http consume the tree or
codec dictionaries. CLI, env, CSV and HTTP access `_borkDecodeFields`, a hidden
schema on derived Decode dictionaries; it includes independent field decoders,
default providers, descriptions, scalar kinds and optionality. GoStruct then
also used those decoders for field metadata. Removing compiler codec generation
without replacing this schema would break more than JSON round trips.

GoStruct is originally an empty prelude class. The checker prepares Go mirror
types before resolving fields, and the generator adds hidden New, FromGo,
ToGo and Fields operations. It requires both type materialization and checked
conversion, not merely a new instance method body.

Ordinary instances are selectable: own and prelude instances are in scope,
foreign instances require `use`, and ambiguity is an error. The original
checker does **not** impose a general type-or-class ownership rule on ordinary
instances. Show has its separate, stricter owner-only rule. The implemented design adds
the requested ownership rule to standalone derivation, without silently
removing existing third-party instance choices. Making all ordinary instances
owner-restricted would be a separate language change.

## The codec package

`bork/codec` owns these names:

| Original prelude name | Implemented owner and name |
| --- | --- |
| Json | codec.Value |
| JsonField | codec.Field |
| DecodeError | codec.DecodeError |
| Encode, Decode | codec.Encode, codec.Decode |
| JsonError | json.JsonError in bork/json |

Value keeps Null, Bool, Number, String, Array and Object variants. Number keeps
its text, preserving exact integer decoding. Array holds `List[Value]`; Object
holds ordered `List[Field]`, where Field has `name: String, value: Value`.
Codec.Decode has `fn decode(value: Value): T | DecodeError`; Codec.Encode has
`fn encode(value: T): Value`. Public class methods remain lowercase, matching
current class conventions; codec references are qualified at call sites.
DecodeError retains `path` and `message` and has no JSON dependency.

The package exports `instances Defaults` containing its basic, numeric-width,
Rune, Option, List, String-keyed Map and identity Value instances. Users write:

```text
import "bork/codec"
import "bork/json"
use codec.Defaults

type Config = { port: Int, host: String = "localhost" }
derive codec.Decode for Config
derive codec.Encode for Config

fn main() {
  println(json.Encode(Config { port: 8080 }))
}
```

Importing codec makes its exported classes and types available by qualification;
`use codec.Defaults` brings instances into scope, not unqualified class names.
That distinction follows the existing meaning of `use`. Inline
`derive (codec.Decode, codec.Encode)` remains valid. We do not introduce class
imports under the existing instance-only `use` syntax in this epic.

`json.Decode[T: codec.Decode]` and `json.Encode[T: codec.Encode]` remain text
conveniences. Parse/Render/Field/Index/At/Pretty accept or return codec.Value;
JSON parse/format errors belong to json. There are no prelude aliases or hidden
codec imports: the same PR migrates every repository consumer. Existing
JSON-named convenience functions such as sql.QueryJson remain named for their
public behavior; their result types become codec.Value.

Codec is independent of JSON, CSV, CLI, SQL, env and http. Those packages import
codec. CSV/CLI/env may additionally import json to parse scalar or compound
text. The generic shape package described below depends on neither.

## Standalone declarations

The grammar adds a top-level declaration:

```text
derive-decl = "derive" qualified-class "for" type-reference
```

One declaration requests one class. It is package scoped, may occur in any
source file of the declaring package, and is order independent like an
instance. Documentation places declarations after their types. A newline ends
the declaration using ordinary declaration termination rules. Inline derive
is shorthand for the same request and shares duplicate detection.

The initial target is a named record or sealed type, including qualified
foreign names. A bare generic name derives a universal instance with the
original type parameters (`derive codec.Decode for Box`). Explicit target
applications must be concrete (`derive codec.Decode for Box[Int]`); free type
variables are rejected. Universal/concrete overlaps follow ordinary ambiguity
rules. Aliases resolve to their underlying nominal owner and representation;
an alias cannot make a foreign type local. A constrained alias retains its
facts on the instance head, following existing constrained-instance selection.

The request is permitted only in the type owner's package or the class owner's
package. Private representation access remains a separate check: owning a
class does not grant access to foreign private fields or variants. Shape
inspection of a foreign private representation fails; a field codec supplied
by the type owner is a delegation boundary. Show and Eq keep their current
special rules.

Derived instance names retain `ConfigDecode` / `ConfigEncode` for ordinary
local heads so `use config.ConfigDecode` continues to work. Collision detection
uses resolved class identity and target identity, not just their short names.
An unambiguous exported name is required for foreign or specialized heads;
append a deterministic identity suffix only when the ordinary name collides.
Describe lists the actual generated name, and diagnostics suggest it. Two
requests for the same resolved class/head, including inline plus standalone,
are an error at the second site with the first site identified.

Derivation does not add foreign dictionaries implicitly. Each derivation uses
the requester package's explicit instance scope for its field requirements.
Generated code resolves library helpers in the derivation author's lexical
scope and field class requirements in the requesting scope. This distinction
must be represented by identities, never accidental name lookup in the target
package. A class-owner-derived foreign instance still needs `use` elsewhere.

## Shape API and library templates

Use **typed generic expansion**, not native execution of a source generator.
Ordinary `comptime` computes closed data with a batched Go evaluation program;
it cannot currently bake functions, types or declarations. Running such an
evaluator just to derive a record would add a build process to ordinary check
and impede the playground and cross compilation. Derivation instead expands
checked bork templates inside the compiler, then checks generated expressions
with the ordinary types, facts, effects and lifetime rules. No source strings,
raw Go bodies or arbitrary AST construction form part of the derivation API.

The proposed spelling is:

```text
import "bork/shape"

class Labels[T] { fn labels(x: T): List[String] }

derive instance labels[T]: Labels[T] {
  fn labels(x: T): List[String] {
    [comptime for (field in shape.fields[T]())
      comptime if (!field.computed) field.name]
  }
}
```

These examples are proposal text, not runnable snippets. A `derive instance`
template belongs to the package defining its class; there is at most one template per resolved
class. It supplies exactly the class's methods with matching signatures and
may publish typed associated metadata. It is not an instance until requested
by derive. A class without a template cannot be derived. The template has a
symbolic target type T; errors independent of a target are checked at definition
time and shape-dependent operations are checked during expansion.

`comptime for` operates only over compiler-known shape sequences and unrolls in
source order. Each iteration has a fresh lexical identity and concrete field
handle. It is not a runtime heterogeneous list. `comptime if` and `comptime match`
select metadata-known branches before type checking their target-specific
bodies; unsupported branches are not checked against the wrong target shape.
Ordinary if/match retain their ordinary meaning. These compile-time control forms are valid
only in derivation templates and helpers declared with `derive fn` (the ordinary
function declaration preceded by derive, specialized only when a template calls
it). Such helpers cannot be called by runtime code or contain unsafe Go.
No general macro substitution leaks into ordinary functions.

Templates introduce no new keywords or source-level special type declarations.
`derive instance` reuses declaration words; `comptime` marks expansion control.
Shape descriptors and builders use ordinary library type names, with generic
operations checked by the compiler as described below. Existing `comptime { ... }`
continues to compute closed data through its native evaluator; these explicitly
marked template controls instead specialize compiler-known metadata without
starting that evaluator.

A list expression may contain one compile-time comprehension:
`[comptime for (field in fields) expression]`. Each expansion contributes one
element, and the completed expression is an ordinary homogeneous List whose
element type is inferred as usual. A zero-output comprehension requires a
contextual element type, like an ordinary empty list; template examples annotate
local results that may have no stored fields. An optional `comptime if (condition)` guard
before the element expression omits that iteration when false. The guard is
metadata-known and is evaluated before checking the element body, so omitted
computed fields require no codec dictionaries. This is filtering within the
list form; an ordinary if expression retains its existing rules. Nested
comprehensions can concatenate only through ordinary List library methods.
The proposal adds AST forms and meaning, not a new keyword inventory.

`bork/shape` exposes compiler-owned handles, with the following contract:

| Handle or operation | Meaning |
| --- | --- |
| shape.kind[T]() | Record, Sealed or another classified type kind |
| shape.name[T](), shape.owner[T]() | Stable declared identity and display metadata |
| shape.fields[T]() | Ordered record descriptors, including computed fields |
| shape.variants[T]() | Ordered sealed variant descriptors |
| variant.fields | Ordered named fields or positional slots |
| variant.project(x) | Runtime Option of a typed, read-only view of the matching payload |
| variant.Type | Payload view type projection for annotations and helper arguments |
| variant.name, variant.positional, variant.index | Declared tag, payload kind and ordinal |
| field.Type | Type projection retaining independent facts for dictionary selection and proven inputs |
| field.RawType | The stored value type without the destination field's facts; no unchecked record representation |
| field.validate(value) | Validate independent field obligations; return the typed value or shape.ValidationError |
| field.check(value) | Check the same obligations without returning a field value; return Ok or shape.ValidationError |
| field.name, field.index, field.doc, field.computed | Declared metadata; slots have index, no wire name |
| field.facts, shape.facts[T](), variant.facts | Opaque typed obligations with source/path metadata |
| field.hasDefault, field.default() | Presence and typed runtime default provider |
| field.read(x) | Typed projection from an accessible, proven value |
| shape.builder[T](), variant.builder() | Opaque incomplete construction state |
| builder.set(field, value) | Typed immutable update of one stored slot |
| builder.finish() | T or shape.ValidationError, never an unchecked T |
| shape.fail(message, site) | Expansion diagnostic; not a runtime panic |

A sealed template unrolls variant metadata but discriminates the runtime value
with `variant.project(x)`. Some carries a view indexed by the target and variant
identity; only that view permits reads of the variant's fields. None carries
no payload. It exposes no representation of a foreign private variant. For
example, the Encode template's sealed branch has this structure:

```text
comptime for (variant in shape.variants[T]()) {
  match (variant.project(x)) {
    Option.Some(payload) => {
      fields: List[codec.Field] = [comptime for (field in variant.fields)
        comptime if (!field.computed)
          codec.Field { name: field.name,
            value: codec.encode[field.Type](field.read(payload)) }]
      return taggedObject(variant.name, fields)
    }
    Option.None => {}
  }
}
```

This sketch is the named-payload branch; a comptime-selected positional branch produces
an array in slot order instead. Exhausting all variant descriptors permits a
checked unreachable tail. Runtime tag dispatch/refinement is a generic sealed
shape operation, distinct from compile-time metadata matching.

A field descriptor has an internal dependent signature `Field[Owner, FieldType]`.
The compiler substitutes `field.Type` per compile-time iteration. Type handles cannot
escape into runtime values, unsafe Go, arbitrary containers or comptime results.
Template helpers preserve dependent handle identities and undergo the same
expansion limits. Ordinary pure functions can compute metadata such as names
or tags from literal strings; the expander folds the supported pure metadata
subset (literals, local bindings, structural matching, string/list operations,
and terminating helper calls). Unsupported metadata computations fail clearly;
they do not silently start a native evaluator. The implementation documents
this subset and tests its limits before exposing it as stable syntax.

Construction handles are immutable opaque values indexed by target and, for
sealed values, variant identity. They are not T and cannot be passed to code
expecting T. set rejects wrong owners, duplicate slots, computed slots and
wrong erased field types. Values need not already satisfy the destination
field's facts: those obligations are discharged by finish. Missing stored
slots use declared defaults; the codec template explicitly supplies None for
missing Option fields according to its own protocol. Other missing required
slots fail. Defaults are ordinary typed runtime expressions, evaluated once
only when needed; they are not evaluated merely by inspecting a shape.

Finish lowers to the compiler's shared construction-validation primitive:
validate independent stored-field facts, initialize computed cells, validate
computed/sibling-dependent field facts, then variant/type invariants. It checks
nested fact paths, including List/Option members, with the existing constraint
and failure-path machinery. Only successful validation exposes T with its
promised facts. ValidationError preserves path, message and source obligation
identity. Codec maps it to DecodeError; Go conversion maps it to GoValueError.
Predicate failures/panics follow the existing validation behavior. Library
code cannot invent a proof by returning a raw unchecked record.

Facts are callable typed obligations with resolved predicates, arguments,
dictionaries, sibling references and traversal paths, plus display text.
They are not parsed predicate strings. The generic field.validate operation,
also used by metadata callbacks, checks only independent field obligations.
field.RawType permits an erased input annotation without claiming the destination
field facts. RawType's root obligations guide dictionary selection only for
class methods producing the target from inputs independent of that target.
They never become an ArgFacts value promise: a raw result still needs check,
validate, or completed construction. Consumers and ordinary generic functions
receive no additional facts or constrained dictionary preference from RawType.
Successful validation supplies the independent facts on that value;
it supplies no fact about an incomplete owner. Success types must be provably
distinct from ValidationError, including through union members. For unresolved
root parameters or failure-typed fields, field.check supplies an unambiguous
Ok-or-error result without promising a typed field value. It cannot promise
sibling/type invariants before a complete value exists. An independent check
may return a typed field value; it never returns an incomplete record as T.
Private construction is permitted only in its owning package and within the
checked expansion context, never through a public raw builder escape.

`field.Type` retains the field's constrained head as well as its erased value
type. A template call such as `codec.decode[field.Type](raw)` resolves using the
root constraints of that head in the requester's instance scope, exactly as an
explicit constrained type argument does today. Result-producing dictionaries
must promise those constraints; the expander never assumes the undecoded input
already satisfies them. Nested-path and sibling-dependent obligations remain
on the descriptor and final builder validation. A schema's independent callback
removes sibling-dependent requirements before resolution/validation; it cannot
select a dictionary whose result contract needs unavailable siblings. The
final complete decoder may use the full field head and then validate relations
after construction. Cache keys include these heads and all competing visible
candidates, including constrained alternatives and ambiguity outcomes.

Templates infer field dictionary requirements from their checked method calls.
Encode can require Encode for stored field types; Labels above requires none.
Unused type parameters do not receive gratuitous class bounds. Recursive
records use provisional instance heads and recursive dictionary references;
a recursion in data is permitted, while a nonproductive expansion dependency
cycle or endlessly changing specialization is diagnosed. Instantiations are
memoized and bounded by work/depth/output limits with expansion traces.

## Metadata and Go interoperability

Codec schema behavior must survive the port. A template may declare
`metadata RecordSchema = ...` alongside its methods; the metadata's key is its
resolved result type and its expression is checked bork. It is associated with
the selected dictionary, not globally with T, because alternative codecs can
have different schemas. `shape.metadata[T, Class, MetadataType]()` requests
that selected dictionary and returns `Option[MetadataType]`. This intrinsic
accepts type/class handles only as compile-time parameters and contains no knowledge
of Decode. Duplicate keys and mismatched metadata types are definition errors.
Handwritten instances without metadata return None; an explicit metadata block
can opt them in without a new required class method.

Metadata keys are closed, resolved types such as `RecordSchema` and
`FieldSchema`, or target-indexed families. Generic targets and typed callbacks in
metadata values are supported. Closed keys use checked Bork type identity,
including callback effects and structural tuple equivalence; runtime Go
reflection does not define identity.

A `metadata type K[T]` family (bork-ugmjgz) is one key whose instances are
always applied to the dictionary's target. Its runtime identity is the family's
declaration, so an instance for `Option[A]` declaring `K[Option[A]]` answers
both a generic query `K[U]` and a concrete `K[Option[Int]]`. The static result
`K[U]` is sound because the selected dictionary for U holds the entry for
U, or for its base type when U carries facts. Both erase to the same Go type,
so the Go assertion holds, and consume-only families make the base-type entry
safe to use at the refined type. Inferring a family from `K[X]` with X equal to the target would be
unstable under substitution: `K[Option[String]]`, written as a closed key for a
generic `Option[A]` instance, would look target-indexed at A = String, and
`Option[RecordSchema]` is already a closed key. So the opt-in is per
declaration. A constrained target (`Int where small`) selects its base type's
instance, which never promises the facts. A family's parameter may therefore
appear only as a whole callback parameter type: values may consume targets but
never produce unchecked ones. Multi-parameter families, and keys that project
the target (`List[A]` on `Option[A]`), are out of scope, because a position's
identity becomes ambiguous after substitution. Lazy initializer dependencies,
captures and duplicate checks work as for closed keys, with keys compared by
family.
Metadata initializers run lazily when their key is queried, rather than when the
dictionary is constructed or one of its ordinary methods is called.

`codec.Schema[T: codec.Decode](): Option[codec.RecordSchema]` and corresponding
field-kind metadata replace hidden decoder members. Schemas contain ordered
fields, names, docs, fact descriptions, default/optional flags and independent
validation callbacks `Value => Ok | DecodeError`. Generic field kinds come
from the actual selected field decoder metadata, preserving custom codecs.
Default metadata includes `defaultValue: Option[() => DefaultSchema]`, with
`display`, `configPath: Option[String]` and `choices: List[String]` projections.
The template uses the typed field.default() and the field's static type to build
them; Option.None remains distinct from the absence of a declared default.
Display formatting matches the existing CLI help behavior, including nested
values. These projections avoid exposing arbitrary erased values or adding an
Encode requirement to Decode. Inspecting schema flags does not run the provider;
consumers invoke it once for the current help/config-default behavior.
Builder default evaluation is separately
once per omitted field when constructing the final value.

Schema consumers validate fields, assemble codec.Value, then call the complete
Decode method before returning T. CLI Partial.Get additionally performs its
requested U decoding; it never obtains T from a partial schema.
`_borkDecodeFields` was a temporary Go compatibility adapter; typed schema
metadata now replaces it in standard consumers. Metadata initializer functions remain runtime code;
only their template structure is expanded at compile time.

GoStruct is a library derivation over a generic foreign-record capability.
The compiler still materializes an exported Go record layout and performs safe
conversion; that is a backend capability, independent of any class name. A
class has it when its derive template declares the closed metadata key
`shape.ForeignRecord`. Deriving such a class reserves the target's Go named
type before Go bindings resolve (one Go layout per record). After defaults and
computed fields are classified, the compiler evaluates the template's
initializer with the bounded metadata evaluator. It evaluates the layout records
(`ForeignRecord`, `ForeignField`, `Tag`), comptime lists, `ForeignOption`
constants and String case operations. Then it validates slot provenance, each
stored field placed exactly once, unique exported Go names, tag names, the
Option policy, resources, generic fields, private ownership and specializations.
The same initializer becomes the ordinary runtime metadata function, so
`shape.metadata` returns the layout the compiler used. The checked plan is
attached to the record, not read back from runtime values.

The template chooses Go field names, order, tags, stored/computed selection
and Option mapping policy. GoStruct's template lives in the prelude, which now
loads the standard packages it imports (`bork/shape`) as ordinary packages.
Their declarations are emitted only when used. Every capability class's
dictionary gets the generated New/FromGo/ToGo/Fields bridge, built on the
checked mirror conversion backend. FromGo keeps accumulated GoValueError
paths, nil/cycle checks, defaults and owner facts. Existing concrete-record
restrictions remain. Mirrors keep their declared Go layout: the template must
agree on field names (ignoring case) and add no tags. Fields() projects only
the layout and declaration; field decoders and kinds come from the source
`codec.Schema` metadata of a separately derived Decode instance, so the
generator no longer refers to bork/codec.

## Codec protocol and migration

Preserve existing named-record and named-variant wire representations,
unknown-field policy, default/Option handling, computed-field input rejection,
constrained-instance choice and validation paths. The positional-variant
worker's approved representation is `{"type":"Pair","values":[1,"x"]}`:
exact payload arity, errors at `.values[index]`, and no synthetic slot names
on the wire. Fieldless variants and Option's explicit null/value instances
remain unchanged. Shape records named versus positional payloads explicitly;
codec chooses the representation rather than the compiler.

Coordinate with context-pattern PR #345 and tuple PR #350 before the positional
payload stage. Positional metadata uses ordered numeric slots (0, 1, ...), with
backend fields E0/E1; neither those names nor the numeric labels become object
keys. Shape payload views use the explicit slot index. Option's representation
migration moves unsafe-Go/projection readers to its first numeric slot, while
its library codec remains the explicit null/value protocol. Inspect helpers
through resolved slot metadata instead of assuming a stored field named value.

The first implementation stage still has compiler codec generation, identified
by the actual bork/codec package and declaration identity, never by short class
names. Its helpers and unsafe Go rewriting must use resolved package prefixes.
Audit checker guards for opaque values and private representation, package
lazy-initializer reachability, constrained facts, dictionary/schema generation,
GoStruct field decoders, and runtime helper selection as part of the move.

Migrate std sources, unsafe Go bodies, prelude ownership notes, examples,
project templates, driver/editor/playground fixtures, golden files, benchmarks,
and docs. Add `bork/codec` to std registration/package docs and use Defaults in
all relevant package instance scopes. JSON error names become qualified;
SQL's own Value type must not collide with codec.Value. Update encoding PR #341
and CLI PR #342 users after rebase, rather than carrying divergent codec APIs.

## Caching and performance contract

Expansion performs no native Go build or execution. Programs with no derive
pay only declaration dispatch. Preserve existing checked-program session reuse
and check-local typed helper memoization; do not retain mutable checked pointer
graphs across checks. Broader retained expansion caching requires a measured
benefit on realistic warm edits. The initial metadata-only session/content
plan prototype was removed after the 200-record codec/Labels workload regressed;
see [the experiment](derive-performance.md). If broader plans are reintroduced,
store immutable plans with stable declaration references and reconstruct
request-local typed nodes.

Retained plan keys must include compiler/shape ABI, template and reachable helper bodies, full
target shape (types, defaults, computed bodies, facts, variant order/payload
kind, privacy, foreign tags), generic arguments, library lexical dependencies,
and the requester's visible dictionaries/rules. Candidate changes that alter
ambiguity invalidate selection even if the formerly chosen instance survives.
Source-position bindings are refreshed when replaying a plan. Bounds/output
limits apply equally on hits and misses. Incomplete dependency inventories
force misses. Errors and in-progress plans are never successful cache entries.
A changed private field or default must not reuse an older expansion.

Compare existing and new derive-heavy focused benchmarks for cold checking,
warm checking after an unrelated body edit, and generated runtime throughput
and allocation. Record expansion hit/miss counts and native execution counts.
Accept the port only with no extra evaluator builds, warm expansion reuse,
and no material per-build or runtime regression; investigate measured changes
before setting a numerical tolerance. CI handles broad performance fixtures.

## Diagnostics, tooling and delivery

A malformed template reports at the library definition. An unsupported shape,
missing dictionary, invalid target ownership or failed target validation reports
at the inline/standalone derive site, with field/type declaration and template
operation notes. Static fail defaults to the derive site and can name a field
site. Runtime DecodeError keeps its existing paths. Generated references carry
both origin and expansion positions for describe, navigation and debug mapping;
rename follows resolved source identities rather than generated spellings.

### Definition witnesses (bork-a7vsad)

Facts and lifetimes are checked on the typed tree, so unused definitions get
them through a partial typed lowering. Each derive helper and template method
gets a witness: a clone checked as an ordinary function after the program
checks, never emitted and never in `Info.FuncOf`. Target-dependent parts become
holes, Invalid-typed calls that still evaluate their arguments: descriptor
operations, `shape` queries, type parameters and projections, native comptime,
and build or embed reads. A staged `if`, `match` or `for` body becomes one copy
under an opaque condition. A runtime match of a dependent value becomes opaque
branches. Code that still fails to check becomes a hole on the next of four
attempts. A witness that still fails, panics, cannot be lowered, or would add
an instance, expansion or independent type to the program is dropped; types
instantiated with Invalid arguments are removed instead. Shipped standard
packages are skipped unless they are the root.

After lowering, the side-table entries keyed by witness syntax are removed and
the position-keyed navigation tables are restored, so editor and lint queries
see only the program. Witness interpolations start no validator.

The fact checker taints a witness's holes, staged values, values of Invalid
type and everything computed from them. A failing obligation is reported only
when its subject and arguments are untainted, no condition involving a tainted
value dominates it, and no earlier staged copy, repaired hole or absorbed
`Never` copy could have exited early. (A guard that returns on a tainted
condition therefore silences everything after it.) Its predicate evaluations are
discarded, so unused definitions never start the evaluator. Expansions that
repeat a reported failure are dropped in favour of the definition's
diagnostic. Staged copies are reported like their types are: an obligation
that fails for every copy fails for every target that has one.

Planned PRs (split further if review size warrants):

1. **Design:** this proposal; lead approves before implementation.
2. **Standalone derive and codec migration:** AST/parser/formatter/checker,
   coherent ownership and instance names, package migration and all syntax
   consumers. Keep the old codec backend temporarily. Coordinate positional
   codecs and the CLI's ongoing Decode APIs before source migration.
3. **Shape and generic derivation:** handles, comptime control forms, typed expansion,
   validated builders, associated metadata, generic foreign layouts, cache and
   diagnostics. Exercise an independent Labels class before porting std.
4. **Library derivations:** port Encode/Decode and schema metadata, then GoStruct
   (separate PR if substantial), migrate schema consumers, delete all compiler
   class-specific generation, and publish a runnable custom-class example.

For stages 2–4, follow [the full syntax checklist](../syntax-changes.md):
AST/tokens/parser recovery; checker/facts/effects; generator/debug mappings;
formatter idempotence; describe; std/prelude/validators; compiler-backed editor
completion/navigation/rename/inlay/semantic/import tooling; playground parity;
project templates; TextMate and tree-sitter grammar generation and queries;
Vim/Emacs behavior; query snapshots, grammar pins and upstream drafts; grammar,
requirements, reader docs and examples. Coordinate editor grammar ownership
when extending comptime syntax. Proposal snippets become compilable docs only
when their corresponding feature lands.

Focused tests cover both spellings and cross-file order, qualified classes,
alias ownership, private delegation, concrete/universal generics, duplicate
and ambiguous requests, recursive targets, constrained dictionaries, defaults,
computed/sibling/type/variant facts, positional arity and paths, opaque/foreign
values, metadata selection, empty/filtered comprehension typing, schema partial-value
safety, layout conversion,
and template/cache failure/invalidation limits. Test clean versus cache-hit
output and diagnostics and that checking leaves parsed ASTs unchanged.
Run the touched packages/cases plus lint/gofmt locally; let GitHub CI run the
full suite and cross-component/editor/doc checks. Review every golden diff.
Each implementation PR gets a fresh cold review before reporting to lead.
