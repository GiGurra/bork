# Incremental compilation (bork-h5rkt4)

Status: proposal for review. No cache, watch command or output-layout change is
implemented by this document. This design guides the remaining work in
[bork-e3r166](performance.md).

## Goal and correctness contract

Repeated `check`, `build`, `run` and `test` should reuse work after a small edit.
Editors and agents repeatedly calling `bork check --json` should receive current
results without paying process startup and checking unchanged dependencies.
Reuse starts at a package: one directory of `.bork` files. Function-level reuse
is a later optimization.

For the same source snapshot, compiler, mode and external inputs, cached and
clean compilation must produce the same ordered diagnostics (including codes,
positions and notes), generated Go bytes, staged assets, module files and test
selection. A cache miss must be harmless. An incomplete dependency record must
force a miss. A failing dependency must never expose an older successful
interface to a dependent.

Generated Go equality is against the clean compiler using the same output-layout
version. Introducing multiple Go packages deliberately changes the old layout;
that migration needs its own output review and runtime tests. Go executables
may contain toolchain/build-location metadata, so byte equality applies to
compiler-produced artifacts; executable behavior and build success are checked
separately unless all Go build inputs and paths are controlled.

## Current pipeline and required boundary

`internal/driver/driver.go` loads the prelude and the transitive import graph,
resolves the program Go module, checks/infer/lowers the whole program, captures
embeds, checks effects and lifetimes, proves facts, then generates Go. Predicate
queries can generate, build and run a Go program during fact checking. Ordinary
`Check` also checks root tests today; a cache must retain that behavior.

`internal/check/check.go` has package namespaces, but its `Info` holds shared
function/type tables, rules, instances, specialization state and syntax-keyed
maps for the whole graph. Expansions are recorded in those semantic maps;
`TestCheckLeavesSyntax` verifies that checking preserves parsed syntax. The
semantic/typed graph remains mutable. Later passes use pointer identity and
sometimes inspect other functions' bodies.
`internal/gen` currently emits one `package main` with package-prefixed names.
A `check.Package` pointer is therefore not an independently reusable result.
Preserved source ASTs and the immutable embedded-token cache are useful early
boundaries, not checked package artifacts.

The refactor must introduce an immutable `PackageArtifact` after all applicable
validation passes, with stable IDs for declarations, types, typed nodes and
source spans. Import references use `(package identity, declaration ID)`, never
serialized Go pointers. Mutable inference variables, specialization work queues
and syntax maps remain request-local. Initially, artifacts can remain in memory;
disk reuse waits for a versioned encoding and an importer that reconstructs the
same relationships without mutating imported artifacts.

Package checking needs explicit inputs: its sources, dependency interfaces,
any required implementation fragments, visible rule/instance/provider sets,
Go metadata and compilation context. Package-local passes then return an
artifact and a dependency manifest. Graph-wide passes remain graph-wide until
all their reads are accounted for; reuse their results only under a complete
graph key. Do not put goroutines around the existing shared checker.

## Identities, keys and snapshots

Use SHA-256 content digests and a canonical, versioned encoding. Sort maps by
stable keys; preserve semantically significant source, parameter and field
order. Hash an explicit tag and length for each component. Distinguish:

| Identity / key | Inputs and purpose |
| --- | --- |
| Package identity | Module identity and logical import path; a standalone root also identifies its selected file/directory and source set. Root/prelude role is compilation context, not an accidental numeric index. |
| Source digest | Ordered logical filenames and exact bytes, including added/deleted files. An added previously absent package/file invalidates recorded failed lookup dependencies. |
| Compiler namespace | Compiler executable digest, artifact/schema version and embedded prelude/std contents. This covers dirty/local builds as well as releases; a version string alone is insufficient. |
| Check key | Namespace, package/source identity, mode/context, resolution configuration, direct semantic dependencies and external-input manifest. |
| Interface digest | Canonical facts consumers can observe; omit irrelevant whitespace and ordinary implementation bodies. |
| Implementation digest | Typed implementation, helper/default/predicate/generic bodies and their transitive implementation dependencies, including embedded data. |
| Generation key | Checked artifact, concrete specialization requests, entry/test/evaluator roots, layout version, name/ABI scheme and code-generation context. |

