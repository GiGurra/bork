# bork/compress

## Compression

`bork/compress` supplies gzip Bytes codecs, suitable for HTTP bodies, and
streaming `GzipTo` / `GunzipTo` transfers between scope-owned `bork/fs.File`
handles. Codecs are pure; file operations declare `uses io`. All failures are
IoError union results. Files remain owned by their original scopes. Transfers
start at the current offsets and return the uncompressed byte count.

## Compression API

- **Compression and archives:** `bork/compress` provides `Gzip(Bytes)` and
  `Gunzip(Bytes, maxBytes = 67108864)` returning `Bytes | IoError`.
  `GzipTo(source: fs.File, target: fs.File)` and `GunzipTo(source, target,
  maxBytes = 67108864)` stream between scoped files and return `Int | IoError`
  (uncompressed byte count).

## Shared archive limits

See [archive operations and shared limits](archive.md#archives).

## Examples

Import `bork/compress` for gzip Bytes codecs and streaming transfers between
`bork/fs.File` handles. `bork/archive` reads and writes ZIP/TAR file and directory
members, with Bytes codecs and file iteration/writing. Readers default to a
64 MiB cumulative decompression limit and reject unsafe archive names and links;
errors are `IoError` values. See [examples/compress_archive](../../examples/compress_archive/main.bork).
