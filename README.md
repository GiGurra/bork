# bork

> **Pre-alpha.** The design is still evolving, and the compiler only handles a small first slice of the language.

**bork** is a pragmatic backend language of guarantees.

Think Go's tooling simplicity, with the functional style of Scala and Haskell, and correctness guarantees that anyone can write, not just the type-theory crowd.

*Bork bork bork!* The name comes from the Swedish Chef. Strict recipes, cheerfully enforced.

## Why bork over Go

bork compiles to Go and keeps Go's runtime, but adds three things Go can't give you:

- **Immutability.** Values never change, so whatever is known about them stays true.
- **Facts.** What you check about a value becomes part of its type, and the compiler proves every function's requirements at every call site.
- **Scopes.** Outside resources (files, connections, transactions, leases) belong to scopes, and using one requires proof that a scope managing it is still open.

## Goals

- **Pragmatic high correctness for backend systems.** That is the whole point.
- **Immutable by default.** Values don't change. In v0.1 there is no mutable state at all; later versions may add explicit opt-in mutable types.
- **No null, no exceptions.** Optional values must be checked before use, and failures are values. Panics exist, but can't be caught.
- **Strict rules, trivially easy.** Declaring what must hold ("amount is positive", "user is non-nil", "list is non-empty") should be as cheap as writing an `if`. The compiler proves it at every call site or fails the build. This builds on ideas from [proven](https://github.com/GiGurra/proven).
- **Functional style.** Algebraic data types, pattern matching, expressions over statements, and first-class functions.
- **Go-like tooling.** One binary with commands like `bork build`, `bork test`, `bork fmt`. Fast builds. No build-system archaeology.
- **Compiles to Go** (first version). Go is only a compilation target: we get its runtime, GC, goroutines, and cross-platform builds without writing our own backend. bork code does not import Go packages, and there is no FFI in v0.1.

## Non-goals

- Minimal runtime size or no-GC.
- Maximum efficiency on embedded devices.
- Being a proof assistant. Guarantees must stay pragmatic.

## Getting started

The compiler is at an early stage, covering most of milestone M0: functions, sized integers and floats with checked conversions, `Bool`/`String` with a small prelude of string and parsing functions, bindings, `if`/`match`/blocks as expressions, records with nested `copy`, sealed types, unions, `Option`, `?`, and `unsafe go` function bodies for calling Go. Facts and scopes come next. It compiles bork to Go, so [Go](https://go.dev/dl/) must be installed.

```bash
go install github.com/GiGurra/bork/cmd/bork@latest

bork run examples/hello      # compile and run
bork build examples/hello    # compile to an executable
bork check examples/hello    # type-check only
bork emit examples/hello     # show the generated Go
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

A larger example, from [examples/users](examples/users/main.bork):

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
    u: User => u.name + " now lives in " + u.address.city
    NotFound { id: missing } => "no user with id " + toString(missing)
    e: DbError => "database error: " + e.message
  }
}

fn main() {
  println(describe(1))
  println(describe(2))
  println(describe(-1))
}
```

Shell completion (via [boa](https://github.com/GiGurra/boa)): `bork completion bash|zsh|fish|powershell`.

## Status

Early design and a first compiler. See [docs/requirements.md](docs/requirements.md) for what has been decided, [docs/roadmap.md](docs/roadmap.md) for the plan, and [docs/grammar.md](docs/grammar.md) for the syntax the compiler accepts today.

## License

[MIT](LICENSE)