Resolution configuration includes `bork.mod` (including unsafe authorization),
module roots, effective Go dependency manifests and sums, selected Go executable
and toolchain, target, applicable flags, workspace/module settings and package
driver configuration. Read effective Go configuration, including its saved
GOENV file, not just process environment variables. Mutable local replacements,
GOPATH packages and custom drivers need content/metadata validation; bypass reuse
where their inputs cannot be described. Avoid treating missing Go module
metadata as proof that a package is immutable standard-library code.

Semantic keys use logical paths; diagnostic/rendering artifacts also record the
request's root, working directory and path-display policy. Initial disk entries
may be workspace-specific until rebasing paths is proven. Moved declarations
can retain their semantic interface digest but must refresh source maps and any
dependent diagnostic notes/codegen caller locations. A source-location dependency
is distinct from a semantic dependency and invalidates the relevant artifact.

Read sources, manifests and assets into a request snapshot. Directory membership
and recursive embed listings belong to it. Watch events are hints to rescan;
mtime/size alone cannot establish unchanged contents. Detect changes while
capturing the snapshot and retry or report that the request was superseded.
All phases, predicate evaluation and emitted assets must consume the captured
bytes rather than rereading a changing workspace halfway through a request.

## What an interface exports

An interface is a consumer contract, broader than exported type signatures.
Include private declarations transitively required to interpret that contract,
using opaque IDs where a consumer must recognize ownership without gaining
source-language access. The summary must retain current visibility rules.

