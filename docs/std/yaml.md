# bork/yaml

`bork/yaml` reads and writes YAML. A parsed document is a
[bork/codec](codec.md) `codec.Value`, so the same queries and the same
`codec.Decode` and `codec.Encode` instances work for YAML and JSON. The
package wraps [go.yaml.in/yaml/v4](https://github.com/yaml/go-yaml), the YAML
organization's continuation of gopkg.in/yaml.

```bork
import codec "bork/codec"
import "bork/yaml"
use codec.Defaults

type Server = { host: String, port: Int } derive (codec.Decode, codec.Encode)

fn main() {
  match (yaml.Decode[Server]("host: localhost\nport: 8080\n")) {
    server: Server => println(yaml.Encode(Server { host: server.host, port: 9090 }))
    error: yaml.Error => println(s"line ${error.line}, column ${error.column}: ${error.message}")
    error: codec.DecodeError => println(s"${error.path}: ${error.message}")
  }
}
```

## Reading

| Function | Result |
| --- | --- |
| `Parse(text)` | `codec.Value \| yaml.Error`: one document. Text with no document is `Null`; a second document is an error. |
| `ParseAll(text)` | `List[codec.Value] \| yaml.Error`: every document of a stream, in order. |
| `ReadFile(path)` | `codec.Value \| yaml.Error \| IoError`, as Parse. Uses io. |
| `ReadAllFile(path)` | `List[codec.Value] \| yaml.Error \| IoError`, as ParseAll. Uses io. |

`yaml.Error` has `path` (the file, or `""` for text), one-based `line` and
`column` (0 when no position applies), and `message`.

YAML values become codec values like this:

- Mappings become objects that keep their key order. Keys must be scalars, and
  a key's name is its text as written: `1: x` has the key `"1"`, and `0x10`
  stays `"0x10"`. Keys are compared by that text, so `1` and `"1"` are the same
  key, while `0x10` and `16` are different ones. A key's tag is checked but
  not its value, so `!!int abc: 1` is the key `"abc"`. A key that appears
  twice is an error.
- Integers become numbers in decimal, so `0x1F` is `31`, `0o17` is `15`, `017`
  is `17` and large integers stay exact. Underscores between digits are
  ignored (`1_000`). Floats keep their text when it is
  valid JSON number text. Other spellings are rewritten through a 64-bit float,
  so `.5` is `0.5`, `1.` is `1`, and a value too small for 64 bits is `0`
  unless its text is valid JSON. `.inf`, `.nan` and floats too large for 64
  bits are errors, since codec numbers are finite.
- `true`/`false` are booleans, `null`, `~` and empty values are `Null`, and
  timestamps stay strings. YAML 1.2 rules apply, so `yes` and `on` are strings.
- Aliases are copies of their anchored value. Merge keys (`<<: *defaults` or
  `<<: [*a, *b]`) are supported: the mapping's own keys win, then earlier
  merged mappings. Merged fields appear where the `<<` key is, and a mapping
  may have only one `<<` key. `<<: {}` merges nothing; `<<: []` is an error.

Some YAML is rejected rather than guessed at:

- Tags other than the core ones (`!!str`, `!!int`, `!!float`, `!!bool`,
  `!!null`, `!!seq`, `!!map`, `!!timestamp`) are errors. That includes
  `!!binary`, `!!set` and application tags such as `!vault`.
- An alias inside the value it refers to is an error.
- Alias expansion is bounded, which guards against "billion laughs" input.
  Aliases may add at most 10,000 values to a document, or ten times the
  document's own node count if that is larger.

## Typed values

| Function | Result |
| --- | --- |
| `Decode[T](text)` | `T \| yaml.Error \| codec.DecodeError`: Parse, then T's Decode instance. |
| `DecodeAll[T](text)` | `List[T] \| yaml.Error \| codec.DecodeError`: every document. A DecodeError path starts with the document's index (`[1].port`). |
| `DecodeFile[T](path)` | `T \| yaml.Error \| IoError \| codec.DecodeError`: ReadFile, then decode. Uses io. |
| `Encode(value, spaces = 2)` | `String \| yaml.Error`: T's Encode instance, then Render. |
| `EncodeFile(path, value, spaces = 2)` | `Ok \| yaml.Error \| IoError`: Encode, then WriteFile. Uses io. |

These use the same `codec.Decode` and `codec.Encode` instances as
[bork/json](json.md), so `derive (codec.Decode, codec.Encode)` records, sealed
types, `Option`, `List`, and `Map[String, V]` work the same way, and decoding
checks where clauses. A `codec.DecodeError` has the path of the value
(`.servers[2].port`), but not its line or file. Text with no document decodes
as `Null`, so it is `None` for an `Option` and an error for a record. A
trailing `---` starts another, empty document, which DecodeAll decodes as
`Null` too.

Unlike `json.Encode`, `yaml.Encode` can fail: a Float that is NaN or infinite
has no YAML number, so it gives `yaml.Error`. Its text ends with a newline, as
Render's does.

## Writing

| Function | Result |
| --- | --- |
| `Render(value, spaces = 2)` | `String \| yaml.Error`: one block-style document. |
| `RenderAll(values, spaces = 2)` | `String \| yaml.Error`: documents separated by `---` lines. |
| `WriteFile(path, value, spaces = 2)` | `Ok \| yaml.Error \| IoError`: replaces the file with one document. Uses io. |

`spaces` is the indent, 2 through 9. List items under a field start at the
field's own indent (`tags:` then `- a`). Objects keep their field order and
numbers their exact text. Strings that would read back as another kind are
quoted (`'true'`, `'1.5'`), and so are `yes`, `no`, `on` and `off`, which
YAML 1.1 readers take as booleans. Strings with line breaks use literal blocks.
A manually built number with invalid or non-finite text, or an object with a
repeated field name, gives `yaml.Error`. Its message names the value's
location (`invalid number text "1,2" at .items[0]`). Its `path` is the file
for WriteFile and EncodeFile and `""` otherwise, and line and column are 0.
Comments and the original formatting are not kept.

## Queries

`Field(value, name)`, `Index(value, index)` and `At(value, path)` are the
[bork/json](json.md) queries: `At(config, ["servers", 0, "host"])` gives
`Option[codec.Value]`, with `None` for a missing path.

## Examples

[examples/yaml_config](../../examples/yaml_config/main.bork) loads a
configuration file that uses anchors and merge keys with `DecodeFile`, checks
its fields, and writes one environment back out with `Encode`.
