# CI test shards

CI runs every Go test exactly once, under the race detector, in ten shards
balanced by `scripts/ci.py`. The race run checks everything an ordinary run
checks: the same tests and assertions, plus data-race detection. Earlier CI ran
each test twice, ordinary and race, and both runs spent most of their time on
the same child `go build`s of generated programs, which are not race-instrumented.
The lint job keeps the ordinary build path exercised: it installs `bork`
without `-race`, checks and lints every example with it, and runs a few
examples, including concurrent ones, against their golden output.

Each shard discovers all packages with `go list -race`, and the Test, Example
and Fuzz parents of `internal/driver`, `internal/lsp` and `cmd/bork` with
`go test -race -list`. Those three packages are too slow to run whole, so their
parents are balanced individually. Every other package runs whole. `TestCases`
and `TestExamples` are split one step further: each child is one fixture
directory under `testdata/cases` or `examples`, discovered with the parent's
own directory rules, so hidden entries and symlinks are excluded. Fixture names
must use ASCII letters, digits, underscores, dots or hyphens; other names fail
discovery rather than risk Go's subtest-name sanitization or deduplication
silently changing coverage. If either parent's directory-selection rules
change, update the runner and its fixture-discovery regression test together.

A shard runs one `go test` per split package plus one for its whole packages,
concurrently, under a shared deadline. Each `-run` expression uses Go's
top-level `|` alternatives: `^(TestA|TestB)$` keeps every subtest of a plain
parent, and `^TestCases$/^(case_a|case_b)$` selects only the listed fixtures.

The seven concurrency runtime tests in `internal/gen` run their child Go tests
with `-race` because the parent test binary has race instrumentation. CI no
longer runs those child tests without `-race`; the lint job's ordinary example
runs cover the uninstrumented generated runtime.
Independent runtime roots run in parallel. Map and snapshot children retain
their existing ordinary Go test commands.

The four HTTP `*Race` integration tests build and execute generated programs
with the Go race detector. They skip in ordinary test binaries, so local
`go test ./...` without `-race` does not run them; CI always does.

New driver test roots must call `t.Parallel()` unless a nearby comment explains
why they must run sequentially. Check descendants and shared helpers for
`t.Setenv`, `t.Chdir`, and mutable process globals before adding parallelism;
those operations also require sequential ancestors. Prefer per-call settings
and independently owned fixture roots. See the [serial-root audit](design/driver-test-scheduling.md)
for existing exceptions and planned refactors.

The partition assigns the longest measured units first to the least loaded
shard, breaking ties by name. Placing the first test of a split package in a
shard adds a five-second overhead for starting that package's test binary, so
small tests cluster. `scripts/ci-timings.json` holds the weights. A new package
or test receives a five-second provisional weight and still runs; stale entries
cannot add or remove coverage. No changed-file filtering is applied, on PRs or
main.

Inspect a partition or run one shard from the repository root:

```sh
python3 scripts/ci.py plan
python3 scripts/ci.py run 3
python3 -m unittest discover -s scripts -p 'test_ci.py'
```

Each shard reports the wall time of its run step, including discovery and
compilation but not cache restore or save. Above 120 seconds it warns, and
above 180 seconds it warns that the budget was exceeded. Discovery, which links
the split packages' race test binaries, and the test run each have their own
180-second limit; reaching one fails the shard and terminates its process
groups, naming the tests still running. A cold build cache, after a go.sum or
Go version change, can push one run past the budget without failing it. A shard also fails when a selected package, test or fixture produced
no result, since Go exits successfully when a `-run` pattern matches nothing. Its log shows each package result and
the output of failing tests. CI uploads a report with the selected tests,
elapsed seconds and status, and the raw `go test -json` events.

Refresh timing weights after substantial test changes from the events of one
complete, successful CI run on main, then review the timing diff:

```sh
gh run download <run-id> --pattern 'ci-*' --dir /tmp/bork-ci
python3 scripts/ci.py refresh --input /tmp/bork-ci/*/*.jsonl
```

