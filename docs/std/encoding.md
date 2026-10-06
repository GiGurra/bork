# bork/encoding

`bork/encoding` converts immutable Bytes to text encodings and reads or writes raw and typed CSV.

```bork
import "bork/encoding"

fn main() {
  bytes = encoding.Utf8("hello")
  println(encoding.Hex(bytes))
  println(encoding.Base64(bytes))
  match (encoding.ParseHex("zz")) {
    _: Bytes => println("valid hex")
    error: ParseError => println(error.message)
  }
}
```

The encoders and invalid-hex parser produce:

```text
68656c6c6f
aGVsbG8=
encoding/hex: invalid byte: U+007A 'z'
```

## Byte encoding API

These functions are pure. Parsers return no partial bytes after an error; `ParseError` has `{ input: String, message: String }`.

| Signature | Meaning |
| --- | --- |
| `Utf8(text: String): Bytes` | Encode text, preserving embedded zero bytes. |
| `ParseUtf8(data: Bytes): String \| ParseError` | Validate and decode UTF-8. |
| `Hex(data: Bytes): String` | Encode lowercase hexadecimal. |
| `ParseHex(text: String): Bytes \| ParseError` | Accept either case; require complete byte pairs. |
| `Base64(data: Bytes): String` | Encode padded standard base64. |
| `ParseBase64(text: String): Bytes \| ParseError` | Parse standard base64. |
| `Base64URL(data: Bytes): String` | Encode padded URL-safe base64. |
| `ParseBase64URL(text: String): Bytes \| ParseError` | Parse URL-safe base64. |

Both base64 parsers require zero trailing padding bits and accept CR/LF. Copy a byte list with `values.toBytes()`; `data.toList()` converts it back. See [bytes_encoding](../../examples/bytes_encoding/main.bork).

## CSV API

| Signature | Meaning |
| --- | --- |
| `CsvRows(text: String): List[List[String]] \| CsvError` | Parse raw rows; permits ragged rows. |
| `Csv(rows: List[List[String]]): String` | Write raw rows with CSV quoting and LF endings. |
| `DecodeCsv[T: codec.Decode](text: String): List[T] \| CsvError \| CsvErrors` | Read header-based derived records. |
| `EncodeCsv[T: codec.Encode + codec.Decode](values: List[T]): String \| CsvError` | Write records with headers in schema order. |

`CsvError` has `{ row: Int, column: String, message: String }`. `CsvErrors` has `{ errors: List[CsvError] }`. Typed errors use one-based rows including the header, field-name columns and appended nested paths. Syntax errors use physical lines and byte positions.

## Round-trip records

Derive both codec classes to encode and decode CSV; the Decode schema supplies field names and optional semantics. String cells are literal, so `007` stays `007`. Other types use JSON syntax. Facts and types are checked before constructing records, collecting all row/field errors; no partial records are returned.

```bork
import "bork/codec"
import "bork/encoding"
use codec.Defaults

type User = { name: String, age: Int, note: Option[String] } derive (codec.Encode, codec.Decode)

fn main() {
  original = [User { name: "Ada", age: 37, note: .None }]
  match (encoding.EncodeCsv(original)) {
    text: String => {
      println(text.trim())
      match (encoding.DecodeCsv[User](text)) {
        users: List[User] => println(users == original)
        error => eprintln(toString(error))
      }
    }
    error: encoding.CsvError => eprintln(error.message)
  }
}
```

The CSV representation decodes to the original record:

```text
name,age,note
Ada,37,
true
```

Headers match fields exactly. Unknown, duplicate or missing required headers, ragged typed rows and zero-field schemas are errors. Empty typed input encodes to empty text.

Empty optional cells and missing optional columns become None. Nonempty optional cells become Some and parse like required fields: an optional String cell `hello` needs no JSON quotes. Some("") encodes like None and cannot round-trip. Nonempty JSON `null` in an optional non-String cell is rejected; use an empty cell for None. The JSON bridge also cannot preserve Some(codec.Value.Null), which encodes like None.

Raw CSV supports quoted newlines, skips blank lines, normalizes CRLF and writes LF, following Go CSV behavior. See [the CSV example](../../examples/csv/main.bork) for collected validation failures.
