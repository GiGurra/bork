# bork/regex

`bork/regex` compiles Go's RE2 patterns into immutable Regex values.
`Compile(pattern: String where ValidPattern)` validates literal patterns through
compile-time predicate evaluation; malformed patterns are compile errors.
Dynamic inputs use `Parse(pattern): Regex | ParseError`, or prove `ValidPattern`
before calling Compile. All operations are pure, and compiled expressions are
shared by their value's private callbacks without exposing mutable Go state.
Methods are `Pattern`, `Matches(text)`, `Find(text): Option[Match]`,
`FindAll(text, limit = -1)`, `Replace(text, replacement)`,
`ReplaceLiteral(text, replacement)`, and `Split(text, limit = -1)`.
Matches searches anywhere; use anchors to require a whole-string match.
Match contains `value`, UTF-8 byte offsets `start`/`end`, numbered `groups`
(excluding the whole match), and `named: Map[String, Option[String]]`.
An unmatched group is None, while a matched empty group is Some(""). Duplicate
capture names retain the last group's value. Replace expands `$name`, `${name}`,
and numbered references; ReplaceLiteral preserves replacement text literally.
Negative limits return all results, zero returns none, and positive limits bound
matches or split substrings. Empty matches and splitting follow Go regexp's
semantics. The predicate `regex.Matches(text, re)` supports dependent String
requirements such as `text: String where regex.Matches(re)`; test the predicate
to establish the fact. See [the regex example](../../examples/regex/main.bork).

## Example

```bork
import "bork/regex"

fn accepted(re: regex.Regex, text: String where regex.Matches(re)): String { text }
fn main() {
  re = regex.Compile("^[a-z]+$")
  text = "hello"
  if (regex.Matches(text, re)) { println(accepted(re, text)) }
  println(re.Find("hello 123"))
}
```

Go's RE2 matching runs in linear time; backreferences and lookaround are
unsupported. `Compile("[")` fails at compile time, while `Parse("[")` returns
ParseError. A successful Parse gives the same compiled Regex methods as Compile.
