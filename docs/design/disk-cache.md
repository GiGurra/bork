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
separately. macOS queries PROC_PIDREGIONPATHINFO for a known Go code address
and matches its mapped vnode dev/inode/size with the opened image descriptor
before hashing. Installed compiler images remain immutable while running;
upgrades replace pathnames atomically. Kernel query failures decline. On other
platforms, persist only when a supported OS mechanism identifies the running image. Reopening os.Executable()
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

Installed Go SDKs are treated as immutable, matching Go's object-cache policy.
In-place edits of GOROOT or the Go launcher are unsupported. Capture/publication
may retain complete SDK name content/membership evidence, but a checked-result
hit identifies the installation by GOROOT path, Go version, and the resolved
launcher and GOROOT VERSION file's device/inode/size/mode/mtime identities.
No SDK content hashing or membership walks run on this hit path. Toolchain
replacement, upgrades, path switches and missing VERSION files decline reuse.
Version 2 Go receipts record this explicit policy identity separately from
content receipts; unknown/missing policy evidence declines the cheap path.

Configuration checks still include raw environment, resolved Go/driver/bridge,
saved GOENV and SDK go.env, telemetry setting, cwd module/work candidates
including negatives, executable availability and cache/tmp modes. Compiler
identity still uses its actual executable content digest. Unsupported flags,
toolchain switching and wrappers retain full discovery/bypass. Source/asset
content and membership checks are unchanged.

The compiler/configuration/source receipt APIs should also be reusable by the
comptime cache, but evaluated-value receipts additionally require a proven
execution closure, rooted input identities, dependency/proof context and shared
versioned value limits. Keep evaluator usage markers until that boundary is
closed; limits and cross-target hardening land before value reuse.

Persistent stat identity is trusted only for the explicitly immutable installed
SDK policy above. Other content-certified paths re-establish launcher byte
evidence and a monotonic observation window before using the existing two-second
shortcut. Do not deserialize stable-since times.
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

## Age-based lifecycle and direct lookup

The next lifecycle slice replaces aggregate size/count budgets and build-time
inventory/eviction with Go's build-cache retention policy. There is no retained
byte or entry-count limit, accounting ledger, admission scan, or eviction during
a build. Artifact decoder and receipt bounds still apply to individual results;
they protect decoding and do not measure total disk use.

Use direct hash-derived paths with two-hex-digit fan-out at every potentially
large directory level. New layouts are `results/v2/<compiler-prefix>/<compiler>/
<key-prefix>/<key>.json` and `stage/v3/<key-prefix>/<key>/tree`. Result keys retain
compiler/request identity and receipt validation. Stage keys retain canonical
program root, mode and pinned Go context, so ordinary edits keep stable absolute
package paths for Go's cache and future multi-package output. No directory list
or global scan belongs to lookup, hit validation, use marking or publication.
Older layouts remain recognizable by explicit cleanup; new publishers use only
new layouts and matching permanent lock mappings.

Fresh-CLI lookup uses an untrusted locator at
`indexes/v1/<key-prefix>/<request-key>.json`, containing only a bounded compiler
namespace digest. It selects one direct artifact path; the compiler namespace
and complete semantic receipt still authorize reuse. Alternating compiler
versions may replace the locator and cause correct misses and republication.
Locator publication is best effort: its failure never fails artifact publication
or compilation. Locators use the same hourly marking and daily age rule as
results; an orphan or missing locator merely causes a miss.

The first implementation slice provides these layouts and removes admission
scans from result and staging publication. The next slice coalesces use marking
at one hour: validated result hits mark the artifact and only a still-matching
namespace locator; stages keep an independent `used` marker across tree swaps.
Busy result lifecycle locks skip marking, and marker failures never fail a build.
Bounded daily trim and the population matrix are implemented. Automatic Linux/macOS
CLI result reuse now follows the validated rollout described below.

Mark successful result hits and stage use with approximate last-use mtimes,
writing at most once per hour. Publications naturally mark new entries used.
Stages keep a separate use marker so replacing their tree does not defeat the
hourly touch rule. Future timestamps retain entries conservatively. Mtime is
only retention evidence; semantic receipts still validate contents/membership.

A small `trim.txt`-style record gates maintenance to one daily trim cycle. Trim
removes entries unused for five days, with an additional one-hour margin for
coarse use marking, as Go does. Missing/corrupt/far-future trim state starts a
new cycle. A separate nonblocking maintenance lock prevents duplicate workers.
Trim runs in the detached publisher or a bounded detached maintenance child,
after useful command work, never on the request's critical path. Existing child
runtime/priority/terminal/platform restrictions apply; unsupported or unavailable
maintenance skips rather than falling back to a synchronous scan.

