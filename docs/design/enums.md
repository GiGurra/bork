# Sealed enums: fallback variants and enum tooling

Ticket: bork-rz5wv6. Status: design for human approval; nothing here is
implemented yet. Naming policies, aliases and tag groups come from bork-bbtlwz
([codec naming](codec-naming.md), PR #408); CLI choices (bork-xls6ya) build on the
tooling described here.

## Problem

Microservices roll out new enum values gradually. During a rollout:

- some services must **reject** a value they don't know (today's behavior);
- others must **accept and pass it through**: decoding `"purple"` and
  encoding it again must write `"purple"`, not fail and not lose it.

Bork also has no way to ask an enum-like sealed type for its values, look one
up by name, or get a value's name. CLI choices, config validation and admin
UIs all need that.

## Example

```bork
import "bork/codec"
import "bork/enum"
import "bork/json"
use codec.Defaults

type Color = sealed {
  Red
  DarkBlue
  Teal codec { aliases: ["Cyan"] }
  Other(String) codec { fallback: true }
} derive (enum.Enum, codec.Decode, codec.Encode)

fn label(c: Color): String {
  match c {
    .Red => "warm"
      .DarkBlue, .Teal => "cool"
    .Other(name) => s"unknown color ${name}" // required: the point of the fallback
  }
}

fn main() {
  println(json.Decode[Color]("\"purple\"")) // Color.Other("purple")
  println(json.Encode(Color.Other("purple"))) // "purple"
  println(json.Encode(Color.DarkBlue)) // "DARK_BLUE"
  println(enum.values[Color]()) // [Red, DarkBlue, Teal]
  println(enum.byName[Color]("DARK_BLUE")) // Some(DarkBlue)
  println(enum.byName[Color]("Cyan")) // Some(Teal)
  println(enum.name(Color.DarkBlue)) // DARK_BLUE
  println(enum.name(Color.Other("purple"))) // purple
}
```

A strict service keeps the same shared type and narrows it with a fact alias:

```bork
type KnownColor = Color where enum.Known

// json.Decode[KnownColor] rejects "purple":
// DecodeError { path: "", message: "must be Known", ... }
// Strict lookup with enum.parse on a closed enum reports expected wire names.
```

## Terms

- **Enum-shaped**: a sealed type whose variants all have no payload, except
  at most one fallback. Phantom type parameters are allowed;
  `enum.values[Box[Int]]()` lists that instantiation's values.
- **External name** (wire name): the one string a variant has outside code,
  from the type's naming policy or a variant override (section 0). Codec,
  CLI, env and enum tooling all use it. Aliases are accepted on input only.
- **Fallback**: the single variant that receives any input name that matches no
  external name or alias.

## Decisions

### 0. Names: mapped by policy, UPPER_SNAKE_CASE by default

