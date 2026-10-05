# bork documentation

bork is a programming language for backend services. These pages describe the language as it works today.

## Start

- [A tour of bork](tour.md): install the compiler and build a small program step by step.
- [Browser playground](playground.md): check code, inspect types, and format a file without installing anything.
- [Examples](examples.md): complete programs to read and run.

## The language

Each page covers one area in plain terms, with code you can run.

| Page | What it covers |
| --- | --- |
| [Scripts](language/scripts.md) | Executable single files, top-level statements, shebangs, and inline Go dependencies |
| [Basics](language/basics.md) | Names, functions, expressions, numbers, strings, runes, lambdas, and methods |
| [Types](language/types.md) | Records, sealed types, unions, `Option`, `Ok`, and generics |
| [Matching and errors](language/matching.md) | `match`, patterns, exhaustiveness, and `?` |
| [Collections](language/collections.md) | Lists, maps, lazy sequences, bytes, and parallel list operations |
| [Facts](language/facts.md) | Predicates, `where`, and what the compiler proves |
| [Effects](language/effects.md) | `uses`, pure functions, and ambient values |
| [Scopes and tasks](language/scopes.md) | Resources, tasks, channels, shared state, cancellation, process signals, and `lazy` and `async` bindings |
| [Channels](language/channels.md) | Sending and receiving between tasks, closing, producers, `select`, timeouts, and pipelines |
| [Derivation templates](language/derivation.md) | Custom class derivation with typed shape metadata |
| [Compile-time evaluation](language/comptime.md) | `comptime` blocks and reading files at build time |
| [Typed interpolation](language/interpolators.md) | `s"..."`, `sql.SQL"..."`, and defining your own prefix |
| [Packages and type classes](language/packages.md) | Packages, modules, imports, type classes, derived instances, and dependency assembly |
| [Calling Go](language/go-interop.md) | `unsafe go`, bindings to Go functions, and Go dependencies |
| [Testing](language/testing.md) | Tests, snapshots, mocks, and property tests |

## Reference

- [The bork command](cli.md): every command, the settings, and the compile cache.
- [Standard packages](std/README.md): files, HTTP, JSON, SQL, time, and more.
- [Editor support and VS Code](../editors/vscode/README.md): `bork lsp` checks unsaved buffers and supplies navigation, completion and formatting.
- [JSON diagnostics](diagnostics.md), [watch mode](watch.md), and [compiler code queries](describe.md): the compiler's interfaces for editors and tools.

## For contributors

- [Contributing and design notes](contributing.md): the grammar, the full requirements, and the design documents.
