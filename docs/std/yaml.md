# bork/yaml

`bork/yaml` reads and writes YAML. A parsed document is the prelude's `Json`
value, so the same queries and the same `Decode` and `Encode` instances work
for YAML and JSON. The package wraps
[go.yaml.in/yaml/v4](https://github.com/yaml/go-yaml), the YAML
organization's continuation of gopkg.in/yaml.

```bork
import "bork/yaml"

type Server = { host: String, port: Int } derive (Decode)

fn main() {
  match (yaml.Parse("host: localhost\nport: 8080\n")) {
    value: Json => println(decode[Server](value))
    error: yaml.Error => println(s"line ${error.line}, column ${error.column}: ${error.message}")
  }
}
```

## Reading

| Function | Result |
| --- | --- |
| `Parse(text)` | `Json \| yaml.Error`: one document. Text with no document is `Null`; a second document is an error. |
| `ParseAll(text)` | `List[Json] \| yaml.Error`: every document of a stream, in order. |
| `ReadFile(path)` | `Json \| yaml.Error \| IoError`, as Parse. Uses io. |
| `ReadAllFile(path)` | `List[Json] \| yaml.Error \| IoError`, as ParseAll. Uses io. |

`yaml.Error` has `path` (the file, or `""` for text), one-based `line` and
`column` (0 when no position applies), and `message`.

YAML values become Json values like this:

- Mappings become objects that keep their key order. Keys must be scalars and
  use their text, so `1: x` has the key `"1"`. A key that appears twice is an
  error, even when spelled differently (`1` and `"1"`).
- Integers become numbers in decimal, so `0x1F` is `31`, `0o17` is `15` and
  large integers stay exact. Floats keep their text when it is valid JSON
  number text, and are rewritten otherwise (`.5` is `0.5`). `.inf` and `.nan`
  are errors, since Json numbers are finite.
- `true`/`false` are booleans, `null`, `~` and empty values are `Null`, and
  timestamps stay strings. YAML 1.2 rules apply, so `yes` and `on` are strings.
- Aliases are copies of their anchored value. Merge keys (`<<: *defaults`) are
  supported: the mapping's own keys win, then earlier merged mappings.

Some YAML is rejected rather than guessed at:

- Tags other than the core ones (`!!str`, `!!int`, `!!float`, `!!bool`,
  `!!null`, `!!seq`, `!!map`, `!!timestamp`) are errors. That includes
  `!!binary`, `!!set` and application tags such as `!vault`.
- An alias inside the value it refers to is an error.
- Alias expansion is bounded, which guards against "billion laughs" input.
  Aliases may add at most 10,000 values to a document, or ten times the
  document's own node count if that is larger.

## Writing

| Function | Result |
| --- | --- |
| `Render(value, spaces = 2)` | `String \| yaml.Error`: one block-style document. |
| `RenderAll(values, spaces = 2)` | `String \| yaml.Error`: documents separated by `---` lines. |
| `WriteFile(path, value, spaces = 2)` | `Ok \| yaml.Error \| IoError`: replaces the file with one document. Uses io. |

`spaces` is the indent, 2 through 9. Objects keep their field order and numbers
their exact text. Strings that would read back as another kind are quoted
(`'true'`, `'1.5'`), and strings with line breaks use literal blocks. A
manually built number with invalid text, or an object with a repeated field
name, gives `yaml.Error` with the path of the value (`.items[0]`). Comments and
the original formatting are not kept.

## Queries

`Field(value, name)`, `Index(value, index)` and `At(value, path)` are the
[bork/json](json.md) queries: `At(config, ["servers", 0, "host"])` gives
`Option[Json]`, with `None` for a missing path.

## Examples

[examples/yaml_config](../../examples/yaml_config/main.bork) loads a
configuration file that uses anchors and merge keys, decodes environments into
a record with checked fields, and writes one back out.
