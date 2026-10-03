# JSON diagnostics

`bork check --json [path]` writes JSON Lines to stdout: one object per diagnostic, ordered by file, line, and column. `bork build --json [path]` and `bork test --json [path]` write diagnostic JSON Lines to stderr; test reports stay on stdout. Text output without `--json` is unchanged. Successful compilation writes no diagnostics, and compilation errors exit with status 1. A failing test retains its usual report and exit status; it is not a compiler diagnostic.

```json
{"schema_version":1,"file":"main.bork","line":1,"column":26,"end_line":1,"end_column":27,"message":"unexpected character '&' (did you mean '&&'?)","code":"syntax.single-ampersand","fixes":[{"message":"replace & with &&","edits":[{"start":{"file":"main.bork","line":1,"column":26},"end":{"file":"main.bork","line":1,"column":27},"replacement":"&&"}]}]}
```

Every object has `schema_version` (currently `1`), `file`, `line`, `column`, `end_line`, `end_column`, `message`, and `code`. File paths are the paths used by the compiler, including paths of imported sources. Lines and columns are one-based; columns count UTF-8 bytes, including one byte for a tab. End positions are exclusive. A diagnostic whose range is unknown has an end equal to its start. Tool failures without a source location use an empty file and zero positions.

Codes are stable identifiers independent of message wording. The current general categories are `syntax.error`, `type.error`, `facts.error`, `lifetime.error`, `import.error`, `go.error` (in an unsafe Go block), `bind.error` (a binding to a Go function that does not match it), `compiler.error` (fallback), and `tool.error` (I/O, toolchain, or other operational failure). These categories group multiple causes; consumers should not treat them as identifying a unique message. Specific codes are `syntax.single-ampersand`, `type.empty-list`, `type.empty-map`, `type.lambda-parameter`, `package.no-main`, `package.no-tests`, `mock.error` (a `mock` that is not allowed there, or names something that cannot be mocked), `mock.parameters` (a mock naming the wrong number of parameters; its fix names the target's), and `effect.mock` (a mock's body using an effect its target does not declare). Additional specific codes may be added as diagnostics gain structured fixes; consumers should accept unknown codes and fields.

`fixes` is omitted when no edit is available. Each fix has a `message` and an `edits` array. All edits in one fix belong together. Each edit replaces the half-open range `[start, end)` with `replacement`; equal positions insert text. Start and end each contain `file`, `line`, and `column`. Apply edits against the original source, from later positions to earlier positions within each file.

A fix with `requires_input: true` is a template: replace the named type placeholders before applying it. Empty collection bindings suggest `: List[Element]` or `: Map[Key, Value]`; unknown lambda parameters suggest `: Type`, adding parentheses for a bare parameter. These placeholders represent types the compiler cannot infer, so they are not automatically safe defaults. Collection fixes are offered for directly bound empty literals; other contexts still receive diagnostics without fixes. Re-check after editing, since diagnostics can cascade and a fix may reveal later errors.

Named-call diagnostics use `call.unknown_argument`, `call.duplicate_argument`, `call.missing_argument`, `call.positional_after_named`, `call.named_receiver`, `call.too_many_arguments`, and `call.named_argument_unavailable`. A unique close parameter name gets a label replacement; duplicate arguments get a removal edit when it does not discard comments. Missing values are never invented, and reordering is not suggested as an automatic edit because it may change effects. Old `copy(field = value)` syntax gets `syntax.copy_separator` with an edit replacing `=` with `:`.

Dependency assembly uses `assemble.missing`, `assemble.duplicate`,
`assemble.cycle`, `assemble.unused`, `assemble.provider`, `assemble.failure`
and `assemble.target`. `assemble.bundle` diagnoses invalid bundle declarations,
misuse outside assembly, and invalid named replacements. Each graph diagnostic's message includes the full rooted
dependency tree and supplied provider list, with missing/ambiguous slots, shared
nodes and cycle paths marked. Facts, effects and lifetime violations in resolved
provider calls use their ordinary diagnostic codes.
