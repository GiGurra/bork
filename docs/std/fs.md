# bork/fs

`bork/fs` reads and writes files, traverses directories, and owns temporary files through scopes.

```bork
import "bork/encoding"
import "bork/fs"

fn demo(s: Scope) uses io: Ok | fs.Error {
  file = fs.TempFile(s)?
  _ = fs.WriteText(file, "hello")?
  data = fs.Read(fs.Path(file))?
  println(encoding.ParseUtf8(data))
  match fs.CreateNew(fs.Path(file), s) {
    _: fs.Exists => println("already exists")
    other => println(other)
  }
}

fn main() {
  println(scope app { demo(app) })
}
```

```text
hello
already exists
Ok
```

The temporary file is closed and removed when `app` ends. The second create
shows a handled failure without depending on a particular host error message.

## API

| Signature | Meaning |
| --- | --- |
| `Read(path: String) uses io: Bytes \| Error` | Read a whole file as immutable bytes. |
| `Write(path: String, data: Bytes) uses io: Ok \| Error` | Create or truncate a file. |
| `Append(path: String, data: Bytes) uses io: Ok \| Error` | Create or append to a file. |
| `Open(path: String, s: Scope) uses io: File \| Error` | Open a scoped file for reading. |
| `Create(path: String, s: Scope) uses io: File \| Error` | Open a scoped file for writing, truncating it. |
| `ReadAll(file: File) uses io: Bytes \| Error` | Consume the remaining bytes from an open handle. |
| `ReadAllText(file: File) uses io: String \| Error` | Consume remaining bytes as raw text, without UTF-8 validation. |
| `WriteTo(file: File, data: Bytes) uses io: Int \| Error` | Write bytes to an open handle; return the byte count. |
| `WriteText(file: File, text: String) uses io: Int \| Error` | Write UTF-8 text; return the byte count. |
| `Path(file: File): String` | Read the stored file path. |
| `DirectoryPath(directory: Directory): String` | Read the temporary directory path. |
| `MkdirAll(path: String) uses io: Ok \| Error` | Create a directory and missing parents. |
| `Remove(path: String) uses io: Ok \| Error` | Remove a file or empty directory. |
| `RemoveAll(path: String) uses io: Ok \| Error` | Remove a path and its contents. |
| `Rename(from: String, to: String) uses io: Ok \| Error` | Rename or move a path using host filesystem rules. |
| `ReadDir(path: String) uses io: List[DirEntry] \| Error` | Materialize entries in lexical order. |
| `Walk(path: String) uses io: List[DirEntry] \| Error` | Materialize a lexical traversal, including its root. |
| `Stat(path: String) uses io: Info \| Error` | Inspect a path without following its final symlink. |
| `TempFile(s: Scope, directory: String = "", pattern: String = "bork-*") uses io: File \| Error` | Create a temporary file, removed at scope end. |
| `TempDir(s: Scope, directory: String = "", pattern: String = "bork-*") uses io: Directory \| Error` | Create a temporary directory, recursively removed at scope end. |
| `ForEachLine(file: File, visit: (String) => Ok) uses io: Ok \| Error` | Visit lines from the current handle position. |
| `Join(parts: List[String]): String` | Join path components using host filepath rules. |
| `Base(path: String): String` | Return the last path component. |
| `Dir(path: String): String` | Return the parent path. |
| `Ext(path: String): String` | Return the filename extension. |
| `Abs(path: String) uses io: String \| Error` | Resolve an absolute path. |
| `ErrorInfo(error: Error): IoError` | Extract common error fields. |
| `CreateNew(path: String, s: Scope) uses io: File \| Error` | Create exclusively; an existing path gives Exists. |
| `Lines(path: String): Seq[String \| Error] uses io: Ok` | Lazily read lines; a failure is the final element. |
| `ReadLines(file: File) uses io: Seq[String \| Error] uses io` | Lazily consume lines from an open handle without closing it. |
| `Entries(path: String): Seq[DirEntry \| Error] uses io: Ok` | Lazily visit directory entries in filesystem order. |

`File` and `Directory` are resources. The compiler checks their
[lifetimes](../language/scopes.md), including when wrapped in other values.
`ForEachLine` also charges the callback's effects; constructing and traversing
filesystem sequences requires `io`.

