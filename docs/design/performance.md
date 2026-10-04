# Compiler performance (bork-e3r166)

The [Go package output experiment](go-package-output.md) records isolated Go
build and end-to-end edit measurements. Its scalar prototype wins on a synthetic
multi-package workload; existing real examples require the flat fallback, so
production output remains unchanged.

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

## Stable Go staging implementation

Generated Go now uses a fixed tree per canonical program source directory,
output mode (`program`, `test`, `predicate`, `comptime`) and pinned Go context.
A cross-process lock spans complete captured source/module/asset replacement
and Go compilation. Go's own content keys validate compilation reuse; Bork
predicates, comptime execution and user programs still run freshly. The current
layout remains one main package; fixed tree/subpackage paths support later
multi-package output without moving unchanged packages after every edit.

Linux and macOS use kernel file locks. Other platforms and any unavailable
cache/lock/publication use the existing temporary-directory build. Staging
lives under `os.UserCacheDir()/bork/stage/v1`. Each entry retains its current
source tree; abandoned replacements are removed on its next request. A fixed pool of 256 hashed lock slots remains outside entries and is never
removed while processes may use it; collisions only serialize unrelated builds.
Eviction/`bork clean` belong to the subsequent persistent-cache slice.

Seven alternating one-request runs compared forced temporary fallback with
stable staging on exactly the same compiler/worktree, captured inputs and
process environment. A private writable cache was required because this
execution environment's default cache root is read-only. Replacing only its
stage-root directory with a regular file forced fallback without changing the
Go configuration. All Go dependency caches were primed. Milliseconds:

| Program / stage | Temporary median (min–max) | Stable median (min–max) |
| --- | ---: | ---: |
| config Bork emission | 315.55 (293.65–394.92) | 167.28 (163.35–185.10) |
| config Go build | 299.00 (278.54–455.20) | 40.30 (39.07–42.33) |
| HTTP Bork emission | 472.52 (428.76–508.38) | 219.55 (208.26–227.60) |
| HTTP Go build | 778.13 (714.48–792.25) | 57.07 (55.90–67.16) |

The Go-stage benchmark keeps a fixed output executable, so Go may skip linking
when it already matches. Emission runs create fresh evaluator executables and
execute them each time. Exact emission bytes remain unchanged between modes.
Tests cover competing processes, complete replacement after source/module/
asset edits, stale checksum/asset removal, cache failure, absolute generated
source paths, mapped caller/panic paths and Linux DWARF source positions.

First Session hello requests, using the same three five-request protocol as the
launcher table below, measured 94.01 ms check (93.81–97.29) and 102.73 ms emit
(102.21–109.98). Established hits stayed 9.29 and 9.34 ms respectively, with
one hit/request. The pure hello path does not build evaluators; stable staging
adds no inventory work to it. These samples are startup monitoring, not evidence
that staging accelerates a check with no Go builds.

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

### Persisted-receipt identity prerequisites

The first receipts slice adds owned loader observations and compiler identity
without enabling persisted hits or adding work to ordinary compilation. The
compiler namespace hashes the actual running image once per process. On Linux
this reads `/proc/self/exe`, so replacing the installed pathname does not change
which compiler is identified. Other platforms conservatively decline persistence
until they have an equivalent running-image boundary.

An embedded Go build ID cannot replace the SHA: changing an equal-length string
literal in a linked binary changed its output and SHA while `go tool buildid`
continued to report the same entire ID, including the final content component.
A missing or stripped ID is therefore not the only case needing full hashing.

`BenchmarkCacheReceiptPrerequisites` hashes afresh on every iteration, rather than
using the process memo or the launcher's established stat shortcut. Set
`BORK_BENCH_COMPILER_IMAGE` to a freshly built CLI to avoid measuring the larger
Go test image. Seven samples of ten iterations on this host used a 16,705,933-byte
Bork image and a 17,142,188-byte Go launcher:

| Prerequisite | Median ms | Min–max ms |
| --- | ---: | ---: |
| Compiler SHA | 10.35 | 9.57–12.63 |
| Go launcher SHA | 11.71 | 11.13–12.20 |
| Source receipt content/membership validation | 0.047 | 0.035–0.056 |
| Both hashes and validation, sequential | 19.86 | 19.66–20.17 |
| Both hashes concurrent with validation, joined | 10.78 | 10.42–11.70 |