Shard events reflect a hosted runner's speed and each shard's own contention.
A local `go test -race -json ./... -count=1 > /tmp/bork.json` also works as
input, but running every package at once inflates parallel tests' weights.
Refresh refuses failed, truncated, repeated or incomplete runs. Weights use the
greater of a parent's reported elapsed time and the sum of its immediate child
weights, recursively. This includes work done by parallel children when a parent
reports near-zero elapsed time and avoids counting nested serial time twice.
These conservative workload weights guide balancing; they are not predictions
of wall time.

If shards grow beyond the budget, refresh the timings first, then raise
`SHARDS` in the runner together with the workflow's `shard` list, which
`test_ci.py` checks. If a single unit exceeds the budget, split that workload
rather than increasing the timeout.
Keep aggregate dependencies current when adding jobs: `CI ok` accepts only
successful dependencies, including all matrix children. The compiler performance
`benchmark` check runs in a separate workflow.

Every Go job restores module and build caches through the shared setup action.
Only successful main jobs save new snapshots, through the `save-go-cache`
action, with a distinct writer key for each job or shard; PR runs reuse main's
caches. Keys put the writer name before the go.sum hash, so a job keeps its own
snapshot across dependency changes. Restore fallbacks then prefer another test
shard, since other jobs hold no race builds, and finally borrow any job's
content-addressed build entries. Before
saving, the action deletes build cache entries this job did not use: Go
refreshes a used entry's modification time once it is an hour old, so entries
older than an hour before the job started went unused. The action removes whole
entries, since an executable entry is a directory whose own modification time
records use. Without trimming, each
snapshot accumulated every commit's builds until restoring it took minutes. A
timings refresh moves units between shards, so the next main run rebuilds the
moved fixtures' generated programs once.
Tests use `-count=1`, so Go build caching never replaces test execution. Cache
effectiveness also depends on generated Go test programs retaining stable
build paths.

Linux golden output cases and examples retain one native output slot per test
identity under the test stage root's `bork/test-outputs-v1` directory. Every test
still calls public `Build` (plain `go build`) and starts a fresh application
process. Go's build action identity decides whether an existing target is fresh;
source, module, environment and toolchain changes reuse the same slot. Its
exclusive lock stays held through build and execution. Source fixtures keep
their normal full cleanup. Failed builds/tests discard the target, and targets
without readable Go build metadata are removed before rebuilding. Unavailable or unsafe
cache directories and other platforms use temporary outputs.

Successful outputs survive test invocations; fixture identities do not create
additional copies for changed inputs or tool versions. With tests stopped,
remove the pool to wipe outputs, including slots for deleted/renamed fixtures.
For the default Linux test stage root:

```sh
rm -rf "${TMPDIR:-/tmp}/bork-driver-tests-$(id -u)/bork/test-outputs-v1"
```

With an explicit `XDG_CACHE_HOME`, remove its `bork/test-outputs-v1` directory
instead. Production `bork clean` owns compiler artifacts and does not remove
this test-only layer. CI persists `GOCACHE`, not these native targets, so the
main benefit is repeated local runs; a cold hosted run may see little change.

CLI integration tests build at most one ordinary compiler and one test-gated
compiler per package test run. Each build uses the environment captured before
individual tests change runtime settings. A package-owned temporary directory
holds these native executables and is removed after the run; no executable is
retained between invocations. CLI commands still start fresh processes, cache
fixtures remain isolated, and tests requiring different compiler images retain
their distinct builds.

Both shared CLI compilers carry a linker-only test marker so automatic detached
cache workers can require explicit test opt-ins. The ordinary compiler uses
`cacheTestGate=test`, which enables neither cache probes nor test callbacks;
the cache fixture compiler retains `cacheTestGate=enabled`.

CI runs in one concurrency group per workflow and ref, cancelling in-progress
runs on every ref including `main`: a newer push supersedes older runs, so only
the latest commit is tested.
