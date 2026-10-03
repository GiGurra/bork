# bork

> **Pre-alpha.** The design is still evolving, and the compiler only handles a small first slice of the language.

**bork** is a pragmatic backend language of guarantees.

Think Go's tooling simplicity, with the functional style of Scala and Haskell, and correctness guarantees that anyone can write, not just the type-theory crowd.

*Bork bork bork!* The name comes from the [Swedish Chef](https://en.wikipedia.org/wiki/Swedish_Chef). Strict recipes, cheerfully enforced.

## Why bork over Go

bork compiles to Go and keeps Go's runtime, but adds things Go can't give you:

- **Immutability.** Values never change, so whatever is known about them stays true.
- **Facts.** What you check about a value becomes part of its type, and the compiler proves every function's requirements at every call site.
- **Scopes.** Outside resources (files, connections, transactions, leases) belong to scopes, and using one requires proof that a scope managing it is still open.
- **Effects in signatures.** A function's signature says whether it does I/O, calls the network, reads the clock, or touches shared state (`uses io + net`), and one that says nothing is pure. Code can't do what its signature doesn't allow, which makes signatures something a reviewer can rely on.
- **Typed ambient values.** Request-scoped values such as a trace id or the signed-in principal are declared with a type (`ambient principal: Principal`), read by functions that say so (`needs principal`), and bound for a block with `with (principal: p) { ... }`. The compiler checks that every caller provides them, so the dependency shows in signatures instead of hiding in an untyped context. A trace id that only labels logs and outgoing calls is marked instead (`logged propagated("traceparent") ambient trace: String`): every log line written while it is bound carries it, and network code sends it on, with no signature in between.

Facts already work:

```
pred positive(x: Int) { x > 0 }

fn transfer(amount: Int where positive): Receipt { ... }

fn payOut(amount: Int) {
  transfer(amount)       // error: transfer requires amount to be positive, but that is not proven for amount
}                        //   (check it first with if (positive(amount)) { ... }, or require it: amount: Int where positive)

fn payOutChecked(amount: Int) {
  if (amount > 0) { transfer(amount) }          // positive unfolds to this comparison
}

transfer(0)              // error: transfer requires amount to be positive, but positive(0) is false
```

Predicates are ordinary bork functions; on constants, the compiler runs them at build time. See [examples/payments](examples/payments/main.bork).

Functions can relate their inputs directly: `fn interval(lo: Int, hi: Int) where lo <= hi: Range { ... }`, or `where sameLength(xs, ys)` using an ordinary predicate. The clause precedes `uses` and the result type; callers prove it and function bodies can rely on it.

Record fields can require facts about siblings: `type Range = { lo: Int, hi: Int where atLeast(lo) }`. Construction and `copy` prove the relation using the completed values; changing `lo` also rechecks the requirement on `hi`.

Facts in positions the checker cannot enforce yet, such as Map keys and values or constrained alias names in patterns and constructors, produce a compile error. See the [supported fact positions](docs/grammar.md#semantics-in-brief).

So do scopes:

```
import "bork/fs"

fn countFile(path: String) uses io: Counts | fs.Error {
  scope s {
    countText(fs.ReadAllText(fs.Open(path, s)?)?)   // the file is closed when s ends, even by ? or a panic
  }
}

fn broken(path: String) uses io: String | fs.Error {
  f = scope s { fs.Open(path, s)? }
  fs.ReadAllText(f)             // error: f may be released: it belongs to scope s, which ended on line 2
}
```

See [examples/wc](examples/wc/main.bork), a small `wc`.

When lifetimes overlap without nesting, as when a connection hands over to the next one before it is released, an owned child scope has an explicit start and end. The compiler checks that it is closed (or passed on) exactly once on every path, and that nothing of it is used after:

```
fn roll(prev: OwnedScope in app, conn: Conn in prev, app: Scope, n: Int) uses io + state {
  next = openScope(app)                  // opens while prev is still open
  nextConn = connect(next.scope)
  handOver(conn, nextConn)
  closeScope(prev)                       // conn is released here; using it now is an error
  if (n > 1) { roll(next, nextConn, app, n - 1) } else { closeScope(next) }
}
```

And effects:

```
fn describe(u: User): String {
  println(u.name)        // error: describe uses io (it calls println), but its signature allows no effects; declare it: uses io
  u.name
}

fn names(users: List[User]) uses io {
  users.forEach(u => println(u.name))   // forEach takes any function: the call uses what its lambda does
}
```

`main` and tests may do anything; everything below them says what it does. See [examples/http_server](examples/http_server/main.bork).

For development, `dbg(expr)` prints the expression and its value to stderr and returns it, including in pure code. `todo()` or `todo("message")` fills an unfinished branch and panics with its location if reached. `bork check` warns about both; `--json` includes a fix to remove each dbg wrapper.

Tests replace functions that have effects with `mock`, with no interfaces or injected clients. A mock lasts until the end of its block, belongs to its test, and is seen by the tasks the test starts. Its body is checked against the function's signature, facts and effects included:

```
test "an upstream answering 503 is down" {
  mock time.Now() { noon }
  gets = mock http.Get(url, s, timeoutMs) { http.Text(503, "") }
  gets.expect(times: 1)
  assertEqual(checkAll(["https://a.example"]), [Health.Down { url: "https://a.example", status: 503, at: noon }])
  assertEqual(gets.args().map(c => c.timeoutMs), [2000])
}
```

A handle records the calls as typed records (`gets.args()`), declares how many calls must come (`expect`, `expectWhere`, checked when the mock ends), and waits for calls from other tasks (`waitFor`). Expectation and wait failures point at the helper call, including expectations checked when tasks finish.

Production builds are unchanged. See [examples/mocking](examples/mocking/main.bork) and [mocking in tests](docs/requirements.md#mocking-in-tests-design-bork-53lit4).

## Goals

- **Pragmatic high correctness for backend systems.** That is the whole point.
- **Immutable by default.** Values don't change. Opaque Go values hold shared state behind an explicit boundary; facts and structural equality exclude them.
- **No null, no exceptions.** Optional values must be checked before use, and failures are values. Panics exist, but can't be caught.
- **Strict rules, trivially easy.** Declaring what must hold ("amount is positive", "user is non-nil", "list is non-empty") should be as cheap as writing an `if`. The compiler proves it at every call site or fails the build. This builds on ideas from [proven](https://github.com/GiGurra/proven).
- **Methods for value operations.** `xs.filter(f).map(g)`, `text.trim()`, and `m.put(k, v)` need no imports. Use `|>` for free functions and lambdas when passing a method operation as a value.
- **Functional style.** Algebraic data types, pattern matching, expressions over statements, and first-class functions. Bound type patterns can check predicates at runtime (`n: Int where positive`), with facts known in the successful arm and an unguarded fallback required.
- **Go-like tooling.** One binary with commands like `bork build`, `bork test`, `bork fmt`. Fast builds. No build-system archaeology.
- **Compiles to Go** (first version). Go is only a compilation target: we get its runtime, GC, goroutines, and cross-platform builds without writing our own backend. bork code does not import Go packages; the explicit boundary is `unsafe go` function bodies (Go imports accept file-local aliases), with a [stable Go helper API](docs/std-go.md) for standard packages, and bindings that call a Go function directly, checked against its Go signature (`fn Atoi(s: String): Int | GoError unsafe go "strconv.Atoi"`). Opaque Go types (`type Request = go "*net/http.Request"`) share boxed Go values through checked function and method bindings; Go resources are owned and closed by a scope; generated context-bound bindings support attachment across scopes with shared cancellation for results from one call. Mirror records (`type Url = go "net/url.URL" { host: String }`) copy selected struct fields at the boundary and check their facts.

## Non-goals

- Minimal runtime size or no-GC.
- Maximum efficiency on embedded devices.
- Being a proof assistant. Guarantees must stay pragmatic.



## Getting started

The compiler is at an early stage, but usable for small programs. Done: the plain language (M0: functions, sized numbers with checked conversions, records with nested `copy`, sealed types, unions, `Option`, `?`, `match`, string interpolation, `unsafe go` bodies), generics, lambdas, `List`, `Map`, methods (`xs.filter(f).map(g)`, references such as `words.map(String.byteLength)`), and `|>`; facts (M1: predicates, `where`, guards, rules, compile-time checks, and test mode); scopes and resources (M2, for one routine); packages; and type classes with JSON decoding (M3, under way: `derive (Decode)` decodes requests into proven values); and [standard packages](docs/std/README.md). [`bork/cli`](docs/std/cli.md) validates options from flags, environment mappings, and JSON configuration files before invoking handlers. Direct calls support named arguments (`http.Listen(addr, s, handler, maxBodyBytes: 1048576)`), with positional arguments first and named arguments in any order; omitted optional parameters use their defaults. Values evaluate in source order, and declared parameter names are public API. Changed copies use colons too: `config.copy(port: 9000)`. Record conversion uses `user.into[UserDto](name: user.name.toUpper())`, copying compatible fields, converting nested records and collections, and checking target facts and construction privacy. A record can declare package-controlled construction (`type Config = private { ... }`): importers can read its fields, and get checked values through the package's constructors or codecs. Whole-value clauses (`type Range = { lo: Int, hi: Int } where ordered`) guarantee the completed record or sealed value, including after copies and decoding. Expected types also supply constructors: `config: Config = .{ port: 8080, tls: .Disabled }`, or `configure(config: .{ port: 9000, tls: .Files { cert: "server.pem", key: "server.key" } })`. Explicit constructors supply types for inferred bindings; shorthand requires a unique expected record or sealed type. Generic heads can specify the specialization directly: `Box[Int] { values: [] }`, `Option[String].Some { value: "trace" }`, and `Option[String].None`; imported owners work too. Record and variant fields can declare closed defaults (`port: Int = 8080`), used by literals and derived decoding when a field is absent. Field docs and defaults are available through the derived schema for standard integrations. `derive (GoStruct)` supplies a separate exported Go struct, checked conversions, and ordered field tags (`go { json: "port" }`) for reflection-based libraries. A `Show[T]` instance in a declared type's own package customizes its text consistently, including inside generic code and nested values. List and map keys use structural equality independently of their text. Unordered maps print deterministically (numeric and string keys by value, other keys by their text, with mixed kinds grouped). [The slow-downstream example](examples/slow_downstream/main.bork) combines bounded HTTP admission with a shared retry budget: six callers make at most ten attempts while the downstream runs two handlers and queues two requests. HTTP clients forward scope deadline budgets to Bork servers; `requestTimeoutMs` caps incoming work before admission and body reads. Explicitly marked ambient values also forward across HTTP, with checked W3C trace context and isolated request labels. [The two-service example](examples/service_context/main.bork) shows trace continuity, typed checked extraction, and shrinking budgets. Third-party Go bindings use pinned `go-deps.mod` and `go-deps.sum` manifests beside `bork.mod`. It compiles bork to Go, so [Go](https://go.dev/dl/) must be installed.

```bash
go install github.com/GiGurra/bork/cmd/bork@latest

bork run examples/hello      # compile and run
bork build examples/hello    # compile to an executable
bork check examples/hello    # type-check only
bork describe main.bork:3:9 --where notEmpty  # query a type and prove a fact
bork emit examples/hello     # show the generated Go
bork test examples/payments  # run the tests, checking trusted facts and rules
bork test --update .         # run the tests, writing the snapshots assertSnapshot finds missing or changed
bork test --parallel 8 .     # run up to 8 tests at a time, each with its own mocks and snapshots
bork test --hermetic .       # fail, without running, the tests that could reach the network unmocked
bork test --auto-properties examples/payments  # also call trusted functions on generated arguments
bork fmt examples           # format .bork files recursively in place
bork fmt --check examples    # exit 1 if formatting would change a file
bork deps get github.com/google/uuid@v1.6.0  # pin a Go dependency beside bork.mod
bork deps download          # download pinned dependencies and fill checksums
```

For tools and agents, `bork check --json path | jq` emits one diagnostic per line on stdout. `bork build --json` and `bork test --json` emit the same JSON Lines on stderr, leaving stdout for test reports. Successful compilation emits no diagnostics; compilation errors still exit with status 1. See [the diagnostic format](docs/diagnostics.md) for codes, positions, and suggested text edits.

`bork/cli` supports typed subcommands with command-specific decoded options, generated help, and scoped handlers. See [the subcommand API](docs/std/cli.md#subcommands).

`bork fmt [paths...]` defaults to the current directory and prints changed paths.
It uses two spaces for indentation, normalizes spacing and blank-line runs, and keeps
existing line breaks and comment text. CRLF line endings become LF, including
line-comment endings. Bytes inside block comments, strings, interpolations, and
raw `unsafe go` bodies are preserved verbatim. Directory traversal skips hidden directories,
`vendor`, and symbolic links. `--check` prints paths needing formatting without
writing files; it exits 0 when all files are already formatted, and 1 otherwise.
Lexically invalid files are reported and left untouched; directory formatting
continues with the remaining files and exits 1 after reporting errors.

`bork describe file.bork:line:column` reports the type, definition, visible methods, and known facts at a position. Add `--where 'between(1, 10)'` to ask the compiler whether a requirement is proven there, or `--json` for a structured answer. See [compiler code queries](docs/describe.md).

A directory is a package. To use several, put a `bork.mod` naming the module (`module example.com/shop`) at its root, and import packages by path: `import "example.com/shop/money"`, then `money.Cents(250)`. A package that implements functions in Go (`unsafe go`) must be listed in `bork.mod` too, as `unsafe "example.com/shop/money"`, so new Go code shows up in review.

```
```

```
fn classify(n: Int): String {
  if (n < 0) {
    "negative"
  } else if (n == 0) {
    "zero"
  } else {
    "positive"
  }
}

fn main() {
  println("Hello from bork!", classify(42))
}
```

More examples: [config](examples/config/README.md) (defaults, named overrides, TLS variants, and package-owned validation), [http_server](examples/http_server/main.bork) (a REST API with JSON, an atom of persistent maps for state, and logging), [signup_api](examples/signup_api/main.bork) (an endpoint whose requests decode into proven values), [calculator](examples/calculator/main.bork) (a parser and evaluator), [orders](examples/orders/main.bork) (validation and pricing), and [accounts](examples/accounts/main.bork) (a state machine). A short one, from [examples/users](examples/users/main.bork):

```
type Address = { city: String }
type User = { name: String, address: Address, email: Option[String] }

type NotFound = { id: Int }
type DbError = { message: String }

fn findUser(id: Int): User | NotFound | DbError {
  if (id == 1) {
    User { name: "Ada", address: Address { city: "London" }, email: Option.None }
  } else if (id < 0) {
    DbError { message: "negative id" }
  } else {
    NotFound { id: id }
  }
}

// ? keeps the User and returns NotFound or DbError to the caller.
fn moveUser(id: Int, city: String): User | NotFound | DbError {
  user = findUser(id)?
  user.copy(address.city: city)
}

fn describe(id: Int): String {
  match (moveUser(id, "Oslo")) {
    u: User => s"${u.name} now lives in ${u.address.city}"
    NotFound { id: missing } => s"no user with id $missing"
    e: DbError => s"database error: ${e.message}"
  }
}

fn main() {
  println(describe(1))
  println(describe(2))
  println(describe(-1))
}
```

Shell completion (via [boa](https://github.com/GiGurra/boa)): `bork completion bash|zsh|fish|powershell`.

For VS Code syntax highlighting, including interpolated strings and embedded Go,
see the [local bork editor extension](editors/vscode/README.md).

Standard packages may use pinned Go modules. Generated builds use Go's module cache; populate it while online before building with `GOPROXY=off`. See the [dependency and offline build contract](docs/std-go.md).

## Status

Early design and a first compiler. See [docs/requirements.md](docs/requirements.md) for what has been decided, [docs/roadmap.md](docs/roadmap.md) for the plan, and [docs/grammar.md](docs/grammar.md) for the syntax the compiler accepts today. The [backpressure proposal](docs/design/backpressure.md) describes bounded task pools and HTTP admission (implemented), with typed client failures implemented and shared retry budgets planned.

Binary data uses immutable `Bytes`: `utf8Bytes("hello")`,
`bytes([toByte(0), toByte(255)])`, and validated `utf8String(data)`.

## Standard packages

| Package | Description |
| --- | --- |
| [bork/archive](docs/std/archive.md) | ZIP and TAR Bytes codecs and file iteration |
| [bork/cli](docs/std/cli.md) | Derived record command-line options through boa |
| [bork/compress](docs/std/compress.md) | Gzip Bytes codecs and scoped file transfers |
| [bork/crypto](docs/std/crypto.md) | Hashes, HMAC, secure bytes and password hashing |
| [bork/embed](docs/std/embed.md) | Compile-time file and directory snapshots |
| [bork/encoding](docs/std/encoding.md) | Hex, base64 and CSV encoding |
| [bork/env](docs/std/env.md) | Environment variables and derived record configuration |
| [bork/fs](docs/std/fs.md) | Scoped files, directories and filesystem operations |
| [bork/http](docs/std/http.md) | Scoped HTTP clients, routes, TLS, bounded admission and shutdown |
| [bork/json](docs/std/json.md) | Dynamic JSON, pretty printing and JSON Lines |
| [bork/log](docs/std/log.md) | Structured logging through Go slog |
| [bork/math](docs/std/math.md) | Float math, exact integers, rationals and Decimal money |
| [bork/net](docs/std/net.md) | Scoped TCP and UDP sockets |
| [bork/process](docs/std/process.md) | Argv processes, captured output and scope cancellation |
| [bork/rand](docs/std/rand.md) | Random draws and immutable seeded generators |
| [bork/regex](docs/std/regex.md) | Compiled RE2 patterns, captures and String facts |
| [bork/sql](docs/std/sql.md) | Scoped SQLite/Postgres connections and transactions |
| [bork/tasks](docs/std/tasks.md) | Shared bounded task pools with typed nonblocking admission |
| [bork/time](docs/std/time.md) | Instants, durations and injectable clocks |
| [bork/url](docs/std/url.md) | Immutable URLs, repeated query parameters and escaping |
| [bork/uuid](docs/std/uuid.md) | Canonical UUIDs, generation and JSON string codecs |

## License

[MIT](LICENSE)

Sequences keep ordered work lazy: `generate[Int] { for (n in Seq.range(0, 10)) { yield n * n } }.take(3).toList()` produces `[0, 1, 4]`. Constructing a `Seq[T]` runs no producer code; each traversal invokes it again. `List.toSeq()`, `map`, `filter`, `flatMap`, `take` and `drop` defer work until `for`, `forEach`, `fold`, `first` or `toList` consumes it. `Seq.unfold(seed, step)` uses a pure step returning `Option[SeqStep[T, S]]`. `Seq[T] uses io` carries effects that consumption must declare. Consumers can `break`, `continue`, `return` or use `?`; stopping closes active producer scopes. `fs.Lines`, `fs.Entries`, `sql.Rows[T]` and `sql.RowsJson` reopen their input on each traversal and yield errors as explicit elements.

Lists also support ordered, bounded parallel work: `xs.parMap(x => x * x,
workers: 4)` accepts pure callbacks. `xs.parMapIn(s, (child, x) => work(child,
x))` runs effectful work in a scope and reports cancellation as a value.
`parFilter`, `parFlatMap`, `parForEach`, and their scoped `In` forms follow
that pattern; `parMapUntil` stops on a failure value. See the
[parallel list example](examples/parallel_lists/main.bork) and
[design](docs/requirements.md#parallel-collections-implemented-bork-pd7rjm).

Compile-time dependency assembly wires ordinary provider functions by their
signatures: `assemble[Server](app, newConfig, openDb, newServer)`. Shared
dependencies build once per call, effects and failures stay checked, and resources
belong to the explicit scope. `assembleAll[T]` collects several providers and
`assembleRecord[R]` assembles a record's fields. Reuse wiring with
`providers Services = { config: newConfig, database: openDb, server: newServer }`
and specialize an entry with `Services(database: fakeDb)` at an assembly call. See the
[SQLite and HTTP service example](examples/assemble/main.bork) and
[assembly semantics](docs/requirements.md#compile-time-dependency-assembly).

Task lists provide ordered `awaitAll()`, `awaitFirst(s)`, and failure-aware
`awaitAllUntil[Success, Failure](s)`. `race(s, [child => work(child)])`
cancels and joins losing work; `withTimeout(s, ms, child => work(child))`
applies a cooperative deadline. Map heterogeneous channel receive arms with
`receiveCase`, then call `arms.select(s)` to receive from exactly one. See the
[task fan-in example](examples/task_fanin/main.bork).
