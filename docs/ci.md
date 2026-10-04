# CI test shards

Normal and race tests use `scripts/ci.py`. Each mode discovers all packages with
`go list` and all driver Test, Example and Fuzz parents with `go test -list`,
using the same race build tags as the actual run. `core` covers every package
except `internal/driver`. Golden cases use three shards, examples one, and the
remaining driver parents four integration shards. Anchored parent patterns retain
every subtest. Golden case children are discovered from `testdata/cases` using
the same directory rules as `TestCases`: hidden entries and symlinks are excluded.
Each discovered fixture directory runs in exactly one case shard, including new
directories. Fixture names must use ASCII letters, digits, underscores, dots or
hyphens; unsupported names fail discovery rather than risking Go's subtest-name
sanitization or deduplication silently changing coverage. If `TestCases`'
directory-selection rules change, update the runner
and its fixture-discovery regression test together.

The four HTTP `*Race` integration tests build and execute generated programs
with the Go race detector. They skip before parallel scheduling in ordinary
test binaries and run when the parent binary is built with `-race`. Their parent
names remain discoverable in both modes; race shards retain all four tests.
Ordinary HTTP golden cases still run in normal shards.

The integration partition assigns the longest measured parents first to the
least loaded shard, breaking ties by name. `scripts/ci-timings.json` contains
separate normal/race weights. A new parent receives a five-second provisional
weight and still runs; stale entries cannot add or remove coverage. New packages
always land in `core`. No changed-file filtering is applied, on PRs or main.

Inspect a partition or run one shard from the repository root:

```sh
python3 scripts/ci.py plan --mode normal
python3 scripts/ci.py run --mode race driver-integration-0
python3 -m unittest discover -s scripts -p 'test_ci.py'
```

Each shard reports actual wall time, including discovery and compilation. Above
120 seconds it warns; at 180 seconds it fails and terminates its test process
group. CI uploads JSON reports with selected tests, elapsed seconds and status.
Compare the slowest shard and workflow start/end times in GitHub Actions;
cache/toolchain setup and artifact upload also contribute to total CI time.

Refresh timing weights after substantial test changes using one complete,
successful uncached run per mode, then review the timing diff:

```sh
go test -json ./... -count=1 > /tmp/bork-normal.json
python3 scripts/ci.py refresh --mode normal --input /tmp/bork-normal.json
go test -race -json ./... -count=1 > /tmp/bork-race.json
python3 scripts/ci.py refresh --mode race --input /tmp/bork-race.json
```

Refresh refuses failed, truncated, repeated or incomplete runs. Weights use the
greater of a parent's reported elapsed time and the sum of its immediate child
weights, recursively. This includes work done by parallel children when a parent
reports near-zero elapsed time and avoids counting nested serial time twice.
These conservative workload weights guide balancing; they are not predictions
of wall time. Measurements depend on machine load and parallel scheduling.

If integration or golden shards grow beyond the budget, increase
`INTEGRATION_SHARDS` or `CASE_SHARDS` in the runner. CI generates both mode matrices
from `scripts/ci.py matrix`, so no workflow shard list needs updating. If `core`
or a dedicated parent exceeds the budget, split that workload rather than
increasing the timeout.
Keep aggregate dependencies current when adding jobs: `CI ok` accepts only
successful dependencies, including all matrix children. The compiler performance
`benchmark` check runs in a separate workflow.

Every Go job restores module and build caches through the shared setup action.
Only successful main jobs save new snapshots, with a distinct writer key for each
mode/shard; PR runs reuse main's caches. Restore fallbacks can borrow another
shard's content-addressed build entries. Tests use `-count=1`, so Go build caching
never replaces test execution. Cache effectiveness also depends on generated
Go test programs retaining stable build paths.

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
