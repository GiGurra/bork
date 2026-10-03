# bork/embed

## Compile-time embedded assets

`bork/embed` embeds regular files as immutable Bytes or validated UTF-8 Strings,
and directories as immutable `FS` snapshots. ReadBytes/ReadString/Directory are
pure compiler intrinsics with a compile-time constant String path, resolved
relative to the source package of the call (including imported packages).
Only direct calls are allowed; function references and dynamic paths fail at
checking. There is no new syntax or filesystem effect. Directory snapshots
include dotfiles recursively, expose lexical `Paths()` and pure `Read(name)`,
and return IoError for a missing snapshot name. Empty directories are supported;
only files appear in Paths. Globs and directory metadata are not included.

Missing or unreadable assets, wrong file/directory types, invalid UTF-8 text,
symlinks and special files produce compile diagnostics at the call. Paths must
stay within the source package: absolute paths, parent segments, backslashes,
colons and NULs are rejected. The driver captures each request once during
checking before evaluating facts. Evaluators, normal builds and test builds
stage captured bytes in the generated module and use `go:embed`, preserving
assets even if their source files disappear after capture. Large assets do not
inflate generated Go source. `bork emit` lists staged file names and their source
paths in a comment beside the embed directives; consumers must stage the listed
assets themselves. The emitted source by itself is not a complete build artifact.

## Embedded asset API

- **Embedded assets:** `bork/embed` provides pure compiler intrinsics
  `ReadBytes("assets/file.bin"): Bytes`, `ReadString("assets/page.html"): String`
  and `Directory("assets"): embed.FS`. Paths must be compile-time constant
  Strings, relative to the calling source package. Missing/unreadable assets,
  wrong file types, invalid UTF-8 for ReadString, symlinks, absolute/parent paths,
  backslashes, colons and NULs are compiler errors at the call. Intrinsics cannot
  be used as function values; wrap a direct constant call in a lambda instead.
  A directory snapshot recursively includes regular files and dotfiles;
  `snapshot.Paths()` returns file names in lexical order and `snapshot.Read(name)`
  returns `Bytes | IoError` without I/O. Empty directories have no file entries.
  There are no glob patterns and no runtime filesystem reads. Builds stage the
  captured bytes into the generated Go module and use Go's embed directives;
  `bork emit` prints source with those directives and a comment listing files
  that must be staged beside it. Its output alone does not contain asset data.
  See [examples/embed](../../examples/embed/main.bork).

## Examples

Import `bork/embed` to capture files as Bytes or UTF-8 Strings and directories as
immutable snapshots at compile time. Missing assets are compiler errors; binaries
need no source files at runtime. See [examples/embed](../../examples/embed/main.bork).
