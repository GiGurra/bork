# Closed predicate evaluator generation

Ordinary fact checking used to compile unrelated user types, derived instances,
foreign aliases and field helpers into every native predicate evaluator. The
first incremental-checking slice trims that program, while still executing every
selection afresh. It applies to fresh CLI requests and Session/editor requests.

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

## Subsequent proof reuse

Session-owned reuse should key a proof result on the checked selection and its
complete audited dependency closure, emitted runtime support, evaluator module
inputs, Go toolchain/configuration and execution policy. Unknown inventories
must execute afresh. Clean-versus-reuse tests must cover dependency edits and
rejected closures. This is separate from the trim and is not enabled here.

A Session cache helps editors and watch processes, but cannot accelerate proof
execution in a fresh CLI process. CLI reuse additionally needs independently
certified execution artifacts in the disk cache, including support/import
initialization, the Go build closure and native/policy inputs. The existing
static audit alone is insufficient evidence for an execution cache hit.
