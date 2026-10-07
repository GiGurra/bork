# Debugging bork

In VS Code, open a bork project and choose **Run and Debug → Debug bork**. The extension saves files, builds a debug executable and starts the debugger. Set breakpoints in `.bork` files, step through statements, inspect locals and evaluate expressions in the Debug Console.

A launch configuration can select a package directory or a script:

```json
{
  "type": "bork",
  "request": "launch",
  "name": "Debug bork",
  "program": "${workspaceFolder}",
  "args": [],
  "stopOnEntry": false
}
```

`cwd` defaults to the workspace directory. The extension uses `bork.serverPath` for the compiler. The optional `bork.delvePath` setting overrides the debugger executable.

The first launch offers to install the pinned debugger if it is missing. Installation uses Go and requires access to Go's module downloads. You can install it ahead of time with `bork debug setup`; the executable goes under `BORKCACHE/tools/delve/v1.27.2`. An existing `dlv` executable on `PATH` is also accepted.

## Command line and other editors

Run `bork debug build . -o ./program` to compile with optimization and inlining disabled. Generated Go source, its module and the compiler’s `debug-map.json` are retained in `./program.bork-debug/` for debugger source views. Keep that directory alongside the executable for the life of your session.

`bork debug dap --listen 127.0.0.1:0` starts a DAP relay in front of the optional debugger and prints its selected TCP address. A DAP client connects there and sends a `launch` request with `mode: "exec"` and the absolute executable path. The listener accepts only loopback IP addresses. `--delve /path/to/dlv` selects an executable explicitly. The CLI owns the adapter process; interrupting it stops the adapter.

## Debug Console expressions

While paused, the Debug Console, watches and hover evaluation accept a read-only
subset of bork expressions in the selected stack frame:

- Available local variables and parameters, using their bork names (including
  names such as `range` and `chan` that Go reserves).
- Integer, float, boolean, string and rune literals, and parentheses.
- Eager record fields, including nested fields and concrete generic records.
- Scalar arithmetic, comparison, boolean and bitwise operators, checked with
  bork's normal operand types and literal ranges.

For example, `user.address.zip`, `count + 1`, `price * 1.5`, and
`!(count > 4) && enabled` work without generated Go names. The compiler parses,
checks and lowers expressions using type information from the build's debug map
and locals from the selected frame. Evaluation does not execute user functions.

Calls and methods, aggregate literals and comparisons, interpolated strings,
blocks, `if`/`match`, pipes, assignments, `?`, deferred bindings/fields, and
option/union payload access are unsupported and produce readable errors.
Whole available option/union/container variables can still be inspected.
Collection access is also unsupported: bork uses methods such as `xs.get(i)`,
which return an `Option` and require runtime helpers. Go's `xs[i]` would change
those semantics and is not bork syntax. Ambiguous generated bindings are rejected
rather than choosing a value with the wrong source identity.

Rebuild to include expression metadata. Old debug maps without that metadata,
and sessions without a compatible map, retain Delve's Go expression behavior.
The map describes the built executable; editing source does not update a paused
session. Other DAP clients should supply `frameId` on evaluation requests.

## Current limits

Source locations come from the compiler's checked tree. Normal builds retain their existing optimization and caching behavior. Compile-time code completes during the build and cannot be stepped through in the runtime debugger.

Locals and watch expressions show bork type names and record fields. Sealed variants appear as `Shape.Circle { radius: 2.0 }`; options appear as `Some(3)` or `None`. Nested values use the same presentation, and expanding a value loads its children lazily. Compiler-generated temporaries are hidden. Generated helper frames are marked subtle, so editors can de-emphasize them while preserving stack positions. These views use the compiler’s debug map, without running the program’s Show methods. Delve’s previews can be truncated; expand a value to inspect its fields. Container values held in unions retain Delve’s `data` node so list paging continues to work. Without a compatible debug map, inspection falls back to Delve’s Go presentation. A statement may require several steps, and stepping into a standard-library helper can open retained generated source.

Scopes, goroutines and deadlines use the real runtime behavior. Pausing in the debugger does not suspend elapsed-time deadlines. A scope can therefore time out while execution is paused. Runtime panic locations use bork source locations in debug builds.
