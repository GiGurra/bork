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

## Current limits

Source locations come from the compiler's checked tree. Normal builds retain their existing optimization and caching behavior. Compile-time code completes during the build and cannot be stepped through in the runtime debugger.

Locals and watch expressions show bork type names and record fields. Sealed variants appear as `Shape.Circle { radius: 2.0 }`; options appear as `Some(3)` or `None`. Nested values use the same presentation, and expanding a value loads its children lazily. Compiler-generated temporaries are hidden. Generated helper frames are marked subtle, so editors can de-emphasize them while preserving stack positions. These views use the compiler’s debug map, without running the program’s Show methods. Delve’s previews can be truncated; expand a value to inspect its fields. Without a compatible debug map, inspection falls back to Delve’s Go presentation. A statement may require several steps, and stepping into a standard-library helper can open retained generated source. Expressions in the Debug Console use Go syntax and generated names.

Scopes, goroutines and deadlines use the real runtime behavior. Pausing in the debugger does not suspend elapsed-time deadlines. A scope can therefore time out while execution is paused. Runtime panic locations use bork source locations in debug builds.
