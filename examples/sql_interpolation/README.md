# SQL interpolation

Run `bork run examples/sql_interpolation`.

The SQLite demo binds a hostile String as a value, quotes table/column names
with `sql.Name`, and composes a WHERE fragment into a larger Statement. It prints
the rendered query and separate parameter list, then verifies that the input
matches exactly one row and that the table still contains both inserted rows.
Finally, it streams the matching name while the connection's scope is open.

A commented-out quoted-hole example explains the rendering error: write
`name = $value` with no SQL quotes around the hole. Rendering rejects holes
inside quoted text before executing any query. This check currently happens at
rendering; library-defined compile-time validators are tracked in bork-hstt36.

For SQL assembled as runtime text, the raw execution/stream APIs require the
explicit `sql.Unsafe(text)` opt-in. This example uses typed literals throughout.
See the [SQL API](../../docs/std/sql.md) and
[interpolation design](../../docs/design/interpolators.md).
