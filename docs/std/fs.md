# bork/fs

## Filesystem package

`bork/fs` owns `File`, moved from the prelude, and provides whole-file binary
Read/Write/Append, scoped Open/Create/CreateNew, ReadAll/WriteTo on handles,
text compatibility helpers ReadAllText/WriteText, and streaming ForEachLine.
File data is Bytes; caller-visible slices never mutate. Text compatibility
helpers preserve the former prelude behavior; validated UTF-8 decoding is
explicit. Streaming has no Scanner line-size limit. File resource lifetimes
remain enforced, including imported resources returned through generic wrappers.

Directories have lexical listing/walking, MkdirAll, Remove/RemoveAll, Rename,
and Stat with size, time.Instant modification time and kind. Walk and Stat do
not follow symbolic links. Host filepath helpers join and split paths and
resolve absolute paths. TempFile/TempDir resources close and remove themselves
at scope end, including early-return and panic cleanup; TempDir removes its
contents. Cleanup is best effort, as existing scope file finalizers were.
Files create with mode 0666 and directories with 0777, modified by the host
umask. Write truncates; Append appends; CreateNew reports Exists for an
existing path. Operational errors are typed union values NotFound,
PermissionDenied, Exists or IoError with the path and underlying message.
There are no new language constructs.

## Filesystem API

- **`bork/fs`:** files and directories use `Bytes` and scoped resources.
  `Read(path)`, `Write(path, bytes)` (create/truncate), and `Append(path, bytes)`
  work with whole files. `Open(path, scope)`, `Create(path, scope)` (truncate),
  and `CreateNew(path, scope)` (exclusive) give scoped `fs.File` resources.
  `ReadAll(file)` consumes remaining bytes; `WriteTo(file, bytes)` returns
  the byte count. `ReadAllText` and `WriteText` replace the former prelude's
  text operations; ReadAllText preserves raw text, while validated decoding
  is `utf8String(ReadAll(file)?)`. `ForEachLine(file, visit)` streams lines
  without a scanner size limit, removing LF/CRLF and keeping a final line.
  Directory operations: `ReadDir` (lexical entry order), `Walk` (includes
  root, lexical traversal, no symlink following), `MkdirAll`, `Remove`,
  `RemoveAll`, `Rename`, and `Stat` (size, modified `time.Instant`, and kind;
  observes symlinks themselves). Kinds are "file", "directory", "symlink",
  or "other". `Join(List[String])`, `Base`, `Dir`, `Ext`, and `Abs` follow
  the host's filepath rules. `TempFile(scope, directory = "", pattern = "bork-*")`
  and `TempDir` create resources removed at scope end; `Path(file)` and
  `DirectoryPath(directory)` give their paths. `fs.Error` is
  `NotFound | PermissionDenied | Exists | IoError`, all carrying path/message;
  `ErrorInfo(error)` extracts those common fields into IoError. The old
  prelude `File`, `openFile`, `createFile`, `readAll`, and `write` have moved
  to this package. I/O functions declare `uses io`; ForEachLine accepts an
  open callback and also charges its effects. See [the filesystem example](../../examples/fs/main.bork).

`Lines(path): Seq[String | Error] uses io` opens a fresh file on traversal, strips LF/CRLF, retains an unterminated final line, and supports unbounded line lengths. `Entries(path): Seq[DirEntry | Error] uses io` visits directory entries in filesystem order, one at a time. Both close on exhaustion or stop; errors are final elements. Use `ReadDir` when sorted materialized entries are needed.
