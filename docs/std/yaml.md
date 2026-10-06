# bork/yaml

`bork/yaml` reads and writes YAML through the same checked codec instances as JSON.

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

Output from the first example:

```text
host: localhost
port: 9090
```

A YAML syntax/mapping failure is `yaml.Error`; a value that does not fit T produces `codec.DecodeError`. The parsed document is a [codec.Value](codec.md). The package uses the YAML organization's [go.yaml.in/yaml/v4](https://github.com/yaml/go-yaml).

## Reading

| Signature | Meaning |
| --- | --- |
| `Parse(text: String): codec.Value \| yaml.Error` | One document. Text with no document is `Null`; a second document is an error. |
| `ParseAll(text: String): List[codec.Value] \| yaml.Error` | Every document of a stream, in order. |
| `ReadFile(path: String) uses io: codec.Value \| yaml.Error \| IoError` | Read a file as one document. |
| `ReadAllFile(path: String) uses io: List[codec.Value] \| yaml.Error \| IoError` | Read every document in a file. |

`yaml.Error` has `{ path: String, line: Int, column: Int, message: String }`.
The path is the file, or `""` for text; line and column are one-based, or 0
when no position applies.

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

| Signature | Meaning |
| --- | --- |
| `Decode[T: codec.Decode](text: String): T \| yaml.Error \| codec.DecodeError` | Parse, then T's Decode instance. |
| `DecodeAll[T: codec.Decode](text: String): List[T] \| yaml.Error \| codec.DecodeError` | Decode every document. A DecodeError path starts with the document's index (`[1].port`). |
| `DecodeFile[T: codec.Decode](path: String) uses io: T \| yaml.Error \| IoError \| codec.DecodeError` | ReadFile, then decode. |
| `Encode[T: codec.Encode](value: T, spaces: Indent = 2): String \| yaml.Error` | T's Encode instance, then Render. |
| `EncodeFile[T: codec.Encode](path: String, value: T, spaces: Indent = 2) uses io: Ok \| yaml.Error \| IoError` | Encode, then WriteFile. |

These use the same `codec.Decode` and `codec.Encode` instances as
[bork/json](json.md), so `derive (codec.Decode, codec.Encode)` records, sealed
types, `Option`, `List`, and `Map[String, V]` work the same way, and decoding
checks where clauses. A `codec.DecodeError` has the path of the value
(`.servers[2].port`), but not its line or file. Text with no document decodes
as `Null`, so it is `None` for an `Option` and an error for a record. A
trailing `---` starts another, empty document, which DecodeAll decodes as
`Null` too.

Derived record keys and variant names follow the [codec naming policies and typed tags](codec.md#wire-names). An enum with no payload alternatives emits a bare canonical string, such as `SITE_ADMIN` for `SiteAdmin` under the default `ScreamingSnake` policy. Decode accepts that scalar or `type: SITE_ADMIN`; source spelling needs an explicit `Verbatim` policy. Mixed sealed types keep their discriminator mappings. Derived variant names must be unchanged YAML string scalars, so names such as `true`, `null`, and `42` fail derivation.

Unlike `json.Encode`, `yaml.Encode` can fail: a Float that is NaN or infinite
has no YAML number, so it gives `yaml.Error`. Its text ends with a newline, as
Render's does.

## Writing

| Signature | Meaning |
| --- | --- |
| `Render(value: codec.Value, spaces: Indent = 2): String \| yaml.Error` | One block-style document. |
| `RenderAll(values: List[codec.Value], spaces: Indent = 2): String \| yaml.Error` | Documents separated by `---` lines. |
| `WriteFile(path: String, value: codec.Value, spaces: Indent = 2) uses io: Ok \| yaml.Error \| IoError` | Replace the file with one document. |

`Indent` is an Int in 2 through 9; `spaces` sets that indentation. List items under a field start at the
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

The pure query functions mirror [bork/json](json.md):

| Signature | Meaning |
| --- | --- |
| `Field(value: codec.Value, name: String): Option[codec.Value]` | First matching object field. |
| `Index(value: codec.Value, index: Int): Option[codec.Value]` | Array element. |
| `At(value: codec.Value, path: List[String \| Int]): Option[codec.Value]` | Follow keys and indices; None for a missing path. |

For example, `At(config, ["servers", 0, "host"])` selects the first server's host.

## Handle a failure

```bork
import "bork/codec"
import "bork/yaml"
use codec.Defaults

type Config = { port: Int } derive (codec.Decode)

fn main() {
  match (yaml.Decode[Config]("port: many\n")) {
    config: Config => println(config.port)
    error: codec.DecodeError => println(error.path + ": " + error.message)
    error: yaml.Error => println(error.message)
  }
}
```

Output:

```text
.port: expected a whole number, found a string
```

## Examples

[examples/yaml_config](../../examples/yaml_config/main.bork) loads a
configuration file that uses anchors and merge keys with `DecodeFile`, checks
its fields, and writes one environment back out with `Encode`.
