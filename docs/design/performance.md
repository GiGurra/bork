# Compiler performance (bork-e3r166)

Phase 1 establishes measurements before changing compiler behavior. The baseline
is main `b12e509`, Go 1.27.1, Linux amd64, AMD Ryzen 7 5700G, October 3, 2026.
These measurements came from a shared development host; latency is illustrative,
not a regression threshold. Repeat comparisons on the same quiet machine.

## Reproduce

Build the compiler once, outside the measured interval:

```sh
go build -o /tmp/bork ./cmd/bork
python3 scripts/benchmark.py --bork /tmp/bork --samples 5 > latency.json
# Restrict to a small workload and include fresh private Go build caches:
python3 scripts/benchmark.py --bork /tmp/bork --program hello --cold > cold.json

go test ./internal/driver -run '^$' -bench '^BenchmarkCompiler$/' -benchmem -count=5
# Phase measurements include setup outside the timed/allocation interval:
go test ./internal/driver -run '^$' -bench '^BenchmarkCompilerPhases$/' -benchtime=5x -count=3
```

The CLI harness covers every example and generated 100/1,000-function programs.
It measures `check`, `build`, `run`, and `test` where tests exist. Each sample
starts a new compiler process; run uses the example's working directory and
`args.txt`, and accepts intentional exits recorded in its golden output. The
JSON records raw samples, medians, corpus revision/dirty state, compiler binary
SHA-256/build metadata, Go version, host information, source size, generated
main Go bytes and main binary bytes. Sizes describe the normal main program,
including on test rows; they do not measure the separate generated test runner. Examples execute their normal effects.
Build/run/test samples include the compiler, Go toolchain and, for run/test,
program execution. Compiler construction and initial cache priming are excluded.

Warm means the ambient Go build cache has been primed for that command. Cold
means a fresh temporary `GOCACHE` for each sample; the module download cache,
filesystem cache and installed toolchain remain warm. No global cache is cleared.
There is no persistent bork prelude/check cache today. Fresh-process and repeated
in-process numbers must be compared separately: Go package-name metadata is
already cached by `sync.OnceValue` within a compiler process.

For profiles, anchor benchmark names exactly so similarly named benchmarks do
not contribute setup to the profile:

```sh
go test ./internal/driver -run '^$' \
  -bench '^BenchmarkCompiler$/^synthetic1000$/^check$' -benchtime=5s \
  -cpuprofile=/tmp/compiler.cpu -memprofile=/tmp/compiler.mem -o /tmp/driver.test
go tool pprof -top -cum /tmp/compiler.cpu
go tool pprof -top -alloc_space /tmp/compiler.mem

# Each pipeline phase is labelled, including untimed prerequisite phases.
go test ./internal/driver -run '^$' \
  -bench '^BenchmarkCompilerPhases$/^hello$/^generate$' -benchtime=50x \
  -cpuprofile=/tmp/phases.cpu -memprofile=/tmp/phases.mem -o /tmp/driver.test
go tool pprof -top -tagfocus='phase=generate' /tmp/phases.cpu
```

`BenchmarkCompilerPhases` reports parse/load (prelude and transitive sources),
module resolution, check/infer, typed-tree lowering, requirement contracts,
embed capture, effects, lifetimes, facts, Go generation/formatting, and Go build.
Facts includes any subprocess compilation/evaluation of constant predicates.
The Go-build phase includes source/module staging and `go build`; its B/op and
pprof data cover the parent bork process, **not** Go compiler/linker subprocess
allocations or CPU. The phase harness explicitly primes process-local metadata before every phase
benchmark, and primes Go builds before timing the Go-build phase. Heap profiles include untimed setup and cannot be filtered
by CPU labels; use phase B/op for allocation comparisons and allocation stacks
for attribution. Timer transitions add overhead, so phases below a few
microseconds should not guide optimization. Phase benchmarks reparse each
iteration to include parsing and create independent inputs. Parsed source ASTs
are preserved by checking (`TestCheckLeavesSyntax`); mutable semantic expansion
state lives in `check.Info`.