A huge cache must not require an unbounded child or directory materialization.
Stream sharded inventories in bounded batches, persist cycle progress, and cap
shards and wall time per worker. Partial cycles resume without
starting another daily cycle; only a completed cycle advances completion state.
Treat cursor/progress records as best-effort maintenance state, never lookup
indexes or semantic evidence. Tests must exercise interrupted/resumed progress,
invalid cursors, bounded traversal and concurrent directory changes. Large entry
removal also respects the per-run traversal/time budget.

The portable engine streams whole two-hex-digit shards with `os.File.ReadDir`
in batches of 128 names, persisting only the next layer and shard number. Result
shards are request-key prefixes across compiler namespaces, so a single compiler
namespace cannot turn one resume unit into the whole result cache. Interrupted
workers revisit their current shard; completed shards advance the cursor.
Concurrent directory changes may defer entries to another cycle. At 100,000
uniform keys a shard contains about 400 entries; at 10 million about 40,000.
If a shard becomes too large for bounded workers, another fan-out level must be
introduced. A worker processes at most 256 shards, checks cancellation between
batches/entries, and applies a cooperative five-second deadline. Detached
scheduling supplies the separate hard timeout for blocking filesystem work.

Progress is a bounded 4 KiB JSON record under a permanent nonblocking trim lock.
No directory cookies, full-path stacks or entry inventories are persisted.
Corrupt or future-cutoff progress resets conservatively. A missing historical
`used` marker can be migrated from the newest recognizable publication timestamp
under SLOT and MUTATION.

An unused entry is renamed atomically into `trash/v1/<prefix>/<digest>` while
holding its SLOT and MUTATION. Both locks are released before deleting a large
tree. A fresh publication then uses the original stable path independently;
an interrupted worker leaves recognizable detached trash for later maintenance
or explicit clean. Trash is streamed first in each cycle and also deleted after
detachment. Explicit clean drains maintenance admission/worker locks before
selecting trash. Unknown paths and aliases remain untouched. The engine is
portable. Linux and macOS queue detached maintenance after staged Go work,
a complete-result hit, or result publication. The parent reads bounded state
and acquires a nonblocking admission lease; it performs no directory scans.
One child per cache root processes up to eight 128-shard steps within a shared
five-second cooperative deadline, with a separate 15-second process-group
kill timer for blocked filesystem work. It has low priority and inherits no
terminal or user streams. BORK_CACHE=off disables scheduling. Other platforms
use temporary staging until a bounded detached launcher is available.

Linux executes the inherited compiler image descriptor. macOS executes its
pathname and verifies the inherited file descriptor against its own kernel
mapped vnode before maintenance or result publication. Both retain a directory descriptor for the queued cache root, so
root replacement cannot redirect the worker. The same platform image launchers also support bounded eligible-result
publication; pathname races cause a skipped publication.
Test-gated pipe barriers and completion notifications verify detachment,
opt-out and the hard timeout without timing-based publication waits. Native
macOS CI exercises the launcher as well as portable trim. The population
acceptance matrix remains the next slice.

Trim acquires candidate SLOT and MUTATION locks only nonblocking, skips busy
entries, then rechecks age and path identity before deletion. It never removes
a Go subprocess's staged inputs or waits behind an active build. Deleted program
roots/targets may be removed during this same daily pass when their versioned
metadata conclusively proves absence; permission failures are not absence.
Only compiler-owned recognizable entries are eligible. Unknown paths, Go's own
build cache and project outputs remain outside maintenance.

Explicit clean retains its cancellable wait protocol: select under MUTATION,
release it before waiting for SLOT, then reacquire MUTATION and recheck paths.
Permanent bounded lock pools stay outside deletable trees and are never unlinked.
Cache permission/platform/locking failure preserves fresh compilation and
temporary Go staging. Clean recognizes current layouts; clean --all also handles
legacy result/staging layouts with their original lock mappings.

Before auto-on, benchmark ordinary lookup/hit/publication at 1, 1,000, 10,000 and
100,000 entries for both layers. Costs must remain flat in total entry count,
including new-key publication into a large already-populated cache. Measure trim
separately at the same populations: total cycle cost and bounded worker cost.
Synthetic entries are acceptable with their shape/size and measured work stated.

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
   AST-returning APIs fresh. This initial test gate preceded automatic reuse.
3. Replace the initial budget lifecycle with direct sharded lookup, hourly use
   marking and bounded daily age-based trim; retain clean commands. Enable the
   CLI cache only after flat per-build cost and separate trim measurements pass.
4. Extend artifact modes/assets only after their identity and output tests pass;
   measure first CLI request, subsequent fresh-process hits, warm Session hits
   and realistic full builds separately.

Every slice retains clean-versus-cached ordered diagnostic/Go-byte comparisons.
Tests must cover equal-mtime edits, negative appearance, directory/component
replacement, saved-env and launcher changes, compiler/schema invalidation,
corrupt/truncated/oversized entries, concurrent processes, unavailable cache,
hourly marking, five-day trim, busy-entry skips, resumable bounded maintenance,
deleted roots and clean during active build/publication.
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
publication. Linux automatic publication is now enabled after age-based retention,
cleanup and the population matrix. Tests opt into an explicit completion pipe and optional barrier;
they wait for publication or process exit without sleep-based polling. These pipes
are never inherited by Go subprocesses.

