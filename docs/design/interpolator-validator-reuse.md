# Standard interpolator validator artifacts (bork-gc48vu)

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
Generated function entries and loops receive budget/deadline guards; function
entries also enforce a recursion limit. The wrapper catches panics and serializes
the generated encoder's budget state. Input and output budgets remain bounded.
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
