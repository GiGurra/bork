# bork/regex

`bork/regex` compiles RE2 regular expressions for matching, capture, replacement, and splitting.

```bork
import "bork/regex"

fn main() {
  re = regex.Compile("^[a-z]+$")
  println(re.Matches("hello"), re.Matches("hello 123"))
  match regex.Parse("[") {
    _: ParseError => println("invalid pattern")
    value: regex.Regex => println(value.Pattern())
  }
}
```

```text
true false
invalid pattern
```

## API

| Signature | Meaning |
| --- | --- |
| `Compile(pattern: String where ValidPattern): Regex` | Compile a proven valid pattern. |
| `Parse(pattern: String): Regex \| ParseError` | Compile dynamic text or return ParseError. |
| `(re: Regex) Pattern(): String` | Read the original pattern. |
| `(re: Regex) Matches(text: String): Bool` | Test for a match anywhere in the input. |
| `(re: Regex) Find(text: String): Option[Match]` | Find the first match, if present. |
| `(re: Regex) FindAll(text: String, limit: Int = -1): List[Match]` | Find matches, bounded by limit. |
| `(re: Regex) Replace(text: String, replacement: String): String` | Replace matches, expanding capture references. |
| `(re: Regex) ReplaceLiteral(text: String, replacement: String): String` | Replace matches with literal replacement text. |
| `(re: Regex) Split(text: String, limit: Int = -1): List[String]` | Split around matches, bounded by limit. |

`Regex` has a private representation. `Match` has these fields:

| Field | Meaning |
| --- | --- |
| `value: String` | The whole matched substring. |
| `start: Int`, `end: Int` | Zero-based Unicode code-point offsets into the input; end is exclusive. |
| `groups: List[Option[String]]` | Numbered captures, excluding the whole match. |
| `named: Map[String, Option[String]]` | Named captures. |

`ValidPattern(pattern: String): Bool` proves a pattern compiles.
`Matches(text: String, re: Regex): Bool` proves a string matches the expression.
All operations are pure. Offsets use the same units as `String.substring`, `indexOf` and `runeAt`; they count code points rather than grapheme clusters.

```bork
import "bork/regex"

fn main() {
  text = "🙂åbc"
  match regex.Compile("b").Find(text) {
    Option.Some(found) => println(found.start, text.substring(found.start, found.end))
    Option.None => {}
  }
}
```

```text
2 b
```

## Patterns and captures

`bork/regex` compiles Go's RE2 patterns into immutable Regex values.
`Compile(pattern: String where ValidPattern)` validates literal patterns through
compile-time predicate evaluation; malformed patterns are compile errors.
Dynamic inputs use `Parse(pattern): Regex | ParseError`, or prove `ValidPattern`
before calling Compile. All operations are pure, and compiled expressions are
shared by their value's private callbacks without exposing mutable Go state.
Matches searches anywhere; use anchors to require a whole-string match.
Match contains `value`, Unicode code-point offsets `start`/`end`, numbered `groups`
(excluding the whole match), and `named: Map[String, Option[String]]`.
An unmatched group is None, while a matched empty group is Some(""). Duplicate
capture names retain the last group's value. Replace expands `$name`, `${name}`,
and numbered references; ReplaceLiteral preserves replacement text literally.
Negative limits return all results, zero returns none, and positive limits bound
matches or split substrings. Empty matches and splitting follow Go regexp's
semantics. The predicate `regex.Matches(text, re)` supports dependent String
requirements such as `text: String where regex.Matches(re)`; test the predicate
to establish the fact. See [the regex example](../../examples/regex/main.bork).


## Carry a matching fact

```bork
import "bork/regex"

fn accepted(re: regex.Regex, text: String where regex.Matches(re)): String { text }

fn main() {
  re = regex.Compile("^[a-z]+$")
  text = "hello"
  if regex.Matches(text, re) { println(accepted(re, text)) }
}
```

```text
hello
```

## Replace text

```bork
import "bork/regex"

fn main() {
  re = regex.Compile("(?P<word>[a-z]+)")
  println(re.Replace("hello 123", "${word}!"))
  println(re.ReplaceLiteral("hello 123", "$word"))
}
```

```text
hello! 123
$word 123
```

RE2 matching runs in linear time; backreferences and lookaround are unsupported.
`Compile("[")` fails at compile time; `Parse("[")` returns `ParseError`.
Use `Parse` for dynamic patterns or establish `ValidPattern` before `Compile`.
See the [regex example](../../examples/regex/main.bork).


Run `bork doc bork/regex` for the generated reference.

[All standard packages](README.md)
