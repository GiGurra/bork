# Standard interpolator validator artifacts (bork-gc48vu)

> **Status:** Implemented: generated standard-library validator artifacts and drift checks. Current implementation reference: [interpolation validators](../grammar.md#writing-an-interpolation-validator). Artifact execution is fresh per check; this feature does not cache results across builds.

Generate owner-declared standard-library validator implementations into the
compiler binary with `go generate`. A staleness test regenerates and compares
both code and the binding manifest. The generic registry matches the exact
standard declaration/dictionary, compiler-embedded source closure and ABI;
there are no compiler SQL rules. Unsupported, custom, generic or modified
selections keep the ordinary bounded comptime path. SQL is the first artifact.

Artifacts receive compiler-owned literal parts and kind metadata, never runtime
values, factories or builders. They return the existing bounded comptime value
transport, decoded and checked against the current typed recipe before mapping
issues to holes. Execute every call afresh; this slice introduces no cross-build
result cache and does not claim that execution receipts are certified.

Use in-process artifacts to remove both Go build and process startup costs.
Generated function entries, call edges and loops receive budget/deadline guards; function
entries also enforce a recursion limit. The wrapper catches panics and serializes
the generated encoder's budget state. Input and output budgets remain bounded. Native allocation, step or depth budget
exhaustion retries the ordinary evaluator with the remaining deadline, preserving
program behavior. Native attempts and retries each have their own uncertified
tracker token. Raw Go admission rejects unknown calls and allocation forms;
formatting accepts scalar arguments, and string/buffer growth is charged before
executing bounded standard operations.
Unsupported target/effective policy falls back before executing an artifact.
Only reviewed standard implementations whose complete dependency closure is
compiled into the tool may register; arbitrary unsafe helpers remain outside
this path. Registration and guards are generated, not library-name branches.

This is a compiler-owned standard intrinsic contract, pinned by generated code
and embedded sources, rather than a freshness claim about mutable Go build
inputs. User-library result reuse still waits for comptime's certified receipts.
Measure unchanged and changed calls on the same 50-site fixture, and test binding
mutation, fallback, panic/timeout/budget handling, concurrency, diagnostics and
staleness. The target is negligible unchanged-site overhead and changed-site
checks well below 0.1s.

## Measurement

The same 50 distinct SQL sites used for #250, checked with `BORK_CACHE=off` and
`GOPACKAGESDRIVER=off`, retaining the ordinary Go build cache:

| Check | Main at #250 | Generated intrinsic |
| --- | ---: | ---: |
| First check | 0.456 s | 0.069 s |
| Median of three repeated checks | 0.434 s | 0.069 s |
| One changed literal | — | 0.067 s |

These are fresh CLI processes: validation runs on every check, with no evaluated
result reuse. Native attempts use the shared tracker but remain uncertified.
The registry namespace identifies the actual compiler image, not the live Go SDK.

## Regeneration scope

The SQL artifact binds three of the seventeen prelude files: `classes.bork`
(the emitted `Ord` dictionary ABI), `interpolation.bork`, and `options.bork`.
Edits to the other fourteen prelude files do not regenerate it. It binds
`bork/sql/interpolation.bork` and `bork/sql/sql.bork`; unused SQL sequence and
unsafe APIs are outside its closure. Binding remains conservative at file
granularity, so any edit inside a bound file still requires regeneration.

The staleness failure lists changed bound source paths (or runtime support /
generator output) and prints the exact fix: `go generate ./internal/stdvalidators`.
Only required classes, the selected instances, and reachable validator functions
are emitted into an artifact; unrelated class signatures and instances cannot
pull their prelude sources into its bound closure.
