# bork

> **Pre-alpha / design phase.** Nothing here compiles yet. We are formulating requirements and functionality.

**bork** is a pragmatic backend language of guarantees.

Think Go's tooling simplicity, with the functional style of Scala and Haskell, and correctness guarantees that anyone can write, not just the type-theory crowd.

*Bork bork bork!* The name comes from the Swedish Chef. Strict recipes, cheerfully enforced.

## Goals

- **Pragmatic high correctness for backend systems.** That is the whole point.
- **Immutable by default.** Values don't change. Mutation is an explicit opt-in, confined to specific mutable types.
- **Strict rules, trivially easy.** Declaring what must hold ("amount is positive", "user is non-nil", "list is non-empty") should be as cheap as writing an `if`. The compiler proves it at every call site or fails the build. This builds on ideas from [proven](https://github.com/GiGurra/proven).
- **Functional style.** Algebraic data types, pattern matching, expressions over statements, and first-class functions.
- **Go-like tooling.** One binary with commands like `bork build`, `bork test`, `bork fmt`. Fast builds. No build-system archaeology.
- **Compiles to Go** (first version). We reuse Go's runtime, GC, concurrency, and ecosystem, and get the backend language right before worrying about anything else.

## Non-goals

- Minimal runtime size or no-GC.
- Maximum efficiency on embedded devices.
- Being a proof assistant. Guarantees must stay pragmatic.

## Status

Early design. Requirements and design docs will land under `docs/` as they take shape.

## License

[MIT](LICENSE)