Variant names are mapped like record fields, with codec naming's mechanism
([codec naming](codec-naming.md), #408): the same word splitting and the same
`codec.Naming` policies (`Verbatim`, `Camel`, `Pascal`, `Snake`, `Kebab`,
`ScreamingSnake`).

- **Default by shape.** A type with no `naming` gets its shape's default:
  `ScreamingSnake` for enum-shaped types (`DarkBlue` → `"DARK_BLUE"`),
  `Verbatim` for records and payload-carrying sealed types, as in #408. For
  this, #408's `TypeTags.naming` becomes `Option[Naming] = Option.None`.
- **Per-type override**: `} codec { naming: codec.Naming.Kebab }` after the
  body gives `"dark-blue"`; `codec.Naming.Verbatim` keeps `"DarkBlue"`.
- **Per-variant**: `DarkBlue codec { name: "navy", aliases: ["DarkBlue"] }`.
  `name` replaces the mapped name; `aliases` are accepted on decode only.
- **Collisions** after mapping are compile errors on the later variant
  (`HttpError` and `HTTPError` both give `HTTP_ERROR`). So are mapped names
  that bork/yaml (YAML 1.2 core schema) reads as non-strings: `TRUE`, `NULL`,
  `~`, numbers. `ON`/`YES` stay legal; the renderer quotes them for YAML 1.1
  readers.
- **Raw source names are not accepted** on decode. Each variant has exactly
  the names it shows: its wire name plus explicit aliases. Accepting `DarkBlue`
  too would double every variant's inputs, hide typos in the policy, and send
  a name that should reach the fallback to a variant instead. To migrate data
  written with source names, add them as aliases, or set `Verbatim`.
- **Everything uses the wire name.** `enum.name`, `byName`, `parse`,
  `enum.Info`, error messages and CLI choices (`--color DARK_BLUE`). Bork
  code and `println` keep the source name (`Color.DarkBlue`).
- The fallback's payload is never mapped: `"purple"` stays `"purple"`.

### 1. Wire form: enum-shaped types encode as bare strings

Today `Color.Red` encodes as `{"type":"Red"}`; decode already accepts `"Red"`.

- Enum-shaped types encode as the bare wire name `"RED"` (JSON string, YAML
  scalar, CSV cell, CLI/env text).
- Decode accepts the bare string, and also the object form `{"type":"RED"}`.
  Old data written as `{"type":"Red"}` needs `Verbatim` or an alias, since
  the default names changed too.
- Sealed types with payload variants keep the tagged object form, including
  their fieldless variants (`{"type":"Empty"}`), so one type has one shape.

This is a wire change for existing programs that encode fieldless-only types.
Bare strings are what Jackson, kotlinx, serde, enumeratum-circe, protobuf JSON
and TypeScript unions produce (circe's default sealed-trait derivation writes
`{"Red":{}}`). Bork is pre-1.0, so the note recommends changing it without an
opt-out.

YAML: wire names that YAML 1.2 reads as Bool, Null or a number are compile
errors (section 0).

### 2. Opting in: a typed tag on the variant

The fallback is marked with codec naming's typed tag group:

```bork
Other(String) codec { fallback: true }
```

- No new keyword; the same group already carries `aliases` and name overrides.
- It is explicit: a variant named `Unknown` gets no special meaning.
- Templates read it like any tag: `variant.tagged[codec.VariantTags]()`
  ([codec naming](codec-naming.md)). `bork/enum` imports `bork/codec` for
  this, since external names and aliases are codec concepts too. There is no
  separate `shape`-level flag.
- The checker validates the `codec` tag group's fallback rules (below), as #408
  already does for names and aliases. That is the one place the compiler knows
  the flag.

Alternatives considered (rejected):

| Option | Why not |
| --- | --- |
| Convention (a variant named `Unknown`) | Magic names; breaks types that already have an `Unknown` meaning "known to be unknown". |
| Marker payload type (`Other(codec.Unknown)`) | Wraps the string twice: `Other(codec.Unknown("purple"))`. |
| Derive-request option | No such syntax exists; the property belongs to the type, not one instance. |
| Contextual keyword (`fallback Other(String)`) | New syntax across parser, formatter and editor grammars for one flag. |

### 3. Rules for the fallback

- At most one fallback per sealed type. A second is an error on that variant.
- An enum with a fallback needs at least one known fieldless alternative; a fallback-only type is a compile error.
- Payload: exactly one positional `String` slot, on an enum-shaped type. It
  holds the input name verbatim; encode writes it back verbatim. Anything else
  is an error. (A `codec.Value` fallback for tagged unions is a later step;
  see below.)
- The fallback must not have named fields, defaults, a user-written variant
  `where`, or its own wire name or aliases. Its only fact is the generated one
  in section 4.
- Inputs: a bare string, or the legacy `{"type": "purple"}` object with no
  other members, falls back. Any other shape (a number, an object with extra
  members) is a decode error, so nothing is silently dropped.
- Order of lookup on decode: wire names, then aliases, then fallback.
  Matching is exact (case-sensitive), as in codec naming: `"dark_blue"` is
  unknown under `ScreamingSnake` unless it is an alias.
- A fallback never appears in `enum.values`, CLI choices or completions.

### 4. A fallback holding a known name

`Color.Other("RED")` can be written in code. Left alone, it would encode as
`"RED"` and come back as `Color.Red`: same wire, different value.

Recommendation: the compiler adds an invariant to the fallback variant: its
`String` payload is not one of the type's wire names or aliases, as computed
from the type's naming policy and the variants' `codec` tags. Construction
follows the usual fact rules, so a literal `Color.Other("RED")` is a compile
error and a runtime string needs a guard or `enum.parse`. Decoding can never
violate it.

The cheaper alternative is to normalize: `enum.parse` and decode never produce
it, and code that constructs it gets what it wrote. That is simpler but leaves
two values with one wire form, so equality and map keys can disagree with the
wire.

### 5. Matching and strictness

- The fallback is an ordinary variant: exhaustive `match` requires an arm for
  it. That is the point; adding a fallback lists every match that must change.
- Strict decoding stays the default: a type without a fallback rejects unknown
  names with `unknown variant "purple" of Color; expected one of Red, Green, Blue`.
- A type with a fallback is lenient wherever codec decodes it: JSON, YAML,
  CSV, env, SQL and config files. The CLI is the exception (section 7).
- A consumer that must reject uses a fact alias with the generic predicate
  `enum.known` (`Color where enum.known`, above). Derived decoders already
  check facts. The error keeps the "expected one of" list.
- Gap: a strict service can narrow a top-level value or a field of its own
  type. It cannot narrow `Order.color` inside a DTO it shares with lenient
  services without a second DTO type. If that matters, the fix is a selectable
  strict decoder instance (`use`), not a per-call option (human decision 4).
- Gap: a match on `KnownColor` still needs the `.Other` arm, because
  exhaustiveness does not use facts. That is a separate, general feature.

### 6. Enum tooling: `bork/enum`

A new std package with one class and a source derivation template built on
`shape.variants`. It applies to enum-shaped types; any other target fails with
`shape.fail("enum.Enum requires variants without payloads (plus one fallback)")`.
A type with private (lower-case) variants is also rejected, since `values`
and `byName` would hand them to other packages.

```bork
class Enum[T] {
  fn values(): List[T]                      // known variants, declaration order
  fn name(value: T): String                 // external name; the fallback's payload
  fn byName(name: String): Option[T]        // external names and aliases only
  fn parse(name: String): T | UnknownName   // byName, then the fallback
  fn index(value: T): Option[Int]           // declaration position; None for the fallback
}
type UnknownName = { name: String, expected: List[String] }
pred Known[T: Enum](value: T) { ... }       // value is not the fallback
```

- `byName` is strict even when a fallback exists; `parse` is total for a type
  with a fallback. `isKnown(name)` is `byName(name).isSome()`, and
  `hasFallback` is in `enum.Info`, so neither is a separate method.
- `Known` needs predicates with class bounds; if the checker can't do that
  yet, the template generates a per-type predicate instead.
- `index` is the declaration position. It is **not** a stable id: reordering
  variants changes it. Stable numeric ids (protobuf-style) are deferred until a
  binary format needs them.
- The template also publishes `metadata enum.Info` (names, docs, whether there
  is a fallback),
  so runtime code and other templates can read it with `shape.metadata`
  without a value of the type.
- Methods are called the class way (`enum.values[Color]()`, `enum.name(c)`),
  like `codec.encode`. Member sugar (`Color.values()`) is not in this design.

### 7. Codec and CLI integration

- The codec templates handle enum-shaped targets themselves (bare string
  in and out, fallback), reading `shape.variants` and the `codec` tags.
  `codec.Decode` does not require `enum.Enum`.
- The derived decoder of an enum-shaped type publishes
  `FieldSchema { kind: "string", variants: [...] }`: a new
  `List[codec.VariantSchema { name, doc }]` of wire names and variant docs,
  fallback excluded. (`DefaultSchema.choices` already means something else.)
  Variant docs need a new `doc` property on `shape.Variant`.
- bork/cli reads `variants` for completion, help and strict
  validation: `--level DEBUG` by default, or `--level debug` for a type with
  `codec { naming: codec.Naming.Snake }`. One policy serves JSON and CLI.
- On the CLI an unknown name is rejected even when the type has a fallback:
  a person typing a flag wants an error, not pass-through. A field can opt
  back in later if a real case appears.

## Comparison

| Language / library | Values and lookup | Unknown values | Round-trips unknown? |
| --- | --- | --- | --- |
| Scala enumeratum | `values`, `withName` (throws), `withNameOption`, case-insensitive variants | No built-in; manual `Unknown(value)` entry and custom codec | Only with hand-written codec |
| Scala 3 enums | `values`, `valueOf` (throws), `ordinal` | None | No |
| circe | `deriveEnumerationCodec` | Decode failure | No |
| Rust serde | No built-in values list (strum adds `iter`, `FromStr`) | `#[serde(other)]` on a unit variant drops the name; `#[serde(untagged)] Other(String)` (serde 1.0.181+) keeps it | Only with `untagged` |
| Protobuf | Numbers and names in descriptors | proto3/open enums keep the number; Java exposes `UNRECOGNIZED`; closed enums move it to unknown fields | Yes, by number |
| Kotlin | `entries`, `valueOf` (throws), `ordinal` | kotlinx: `coerceInputValues` uses a default; Jackson: `@JsonEnumDefaultValue` | No |
| TypeScript | String unions; values only via `as const` arrays | `"red" \| (string & {})` widens the type | Yes, but nothing is checked |
| **Bork (proposed)** | `enum.values`, `byName`, `parse`, `name`, `index` | Typed `fallback` variant holding the `String` | Yes, by name; strictness via fact alias |

What bork takes from each:

- enumeratum: separate strict lookup (`byName`) and parse; no exceptions.
- serde: the variant-level marker, keeping the value as `untagged` does, but
  with one explicit flag instead of a general untagged mechanism.
- protobuf: the open-vs-closed distinction, decided per type, with pass-through.
  Bork carries the name rather than a number, because JSON/YAML carry names.
- Kotlin/Scala 3: declaration-order `values` and an `index`, documented as
  unstable for storage.
- TypeScript: the lesson that an open string type is easy to write and easy
  to forget to handle; bork makes the open case a variant that `match` enforces.

## Implementation plan (after approval)

1. **Wire form**: enum-shaped types encode as bare strings; decode errors list
   the expected names. Golden and doc updates.
2. **Fallback**: checker rules for the `codec` fallback tag (one fallback,
   payload, YAML-safe names), codec encode/decode, the known-name invariant.
   Depends on codec naming's tag groups (#408). If those slip, this step
   brings the tag-group grammar itself: parser, fmt, LSP and editor grammars,
   following docs/syntax-changes.md.
3. **`bork/enum`**: class, template, `enum.known`, `enum.Info` metadata, docs
   page (`docs/std/enum.md`), example.
4. **Schema variants**: `shape.Variant.doc` and `FieldSchema.variants` for
   bork/cli to consume.
5. Later: a `codec.Value` fallback for payload-carrying sealed types. It holds
   the whole unknown object and re-encodes it. A known tag with a bad payload
   is still an error, and an object without `type` or a non-object is not
   captured.

Each step has golden cases for decode, encode, round trip, fallback,
fact-alias strictness and diagnostics, plus `TestDocSnippets` coverage.

## Not in this design

- Stable numeric ids or binary enum encodings.
- Case-insensitive lookup (codec naming keeps matching exact; aliases cover
  known spellings).
- Fact-aware exhaustiveness (`KnownColor` matches without `.Other`).
- Member syntax such as `Color.values()`.
- Fallbacks for records' unknown fields (codec naming's unknown-members topic).
- Enum-keyed maps (`Map[Color, V]`); codec maps take `String` keys today.

## Decisions for the human

0. **Names**: enum-shaped types default to `UPPER_SNAKE_CASE` wire names, with
   per-type `naming` and per-variant `name`/`aliases` (recommended, per the
   human's feedback). Decode accepts only wire names and aliases, not raw
   source names (recommended).
1. **Bare-string wire form** for enum-shaped types, with no opt-out
   (recommended), or keep `{"type": ...}` and make strings opt-in.
2. **Marker**: `codec { fallback: true }` on the variant (recommended), or a
   different spelling.
3. **Fallback holding a known name**: compiler invariant (recommended) or
   leave it to the programmer.
4. **Strict consumers via fact alias** (`Color where enum.known`,
   recommended). Accept the gap for shared DTOs for now, or add a selectable
   strict decoder instance too.
5. **`codec.Value` fallback** for payload-carrying sealed types: later
   (recommended), or in this epic.
6. **CLI rejects unknown names** even for types with a fallback (recommended).
