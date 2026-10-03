# bork/cli

## Command-line options

`bork/cli` wraps boa through a reflection shadow struct built from a derived
Decode schema. `Parse[T]` returns proven options, collected field errors, or help;
`Run[T]` invokes a `(T, Scope) => Unit` handler only after successful validation.
Docs/defaults drive help, `cli.Flag` maps short/env/positional metadata, and List
fields take repeated flags. Unknown metadata names include a closest-field hint;
duplicate short/env/positional mappings are errors before parsing. Environment
loading stays independent. JSON configuration files use explicit precedence;
subcommand APIs are deferred. See
[the schema adapter](cli.md#command-line-schema-adapter) and
[the example](../../examples/cli/main.bork).

## CLI API

`bork/cli` parses a derived `Decode` record through boa. `Parse[T]` accepts
explicit arguments and returns options, collected errors, or help text; `Run[T]`
uses process arguments and calls a handler in a scope. Field docs/defaults appear
in help; `cli.Flag` supplies short/env/positional metadata. Lists use repeated
flags. See [the schema adapter](cli.md#command-line-schema-adapter).

## Command-line schema adapter

`bork/cli` wraps boa with a `reflect.StructOf` shadow struct built from the
derived Decode field schema. It requires `Decode`, without a GoStruct bound.
Docs and defaults appear in generated help. Optional shadow pointers track
which fields were omitted, so defaults and Option values follow the same rules
as regular decoding. After parsing, the adapter validates each supplied field,
collects missing, JSON conversion, and fact errors across fields, then decodes the complete record. Boa flag syntax errors (including invalid booleans) stop parsing and return one error before field validation.
Only that proven record can reach the handler.

`cli.Flag { field, short, env, positional, configFile }` maps boa metadata to a bork field.
The adapter validates these names and rejects duplicate field metadata, short
flags, environment names, or positional fields. Short flags must be single ASCII characters; `-h` and `--help` are reserved. Field names that produce the same kebab-case flag are rejected. Unknown fields include a closest
name hint. There is at most one positional field; a List can collect its values.
Flag names use kebab case (`httpPort` becomes `--http-port`). Strings are literal,
booleans support `--verbose` and `--verbose=false`, and numbers use JSON syntax.
Lists take repeated flags (`--tag a --tag b`); string elements are literal and
other elements use JSON. Nested lists use a JSON array for each occurrence.
Explicit `env` mappings use boa's environment parsing, and CLI values override
them. Boa treats an empty environment value as absent. Environment loading through `bork/env` remains independent.

`Parse[T: Decode](name, description, args, flags = [], configFiles = [])` returns `T | cli.Error |
cli.Help`. Arguments exclude the executable name. It captures help text rather
than printing it. `Run[T: Decode](name, description, handler, flags = [], configFiles = [])` reads
process arguments, prints help, and invokes `(T, Scope) => Unit` in a fresh scope
on success. It returns `Unit | cli.Error`; applications choose how to render
errors and exit. Both use `io` for environment access, and Run carries its
handler's effects. Subcommands remain follow-up work.

## Configuration files

`configFiles` is a list of JSON files using the options record's exact bork field
names (`httpPort`, not `http-port`). Files overlay from left to right; a later
field replaces an earlier field completely, including lists and nested records.
Missing fields keep earlier values. Empty file paths are skipped. JSON must be
an object; unknown top-level keys, malformed JSON, and unreadable files return
`cli.Error`. Nested records follow their derived decoder's usual rules, which
ignore unknown nested keys.

Mark one String or Option[String] field with `cli.Flag { field: "config",
configFile: true }` to expose `--config <path>` (with ordinary short/env metadata
if desired). Its path comes from flags, its mapped environment variable, or its
declared default. A nonempty selected path overlays `configFiles`; values inside
files do not select additional files. Two selector fields or a selector with
another field type are metadata errors. Optional selector fields must declare
`Option[String]` concretely in the record schema. An empty selector path loads no extra
file.

Precedence, highest first: explicit CLI flags/positionals, mapped environment
values, the selected config file, later `configFiles`, earlier `configFiles`,
then declared defaults. An empty environment value is absent, as in ordinary
parsing. False, zero, empty lists, and explicit null retain their meaning; null
is valid only when the decoder accepts it. The adapter validates the final
merged values, collecting field errors; a lower-precedence invalid value can be
overridden by a valid flag. Only the resulting immutable proven record reaches
the handler. Help does not read files or invoke the handler.

```bork
import "bork/cli"

type Options = {
  config: Option[String]
  port: Int = 8080
} derive (Decode)

fn main() {
  result = cli.Run[Options]("app", "Example", (options, s) => {
    println(options.port)
  }, flags: [cli.Flag { field: "config", configFile: true }], configFiles: ["settings.json"])
  println(result)
}
```

Run with `app --config local.json --port 9000`. `settings.json` might contain
`{"port": 8081}`; `local.json` might contain `{"port": 8082}`. The handler sees
9000.

## Examples

`bork/cli` wraps boa to parse proven options from a record deriving `Decode`.
It generates help from field docs and defaults, supports short flags, explicit
environment bindings, positionals, and repeated list flags, and collects field
errors before invoking a handler. See [examples/cli](../../examples/cli/main.bork).

The checked-in example also supports a selectable config file:

```sh
bork run examples/cli -- --config examples/cli/config.json --port 9000
```
