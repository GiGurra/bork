# Cached compiler evaluations

Status: proposal. Predicate proofs and explicit comptime evaluations still run
fresh, and their existing Session/result-cache bypass markers remain enabled.
This design supplies the shared execution-receipt boundary needed before either
kind of result can be reused. It extends the input/result layers described in
[disk-cache.md](disk-cache.md) and the value contract in [comptime.md](comptime.md).

## Outcome and acceptance cases

A successful predicate evaluation is a deterministic ordered list of booleans
only when its complete executed program and closed arguments have been proven
independent of ambient state. Cache that execution result, then let the ordinary
Facts pass interpret each result and issue diagnostics. A false predicate is a
valid evaluation result; a failed compilation is never an enclosing artifact hit.

Use `examples/config` and `examples/http_server` as acceptance cases. A tracked
check of each reports evaluator use and no Go export-data loading. Their current
bypass therefore can be removed by complete proof receipts without separately
caching export data. Their predicates call prelude `String.byteLength`, whose
implementation uses unsafe Go `len`; a blanket rejection of every unsafe
implementation would leave these cases ineligible.

Measure ordinary, first eligible miss, in-process evaluation hit, fresh-process
evaluation hit, enclosing result hit and end-to-end build separately. Retain
byte-for-byte clean/cached emission and identical diagnostics. Never claim
execution reuse merely because the staging directory or Go object cache is warm.

## Staleness model

This cache protects against accidental staleness: source edits, SDK upgrades,
switched toolchains, directory membership changes and equal-mtime rewrites.
It does not defend against an adversary modifying build inputs only while the
Go build runs and restoring them before validation. Build-closure certification
uses complete content observations before and after the build; it assumes an
accidental input change remains observable at a validation boundary. A complete
change-and-restore between both observations is outside that contract. This is
not a claim that endpoint hashing proves every byte read by an adversarial build.
Keep the existing frozen Bork source/module/generated inputs; there is no planned
copy of the entire SDK/toolchain. Permission errors or unstable observations
always decline reuse.

## Shared eligibility boundary

Analyze checked, resolved declarations, starting from every actual query in the
batch or each explicit computation. Walk ordinary and higher-order calls,
instantiations, literals, captures, defaults, predicates, dictionaries, providers,
rules and implementation selection. Retain a conservative complete checked graph
identity initially; narrowing the semantic key is a later measured change.
Unknown call targets, unresolved closures or dependency kinds decline reuse.

Initially accept deterministic pure Bork operations and explicitly audited
compiler intrinsics. Decline user/foreign unsafe Go, build-effect reachability,
ambient effects/needs, unresolved callbacks and unordered traversal with observable
results or failures. Check those restrictions transitively, including defaults and
wrappers that look pure at the call site. Tests/mocks keep their distinct mode,
roots, visible declarations and locations; do not reuse across those overlays.

An intrinsic entry names the resolved declaration identity, its checked signature,
its implementation digest in the compiler namespace and a versioned behavior and
dependency contract. Package spelling or a pure signature is never authorization.
The first proposed audit is precisely prelude `String.byteLength`: Go `len` of
that closed String returns its byte count, including invalid UTF-8; it reads no
filesystem, environment or locale. Native target and integer representation are
part of the contract. Newly added/changed implementations decline until audited;
wrappers/defaults/dictionary/provider calls are audited separately and transitively.
Do not authorize every unsafe use of Go `len` by its textual spelling.

Include generated evaluator support and initialization in the audit. An eligible
Bork call graph alone does not prove that imported Go initialization or generated
helpers lack ambient inputs. Compiler-owned runtime helpers require explicit
contracts pinned by the actual compiler image. Foreign packages and cgo decline
initially, even if the queried function appears deterministic.

## Execution identity and owned receipts

Use the existing actual compiler-image SHA namespace, plus independent versions
for execution-closure analysis, intrinsic contracts, query/value encoding,
evaluator protocol and value-limit policy. Never trust a Go build ID or persisted
stat tuple as content evidence. Native GOOS/GOARCH, selected toolchain, relevant
flags and raw captured configuration must match before any hit.

An execution receipt owns canonical data, not syntax/checker pointers. It records:

- Complete checked source/module identities, mode, roots, source locations and
  selected declaration/default/rule/provider/dictionary dependencies.
- Exact generated evaluator program bytes and query batch order. Each query
  includes resolved predicate identity, concrete type arguments/parameters,
  dictionaries/facts and canonical closed argument values. Preserve And/Or shape;
  neither Query.Text nor an unordered set of results is a key.
- Earlier comptime values in evaluation order, with their typed canonical
  identities and their validated execution receipts.
- The complete Go execution/build closure and configuration needed to certify
  the generated evaluator, including compiler/linker tools, runtime support,
  transitive SDK source/assembly/embedded inputs and file/directory membership.

Existing name/config receipts only certify metadata used during checking. They
are not execution receipts. The closure collector must identify and inventory the
complete inputs needed by Go compilation/linking for the accepted native, non-cgo subset; incomplete or
unsupported toolchain behavior declines reuse. Capture `go list -deps` dependency
discovery from the pinned context and staged program, freeze its module inputs,
and inventory all relevant source/tool/config bytes and membership. Any changed
input forces fresh discovery/evaluation. A directory-stat match or launcher SHA cannot replace
SDK content validation. Measure the collector before promising fresh-CLI wins.

