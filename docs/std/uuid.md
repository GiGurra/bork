# bork/uuid

`bork/uuid` creates and parses canonical UUID values, with JSON string codecs.

```bork
import "bork/uuid"

fn main() {
  println(uuid.Format(uuid.Nil()))
  match (uuid.Parse("not-a-uuid")) {
    _: ParseError => println("invalid UUID")
    id: uuid.Uuid => println(id)
  }
}
```

```text
00000000-0000-0000-0000-000000000000
invalid UUID
```

## API

| Signature | Meaning |
| --- | --- |
| `Parse(text: String): Uuid \| ParseError` | Parse and normalize supported UUID text forms. |
| `V4() uses random: Uuid \| IoError` | Generate a random version-4 UUID. |
| `V7() uses random + clock: Uuid \| IoError` | Generate a timestamp-ordered version-7 UUID. |
| `Format(id: Uuid): String` | Return canonical lowercase text. |
| `Nil(): Uuid` | Return the all-zero UUID. |
| `Version(id: Uuid): Int` | Read the UUID version. |

`Uuid` is `{ value: String where Canonical }`. `Canonical(text: String): Bool`
proves valid, lowercase, dashed UUID text, so invalid or noncanonical values
cannot be constructed directly. Values compare structurally and work as map keys.

## Generate identifiers

```bork
import "bork/uuid"

fn main() {
  match (uuid.V4()) {
    id: uuid.Uuid => println(uuid.Version(id))
    error: IoError => eprintln(error.message)
  }
  match (uuid.V7()) {
    id: uuid.Uuid => println(uuid.Version(id))
    error: IoError => eprintln(error.message)
  }
}
```

```text
4
7
```

V4 is random; V7 includes a timestamp for chronological ordering. Both can
return `IoError` on entropy failure.

## Parse and encode

```bork
import "bork/json"
import "bork/uuid"
use uuid.Codecs

fn main() {
  println(json.Encode(uuid.Nil()))
  println(uuid.Parse("URN:UUID:550e8400-e29b-41d4-a716-446655440000"))
}
```

Parse accepts dashed, compact, braced, and `urn:uuid` forms and normalizes them.
Printing and `toString(id)` use canonical text. `Codecs` provides Encode/Decode
instances for JSON strings, validating nested record fields too. `env.Load`
UUID cells use JSON string syntax; `env.LoadJson` accepts ordinary JSON UUID
strings. See [JSON codecs](codec.md) and the
[UUID example](../../examples/uuid/main.bork).


[All standard packages](README.md)
