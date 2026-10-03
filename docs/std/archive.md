# bork/archive

## Archives

`bork/archive` encodes/decodes ZIP and TAR with immutable `Member` records
(name, binary data, directory flag). Direct file writers avoid buffering the
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

## Archive API

`bork/archive` represents files and directories as
`Member { name: String, data: Bytes, directory: Bool }`; directories have empty
data. `Zip(members)` / `Tar(members)` return `Bytes | IoError`, and
`ReadZip(bytes, maxBytes = 67108864)` / `ReadTar(...)` return
`List[Member] | IoError`. `WriteZip(file, members)` / `WriteTar(...)` write
directly to an `fs.File`; `ForEachZip(file, visit, maxBytes = 67108864)` /
`ForEachTar(...)` visit one member at a time, buffering its data. File APIs
declare `uses io` and charge callback effects. Callers retain file ownership.
TAR and gzip consume the current file offset; ZIP iteration uses random access
over the whole file. Writers use the current output offset; ZIP files should
start empty at offset zero. Archives contain relative paths;
traversal, absolute paths, backslashes, colons, NULs, links and special files are
errors. No API extracts entries onto disk. Limits are nonnegative, cumulative
uncompressed data bytes across members (including concatenated gzip members).
Checksum and malformed input failures return IoError; TAR permits omitted
trailing zero blocks, as Go does. File writes and callbacks
may have already happened when a later error is returned. See
[examples/compress_archive](../../examples/compress_archive/main.bork).
