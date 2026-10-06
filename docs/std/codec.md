# bork/codec

`bork/codec` converts typed values to and from a format-independent value tree, checking facts when decoding.

```bork
import "bork/codec"
import "bork/json"
use codec.Defaults

type User = { name: String, age: Int } derive (codec.Decode, codec.Encode)

fn main() {
  user = User { name: "Ada", age: 37 }
  tree = codec.encode(user)
  println(json.Render(tree))
  match (codec.decode[User](tree)) {
    decoded: User => println(decoded.name, decoded.age)
    error: codec.DecodeError => eprintln(error.path + ": " + error.message)
  }
}
```

The round-trip preserves the record:

```text
{"name":"Ada","age":37}
Ada 37
```

Select `use codec.Defaults` for standard instances; derive codec classes on your records and sealed types. The same instances work with [JSON](json.md), [YAML](yaml.md), [CSV](encoding.md), [environment configuration](env.md) and [SQL rows](sql.md).

## API and value tree

These APIs are pure. `codec.Encode[T]` supplies `encode(x: T): codec.Value`; `codec.Decode[T]` supplies `decode(json: codec.Value): T | codec.DecodeError`.

| Signature/type | Meaning |
| --- | --- |
| `codec.encode[T: codec.Encode](x: T): codec.Value` | Convert a typed value to a tree. |
| `codec.decode[T: codec.Decode](json: codec.Value): T \| codec.DecodeError` | Convert a tree to a checked typed value. |
| `codec.DecodeError` | `{ path: String, message: String }`; paths identify fields/indices. |
| `codec.Field` | `{ name: String, value: codec.Value }` |
| `codec.Value.Null` | No value. |
| `codec.Value.Bool` | `{ value: Bool }` |
| `codec.Value.Number` | `{ text: String }`; preserves exact number digits. |
| `codec.Value.String` | `{ value: String }` |
| `codec.Value.Array` | `{ items: List[codec.Value] }` |
| `codec.Value.Object` | `{ fields: List[codec.Field] }`; preserves field order. |

Defaults includes Int, Int8/16/32, Uint8/16/32/64, Float, Float32, Bool, String, Rune, Option, List, Map[String, V], and codec.Value. Numeric-width decoders reject values outside their decimal ranges. Containers require the selected element instances. Custom codecs are ordinary selectable instances: import them with `use` or an exported instance bundle.

## Decode checked values

Derived decoders check field facts and record invariants before returning a record. Handle the error path at the boundary rather than treating decoded input as already valid:

```bork
import "bork/codec"
import "bork/json"
use codec.Defaults

pred adult(n: Int) { n >= 18 }
type User = { age: Int where adult } derive (codec.Decode)

fn main() {
  match (json.Decode[User]("{\"age\":17}")) {
    user: User => println(user.age)
    error: codec.DecodeError => println(error.path + ": " + error.message)
    error: json.JsonError => println(error.message)
  }
}
```

The derived decoder reports the failed field fact:

```text
.age: must be adult
```

JSON/YAML syntax errors belong to the format package; typed-conversion errors belong to codec. A manually constructed codec.Value is a tree, not evidence that its contents fit any typed model.

## Record and variant encoding

Records encode as objects in field order. A named sealed variant uses a `"type"` discriminator and its named fields. A fieldless variant can also decode from its name. Option encodes None as null and Some as the bare value; a missing optional record field decodes as None.

Positional sealed payloads use a `"values"` array in declaration order:

```bork
import "bork/codec"
import "bork/json"
use codec.Defaults

type Reply = sealed { Found(Int, String), Missing } derive (codec.Encode, codec.Decode)

fn main() {
  println(json.Encode(Reply.Found(3, "count")))
  println(json.Encode(Option.Some(3)))
}
```

Positional variants are tagged, while Option uses the bare value:

```text
{"type":"Found","values":[3,"count"]}
3
```

Decoding checks the exact payload count and reports paths such as `.values[0]`. A variant `One((Int, String))` has one tuple-valued payload; `Pair(Int, String)` has two. See [matching](../language/matching.md) for construction and patterns.

Lazy/computed record fields are omitted by derived codecs; their values are computed again after decoding. See [types](../language/types.md) for field semantics.

Records encode as objects. Sealed types whose alternatives all have no payload (enums) encode as bare strings using `ScreamingSnake` names by default: `SiteAdmin` becomes `"SITE_ADMIN"`. They decode from that string or the existing object form `{"type":"SITE_ADMIN"}`. A sealed type with any payload keeps its object representation and defaults to source names: `{"type":"Item","value":3}`. Fieldless alternatives of such a mixed sealed type also accept their canonical string on decode. Source spellings are accepted only when they equal the canonical name. `Option` retains its null/value representation, and missing optional record fields decode as `None`.