These are uncached identity and loader-validation costs with warm filesystem
buffers, not complete fresh-CLI disk-hit measurements. The source sample observes
one small file, its directory, and a missing module file. Configuration, standard
library names, decoding, and publication still need their own receipts and
measurement. Samples ran alongside other checks; separately calibrated medians
need not sum to the combined median. Concurrency reduces wall time, not CPU work.
When persisted reads are enabled, start image and resolved-launcher hashing beside
source loading/receipt validation, and join before accepting any result. Keep
ordinary uncached startup free of this work while disk reads remain disabled.

The following Go receipt slice restores only supported metadata-name configuration
and positively standard package inventories. It re-establishes launcher and
bridge evidence from the current process and never imports persisted stat tuples
or elapsed-time guards. Configuration restoration uses captured effective settings
without `go env`, but retains content, membership, directory-mode and compiler
availability checks. Standard-name restoration requires every recorded package's
complete immediate-file inventory, including ignored and test files. These private
primitives do not enable result hits; fresh-CLI timing must include their validation
and outcome decoding when the storage path is wired.

### Fresh-process disk result gate

The first result-cache integration uses a link-time test gate and an explicit
private root. Environment settings alone cannot enable ordinary compiler builds.
Automatic use remains disabled pending admission budgets, eviction and `bork clean`.

Seven fresh CLI processes per row, warm filesystem and Go caches, on the same
Ryzen 7 host while other checks ran:

| Hello operation | Ordinary median (min–max), ms | Disk hit median (min–max), ms | First miss/publication, ms |
| --- | ---: | ---: | ---: |
| Check | 51.27 (49.69–70.73) | 38.02 (36.53–41.54) | 167.94 |
| Emit | 58.81 (57.93–65.21) | 37.89 (37.10–41.14) | 178.02 |

Initially hits took about 85 ms: five launcher hash passes and two SDK inventory
passes erased the checking savings. The consolidated path starts actual compiler
and launcher hashing concurrently at process initialization, restores configuration
from the independently captured launcher evidence, hashes each metadata inventory
once, then checks source/configuration and launcher content before serving. It
never trusts persisted stat evidence. Ordinary Session validation is unchanged.

An actual CLI first-miss phase trace (seven fresh processes) explains the remaining
penalty. Total median was 162.91 ms (160.18–172.29); independently measured phase
medians do not necessarily sum to that total:

| Phase | Median ms |
| --- | ---: |
| First Session configuration | 40.82 |
| Checking, including metadata inventory capture | 52.34 |
| Parsing | 2.72 |
| Publication Go receipt | 20.55 |
| Publication metadata receipts | 18.42 |
| Publication final receipt validation | 10.36 |
| Encoding and atomic store | 2.46 |
| Publication source receipt | 0.02 |

The write itself is cheap. First-Session inventory capture and repeated launcher
validation inside component receipts dominate the extra cost. The publication
benchmark separates these stages, but a warm long-lived benchmark can activate
the two-second launcher stat guard and therefore understate fresh-process costs.
Use the test-gated CLI phase trace for first-miss profiling. Before automatic use,
consolidate publication around a final composite certification and reduce inventory
work on misses and bypasses. Future build/run/test integration can overlap owned
publication with downstream Go builds; check/emit exit immediately, so background
publication alone cannot hide their cost.

All 37 runnable top-level `examples/` programs emitted successfully in the gate:
17 (46%) qualified, 17 bypassed for compile-time evaluators (including proof
execution), two for external/unavailable Go names (`sql`, `uuid`), and one for
assets (`embed`). Eligible examples include filesystem, process, CSV, archive,
accounts and parallel-list programs as well as hello; the eligible fraction still
limits this layer's practical benefit.

| Realistic emission | Ordinary median (min–max), ms | Repeated gated bypass median (min–max), ms |
| --- | ---: | ---: |
| config | 320.73 (312.94–354.65) | 412.98 (395.44–432.26) |
| http_server | 515.08 (501.32–556.60) | 584.23 (571.92–617.48) |

These rows use seven fresh processes, the host's default staging fallback and warm
Go cache; they are not the writable stable-staging measurements above. Both run
proof evaluators, so neither publishes a result nor produces a disk hit. Proven
execution receipts are prerequisite to gains on these programs. Keep evaluator
bypass until the complete closure and external-input contract is established.

