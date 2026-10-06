# bork/enum

`bork/enum` derives enumeration and lookup for sealed types with public,
fieldless variants. It shares codec wire names: the default is UPPER_SNAKE,
with the type's `codec { naming: ... }` policy and per-variant name overrides.
Lookup is exact and case-sensitive. Source names are accepted only when they
are the chosen wire names.

```bork
import "bork/codec"
import "bork/enum"

type Color = sealed {
  Red
  DarkBlue
  Teal codec { name: "cyan" }
} derive (enum.Enum)

fn main() {
  println(enum.values[Color]()) // [Color.Red, Color.DarkBlue, Color.Teal]
  println(enum.name(Color.DarkBlue)) // DARK_BLUE
  println(enum.byName[Color]("DARK_BLUE")) // Some(Color.DarkBlue)
  println(enum.byName[Color]("DarkBlue")) // None
  println(enum.parse[Color]("purple")) // UnknownName with expected wire names
  println(enum.index(Color.Teal)) // Some(2)
}
```

The `Enum[T]` class supplies these methods:

| Method | Result |
| --- | --- |
| `enum.values[T]()` | `List[T]` in declaration order |
| `enum.name(value)` | Canonical wire name |
| `enum.byName[T](input)` | `Option[T]`; unknown names return `None` |
| `enum.parse[T](input)` | `T` or `UnknownName { name, expected }` |
| `enum.index(value)` | `Option[Int]` containing the declaration position |

An index is not a stable storage ID: reordering variants changes it. Phantom
parameters are supported; `enum.values[Box[Int]]()` produces that instantiation's
values. Derivation rejects records, payload-carrying sealed types, and private
variants. Generated values pass through the type's checked builder; a failed
whole-value invariant panics rather than returning an invalid value.

## Metadata and facts

The selected instance publishes `enum.Info { names, docs, hasFallback }`.
Names and documentation are parallel lists in declaration order; undocumented
variants have an empty documentation string. Read it through
`shape.metadata[T, enum.Enum, enum.Info]()`. The current derivation supports
fieldless variants only, so its `hasFallback` is `false`. Aliases and fallback
variants are not supported yet.

`enum.Known(value)` is a generic predicate with an `Enum` bound. It checks
whether `enum.index(value)` returns `Some`; it can narrow a fact alias:

```bork
import "bork/codec"
import "bork/enum"
import "bork/json"
use codec.Defaults

type Color = sealed { Red; DarkBlue } derive (enum.Enum)
type KnownColor = Color where enum.Known derive (codec.Decode)

fn main() {
  println(json.Decode[KnownColor]("\"DARK_BLUE\""))
}
```

Derived decoders check the alias's facts before returning its value. For the
current fieldless derivation every value is known; unknown input names fail
lookup or decoding. Predicate failures use the ordinary fact diagnostic,
`must be Known`. `enum.parse` reports canonical expected names separately.

See [the enums example](../../examples/enums/main.bork) for JSON integration.
