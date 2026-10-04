# bork

> **Pre-alpha.** The language is still changing, and programs may need updating between versions.

**bork** is a programming language for backend services. Its compiler checks more than types: that a value was validated before it is used, which functions do I/O, that a file or connection is still open, and that every case of a result is handled. Programs compile to a single executable.

*Bork bork bork!* The name comes from the [Swedish Chef](https://en.wikipedia.org/wiki/Swedish_Chef). Strict recipes, cheerfully enforced.

## Install

bork compiles through Go, so [Go](https://go.dev/dl/) 1.26 or later must be installed.

```sh
go install github.com/GiGurra/bork/cmd/bork@latest
```

Update explicitly with `bork upgrade`, or choose a release with `bork upgrade v0.4.0`. This runs `go install` into `BORKBIN` (shown by `bork env BORKBIN`) and reports the old and installed versions. Add that directory to `PATH`. See [upgrades](docs/cli.md#upgrades).

## Start a project

```sh
bork new hello
cd hello
bork run .
bork test .
```

Choose `--template cli`, `--template http`, or `--template lib` for other starting points. See [creating projects](docs/cli.md#new).

## Hello, world

Put this in `hello.bork`:

```bork
fn main() {
  println("Hello from bork!")
}
```

```sh
bork run hello.bork      # compile and run
bork build hello.bork    # compile to an executable
```

The [tour](docs/tour.md) continues from here. Read the [documentation online](https://gigurra.github.io/bork/). For a single file with top-level statements, use `bork script hello.bork`; see [scripts](docs/language/scripts.md).

On Linux and macOS, checks and builds automatically cache unchanged compiler results and pure predicate answers. See [the compile cache](docs/cli.md#the-compile-cache).

Package values such as `MaxRetries = 3` are immutable and pure, computed once on first read, or baked as data when read by `comptime`. See [bindings and package values](docs/language/basics.md).

Method chains can span lines with a leading dot, such as `.map(...)`; see [methods](docs/language/basics.md#methods).

## A taste of the language

### Records, unions, and exhaustive matching

Values are immutable. A function that can fail returns a union of its outcomes, written with `|`, and `match` must handle every one of them. A value that may be missing is an `Option`. There is no null and there are no exceptions.

```bork
type User = { name: String, email: Option[String] }
type NotFound = { id: Int }

fn findUser(id: Int): User | NotFound {
  if (id == 1) {
    User { name: "Ada", email: Option.None }
  } else {
    NotFound { id: id }
  }
}

fn describe(id: Int): String {
  match (findUser(id)) {
    User { name, email: Option.Some { value } } => s"$name <$value>"
    User { name } => name
    NotFound { id: missing } => s"no user with id $missing"
  }
}
```

Without the last arm, the program does not compile: `match is not exhaustive: missing NotFound`. More on [types](docs/language/types.md) and [matching](docs/language/matching.md).

### Facts

A fact is something the compiler has proven about a value. A predicate, declared with `pred`, is a function that returns a Bool. A parameter can require one with `where`, and then every caller has to show that it holds.

```bork
pred positive(x: Int) { x > 0 }

fn transfer(amount: Int where positive): String {
  s"sent $amount"
}

fn payOut(amount: Int): String {
  if (positive(amount)) { transfer(amount) } else { "nothing to send" }
}
```

Calling `transfer(amount)` without the check is a compile error, and so is `transfer(0)`:

```text
transfer requires amount to be positive, but that is not proven for amount
transfer requires amount to be positive, but positive(0) is false
```

More on [facts](docs/language/facts.md).

### Effects

A function's signature says what it does to the outside world: `uses io`, `net`, `clock`, `random`, or `state`. A function that declares nothing is pure, and the compiler holds it to that. Only `main` and tests may use effects without saying so.

```bork
fn greeting(name: String): String {
  s"Hello, $name!"
}

fn greet(name: String) uses io {
  println(greeting(name))
}
```

Printing inside `greeting` would be an error: `greeting uses io (it calls println), but its signature allows no effects`. More on [effects](docs/language/effects.md).

### Scopes and tasks

Files, connections, and tasks belong to a scope. A task is a piece of work that runs concurrently, started with `spawn`. When the scope ends, its files are closed and its tasks have finished, whether the work succeeded or not. Using a file after its scope has ended is a compile error.

```bork
import "bork/fs"

fn size(path: String) uses io: Int | fs.Error {
  scope s {
    fs.ReadAllText(fs.Open(path, s)?)?.byteLength()
  }
}

fn main() {
  sizes = scope s {
    tasks = ["a.txt", "b.txt"].map(path => spawn(s, () => size(path)))
    tasks.map(t => await(t))
  }
  println(sizes)
}
```

The `?` returns an error to the caller and keeps the successful value. More on [scopes and tasks](docs/language/scopes.md).

### Compile-time evaluation

A `comptime` block runs while the program compiles, and its result is stored in the executable as plain data.

```bork
fn square(n: Int): Int {
  n * n
}

fn main() {
  squares = comptime { range(1, 6).map(square) }
  println(squares)
}
```

More on [compile-time evaluation](docs/language/comptime.md).

### Typed string interpolation

`s"..."` builds a String. A library can define its own prefix that keeps the inserted values apart from the literal text. `sql.SQL` sends them to the database as bound parameters, so they are never spliced into the SQL text.

```bork
import "bork/sql"

type User = { name: String } derive (Decode)

fn find(db: sql.Connection, name: String) uses io + net: List[User] | sql.Error | DecodeError {
  sql.SQL"SELECT name FROM users WHERE name = $name".Query[User](db)
}
```

The library also checks the literal while compiling. Writing `'$name'` in quotes is rejected: `SQL hole is inside quoted text or an identifier`. More on [typed interpolation](docs/language/interpolators.md).

## Editor support

The [VS Code extension](editors/vscode/README.md) combines syntax highlighting
with `bork lsp`: diagnostics for unsaved edits, types and facts on hover,
navigation, completion, formatting and compiler fixes. Other LSP clients can
launch `bork lsp` over stdio.

## Learn more

- [Documentation](docs/README.md): the tour, a page for each part of the language, and the command-line reference.
- [Standard packages](docs/std/README.md): files, HTTP, JSON, SQL, time, and more.
- [Dependency tooling](docs/cli.md#deps): pin Go and bork libraries in `bork.mod` with `bork deps`; commit `bork.sum` and generated `go.mod`.
- [Examples](docs/examples.md): runnable programs, from `wc` to an HTTP service.
- [Contributing and design notes](docs/contributing.md): the grammar, requirements, and design documents.

## License

[MIT](LICENSE)
