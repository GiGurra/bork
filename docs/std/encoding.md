# bork/encoding

## CSV encoding

CSV has raw row parsing/writing and typed record parsing/writing through
`Decode`/`Encode`. Typed CSV uses the derived Decode field schema: String
fields are verbatim; numbers, booleans and compound fields use JSON. Empty
optional cells and missing optional columns become None. Nonempty optional
cells become Some, parsed exactly like the required field (no JSON quotes
for String). Encoding Some("") writes empty like None; Some("") cannot
round-trip and decodes as None. Nonempty JSON null in optional non-String cells
is rejected with a per-cell error; use an empty cell for None. The JSON bridge
cannot preserve Some(Json.Null), which encodes like None. Header names match
fields exactly; unknown,
duplicate and missing required headers are errors. Ragged typed rows are
errors. Facts and field types are checked before constructing records, with
all field errors collected in `CsvErrors` by row and column. Malformed CSV
returns `CsvError`; partial records are never returned. Encoding requires both
Encode and Decode for the schema and optional semantics. Zero-field records
are unsupported; empty input encodes to empty text. Raw CSV follows Go CSV
quoting, blank-line skipping, CRLF normalization and LF output, permitting
ragged rows. No new syntax is introduced.

## Binary encoding

`bork/encoding` supplies UTF-8, hex and padded standard/URL-safe base64 encoders and
parsers. `Utf8(text): Bytes` preserves embedded zero bytes;
`ParseUtf8(data): String | ParseError` rejects invalid UTF-8. Invalid encodings return `ParseError` without partial data. The
base64 parsers enforce zero trailing padding bits and accept CR/LF.

## Encoding API

- **UTF-8:** `Utf8(text): Bytes` encodes text; `ParseUtf8(data): String | ParseError`
  validates and decodes bytes. Copy a byte list with `values.toBytes()`;
  `data.toList()` makes the reverse conversion.
- **`bork/encoding`:** `Hex` / `ParseHex`, `Base64` / `ParseBase64`, and
  `Base64URL` / `ParseBase64URL` convert Bytes and String. Parsers return
  `Bytes | ParseError`; hex accepts either case and requires complete pairs.
  Both base64 forms use padding and reject nonzero trailing padding bits;
  parsers accept CR/LF as Go's base64 decoder does. No partial bytes are
  returned after an error. See [examples/bytes_encoding](../../examples/bytes_encoding/main.bork).

## CSV API

- **CSV:** `encoding.CsvRows(text)` parses comma-separated text into
  `List[List[String]] | encoding.CsvError`; `encoding.Csv(rows)` writes rows
  with LF endings and CSV quoting. Raw parsing permits ragged rows and
  quoted newlines, skips blank lines, and normalizes CRLF as Go's CSV parser
  does. `encoding.DecodeCsv[T: Decode](text)` reads a header row and a derived
  record schema, returning `List[T] | encoding.CsvError | encoding.CsvErrors`.
  String fields are literal (so `007` stays `007`); other fields use JSON
  syntax. Empty optional cells and missing optional columns become None;
  nonempty optional cells become Some, parsed like the required field. Thus
  an optional String cell `hello` needs no JSON quotes. Encoding None and
  Some("") both writes empty cells, which decode as None: Some("") cannot
  round-trip. Optional non-String cells containing nonempty JSON `null` are
  rejected with a cell error; None must use an empty cell. The JSON bridge
  also cannot preserve Some(Json.Null), which encodes like None. These are
  known limitations of the CSV representation.
  Headers match field names exactly; duplicate, unknown, and missing required
  columns are errors. Field types and facts are checked, collecting every
  row/field error without returning partial records. `EncodeCsv[T: Encode + Decode]`
  writes headers in schema order; both classes are required to obtain the
  schema and preserve optional-cell semantics. Record schemas need at least
  one field. Empty typed input writes empty text. Errors use one-based rows
  including the header and field-name columns (nested paths appended); CSV
  syntax errors use physical line and byte position. See [the CSV example](../../examples/csv/main.bork).

## Examples

Import `bork/encoding` for UTF-8, hex and standard or URL-safe base64; malformed
input returns `ParseError`. See [the encoding example](../../examples/bytes_encoding/main.bork).

Import `bork/encoding` for CSV. `encoding.DecodeCsv[T]` reads header-based records
using each field's declared type and facts, collecting errors by row and column.
`EncodeCsv` writes empty optional cells as None (Some("") cannot round-trip); [the CSV example](../../examples/csv/main.bork) shows both.
