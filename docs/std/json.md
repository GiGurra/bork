# bork/json

## Dynamic JSON and JSON Lines

- **Dynamic JSON and JSON Lines (implemented):** `bork/json` queries the prelude's Json without moving its typed Decode/Encode API: Field selects the first matching object field, Index selects an array element, and At follows a list of string keys and integer indices. None denotes a missing path; Some(Null) preserves a present JSON null, and an empty path returns the input. Pretty preserves field order and exact number text, with 0–8 spaces (default 2); invalid manually constructed number text returns JsonError. OpenLines/CreateLines own files in a scope. Next reads one line at a time without a scanner token limit, accepts LF/CRLF and a final unterminated line, and returns None at EOF. Blank/malformed lines give LineError with path, one-based line number and message; a later Next continues at the next line. Read/write failures give IoError. Write accepts any Encode value, validates the rendered JSON, and immediately writes one compact document plus LF without a deferred flush. Concurrent calls on one reader are serialized; concurrent writes on one writer keep each line together, with unspecified order. A failed write may leave a partial final line. Resources can be attached to another scope under the standard ownership rules; there is no whole-file buffering. See [json_lines](../../examples/json_lines/main.bork).

## JSON API

`bork/json` adds `Field(value, name)`, `Index(value, index)`, and `At(value, path)` on dynamic Json values. Paths are lists of string keys and integer indices; missing paths give None and JSON null gives Some(Null). `Pretty(value, spaces = 2)` preserves object order and number text; spaces must be 0 through 8. `OpenLines(path, s)` and `CreateLines(path, s)` return scope-owned Reader/Writer resources. `Next(reader)` returns Option[Json], LineError (with a one-based line number), or IoError. `Write[T: Encode](writer, value)` writes one compact value and LF and returns Ok, JsonError, or IoError. Both operations use io.

## Examples

`bork/json` queries dynamic JSON values, pretty-prints them, and streams JSON Lines through scope-owned readers and writers. See [json_lines](../../examples/json_lines/main.bork).
