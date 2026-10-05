# Integer bits

Import `bork/bits` to use these methods on `Int8`, `Int16`, `Int32`, `Int` (`Int64`), `Byte` (`Uint8`), `Uint16`, `Uint32`, and `Uint64`. Operations are pure. Signed values use their fixed-width two's-complement bit patterns. Bit zero is the least significant bit.

```bork
import "bork/bits"

fn main() {
  flags: Byte = 240
  println(flags.OnesCount(), flags.LeadingZeros(), flags.TrailingZeros())
  println(flags.RotateLeft(2), flags.Reverse(), flags.ReverseBytes())
  println(flags.TestBit(7), flags.ClearBit(7), flags.SetBit(0))
  println(flags.Extract(4, 4), flags.Insert(0, 4, 5))
}
```

| Method | Result and behavior |
| --- | --- |
| `OnesCount()` | `Int`: number of one bits |
| `LeadingZeros()` | `Int`: zero bits before the highest one bit; the full width for zero |
| `TrailingZeros()` | `Int`: zero bits after the lowest one bit; the full width for zero |
| `RotateLeft(count)` | Same integer type: rotate left; a negative count rotates right; counts wrap modulo the width |
| `Reverse()` | Same integer type: reverse the order of every bit |
| `ReverseBytes()` | Same integer type: reverse byte order; an 8-bit value stays the same |
| `TestBit(index)` | `Bool | OutOfRange`: test a bit |
| `SetBit(index)`, `ClearBit(index)` | Same integer type or `OutOfRange`: return a value with that bit set or cleared |
| `Extract(offset, width)` | Same integer type or `OutOfRange`: move the field to the lowest bits and clear the others |
| `Insert(offset, width, field)` | Same integer type or `OutOfRange`: replace the field, preserving the other bits |

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
