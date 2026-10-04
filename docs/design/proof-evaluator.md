# Closed predicate evaluator generation

Ordinary fact checking used to compile unrelated user types, derived instances,
foreign aliases and field helpers into every native predicate evaluator. The
first incremental-checking slice trims that program, while executing each selection afresh unless the separate Session cache below
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

Config retained one additional miss in two samples; a third sample was 72.41 ms
with facts at 4.34 ms. A subsequent ten-iteration reuse probe reported 0.9–1.0
hits/request and 72.69–83.20 ms total. The benchmark exposes batch-hit/miss metrics
so residual misses stay visible. These are full editor rechecks; source checking
still costs approximately 55 ms for config and 105 ms for HTTP.

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
