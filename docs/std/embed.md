# bork/embed

`bork/embed` captures files and directories at compile time so a program can read immutable assets without runtime filesystem access.

This example embeds its own source. Save it as `main.bork` before running `bork run main.bork`:

```bork
import "bork/embed"

fn main() {
  source = embed.ReadString("main.bork")
  println(source.contains("fn main"))
}
```

Output:

```text
true
```

For an application asset, replace the path with a regular file within the source package, such as `"assets/page.html"`. The compiled binary needs no source asset at runtime.

## API

Every API here is pure. Paths passed to the three compiler intrinsics must be compile-time constant Strings, relative to the calling source package, including calls inside imported packages.

| Signature | Meaning |
| --- | --- |
| `ReadBytes(path: String): Bytes` | Capture a regular file as immutable bytes. |
| `ReadString(path: String): String` | Capture and validate a UTF-8 text file. |
| `Directory(path: String): FS` | Capture an immutable recursive directory snapshot. |
| `(snapshot: FS).Paths(): List[String]` | List embedded file names in lexical order. |
| `(snapshot: FS).Read(name: String): Bytes \| IoError` | Read a snapshot entry by runtime name. |

`embed.FS` has `{ files: Map[String, Bytes] }`.

Directories include regular files and dotfiles recursively. Empty directories work; only files appear in Paths. There are no glob patterns or directory metadata. Snapshot names can be computed at runtime, unlike the constant Directory path.

## Compiler errors and missing entries

Missing/unreadable assets, incorrect file/directory types, invalid UTF-8 in ReadString, symlinks and special files are compile errors at the call. Paths must stay inside the source package: absolute paths, parent segments, backslashes, colons and NULs are rejected.

```bork fails
import "bork/embed"

fn main() {
  println(embed.ReadString("../outside.txt"))
}
```

```text
path must be relative to the source package, without parent segments, backslashes, colons or NULs
```

The intrinsics permit direct calls only. Dynamic paths and function references are check errors; wrap a direct constant call in a lambda when you need a callback. A missing *snapshot entry* is instead `IoError` at runtime, because its name can be computed dynamically.

## Read by naming convention

Embed a directory once and look up files by convention. The [embed_templates example](../../examples/embed_templates/main.bork) has a complete `assets/` fixture and uses names such as `templates/home.html`. Add an asset under that directory and rebuild to include it; Paths lists what the binary actually contains. Check Read's result before decoding the returned Bytes. For example, this helper
looks up a template while keeping a missing entry explicit:

```bork
import "bork/embed"

fn readAsset(snapshot: embed.FS, name: String): Bytes | IoError {
  snapshot.Read(s"templates/$name.html")
}
```

## Builds and emitted source

The compiler captures each request once during checking, before evaluating facts. Normal and test builds stage the captured bytes in the generated Go module and use Go embed directives; large assets do not expand the generated Go source. Captured files remain available even if their source disappears after capture.

`bork emit` lists staged filenames and source paths in a comment beside the embed directives. Consumers of emitted source must stage those assets themselves: source alone is not a complete build artifact. See [the embedded-files example](../../examples/embed/main.bork).
