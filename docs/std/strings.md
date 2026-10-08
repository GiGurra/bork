# String indexing

String methods are available without imports. Ordinary indexes count Unicode
code points, starting at zero. Search results compose with slicing and rune
access:

```bork
fn main() {
  text = "🙂åbc"
  match (text.indexOf("b")) {
    Option.Some(index) => println(index, text.substring(index, index + 1), text.runeAt(index))
    Option.None => {}
  }
}
```

```text
2 b Option.Some(b)
```

| Method | Units and bounds |
| --- | --- |
| `runeCount(): Int` | Number of Unicode code points. |
| `indexOf(part: String): Option[Int]` | First matching code-point offset; None if absent; empty part gives Some(0). |
| `runeAt(index: Int): Option[Rune]` | Code point at index; None for a negative or out-of-range index. |
| `substring(from: Int, to: Int): String \| OutOfRange` | Code points from from to the exclusive to; requires `0 <= from <= to <= runeCount()`. |
| `byteLength(): Int` | UTF-8 byte length. |
| `byteIndexOf(part: String): Option[Int]` | First matching byte offset; None if absent; empty part gives Some(0). |
| `byteSubstring(from: Int, to: Int): String \| OutOfRange` | Bytes from from to the exclusive to; requires `0 <= from <= to <= byteLength()`. |

[Regex](regex.md) match fields `start` and `end` also count code points, with an
exclusive end. `text.substring(found.start, found.end)` extracts the match.

Code points are not grapheme clusters. A letter followed by a combining accent
counts as two code points even when displayed as one character; emoji sequences
can contain several code points. These methods do not normalize Unicode text.

The `byte*` methods support byte-oriented interop. Use `byteIndexOf` results
with `byteSubstring`, and `indexOf` results with `substring`. Cutting through a
code point with `byteSubstring` can produce invalid UTF-8. Prefer code-point
methods for text.

Before code-point indexing, `indexOf` and regex offsets counted UTF-8 bytes.
Callers that require the old String search unit should use `byteIndexOf` and
`byteSubstring`. For regex interop, convert a code-point prefix to its byte
length: `text.substring(0, found.start)` followed by `byteLength()` on the
successful String result.

Run `bork doc builtin` for all String method signatures.

[All standard packages](README.md)
