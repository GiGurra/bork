# bork documentation

bork is a programming language for backend services. These pages describe the language as it works today.

## Start

- [Installing bork](install.md): Go, `go install`, Homebrew, release archives, and upgrades.
- [A tour of bork](tour.md): build a small program step by step.
- [Build a service](tour-service.md): a SQLite-backed JSON API, handler tests, and graceful shutdown.
- [Browser playground](playground.md): check code, inspect types, and format a file without installing anything.
- [Examples](examples.md): complete programs to read and run.

## The language

Each page covers one area in plain terms, with code you can run. Follow the Previous/Next links through the same order.

| Page | What it covers |
| --- | --- |
| [Scripts](language/scripts.md) | Executable single files, top-level statements, shebangs, and inline Go dependencies |
| [Basics](language/basics.md) | Names, rebinding, functions, control flow, loops, tail calls, and printing |
| [Types](language/types.md) | Records, sealed types, unions, `Option`, `Ok`, and generics |
| [Matching and errors](language/matching.md) | `match`, patterns, exhaustiveness, and `?` |
| [Adding error context](language/errors.md) | Wrapping failures at a single `?` |
| [Collections](language/collections.md) | Lists, maps, lazy sequences, bytes, and parallel list operations |
| [Facts](language/facts.md) | Predicates, `where`, and what the compiler proves |
| [Effects](language/effects.md) | `uses`, pure functions, and ambient values |
| [Scopes and tasks](language/scopes.md) | Resources, tasks, channels, shared state, cancellation, process signals, and `lazy` and `async` bindings |
| [Channels](language/channels.md) | Sending and receiving between tasks, closing, producers, `select`, timeouts, and pipelines |
| [Packages and type classes](language/packages.md) | Packages, modules, imports, type classes, derived instances, and dependency assembly |
| [Libraries](language/libraries.md) | Dependencies, publishing, private modules, and reusable packages |
| [Compile-time evaluation](language/comptime.md) | `comptime` blocks and reading files at build time |
| [Typed interpolation](language/interpolators.md) | `s"..."`, `sql.SQL"..."`, and defining your own prefix |
| [Derivation templates](language/derivation.md) | Custom class derivation with typed shape metadata |
| [Calling Go](language/go-interop.md) | `unsafe go`, bindings to Go functions, and Go dependencies |
| [Testing](language/testing.md) | Tests, snapshots, mocks, and property tests |

## Quick references

- [Cheat sheet](cheatsheet.md): everyday syntax and useful commands.
- [Cookbook](cookbook.md): complete programs for common backend tasks.
- [FAQ and pitfalls](faq.md): compiler diagnostics, explanations, and checked fixes.
- [Coming from another language](coming-from.md): Go, TypeScript, Rust, and Python comparisons.

- [Built-in APIs](std/builtins.md): generated signatures and comments for functions and methods available without imports.

## Reference

- [The bork command](cli.md): every command, the settings, and the compile cache.
- [Standard packages](std/README.md): files, HTTP, JSON, SQL, time, and more.
- [Editors](editors.md): VS Code, Neovim, Vim, Emacs, Helix, Zed, and any LSP client. The [VS Code extension](../editors/vscode/README.md) has its own page.
- [Debugging](debugging.md): breakpoints and stepping in bork source.
- [Debugger arithmetic rounding](debugger-rounding.md): Float and Float32 evaluation guarantees.
- [JSON diagnostics](diagnostics.md), [watch mode](watch.md), and [compiler code queries](describe.md): the compiler's interfaces for editors and tools.

## For contributors

- [Contributing and design notes](contributing.md): the grammar, the full requirements, and the design documents.
