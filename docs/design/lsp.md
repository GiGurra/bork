# Language server and VS Code integration

> **Status:** Implemented: language server and editor integration. Current docs: [editors](../editors.md) and [VS Code settings](../../editors/vscode/README.md). Measurements below record the initial implementation; the capability and rename descriptions reflect current behavior.

`bork lsp` serves Language Server Protocol 3.17 over stdio. Only framed JSON-RPC
messages go to stdout. The protocol implementation uses the Go standard library;
compiler and formatting behavior come from existing in-process APIs.

Documents belong to their containing package directory. Each package has a
Session. Every open file supplies an immutable overlay, shared across package
checks so edits to imports and new sibling files participate in loading. Closing
a document removes its overlay. No request writes source files. Manifests, Go
metadata and assets continue to use the real filesystem.

Session's editor API retains a separate checked graph for queries. Check and
Emit retain their existing independent-result guarantees. An analysis is reused
only with equal overlays, current source/asset inputs, unchanged Go context and
validated Go name inputs. Go type metadata and compile-time evaluator use bypass
reuse. A failed check publishes current diagnostics but keeps the last successful graph
for navigation. Hover and completion mark stale results; rename and fix actions
never use stale semantic state. Positions may move while a buffer is broken.

Positions use UTF-16 in the protocol and byte columns inside the compiler.
Full-document synchronization is supported. Changes are coalesced for 150 ms;
open/save and semantic requests flush pending checks. The initial target is
under 250 ms for warm small-package checking after that delay. Go metadata and
compile-time execution may take longer. This is a target, not a latency promise.

Coverage: diagnostics, hover (types and facts), definitions, type definitions,
implementations, call hierarchy, document highlights, formatting, document
symbols, completion, signature help, semantic tokens, inlay hints, references,
rename, run/test code lenses and compiler suggested-edit code actions. References and rename use compiler definition positions rather
than matching identifier spelling. Workspace navigation and rename load checked packages below configured
workspace roots, including importers; unreadable or invalid packages cause rename
to fail rather than return a partial edit set. Embedded prelude/standard library definitions are virtual sources and
must not produce unusable disk navigation or rename edits. Completion is
conservative rather than inferred for arbitrary broken expressions.

The VS Code client locates `bork` on PATH or uses `bork.serverPath`, launches
`bork lsp`, and watches module/source changes. npm scripts test the grammar and
package a VSIX using vsce. No Marketplace publication is part of this work.

Protocol reference: https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/

## Initial measurements and limits

On the shared Linux Ryzen 7 5700G, five alternating newline edits with
`BenchmarkEditorDiagnostics` averaged 90 ms for `examples/hello` and 292 ms for
`examples/http_server`, excluding debounce. The small example meets the target;
the HTTP example does not. The benchmark includes the first cold analysis.
Successful unchanged analyses reuse their graph; source edits still recheck the
complete loaded program. Package-level incremental checking is future work.

Rename supports checked variables, parameters, type parameters, functions,
predicates, methods, types, fields, variants and package values, including exported
names referenced by workspace importers. Dependency and standard-library sources
are read-only. Potential references in unsafe Go bodies are rejected when they
cannot be verified. Proposed edits must pass isolated workspace checks, including
export visibility and name-capture checks. References include interpolation holes
and distinguish same-spelled locals.

The VSIX packaging smoke test verifies runtime client dependencies are included;
client tests exercise startup/cleanup with a mocked extension host. An actual
interactive VS Code editing session has not been exercised in this environment.

The phase profile exposed repeated Go-context discovery. Analyze now passes its
previous context through Session's existing content-validated capture path;
changes to Go configuration/toolchain inputs still invalidate it. With one
priming check and five subsequent edits, configuration averages ~10 ms rather
than ~40 ms. Final warm phase measurements: hello ~64 ms (check 52 ms,
configuration 10 ms, parse 1.3 ms); http_server ~299 ms (facts/prover 167 ms,
check 117 ms, configuration 10 ms, parse 3.5 ms, lifetimes 1.2 ms). Timing varies
on the shared host. No comptime or Go type-loading phase was observed for these
examples. Incr has the phase benchmark for package-level incremental work.

Rename scans sibling files for collisions, resolves declaration positions past
receiver signatures, validates names with lexer diagnostics and indexes typed
signature requirements. Proposed edits must also check in an isolated overlay
Session. Incomplete workspace inventories and unverifiable unsafe Go references
are rejected rather than offered as partial edits. No proposed edits are written to disk.

Shebang scripts select their own file as the analysis root; ordinary documents
select the containing directory. This keeps script roots independent of adjacent
package files. Shebang scripts are parsed into an implicit main body, so top-level statement
positions participate in ordinary typed queries. Synthetic main is omitted from
symbols, completion and declaration identities. Adjacent open scripts have
independent Sessions, and rename validation uses the same selected-file root.
Scripts without shebangs remain ordinary package documents in the editor.
