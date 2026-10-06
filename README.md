# bork

> **Pre-alpha.** The language is still changing, and programs may need updating between versions.

**bork** is a programming language for backend services. Its compiler checks more than types: that a value was validated before it is used, which functions do I/O, that a file or connection is still open, and that every case of a result is handled. There is no null and there are no exceptions. Programs compile to a single executable.

*Bork bork bork!* The name comes from the [Swedish Chef](https://en.wikipedia.org/wiki/Swedish_Chef). Strict recipes, cheerfully enforced.

## A quick look

```bork
type User = { name: String, email: Option[String] }
type NotFound = { id: Int }

pred positive(x: Int) { x > 0 }

fn findUser(id: Int where positive): User | NotFound {
  if (id == 1) {
    User { name: "Ada", email: Option.None }
  } else {
    NotFound { id: id }
  }
}

fn describe(id: Int where positive): String {
  match (findUser(id)) {
    User { name, email: .Some(value) } => s"$name <$value>"
    User { name } => name
    NotFound { id: missing } => s"no user with id $missing"
  }
}

fn greet(id: Int) uses io {
  if (positive(id)) { println(describe(id)) } else { println("ids start at 1") }
}

fn main() {
  greet(1)
}
```

`findUser` returns one of two outcomes, and `match` must handle both. `describe` only accepts ids proven positive, and it is pure: only `greet` says it does I/O. Break any of these rules and `bork check users.bork` stops you:

```text
users.bork:15:3: match is not exhaustive: missing NotFound
users.bork:23:20: describe requires id to be positive, but that is not proven for id (check it first with if (positive(id)) { ... }, or require it: id: Int where positive)
users.bork:18:7: describe uses io (it calls println), but its signature allows no effects; declare it: uses io
```

Those come from deleting the `NotFound` arm, calling `describe(id)` without the `if`, and adding a `println` to `describe`.

## Install

bork needs [Go](https://go.dev/dl/) to compile programs. Then either:

```sh
go install github.com/GiGurra/bork/cmd/bork@latest
brew install gigurra/tap/bork
```

Prebuilt archives are on [GitHub Releases](https://github.com/GiGurra/bork/releases). See [installing bork](docs/install.md) for Go versions, upgrades and pinning a compiler version.

## Quick start

```sh
bork new hello      # or --template cli, http, or lib
cd hello
bork run .
bork test .
```

A single file works too: `bork run hello.bork`, or `bork script hello.bork` for a file with top-level statements. The [tour](docs/tour.md) continues from here.

## Highlights

- [Unions and exhaustive matching](docs/language/matching.md): failures are values, and every case is handled.
- [Facts](docs/language/facts.md): check a value once, and the compiler remembers it.
- [Effects](docs/language/effects.md): signatures say which functions do I/O, networking, or read the clock.
- [Scopes and tasks](docs/language/scopes.md): files, connections and concurrent tasks cannot outlive their scope.
- [Channels](docs/language/channels.md): typed communication between tasks, with `select` and timeouts.
- [Compile-time evaluation](docs/language/comptime.md): `comptime` blocks run while compiling and become data.
- [Typed interpolation](docs/language/interpolators.md): `sql.SQL"..."` keeps inserted values out of the query text.
- [Testing](docs/language/testing.md): tests live next to the code, with snapshots, mocks and property tests.
- [Standard packages](docs/std/README.md): files, HTTP, JSON, SQL, CLI apps, time, and more.
- [Fast rebuilds](docs/cli.md#the-compile-cache): unchanged programs start without recompiling.

## Editors and playground

- [VS Code](editors/vscode/README.md): install with `bork editor install vscode`, including Cursor and VSCodium.
- [Other editors](docs/editors.md): Neovim, Vim, Emacs, Helix, Zed and any LSP client, plus a [debugger](docs/debugging.md).
- [Browser playground](docs/playground.md): check and format code without installing anything.

## Documentation

- [Documentation home](docs/README.md), also [online](https://gigurra.github.io/bork/).
- [A tour of bork](docs/tour.md): build a small program step by step.
- [The language](docs/README.md#the-language): one page per area.
- [The bork command](docs/cli.md): every command and setting.
- [Examples](docs/examples.md): runnable programs, from `wc` to an HTTP service.
- [Contributing and design notes](docs/contributing.md).

## License

[MIT](LICENSE)
