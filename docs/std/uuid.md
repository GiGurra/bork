# bork/uuid

## UUID values

`bork/uuid` exposes immutable, canonical lowercase UUIDs, random v4 and
chronologically ordered v7 generation, parsing, formatting, nil and version
inspection. Parse normalizes backend-supported text forms; a field fact prevents
constructing an invalid or noncanonical Uuid. Uuids compare structurally and
work as map keys. The Codecs instance bundle encodes/decodes JSON strings and
validates nested values, including environment configuration. V4 declares
random, V7 random + clock; entropy failures return IoError and parsing returns
ParseError. google/uuid v1.6.0 is pinned behind this API for Go 1.26/offline
compatibility; switch to the Go standard UUID backend when the minimum is 1.27.

## UUID API

`bork/uuid` provides `Uuid`, a canonical lowercase UUID value guarded by the
`Canonical` fact. `Parse(text)` accepts dashed, compact, braced, and urn:uuid
forms supported by the backend and normalizes them, returning Uuid or ParseError.
`V4()` generates a random UUID (`uses random`); `V7()` generates a time-ordered
UUID (`uses random + clock`); entropy errors are IoError. `Nil()`, `Version(id)`,
and `Format(id)` are pure. Printing and `toString(id)` use canonical text, and
Uuid works as a map key. `use uuid.Codecs` enables Encode/Decode as JSON strings,
including nested records and environment config. `env.Load` UUID cells therefore
use JSON string syntax; `env.LoadJson` works with ordinary JSON UUID strings.
The backend is pinned google/uuid v1.6.0 to retain Go 1.26 support; it does not
leak into bork values. See [the UUID example](../../examples/uuid/main.bork).

## Examples

Import `bork/uuid` for canonical UUIDs, v4/v7 generation, map keys, and JSON
string codecs (`use uuid.Codecs`). See [examples/uuid](../../examples/uuid/main.bork).
