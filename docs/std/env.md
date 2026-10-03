# bork/env

## Configuration

`bork/env` distinguishes missing and empty variables, snapshots the environment, and loads derived records with `Load[T: Decode](prefix)`: `httpPort` maps to `PREFIX_HTTP_PORT`, strings are literal, other values use JSON syntax, absent Option fields are None. All invalid/missing variables are collected, including field fact violations; `LoadWith` injects a reader for tests. Custom decoders can use `LoadJson` for a JSON configuration variable. Derived record schemas expose general field metadata to std Go code ([helper API](../std-go.md)).

## API

`bork/env.Load[T: Decode](prefix)` and `LoadWith` decode environment configuration from derived record fields; `LoadJson` decodes one JSON variable. These packages introduce no new syntax. See [examples/time_env](../../examples/time_env/main.bork) and the [Go schema helpers](../std-go.md).

## Examples

`bork/env` loads one environment variable per record field through `Decode`, checking facts and reporting all invalid or missing variables. See [examples/time_env](../../examples/time_env/main.bork).
