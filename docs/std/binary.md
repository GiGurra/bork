# bork/binary

`bork/binary` reads and writes fixed-width binary formats with immutable buffers and cursors.

```bork
import "bork/binary"

fn main() {
  data = [toByte(18), toByte(52)].toBytes()
  println(data.ReadUint16(0, binary.ByteOrder.BigEndian))
  match (data.ReadUint32(0, binary.ByteOrder.BigEndian)) {
    error: binary.BinaryError => println(s"need ${error.required} bytes; have ${error.length}")
    value: Uint32 => println(value)
  }
}
```

```text
4660
need 4 bytes; have 2
```

## API

| Signature | Meaning |
| --- | --- |
| `(data: Bytes) ReadByte(offset: Int): Byte \| BinaryError` | Read one byte. |
| `(data: Bytes) WriteByte(offset: Int, value: Byte): Bytes \| BinaryError` | Replace one byte in a copied buffer. |
| `(data: Bytes) ReadUint16(offset: Int, order: ByteOrder): Uint16 \| BinaryError` | Read an unsigned 16-bit integer. |
| `(data: Bytes) WriteUint16(offset: Int, value: Uint16, order: ByteOrder): Bytes \| BinaryError` | Write an unsigned 16-bit integer. |
| `(data: Bytes) ReadUint32(offset: Int, order: ByteOrder): Uint32 \| BinaryError` | Read an unsigned 32-bit integer. |
| `(data: Bytes) WriteUint32(offset: Int, value: Uint32, order: ByteOrder): Bytes \| BinaryError` | Write an unsigned 32-bit integer. |
| `(data: Bytes) ReadUint64(offset: Int, order: ByteOrder): Uint64 \| BinaryError` | Read an unsigned 64-bit integer. |
| `(data: Bytes) WriteUint64(offset: Int, value: Uint64, order: ByteOrder): Bytes \| BinaryError` | Write an unsigned 64-bit integer. |
| `(data: Bytes) ReadInt16(offset: Int, order: ByteOrder): Int16 \| BinaryError` | Read a signed 16-bit integer. |
| `(data: Bytes) WriteInt16(offset: Int, value: Int16, order: ByteOrder): Bytes \| BinaryError` | Write a signed 16-bit integer. |
| `(data: Bytes) ReadInt32(offset: Int, order: ByteOrder): Int32 \| BinaryError` | Read a signed 32-bit integer. |
| `(data: Bytes) WriteInt32(offset: Int, value: Int32, order: ByteOrder): Bytes \| BinaryError` | Write a signed 32-bit integer. |
| `(data: Bytes) ReadInt64(offset: Int, order: ByteOrder): Int \| BinaryError` | Read a signed 64-bit integer. |
| `(data: Bytes) WriteInt64(offset: Int, value: Int, order: ByteOrder): Bytes \| BinaryError` | Write a signed 64-bit integer. |
| `(data: Bytes) ReadInt8(offset: Int): Int8 \| BinaryError` | Read a signed byte. |
| `(data: Bytes) WriteInt8(offset: Int, value: Int8): Bytes \| BinaryError` | Write a signed byte. |
| `NewReader(data: Bytes, order: ByteOrder): Reader` | Start a cursor at offset zero. |
| `(value: Reader) Offset(): Int where ByteCount` | Read the cursor offset. |
| `(value: Reader) Remaining(): Int where ByteCount` | Count bytes after the cursor. |
| `(value: Reader) Skip(count: Int): Reader \| BinaryError` | Return a cursor advanced by count. |
| `(value: Reader) ReadBytes(count: Int): (Bytes, Reader) \| BinaryError` | Read count bytes and a new cursor. |
| `(value: Reader) ReadByte(): (Byte, Reader) \| BinaryError` | Read one byte. |
| `(value: Reader) ReadInt8(): (Int8, Reader) \| BinaryError` | Read a signed byte. |
| `(value: Reader) ReadUint16(): (Uint16, Reader) \| BinaryError` | Read an unsigned 16-bit integer. |
| `(value: Reader) ReadUint32(): (Uint32, Reader) \| BinaryError` | Read an unsigned 32-bit integer. |
| `(value: Reader) ReadUint64(): (Uint64, Reader) \| BinaryError` | Read an unsigned 64-bit integer. |
| `(value: Reader) ReadInt16(): (Int16, Reader) \| BinaryError` | Read a signed 16-bit integer. |
| `(value: Reader) ReadInt32(): (Int32, Reader) \| BinaryError` | Read a signed 32-bit integer. |
| `(value: Reader) ReadInt64(): (Int64, Reader) \| BinaryError` | Read a signed 64-bit integer. |
| `NewWriter(order: ByteOrder): Writer` | Create an empty writer. |
| `(value: Writer) Length(): Int` | Read accumulated byte count. |
| `(value: Writer) WriteBytes(data: Bytes): Writer \| BinaryError` | Append an immutable chunk. |
| `(value: Writer) Bytes(): Bytes` | Materialize a snapshot of all chunks. |
| `(value: Writer) WriteByte(number: Byte): Writer \| BinaryError` | Append one byte. |
| `(value: Writer) WriteInt8(number: Int8): Writer \| BinaryError` | Write a signed byte. |
| `(value: Writer) WriteUint16(number: Uint16): Writer \| BinaryError` | Write an unsigned 16-bit integer. |
| `(value: Writer) WriteUint32(number: Uint32): Writer \| BinaryError` | Write an unsigned 32-bit integer. |
| `(value: Writer) WriteUint64(number: Uint64): Writer \| BinaryError` | Write an unsigned 64-bit integer. |
| `(value: Writer) WriteInt16(number: Int16): Writer \| BinaryError` | Write a signed 16-bit integer. |
| `(value: Writer) WriteInt32(number: Int32): Writer \| BinaryError` | Write a signed 32-bit integer. |
| `(value: Writer) WriteInt64(number: Int): Writer \| BinaryError` | Write a signed 64-bit integer. |

