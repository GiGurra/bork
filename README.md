# bork

> **Pre-alpha.** The language is still changing, and programs may need updating between versions.

**bork** is a programming language for backend services. Its compiler checks more than types: that a value was validated before it is used, which functions do I/O, that a file or connection is still open, and that every case of a result is handled. Programs compile to a single executable.

*Bork bork bork!* The name comes from the [Swedish Chef](https://en.wikipedia.org/wiki/Swedish_Chef). Strict recipes, cheerfully enforced.

Fixed-width integers support bitwise `&`, `|`, `^`, Go-style complement `^x`, and shifts with checked nonnegative counts; see [numbers](docs/language/basics.md).
[bork/bits](docs/std/bits.md) adds integer bit counts, rotations, reversal, and checked bit fields.

## Install

bork compiles through Go. Install [Go](https://go.dev/dl/) 1.21 or later with
automatic toolchain switching enabled (the Go default). Bork needs Go 1.26+
and asks Go to download a suitable toolchain when your installed Go is older.
The first install or build may need network access; cached toolchains work offline.
With `GOTOOLCHAIN=local`, install Go 1.26+ yourself. See
[Go toolchains](docs/cli.md#go-toolchains).

```sh
go install github.com/GiGurra/bork/cmd/bork@latest
```

Prebuilt archives for Linux, macOS and Windows (amd64 and arm64) are available on [GitHub Releases](https://github.com/GiGurra/bork/releases), with `checksums.txt` and the VS Code `.vsix`. Extract the compiler archive and put `bork` (`bork.exe` on Windows) on `PATH`. Go is still required to compile programs.

On macOS or Linux with Homebrew:

```sh
brew install gigurra/tap/bork
```

The formula installs Go as a runtime dependency. Use `brew upgrade bork` to update an installation managed by Homebrew; `bork upgrade` detects Homebrew installations and directs you to that command.

Update explicitly with `bork upgrade`, or choose a release with `bork upgrade v0.4.0`. This downloads a release binary, verifies its SHA-256 checksum, and installs it into `BORKBIN` (shown by `bork env BORKBIN`). It reports progress and the old and installed versions; use `--from-source` to build with Go instead. Add that directory to `PATH`. See [upgrades](docs/cli.md#upgrades). Interactive commands can offer a quiet daily update notice; disable it with `bork env -w BORKUPDATECHECK=off`.

Projects can set a minimum compiler with `bork 0.4` in `bork.mod`. Older compilers automatically install and use a suitable release through Go's module proxy. Use `BORKTOOLCHAIN=local` to disable switching, or `BORKTOOLCHAIN=v0.4.2` to pin an exact compiler. `bork version` and `bork env BORKVERSION` explain the selection. See [compiler versions](docs/cli.md#compiler-versions).

## Start a project

```sh
bork new hello
cd hello
bork run .
bork test .
```

Run `bork lint .` for advisory warnings and safe editor fixes; the language server shows the same warnings. See [lint](docs/cli.md#lint).

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

On Linux and macOS, checks and builds automatically cache unchanged compiler results and pure predicate answers. `run` and `script` reuse unchanged executables, and unchanged builds, runs and scripts start neither Go nor the compiler on a warm hit, including embeds, cgo and compile-time code. Use `--rebuild` for an exhaustive rebuild; `--fast` accepts reuse with untracked external inputs. See [the compile cache](docs/cli.md#the-compile-cache).

Package values such as `MaxRetries = 3` are immutable and pure, computed once on first read, or baked as data when read by `comptime`. See [bindings and package values](docs/language/basics.md).

Method chains can span lines with a leading dot, such as `.map(...)`; see [methods](docs/language/basics.md#methods).

## A taste of the language

### Records, unions, and exhaustive matching

Values are immutable. Reusing a name in the same block creates a new binding; closures retain captured values, and nested shadowing is forbidden. Unused locals are compile errors: discard explicitly with `_`. A function that can fail returns a union of its outcomes, written with `|`, and `match` must handle every one of them. A value that may be missing is an `Option`. There is no null and there are no exceptions.

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
    User { name, email: .Some { value } } => s"$name <$value>"
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

Ctrl+C and SIGTERM cancel root scopes and allow cleanup, then exit with 130 or 143.
[bork/signal](docs/std/signal.md) configures shutdown grace, subscriptions, and ignored signals.

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

type User = { name: String }
derive Decode for User

fn find(db: sql.Connection, name: String) uses io + net: List[User] | sql.Error | DecodeError {
  sql.SQL"SELECT name FROM users WHERE name = $name".Query[User](db)
}
```

The library also checks the literal while compiling. Writing `'$name'` in quotes is rejected: `SQL hole is inside quoted text or an identifier`. More on [typed interpolation](docs/language/interpolators.md).

## Testing shapes and facts

`value is Pattern` returns a Bool without binding names. Use
`assert(reply is .Found { text: _ })` for a shape assertion, or
`test.AssertIs[Int where positive](value)` from `bork/test` to return a typed
value with checked facts. See [matching](docs/language/matching.md) and
[testing](docs/language/testing.md).

## Editor support

The [VS Code extension](editors/vscode/README.md) combines syntax highlighting
with `bork lsp`: diagnostics for unsaved edits, types and facts on hover,
navigation, completion, formatting and compiler fixes. Install it with
`bork editor install vscode` (use `--editor cursor` or `--editor codium` for
those editors). See [editor setup](docs/editors.md) for other LSP clients.
The [debugger](docs/debugging.md) supports bork source breakpoints and shows records, union variants and options as bork values.
The shared [tree-sitter grammar](editors/tree-sitter-bork/README.md) provides
highlighting, indentation, folds and embedded Go queries. Native packages for
Vim, Neovim, Emacs, Helix and Zed are linked from [editor setup](docs/editors.md).

## Learn more

- [Documentation](docs/README.md): the tour, a page for each part of the language, and the command-line reference.
- [Integer bases](docs/std/strconv.md): hex, binary, octal, arbitrary radix, and checked parsing.
- [Standard packages](docs/std/README.md): files, HTTP, JSON, SQL, time, and more.
- [Typed CLI applications](docs/std/cli.md): proven options, field docs, configurable flag/environment mappings, and reusable metadata policies.
- [Package API documentation](docs/cli.md#doc): run `bork doc`, with `--all` for a module or `--html` for a standalone page; standard and cached pinned libraries work too.
- [Dependency tooling](docs/cli.md#deps): pin Go and bork libraries in `bork.mod` with `bork deps`; commit `bork.sum` and generated `go.mod`.
- [Library and consumer example](testdata/libraries/README.md): publish, pin and upgrade a Bork library, with an offline proxy test. Scripts accept Bork libraries through `bork:require`; editor navigation keeps cached sources read-only.
- [Examples](docs/examples.md): runnable programs, from `wc` to an HTTP service.
- [Contributing and design notes](docs/contributing.md): the grammar, requirements, and design documents.

## License

[MIT](LICENSE)

Tuples group heterogeneous values without a record declaration: `(3, "count")`,
with type `(Int, String)`, `.0` access and `(number, label) = pair` destructuring.
See [tuples](docs/language/types.md#tuples).