| Type | Fields or variants |
| --- | --- |
| `Error` | `NotFound \| PermissionDenied \| Exists \| IoError` |
| `NotFound`, `PermissionDenied`, `Exists`, `IoError` | `path: String`, `message: String` |
| `DirEntry` | `name: String`, `path: String`, `kind: String` |
| `Info` | `size: Int`, `modified: time.Instant`, `kind: String` |

Kinds are `"file"`, `"directory"`, `"symlink"`, or `"other"`. Files are created
with mode 0666 and directories with 0777, modified by the host umask. Operations
on symbolic links follow host rules except `Walk` and `Stat`, which observe
links themselves.

## Read validated text

Whole-file and handle reads return `Bytes`. Decode explicitly when invalid
UTF-8 should be rejected:

```bork
import "bork/encoding"
import "bork/fs"

fn readText(path: String) uses io: String | fs.Error | ParseError {
  encoding.ParseUtf8(fs.Read(path)?)
}

fn main() {
  invalid: List[Byte] = [255]
  println(encoding.ParseUtf8(invalid.toBytes()))
}
```

`ReadAllText` preserves bytes as text without validation. A failed write can
leave partial output; an error does not imply that the file is unchanged.

## Stream lines and entries

```bork
import "bork/fs"

fn showLines(path: String) uses io {
  for item in fs.Lines(path) {
    match item {
      line: String => println(line)
      error: fs.Error => eprintln(fs.ErrorInfo(error).message)
    }
  }
}

fn main() {
  showLines("notes.txt")
}
```

`Lines` opens a fresh file for each traversal, removes LF or CRLF, and keeps a
final unterminated line. There is no fixed scanner line-size limit.
`Entries` opens a fresh directory for each traversal and visits one entry at a
time in filesystem order. Both close on exhaustion or early stop; failures
are final elements. Use `ReadDir` for a sorted, materialized listing.
`ForEachLine` provides the same line handling for an already-open file.

`ReadLines(file)` borrows an already-open file from its owning scope. It reads
from the handle's current position, uses the same newline handling as `Lines`,
and reports a read failure as its final element. Constructing the sequence
does not read. Each traversal continues from the handle's current position;
it does not reopen or rewind the file. Traversals share that position, so
consume them sequentially, and do not read from, seek, or close the handle
inside a traversal's callback. The sequence cannot outlive the file's scope.

Stopping early leaves the next unread byte available to another traversal or
to `ReadAll`/`ReadAllText`. On seekable handles, including regular files,
`ReadLines` buffers reads and seeks back by any unread buffered bytes when
traversal stops. On non-seekable handles such as pipes, it reads one byte at a
time without read-ahead, trading throughput for precise partial consumption.
`ReadLines` never closes the borrowed file, even on exhaustion or error; the
caller's scope closes it. Use `Lines(path)` when each traversal should open
and own a fresh file instead.

```bork
import "bork/fs"

fn firstLine(path: String) uses io: Option[String | fs.Error] | fs.Error {
  scope producer {
    file = fs.Open(path, producer)?
    fs.ReadLines(file).first()
  }
}

fn main() { println(firstLine("notes.txt")) }
```

The [generators example](../../examples/generators/main.bork) opens a file
inside a generator's scope and uses `ReadLines` across yields. Stopping its
consumer early closes that scope and its file before the consumer continues.

## Temporary directories

```bork
import "bork/encoding"
import "bork/fs"

fn demo(s: Scope) uses io: Ok | fs.Error {
  directory = fs.TempDir(s)?
  root = fs.DirectoryPath(directory)
  path = fs.Join([root, "notes.txt"])
  fs.Write(path, encoding.Utf8("one\ntwo\n"))?
  println((fs.Stat(path)?).size)
  println((fs.ReadDir(root)?).map(entry => entry.name))
}

fn main() {
  println(scope app { demo(app) })
}
```

```text
8
["notes.txt"]
Ok
```

An empty temporary-directory argument uses the host temporary directory; the
last `*` in a pattern is replaced with a random suffix. Cleanup closes and
removes temporary files, and recursively removes temporary directories, on
normal completion, early return and panic. Cleanup is best effort.

See the [filesystem example](../../examples/fs/main.bork).

Run `bork doc bork/fs` for the generated reference.

[All standard packages](README.md)
