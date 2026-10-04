# Go package output experiment (bork-iqrd0t)

Status: measured scalar-only prototype. The production compiler keeps its
single-package output. The prototype establishes potential savings but does
not meet the acceptance condition on an existing real multi-package program.

## Experiment boundary

The benchmark partitions actual compiler-generated Go for a Bork scoring
application into one package per imported domain, one main package and one
shared prelude/runtime package. Each domain has forty threshold/weighted-score
policies and a formatted result. Four domains contain 164 functions; twelve
contain 492. The shared runtime includes the single `_Rune` representation,
renamed to `B_Rune` for the experimental Go boundary. Top-level references are
resolved before moving declarations; local bindings retain their identity.
Dependencies use qualified exported names. Runtime code is not copied into
each domain package.

This is a synthetic application model, not a production service. The
benchmark-only partitioner handles the selected scalar free-function workload.
It falls back to the original bytes for single-package programs, user records
or sealed types, methods, generics and unsafe Go. Unsupported reverse
dependencies fail the experiment rather than silently producing a wrong build.
It is not an alternative compiler backend.

`examples/config` is the only current example with more than one user source
directory: two directories and 3,648 Bork bytes. Its private records, checked
constructors and derived codecs require the flat fallback. Hello and calculator
also stay flat. Their comparisons measure the fallback/common case, not the
cost or benefit of a general emitter. All measured programs produced identical
stdout for their initial fixture builds to the ordinary compiler. Edited builds
were compiled successfully but not executed for parity. A focused regression verifies the scoring
model's output and the shared runtime's Rune declaration.

## Measurements

Linux/amd64, Go 1.27.1, AMD Ryzen 7 5700G; October 4, 2026. Shared development
host; these observations are not regression thresholds. Each edit changes a
literal to a previously uncompiled value. Alternating two source versions would
incorrectly measure object-cache hits. Stable stage paths are reused within each
sample. The SDK's Go object cache is warm. Setup, editing, execution and parity
builds are outside the timed edit interval. No complete-result Bork cache is used.

Go-only results are medians of five samples of five builds, with min–max ranges
of sample means, in milliseconds. Checking, generation and partitioning happen
before timing; only the changed generated file is written.

| Domains | Flat Go bytes | Split Go bytes | Flat build, ms | Split build, ms |
| --- | ---: | ---: | ---: | ---: |
| 4 | 60,221 | 59,320 | 316.15 (312.39–328.91) | 138.48 (135.78–139.47) |
| 12 | 105,361 | 105,602 | 385.12 (380.80–390.65) | 142.37 (140.98–143.31) |

End-to-end edit results include loading, configuration, checking, generation,
partitioning, writing the complete Go tree and Go building. These are medians
of three samples of three edits, with min–max sample-mean ranges. First-build
columns give medians of three first application builds in a new stage, including
the same compiler pipeline. They are **not** cold SDK builds.

| Program | Flat edit, ms | Candidate edit, ms | Flat first, ms | Candidate first, ms | Candidate Go packages |
| --- | ---: | ---: | ---: | ---: | ---: |
| config | 939.05 (917.22–940.72) | 932.36 (931.58–935.63) | 934.3 | 922.8 | 1, fallback |
| hello | 297.36 (297.20–299.29) | 301.90 (299.46–303.04) | 300.8 | 299.6 | 1, fallback |
| calculator | 363.68 (361.41–372.96) | 376.17 (362.34–380.22) | 362.7 | 359.4 | 1, fallback |
| scoring, 12 domains | 453.83 (444.73–479.98) | 219.87 (219.74–230.07) | 447.2 | 311.7 | 14 |

The model reduces Go-only edit time by 56–63%, and twelve-domain end-to-end edit
time by about 52%. Host variation remains visible. Single-package differences
are paired fallback observations, not a guarantee of zero overhead. Config's
proof evaluations still run freshly. First builds do not establish fully cold
Go-cache costs or general scaling.

A separate config edit profile through the ordinary stable driver build path
(three samples of five edits) gives a 933.61 ms median: facts/proof evaluation
416.4 ms, final Go build plus staging 479.0 ms, generation 19.72 ms,
configuration 17.06 ms, checking 3.025 ms, parsing 1.924 ms, lowering 0.240 ms
and lifetimes 0.573 ms. There was no comptime phase or Go export-type loading.
Reusing type-checking alone cannot materially improve this workload. This is
separate from the custom-stage layout comparison above; values are medians of
individual phase sample means and need not sum exactly to the total median.

Measurements used compiler revision `c61f18690312dd44f2c2272e0372f01a0e3ce40e`.
The temporary benchmark harness is attached to awb ticket `bork-iqrd0t` as
`go_packages_benchmark_test.go`. Retrieve it into a clean checkout to reproduce;
it is deliberately excluded from the production compiler and this docs-only PR.
The harness SHA-256 is
`79571c543265673fe5f5441194c03921fb79a4be6f43304e9c039263d22e4387`.

```sh
awb attach get bork-iqrd0t go_packages_benchmark_test.go --output internal/driver/go_packages_benchmark_test.go
go test ./internal/driver -run '^TestGoPackageExperiment$' -count=1
go test ./internal/driver -run '^$' -bench '^BenchmarkGoPackageEdit$' -benchtime=5x -count=5
go test ./internal/driver -run '^$' -bench '^BenchmarkGoPackageBuild$' -benchtime=3x -count=3
go test ./internal/driver -run '^$' -bench '^BenchmarkConfigEditPhases$' -benchtime=5x -count=3
```

## Decision and requirements for a general emitter

Do not adopt this partitioner or change the default layout. A general emitter
must assign ownership to types, instances, dictionaries and specialized helpers,
and define the shared runtime ABI without changing reflection or unsafe-Go
semantics. Exporting every private generated field/name is not an acceptable
shortcut: field visibility, dynamic interface identity and reflected type names
are observable. Generated callbacks and concrete dictionaries can also create
Go package cycles even when the Bork import graph is acyclic.

Before adoption, repeat the end-to-end experiment on config with genuinely split
output, cover cross-package records/sealed types, generic instances, unsafe Go,
assets and test/mock overlays, and verify identical observable behavior. Retain
a flattening fallback for unsupported graphs and modes. Generated package paths
must remain stable across unrelated graph edits; loading-order indices are not
package identities. The flattened `emit` artifact and persistent result receipts
need an explicit multi-file artifact/layout migration before cached CLI builds
can reuse a split tree. Single-package output should bypass splitting entirely.

This extends the output-layout boundary in [incremental.md](incremental.md).
The current stable staging and [disk-cache](disk-cache.md) policies remain intact.
