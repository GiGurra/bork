# bork/archive

`bork/archive` reads and writes ZIP and TAR members without extracting them onto disk.

```bork
import "bork/archive"
import "bork/encoding"

fn demo() uses io: Ok | IoError {
  entries = [archive.Member { name: "hello.txt", data: encoding.Utf8("hello"), directory: false }]
  data = archive.Zip(entries)?
  println(archive.ReadZip(data)?.map(member => member.name))
  match (archive.ReadZip(data, maxBytes: 4)) {
    _: IoError => println("uncompressed data exceeds limit")
    _: List[archive.Member] => println("read archive")
  }
}

fn main() {
  println(demo())
}
```

```text
["hello.txt"]
uncompressed data exceeds limit
Ok
```

## API

| Signature | Meaning |
| --- | --- |
| `Zip(entries: List[Member]): Bytes \| IoError` | Encode ZIP members. |
| `WriteZip(file: fs.File, entries: List[Member]) uses io: Ok \| IoError` | Write ZIP directly to an open file. |
| `ReadZip(data: Bytes, maxBytes: Int = 67108864): List[Member] \| IoError` | Decode ZIP with a cumulative uncompressed byte limit. |
| `ForEachZip(file: fs.File, visit: (Member) => Ok, maxBytes: Int = 67108864) uses io: Ok \| IoError` | Visit ZIP members, buffering one member at a time. |
| `Tar(entries: List[Member]): Bytes \| IoError` | Encode TAR members. |
| `WriteTar(file: fs.File, entries: List[Member]) uses io: Ok \| IoError` | Write TAR directly to an open file. |
| `ReadTar(data: Bytes, maxBytes: Int = 67108864): List[Member] \| IoError` | Decode TAR with a cumulative uncompressed byte limit. |
| `ForEachTar(file: fs.File, visit: (Member) => Ok, maxBytes: Int = 67108864) uses io: Ok \| IoError` | Visit TAR members from the current file offset. |

`Member` is `{ name: String, data: Bytes, directory: Bool }`; directories
have empty data. Pure codecs need no effects. File operations use `io` and
iteration charges the callback's effects; callers retain file ownership.

## Write and iterate a file

```bork
import "bork/archive"
import "bork/encoding"
import "bork/fs"

fn demo(s: Scope) uses io: Ok | fs.Error | IoError {
  target = fs.TempFile(s)?
  archive.WriteZip(target, [archive.Member {
    name: "hello.txt", data: encoding.Utf8("hello"), directory: false
  }])?
  source = fs.Open(fs.Path(target), s)?
  archive.ForEachZip(source, member => println(member.name))?
}

fn main() {
  println(scope app { demo(app) })
}
```

```text
hello.txt
Ok
```

## Formats and shared limits

Direct file writers avoid buffering the
entire encoded archive. File iterators buffer one member's data at a time;
ZIP also holds its central directory metadata. TAR consumes the file's current
offset; ZIP reads the whole file with random access. File writers use the
current offset; a ZIP output should be empty at offset zero. Callbacks may use
effects, which are charged to their callers. Writers preserve entry order and
use fixed regular-file/directory permissions (0644/0755), without preserving
source timestamps or ownership. ZIP directory names are normalized with a
trailing slash. These APIs return data rather than extracting onto disk.

Readers and writers reject path traversal, absolute paths, backslashes, colons, NULs,
links and special entries; directories cannot carry data. Decompression limits
(default 64 MiB, configurable and nonnegative) count total uncompressed data
across archive entries and concatenated gzip members. They do not bound header
metadata, member count, compressed input or CPU time. Gzip and ZIP checksums are
verified, and malformed headers or truncated member data becomes IoError.
TAR permits omitted trailing zero blocks, following Go's archive reader. Pure APIs return no
partial data on error; file operations and callbacks may already have produced
partial output. Stream output never exceeds its configured byte limit.


A negative `maxBytes` returns `IoError`. Choose a smaller limit when accepting
untrusted archives, and enforce compressed-input, member-count, metadata and
work limits separately as needed. See [gzip](compress.md) for the corresponding
compression limit, and the
[compression and archive example](../../examples/compress_archive/main.bork).


Run `bork doc bork/archive` for the generated reference.

[All standard packages](README.md)
