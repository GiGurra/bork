# Watch compiler diagnostics

`bork check --watch [path]` checks immediately and stays running. It polls tracked
inputs once per second, checks again when they change, and prints the complete
result of each check. Compiler errors do not stop the watcher: fix the source or
create a missing file to get another result. Stop with Ctrl-C or SIGTERM.

The watcher tracks source files, imports and directory membership, Go manifests,
observed embedded assets, and captured Go configuration/name inputs. It compares
contents, so restoring file size and modification time does not hide an edit.
It retains one Session and reuses a successful result only when that Session can
validate all required inputs. Programs using Go type metadata or compile-time
evaluation still compile afresh after a tracked change; idle polling does not
repeat those expensive compilations.

Inputs the compiler cannot inventory need a manual check: examples include files
read through unsafe Go, external driver dependencies, and changes outside the
captured Go metadata inventory. On Unix, send SIGHUP to the watch process to
force a fresh compilation. On Windows, restart the command. Manual requests do
not reuse the previous semantic result. If inputs change during checking, the
watcher withholds that result and retries. Interrupting checking prevents result
publication and stops the loop after the ongoing synchronous check returns.

## JSON stream

`bork check --watch --json [path]` writes one complete result object per line to
stdout. This streaming format is separate from one-shot diagnostic JSON Lines.
Each object has `schema_version: 1`, an increasing `request_id`, `status` (`ok` or
`error`), and a `diagnostics` array in source order. An empty array is explicit;
warnings include the same codes, positions and edits as one-shot diagnostics.
Request IDs can skip when an obsolete check is withheld. Validated unchanged
inputs produce no new line. Each published object replaces the previous result.

```json
{"schema_version":1,"request_id":1,"status":"ok","diagnostics":[]}
```

One-shot commands retain their existing output and exit status. Watch mode
continues after compiler errors and exits normally when interrupted; output or
setup failures stop it with an error.