CI uploads `phases.txt` and `latency.json` for hello and synthetic1000 on each PR
and main push, with a manual trigger too. This provides a stored baseline for
comparison; no noisy shared-runner latency threshold fails PRs yet. Compare raw
Go benchmark artifacts with benchstat and compare JSON sizes directly.

## Baseline and hotspots

An initial three-iteration phase run produced these times (milliseconds):

| Program | Parse/load | Check/infer | Lower | Facts | Go generation | Go build |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| hello | 2.68 | 1.82 | 0.17 | 0.01 | 9.81 | 209.66 |
| calculator | 2.89 | 3.17 | 0.23 | 0.03 | 12.41 | 235.45 |
| synthetic100 | 3.10 | 6.58 | 0.52 | 0.02 | 13.65 | 236.44 |
| synthetic1000 | 11.21 | 285.71 | 8.49 | 0.13 | 41.36 | 446.64 |

A later three-sample fresh-process run recorded these medians and sizes:

| Program | Check ms | Build ms | Run ms | Test ms | Generated Go bytes | Binary bytes |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| hello | 159.1 | 384.8 | 380.7 | — | 49,378 | 2,416,923 |
| calculator | 160.7 | 448.3 | 412.6 | — | 63,464 | 2,536,248 |
| signup_api | 523.3 | 1265.2 | 1259.0 | 942.7 | 158,424 | 11,922,498 |
| synthetic100 | 162.8 | 400.3 | 404.2 | 410.1 | 62,541 | 2,240,324 |
| synthetic1000 | 442.9 | 935.8 | 894.5 | 775.2 | 190,551 | 2,240,340 |

The 10x function-count increase gives ~43x check time, but only ~3.6x parse and
~3x generation time. A separate synthetic1000 CPU profile attributes **76.5%**
of samples cumulatively to `materializeDefaultUses`, which visits specializations
and scans every expression and call to locate those owned by each function.
Global scans inside that visit make this workload quadratic. The same profile
attributes 40.1% of allocated bytes to lexer token emission. Total repeated
check cost was ~303 ms, 30.4 MB and 144,202 allocations per iteration.

Hello Go generation allocates ~3.05 MB and 63,425 objects for roughly 49 KB of
output. The generation CPU stacks include Go parsing, formatting and printer
work, including repeated printer size calculations. The emitted runtime includes
prelude helpers even when main uses only a few of them; reachability elimination
could reduce both formatting and Go frontend work. Link-time dead code removal
already limits executable growth: the 100- and 1,000-function synthetic programs
produce about 62.5/190.6 KB of Go, but both executables are about 2.24 MB because
the generated assertions and pure computations can be optimized away.

Fresh-process hello checking is ~157 ms in a separate five-sample run, while
`bork version` is ~2.5 ms. `standardGoNames` runs `go list ... std` once per
compiler process even for prelude imports; that startup metadata request is
absent from steady-state phase measurements. Measure and reduce it before
attributing the difference to parser/checker speed. A fresh Go cache increased
hello build from ~485 ms to ~3.3 s in a one-sample shared-host run; cache reuse
matters far more than small checker changes for cold builds.

## Proposed fixes, in priority order

| Rank | Fix | Expected impact | Risk / validation |
| --- | --- | --- | --- |
| 1 | Index default-use expressions/calls by owning function before visiting specializations | Remove the measured quadratic scan; largest large-program checker gain | Low: preserve sorted traversal and first-use diagnostics; all goldens unchanged |
| 2 | Reduce/cache Go standard package-name discovery keyed by toolchain/target | Remove much of fresh-process startup cost, particularly small checks | Medium: aliases and toolchain/environment changes must invalidate correctly; test custom Go names |
| 3 | Cache immutable embedded prelude/std parse data and eventually checked packages by content | Avoid recurring parsing and allocations, especially daemon/batch use | Medium/high: keep mutable semantic state separate from preserved source ASTs; persistent cache needs version/content/target keys |
| 4 | Generate only reachable helpers/types/dictionaries, format less | Smaller Go files and less printer/Go frontend work | Medium/high: tests, predicates, mocks, derived methods and effects are separate roots; justify any emitted-Go golden changes |
| 5 | Parallel independent package/function work with deterministic merging | Potential throughput improvement for multi-package programs | High: checker tables and specialization discovery are shared; introduce package boundaries before goroutines; compare diagnostics and race tests |
| 6 | Change union/option/closure/dictionary representations only after targeted profiles | Possible runtime size/Go-build improvements | High: representation correctness and readability; baseline does not yet justify a particular replacement |