## Landed budget lifecycle (to be replaced)

The following records the currently landed result-v1/stage-v2 implementation.
The age-based design above replaces its admission accounting and eviction;
its existing explicit cleanup and coordination safeguards remain relevant.

The first lifecycle slice applies a 256 MiB/1,024-entry policy to result artifacts
across all compiler namespaces. Publication inventories recognized artifacts under
MUTATION, removes abandoned result temporaries, and evicts oldest inactive entries
using nonblocking result-slot acquisition. Both the old generation and the entire
new temporary must fit during replacement. Busy entries or a scan beyond 4,096
directory records cause publication to skip rather than overshoot. A validated hit
updates its last-use hint with nonblocking SLOT/MUTATION acquisition. Hints affect
only eviction, never receipt validity. Staging-v2 accounting, deleted-target cleanup
and explicit clean commands have landed; age-based retention and the larger
population acceptance measurements now precede automatic use.

Staging version 2 uses the same cache root and MUTATION lock as results, with a
separate permanent 256-slot pool in `locks/stage-v2`. Its versioned metadata records
the canonical program directory, mode and pinned Go-context namespace. Publishers
hold SLOT through Go execution; publication takes MUTATION only while accounting,
evicting inactive entries and replacing complete trees. Retained stage limits are
256 MiB/1,024 entries. Admission also reserves file/directory scanner nodes (65,536
across the layer), including module files, unique parents, tree/entry directories
and metadata, so legal small-file trees cannot exhaust inventory capacity. Account
actual existing tree nodes/bytes and old/new generations conservatively. Busy slots
or insufficient capacity use temporary staging.

Maintenance prioritizes conclusively deleted program directories, then oldest
inactive entries. It never follows entry symlinks; contained aliases are rejected
before abandonment cleanup or publication because confinement alone would not prove
which entry lock protects the tree. An entry's abandoned `new-*`/`previous` trees are
removed while its SLOT and MUTATION are held. Unsupported or unavailable caches keep
temporary staging. Version 1 publishers are retired without compatibility shims;
`clean --all` will recognize and remove their entries while preserving old lock files.

The cleanup slice exposes `bork clean` and `bork clean --all`. It first drains the
eight publisher admission slots, retaining them through cleanup so a queued child
cannot repopulate selected entries. Select under MUTATION, release it before waiting
for each entry slot, then reacquire MUTATION and recheck the selected identity before
removal. All waits are cancellable; an interrupted operation may have removed earlier
selected entries. Cleanup recognizes current result schema namespaces, staging-v2,
and (with --all) legacy staging-v1 with its original lock mapping. Lock files remain.
A missing cache succeeds; inaccessible storage or unsupported locking reports errors.
On platforms without current-result image identity, default cleanup covers staging;
--all selects all recognized result namespaces without that identity prerequisite.

Abandoned result temporaries and zero-byte job names left before descriptor unlink
are also selected; draining admission protects parent-side handoff creation. Cleanup
uses bounded no-follow traversal and can remove malformed entry symlinks without
following their targets. Unrecognized cache paths, project outputs and Go's cache
are preserved. Result maintenance additionally reads a bounded canonical request
header to prioritize conclusively absent selected targets (including standalone
files). This is an eviction hint only: hit validation still certifies the complete
checksummed artifact and every semantic receipt. Unsupported/truncated headers retain
ordinary LRU eligibility. Empty result namespace directories are removed under
MUTATION so failed publication does not accumulate directory records.


## Automatic Linux/macOS CLI rollout

`bork check`, `emit`, `build`, and `run` now enable validated complete-result
reuse at CLI startup. Library AST APIs, install, test and watch retain their
existing pipelines. Build hits reconstruct only owned source-location records,
using the original root-source identity for staging; they still invoke Go with
validated module and context data. Build misses pass their fresh checked graph
directly to generation and Go work once. Run executes the resulting program on
every invocation. No native executable or evaluation result is retained here.

Eligible misses always use bounded detached publication on Linux and macOS. Failed
admission, an unwritable root, or unavailable image/process facilities skip
publication; there is no inline certification fallback. Source loading and
eligibility precede expensive inventories. Deferred requests do not collect
comptime/predicate observations. Proof/evaluator execution, Go export loading,
custom drivers and assets remain bypasses. macOS now uses mapped-vnode compiler
identity and the pathname launcher for the same result and maintenance paths.

Every test CLI has a linker-only marker. Detached trim and publication are off
in marked images unless `BORK_TEST_CACHE_TRIM=on` or
`BORK_TEST_CACHE_PUBLISH=on` respectively. Cache tests opt in and wait using
owned completion pipes. Production activation in an enabled test image also
requires `BORK_TEST_CACHE_PRODUCTION=1`; ordinary CLI images need no opt-in.
