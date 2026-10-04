# Closed predicate evaluator generation

Ordinary fact checking used to compile unrelated user types, derived instances,
foreign aliases and field helpers into every native predicate evaluator. The
first incremental-checking slice trims that program, while executing each selection afresh unless the separate Session or CLI proof cache below
qualifies. The trim applies to fresh CLI requests and Session/editor requests.

`gen.ClosedProofProgram` requires the existing typed `AuditExecutionQueries`
audit to accept every query, including And/Or leaves, argument expressions,
function references, called bodies, type defaults and constraints. Unknown
nodes, unsafe helpers, generics, lazy state and foreign representations decline.
The generator also checks that its call metadata stays within the audited
declaration inventory: replaced comptime bodies can retain extra call edges.
A mismatch declines trimming. The driver then uses the original full evaluator. Bounded comptime evaluation
keeps its existing path. Accepted selections omit unrelated instances, package
initializers and foreign aliases, and emit only used types and reachable helpers.
The original checked graph and final program generation remain intact.

## Measurements

Linux, Go 1.27.1, SDK object cache warm. Three samples of five iterations each;
values are medians across sample averages. Each iteration changes one proof
argument to a previously uncompiled value. Timing includes stable Go staging,
building and native execution, and excludes Bork checking and generation.

| Fixture | Full evaluator | Closed evaluator | Full / closed Go bytes |
| --- | ---: | ---: | ---: |
| config | 395.05 ms | 182.91 ms | 74,686 / 12,063 |
| http_server | 516.27 ms | 124.93 ms | 92,588 / 846 |

```sh
go test ./internal/driver -run '^$' -bench '^BenchmarkClosedProofBuild$' -benchtime=5x -count=3
```

A separate fresh-process CLI comparison used CGO disabled, private Bork caches,
independent source directories for each arm and a shared warm SDK Go cache.
One initial request was excluded, followed by seven alternating paired edits.
Config edits changed the host string; HTTP edits added a newline and retained
identical proof arguments. These preliminary medians include checking and the
normal final Go build where applicable; host variation affected config/check.

| Request | Before | After |
| --- | ---: | ---: |
| config check | 433.88 ms | 258.41 ms |
| config build | 736.44 ms | 598.34 ms |
| http_server check | 219.21 ms | 169.98 ms |

The initial config builds were 737.27 / 584.26 ms. Initial HTTP checks were
497.81 / 183.21 ms. HTTP edits reuse Go object-cache entries even before this
change, so their warm improvement is smaller than the changing-argument row.
These timings compare the full evaluator with the trim; they are not proof
result-cache hits. Config runtime stdout and HTTP emitted Go matched in a
same-source comparison. Native parity tests cover helper calls, callbacks,
recursion, literals/defaults, interpolation and And/Or combinations. Rejection
tests exercise fallback for direct/transitive unsafe code, unsafe callbacks,
generics, lazy state and replaced comptime dependencies. Real config/HTTP
fixtures also compare full and reduced native proof output.

## Session proof reuse

`Session.Check`, `Emit` and `Analyze` retain a separate cache of successful closed
boolean batches, including false results. They still recheck edited sources and
recompute diagnostics. Programs requiring evaluation still bypass complete-program reuse;
no checked graph or native executable is stored in the proof cache.

Every request regenerates and audits the selection. Its ordered queries,
arguments, reachable helpers and compiler runtime appear in the generated Go
bytes, which serve as the closure identity. The key also binds actual staged
module/assets, stage path, native Go launcher digest, build/process environment
and execution settings. Unknown startup/support imports decline. Native Go
context validation excludes wrappers, unsupported flags/workspaces and switched
metadata toolchains. Parsing the actual staged module additionally rejects
newer Go requirements and toolchain directives that could auto-switch the build.
The installed compiler and Go installation stay immutable while running, as in
Go's own object-cache contract. No SDK directory scan is added.

Staging still happens on hits, preserving module-hook observations. Results are
owned copies, capped at 256 entries and 64 KiB, with a 4 MiB input budget. Native
failures and malformed output are never stored. The bounded-comptime invocation
memo remains separate; explicit comptime values execute afresh.

Linux/Go 1.27.1, three five-iteration samples, alternating newline overlays after
one priming request. Both arms use the trim and warm Go context/object caches.
The fresh arm discards only the proof cache before each request. Sample medians:

