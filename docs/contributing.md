# Contributing and design notes

These documents are for people working on the compiler and the standard library. They record what was decided and why, in much more detail than the reader pages. You do not need them to use the language.

## References

- [Grammar](grammar.md): the syntax the compiler accepts, in EBNF, with the semantic rules in brief.
- [Requirements](requirements.md): the full specification of every feature, including the decisions behind it.
- [Roadmap](roadmap.md): the milestones and the compiler pipeline.
- [Go helpers for standard packages](std-go.md): the Go API that `unsafe go` code in the standard library uses, and how Go dependencies are pinned.
- [The prelude sources](../internal/prelude/README.md): the bork code that is available in every program.

## Working on the compiler

- [CI test shards](ci.md): how the test suite is split and cached in CI.
- [Compiler performance](design/performance.md): the benchmark harness and profiling reports.
- [Driver test scheduling](design/driver-test-scheduling.md): an audit of the tests that run serially.

Golden tests live in `testdata/cases/<name>/`, and every example's expected output in `testdata/examples/`. The bork code blocks in the README and the reader pages are compiled by `TestDocSnippets` in `internal/driver`. A block opened with <code>```bork</code> must compile and be formatted, <code>```bork fails</code> must not compile, and <code>```bork fragment</code> is not checked.

## Design notes

| Note | Subject |
| --- | --- |
| [async](design/async.md) | Transparent async bindings |
| [lazy](design/lazy.md) | Lazy bindings and computed record fields |
| [comptime](design/comptime.md) | Compile-time evaluation |
| [interpolators](design/interpolators.md) | Typed string interpolation |
| [interpolator validation](design/interpolator-validation.md) | Compile-time validators for interpolators |
| [interpolator validator reuse](design/interpolator-validator-reuse.md) | Shipping standard validators with the compiler |
| [backpressure](design/backpressure.md) | Bounded task pools, HTTP admission, and retry budgets |
| [HTTP propagation](design/http-propagation.md) | Forwarding deadlines and ambient values between services |
| [incremental compilation](design/incremental.md) | Package interfaces, cache invalidation, and watch sessions |
| [disk cache](design/disk-cache.md) | The compile cache on disk |
| [evaluation cache](design/evaluation-cache.md) | Caching compile-time evaluation |
| [execution API](design/execution-api.md) | How the compiler runs Go tools |
| [Go execution inventory](design/go-execution-inventory.md) | Where the compiler invokes Go |
