# Standard packages

Standard packages ship with the compiler. Import one as `bork/name` and call its functions with the package name in front: `import "bork/fs"`, then `fs.Open(path, s)`. Each page describes the package's API, with examples and limits.

| Package | Description |
| --- | --- |
| [bork/archive](archive.md) | ZIP and TAR Bytes codecs and file iteration |
| [bork/cli](cli.md) | Command-line options and subcommands decoded into records |
| [bork/compress](compress.md) | Gzip Bytes codecs and scoped file transfers |
| [bork/crypto](crypto.md) | Hashes, HMAC, secure bytes and password hashing |
| [bork/embed](embed.md) | Compile-time file and directory snapshots |
| [bork/encoding](encoding.md) | Hex, base64 and CSV encoding |
| [bork/env](env.md) | Environment variables and derived record configuration |
| [bork/fs](fs.md) | Scoped files, directories and filesystem operations |
| [bork/http](http.md) | Scoped HTTP clients, routes, TLS, bounded admission and shutdown |
| [bork/json](json.md) | Dynamic JSON, pretty printing and JSON Lines |
| [bork/log](log.md) | Structured logging |
| [bork/math](math.md) | Float math, exact integers, rationals and Decimal money |
| [bork/net](net.md) | Scoped TCP and UDP sockets |
| [bork/process](process.md) | Program arguments, subprocesses, captured output and scope cancellation |
| [bork/rand](rand.md) | Random draws and immutable seeded generators |
| [bork/regex](regex.md) | Compiled RE2 patterns, captures and String facts |
| [bork/sql](sql.md) | Scoped SQLite/Postgres connections, transactions, and typed SQL literals |
| [bork/tasks](tasks.md) | Shared bounded task pools with typed nonblocking admission |
| [bork/time](time.md) | Instants, durations and injectable clocks |
| [bork/url](url.md) | Immutable URLs, repeated query parameters and escaping |
| [bork/uuid](uuid.md) | Canonical UUIDs, generation and JSON string codecs |

Operations on strings, lists, maps, options, and bytes need no import. They are methods that are available everywhere, such as `text.trim()` and `xs.map(f)`.

For contributors: when adding a package, add its page and a row to this table. The [Go helper API](../std-go.md) documents the contracts that standard-package implementations share.
