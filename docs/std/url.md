# bork/url

`bork/url` parses and builds immutable URL values using Go's `net/url`.
Operations are pure. `Parse(text): Url | ParseError` accepts absolute URLs,
network-path references and relative references. It rejects malformed URLs and
query strings without returning a partial result.

## URL fields and building

`Url` has decoded `scheme`, `host`, `path`, `query`, and `fragment` fields.
`query` is a `Query` alias for `Map[String, List[String]]`, preserving every value
of repeated keys. `user: Option[UserInfo]` contains a username and optional
password, distinguishing absent and empty passwords.

Other fields preserve Go URL distinctions: `opaque`, `rawPath`, `rawFragment`,
`forceQuery` (an empty trailing `?`), and `omitHost` (a hierarchical scheme without
an authority). Every field defaults to empty, None or false, so a URL can be
constructed with only the fields it needs.

`Build(value)` and `value.String()` format fields with Go URL escaping rules.
A whole decoded path keeps `/` separators; use `segment.PathEscape()` to escape a
single path segment. Raw path/fragment hints preserve escaped separators and
other valid spellings from Parse. If a hint no longer decodes to the corresponding
field after `copy`, formatting ignores it and escapes the updated field.
`Hostname()` removes brackets and a port; `Port()` returns the port or empty text.

Building does not validate constructed fields; Parse the result if validation is
needed. A URL is a reference, and parsing does not require a scheme or host.
Query formatting is canonical: keys sort lexically, values retain their order,
spaces become `+`, and a bare query flag becomes `flag=`. Exact query spelling and
key ordering from input are not retained. Empty value lists omit the key.

## Queries and escaping

`ParseQuery(text): Query | ParseError` decodes a query without the leading `?`.
Repeated keys retain every value; a bare key has one empty String value.
Invalid percent escapes and unescaped semicolons reject the whole query.
`EncodeQuery(query)` and `query.EncodeQuery()` encode the map deterministically.

String methods `PathEscape()` and `QueryEscape()` encode one path segment or one
query component. `PathUnescape()` and `QueryUnescape()` return `String | ParseError`
for malformed escapes. Path unescaping preserves `+`; query unescaping treats it
as a space. Query escaping escapes `/` and literal `+`, while path escaping keeps
`+` as a literal path character.

HTTP's `Query(request)` and `QueryAs[T](request)` use this parser, preserving
repeated values and their existing IoError/DecodeError results. Request keeps its
raw query String for HTTP forwarding and form decoding. HTTP clients accept the
formatted `value.String()`.

## Example

```bork
import "bork/url"

fn main() {
  value = url.Url {
    scheme: "https",
    host: "example.com",
    path: "/search",
    query: { "q": ["bork language"], "page": ["1"] },
  }
  println(value.String())
  println("one/two".PathEscape())
}
```

See [examples/url](../../examples/url/main.bork) for parsing and immutable updates.
