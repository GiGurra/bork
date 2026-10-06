# bork/codec

`bork/codec` separates typed values from text formats. `codec.Value` contains `Null`, `Bool`, `Number`, `String`, `Array`, and `Object`; an object stores an ordered list of `codec.Field { name, value }`. Numbers retain their text so integers can decode exactly.

`codec.Encode[T]` provides `codec.encode(value): codec.Value`. `codec.Decode[T]` provides `codec.decode(value): T | codec.DecodeError`. Errors carry a field/index path and a message. Derived decoders check field facts and record invariants before returning a value.

Import the classes and select the standard instances explicitly:

```bork
import "bork/codec"
import "bork/json"
use codec.Defaults

type User = { name: String, age: Int } derive (codec.Decode, codec.Encode)

fn main() {
  user = User { name: "Ada", age: 37 }
  println(codec.encode(user))
  println(json.Decode[User](json.Encode(user)))
}
```

`codec.Defaults` contains instances for `Int`, `Int8`, `Int16`, `Int32`, `Uint8`, `Uint16`, `Uint32`, `Uint64`, `Float`, `Float32`, `Bool`, `String`, `Rune`, `Option`, `List`, `Map[String, V]`, and `codec.Value`. Numeric-width decoders reject values outside the type’s decimal range. Container instances require the corresponding element instances. Custom codecs remain ordinary selectable instances; another package imports them with `use` or an exported instance bundle.

Records encode as objects. A named sealed variant encodes with a `"type"` discriminator and its fields; a fieldless variant can also decode from its name. `Option` retains its null/value representation, and missing optional record fields decode as `None`.

[bork/json](json.md) parses and renders this tree and supplies typed text adapters. [bork/yaml](yaml.md) does the same for YAML (`yaml.Decode[T]`, `yaml.Encode`), so a type that derives `codec.Decode` and `codec.Encode` reads and writes both formats. JSON syntax errors are `json.JsonError`; typed conversion errors are `codec.DecodeError`. The old prelude `Json`, `JsonField`, `DecodeError`, `JsonError`, `Encode`, and `Decode` names have no compatibility aliases.

Selected decoders can publish `metadata codec.FieldSchema = ...` with a
`kind` (`string`, `number`, `bool`, `list:` followed by an element kind, or
`json`) and `optional` flag. `codec.FieldInfo[T]()` reads that actual instance;
container metadata delegates to its selected element decoder. Missing metadata
uses the general `json` kind and a required input.

`codec.RecordSchema` describes named record inputs with typed field validation
and optional default display callbacks. `codec.Schema[T]()` reads this metadata
from the selected decoder, returning `None` when it has no schema. Field
validation checks independent facts; facts involving siblings require complete
`codec.decode[T]`. Looking up a schema does not evaluate field defaults.
Structural schema and decoding use source derivation templates and checked
builders. CLI, environment, HTTP field adapters and CSV read this typed schema.
A field's optional `defaultValue` callback returns a `codec.DefaultSchema` with
display text, an optional configuration path and string choices from one default
evaluation. Tuple constraints that capture caller values use typed source
helper parameters, with ordinary lifetime checks and pure predicate callbacks.