Facts and lowering are small for these workloads; optimize them only when a
predicate-heavy or lifetime-heavy workload identifies a real hotspot. Synthetic
programs exercise compiler scaling rather than runtime performance. Additional
corpora should cover generic specialization, imported package graphs and heavy
facts before deciding on parallelism or representation changes.

## Independent source parsing

Source files can be parsed independently before declaration/checking begins.
`syntax.ParseFiles` uses at most eight workers, bounded by GOMAXPROCS and file
count, when a package has at least four files totaling 32 KiB. Smaller inputs
remain sequential to avoid scheduling overhead. The prelude crosses this
threshold; package import resolution and checking remain sequential. Each file
gets private diagnostics, joined in the original input order after all workers
finish. This retains lexer/parser error order as well as file/declaration order.

On the same shared development host with immutable tokens primed, three
one-second prelude parsing samples had serial times 2.80/2.61/2.04 ms and
parallel times 1.10/1.07/1.33 ms. Allocation changed from ~1.070 MB/19,034 objects
to ~1.072 MB/19,069 objects. Hello parse/load phase samples were
1.29/1.30/1.36 ms, compared with approximately 2.5 ms after token caching. These
are in-process parsing measurements, not a claim about total fresh CLI speed.

```sh
go test ./internal/syntax -run '^$' -bench '^BenchmarkParseFiles$' -benchmem -count=5
go test -race ./internal/syntax -run '^TestParseFiles' -count=1
```

The [incremental compilation proposal](incremental.md) keeps this boundary
separate from parallel checking, which first needs immutable package artifacts
and complete semantic dependency manifests.

## Follow-up profile and traversal metadata

After the default-use index, selective Go-name loading, embedded tokens and
parallel parsing, a three-second synthetic1000 check profile measured ~39 ms
and 29.7 MB per whole in-process check. Default materialization contributed
~8% of sampled CPU, down from the baseline ~76%. The reflective type-expression
walker used by unapplied-fact diagnostics contributed ~15%; its pointer-tracking
map also remains a significant allocation source. This does not justify a
union, option, closure or dictionary representation change.

Cache immutable struct traversal metadata by `reflect.Type`: exported field
indexes in declaration order, restricted to kinds the walker actually visits.
Pointer tracking, context labels and per-source checker state stay local.
The isolated 1,000-function walker benchmark measured baseline
2.69/2.74/2.77 ms versus cached 1.93/2.49/1.77 ms on the shared host. Both versions
allocated ~1.31 MB and 79 objects; this optimization reduces metadata discovery
work, not the pointer-tracking allocations. Whole check-phase samples are noisy
and show a much smaller change; do not extrapolate the isolated speedup to CLI
latency.

```sh
go test ./internal/check -run '^$' -bench '^BenchmarkTypeExprTraversal$' -benchmem -count=5
```

## Implementation progress

The initial ranked list has these boundaries now:

| Work | Implemented / remaining |
| --- | --- |
| Default-use indexing | [#172](https://github.com/GiGurra/bork/pull/172) removes the measured quadratic owner scans. |
| Go-name startup | [#174](https://github.com/GiGurra/bork/pull/174) avoids whole-standard-library discovery; [#177](https://github.com/GiGurra/bork/pull/177) handles GOPATH and effective GOENV settings conservatively. |
| Source/package reuse | [#175](https://github.com/GiGurra/bork/pull/175) reuses immutable embedded tokens. Checked interfaces/session/disk reuse follow [the incremental design](incremental.md), not shared mutable checker state. |
| Emitted helpers | [#176](https://github.com/GiGurra/bork/pull/176) prunes unreachable free helpers before printing; user validation and method/global roots remain. Further pruning needs evidence and root coverage. |
| Parallelism | [#180](https://github.com/GiGurra/bork/pull/180) parses independent sources with deterministic joins. Parallel checking waits for immutable package artifacts and explicit dependencies. |
| Representations | Deferred until targeted runtime or Go-build profiles establish a benefit; current compiler profiles identify frontend work instead. |

[#179](https://github.com/GiGurra/bork/pull/179) fixes the imported-user-default
proof gap discovered during design review. Cache artifacts may rely on those
proofs only after the clean compiler performs them for every user package.

## Incremental snapshot latency follow-up

[#194](https://github.com/GiGurra/bork/pull/194) captures effective Go settings
and hashes the selected launcher once per compilation request. On October 3,
2026, three-sample fresh-process hello medians on the shared host changed from
29.4 to 48.8 ms for check, 240.6 to 251.9 ms for build, and 246.6 to 253.7 ms for
run, compared with main `2bc0dc6`; generated Go stayed at 34,107 bytes. These
numbers record an accepted correctness cost, not a speed improvement or a CI
threshold.

Track recovery under bork-h5rkt4: when Session reuse lands, report both first
request and repeated unchanged-request latency, including input validation and
configuration refresh, against this one-shot baseline on the same quiet host.
Repeated requests must recover the added snapshot cost without skipping input
validation. Keep fresh-process CLI measurements separate; Session speed does not
by itself remove their regression. If Session reuse is deferred beyond phase 1,
revisit this cost before closing that phase and measure a cheaper sound capture
strategy rather than treating the regression as resolved.

## Initial Session measurements

The conservative Session refreshes configuration and builtin package-name
metadata before a hit. Three five-iteration samples on the shared host gave:

| Workload | First Session request ms | Unchanged Session request ms | First/unchanged allocation MB |
| --- | ---: | ---: | ---: |
| hello check | 47.5–49.0 | 38.2–41.6 | 3.2–3.7 / 0.32–0.34 |
| hello emit | 51.8–53.0 | 38.6–39.0 | 5.9 / 0.36–0.37 |
| synthetic1000 check | 74.2–75.9 | 38.8–39.3 | 30.2–30.6 / 0.46–0.47 |
| synthetic1000 emit | 100.2–103.0 | 38.5–39.5 | 43.1 / 0.70 |

Every unchanged sample reported one hit per request. These are in-process
measurements with configuration refresh and content/name validation included;
they do not recover fresh-process CLI latency. Existing repeated one-shot hello
check measured 21.7–27.4 ms and emit 30.9–31.8 ms, benefiting from its existing
standard-name cache. The conservative Session is therefore slower on this small
workload despite avoiding checker work. Larger graphs benefit and allocate much
less; the synthetic one-shot samples on the shared host were too noisy for a
precise comparison beyond the first/unchanged Session table.

Before wiring watch, profile and remove unnecessary hit-path metadata work while
preserving invalidation. Validate known standard-name inputs cheaply under a
sound toolchain/provenance boundary; SDK source edits must not be hidden by equal
sizes/mtimes or unchanged directory mtimes. Keep full metadata reload as the
fallback for unknown inputs. Profile fresh one-shot configuration capture
separately and report both before/after tables; a Session benchmark improvement
does not settle the CLI regression tracked above.

```sh
go test ./internal/driver -run '^$' -bench '^BenchmarkSession/' -benchmem -benchtime=5x -count=3
```

## Session content-validation measurements

The next Session step avoids repeated `go env` and standard-package discovery
for recognized, inventoried Go configurations. Main (#203 plus #199) and the final validator ran three five-request
samples each on the same host:

| Workload | First request before → after ms | Unchanged request before → after ms |
| --- | ---: | ---: |
| hello check | 43.97 → 74.22 | 38.75 → 18.74 |
| hello emit | 52.49 → 81.60 | 38.30 → 18.70 |
| synthetic1000 check | 74.89 → 106.54 | 38.92 → 18.99 |
| synthetic1000 emit | 99.69 → 130.07 | 38.94 → 18.66 |

These are medians including validation, with exactly one hit per unchanged
request. Initial requests seed and verify inventories; this increases their
cost. Ordinary one-shot APIs are unchanged. Hit allocation rises modestly:
hello check 0.34 → 0.57 MB and synthetic1000 emit 0.71 → 0.95 MB, because the
validator opens and streams all immediate files of observed standard packages.
It retains digests rather than package-source bytes or mutable checker graphs.

The isolated hit validation medians were configuration 17.22 → 10.06 ms,
standard names 21.18 → 8.03 ms, and source/assets about 0.06 ms. Configuration is
now dominated by hashing the selected Go executable. A follow-up can validate
that digest with filesystem identity including inode/ctime where available;
platforms lacking that evidence must keep content hashing. SDK/source/env file
edits still require content checks, including equal-size/equal-mtime edits.
Fresh-process CLI capture latency remains the separate unresolved #194 follow-up.

`BenchmarkBuildStages` separates complete Bork emission (including Go metadata
and any compiler-time proof work) from staged compilation/linking of that Go.
It primes dependency build caches, uses the existing flat-output build path with
a fresh staging directory per build, and executes no generated program. This
is not a Go cache-hit-only measurement: staging paths can cause the generated
main package to compile again. Go-child allocations are outside parent Go
benchmark allocation counts.

```sh
go test ./internal/driver -run '^$' -bench '^BenchmarkSession/' -benchmem -benchtime=5x -count=3
go test ./internal/driver -run '^$' -bench '^BenchmarkSessionValidation$' -benchmem -benchtime=5x -count=3
go test ./internal/driver -run '^$' -bench '^BenchmarkBuildStages$' -benchmem -benchtime=1x -count=7
```

A separate seven-run measurement (`-benchtime=1x -count=7`) after local tests
finished gave the following milliseconds; the earlier concurrent measurements
were much noisier:

| Program | Bork compile median (min–max) | Staged Go build median (min–max) | Emitted Go bytes |
| --- | ---: | ---: | ---: |
| config | 325.30 (299.20–454.41) | 282.31 (278.55–359.60) | 63,706 |
| http_server | 450.89 (424.53–466.70) | 711.87 (705.66–717.83) | 166,369 |

Both stages deserve attention, though these quieter emission samples are below
the earlier roughly-one-second averages. Profile these realistic emission paths
before the disk-cache step, separating Go metadata/proof subprocess latency from
the compiler's own checking/generation work. These examples can load Go types
and run proofs; the conservative complete-result Session does not claim hits
for them.

## Launcher validation after monotonic observation

Launcher metadata can skip hashing after more than two seconds of monotonic
observation of a matching tuple and matching bytes. This avoids comparing local
wall time with filesystem/server timestamps. Platforms without inode/change-time
evidence and FAT/exFAT keep hashing. Timestamp granularity must be at most two
seconds; persistent entries must establish new observations in each process.

Three five-request samples on main including #201/#206, after local tests
finished, gave these medians (milliseconds). `unchanged_stable` waits a real two
seconds before timing; `unchanged` starts immediately and includes warm-up hashing.

| Workload | First | Immediate unchanged | Established unchanged (min–max) |
| --- | ---: | ---: | ---: |
| hello check | 101.51 | 29.28 | 9.29 (9.07–9.91) |
| hello emit | 110.54 | 29.98 | 9.53 (9.04–9.81) |
| synthetic1000 check | 131.03 | 30.21 | 8.81 (8.69–8.82) |
| synthetic1000 emit | 151.78 | 29.10 | 8.75 (8.65–9.26) |

Every unchanged sample reports one hit/request. Established configuration
validation is 0.31 ms (0.30–0.33), source/assets 0.05 ms (0.05–0.07), and names
8.13 ms (8.10–8.38). Hello-check hit allocations are 0.53 MB; synthetic emit
0.91 MB. Earlier #206 hits were about 19 ms. The new stable path saves launcher
hashing, while startup/warm-up adds descriptor and repeated hashing work.
Ordinary one-shot APIs are unchanged; the #194 CLI issue remains separate.

## Realistic emission and Go staging experiment

The parent-process CPU profile accounted for only 19% of elapsed time in a
74-second phase sample. Facts/proof evaluation waited for freshly built Go
executables: config facts took 282.82 ms (256.43–294.54), HTTP facts 411.90 ms
(411.48–436.80). Checking itself took 2.37 and 5.63 ms respectively after
metadata priming. These are three one-request samples; child-process CPU is
outside the parent profile. Go's build action hashes the package directory
without `-trimpath`, so a fresh `bork-build-*` directory defeats compilation
cache reuse even when the emitted source is identical.

A prototype compared ordinary random staging, random staging with `-trimpath`,
and a stable directory. Seven rounds alternated the modes in that order on the
same source tree, with identical emitted Go bytes and captured module/context.
Each mode primed its Go cache before timing. The machine also ran other workers;
large upper tails are reported rather than removed. Milliseconds, median
(min–max):

| Program / stage | Random | Trimpath | Stable |
| --- | ---: | ---: | ---: |
| config Bork emission | 301.65 (284.61–1091.64) | 171.53 (165.52–652.45) | 174.83 (162.68–432.04) |
| config Go build | 302.31 (281.86–1243.43) | 39.40 (38.32–373.27) | 39.96 (37.79–84.64) |
| HTTP Bork emission | 477.22 (423.81–1486.84) | 232.03 (209.13–883.29) | 241.34 (206.91–263.27) |
| HTTP Go build | 767.49 (710.71–2575.64) | 57.29 (54.79–413.51) | 59.92 (55.27–88.31) |

Config emitted 63,722 bytes; HTTP 166,669 bytes. Bork emission includes proof
builds and fresh evaluation execution. This experiment reuses Go's compilation
cache, never predicate results. The Go stage includes staging/linking and
excludes Bork emission/execution. `BenchmarkGoStaging` reproduces the isolated
Go-stage comparison without changing production staging:

```sh
go test ./internal/driver -run '^$' -bench '^BenchmarkGoStaging$' -benchmem -benchtime=1x -count=7
```

The experimental modes passed exact mock caller-location, test-failure and dbg
fixtures. Absolute `.bork` mappings survived runtime.Caller, panic stacks and
ELF DWARF line tables in all modes. Caller-location strings are emitted directly
by Bork. Trimpath changes unmapped generated frames to `borkprogram/main.go`
and SDK frames to package-relative paths; stable staging preserves absolute
paths and does not introduce a separate trimpath SDK cache variant. Delve was
not installed: DWARF line-table inspection is evidence for source mapping,
not an interactive debugger test. Prefer stable staging.

For the first Session request, three hello phase samples gave configuration
39.94 ms (39.28–40.48), checking/name inventory 50.37 ms (50.29–51.09),
generation 8.04 ms (7.85–8.13), and artifact retention 0.102 ms
(0.102–0.108). Launcher hashing appears during both initial inventories;
retaining compiler results is negligible. These are separate phase samples,
not an additive single-request trace. Preserve the established ~9 ms hit path
while revisiting repeated seed hashes and configuration subprocess work:

```sh
go test ./internal/driver -run '^$' -bench '^BenchmarkSessionFirstPhases/(hello|config|http_server)/(configuration|check|facts|generate|retain)$' -benchmem -benchtime=1x -count=3
```

### Stable staging layout

Use a stable directory for each canonical program/module root, output mode and
pinned Go configuration, with a fixed `tree` subdirectory containing generated
packages. Do **not** put the complete graph content digest in the pathname:
when multi-package output arrives, editing one package must leave unchanged
packages at the same absolute directory so Go can reuse their cache entries.
Go's own action keys still incorporate changed source/module/toolchain inputs.
Compiler-version namespaces may change between compiler versions; they must
not change for ordinary source edits.

Serialize staging and the entire Go build under a cross-process lock for that
entry. Write a complete replacement tree from captured Go/module/asset bytes
before publishing it at the fixed path; publication and rollback happen while
the lock is held. Readers that invoke Go participate in the same lock. This
avoids mixing two requests' modules or assets and removes stale files when a
package disappears. Keep user binaries/evaluators outside the mutable tree.
A cache/locking failure falls back to the current temporary-directory build.
The implementation must cover concurrent processes, changed files/modules/
assets, cache failure, source-location parity and fresh evaluator execution.
Persistent checked-result artifacts remain a separate later cache layer;
this layout reuses Go compilation without certifying Bork semantic hits.

`BenchmarkSessionFirstPhases` reports the selected phase as `phase-ns/op`;
ordinary `ns/op` and allocation columns describe the complete first request.
Calibration uses whole requests so very short phases cannot accidentally
schedule millions of full compilations. Unexecuted phases are skipped.