The follow-up miss path compiles through the ordinary pipeline with deferred
usage observations. Evaluator/export/custom-driver/foreign-name/asset bypasses
stop before SDK/configuration inventories or compiler-identity hashing. Candidate
lookup is bounded and untrusted until actual compiler identity is checked; no
candidate on a known-bypass program causes no cache hashing. Eligible misses
capture fresh configuration and independently re-query metadata names between SDK
observations, compare them with names used by checking, and certify current source
inputs before publication. Capturing SDK bytes after checking without re-querying
would not prove the earlier result.

Seven fresh processes per row on the same host, within this follow-up experiment:

| Emission | Ordinary median (min–max), ms | Gated bypass median (min–max), ms |
| --- | ---: | ---: |
| config | 313.07 (310.86–314.63) | 311.90 (308.32–314.86) |
| http_server | 459.10 (453.05–496.42) | 459.51 (455.84–478.41) |

These paired results remove the inventory penalty on bypasses within observed
variation. They do not claim evaluator result hits. Compiler and launcher hashing
is now lazy: overlap it with candidate validation or eligible publication, while
skipping it entirely on known-bypass misses. This takes precedence over unconditional
startup hashing because unknown programs must not pay extra cache inventory costs.

Composite publication snapshots component observations without repeating their
current-input checks, then certifies each metadata inventory once before a final
source/assets/shared-context check. Standalone component receipt APIs still perform
full validation. Paired seven-process hello check first-miss profiles after the
ordinary-cost bypass change:

| Phase | Before composite, median ms | Composite, median ms |
| --- | ---: | ---: |
| Late eligible inventory capture | 98.53 | 88.55 |
| Publication Go receipt | 20.15 | 0.05 |
| Publication metadata receipts | 18.01 | 0.22 |
| Final publication certification | 10.10 | 18.36 |
| Encoding/store | 2.13 | 2.19 |
| Total first miss | 207.91 | 168.37 |

This removes repeated certification; it does not meet the automatic-cache
acceptance bar of ordinary compilation plus about 10–15 ms on an eligible miss.
The remaining late inventory capture is the target. Reusing SDK certification
from launcher SHA and GOROOT directory identity is insufficient: an in-place SDK
file edit with restored mtime leaves launcher bytes and the parent directory's
device/inode/mtime/ctime unchanged. SDK content proof remains required. The next
experiment moves owned receipt verification/publication to a bounded, low-priority
detached child, retaining ordinary-cost bypasses and explicit opt-out. Automatic
use waits for measured parent overhead and lifecycle integration.

The detached-publication experiment retains SDK content proof in a child instead
of charging certification to the edit/compile loop. Nine paired fresh processes
per row on hello, warm Go cache, with a separate empty result cache for every miss:

| Operation | Ordinary median (min–max), ms | Detached parent median (min–max), ms | Publication completion median (min–max), ms |
| --- | ---: | ---: | ---: |
| check | 48.42 (47.70–50.15) | 49.35 (48.08–49.72) | 169.48 (167.29–207.61) |
| emit | 56.94 (55.46–58.48) | 58.76 (56.89–80.88) | 179.92 (177.93–204.52) |

The parent pays about 0.9–1.8 ms in this experiment, within the ordinary-plus-10–15
ms miss target. Certification work still happens, at low priority and bounded
runtime. This measures return latency separately from receipt availability; an
immediate repeat while publication is in flight may compile again. Large jobs and
busy/unwritable caches skip publication. Automatic use remains off pending lifecycle
integration; proof-evaluator programs continue to bypass at ordinary cost.

Staging version 2 adds shared accounting and persistent program metadata while
keeping stable absolute package paths. Seven paired fresh CLI emissions per row,
with writable private XDG_CACHE_HOME, shared warm Go build cache, and both staging
versions warmed before measurement:

| Program | Version 1 median (min–max), ms | Version 2 median (min–max), ms |
| --- | ---: | ---: |
| config | 211.41 (205.40–223.16) | 208.59 (199.35–408.42) |
| http_server | 261.40 (258.22–287.62) | 271.11 (263.10–301.27) |

