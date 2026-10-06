# bork/json

`bork/json` parses and renders JSON, converts checked typed values, queries dynamic trees and streams JSON Lines.

```bork
import "bork/codec"
import "bork/json"
use codec.Defaults

type User = { name: String, age: Int } derive (codec.Encode, codec.Decode)

fn main() {
  text = json.Encode(User { name: "Ada", age: 37 })
  println(text)
  match (json.Decode[User](text)) {
    user: User => println(user.name, user.age)
    error => eprintln(toString(error))
  }
}
```

The JSON text decodes back to the same fields:

```text
{"name":"Ada","age":37}
Ada 37
```

Import [bork/codec](codec.md) for the value tree and Encode/Decode classes, and select `use codec.Defaults` for standard instances. Derived decoders check field facts and record invariants.

## Derived wire names

Derived record keys and sealed names follow the [codec naming policies and typed tags](codec.md#wire-names). Payload-free sealed types encode as canonical bare strings: `SiteAdmin` becomes `"SITE_ADMIN"` by default. Decoding accepts `"SITE_ADMIN"` or `{"type":"SITE_ADMIN"}`; the old source name `"SiteAdmin"` needs an explicit `Verbatim` policy. Mixed sealed types keep their discriminator objects. Naming also applies to decode error paths. Declared aliases read old spellings but always encode the canonical name. A second spelling for one field is an error. Set `unknown: codec.Unknown.Reject` on the type to reject unrecognized object keys.

## Text API

These functions are pure; `json.JsonError` has `{ message: String }`. A `codec.DecodeError` has `{ path: String, message: String }`.

| Signature | Meaning |
| --- | --- |
| `Parse(text: String): codec.Value \| JsonError` | Parse one JSON document. |
| `Render(j: codec.Value): String` | Render compact JSON. |
| `Decode[T: codec.Decode](text: String): T \| JsonError \| codec.DecodeError` | Parse and decode a checked value. |
| `Encode[T: codec.Encode](x: T): String` | Encode a typed value as compact JSON. |
| `Pretty(value: codec.Value, spaces: Indent = 2): String \| JsonError` | Render indented JSON; Indent is Int in `0..8`. |

`json.Parse` rejects duplicate decoded object keys, including equivalent escaped spellings, and reports the second key's line/column and the first key's location. It rejects invalid UTF-8 before parsing with a line/column diagnostic; a valid U+FFFD replacement character is accepted. `json.Encode` sorts keys for unordered hash maps, while insertion-ordered and custom ordered maps retain traversal order. These parsing checks do not restrict manually constructed `codec.Value` objects.

Pretty preserves object field order and exact number text, and rejects invalid manually constructed number text. Render/Encode return text directly; use Pretty or Write when you need checked rendering of a manually assembled tree.

## Separate syntax and type errors

```bork
import "bork/codec"
import "bork/json"
use codec.Defaults

type User = { age: Int } derive (codec.Decode)

fn main() {
  for (text in ["{", "{\"age\":\"old\"}"]) {
    match (json.Decode[User](text)) {
      user: User => println(user.age)
      _: json.JsonError => println("invalid JSON syntax")
      error: codec.DecodeError => println(error.path + ": " + error.message)
    }
  }
}
```

Syntax and field errors have different result types:

```text
invalid JSON syntax
.age: expected a whole number, found a string
```

## Query dynamic JSON

| Signature | Meaning |
| --- | --- |
| `Field(value: codec.Value, name: String): Option[codec.Value]` | First matching object field. |
| `Index(value: codec.Value, index: Int): Option[codec.Value]` | Array element. |
| `At(value: codec.Value, path: List[String \| Int]): Option[codec.Value]` | Follow string keys and integer indices. |

None means a missing path; Some(codec.Value.Null) preserves a present JSON null. An empty path returns the input.

```bork
import "bork/codec"
import "bork/json"

fn main() {
  match (json.Parse("{\"users\":[{\"name\":\"Ada\",\"note\":null}]}")) {
    value: codec.Value => println(json.At(value, ["users", 0, "name"]))
    error: json.JsonError => eprintln(error.message)
  }
}
```

## JSON Lines API

JSON Lines stores one compact document per line. Reader and Writer are scope-owned resources; their operations use io.

| Signature | Meaning |
| --- | --- |
| `OpenLines(path: String, s: Scope) uses io: Reader \| IoError` | Open a line reader. |
| `CreateLines(path: String, s: Scope) uses io: Writer \| IoError` | Create/truncate a line writer. |
| `Next(reader: Reader) uses io: Option[codec.Value] \| LineError \| IoError` | Read one document; None at EOF. |
| `Write[T: codec.Encode](writer: Writer, value: T) uses io: Ok \| JsonError \| IoError` | Validate and immediately write one compact document plus LF. |

`LineError` has `{ path: String, line: Int, message: String }`; line is one-based. Blank or malformed lines return LineError, and the next call continues at the following line. Next accepts LF/CRLF and a final unterminated line, without a scanner token-size limit or whole-file buffering.

```bork
import "bork/codec"
import "bork/json"
use codec.Defaults

fn write(path: String) uses io: Ok | IoError | json.JsonError {
  scope output {
    writer = json.CreateLines(path, output)?
    json.Write(writer, { "name": "Ada" })?
    json.Write(writer, { "name": "Grace" })?
  }
}

fn consume(reader: json.Reader) uses io: Ok | IoError | json.LineError {
  match (json.Next(reader)) {
    .Some(value) => {
      println(json.Render(value))
      consume(reader)
    }
    .None => Ok
    error: IoError => error
    error: json.LineError => error
  }
}

fn read(path: String) uses io: Ok | IoError | json.LineError {
  scope input {
    reader = json.OpenLines(path, input)?
    consume(reader)?
  }
}

fn main() {
  match (write("users.jsonl")) {
    _: Ok => {
      match (read("users.jsonl")) {
        _: Ok => {}
        error => eprintln(toString(error))
      }
    }
    error => eprintln(toString(error))
  }
}
```

Run this in a disposable directory; it creates `users.jsonl`. Concurrent calls on one reader are serialized. Concurrent writes on one writer keep each line together, with unspecified order; a failed write may leave a partial final line. Resources can be attached using [scope ownership](../language/scopes.md). See [json_lines](../../examples/json_lines/main.bork) for recoverable line errors.
