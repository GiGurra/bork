# Browser playground

> **Status:** Implemented: browser-local check, describe and formatting. Current docs: [browser playground](../playground.md). Hosted execution remains outside this feature.

Start with a browser-local playground on the documentation site: diagnostics, type information at the cursor, and formatting for one `.bork` file. Hosted execution is outside this phase. This gives readers a way to explore ordinary functions, immutable data, matching, effects, and guarded facts without installing Go or bork.

## Options and cost

| Approach | What works | Hosting and cost | Main constraint |
| --- | --- | --- | --- |
| Native compiler and runner behind an API | Full checking and execution, including compile-time Go | A separate service with Go installed; compute, bandwidth, maintenance, and abuse controls | Must isolate compilation as well as execution |
| Browser Wasm checker plus a run API | Immediate local diagnostics; server handles unsupported checks and running | Static frontend plus the same isolated backend | Two deployments; results must identify the compiler version |
| Browser Wasm checker only | Core checking, types, and formatting; no execution | Existing GitHub Pages deployment; no per-check server compute | Go-dependent checks need local bork |

Choose the third option now. GitHub Pages serves static content and cannot host the native Go toolchain or a run API. Its published-site limit is 1 GB and its bandwidth soft limit is 100 GB/month; this small educational frontend fits the site-size limit, while downloads should be measured after deployment. See [Pages limits](https://docs.github.com/en/pages/getting-started-with-github-pages/github-pages-limits). A future execution service requires a separate hosting decision and traffic measurements before estimating a monthly compute bill.

## Feasibility and download size

A local probe at commit `c34bd31`, built with Go 1.27.1, successfully ran `syntax.Parse`, the embedded prelude, `check.Program`, effect checking, lifetime checking, and facts in `GOOS=js GOARCH=wasm`. A valid hello-world and a guarded predicate passed; an undefined name produced the compiler's diagnostic. The full CLI does not currently build for Wasm (`cmd/bork/watch.go` references `syscall.SIGHUP`), so use a dedicated browser entrypoint rather than porting its filesystem/process-oriented driver.

| Probe artifact | Bytes | MiB |
| --- | ---: | ---: |
| Wasm | 13,256,929 | 12.64 |
| gzip, level 9 | 3,496,820 | 3.33 |
| Brotli, quality 11 | 2,503,663 | 2.39 |

These measure the checker probe, not the final UI bundle; adding describe/format changes the total. Measure the shipped bundle in CI. Build `wasm_exec.js` from the same Go SDK as the Wasm module, as required by the [Go Wasm guide](https://go.dev/wiki/WebAssembly). Pages controls transport compression; do not assume that uploading `.br` files configures content negotiation. Fetch an explicitly compressed asset and decompress in the browser where supported, with a raw-Wasm fallback. Load lazily and report progress; subsequent visits can use the browser's HTTP cache.

## Honest diagnostics

Use the existing checker and formatter without executing user code. Initially accept a single file with the prelude; imports, user `unsafe go`, Go type bindings, `comptime`, and custom compile-time interpolation validators require local bork. Structural facts and guards remain useful locally.

Facts sometimes defer predicates to the native Go evaluator. **Never pass a nil evaluator:** `check.Facts` then returns without resolving pending queries. Supply an evaluator that explicitly reports **“needs local bork: compile-time predicate execution is unavailable in the browser”**. Unsupported operations must prevent a “checked successfully” result. Preserve diagnostic codes and source positions, distinguish unavailable checks from language errors, and offer the source for download with local `bork check` instructions. Types are available only from a successful current check; formatting can still work on type-invalid code.

## Security and implementation

Run the Wasm adapter in a Web Worker, with a small input limit and a deadline enforced by terminating and recreating the worker. A worker prevents compiler loops from freezing the UI; it does not provide a hard memory quota. Do not instantiate generated programs, expose an execution endpoint, load remote imports, or upload source. Render all user source and diagnostic text as text, never HTML. Avoid share URLs containing private source in this phase.

The first delivery includes an editor, small examples, Check, Format, type inspection at the cursor, diagnostic links, and source download. CI builds the adapter and tests native/browser diagnostic parity, unsupported proofs, Unicode cursor positions, formatting, and worker recovery. The existing Pages workflow publishes it alongside the docs. No account or secret is needed.

If hosted execution is reconsidered, isolate both compiler and runner in disposable environments with no outbound network, read-only toolchains, bounded input/output/time/memory, admission limits, and no credentials. The [Go playground sandbox](https://github.com/golang/playground/blob/master/sandbox/sandbox.go) is a reference for separate execution isolation and resource limits. Do not run submitted bork in the Pages build or on an ordinary application server.