Emitted bytes match in every pair. Tests were running during this experiment;
the config maximum records that variation. HTTP's median adds about 10 ms, which
should be revisited if accounting grows with a fuller cache. Cache-full, busy and
unsupported cases still build through temporary staging. These are ordinary CLI
emission measurements, separate from first-request Session inventory seeding.

### Cache population and accounting cost

Automatic disk caching must keep ordinary per-build overhead flat as more
programs populate the cache. The age-based policy supersedes size/count admission
and ledger/low-water proposals: no global listing or eviction is allowed on the
per-build path. Measure lookup, hits and publication at 1, 1,000, 10,000 and
100,000 entries for both layers, including new-key publication into a populated
cache. Measure bounded daily trim separately. Replacing the same key alone does
not establish this bar.

The superseded full-inventory implementation failed that requirement. On Linux,
Go 1.27.1, Ryzen 7 5700G, five warm samples of three iterations produced the
following medians and ranges in milliseconds:

| Measured work | 1 entry | 128 entries | 512 entries | 1024 entries |
| --- | ---: | ---: | ---: | ---: |
| Result admission | 0.168 (0.160–0.194) | 13.432 (13.235–14.119) | 54.027 (53.665–54.786) | 108.610 (107.987–110.040) |
| Config staging publication | 1.065 (1.051–10.089) | 31.454 (31.137–32.066) | 124.372 (123.134–127.996) | 242.887 (241.358–249.794) |
| HTTP staging publication | 1.172 (1.072–10.163) | 32.204 (31.827–32.539) | 123.034 (121.294–125.206) | 244.695 (243.358–247.724) |

These isolated benchmarks use private temporary cache roots. Entry counts
include the measured entry. Result fixtures have valid request headers and
small recognizable envelopes, but omit receipts and payloads: admission reads
only the bounded request prefix. Staging uses actual emitted config/HTTP bytes
for the measured target; other entries have a small Go main, module and metadata
pointing at the same existing program root. The target is primed before timing.
Checking, generation and Go subprocesses are excluded from the measured loop.
Result timing covers admission only; staging timing includes locks, complete
publication and release. The two rows measure different amounts of work.

Historical reproduction at the lifecycle-design PR #249:

```sh
go test ./internal/driver -run '^$' -bench '^(BenchmarkGoStageAccounting|BenchmarkCacheResultAccounting)$' -benchtime=3x -count=5
```

A preliminary 128-entry staging CPU profile with four-node synthetic trees
(before adding their module files) attributed 1.96 s to `stageInventory` out of
2.02 s in `stageGoStable`. Filesystem traversal/path resolution dominated;
staging does not fsync. Whole-process CPU profiles include untimed setup.

