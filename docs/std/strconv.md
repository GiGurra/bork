# bork/strconv

`bork/strconv` formats and parses fixed-width integers in bases 2 through 36.

```bork
import "bork/strconv"

fn main() {
  println(255.Hex(width: 4, uppercase: true, prefix: true))
  println("-0x_80".ParseInt8(base: 0))
  match ("256".ParseByte()) {
    _: ParseError => println("outside byte range")
    value: Byte => println(value)
  }
}
```

```text
0X00FF
-128
outside byte range
```

## API

These pure methods exist for each integer receiver type `I`: `Int8`, `Int16`,
`Int32`, `Int` (`Int64`), `Byte` (`Uint8`), `Uint16`, `Uint32`, and `Uint64`.
Here `I` denotes one of those concrete receiver types, not a type to import.

| Signature | Meaning |
| --- | --- |
| `(value: I) Hex(width: Int where ValidWidth = 0, uppercase: Bool = false, prefix: Bool = false): String` | Format hexadecimal digits with optional 0x. |
| `(value: I) Binary(width: Int where ValidWidth = 0, uppercase: Bool = false, prefix: Bool = false): String` | Format binary digits with optional 0b. |
| `(value: I) Octal(width: Int where ValidWidth = 0, uppercase: Bool = false, prefix: Bool = false): String` | Format octal digits with optional 0o. |
| `(value: I) Format(base: Int where ValidBase, width: Int where ValidWidth = 0, uppercase: Bool = false): String` | Format digits without a prefix. |
| `(text: String) ParseInt(base: Int where ValidBaseOrZero = 10): Int \| ParseError` | Parse Int/Int64. |
| `(text: String) ParseInt8(base: Int where ValidBaseOrZero = 10): Int8 \| ParseError` | Parse Int8. |
| `(text: String) ParseInt16(base: Int where ValidBaseOrZero = 10): Int16 \| ParseError` | Parse Int16. |
| `(text: String) ParseInt32(base: Int where ValidBaseOrZero = 10): Int32 \| ParseError` | Parse Int32. |
| `(text: String) ParseUint8(base: Int where ValidBaseOrZero = 10): Uint8 \| ParseError` | Parse Uint8. |
| `(text: String) ParseByte(base: Int where ValidBaseOrZero = 10): Byte \| ParseError` | Alias of ParseUint8. |
| `(text: String) ParseUint16(base: Int where ValidBaseOrZero = 10): Uint16 \| ParseError` | Parse Uint16. |
| `(text: String) ParseUint32(base: Int where ValidBaseOrZero = 10): Uint32 \| ParseError` | Parse Uint32. |
| `(text: String) ParseUint64(base: Int where ValidBaseOrZero = 10): Uint64 \| ParseError` | Parse Uint64. |
| `ValidBase(base: Int): Bool` | Prove base is 2–36. |
| `ValidBaseOrZero(base: Int): Bool` | Prove base is 0 or 2–36. |
| `ValidWidth(width: Int): Bool` | Prove width is 0–4096. |

## Format integers

`base: Int where strconv.ValidBase` requires a proof that
`2 <= base <= 36`. `width: Int where strconv.ValidWidth` requires a proof that
`0 <= width <= 4096`. Constants are checked at compile time; guard dynamic values
with these predicates. Invalid options are compile errors unless guarded.

Width is the minimum digit count. Zeroes go before the digits, after any sign
and prefix; width never truncates. Negative integers use a minus sign and their
magnitude, including the smallest signed value. Unsigned integers retain their
full range. Digits are `0` through `9`, then `a` through `z`; `uppercase` changes
letters and prefixes to uppercase.

```bork
import "bork/strconv"

fn main() uses io {
  println(255.Hex(width: 4, uppercase: true, prefix: true)) // 0X00FF
  println((-5).Binary(width: 8, prefix: true)) // -0b00000101
  println(63.Octal(prefix: true)) // 0o77
  println(35.Format(36)) // z
}
```

## Parsing

Strings have `ParseInt`, `ParseInt8`, `ParseInt16`, `ParseInt32`, `ParseUint8`,
`ParseUint16`, `ParseUint32`, `ParseUint64`, and `ParseByte` methods. Each returns
the named integer type or `ParseError`, never a partial or wrapped value.
`ParseInt` covers `Int64` too; `ParseByte` is equivalent to `ParseUint8`.

All take `base: Int where strconv.ValidBaseOrZero = 10`. Base zero detects
`0b`, `0o`, and `0x` prefixes (either case), otherwise it uses decimal.
Bare leading zeroes remain decimal: `"077".ParseInt(0)` is 77.
An explicit base strips a matching prefix. A prefix-looking sequence that does
not match that base is ordinary digits: `"0b10".ParseInt(16)` is 2832 and
`"0x".ParseInt(36)` is 33. This lets padded arbitrary-base output round-trip.

Signed parsing accepts `+` or `-`. Unsigned parsing accepts `+`, rejects `-`
(including `-0`), and supports the full `Uint64` range. Letters are case
insensitive. An underscore may separate digits, or occur once immediately
after a recognized prefix. Leading, trailing, and repeated separators, missing
digits, whitespace, invalid digits, and overflow return `ParseError`; its
`input` is the original string. Use `encoding.ParseInt(text)` for strict decimal parsing without separator
support.

```bork
import "bork/strconv"

fn main() uses io {
  match ("-0x_80".ParseInt8(base: 0)) {
    value: Int8 => println(value) // -128
    error: ParseError => println(error.message)
  }
  match ("18446744073709551615".ParseUint64()) {
    value: Uint64 => println(value.Hex()) // ffffffffffffffff
    error: ParseError => println(error.message)
  }
}
```

For a dynamic base, check `strconv.ValidBaseOrZero(base)` before parsing.

## Bytes and hex

Integer formatting prints a number's magnitude. For a byte sequence use
[`bork/encoding`](encoding.md): `encoding.Hex(data)` prints two lowercase hex
digits per byte, and `encoding.ParseHex(text): Bytes | ParseError` accepts either
case and requires complete pairs. Bytes hex has no sign, prefix, or separators;
leading zero bytes are preserved.


Run `bork doc bork/strconv` for the generated reference.

[All standard packages](README.md)
