# Typed interpolation

A string with a prefix can have values inserted into it. The prefix decides what gets built: `s"..."` builds a `String`, and a library's prefix builds whatever that library wants, with the inserted values kept apart from the literal text.

## `s"..."`

```bork
type Order = { id: Int, items: List[String] }

fn main() {
  order = Order { id: 7, items: ["tea", "milk"] }
  println(s"Order ${order.id} has ${order.items.length()} items")
  println(s"The whole value: $order")
  println(s"Price: $$5")
}
```

- `$name` inserts a name, and `${...}` any expression.
- `$$` is a dollar sign.
- Any value can be inserted. It appears as `println` would show it.
- A string without a prefix never interpolates.

## `sql.SQL"..."`

The `sql.SQL` prefix from [bork/sql](../std/sql.md) builds a `Statement` instead of a `String`. The literal text is the query, and each inserted value is sent to the database separately, as a bound parameter.

```bork
import codec "bork/codec"

import "bork/sql"
use codec.Defaults

type User = { id: Int, name: String } derive (codec.Decode)

fn byName(db: sql.Connection, name: String) uses io + net: List[User] | sql.Error | codec.DecodeError {
  sql.SQL"SELECT id, name FROM users WHERE name = $name".Query[User](db)
}
```

Whatever `name` contains, it is only ever a value. It cannot change the query.

`derive (codec.Decode)` lets rows be read into `User` records. See [derived instances](packages.md#derived-instances).

Types carry this through the API. Query functions take a `Statement`, and a `Statement` comes from a literal in your source code, so a `String` assembled at run time cannot be run as a query by accident. Running such text is possible, but it has to be spelled out with `sql.Unsafe(text)`.

Holes accept `String`, `Int`, `Float`, `Bool`, `Bytes`, and `sql.Null` as values. Two more kinds of hole compose queries safely:

```bork
import "bork/sql"

fn search(table: String, name: String): sql.Statement | sql.Error {
  target = sql.Name(table)?
  filter = sql.SQL"name = $name"
  sql.SQL"SELECT * FROM $target WHERE $filter"
}
```

- `sql.Name(text)` makes a checked, quoted identifier for a table or column name.
- Another `Statement` is spliced in as a fragment, and keeps its own parameters.

### Checked while compiling

The library inspects the literal during compilation and rejects holes that cannot work:

```bork fails
import "bork/sql"

fn broken(name: String): sql.Statement {
  sql.SQL"SELECT * FROM users WHERE name = '$name'"
}
```

```text
SQL hole is inside quoted text or an identifier
```

The hole is already a bound value, so quoting it is a mistake, and the compiler says so before the program ever runs. The check covers where holes are placed. It does not parse SQL or know your schema.

The [sql_interpolation example](../../examples/sql_interpolation/README.md) runs all of this against SQLite.

## Defining your own prefix

A prefix is an ordinary function, so any package can define one. This one builds HTML and escapes every inserted value:

```bork
type Html = { text: String }
type Builder = { parts: List[String], done: String, next: Int }

fn escape(text: String): String {
  text.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll("\"", "&quot;").replaceAll("'", "&#39;")
}

fn HTML(parts: StaticParts): Builder {
  Builder { parts: parts.values, done: parts.values.get(0).getOr(""), next: 1 }
}

fn (b: Builder) Interpolate(value: String): Builder {
  b.copy(done: b.done + escape(value) + b.parts.get(b.next).getOr(""), next: b.next + 1)
}

fn (b: Builder) Finish(): Html {
  Html { text: b.done }
}

fn main() {
  comment = "<script>alert(1)</script>"
  page = HTML"<p>$comment</p>"
  println(page.text)
  label = "\"quoted\" & 'single'"
  attribute = HTML"<p title=\"$label\">reply</p>"
  println(attribute.text)
}
```

```text
<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>
<p title="&quot;quoted&quot; &amp; &#39;single&#39;">reply</p>
```

The compiler turns `HTML"<p>$comment</p>"` into three steps:

1. It calls `HTML` with the literal pieces, here `["<p>", "</p>"]`. They arrive as a `StaticParts`, a value only the compiler can create.
2. It calls `Interpolate` on the result once for each hole, in order.
3. It calls `Finish`, and the result of that is the value of the whole expression.

Each hole is checked against the type `Interpolate` accepts. Here that is `String`, so `HTML"<p>${42}</p>"` is a type error. A library decides which types it accepts and what to do with each.

A library can also check its literals at compile time, as `sql.SQL` does, by declaring an `InterpolationValidator` for its builder. The [grammar reference](../grammar.md#writing-an-interpolation-validator) describes how.

---

Previous: [Compile-time evaluation](comptime.md) · Next: [Derivation templates](derivation.md) · [All pages](../README.md#the-language)
