# bork/compress

`bork/compress` encodes gzip bytes and streams compression between scoped files.

```bork
import "bork/compress"
import "bork/encoding"

fn demo() uses io: Ok | IoError | ParseError {
  packed = compress.Gzip(encoding.Utf8("hello"))?
  println(encoding.ParseUtf8(compress.Gunzip(packed)?)?)
  match (compress.Gunzip(packed, maxBytes: 4)) {
    _: IoError => println("uncompressed data exceeds limit")
    _: Bytes => println("decompressed")
  }
}

fn main() {
  println(demo())
}
```

```text
hello
uncompressed data exceeds limit
Ok
```

## API

| Signature | Meaning |
| --- | --- |
| `Gzip(data: Bytes): Bytes \| IoError` | Encode bytes as gzip. |
| `Gunzip(data: Bytes, maxBytes: Int = 67108864): Bytes \| IoError` | Decode gzip with a cumulative uncompressed byte limit. |
| `GzipTo(source: fs.File, target: fs.File) uses io: Int \| IoError` | Compress from source’s current offset to target’s current offset. |
| `GunzipTo(source: fs.File, target: fs.File, maxBytes: Int = 67108864) uses io: Int \| IoError` | Decompress files within the byte limit. |

All failures are `IoError` values. Codecs are pure; streaming operations use
`io`, retain file ownership, begin at current offsets, and return the number
of uncompressed bytes transferred.

## Stream between files

```bork
import "bork/compress"
import "bork/fs"

fn demo(s: Scope) uses io: Ok | fs.Error | IoError {
  original = fs.TempFile(s)?
  _ = fs.WriteText(original, "hello")?
  source = fs.Open(fs.Path(original), s)?
  packed = fs.TempFile(s)?
  println(compress.GzipTo(source, packed))
  input = fs.Open(fs.Path(packed), s)?
  output = fs.TempFile(s)?
  println(compress.GunzipTo(input, output))
}

fn main() {
  println(scope app { demo(app) })
}
```

```text
5
5
Ok
```

## Bound decompression

The default `maxBytes` is 67108864 (64 MiB), counting total uncompressed bytes
across concatenated gzip members. Zero accepts only empty decompressed data;
negative limits return `IoError`. Checksums are verified. Invalid headers,
truncated input, checksum failures, and an exceeded limit also return `IoError`.

The limit does not bound compressed input, metadata, or CPU time. Pure `Gunzip`
returns no partial output on error; `GunzipTo` may have written a prefix before
failing, but never writes past the configured uncompressed byte limit. Temporary
output followed by a successful rename can keep an existing destination intact.

See [archive format limits](archive.md#formats-and-shared-limits) and the
[compression and archive example](../../examples/compress_archive/main.bork).


Run `bork doc bork/compress` for the generated reference.

[All standard packages](README.md)
