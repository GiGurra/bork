# bork/bits

`bork/bits` counts, rearranges, and edits bits in fixed-width integers.

```bork
import "bork/bits"

fn main() {
  flags: Byte = 240
  println(flags.OnesCount(), flags.LeadingZeros(), flags.TrailingZeros())
  println(flags.Extract(4, 4))
  match flags.TestBit(8) {
    _: OutOfRange => println("bit index outside byte")
    value: Bool => println(value)
  }
}
```

```text
4 0 4
15
bit index outside byte
```

## API

All methods are pure. They exist for each concrete receiver type `I`: `Int8`,
`Int16`, `Int32`, `Int` (`Int64`), `Byte` (`Uint8`), `Uint16`, `Uint32`, and
`Uint64`. In this table `I` denotes that receiver type. `CountN` is `Count8`,
`Count16`, `Count32`, or `Count64` according to its width.

| Signature | Meaning |
| --- | --- |
| `(value: I) OnesCount(): Int where CountN` | Count one bits. |
| `(value: I) LeadingZeros(): Int where CountN` | Count zeros before the highest one bit; full width for zero. |
| `(value: I) TrailingZeros(): Int where CountN` | Count zeros after the lowest one bit; full width for zero. |
| `(value: I) RotateLeft(count: Int): I` | Rotate left; negative rotates right; counts wrap modulo width. |
| `(value: I) Reverse(): I` | Reverse all bits. |
| `(value: I) ReverseBytes(): I` | Reverse byte order; unchanged for 8-bit values. |
| `(value: I) TestBit(index: Int): Bool \| OutOfRange` | Inspect one bit. |
| `(value: I) SetBit(index: Int): I \| OutOfRange` | Return a value with one bit set. |
| `(value: I) ClearBit(index: Int): I \| OutOfRange` | Return a value with one bit cleared. |
| `(value: I) Extract(offset: Int, width: Int): I \| OutOfRange` | Move a field to the lowest bits and clear the rest. |
| `(value: I) Insert(offset: Int, width: Int, field: I): I \| OutOfRange` | Replace a field without truncating its value. |

Signed values use their fixed-width two's-complement patterns. Bit zero is the
least significant bit. `Count8`, `Count16`, `Count32`, and `Count64` are predicates
on Int, each proving a count from zero through that width inclusive.

## Replace a bit field

```bork
import "bork/bits"

fn main() {
  original: Byte = 240
  println(original.Insert(0, 4, 5))
  println(original)
}
```

```text
245
240
```

Every result is a new value; the receiver is unchanged.

Indices are `Int` values in `[0, integer width)`. Fields require nonnegative offset and width and must fit within the integer. A zero-width field is allowed, including at an offset equal to the integer width: extraction returns zero and inserting zero returns the original value. Bounds checks handle even the largest `Int` values without overflow.

`Insert` requires `field` to have the receiver's integer type and rejects a field value whose bits do not fit the requested width. It never silently truncates that value. A negative signed field fits only a full-width field. A full-width signed extraction preserves the original sign bit; a narrower extraction is nonnegative.

Count methods promise `bits.Count8`, `Count16`, `Count32`, or `Count64`, according to the receiver width. Each predicate means the result is between zero and that width, inclusive, so functions can preserve the fact:

```bork
import "bork/bits"

fn population(value: Byte): Int where bits.Count8 {
  value.OnesCount()
}
```

[Integer operators](../language/basics.md) provide AND, OR, XOR, complement, and shifts. [bork/encoding](encoding.md) handles hex and base64 representations of Bytes.


Run `bork doc bork/bits` for the generated reference.

[All standard packages](README.md)
