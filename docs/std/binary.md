# Binary formats

Import `bork/binary` to read and write fixed-width integers in immutable `Bytes`. Choose `binary.ByteOrder.BigEndian` or `LittleEndian` explicitly. Signed reads and writes preserve the integer's two's-complement bit pattern. All operations are pure.

```bork
import "bork/binary"

fn main() {
  bytes = [toByte(18), toByte(52), toByte(255), toByte(254)].toBytes()
  println(bytes.ReadUint16(0, binary.ByteOrder.BigEndian))
  println(bytes.ReadInt16(2, binary.ByteOrder.BigEndian))
  println(bytes.WriteUint16(0, 22136, binary.ByteOrder.LittleEndian))
  println(bytes)
}
```

`Bytes` methods are `ReadByte(offset)`, `ReadInt8(offset)`, and `ReadUint16`, `ReadUint32`, `ReadUint64`, `ReadInt16`, `ReadInt32`, `ReadInt64` with `(offset, order)`. The result is the integer or `binary.BinaryError`. `Byte` is also `Uint8`; `Int64` is also `Int`.

Matching write methods take `(offset, value)` for one-byte values and `(offset, value, order)` otherwise. They return a new `Bytes` value or `BinaryError` and require existing space for the entire integer. They never resize or mutate the receiver. Offsets need not be aligned.

`BinaryError` has `offset`, `required`, and `length` fields. A read or replacement write requires a nonnegative offset and enough remaining bytes. Bounds checks avoid adding the offset and width before validating them, so negative or extremely large offsets return errors without overflow or panic.

## Immutable readers

`NewReader(data, order)` starts at byte zero. `Offset()` and `Remaining()` return nonnegative byte counts, with the `binary.ByteCount` fact. `Skip(count)` returns a new Reader or BinaryError; negative counts and skipping past the end are errors.

Reader methods `ReadByte`, `ReadInt8`, `ReadUint16`, `ReadUint32`, `ReadUint64`, `ReadInt16`, `ReadInt32`, and `ReadInt64` take no arguments. `ReadBytes(count)` reads an arbitrary number of bytes. Each returns `(value, nextReader) | BinaryError`; use `?` followed by tuple destructuring to advance:

```bork
import "bork/binary"

fn payload(data: Bytes): Bytes | binary.BinaryError {
  reader = binary.NewReader(data, binary.ByteOrder.BigEndian)
  (size, next) = reader.ReadUint16()?
  (body, end) = next.ReadBytes(toInt(size))?
  body
}

fn main() {
  bytes = [toByte(0), toByte(2), toByte(17), toByte(34)].toBytes()
  println(payload(bytes))
}
```

The original reader stays at the same offset, on success or failure. Reading zero bytes succeeds even at the end and returns an empty Bytes plus the same cursor position. A returned cursor shares the immutable input data.

## Immutable writers

`NewWriter(order)` creates an empty writer. Methods `WriteByte`, `WriteInt8`, `WriteUint16`, `WriteUint32`, `WriteUint64`, `WriteInt16`, `WriteInt32`, and `WriteInt64` append one integer. `WriteBytes(data)` appends bytes. Each returns `Writer | BinaryError`; `?` propagates a size error. The result shares immutable earlier chunks, so appending does not repeatedly copy existing bytes.

`Length()` reports the accumulated byte count. `Bytes()` combines the chunks into a new immutable snapshot. Earlier writers and snapshots remain usable after later writes. Writer size is checked against the platform's maximum slice length before addition; a size error uses that limit as the BinaryError's `length`.

```bork
import "bork/binary"

fn packet(): Bytes | binary.BinaryError {
  empty = binary.NewWriter(binary.ByteOrder.BigEndian)
  header = empty.WriteUint16(2)?
  body = header.WriteBytes([toByte(17), toByte(34)].toBytes())?
  body.Bytes()
}

fn main() {
  println(packet())
}
```

Use [bork/bits](bits.md) for integer bit fields and [bork/encoding](encoding.md) for hex, base64, and UTF-8 representations.
