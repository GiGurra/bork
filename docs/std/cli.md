# bork/cli

## Command-line options

`bork/cli` wraps boa through a reflection shadow struct built from a derived
Decode schema. `Parse[T]` returns proven options, collected field errors, or help;
`Run[T]` invokes a `(T, Scope) => Unit` handler only after successful validation.
Docs/defaults drive help, `cli.Flag` maps short/env/positional metadata, and List
fields take repeated flags. Unknown metadata names include a closest-field hint;
duplicate short/env/positional mappings are errors before parsing. Environment
loading stays independent. Config-file and subcommand APIs are deferred. See
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

`cli.Flag { field, short, env, positional }` maps boa metadata to a bork field.
The adapter validates these names and rejects duplicate field metadata, short
flags, environment names, or positional fields. Short flags must be single ASCII characters; `-h` and `--help` are reserved. Field names that produce the same kebab-case flag are rejected. Unknown fields include a closest
name hint. There is at most one positional field; a List can collect its values.
Flag names use kebab case (`httpPort` becomes `--http-port`). Strings are literal,
booleans support `--verbose` and `--verbose=false`, and numbers use JSON syntax.
Lists take repeated flags (`--tag a --tag b`); string elements are literal and
other elements use JSON. Nested lists use a JSON array for each occurrence.
Explicit `env` mappings use boa's environment parsing, and CLI values override
them. Boa treats an empty environment value as absent. Environment loading through `bork/env` remains independent.

`Parse[T: Decode](name, description, args, flags = [])` returns `T | cli.Error |
cli.Help`. Arguments exclude the executable name. It captures help text rather
than printing it. `Run[T: Decode](name, description, handler, flags = [])` reads
process arguments, prints help, and invokes `(T, Scope) => Unit` in a fresh scope
on success. It returns `Unit | cli.Error`; applications choose how to render
errors and exit. Both use `io` for environment access, and Run carries its
handler's effects. Config-file and subcommand APIs remain follow-up work.

## Examples

`bork/cli` wraps boa to parse proven options from a record deriving `Decode`.
It generates help from field docs and defaults, supports short flags, explicit
environment bindings, positionals, and repeated list flags, and collects field
errors before invoking a handler. See [examples/cli](../../examples/cli/main.bork).