### Wire names

Typed `codec` tags configure derived codecs. A type's `naming` policy applies to its fields and variant names; a field or variant's `name` overrides the policy.

```bork
type User = {
  userID: String
  httpURLPort: Int codec { name: "port" }
} codec { naming: codec.Naming.Snake } derive (codec.Decode, codec.Encode)
// JSON: {"user_id":"u","port":8080}

type Access = sealed {
  SiteAdmin
  ReadOnly codec { name: "reader" }
} derive (codec.Decode, codec.Encode)
// SiteAdmin encodes as "SITE_ADMIN"; ReadOnly as "reader".
```

`codec.Naming` provides `Verbatim`, `Camel`, `Pascal`, `Snake`, `Kebab`, and `ScreamingSnake`. Records and mixed sealed types default to `Verbatim`. Set an enum's `naming` to `Verbatim` to preserve source spelling; it still encodes as a bare string.

`codec.Words(name)` splits underscores, hyphens, whitespace, lowercase-to-uppercase transitions, and acronym boundaries. Digits stay within their word: `httpURLPort` gives `["http", "url", "port"]` and `ipv4Addr` gives `["ipv4", "addr"]`. `codec.JoinWords(words, naming)` joins words under a policy; `codec.WireName(name, naming)` also preserves the original text under `Verbatim`. The derived helpers `NamingOf[T]`, `FieldWireName[T]`, and `VariantWireName[T]` use the same rules and typed tags. `EnumShape[T]` identifies the payload-free sealed shape.

Derivation rejects duplicate canonical names within a record or variant namespace, empty names, the YAML merge key `<<`, and a named sealed payload field renamed to the discriminator `type`. Variant names must parse as unchanged YAML string scalars: `true`, `null`, and numeric spellings fail, while YAML 1.2 string spellings such as `ON` are allowed. Derived decode errors use canonical field names, including errors from checked builders. Map keys and tuple indices retain their existing representation.


## Tuples

Tuples encode as arrays of exactly their declared length. Element failures carry
an index path; a length mismatch applies to the whole tuple.

```bork
import "bork/codec"
import "bork/json"
use codec.Defaults

fn main() {
  println(json.Encode((3, "count")))
  for (text in ["[3]", "[3, false]"]) {
    match (json.Decode[(Int, String)](text)) {
      pair: (Int, String) => println(pair)
      error: codec.DecodeError => println(error.path + ": " + error.message)
      error: json.JsonError => println(error.message)
    }
  }
}
```

The first input has too few elements; the second has the wrong type at index 1:

```text
[3,"count"]
: expected an array of length 2
[1]: expected a string, found a boolean
```

## Decoder metadata

Field adapters need more than a value decoder. The selected decoder can publish `metadata codec.FieldSchema = ...`; the schema kind is `string`, `number`, `bool`, `list:` followed by the element kind, or `json`, plus an optional flag.

| Signature/type | Meaning |
| --- | --- |
| `codec.FieldInfo[T: codec.Decode](): codec.FieldSchema` | Read metadata from the actual selected decoder. |
| `codec.FieldSchema` | `{ kind: String, optional: Bool }` |
| `codec.Schema[T: codec.Decode](): Option[codec.RecordSchema]` | Read the selected decoder's record metadata, or None. |
| `codec.RecordSchema` | `{ fields: List[codec.RecordField] }` |
| `codec.RecordField` | `name: String`, `wireName: String`, `typeName: String`, `doc: String`, `facts: List[String]`, `kind: String`, `optional: Bool`, `hasDefault: Bool`, `defaultValue: Option[() => codec.DefaultSchema]`, `validate: (codec.Value) => Ok \| codec.DecodeError` |
| `codec.DefaultSchema` | `{ display: String, configPath: Option[String], choices: List[String] }` |

Container metadata delegates to its selected element decoder. Missing field metadata uses the general `json` kind and a required input. Record schemas describe named fields, typed validation and optional default-display callbacks. Looking up the schema does not evaluate defaults. A field's defaultValue callback returns display text, an optional configuration path and choices from one default evaluation.

Field validation checks independent facts; facts involving sibling fields require complete `codec.decode[T]`. CLI, environment, HTTP field adapters and CSV read this metadata. Tuple constraints capturing caller values use typed helper parameters with ordinary lifetime checks and pure predicate callbacks. See [the Go schema helpers](../std-go.md) for integration details.

`codec.RecordField.name` is the source field name; `wireName` is its canonical codec key.
