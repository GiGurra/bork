# bork/enum

`bork/enum` derives enumeration and lookup for sealed types with public,
fieldless variants and an optional positional String fallback. It shares codec wire names: the default is UPPER_SNAKE,
with the type's `codec { naming: ... }` policy and per-variant name overrides.
Lookup is exact and case-sensitive. Source names are accepted only when they
are the chosen wire names or explicit aliases.

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
| `enum.values[T]()` | Known values in declaration order; fallback excluded |
| `enum.name(value)` | Canonical wire name, or the fallback payload unchanged |
| `enum.byName[T](input)` | Exact canonical or alias lookup; unknown names return `None` |
| `enum.parse[T](input)` | Known value, fallback, or `UnknownName { name, expected }` |
| `enum.index(value)` | Declaration position; `None` for the fallback |

An index is not a stable storage ID: reordering variants changes it. Phantom
parameters are supported; `enum.values[Box[Int]]()` produces that instantiation's
values. Derivation rejects records, payload-carrying known alternatives, and private
variants. The fallback must follow [codec marker rules](codec.md). Generated values pass through the type's checked builder; a failed
whole-value invariant panics rather than returning an invalid value.

## Metadata and facts

The selected instance publishes `enum.Info { names, docs, hasFallback }`.
Names and documentation are parallel lists in declaration order; undocumented
variants have an empty documentation string. Read it through
`shape.metadata[T, enum.Enum, enum.Info]()`. The lists exclude the fallback; `hasFallback` reports whether it exists.

`enum.Known(value)` is a generic predicate with an `Enum` bound. It checks
whether `enum.index(value)` returns `Some`; it can narrow a fact alias:

```bork
import "bork/codec"
import "bork/enum"
import "bork/json"
use codec.Defaults

type Color = sealed {
  Red codec { aliases: ["red"] }
  DarkBlue
  Other(String) codec { fallback: true }
} derive (enum.Enum)
type KnownColor = Color where enum.Known derive (codec.Decode)

fn main() {
  println(enum.byName[Color]("purple")) // None: lookup is strict
  println(enum.parse[Color]("purple")) // Color.Other("purple")
  println(enum.name(Color.Other("purple"))) // purple
  println(enum.Known(Color.Other("purple"))) // false
  println(json.Decode[KnownColor]("\"DARK_BLUE\""))
  println(json.Decode[KnownColor]("\"purple\"")) // DecodeError: must be Known
}
```

Derived decoders check the alias's facts before returning its value. Without a fallback, unknown input names fail lookup or decoding. With a fallback, `byName` remains strict and `parse` preserves unknown names. Exhaustive matches still require the fallback arm, including for a `Known` alias. Predicate failures use the ordinary fact diagnostic,
`must be Known`. `enum.parse` reports canonical expected names separately.

See [the enums example](../../examples/enums/main.bork) for JSON integration.