The design now removes these scans from the build path entirely. Hourly use
marking and bounded off-path daily trim replace total-byte/count accounting.
The measurements above preserve the superseded policy baseline; the new layout
and maintenance implementation must establish the larger-population acceptance
matrix before auto-on. See [disk-cache lifecycle](disk-cache.md#age-based-lifecycle-and-direct-lookup).


The first sharded-layout slice removes admission scans and inventories from the
per-build path. Three samples of twenty primed-entry publications on the same
host give these medians (min–max), in milliseconds:

| Program | 1 entry | 128 entries | 512 entries | 1024 entries |
| --- | ---: | ---: | ---: | ---: |
| config | 0.932 (0.926–2.815) | 0.923 (0.921–0.926) | 2.719 (0.932–2.801) | 2.800 (2.783–2.869) |
| http_server | 0.965 (0.953–2.811) | 0.965 (0.951–0.992) | 2.753 (0.955–2.790) | 2.839 (0.998–2.873) |

Payloads are 84,756 and 178,290 emitted Go bytes respectively. Private roots,
priming and synthetic surrounding trees follow the baseline staging setup above;
checking, generation and Go subprocesses remain outside the timed loop. Fixed
sample spikes remain visible, so these samples establish removal of the previous
hundreds-of-milliseconds scan growth, not the final flat-cost acceptance claim.
Result publication is covered by direct-path/no-eviction and alternating-compiler
locator regressions here; its timing matrix follows with daily trim. The larger
1/1k/10k/100k matrix must cover lookup, hits and new-key publication as well as
replacement before automatic complete-result caching is enabled.

```sh
go test ./internal/driver -run '^$' -bench '^BenchmarkGoStagePublication$' -benchtime=20x -count=3
```


The installed-SDK hit policy supersedes the earlier requirement to hash SDK
contents and launcher bytes on every fresh-process hit. As with Go's object
cache, in-place GOROOT or launcher edits are unsupported. Go receipt schema 2
records the installation root/version and launcher/VERSION file stat identities;
metadata-result hits validate those identities without SDK membership walks.
Source/configuration/environment/compiler validation remains in place. Generic
content-certified receipt restoration and publication retain their content checks.

Fresh CLI wall times on the same host, 15 invocations per arm after one priming
invocation, show these medians (min–max), in milliseconds. These specially built
CLIs enable the still-test-gated result store; every claimed hit was confirmed
by its probe. Go caches/filesystem caches are warm and stdout is discarded.

| Fixture / operation | Ordinary uncached | Old content-heavy hit | Installed-SDK hit |
| --- | ---: | ---: | ---: |
| hello / check | 48.89 (47.72–50.33) | 37.03 (36.21–38.24) | 18.50 (17.53–19.51) |
| hello / emit | 57.49 (56.38–58.34) | 37.44 (36.86–37.95) | 19.14 (18.90–19.59) |
| calculator, two files / check | 50.05 (49.21–51.64) | 37.26 (36.43–38.61) | 19.05 (18.29–19.78) |
| calculator, two files / emit | 62.30 (61.87–64.25) | 38.08 (37.29–39.01) | 19.84 (19.30–20.84) |

Hello is 12 Bork bytes, emitting 37,719 Go bytes; its old receipt was 146,154
bytes. The two-file fixture splits the unchanged 5,064-byte calculator example
before `fn main`, putting parser/evaluator declarations in `expressions.bork`
and its entry point in `main.bork`. Calculator is the largest eligible example
by Bork source bytes. The larger http_server example (7,624 bytes) and the
two-file config example still bypass because they execute proof evaluators.
These measurements do not claim evaluator hits or a speedup for those examples.
`build` is also outside the current test-gated result lookup entry points, so
its ordinary Go build cost is not reported as a result-cache hit.

The content-heavy before benchmark separated decode (3.90 ms), source hashing
(0.057 ms), Go receipt restoration (19.94 ms, including launcher hashing), and
SDK names content/membership validation (7.02 ms). A launcher SHA alone cost
9.64 ms. These are N=3 medians of five-iteration in-process averages, measured
independently and not additive: generic restoration also performs an extra
launcher endpoint validation that the CLI had already consolidated. The exact
old CLI-style overlapped entry point cost 28.63 ms in-process before process
startup/compiler identity, versus the 37–38 ms fresh-CLI results above.

Population acceptance and automatic enablement remain separate follow-ups.


The final population matrix uses the installed-SDK policy with warm filesystem
caches. Each cell below is the median of three samples of ten operations
(min–max), in milliseconds. Private roots are seeded with 1/1k/10k/100k entries
in each layer; neighbours are synthetic small files/trees with uniformly hashed
keys in one compiler namespace. The measured target is a real hello artifact
and stage (12 Bork bytes, 37,719 emitted Go bytes). Setup/cleanup are excluded.
New-key publication retains each generation, and keys remain fresh across
repeated samples. Each publication arm adds 30 entries to its initial population.

| Operation | 1 | 1,000 | 10,000 | 100,000 |
| --- | ---: | ---: | ---: | ---: |
| Result locator + decode | 5.444 (5.183–5.603) | 4.655 (4.572–5.323) | 4.565 (4.344–4.954) | 4.583 (4.195–4.614) |
| Result validated hit | 6.858 (6.849–6.926) | 6.299 (6.174–6.431) | 6.229 (6.142–6.945) | 6.431 (6.425–6.459) |
| Result new-key publication | 2.669 (2.128–3.012) | 1.925 (1.662–1.933) | 1.802 (1.793–1.840) | 1.962 (1.846–2.153) |
| Stage direct-path stat | 0.011 (0.010–0.011) | 0.008 (0.008–0.009) | 0.009 (0.008–0.011) | 0.008 (0.008–0.009) |
| Stage repeat publication | 1.063 (1.050–1.300) | 1.114 (1.073–4.506) | 1.049 (1.037–6.162) | 1.087 (1.057–1.215) |
| Stage new-key publication | 1.165 (1.136–1.361) | 1.147 (1.108–1.194) | 1.110 (1.088–1.151) | 1.127 (1.092–1.140) |
| Incomplete-cycle busy admission | 0.054 (0.053–0.076) | 0.054 (0.053–0.079) | 0.051 (0.051–0.065) | 0.051 (0.051–0.057) |

The stage stat row measures its direct content-addressed path only, not semantic
validation or Go's object-cache hit. Staging always republishes generated bytes,
so the repeat-publication row is the actual repeat-stage cost. Result validation
includes source/configuration/installed-SDK checks and hourly touch, but compiler
image hashing/process startup are excluded here: the fresh-CLI comparison above
includes those costs. Fixed replacement spikes remain visible in the ranges.

The incomplete-cycle arm writes a valid unfinished cursor, holds the actual
admission lease and repeatedly requests scheduling. No request launches another
worker; its approximately 51–54 microseconds and constant-sized allocations show
that a huge remaining cycle introduces neither a growing queue nor enumeration
on the build path. The detached CLI integration separately verifies a real worker
can remain blocked while its parent completes, then resume on a pipe notification.

Whole-cycle fresh-entry trim is measured separately, including streaming, age
checks and cursor publication. At 1/1k/10k entries, N=3 three-iteration samples
were approximately 0.067/0.52/3.9 seconds; a separate 100k single-cycle probe took
39.65 seconds, resuming across five-second worker deadlines. That final probe is
a single observation, not a median. These are total maintenance wall time
without detached low-priority scheduling, not parent build latency. Streaming
allocations are cumulative over the cycle, not retained inventories. Busy entries
and age-based deletion are covered by trim regressions; these timing fixtures
are fresh, so no entries are deleted. No size pressure/admission eviction remains.

The hot paths stay flat through 100k entries. The acceptance criterion concerns
entry-count scaling; work still depends on the selected artifact payload and its
source/configuration inputs. The matrix does not claim evaluator reuse.

```sh
go test ./internal/driver -run '^$' -bench '^BenchmarkCachePopulation/entries(1|1000|10000|100000)$/(result|stage|trim-mid)' -benchtime=10x -count=3
go test ./internal/driver -run '^$' -bench '^BenchmarkCachePopulation/entries100000$/trim-fresh-whole-cycle$' -benchtime=1x -count=1
```

## Automatic CLI cache rollout

Linux automatic reuse was measured with fresh ungated CLI processes on Go 1.27.1,
using `fn main(){println(42)}` (22 source bytes), seven paired observations per
mode. Eligible miss publication completed before the subsequent hit measurement,
using the publisher admission lock as a completion barrier outside timed work.
These are parent wall medians; background certification CPU is excluded.

| Mode | `BORK_CACHE=off` | Automatic miss | Automatic hit |
| --- | ---: | ---: | ---: |
| check | 52.07 ms | 54.02 ms | 20.63 ms |
| emit | 62.08 ms | 62.72 ms | 20.02 ms |
| build | 282.78 ms | 287.41 ms | 62.91 ms |
| run | 273.55 ms | 289.70 ms | 130.68 ms |

`off` disables stable staging too, so build/run differences include Go's cache
benefit from stable paths and are not isolated Bork result-cache speedups. Go
still builds on each build/run, and run still executes. The check/emit miss
increments remain below the ordinary-plus-10–15 ms rollout bar. Host load and
Go subprocess variation limit interpretation of the build/run miss deltas.

Known-bypass checks were compared separately with persistent staging enabled in
both arms: an ordinary test-marked CLI (result reuse disabled), and the ungated
automatic CLI. With CGO disabled, one initial pair excluded and six measured
pairs, config medians were 199.44 vs 198.71 ms; http_server medians were 213.04
vs 216.97 ms. Both produced a `bypass` eligibility probe in a separate enabled
test image. Neither captures execution observations or SDK inventories for
publication. This comparison keeps staging available, unlike `BORK_CACHE=off`.

The previously recorded 1/1k/10k/100k matrix remains the population-cost gate:
lookup, validated hit and publication use direct sharded paths; only bounded
background trim enumerates. Automatic misses never fall back to inline
certification when the cache or detached process facilities are unavailable.
