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
  Green
  Blue
  Other(String) codec { fallback: true }
} derive (enum.Enum, codec.Decode, codec.Encode)

fn label(c: Color): String {
  match (c) {
    .Red => "warm"
    .Green, .Blue => "cool"
    .Other(name) => s"unknown color ${name}"   // required: the point of the fallback
  }
}

fn main() {
  println(json.Decode[Color]("\"purple\""))            // Color.Other("purple")
  println(json.Encode(Color.Other("purple")))           // "purple"
  println(enum.values[Color]())                         // [Red, Green, Blue]
  println(enum.byName[Color]("Green"))                  // Some(Green)
  println(enum.name(Color.Other("purple")))             // purple
}
```

A strict service keeps the same shared type and narrows it with a fact alias:

```bork
pred known(c: Color): Bool { !(c is .Other(_)) }
type KnownColor = Color where known

// json.Decode[KnownColor] rejects "purple" with a DecodeError.
```

## Terms

- **Enum-shaped**: a sealed type whose variants all have no payload, except
  at most one fallback. Generic parameters are allowed if no variant uses them.
- **External name**: the one string a variant has outside code. It is the
  source name unless the type's naming policy or a variant override from
  [codec naming](codec-naming.md) changes it. Codec, CLI, env and enum tooling
  all use it. Aliases are accepted on input only.
- **Fallback**: the single variant that receives any input name that matches no
  external name or alias.

## Decisions

### 1. Wire form: enum-shaped types encode as bare strings

Today `Color.Red` encodes as `{"type":"Red"}`; decode already accepts `"Red"`.

- Enum-shaped types encode as the string `"Red"` (JSON string, YAML scalar,
  CSV cell, CLI/env text).
- Decode accepts the bare string, and keeps accepting `{"type":"Red"}` for
  compatibility with data written by older bork programs.
- Sealed types with payload variants keep the tagged object form, including
  their fieldless variants (`{"type":"Empty"}`), so one type has one shape.

This is a wire change for existing programs that encode fieldless-only types.
Every other ecosystem in the comparison writes enums as bare strings, and
bork is pre-1.0, so the note recommends changing it without an opt-out.

### 2. Opting in: a typed tag on the variant

The fallback is marked with codec naming's typed tag group:

```bork
Other(String) codec { fallback: true }
```

- No new keyword; the same group already carries `aliases` and name overrides.
- It is explicit: a variant named `Unknown` gets no special meaning.
- The checker exposes it to templates as `variant.fallback: Bool` on
  `shape.Variant`, a closed, typed descriptor property like `computed`.
  Codec and enum templates read the descriptor; neither names the other.

Alternatives considered (rejected):

| Option | Why not |
| --- | --- |
| Convention (a variant named `Unknown`) | Magic names; breaks types that already have an `Unknown` meaning "known to be unknown". |
| Marker payload type (`Other(codec.Unknown)`) | Wraps the string twice: `Other(codec.Unknown("purple"))`. |
| Derive-request option | No such syntax exists; the property belongs to the type, not one instance. |
| Contextual keyword (`fallback Other(String)`) | New syntax across parser, formatter and editor grammars for one flag. |

### 3. Rules for the fallback

- At most one fallback per sealed type. A second is an error on that variant.
- Payload:
  - `String` (one positional slot) on an enum-shaped type. It holds the input
    name verbatim; encode writes it back verbatim.
  - `codec.Value` (one positional slot) on any sealed type. It holds the whole
    unknown input (the tagged object, or the bare string) and encode writes
    that value back. This gives open tagged unions the same pass-through.
  - Anything else is an error that lists the two allowed forms.
- The fallback must not have named fields, defaults, a variant `where`, or
  its own wire name or aliases.
- Order of lookup on decode: external names, then aliases, then fallback.
  Matching is exact (case-sensitive), as in codec naming.
- A fallback never appears in `enum.values`, CLI choices or completions.

### 4. A fallback holding a known name

`Color.Other("Red")` can be written in code. Left alone, it would encode as
`"Red"` and come back as `Color.Red`: same wire, different value.

Recommendation: the compiler adds an invariant to the fallback variant: its
`String` payload is not an external name or alias of the type. Construction
follows the usual fact rules, so a literal `Color.Other("Red")` is a compile
error and a runtime string needs a guard or `enum.parse`. Decoding can never
violate it. `codec.Value` payloads get the matching rule on their tag.

The cheaper alternative is to normalize: `enum.parse` and decode never produce
it, and code that constructs it gets what it wrote. That is simpler but leaves
two values with one wire form, so equality and map keys can disagree with the
wire.

### 5. Matching and strictness

- The fallback is an ordinary variant: exhaustive `match` requires an arm for
  it. That is the point; adding a fallback lists every match that must change.
- Strict decoding stays the default: a type without a fallback rejects unknown
  names with `unknown variant "purple" of Color; expected one of Red, Green, Blue`.
- A type with a fallback is lenient everywhere it is decoded. A consumer that
  must reject uses a fact alias (`Color where known`, above); derived decoders
  already check facts. No per-call decode options are needed.
- Known gap: a match on `KnownColor` still needs the `.Other` arm, because
  exhaustiveness does not use facts. That is a separate, general feature.

### 6. Enum tooling: `bork/enum`

A new std package with one class and a source derivation template built on
`shape.variants`. It applies to enum-shaped types; any other target fails with
`shape.fail("enum.Enum requires variants without payloads (plus one fallback)")`.

```bork
class Enum[T] {
  fn values(): List[T]                      // known variants, declaration order
  fn name(value: T): String                 // external name; the fallback's payload
  fn byName(name: String): Option[T]        // external names and aliases only
  fn parse(name: String): T | UnknownName   // byName, then the fallback
  fn index(value: T): Option[Int]           // declaration position; None for the fallback
  fn hasFallback(): Bool
}
type UnknownName = { name: String, expected: List[String] }
```

- `byName` is strict even when a fallback exists; `parse` is total for a type
  with a fallback. `isKnown(name)` is `byName(name).isSome()`, so it is not a
  separate method.
- `index` is the declaration position. It is **not** a stable id: reordering
  variants changes it. Stable numeric ids (protobuf-style) are deferred until a
  binary format needs them.
- The template also publishes `metadata enum.Info` (names, docs, fallback flag),
  so runtime code and other templates can read it with `shape.metadata`
  without a value of the type.
- Methods are called the class way (`enum.values[Color]()`, `enum.name(c)`),
  like `codec.encode`. Member sugar (`Color.values()`) is not in this design.

### 7. Codec and CLI integration

- The codec templates handle enum-shaped targets themselves (bare string
  in and out, fallback), reading `shape.variants` and `variant.fallback`.
  `codec.Decode` does not require `enum.Enum`.
- The derived decoder of an enum-shaped type publishes
  `FieldSchema { kind: "string", choices: [...] }`: a new `choices` list of
  external names with variant docs, fallback excluded.
- bork/cli (boacli) reads `choices` for completion, help and strict
  validation: `--level debug` with names from the same policy as JSON.
- On the CLI an unknown name is rejected even when the type has a fallback:
  a person typing a flag wants an error, not pass-through. A field can opt
  back in later if a real case appears.

## Comparison

| Language / library | Values and lookup | Unknown values | Round-trips unknown? |
| --- | --- | --- | --- |
| Scala enumeratum | `values`, `withName` (throws), `withNameOption`, case-insensitive variants | No built-in; manual `Unknown(value)` entry and custom codec | Only with hand-written codec |
| Scala 3 enums | `values`, `valueOf` (throws), `ordinal` | None | No |
| circe | `deriveEnumerationCodec` | Decode failure | No |
| Rust serde | No built-in values list (strum adds `iter`, `FromStr`) | `#[serde(other)]` on a unit variant | No: the name is dropped |
| Protobuf | Numbers and names in descriptors | proto3/open enums keep the number; Java exposes `UNRECOGNIZED`; closed enums move it to unknown fields | Yes, by number |
| Kotlin | `entries`, `valueOf` (throws), `ordinal` | kotlinx: `coerceInputValues` uses a default; Jackson: `@JsonEnumDefaultValue` | No |
| TypeScript | String unions; values only via `as const` arrays | `"red" \| (string & {})` widens the type | Yes, but nothing is checked |
| **Bork (proposed)** | `enum.values`, `byName`, `parse`, `name`, `index` | Typed `fallback` variant holding `String` or `codec.Value` | Yes, by name; strictness via fact alias |

