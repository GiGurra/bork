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

Facts already work:

```
pred positive(x: Int) { x > 0 }

fn transfer(amount: Int where positive): Receipt { ... }

fn payOut(amount: Int) {
  transfer(amount)       // error: transfer requires amount to be positive, but that is not proven for amount
}                        //   (check it first with if (positive(amount)) { ... }, or require it: amount: Int where positive)

fn payOutChecked(amount: Int) {
  if (positive(amount)) { transfer(amount) }   // proven by the guard
}

transfer(0)              // error: transfer requires amount to be positive, but positive(0) is false
```

Predicates are ordinary bork functions; on constants, the compiler runs them at build time. See [examples/payments](examples/payments/main.bork).

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

## Goals

- **Pragmatic high correctness for backend systems.** That is the whole point.
- **Immutable by default.** Values don't change. In v0.1 there is no mutable state at all; later versions may add explicit opt-in mutable types.
- **No null, no exceptions.** Optional values must be checked before use, and failures are values. Panics exist, but can't be caught.
- **Strict rules, trivially easy.** Declaring what must hold ("amount is positive", "user is non-nil", "list is non-empty") should be as cheap as writing an `if`. The compiler proves it at every call site or fails the build. This builds on ideas from [proven](https://github.com/GiGurra/proven).
- **Methods for value operations.** `xs.filter(f).map(g)`, `text.trim()`, and `m.put(k, v)` need no imports. Use `|>` for free functions and lambdas when passing a method operation as a value.
- **Functional style.** Algebraic data types, pattern matching, expressions over statements, and first-class functions.
- **Go-like tooling.** One binary with commands like `bork build`, `bork test`, `bork fmt`. Fast builds. No build-system archaeology.
- **Compiles to Go** (first version). Go is only a compilation target: we get its runtime, GC, goroutines, and cross-platform builds without writing our own backend. bork code does not import Go packages; the explicit boundary is `unsafe go` function bodies, with a [stable Go helper API](docs/std-go.md) for standard packages, and bindings that call a Go function directly, checked against its Go signature (`fn Atoi(s: String): Int | GoError unsafe go "strconv.Atoi"`). Opaque Go types (`type Request = go "*net/http.Request"`) share boxed Go values through checked function and method bindings; Go resources are owned and closed by a scope.

## Non-goals

- Minimal runtime size or no-GC.
- Maximum efficiency on embedded devices.
- Being a proof assistant. Guarantees must stay pragmatic.

## Getting started

The compiler is at an early stage, but usable for small programs. Done: the plain language (M0: functions, sized numbers with checked conversions, records with nested `copy`, sealed types, unions, `Option`, `?`, `match`, string interpolation, `unsafe go` bodies), generics, lambdas, `List`, `Map`, methods (`xs.filter(f).map(g)`, references such as `words.map(String.byteLength)`), and `|>`; facts (M1: predicates, `where`, guards, rules, compile-time checks, and test mode); scopes and resources (M2, for one routine); packages; and type classes with JSON decoding (M3, under way: `derive (Decode)` decodes requests into proven values); and standard packages `bork/http`, `bork/log`, `bork/time`, and `bork/env` (see [examples/http_server](examples/http_server/main.bork), a REST API, and [examples/signup_api](examples/signup_api/main.bork), JSON requests with constrained fields). A `Show[T]` instance in a declared type's own package customizes its text consistently, including inside generic code and nested values. List and map keys use structural equality independently of their text. Unordered maps print deterministically (numeric and string keys by value, other keys by their text, with mixed kinds grouped). It compiles bork to Go, so [Go](https://go.dev/dl/) must be installed.

```bash
go install github.com/GiGurra/bork/cmd/bork@latest

bork run examples/hello      # compile and run
bork build examples/hello    # compile to an executable
bork check examples/hello    # type-check only
bork describe main.bork:3:9 --where notEmpty  # query a type and prove a fact
bork emit examples/hello     # show the generated Go
bork test examples/payments  # run the tests, checking trusted facts and rules
bork test --update .         # run the tests, writing the snapshots assertSnapshot finds missing or changed
bork test --auto-properties examples/payments  # also call trusted functions on generated arguments
bork fmt examples           # format .bork files recursively in place
bork fmt --check examples    # exit 1 if formatting would change a file
```

For tools and agents, `bork check --json path | jq` emits one diagnostic per line on stdout. `bork build --json` and `bork test --json` emit the same JSON Lines on stderr, leaving stdout for test reports. Successful compilation emits no diagnostics; compilation errors still exit with status 1. See [the diagnostic format](docs/diagnostics.md) for codes, positions, and suggested text edits.

`bork fmt [paths...]` defaults to the current directory and prints changed paths.
It uses two spaces for indentation, normalizes spacing and blank-line runs, and keeps
existing line breaks and comment text. Strings, interpolations, and raw `unsafe
go` bodies are preserved verbatim. Directory traversal skips hidden directories,
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

More examples: [http_server](examples/http_server/main.bork) (a REST API with JSON, an atom of persistent maps for state, and logging), [signup_api](examples/signup_api/main.bork) (an endpoint whose requests decode into proven values), [calculator](examples/calculator/main.bork) (a parser and evaluator), [orders](examples/orders/main.bork) (validation and pricing), and [accounts](examples/accounts/main.bork) (a state machine). A short one, from [examples/users](examples/users/main.bork):

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
  user.copy(address.city = city)
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

HTTP clients take a scope (`http.Get(url, s, timeoutMs = 0)`); cancellation and optional timeouts cover the response body too. Request/response headers are `Map[String, List[String]]`, preserving repeated header values.

`bork/sql` opens SQLite or Postgres connections in scopes, rolls uncommitted transactions back on scope exit, binds query parameters, and decodes rows into proven records. See [examples/sql](examples/sql/main.bork).

## Status

Early design and a first compiler. See [docs/requirements.md](docs/requirements.md) for what has been decided, [docs/roadmap.md](docs/roadmap.md) for the plan, and [docs/grammar.md](docs/grammar.md) for the syntax the compiler accepts today.

`bork/time` provides instants, durations, formatting/parsing, cancellable sleep, and injectable clocks. `bork/env` loads one environment variable per record field through `Decode`, checking facts and reporting all invalid or missing variables. See [examples/time_env](examples/time_env/main.bork).

Binary data uses immutable `Bytes`: `utf8Bytes("hello")`,
`bytes([toByte(0), toByte(255)])`, and validated `utf8String(data)`.
Import `bork/encoding` for hex and standard or URL-safe base64; malformed
input returns `ParseError`. See [the encoding example](examples/bytes_encoding/main.bork).

Import `bork/encoding` for CSV. `encoding.DecodeCsv[T]` reads header-based records
using each field's declared type and facts, collecting errors by row and column.
`EncodeCsv` writes empty optional cells as None (Some("") cannot round-trip); [the CSV example](examples/csv/main.bork) shows both.

Import `bork/process` to run argv commands with captured Bytes output, an exit
code, optional environment/workdir/stdin, and scope-owned cancellation. SIGINT
and SIGTERM cancel root scopes so scope-aware work can finish and clean up.
See [examples/process](examples/process/main.bork).

Import `bork/net` for scope-owned TCP/UDP sockets, binary data and UTF-8 lines,
per-call timeouts, and address helpers. See [examples/net](examples/net/main.bork).

## License

[MIT](LICENSE)