Dependency discovery also needs endpoint coherence. After initial `go list -deps`
and content/membership capture, validate that inventory, rediscover the complete
package-resolution set using the same pinned staged inputs, and validate the
inventory again. Require the two resolved dependency sets, selected files and
selection/search evidence to match; otherwise retry capture or decline. This
catches a persistent SDK edit adding an import between initial discovery and its
first content capture, which could otherwise record new source bytes while
omitting the newly linked package. Any missing or newly discovered input declines
until captured and rechecked. This guard needs no SDK copy and is part of the
collector cost measurement. Hits validate the fully established receipt rather
than trusting a single earlier discovery result.

Capture and execution use the same pinned Go context, staged generated program
and frozen module inputs. The key includes generated source bytes, the selected
launcher and Go compiler/linker/assembler tool identities, and content digests of
the SDK/module packages actually linked, including support/init code and embedded
or assembly inputs. Record package-file membership and relevant selection/search
inputs as well as file bytes; a transitive package set alone does not certify
resolution after membership/configuration changes. Disable toolchain autodownload
for the pinned build. Validate the complete captured closure before and after
execution; any mismatch discards certification. Unknown tools/modes/inputs decline.

Under the accidental-staleness model above, these endpoint content checks suffice
without copying and managing an SDK/tool closure. Ordinary upgrades, edited
package files and switched tools that remain changed fail validation, including
equal-mtime changes. If an actual accidental change-and-restore scenario becomes
material, reproduce it and measure stronger coherence protection before extending
this contract. Measure dependency discovery and content validation before enabling
fresh-process reuse; SDK cost is a performance prerequisite, not permission to
replace content proof with launcher/directory stat matches.

A compiled executable SHA may serve as a witness in tests, but rebuilding that
executable to discover the key on every hit would retain most of the cost this
cache intends to remove. The final receipt must validate its captured build
closure without invoking Go compilation on a hit. Unknown execution inputs,
including unsupported linking modes or ambient import initialization, decline.

## Driver API and certification

Proposed private boundary, with concrete names settled in the implementation:

1. Preserve ordinary scheduling before lookup: perform cycle/dependency
   detection and recipe preflight, using already validated earlier values.
   A cached recipe never proves a presently cyclic dependency or relies on an
   enclosing runtime guard. Then `prepareEvaluation` takes the captured compilation context and actual checked
   query/computation and returns an owned candidate only if closure analysis is
   complete. It does not clear the usage marker.
2. `evaluationReceipt.current` validates owned source, toolchain, configuration,
   intrinsic and execution-closure evidence. Session validation can retain
   process-local guards; persisted receipts re-establish content proof per process.
3. `evaluateCached` validates a candidate receipt, looks up its exact identity,
   and either returns owned canonical result bytes or performs fresh execution.
   The value layer reconstructs against the current type graph and runs
   contextual/result/field obligations before certification. Revalidate around
   execution/reconstruction; failed reconstruction or obligations cannot qualify
   the enclosing artifact.
4. An evaluation tracker records every actual invocation and its receipt/result
   identity. The enclosing artifact qualifies only if every invocation is
   certified and no other bypass reason remains. One eligible generated program
   or one hit does not certify all queries in the compilation.

Predicate results must contain exactly one strict boolean per ordered query.
Reject unknown tokens, wrong counts, duplicate/unknown envelope fields,
unsupported versions and corruption. Bound receipt/result decoding before
allocation, including canonical typed IDs, ordered Map pairs, union/variant tags,
exact numeric widths/float bits/String bytes and node/depth/result limits.
Malformed fresh output is a compilation failure; malformed, corrupt or stale
persisted data is a miss followed by ordinary execution. Return cloned values;
share no mutable
semantic objects. Panic, timeout, malformed output and infrastructure errors are
never cached. Successful typed error alternatives in explicit comptime remain
values, following comptime's existing contract and limits.

Comptime owns canonical typed value encoding, reconstruction and subsequent
contextual Facts checking. Perf owns shared eligibility/execution receipts,
proof-result lookup and enclosing-artifact certification. All existing preflight,
post-evaluation type/predicate checks and native-target/value-policy gates also
apply on hits. A matching content key never exempts a value from current limits.

## Rollout and validation

Land the design before implementation. First add closure analysis/owned receipts
without enabling hits, with reviewed intrinsic contracts and declining unknowns.
Then add Session proof hits; integrate ordered comptime values separately through
the same receipt boundary. Only after clean-versus-hit parity and measurements
add persistent evaluation entries through bounded storage/lifecycle/clean.
Do not remove an enclosing evaluator bypass until every actual invocation is
certified. Build-read/foreign unsafe programs remain ordinary bypass paths.

Tests change predicate/default/rule/provider/dictionary implementations, types,
query order/And/Or structure, earlier computed values, source locations, overlays,
SDK content with equal mtimes, membership, compiler image, tool/config/target and
policy versions. Unknown/new intrinsics and unsafe transitive wrappers decline.
Exercise invalid UTF-8 byte-length inputs, false predicates, panic/timeout,
corruption, ownership, incomplete receipts and mixed eligible/ineligible batches.
Compare emitted bytes and diagnostic order against clean checking in Session and
fresh CLI runs, including both realistic acceptance examples. Include a mixed
eligible/ineligible proof or computation and a valid value hit followed by a
false current contextual predicate; neither may publish an enclosing result hit.
Cache failure always falls back to ordinary checking. Known bypass programs
capture no additional execution-cache inventory; ordinary attempted source/rooted
snapshots remain necessary for Watch recovery.