What bork takes from each:

- enumeratum: separate strict lookup (`byName`) and parse; no exceptions.
- serde: the variant-level marker, but keeping the value, which serde drops.
- protobuf: the open-vs-closed distinction, decided per type, with pass-through.
  Bork carries the name rather than a number, because JSON/YAML carry names.
- Kotlin/Scala 3: declaration-order `values` and an `index`, documented as
  unstable for storage.
- TypeScript: the lesson that an open string type is easy to write and easy
  to forget to handle; bork makes the open case a variant that `match` enforces.

## Implementation plan (after approval)

1. **Wire form**: enum-shaped types encode as bare strings; decode errors list
   the expected names. Golden and doc updates.
2. **Fallback**: `variant.fallback` on shape descriptors, checker rules
   (one fallback, payload forms), codec encode/decode, the known-name invariant.
   Depends on codec naming's tag groups landing first; if they slip, this PR
   adds the `codec { fallback: true }` group alone, with the same syntax.
3. **`bork/enum`**: class, template, `enum.Info` metadata, docs page
   (`docs/std/enum.md`), example.
4. **Schema choices**: `FieldSchema.choices` for bork/cli to consume.
5. Later, separately: `codec.Value` fallback for payload-carrying sealed types,
   if (2) gets long.

Each step has golden cases for decode, encode, round trip, fallback,
fact-alias strictness and diagnostics, plus `TestDocSnippets` coverage.

## Not in this design

- Stable numeric ids or binary enum encodings.
- Case-insensitive lookup (codec naming keeps matching exact; aliases cover
  known spellings).
- Fact-aware exhaustiveness (`KnownColor` matches without `.Other`).
- Member syntax such as `Color.values()`.
- Fallbacks for records' unknown fields (codec naming's unknown-members topic).

## Decisions for the human

1. **Bare-string wire form** for enum-shaped types, with no opt-out
   (recommended), or keep `{"type": ...}` and make strings opt-in.
2. **Marker**: `codec { fallback: true }` on the variant (recommended), or a
   different spelling.
3. **Fallback holding a known name**: compiler invariant (recommended) or
   leave it to the programmer.
4. **Strict consumers via fact alias** (recommended) rather than per-call
   decode options.
5. **`codec.Value` fallback** for payload-carrying sealed types: in scope now,
   or later.
6. **CLI rejects unknown names** even for types with a fallback (recommended).