| Editor fixture | Fresh proofs | Session proofs | Facts fresh / reuse |
| --- | ---: | ---: | ---: |
| config | 184.34 ms | 94.77 ms | 115.82 / 26.58 ms |
| http_server | 233.28 ms | 122.72 ms | 112.89 / 1.90 ms |

These initial samples retained one additional config miss in two runs. Diagnosis
found identical stage/module/tool/environment inputs but swapped final field-default
queries: `Facts` iterated a map. Ordering ordinary defaults by declaration position
removes that observed source variation. The current profile below reports one
hit/request and zero misses for both fixtures after priming. The eight-default
ordering regression fails on the previous implementation.

```sh
go test ./internal/driver -run '^$' -bench '^BenchmarkSessionProofEdits$' -benchtime=5x -count=3
```

Clean-versus-reuse tests cover imported helper edits, query argument edits,
failed proofs and recovery, emitted bytes and editor snapshots. Separate tests
cover result ownership, Session isolation, execution environment and actual
staged-module changes, native failure nonretention, wrapper bypass and staged
Go toolchain selection. Existing key/support/budget regressions remain applicable.

A Session cache helps editors and watch processes but cannot accelerate proof
execution in a fresh CLI process. CLI reuse still needs independently certified
execution artifacts in the disk cache, including support/import initialization,
the Go build closure and native/policy inputs. The static audit alone is
insufficient evidence for a persisted execution cache hit.

## Incremental checking checkpoint

After the ordering fix, three five-edit samples on the same host give the
following medians. Editor edits alternate trailing newlines, retaining all proof
arguments; these are full checks with validated warm Go context/object caches.

| Editor fixture | Fresh proofs | Session proofs | Check phase | Configuration | Facts with reuse |
| --- | ---: | ---: | ---: | ---: | ---: |
| config | 190.95 ms | 72.90 ms | 55.72 ms | 10.24 ms | 4.42 ms |
| http_server | 240.15 ms | 123.72 ms | 106.74 ms | 10.33 ms | 1.97 ms |

The check phase includes tracked Go package-name discovery, so it is an upper
bound on Bork type-checking time. Config's fresh proofs take 121.26 ms and HTTP's
117.52 ms; parsing/lowering/lifetimes and remaining overhead account for the rest.

The config one-shot CLI compiler path after a host-string edit takes 724.17 ms:
final Go staging/build 473.04 ms, freshly executed Facts 184.21 ms, check/name
discovery 27.90 ms, generation 19.54 ms and Go configuration 16.97 ms. This
three-by-five profile clears process-local Go names to model fresh CLI requests,
with CGO disabled and Go subprocess workers fixed at two. It excludes process
startup and compiler-image hashing. It is distinct from the earlier unrestricted
fresh-process wall measurement of 598.34 ms; compare within each setup.

```sh
go test ./internal/driver -run '^$' -bench '^BenchmarkSessionProofEdits$' -benchtime=5x -count=3
go test ./internal/driver -run '^$' -bench '^BenchmarkProofCLIEditPhases$' -benchtime=5x -count=3
```

General package interface artifacts are deferred at this checkpoint. They would
not reduce the dominant CLI native-build/proof costs, and current editor checking
is bounded at roughly 56/107 ms including dependency discovery. Revisit artifacts
when profiles on larger real package graphs show unchanged-package Bork checking
dominating edit latency after cheaper discovery/proof reuse, and a small edit can
avoid substantial checking work. Any implementation must first demonstrate a
net win and clean-versus-incremental parity for public types, predicates/defaults,
bodies, imported dependencies and test/mock overlays. CLI disk proof reuse remains
separate deferred work, tracked as bork-44yvg8.

## Persistent CLI proof batches (bork-44yvg8)

Linux/macOS CLI `check`, `emit`, `build` and `run` automatically retain one
successful closed boolean batch beside each stable predicate staging tree. The
sidecar uses the existing stage slot and disappears with that entry on `clean`
or five-day trim. It is bounded to 64 KiB of booleans and a 512 KiB checksummed
encoding. It contains owned booleans, an actual compiler-image namespace, the
Session staged-input key and the installed-SDK identity. It stores no executable,
checked graph, contextual Facts or explicit comptime value.

