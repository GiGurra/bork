# Standard packages

Standard packages ship with the compiler. Import one as `bork/name` and call its functions with the package name in front: `import "bork/fs"`, then `fs.Open(path, s)`. Each page describes the package's API, with examples and limits.

| Package | Description |
| --- | --- |
| [bork/archive](archive.md) | Reading and writing ZIP and TAR archives |
| [bork/bits](bits.md) | Counting, rotating, reversing, and checking integer bit fields |
| [bork/build](../language/comptime.md#reading-files-at-build-time) | Reading files while the program compiles |
| [bork/cli](cli.md) | Command-line options and subcommands decoded into records; [application cookbook](cli-cookbook.md) |
| [bork/compress](compress.md) | Gzip compression of bytes and files |
| [bork/crypto](crypto.md) | Hashes, HMAC, secure random bytes, and password hashing |
| [bork/embed](embed.md) | Files and directories built into the executable |
| [bork/encoding](encoding.md) | Hex, base64, and CSV |
| [bork/env](env.md) | Environment variables, and configuration decoded from them |
| [bork/fs](fs.md) | Files, directories, and paths |
| [bork/http](http.md) | HTTP clients and servers: routes, TLS, load limits, and shutdown |
| [bork/codec](codec.md) | Value trees and derived encode/decode classes |
| [bork/json](json.md) | Parsing, rendering, and streaming JSON |
| [bork/log](log.md) | Structured logging |
| [bork/math](math.md) | Float math, big integers, rationals, and decimal money |
| [bork/net](net.md) | TCP and UDP sockets |
| [bork/process](process.md) | Program arguments, exit codes, and running subprocesses |
| [bork/rand](rand.md) | Random numbers and seeded generators |
| [bork/regex](regex.md) | Regular expressions, captures, and facts about matching strings |
| [bork/signal](signal.md) | Shutdown signals, grace deadlines, and scope-owned subscriptions |
| [bork/sql](sql.md) | SQLite and Postgres connections, transactions, and typed SQL literals |
| [bork/strconv](strconv.md) | Integer formatting and parsing in bases 2 through 36 |
| [bork/tasks](tasks.md) | Task pools that limit how much work runs at once |
| [bork/test](test.md) | Typed assertions that return checked values and facts |
| [bork/time](time.md) | Instants, durations, and clocks that tests can replace |
| [bork/url](url.md) | Parsing, building, and escaping URLs |
| [bork/uuid](uuid.md) | Parsing and generating UUIDs |
| [bork/yaml](yaml.md) | Parsing and writing YAML as codec values |

Operations on strings, lists, maps, options, and bytes need no import. They are methods that are available everywhere, such as `text.trim()` and `xs.map(f)`.

For contributors: when adding a package, add its page and a row to this table. The [Go helper API](../std-go.md) documents the contracts that standard-package implementations share.
