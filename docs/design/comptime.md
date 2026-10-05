# Explicit compile-time computation (bork-pvx43y)

Status: implemented syntax, checking, evaluation, value baking and module-file
inputs (PRs #200, #204, #205 and #207). `comptime { ... }` and
`bork/build.ReadString` / `ReadBytes` are accepted APIs. Native-target checks and
the initial execution/input/result limits are enforced. Limit hardening and
Session value reuse remain planned; disk value reuse will use the shared
incremental cache layer. The cache contract below describes those remaining
requirements, not enabled reuse. User-defined derive, structural constraints
and source/declaration generation are outside this ticket.

See [the runnable comptime example](../../examples/comptime/README.md) for a baked
lookup table, validated module-file configuration and dependent computations.

## Contract and syntax

A `comptime` block computes a closed, typed value during checking. The generated
program contains the result; it does not run the block's recipe. `check`,
`describe`, `emit`, `build`, `run` and `test` use the same evaluated values and
report build-time failures even if runtime control flow would skip the block.
Tests retain the driver's existing checking/selection policy.

```bork
fn defaultPort(): Int {
  comptime { 8000 + 80 }
}

fn table(): List[Int] {
  values = comptime { range(1, 16).map(n => n * n) }
  total = comptime { values.fold(0, (sum, n) => sum + n) }
  values
}
```

`comptime` is contextual only before `{` where an expression starts. It is an
expression, so ordinary binding annotations supply an expected type:
`port: Int where positive = comptime { calculatePort() }`. The block's final
expression supplies its result. Its type is concrete at the computation site;
an enclosing generic type parameter cannot determine the result or its recipe.
Generic helpers instantiated with concrete types remain usable.

The block is a function boundary, like a lazy initializer: `return` returns
from the block, `?` propagates into its own result union, and `break`, `continue`
and `yield` cannot cross it. An Option `?` requires a concrete expected result
annotation when ordinary inference cannot determine its specialized None.
Returning a normal error alternative is a successful computation of that typed
value; callers must handle it. Panic, timeout and malformed evaluator output
are compilation failures. Use `panic` to reject invalid build data explicitly.

There is no `comptime fn` modifier: ordinary checked pure functions are reusable
at runtime and at build time. There is no separate binding modifier; placing a
block on its initializer makes any existing binding a computed binding. Package
bindings, when implemented by bork-9zpf2t, use the same expression. This feature
does not depend on that ticket or introduce package declarations itself.

The inspiration is [q's AtCompileTime](https://gigurra.github.io/q/api/atcompiletime/):
explicit call-site computation, earlier computed-value dependencies and baking
values into output. Bork checks purity and capture closure using its own typed
code and represents results as Bork values. It does not splice source strings.

## Closed inputs and purity

A block can refer to ordinary declared functions, types, dictionaries and closed
literal values. It can capture earlier immutable bindings only when their value
is a compiler-known literal/composite or a completed comptime result. A literal
binding in a runtime function is acceptable; a parameter, runtime function
result, loop variable, scope, ambient need or runtime lazy cell is not. The
compiler checks resolved variable identity, not spelling. Inner local bindings,
lambdas and loop variables are ordinary computation-local values.

A block's reachable calls may use no runtime effects (`io`, `net`, `clock`,
`random`, `state`). Higher-order arguments and open effects must be resolved and
pure; an unknown callback is not evidence of purity. The block has no runtime
effects, even when its enclosing function does. Ambient inputs must be bound
inside the computation from closed values; runtime `needs` cannot be captured.
Mock/test context is not a build input, and compiler evaluation never invokes
runtime mocks. Logs are discarded, as for existing predicate probes.

A new checked `build` effect is allowed only while evaluating a comptime block.
It describes compiler-supplied immutable input data, not arbitrary I/O. Helpers
may declare `uses build` and call one another, but runtime callers and ordinary
predicates cannot use that effect. The block discharges it and cannot return a
function, cell or capability which would perform it later. Predicates can
inspect the resulting String/bytes/config as ordinary values.

Pure `unsafe go` bodies/bindings retain the existing trust boundary: their
signatures must be truthful. Comptime is not an operating-system sandbox for
foreign code. We cannot promise reproducibility for Go code that secretly reads
the environment, clock or mutable globals. Unknown foreign build inputs force compiler-result
cache bypass for checking/emission; executable reuse defaults to the recorded
input recipe, warns about external state, and offers `--rebuild`. See
[executable reuse](executable-reuse.md). Authorization in `bork.mod` is still required. Document this
limitation rather than presenting effect checking as process isolation.

Bork's known pure-but-nondeterministic unordered-map iteration is rejected in
reachable comptime code. Ordinary Map iteration retains its stable ordering.
Foreign hidden nondeterminism remains subject to the unsafe trust contract.

## Explicit module-file inputs

The initial API is a compiler intrinsic package, `bork/build`:

```bork
import "bork/build"
import "bork/json"

// ReadString(path: String) uses build: String
// ReadBytes(path: String) uses build: List[Byte]
fn configText() uses build: String {
  build.ReadString("config/defaults.json")
}

fn settings(): Config {
  comptime {
    match (json.Decode[Config](configText())) {
      value: Config => value
      error => panic(s"invalid build config: $error")
    }
  }
}
```

Each read is a direct intrinsic call with a compile-time constant String path;
paths may use ordinary literal constant folding but not runtime helper
parameters or a prior computed String. Functions may contain those direct reads
and be called by comptime blocks. The static read inventory is conservative:
all intrinsic sites in the relevant checked code are captured, even if a branch
would skip them. Missing/unreadable files fail compilation at the read site.
Reads cannot be taken as function values or escape through callbacks.

Paths are relative to the declaring source's module root, identified by its
`bork.mod`; a standalone package uses its source directory as that root.
Imports resolve against their own module, never the caller's module or process
working directory. Reject absolute paths, parent segments, symlinks in any
relative operand component, special files, backslashes, colons and NULs. Established module-root
ancestors may be symlinks, as with macOS /tmp; capture their identities and the
resolved root identity, and confine operands with os.Root. ReadString requires
UTF-8;
ReadBytes accepts arbitrary bytes. No directory enumeration, env, clock,
network, write or dynamic file-open capability is included in the first API.
Existing `bork/embed` remains package-relative and retains its existing rules.

The driver captures bytes using the same rooted, symlink-rejecting primitives as
embed. It records module/root identity, logical path, component kinds, bytes
and failed lookups. Every evaluator receives the frozen bytes as generated
constants; it cannot reread workspace files. Capture and reuse validate the
inventory, retry a changing snapshot once, then diagnose concurrent change.
The private rooted inventory composes with source inputs. Its dependency digest
is a content summary; component and resolved-root identities are additionally
validated with SameFile, so that digest alone is not a complete replay/value key.
Semantic replay reads only the captured bytes/errors and diagnoses absent keys;
current-input validation is separate. The Session inventory includes these inputs
so a file edit/addition/removal or
module-root change invalidates the whole checked result as well as comptime.

## Result representation and facts

Support Bool, String, numeric types, Ok,
lists, ordered Maps, records, sealed variants, unions and concrete generic data
made recursively from those types. Aliases retain their nominal/constraint
metadata. Reject function values, scopes/resources, tasks/channels/atoms, Seq,
opaque Go types and lazy fields/cells anywhere in the result. No forcing a lazy
field merely to serialize it. Errors identify the unsupported nested type/path.
This restriction also prevents exporting runtime behavior or process identity.

Use a compiler-owned, versioned value schema derived from checked types and the
existing structural metadata used for derived Encode/Decode/GoStruct. No public
Encode/Decode instance is required; user codecs cannot silently change the
meaning of values or satisfy facts by decoding a different value. Transport
includes nominal type IDs, field order and union/variant tags. Numeric values
use their concrete runtime semantics, with exact integer widths and float bit
patterns (including NaN, infinities and negative zero); Strings preserve bytes.
Only insertion-ordered Map cores may cross the result boundary. A sorted Map
retains a comparator closure, and an unordered Map has nondeterministic
traversal: both are rejected recursively, rather than reconstructed with a
different policy. Call `.inOrder()` on a sorted map to export its current order
as insertion order explicitly. Cyclic foreign values are rejected.

The compiler decodes the transport into a closed typed literal/composite tree,
validates the complete schema, ranges and sizes, and emits ordinary typed Go
literals. A large value can use a compiler-generated decoder over a canonical
blob if emission size requires it; that decoder must preserve exactly the same
schema, has no effects, and performs no recipe/file parsing. Start with literals
so `emit` makes the baked data reviewable. Private construction permissions do
not broaden: only an ordinarily checked expression can produce that private
value, and reconstruction is a compiler-internal operation.

Comptime does not automatically invent predicates such as `sorted`. The result
is available to the ordinary backward facts search as a known value, just like
a literal record/list. A use requiring `nonEmpty` or `sorted` runs that predicate
on the baked result. Declared result/field/element requirements are still
checked; `trust` and foreign promises keep their existing explicit status.
Later comptime blocks capture that same typed result. Facts are erased in
runtime output and follow ordinary alias/projection/constraint rules.

## Checking and evaluation stages

Add syntax and typed Comptime nodes without mutating parsed source trees.
Resolve/infer/lower all code normally, checking result representability and
closed captures. Check effects and lifetimes before executing any user code.
Collect static build reads and construct a dependency graph between blocks,
including references through closed bindings and helper bodies. Report cycles
with the participating source sites. A block in a helper is evaluated once per
concrete source site, never per runtime invocation; it cannot capture the
helper's runtime parameters. Nested blocks are dependencies, evaluated inside
out. Stable source order breaks ties between independent blocks.

Facts and evaluation require staged cooperation, not an unconditional new
phase before the current Facts pass. Before running each block, prove all
requirements on its recipe's calls and construction, including reachable
helpers' own declared result promises under the normal facts rules. The
comptime expression's contextual output constraints are checked after execution,
not required as a precondition or assumed inside the recipe. Thus
`port: Int where positive = comptime { parsePort("8080") }` may evaluate a
helper returning unconstrained Int, then prove positive on its baked result. Prerequisite comptime
nodes are already closed values, so their predicates can run normally. A block
cannot use a fact about its unevaluated result to justify executing its own
recipe. Any cyclic proof/evaluation dependency fails with a source diagnostic.
For example, `comptime { requiresPositive(-1) }` is rejected without executing
the constrained function, even if it happens to tolerate -1 at runtime.

Preflight proof starts from closed capture values and checked declarations,
never runtime guards or the enclosing function's entry assumptions. Build-time
execution is unconditional even inside an unreachable runtime branch. For
example, `x = 0; if (positive(x)) { comptime { requiresPositive(x) } }` must
fail: the surrounding guard cannot justify the build-time call. Facts established
by guards evaluated inside the block follow the ordinary branch rules.

After evaluation, replace the typed node's runtime payload with its validated
closed value while retaining source/type information for describe/diagnostics.
Check obligations on the reconstructed result and complete the ordinary
whole-program facts pass before publishing any checked artifact. Evaluate
requested predicates on the frozen values, not by rerunning recipes. Source
queries retain the original block span, inferred type and known facts.

Generate an evaluator from the same checked program and frozen module, assets
and effective Go context as predicate evaluation. Integrate with
`evaluatorWithContext` when bork-h5rkt4 lands its context boundary; do not use
one-shot capture helpers from a Session request. Initial correctness may use a conservative whole-program
implementation digest and one subprocess per dependency layer; narrower roots
and batching are optimizations. Do not execute unrelated package initializers,
particularly future lazy/async package bindings. Emitter roots and required
dictionaries must be limited to the computation's reachable code.

Multiple ordinary blocks share one built value evaluator within a compilation,
as package-backed computations already do. The driver sends one recipe only after
its dependencies and preflight obligations pass, decodes its bounded result and
checks its nominal/field constraints before authorizing a dependent request.
Contextual result constraints remain with the enclosing Facts pass. Getter slots
hold the same typed values throughout this compilation; their results are never
persisted. Each request resets the encoder budgets and receives a fresh deadline.
Single ordinary recipes retain their standalone path, including the reviewed
native interpolation-validator shortcut. Predicate requests remain separate:
their argument values and proof prerequisites can become available between value
requests. See the [measured batching checkpoint](proof-evaluator.md#ordinary-comptime-batching-bork-vwsr41).

## Limits, diagnostics and target semantics

Initial execution limit: ten seconds per recipe or predicate request; cap each captured
file at 16 MiB, aggregate build files at 64 MiB and serialized results at 16 MiB.
Decode at most one million nodes and depth 256. The schema parser checks these
budgets before consuming each value node, rejects duplicate fields and validates
scalar field types before constructing the typed result. The shared value-limit
policy has its own version for future reuse keys. Enforce read/output bounds while
streaming, not after unbounded allocation. Kill and reap the evaluator process
on timeout, including compiler-started descendants where supported. Bound
stderr/log capture and avoid using stdout as a fragile protocol stream; use a
separate framed result channel. These limits are versioned policy inputs.
A process memory hint is useful but is not a guaranteed hard memory limit; this
first implementation must not claim otherwise. Go build errors remain distinct
from evaluator timeouts; toolchain compilation has its own driver policy.

Diagnostics have stable `comptime.*` and `build.*` codes and point to the block
or read, with notes for dependency/helper sites. Cover runtime capture, impure
call, unresolved type/effect, unsupported result, cycle, input escape/read,
panic, timeout, oversized data and malformed evaluator output. Never publish a
partially decoded value or silently fall back to runtime evaluation.

Initially computation supports native Go target builds only. Reject cross-target
comptime requests explicitly until target execution is supported, rather than
baking platform/foreign behavior into another target. Bork Int is always
64-bit; native dependence comes from generated Go, foreign libraries and
platform execution, not the width of Bork Int. Record the resolved Go
executable/toolchain, GOOS/GOARCH and applicable flags in keys.
Foreign/native-library and mutable local Go replacements must either have a
complete input inventory or bypass reuse, as in the incremental design.

## Cache contract with bork-h5rkt4

Use SHA-256 with the incremental design's canonical tagged, length-delimited
encoding. The computation key includes:

- Compiler executable digest, embedded prelude/std and value/codegen/policy
  schema versions; a release string alone misses local compiler changes.
- Concrete typed expression, result type/constraints, generic specializations,
  selected dictionaries, reachable implementation/default/predicate/rule bodies
  and their dependencies. Begin with the complete checked source graph.
- Prior comptime values' canonical bytes and type IDs, captured file/input
  manifests (including negative dependencies), bork.mod and pinned Go manifests.
- Effective Go/toolchain/target/configuration and any permitted foreign inputs.
- Evaluation mode/roots, visible rule/instance/provider sets, resource limits
  and source-location dependencies when debug
  text, logging, diagnostics or generated behavior observes a source position.

Store canonical values and their full dependency manifests, not check.Info
pointers or merely generated Go's source hash. Reuse revalidates every input,
reconstructs values in the current request's type graph and rechecks required
facts under that request's predicates/rules. Changed predicate implementations
invalidate proof results even if the serialized table bytes remain identical.
Do not cache timeout/panic/transient toolchain failures persistently initially.

Integrate with the Session source/embed snapshot and dependency inventory.
Captured Go settings/launcher selection are distinct from complete toolchain
identity: the current private helpers do not freeze all toolchain bytes or
external metadata, and are not a versioned cache API. Keep reuse disabled until
that wider inventory is complete. Do not introduce a second independently stale
whole-program cache. Initial value
reuse may remain in memory until a safe versioned disk artifact exists. Unknown
execution dependencies disable both value and enclosing checked-result reuse.
For the same captured tracked inputs and truthful unsafe signatures, cache
misses and `--no-cache` produce identical values, diagnostics and emitted Go. Comptime implementation fragments remain dependencies of importing users
even when the ordinary runtime public interface is unchanged.

## Implementation and acceptance

Implement parser/formatter/type closure and effect checks, then module-input
capture, bounded evaluator/value transport, facts staging and baking, then
Session value reuse. Each shipping PR updates grammar/requirements/README to
identify accepted syntax and APIs separately from remaining planned work.

Golden and integration coverage must include tables and parsed configuration;
concrete generic/record/list/map/variant/union results; float edge cases; nested
and dependent blocks; alias/field/element facts including false predicates;
private records; imported helpers; rejected runtime/lazy/ambient captures,
effects, function-valued results and open callbacks; unordered iteration;
preconditions rejected before executing code, including skipped-branch/entry
assumptions; sorted/hash Map result rejection and explicit inOrder conversion;
cycles; file escape/symlinks,
missing/edited/added inputs, UTF-8 and snapshot races; panic/time/output limits;
unsupported cross-target evaluation; clean versus warm/no-cache output; changed
helpers/dictionaries/predicates/modules/limits and fresh describe locations.
Include no-edit Session requests where a pure unsafe helper observes a changed
untracked file/environment input, verifying both value and enclosing-artifact
cache bypass; predicates/helpers containing comptime to exercise proof/evaluation
cycles; and both true and false contextual result constraints checked only after
evaluation, separately from invalid recipe preconditions.
Run the baked program after removing its source data files, and inspect emitted
Go to verify that recipes and file reads are absent from execution paths.
