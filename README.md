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

So do scopes:

```
fn countFile(path: String): Counts | IoError {
  scope s {
    countText(readAll(openFile(path, s)?)?)   // the file is closed when s ends, even by ? or a panic
  }
}

fn broken(path: String): String | IoError {
  f = scope s { openFile(path, s)? }
  readAll(f)             // error: f may be released: it belongs to scope s, which ended on line 2
}
```

See [examples/wc](examples/wc/main.bork), a small `wc`.

## Goals

- **Pragmatic high correctness for backend systems.** That is the whole point.
- **Immutable by default.** Values don't change. In v0.1 there is no mutable state at all; later versions may add explicit opt-in mutable types.
- **No null, no exceptions.** Optional values must be checked before use, and failures are values. Panics exist, but can't be caught.
- **Strict rules, trivially easy.** Declaring what must hold ("amount is positive", "user is non-nil", "list is non-empty") should be as cheap as writing an `if`. The compiler proves it at every call site or fails the build. This builds on ideas from [proven](https://github.com/GiGurra/proven).
- **Functional style.** Algebraic data types, pattern matching, expressions over statements, and first-class functions.
- **Go-like tooling.** One binary with commands like `bork build`, `bork test`, `bork fmt`. Fast builds. No build-system archaeology.
- **Compiles to Go** (first version). Go is only a compilation target: we get its runtime, GC, goroutines, and cross-platform builds without writing our own backend. bork code does not import Go packages; the explicit boundary is `unsafe go` function bodies.

## Non-goals

- Minimal runtime size or no-GC.
- Maximum efficiency on embedded devices.
- Being a proof assistant. Guarantees must stay pragmatic.

## Getting started

The compiler is at an early stage, but usable for small programs. Done: the plain language (M0: functions, sized numbers with checked conversions, records with nested `copy`, sealed types, unions, `Option`, `?`, `match`, string interpolation, `unsafe go` bodies), generics, lambdas, `List`, and `|>`; facts (M1: predicates, `where`, guards, rules, compile-time checks, and test mode); scopes and resources (M2, for one routine); packages; and type classes with JSON decoding (M3, under way: `derive (Decode)` decodes requests into proven values); and a first standard package, `bork/http` (see [examples/signup_api](examples/signup_api/main.bork)). It compiles bork to Go, so [Go](https://go.dev/dl/) must be installed.

```bash
go install github.com/GiGurra/bork/cmd/bork@latest

bork run examples/hello      # compile and run
bork build examples/hello    # compile to an executable
bork check examples/hello    # type-check only
bork emit examples/hello     # show the generated Go
bork test examples/payments  # run the tests, checking trusted facts and rules
```

A directory is a package. To use several, put a `bork.mod` naming the module (`module example.com/shop`) at its root, and import packages by path: `import "example.com/shop/money"`, then `money.Cents(250)`.

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

More examples: [calculator](examples/calculator/main.bork) (a parser and evaluator), [orders](examples/orders/main.bork) (validation and pricing), and [accounts](examples/accounts/main.bork) (a state machine). A short one, from [examples/users](examples/users/main.bork):

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

## Status

Early design and a first compiler. See [docs/requirements.md](docs/requirements.md) for what has been decided, [docs/roadmap.md](docs/roadmap.md) for the plan, and [docs/grammar.md](docs/grammar.md) for the syntax the compiler accepts today.

## License

[MIT](LICENSE)
