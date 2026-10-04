# Execution observation and installed SDK identity

Status: request-owned accounting is integrated for comptime and predicate calls,
including invocation-local and Session predicate memo hits. Every attempt still
declines an execution receipt. Narrow [Session proof batches](proof-evaluator.md)
reuse owned booleans under a validated native Go installation contract; persisted
and enclosing evaluated-value reuse remain deferred. The complete-program
compile-time evaluator bypass is unchanged.

The driver uses the shared `captureInstalledSDK` / `installedSDKIdentity.current`
implementation. It records owned root/version, resolved launcher and launcher /
VERSION device, inode, size, mode and modification-time evidence under the shared
immutable installed-SDK policy. No package discovery or SDK content/membership
walk runs during observation. The former Go content collector has been removed.
In-place GOROOT edits are unsupported, same as go build; run go clean -cache.

Counters record logical calls, installed identity capture/validation and local
memo hits. Separate bounded hashes observe actual build and evaluator argv,
environment and working directory; mode and effective deadline are recorded too.
A memo hit clears command witnesses rather than inheriting a prior build. The
commands, module staging, subprocess execution, result decoding and current Facts
retain their ordinary behavior. Installed identity is configuration evidence,
not proof of the SDK actually selected by an automatic toolchain launcher or of
all compiler-consumed inputs. No AST or execution result is retained by these
observations. Deferred result-cache misses skip execution observation and the former comptime
selection audit; the existing predicate memo still requires its static eligibility
audit. Empty phases allocate no tracker.

## Measurement and deferred certification

On Go 1.27.1, one fresh check of each of 39 examples produced 23 predicate and
7 comptime executions. The local proof memo eliminates four repeated predicate
calls. The conservative static/generated-support boundary admitted 6 of the
remaining 19 predicate calls and none of the 7 comptime calls: 6/26 executions,
with about 2.09 seconds of gross potential savings across all examples. The
content collector cost about 0.91 seconds to capture and 0.51 seconds to validate
one SDK inventory; validating six hits alone exceeded the possible savings.
These measurements justify deferring cross-request certification and value reuse.
They are an eligibility/cost probe, not a claim of cache hits or a performance
prediction for other machines or programs.

Persisted and enclosing hits described in [evaluation-cache.md](evaluation-cache.md) and
[execution-api.md](execution-api.md) requires all of the following before hits:

- Bind actual ordered query/comptime selections, canonical closed arguments,
  resolved type/dictionary/provider/default dependencies and ordered prior
  site/type/value identities to the current checked source/module graph.
- Audit the complete emitted support/import-initialization closure and pin resolved
  intrinsic contracts to actual compiler bytes/schema/embedded standard library.
  Unknown unsafe, foreign and build-effect inputs must decline.
- Bind frozen generated/module/assets, actual selected toolchain and effective
  build and evaluator process argv/environment, native target, execution mode,
  output/value codec and effective resource policies. Shared installed-SDK identity
  follows the supported immutable-installation policy; observations alone do not
  satisfy this execution boundary.
- Perform dependency/cycle preflight before lookup, bounded owned canonical
  decoding/reconstruction after lookup, and current contextual/result/field Facts
  before certification. Malformed persisted data is a miss, not a compiler error.
- Certify every logical invocation, including local memo hits and native validator
  attempts/fallbacks, separately from final enclosing Facts. Only then may the
  evaluator bypass change. Persistence must use the shared typed receipt/store
  layer and its ownership, budgets and lifecycle.

The identity/tracker primitives and static audit remain fail-closed foundations;
`prepareExecution`, candidate/receipt current checks and certification do not
qualify any execution. Further work should first show enough eligible real calls
and net savings to justify this boundary, rather than restoring an unused SDK
content collector.
