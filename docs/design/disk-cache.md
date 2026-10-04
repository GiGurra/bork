# Persistent compiler cache

Status: implementation plan for the next bork-h5rkt4 slices. Builds remain
correct with an absent, full, unavailable or corrupt cache. This extends the
[complete-graph Session](incremental.md) and the stable Go staging work in
[PR #213](https://github.com/GiGurra/bork/pull/213); it does not serialize checker
pointers, reuse execution results or introduce package interfaces.

## Stored results and eligibility

Persist successful complete-graph outcomes: ordered owned warning diagnostics,
emitted Go bytes when requested, captured module/asset bytes needed to build
those bytes, source-path mappings and a versioned input receipt. A check-only
outcome cannot answer an emission request; an emission outcome may answer a
check after verifying its entire receipt. Failed checks stay uncached.

Keep the Session's conservative evaluator, Go export-data and custom-driver
bypasses. For the first disk schema also require positive standard-package name
provenance with complete SDK name inventories, a supported Go-configuration
receipt, portable file-identity evidence where required, and representable
filesystem observations. Unknown origins, nonportable observations, nonempty
rooted build-read inventories and incomplete/error receipts force fresh work.
Pure Bork graphs with provable defaults remain eligible; imported user packages'
default obligations are part of their checked graph outcome, not discarded.

Runtime execution remains fresh for `run`, `test` and every evaluator. In
particular, changing an external file read by unsafe Go must still cause the
existing evaluator bypass even when Go itself reuses object compilation.
Persistent checked-result artifacts and mutable Go staging are separate layers.

## Namespace, lookup and receipt

Use SHA-256 of the actual running compiler executable bytes, plus explicit
artifact/configuration schema and output-layout versions, as the artifact
namespace. Do not use a version string, build ID or executable mtime as the
compiler digest. Hash once per process; Linux may read `/proc/self/exe` to
identify the running image, while child-bridge executable identity is validated
separately. On other platforms, persist only when a supported OS mechanism
proves that the opened bytes are the running image. Reopening os.Executable()
after a replacement does not prove this; absent that evidence, bypass persistent
results while retaining Session and temporary/stable Go staging behavior.
Compiler replacement, local development builds and embedded standard
library changes therefore cannot cross namespaces.

An index key includes the raw requested path, captured working directory and
request mode/options. Lexical aliases cannot share diagnostics or generated
caller strings without proving location equivalence. Do not normalize the raw
operand before filesystem validation: `link/../file` and missing intermediates
have existing observable semantics. Test code-generation options belong to its
mode; runtime parallelism, snapshot updating and execution are not cached.

The receipt preserves every observed source/module read, including negative
lookups, directory membership/kinds, working directory and relevant path
resolution context. Store a canonical sorted representation; only explicitly
supported error classes are encodable. On a hit, validate contents/membership,
not mtimes, and retain captured module/asset data for generation/building rather
than reopening semantic inputs. Assets also require their rooted/component
identity checks; a content-summary digest alone cannot replace SameFile checks.
The encoding must either reconstruct that evidence safely on this platform or
reject the entry. A first focused receipt implementation may bypass assets
until their identity encoding is covered by tests.

Standard Go names require the recorded SDK directory membership and all
immediate file-content digests, plus supported configuration evidence. A
configuration receipt includes raw environment, resolved Go/driver/bridge,
launcher digest, saved GOENV and SDK go.env, telemetry setting, cwd module/work
candidates including negatives, executable availability and cache/tmp modes.
Unsupported flags/toolchain switching/wrappers retain full discovery/bypass.

The compiler/configuration/source receipt APIs should also be reusable by the
comptime cache, but evaluated-value receipts additionally require a proven
execution closure, rooted input identities, dependency/proof context and shared
versioned value limits. Keep evaluator usage markers until that boundary is
closed; limits and cross-target hardening land before value reuse.

No stat tuple is trusted merely because it was persisted. Re-establish launcher
byte evidence once per process and a new monotonic observation window before
using the existing two-second shortcut. Do not deserialize stable-since times.
A matching proven configuration receipt can reconstruct pinned Go values/env
without rerunning `go env`; this must preserve all existing configuration-change
tests. If it cannot, perform fresh discovery. Measure this separately: paying
full first-Session seeding on every disk hit would defeat small CLI workloads.

Validate the receipt before serving and check it again around slow reconstruction
or publication work. An unstable input set forces fresh work/retry. The cached
artifact contains owned immutable data; no loaded syntax or shared semantic maps
are reconstructed merely to return diagnostics or emitted bytes.

## Encoding and publication

A versioned envelope contains a canonical body and its checksum. The checksum
covers the entire body: namespace, schemas, mode/key, full receipt and payload;
only the checksum field itself is excluded. Check namespace/key against the
requested lookup too. Read at most a fixed bound before decoding; reject
truncation, unsupported versions, duplicate/unknown fields, malformed receipts
and checksum mismatch as misses. Dropping a dependency from a receipt must be
detected just as changing emitted bytes is.
Use a 64 MiB maximum encoded artifact initially; an oversized result compiles
normally without persistence. This is a cache limit, not a language limit.

Write complete bytes to a private temporary file, close it, then publish by
rename while holding the cache mutation lock. Concurrent readers either open
an old complete artifact or a new complete artifact. Read/decode/validation
errors are misses; cache write errors never replace successful compilation
with an error. Remove abandoned temporary files during maintenance. Verify all
compiler-produced payloads against clean compilation in the parity tests.

## Shared cache budget and staging lifecycle

Default to a combined 512 MiB budget: 256 MiB for result artifacts and 256 MiB
for staged source trees, with at most 1,024 entries per layer. Track actual
stored bytes, not only source bytes. A result over its per-entry bound, or a
stage tree over the staging budget, still builds through the uncached path.
Budget limits may become explicit options after behavior is measured. Limits
apply to retained published entries. Replacement temporaries are bounded per
request and may transiently coexist with the old tree; account their maximum
separately. Before publication, evict eligible inactive entries and recheck
admission under the mutation lock. If busy entries prevent fitting the new
entry within byte/count limits, do not publish: return the fresh result without
persistence, or build Go in temporary staging. No unbounded retained overshoot
is allowed merely because maintenance skipped busy entries.

Track last use on both layers, including reused stage trees. Add a small
versioned staging metadata record with canonical program directory, mode,
pinned-context identity and last-use evidence; a hashed directory name cannot
reveal whether its program was deleted. Standalone-file result entries also
record the selected target, so maintenance can recognize a deleted file even
when its parent remains.

Maintenance removes entries whose recorded program/target is conclusively
absent, then least-recently-used entries until budget/count limits hold. A
permission error is not proof of absence. Corrupt metadata is a cache miss and
eligible for removal, with bounded traversal and no following entry symlinks.
All maintenance paths are restricted to compiler-owned entries; Go's own build
cache and project outputs are outside this policy.

Stage eviction acquires its existing per-entry lock without blocking background
maintenance: skip busy entries, never remove a Go subprocess's input tree.
The next maintenance pass revisits skipped entries. Stage metadata/publication
updates happen under the same lock. A cache mutation lock serializes result
publication, accounting and deletion. Publishers and explicit cleaners acquire
a stage slot before the mutation lock. Background eviction may inspect under
the mutation lock but must acquire stage slots nonblocking and skip busy ones;
it never waits for a slot while holding the mutation lock. Explicit cleanup
selects candidates under the mutation lock, releases it, waits for the slot,
then reacquires the mutation lock and rechecks the candidate before deleting.
This order prevents an eviction/build deadlock while allowing publication to
account bytes under both locks before releasing the mutation lock for Go build.

Use a fixed pool of 256 hashed staging lock slots outside evicted entries and
never unlink their files while clients may use them. Hash collisions only
serialize unrelated builds. This bounds coordination records without unsafe
lock-file reclamation: unlinking a live lock can let two processes lock distinct
inodes for the same logical key. Byte budgets exclude these fixed small records.
Legacy per-entry locks, if any were published by an older schema, must remain
until a separate process-coordination protocol can prove they are unused;
mtime is not such a proof. Cache permission/platform/locking failure keeps
temporary Go staging and fresh compilation available.

## `bork clean` and `bork clean --all`

`bork clean` removes result artifacts for the running compiler namespace and
all staging entries in the current staging schema. `--all` also removes result
namespaces for other compiler digests and all recognized staging schema
versions. Both remove abandoned publication files and cover staged trees as
well as checked-result artifacts. Neither removes user outputs or Go's cache.
Select entry identities under the mutation lock at invocation: clean removes
that selected set, including replacements of those identities before deletion.
Concurrent publications of new identities after selection may remain. Do not
promise an empty-cache linearization point while other clients build.

Preserve coordination directories/lock inodes. Explicit cleanup waits for each
active stage build to finish, or is interruptible before removing that entry;
background eviction instead skips active builds. Concurrent publication and
cleanup follow the same mutation/entry lock order. A missing cache succeeds
with zero removals; an inaccessible cache reports a clean error rather than
pretending it was emptied. Report entry counts and bytes removed concisely.

## Integration and focused PR order

1. Add owned versioned receipts and compiler-digest identity, with
   serialization/rehydration parity tests and no disk hits enabled yet.
2. Add bounded atomic result storage and supported receipt validation; prepare
   a real CLI check/emission path behind an internal opt-in test gate. Keep
   AST-returning APIs fresh. Automatic reads/writes remain disabled.
3. Add stage metadata, shared accounting/LRU, deleted-root cleanup and clean
   commands, then enable the CLI cache. Automatic persistent reads/writes must
   not become available before this lifecycle work lands; growth is bounded
   from its first enabled release.
4. Extend artifact modes/assets only after their identity and output tests pass;
   measure first CLI request, subsequent fresh-process hits, warm Session hits
   and realistic full builds separately.

Every slice retains clean-versus-cached ordered diagnostic/Go-byte comparisons.
Tests must cover equal-mtime edits, negative appearance, directory/component
replacement, saved-env and launcher changes, compiler/schema invalidation,
corrupt/truncated/oversized entries, concurrent processes, unavailable cache,
budget/count eviction, deleted roots and clean during active build/publication.
Comptime/external-file tests must still execute freshly and record bypasses.
Publish before/after medians and min/max ranges; a hit slower than an uncached
repeat is a profiling issue to resolve before treating the cache as successful.

## Detached eligible-miss publication (internal gate)

Eligible misses compile normally and return their output without SDK inventory
certification. On Linux, the experimental gate can hand an owned job to a detached
publisher. The job contains frozen source reads (including errors and membership),
the exact process environment, configuration/launcher identity used by checking,
module bytes, positive standard metadata names, generated bytes and warnings.
The child reloads configuration and SDK contents, independently re-queries and
compares the names used by checking, validates the complete receipt and hashes its
actual running image before writing. Any mismatch skips publication; the parent's
already returned result is unaffected. No evaluator or export-data result qualifies.

Eight permanent hashed admission locks bound concurrent publishers. Admission is
nonblocking; busy slots skip publication. Jobs have a conservative 1 MiB encoding
budget. A job file is unlinked immediately after opening, before any bytes are
written, and passed only by descriptor; terminated processes retain no spool
payload. The child executes the parent's actual image descriptor, becomes a new
session/process-group leader, inherits no terminal or standard streams, and lowers
CPU and IO priority where Linux permits. Its 15-second self-timeout kills its entire
process group, including Go subprocesses, and remains armed through completion.
Admission remains held until process exit. Result publication subsequently follows
the existing result-slot/mutation lock order. Future cleanup must coordinate these
admission slots when selecting pending publications and preserve their lock inodes.

`BORK_CACHE=off` explicitly disables result-cache lookup and publication. An
unwritable cache, oversized job or unavailable process/locking facility skips
publication and returns ordinary output. Other platforms currently skip detached
publication. Automatic persistent use remains disabled until accounting, eviction
and cleanup land. Tests opt into an explicit completion pipe and optional barrier;
they wait for publication or process exit without sleep-based polling. These pipes
are never inherited by Go subprocesses.

The first lifecycle slice applies a 256 MiB/1,024-entry policy to result artifacts
across all compiler namespaces. Publication inventories recognized artifacts under
MUTATION, removes abandoned result temporaries, and evicts oldest inactive entries
using nonblocking result-slot acquisition. Both the old generation and the entire
new temporary must fit during replacement. Busy entries or a scan beyond 4,096
directory records cause publication to skip rather than overshoot. A validated hit
updates its last-use hint with nonblocking SLOT/MUTATION acquisition. Hints affect
only eviction, never receipt validity. Staging-v2 accounting, deleted-target cleanup
and explicit clean commands remain prerequisites before automatic use.