`ByteOrder` is `BigEndian | LittleEndian`; choose one explicitly. Signed
reads and writes preserve two's-complement patterns. `Reader` and `Writer` have
private representations; use `NewReader` and `NewWriter`. `ByteCount(value: Int)`
proves a nonnegative Int. Every operation is pure.

## Read and replace bytes

`Bytes` methods are `ReadByte(offset)`, `ReadInt8(offset)`, and `ReadUint16`, `ReadUint32`, `ReadUint64`, `ReadInt16`, `ReadInt32`, `ReadInt64` with `(offset, order)`. The result is the integer or `binary.BinaryError`. `Byte` is also `Uint8`; `Int64` is also `Int`.

Matching write methods take `(offset, value)` for one-byte values and `(offset, value, order)` otherwise. They return a new `Bytes` value or `BinaryError` and require existing space for the entire integer. They never resize or mutate the receiver. Offsets need not be aligned.

`BinaryError` has `offset: Int`, `required: Int`, and `length: Int` fields. A read or replacement write requires a nonnegative offset and enough remaining bytes. Bounds checks avoid adding the offset and width before validating them, so negative or extremely large offsets return errors without overflow or panic.

## Immutable readers

`NewReader(data, order)` starts at byte zero. `Offset()` and `Remaining()` return nonnegative byte counts, with the `binary.ByteCount` fact. `Skip(count)` returns a new Reader or BinaryError; negative counts and skipping past the end are errors.

Reader methods `ReadByte`, `ReadInt8`, `ReadUint16`, `ReadUint32`, `ReadUint64`, `ReadInt16`, `ReadInt32`, and `ReadInt64` take no arguments. `ReadBytes(count)` reads an arbitrary number of bytes. Each returns `(value, nextReader) | BinaryError`; use `?` followed by tuple destructuring to advance:

```bork
import "bork/binary"

fn payload(data: Bytes): Bytes | binary.BinaryError {
  reader = binary.NewReader(data, binary.ByteOrder.BigEndian)
  (size, next) = reader.ReadUint16()?
  (body, _) = next.ReadBytes(toInt(size))?
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


Run `bork doc bork/binary` for the generated reference.

[All standard packages](README.md)
