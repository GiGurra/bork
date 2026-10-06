# bork/url

`bork/url` parses and builds immutable URL references and repeated query parameters.

```bork
import "bork/url"

fn main() {
  value = url.Url { scheme: "https", host: "example.com", path: "/search", query: { "q": ["bork language"] } }
  println(value.String())
  match (url.Parse("https://example.com/%zz")) {
    _: ParseError => println("invalid escape")
    parsed: url.Url => println(parsed.String())
  }
}
```

```text
https://example.com/search?q=bork+language
invalid escape
```

## API

| Signature | Meaning |
| --- | --- |
| `Parse(text: String): Url \| ParseError` | Parse an absolute URL or relative reference; reject malformed queries. |
| `Build(value: Url): String` | Format fields with URL escaping rules. |
| `(value: Url) String(): String` | Format the immutable URL. |
| `(value: Url) Hostname(): String` | Read the host without brackets or port. |
| `(value: Url) Port(): String` | Read the port, or empty text. |
| `ParseQuery(text: String): Query \| ParseError` | Decode a query without its leading question mark. |
| `EncodeQuery(query: Query): String` | Sort keys and encode every value. |
| `(query: Query) EncodeQuery(): String` | Sort keys and encode every value. |
| `(text: String) PathEscape(): String` | Escape one path segment. |
| `(text: String) PathUnescape(): String \| ParseError` | Decode percent escapes while retaining plus. |
| `(text: String) QueryEscape(): String` | Escape one query component. |
| `(text: String) QueryUnescape(): String \| ParseError` | Decode percent escapes and plus-as-space. |

## URL fields and building

`Url` has these public fields and defaults:

| Field | Default |
| --- | --- |
| `scheme: String`, `host: String`, `path: String`, `fragment: String` | `""` |
| `query: Query` | `{:}` |
| `user: Option[UserInfo]` | `.None` |
| `opaque: String`, `rawPath: String`, `rawFragment: String` | `""` |
| `forceQuery: Bool`, `omitHost: Bool` | `false` |

`UserInfo` has `username: String` and `password: Option[String] = .None`.
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
repeated values and their existing IoError/codec.DecodeError results. Request keeps its
raw query String for HTTP forwarding and form decoding. HTTP clients accept the
formatted `value.String()`.


## Update a URL

```bork
import "bork/url"

fn demo() uses io: Ok | ParseError {
  original = url.Parse("https://example.com/one%2Ftwo?q=first&q=second")?
  println(original.query.get("q"))
  println(original.copy(path: "/changed path").String())
  println("one/two".PathEscape())
}

fn main() {
  println(demo())
}
```

## Decode query components

```bork
import "bork/url"

fn main() {
  println("a+b".PathUnescape())
  println("a+b".QueryUnescape())
  println(url.EncodeQuery({ "q": ["a b", "c+d"] }))
}
```

```text
a+b
a b
q=a+b&q=c%2Bd
```

See the [URL example](../../examples/url/main.bork).


Run `bork doc bork/url` for the generated reference.

[All standard packages](README.md)