| Area | Required summary / dependency |
| --- | --- |
| Names and types | Exported names, aliases, records, sealed members, field/parameter order, method signatures, generic constraints, resource identity, representations relevant to Go interop and private-construction ownership. Include constructor accessibility and opaque identities. |
| Effects and ambient needs | Declared/checked effects, higher-order effect contracts, needs and hidden-parameter order, ambient declarations and their constraints, providers and provider-bundle assembly contracts. |
| Facts and predicates | Parameter/result/field constraints, requirements, exported promises and predicate identities; any body-derived result facts or validation behavior read by the proof engine. |
| Rules | The complete rule set actually visible to the proof engine, including premises, conditions, conclusions and referenced predicate/type IDs. Current `Info.Rules` is program-wide; begin with a whole-visible-set digest rather than inventing narrower package visibility. |
| Classes and instances | Class methods, associated contracts, derived instances, instance candidates, explicit uses/bundles, coherence and resolution inputs. Absence of a matching candidate is also a dependency. |
| Defaults | Names/availability, parameter ordering and checked default contracts. Closed non-generic field defaults are checked when their declaring package is the root (#148); current imported-package checking skips that proof, a soundness gap tracked by bork-5yn71y. Require that fix before treating imported success as a proved artifact. Generic defaults and sibling-dependent constraints retain specialization/use-site obligations. |
| Lifetimes | Resource/scoped ownership identity, parameter/result lifetime relations, constraints on capture/escape and borrow/consumption behavior that callers rely on. If a relation is currently derived from a body, hash that relation or retain the body dependency. |
| Go interop | Binding signatures, opaque/mirror type metadata, conversion contracts and hidden ABI details actually used by consumers, with external Go inputs in the manifest. |

A dependency on a lookup depends on its candidate set, not just the selected
symbol. Initially hash whole interfaces and visible sets. This conservatively
invalidates more packages but catches a newly added overload/instance/rule or a
removed declaration. Symbol-level read sets can follow with negative-lookup
invalidation tests.

Ordinary runtime body edits should leave the interface unchanged when all
caller-visible contracts stay the same. That does not imply all caller work is
reusable: callers may consume implementation fragments too.

## Body dependencies, generics and compile-time evaluation

The manifest records dependency edges by kind: interface, implementation,
visible set, source location and external input. Recheck consumers only when
an edge they use changes. Separate rechecking from regenerating/relinking:
a runtime body edit can preserve a consumer's check artifact while invalidating
its build or any currently flattened output containing that body.

Current fact checking can inspect body-derived results and compile predicates
on constants. A predicate body edit can flip a caller's proof without changing
its signature. Key a query by canonical typed arguments, type arguments,
selected dictionaries, predicate implementation and the transitive callable
implementation/input closure. Until that closure can be recorded reliably,
include the whole reachable program implementation digest and rerun facts.
Do not reuse evaluator results, or successful artifacts that depend on them,
if reads/effects cannot be tracked; this includes no-edit in-memory session hits. Being
language-level pure is insufficient if unsafe Go can access unrecorded inputs.
No successful cached proof may bypass a predicate failure under changed inputs.

There is a prerequisite soundness fix (bork-5yn71y): the current clean driver
skips closed non-generic defaults in non-root packages. Imported user packages
must prove their own defaults once per build, even when no caller uses the
field. Fix that in the clean compiler first and test invalid imported defaults;
then cached package artifacts can carry successful declaring-package proofs.
Embedded standard defaults can rely on compiler-test validation tied to the
compiler namespace; this exemption must never include mutable user packages.
Until that fix is present, imported success cannot certify these defaults and
is not eligible for a universally proved artifact. Generic and sibling-dependent
use-site obligations remain separate. Root tests are still context-specific;
proving defaults does not mean running every dependency's tests.

Defaults used in generated callers also need implementation dependencies even
when their proof contract is unchanged. Their private helper dependencies remain
owned by the declaring package. Generic defaults are keyed by concrete type and
dictionary arguments. Preserve the existing distinction between declaration
proofs, specialized proofs and sibling-dependent use-site checks.

Generic bodies and future inline/comptime work (bork-pvx43y) are explicitly
implementation inputs. Key each specialization by defining package/declaration,
body digest, canonical concrete types, dictionaries, relevant contracts and
target/ABI. Two requests must not mutate the same cached specialization.
Initially generate caller-owned specializations with stable IDs and retain
package-level invalidation for their consumers. Sharing machine artifacts or
per-function checking is later work, after ownership, opaque types and private
access across Go package boundaries are settled.

Root-specific tests, mocks, root overrides of prelude functions, entry selection
and evaluator roots are compilation overlays. Never store a mocked callee as
the normal dependency artifact. Include the overlay in checking/generation keys
where it changes analysis; reuse only proven context-independent base artifacts.
Running tests or a program always runs the requested execution. Compilation
reuse must not reuse a prior runtime result or skip snapshots/assertions.

## Invalidation and deterministic scheduling

Resolve the import DAG in the existing deterministic order. For a changed
package, validate its dependencies, recheck it, then compare its interface and
implementation digests to the previous successful artifact. Propagate changes
along the corresponding reverse edges. Declaration locations have their own
propagation path. If checking fails, invalidate its consumers for this snapshot
and report errors under the existing phase gating and order.

A body-only edit normally rechecks the declaring package, preserves ordinary
consumers' check results and rebuilds the changed implementation. A changed rule
or instance candidate set invalidates every proof/resolution consumer that saw
that set. A predicate-body edit invalidates query/proof consumers; a new asset
invalidates the directory-embed consumer even if source bytes did not change.
Unknown dependencies fall back to the complete graph key.

Concurrency starts with independent source lexing/parsing or packages whose
interfaces and implementation inputs are ready and frozen. Use a bounded worker
pool. Workers collect local diagnostics; merge by the clean compiler's phase,
file/declaration order and diagnostic ordinal, not completion time. First make
the clean pipeline deterministic, including map traversals that currently
produce diagnostics. A reused artifact must not hide diagnostics otherwise
emitted from an unused user declaration or unsafe Go body.

## Generated Go packages

Prototype one Go package per bork package, a shared runtime/prelude package and
a small main/test/evaluator entry package in a staged Go module. Use stable
logical Go import paths and deterministic declaration names. An implementation
edit then changes the owner's Go file, allowing Go's build cache to reuse the
rest. Measure total staging, formatting, Go compilation and link time against
the current single-file layout; multiple packages are not an assumed win for
small programs.

Generated cross-package symbols may need Go-exported names even when private in
bork; checker visibility and private-construction rules remain authoritative.
Go type identity, union layouts, dictionaries, closure captures, conversions,
hidden needs and lifetime representations must stay compatible across packages.
Keep helpers with their owner or the shared runtime instead of copying global
mutable state. Root prelude overrides require explicit resolution so unrelated
packages continue to refer to their own prelude bindings.

Caller-generated generics can require private helpers/types and create synthetic
Go dependency cycles even though the bork import graph is acyclic. Start with
bridges/accessors only where their semantics and ABI are clear. For unresolved
cases, keep an affected cohort flattened, or use the existing whole-program
backend. Diagnose neither a new language restriction nor an internal Go cycle
to the user. Full splitting waits for a tested ownership strategy. Unsafe Go,
`//line` mapping, linkname directives, tests/mocks and predicate evaluators each
need explicit migration coverage.

Retain current `Emit` and CLI emit behavior during the experiment. If a split
backend needs a directory-output API, introduce it separately; do not silently
turn the existing single-Go-source output into a fragment that cannot build.
Each layout's cached and clean outputs must agree independently.

## In-memory session, watch and disk cache

Introduce a driver `Session` that owns immutable snapshots/artifacts and reverse
edges. Existing one-shot APIs create a short-lived session and retain their
behavior. First expose it to a benchmark harness, then propose
`bork check --watch` with one complete result per snapshot. For `--json`, use
newline-delimited complete result objects with a request/snapshot ID, diagnostics
and completion status; document this streaming schema separately from one-shot
JSON. Watch is a long-running process until interrupted; one-shot exit codes
remain unchanged. Publish only results for the current snapshot and retain
complete diagnostics rather than emitting confusing partial deltas.

A workspace-scoped daemon can reuse the same session across client invocations.
Requests identify workspace/module root, working directory, source selection,
mode, effective environment and compiler identity. Different configurations
must not share contextual results. Handle concurrent requests with immutable
snapshots, cancellation and bounded work; a cancelled request cannot publish
partial cache entries. A daemon mismatch or unavailable service falls back to
one-shot compilation. Persistent daemon protocol and unsaved editor buffers are
follow-up API designs, not required for the first cache implementation.

Store disk artifacts below `os.UserCacheDir()/bork/<compiler-digest>/<schema>/`.
Content-addressed entries contain a versioned manifest, immutable interface/IR
and, later, generated source/assets. Write temporary entries and atomically
publish complete validated entries. Verify manifest/digests on read; a missing,
corrupt or incompatible entry becomes a miss. Cache successful semantic artifacts
first; transient I/O/toolchain failures and partial proofs are not durable
successes. Bound disk and session memory by size with explicit eviction.

Propose `bork clean` for the current compiler namespace and `bork clean --all`
for all bork namespaces under that cache root. Coordinate with active sessions
using an epoch/lock: deletion cannot turn an in-flight write into a resurrected
old entry, and readers must tolerate disappearing entries. Do not clear Go's
module or build caches. Output binaries remain normal command outputs; let the
Go toolchain own executable/build caching initially.

## Delivery plan and acceptance evidence

1. **Dependency inventory and determinism.** Instrument cross-package reads in
   checking, lowering, contracts, effects, lifetimes, facts and generation.
   Establish stable IDs/source order, deterministic diagnostics and a frozen
   request snapshot. Keep the clean whole-program pipeline as the oracle.
2. **Session and whole-graph reuse.** Reuse a fully validated result only when
   the entire source/config/input graph matches. Reuse preserved source ASTs only with fresh request-local semantic state;
   retain stable source identity and span ownership. Benchmark repeated no-edit checks and bounded memory.
   This gives useful watch latency before pretending packages are isolated.
3. **Package interfaces and artifacts.** Refactor passes to consume immutable
   dependency artifacts. Begin with conservative whole visible-set/body edges
   and graph-wide fallback. Add package-level reuse only once invalidation tests
   cover each consumed contract. Enable bounded parallel package work afterward.
4. **Persistent cache and watch.** Version encoding, validate manifests, add
   clean/eviction, expose the session watch stream, then design a reusable daemon.
   Never accept a disk entry merely because an in-memory pointer graph worked.
5. **Go-package output experiment.** Compare clean/cached multi-file outputs,
   demonstrate Go-cache reuse, test ABI/private access and retain a flattening
   fallback. Decide rollout from measurements. Fine-grained read sets,
   specializations and per-function reuse follow demonstrated hotspots.

CI should run an opt-in cache-verification mode over the same captured inputs:
compile using the session/cache, compile with all bork reuse disabled, and
compare ordered diagnostics and all compiler-produced artifacts byte for byte.
A mismatch fails the test and records keys/manifests/invalidation reasons.
Predicate evaluation inputs must be replayable; untracked evaluators force
fresh evaluation, with dedicated deterministic fixtures for comparisons.

Use edit sequences, not just two identical builds. At minimum cover:

- Signature, type/alias/sealed-member, private-constructor and lifetime changes;
  ordinary body edits with unchanged contracts; declaration relocation.
- Effects, ambient needs/order, provider bundles and higher-order contracts.
- Fact promises, predicate/helper bodies and arguments, rules/conditions,
  newly added candidate instances and removed/failed lookup targets.
- Declaring-package defaults in imported user packages (including unused invalid
  defaults), private default helpers, generic defaults,
  sibling constraints and cross-package generic specialization/dictionaries.
- Root tests/mocks, rule testing, prelude overrides and evaluator entry roots.
- Added/deleted files/packages, import resolution, unsafe authorization,
  embedded file contents and added/removed directory assets.
- Go toolchain/target/flags, effective GOENV settings, dependency manifest/sums,
  local replacements and external package-driver changes.
- Failed-to-valid edits, valid-to-failed dependencies, eviction/corruption,
  simultaneous sessions, cancelled/superseded requests and clean during writes.

Measure fresh CLI, warm no-edit session, warm leaf-body edit, public-contract
edit and predicate/default edit separately. Report hit/miss/rechecked package
counts and reasons, parent/subprocess time, generated bytes and peak session
memory. Add multi-package and proof-heavy corpora to the existing harness;
hello alone cannot demonstrate package invalidation. Latency budgets should
come from quiet-machine measurements, not shared CI noise.

## Review decisions

The initial decisions are package-level reuse, conservative dependency sets,
immutable artifacts, exact clean/cached compiler-output parity and staged
rollout with a whole-program fallback. The first useful cache is a session's
complete-graph hit; skipping unchanged package bodies requires the checker
boundary refactor. Per-package Go output is a measured backend experiment.

Review should confirm the exported proof/ownership/default contracts and the
body-dependency fallback, then ship watch before a daemon once package artifacts exist. Disk reuse
requires the versioned artifact boundary and validation manifest. No universal body-only-edit guarantee applies to
predicates, generic/default implementations or body-derived proofs. Daemon
transport, editor-buffer protocol and cross-package specialization placement
remain follow-up designs with conservative fallback in the meantime.

## Phase 1: source-load snapshot boundary

The first implementation step captures the source loader's reads in a private
`sourceSnapshot`: file bytes, source-directory membership, directory/file
selection and module-resolution reads (including missing ancestor `bork.mod`
files). Relative reads resolve against the captured working directory; diagnostics
retain the original display paths. Replays receive copies of source bytes and
membership and construct independent ASTs. The inventory reports captured filesystem
operands, read kinds and content digests in deterministic order. IO preserves
path components (including symlinks followed by `..`); module identity retains
its existing lexical normalization.

Loading validates the captured bytes and membership after traversal and retries
once if inputs changed. Equal sizes or mtimes cannot establish freshness.
Continuous edits return a source-input-change error. This validation covers the
load attempt, not atomic filesystem isolation for the entire compilation.

This is deliberately **not a semantic cache key**, and enables no reuse of
checked results. Embedded prelude/std contents belong to the compiler namespace;
ordered logical source paths and selected root/context remain required in a
future compilation manifest alongside this filesystem inventory. The next
boundaries still need explicit capture and invalidation:

| Read / context | Existing consumers and required follow-up |
| --- | --- |
| User Go manifests/checksums | Captured alongside sources; one frozen merged module is shared by metadata loading, predicate builds and final staging. Test staging hooks receive private copies. |
| Embedded assets | A private asset snapshot freezes bytes, component kinds, symlink checks and recursive membership (including empty directories and failed reads), with independent replay and content validation. |
| Effective Go environment/toolchain | One request freezes process/effective Go settings and launcher/driver selection, shared by metadata, predicates and builds. Standard-name caches use a configuration/launcher namespace. Full toolchain/external metadata contents remain to inventory or bypass before result reuse. |
| Compile-time evaluator | `Facts` invokes generated predicate programs. Track arguments, implementation/input closure and execution context; untracked reads/effects prevent reuse of any success depending on evaluation. |
| Graph and mode | Whole-graph source content covers semantic bodies, facts/rules and candidate-set changes initially. Root selection, source locations, tests/mocks and entry/evaluator mode must remain explicit context. |
| Runtime effects | `Run`, test execution, assertions and snapshot updates remain fresh executions. Dependency-management commands mutate manifests; they are not cached compilation results. |

Only after these inputs are complete may Session artifacts reuse immutable
outputs. The clean-versus-cached verifier must land before or alongside that
reuse and compare the same captured snapshot; the source snapshot by itself
cannot satisfy the verifier's whole-compiler contract.

### Phase 1: frozen Go manifests

Compilation requests capture user `go-deps.mod`/`go-deps.sum` and module-resolution
lookups through the same source snapshot. They merge user and embedded standard
manifests once, then validate the combined source/manifest inventory before
semantic checking. A bounded retry covers edits during capture; syntax/import
diagnostics retain precedence over manifest validation errors. Missing user
manifests are inventoried too.

A private compiled-program context carries immutable module bytes through Go
name/type loading, compile-time predicate builds, final executable/test builds
and additional `describe` proofs. Staging receives copies so test hooks cannot
mutate a request's module. Independent helper calls used by compiler tests retain
one-shot captures. Effective Go configuration, external module contents and
execution effects remain separate inputs to capture before Session reuse.

### Phase 1: asset input inventory

Asset capture records each request's resolved source-package directory, intrinsic
kind and relative path. It freezes observed component kinds, recursive membership
(including empty directories), file bytes and failures. Invalid UTF-8 bytes remain
in the inventory even when `ReadString` rejects them. Capture preserves the
existing `os.Root` containment and symlink rejection; validation repeats those
checks and compares actual contents rather than sizes or mtimes.

All requests are captured and validated before publishing their files to `Info`.
One retry handles edits during capture; continuous edits produce diagnostics.
Replay and staging get independent bytes and caller-owned staging paths. The
private compiled-program context retains the asset inventory alongside the
source/manifest inventory. This adds no checked-result reuse: effective Go
configuration, external metadata/toolchain inputs and evaluator execution remain
required boundaries before Session artifacts can be published or reused.

### Phase 1: effective Go configuration

A private request context captures process environment, effective `go env` values,
resolved Go launcher, launcher content identity and package-driver selection.
Generated Go subprocesses receive explicit settings with `GOENV=off`, so later
saved-config edits do not change the request. Target sizing, metadata lookup,
compile-time predicate environment and final compilation share the context.
Runtime programs and test execution retain fresh runtime environments. Missing
Go remains a deferred error for pure check-only requests.

`go/packages` resolves its launcher using the parent's process PATH rather than
`Config.Env`. Metadata therefore runs in a private child of the compiler
executable whose PATH and driver choice come from the context. A private Go-only launcher
shim pins `go` while preserving auxiliary executable lookup from the captured
PATH. External driver responses retain their protocol fields; `NotHandled`
fallback also uses the captured launcher. That child uses
the library's metadata loader and documented driver wire format; the parent
still decodes type/export data. The wire format omits module provenance, so
standard-name caching positively checks source-file locations under the
captured GOROOT instead of treating missing Module as evidence of standard
origin. External/custom-driver names stay fresh.

The namespace includes effective configuration, resolved driver choice and Go
launcher bytes. This is not a full compilation-result key: complete toolchain
contents, mutable external package/driver inputs and evaluator effects remain
untracked. No Session result reuse is enabled by this step.

## Phase 1: clean comparison baseline

`TestCompilationReplayMatchesClean` compiles deterministic fixtures through the
normal semantic pipeline, then reparses their frozen source capture into a
separate AST and rebuilds semantic state. The second run reconstructs manifests
from frozen reads and installs owned copies of captured assets. It compares main
and test Go, dependency manifests/sums, staged asset paths/names/bytes, rendered
errors and ordered diagnostic JSON. It also checks that replay discovers no
additional source or asset dependencies. One fixture deletes its source and
assets between runs: replay retains the captured output while a new request sees
the deletion. CI exposes this as the Captured-input parity check.

This establishes the byte-comparison baseline before introducing result reuse.
Both runs still execute semantic checking, Go metadata loading and predicate
execution; this is not a Session hit or proof that external effects are tracked.
The first Session PR must route its reused artifacts through the same comparison
and extend the fixtures to edit/invalidation sequences. Unknown metadata or
evaluator inputs must still force fresh compilation.

## Phase 1: conservative complete-result Session

`driver.Session` exposes checking with owned warning diagnostics and emission
with owned Go bytes. It serializes requests and retains at most one successful
result; a miss clears the previous entry. Parsed ASTs and `check.Info` are never
retained or returned. Failed requests always compile again. Existing one-shot
APIs retain their mutable graph behavior and do not use this result cache.

A hit requires the same request path/mode and validated Go configuration,
unchanged source/manifests and assets by content, and unchanged builtin
package-name inputs. For recognized Go 1.26/1.27 launchers, Session inventories
saved/SDK environment files, telemetry mode, enclosing module/workspace files
(including missing candidates), temporary/cache directory availability and modes,
and default C compiler lookup. Each request compares the process environment,
resolved launcher/driver, launcher bytes and this inventory. On Linux the
executing compiler's inode and mode prove its immutable executable identity;
other hosts hash its executable bytes. This avoids repeating `go env` on hits.

Positive standard-source provenance permits validating package-directory
membership and all immediate file bytes instead of repeating the filtered
metadata-only `-find` query. Ignored/test sources and assembly are included.
Unknown launchers, unsupported Go versions, switched toolchains, workspaces,
Go flags, FIPS modes and unproven configuration dependencies retain full
configuration/name reloads. Filesystem content checks ignore mtimes and sizes.
These inventories are metadata-only evidence, not a complete SDK/export-data or
execution key.
Go type/export metadata, custom drivers, unavailable/external package-name
origins and any compile-time evaluator invocation bypass result reuse. Windows
drive-relative/rooted filesystem operands also bypass until their drive context
is captured. Input changes during compilation prevent storing the result.

`TestSessionMatchesClean` extends the CI comparison to actual hits and owned
warnings. Other tests cover concurrent requests, equal-size/mtime source/SDK/asset
changes, directory membership, invalid unused defaults, failed-to-valid repair,
configuration/mode changes and evaluator/export-data bypasses. This is an
in-memory complete-graph boundary; package interfaces, disk artifacts and runtime
results remain separate work.


### Launcher digest validation

In-memory launcher digests can reuse a matching path/device/inode/size/mode/
mtime/ctime tuple after more than two seconds of monotonic observation. During
that warm-up the compiler hashes bytes again; matching bytes and metadata retain
the observation start, while changed metadata resets it. This assumes timestamp
granularity at most two seconds and ordinary change-time semantics. FAT/exFAT
and platforms without inode/change-time evidence always hash. The rule does not
compare process wall time with a remote filesystem's timestamps.

Digest capture hashes one open descriptor and verifies its before/after identity
plus the final pathname identity. A persistent disk entry must never trust a
serialized observation time: every process must hash once and establish its own
monotonic observation period before using stat-only validation. Configuration,
SDK source, module and embedded-file content validation remain separate.