The existing closed-query and generated-support audits run on every request.
Lookup stages the current program and binds exact ordered arguments/helper bytes,
module/assets, path, build/process environment and evaluator policy. Native
configuration validation and the actual staged module reject wrappers, unsupported
settings, cross-target execution and generated toolchain switching. The SDK
contract is the existing immutable-installation policy: launcher/VERSION identity
changes invalidate; in-place SDK edits are unsupported, as for Go's object cache.
The startup launcher hash retains file evidence so validating this same native
installation does not hash it again. Compiler-image hashing overlaps a fresh
build on misses; hits still require that content namespace before serving.

The stage slot stays held through native execution and atomic sidecar publication.
Recheck the staged key, installed identity and configuration after execution.
Malformed output, failures and uncertain identities never publish. Missing,
corrupt, oversized, symlinked or stale sidecars execute afresh; publication failure
cannot change the fresh result. False booleans qualify, while current Facts always
reconstructs diagnostics at today's source locations. Evaluated programs still
bypass the enclosing complete-result cache. Session and bounded-comptime owners
remain separate.

### Evaluation checkpoint

Go 1.27.1/Linux, `GOMAXPROCS=2`, `CGO_ENABLED=0`, warm SDK objects. Fresh CLI
processes used private source copies and cache roots. Five samples followed two
warmups; each unchanged-proof sample appended a distinct one-line comment.
The before arm is main after #289 and includes stable Go staging, not an
uncached temporary-stage approximation. Compiler-phase profiles came from the
existing test-only CLI timing seam; ordinary builds expose no profiling setting.

| Fixture/request | Fresh median (min–max) | Proof reuse median (min–max) | Net saving |
| --- | ---: | ---: | ---: |
| config check | 172.25 (171.11–173.47) ms | 69.73 (68.35–70.35) ms | 102.52 ms |
| config build | 245.53 (243.26–247.29) ms | 140.28 (138.12–140.55) ms | 105.25 ms |
| http_server check | 192.75 (188.55–194.51) ms | 87.58 (86.28–109.21) ms | 105.17 ms |
| http_server build | 312.57 (303.86–366.91) ms | 206.77 (206.06–209.91) ms | 105.80 ms |

Each fixture has one eligible batch (config: 15 booleans; HTTP: 9), so 100% of
its native proof evaluations qualify. Fresh facts cost 116–119 ms; reused facts,
including generation, staging, configuration validation and compiler-image hashing,
cost 11–14 ms. The first prototype instead spent 54–56 ms on hits: three repeated
launcher hashes cost about 30 ms. Using the already supported installed-SDK
identity removed those repetitions; retaining startup hash evidence removed a
fourth. No SDK content walk or broader intrinsic audit was introduced.

Separate fresh-process miss samples used independent directories for each arm,
alternating their order and changing the config host or HTTP status literal to a
previously uncompiled value on every iteration. Every reuse-arm attempt published
rather than hit. Medians (min–max), again five samples after two warmups:

| Fixture/check | Today's fresh | Persistent proof miss | Added miss time |
| --- | ---: | ---: | ---: |
| config | 242.95 (241.32–249.89) ms | 250.53 (248.55–253.19) ms | 7.58 ms |
| http_server | 204.38 (191.15–205.21) ms | 213.01 (204.02–213.43) ms | 8.63 ms |

Miss publication adds a bounded atomic sidecar write; total miss overhead includes
native configuration checks, rehashing staged bytes and compiler-image work.
The initial miss prototype added roughly 25 ms. The final path shares the startup
launcher evidence and overlaps image hashing with fresh Go evaluation, reducing
that overhead without weakening identity validation. These figures characterize
this machine and edit pattern; they are not a promise of every edit's latency.

For the broader eligibility checkpoint, fresh checks/builds of `examples/comptime`
were 1108/1181 ms, with 1006/1017 ms in comptime plus about 44 ms in final Facts.
Its untrimmed build-read/JSON/generic evaluations do not qualify under the existing
closed-proof contract. `signup` and `orders` invoked no evaluators (checks 59/54 ms,
builds 134/110 ms). Persisting explicit values would need substantially more
certification and reconstruction machinery; it remains deferred. The narrow proof
cache is justified by saving more than 100 ms and over half of check time on the
two ordinary backend fixtures, without adding flags or language concepts.
