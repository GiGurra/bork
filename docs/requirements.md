# bork requirements

> Living document. Each section is worked through in discussion and recorded here once agreed.
> **Decided** items are commitments for v0.1. Open questions are listed per section.

## Scope of v0.1

v0.1 is deliberately feature-sparse. It exists to prove the core idea: **when everything is immutable, every proven fact stays true**, so correctness checks are cheap, local, and permanent. Anything that doesn't serve that idea waits.

## 1. Core values

In priority order. When two values conflict, the higher one wins.

1. **If it compiles, whole classes of bugs cannot happen.** No null or nil, no unchecked access to optional values, no exceptions, no non-exhaustive matches, no mutable state (v0.1), and no broken contracts.
2. **Rigor must be cheap.** Declaring a rule should cost about as much as writing an `if`. Haskell-level guarantees without Haskell-level ceremony. If a guarantee is hard to express, that is a language bug.
3. **Explicit escape hatches, never silent bypasses.** Whenever a guarantee is weakened, it is visible in the code, greppable, and reviewable.
4. **Readable at 3am during an incident.** Explicit over clever. One obvious way to do common things.
5. **Boring, fast tooling.** One `bork` binary, fast builds, and a formatter with no options.
6. **Friendly, specific diagnostics.** Errors say what could not be proven, where, and how to fix it.

## Decided

### Language fundamentals

- **Statically typed.**
- **No null, no nil.** The concept does not exist in the language.
- **`Option` is statically checked.** The value inside an `Option` cannot be extracted without first checking that it is present (via `match`, or a guard the compiler understands). There is no `.get()`-style unchecked extraction.
- **No mutable state in v0.1.** Every value is immutable. This is the foundation of the proof model: a fact proven about a value can never be invalidated later, so proofs are never lost to mutation. Opt-in mutable types may come in a later version.
- **Exhaustive matching is enforced.** A `match` that does not cover every case is a compile error. The compiler never inserts an implicit panic for a missing case.

### Errors and failure

- **No exceptions. At all.** There is no `throw`, no `try`/`catch`. Expected failures are values (a `Result`-style type).
- **Panics exist but are discouraged.** A panic means a bug, not an expected failure.
- **No `recover`.** A panic cannot be caught in bork code. Erlang-style isolation of routines (a failing routine brought down without taking the rest with it) may be introduced later.

### Effects and concurrency

- **Side effects are allowed.** Reading and writing files, sockets, and so on are ordinary operations.
- **No effect or state monads.** No `IO`, `State`, or time monads. Concurrency uses Go's goroutines (virtual threads), so sequential blocking code is the norm, and there is no need to thread effects through types.

### Compilation target and Go

- **Compiles to Go.** Go is purely a compilation target, chosen so bork gets Go's runtime, GC, goroutine scheduler, and cross-platform compilation without building its own backend.
- **No access to Go's standard library or Go packages from bork code.** bork code never calls Go directly. bork has its own standard library. The Go code the compiler generates, and the bork standard library's implementation, can freely use Go's standard library under the hood. That is an implementation detail, invisible to bork programs.
- **No FFI in v0.1.** Calling into Go (or anything else) from bork may come later, as an explicit boundary.

### General

- **Strict evaluation**, not lazy. Laziness makes memory and performance hard to reason about in backend systems.
- **A short error-propagation operator** (like Rust's `?`) so `Result` handling stays cheap. Exact syntax to be settled with errors and effects.
- **No user-defined symbolic operators** (e.g. `|+|`, `>>=`).
- **No implicit resolution magic** (e.g. Scala 2 implicits).
- **Performance target:** roughly Go-level performance, traded away for guarantees where needed.

## 2. Type system

### Decided

- **Records and sum types (ADTs) are the core data types.** Records are product types. Sum types are tagged unions whose variants can carry data.
- **Generics in v0.1.** User code can declare generic types and functions, not just use built-in ones like `Option[T]` and `Result[T, E]`.
- **Variance: planned, not in v0.1.** Scala-style declaration-site variance controls (covariant and contravariant type parameters) are planned for a later version. The syntax should leave room for them.
- **Structural typing for interfaces and constraints.** A type satisfies an interface or generic constraint implicitly, by having the required shape (like Go). No `implements` declarations.
- **Sealed types and traits are opt-in.** Marking a type sealed closes its set of variants to its own declaration. That is what makes exhaustive matching possible without a default case. Matching on an open (unsealed) type always needs a default case.
- **Immutable persistent collections** in the standard library: `List`, `Map`, `Set`.
- **Pragmatic local type inference, like Go and Scala.** Function signatures are written out, which doubles as documentation, and local values are inferred.
- **Scala-style inline lambdas.** Lambda parameter types are inferred from the expected type at the call site, so `users.map(u => u.name)` needs no annotations.

### Open questions

- Scala-style placeholder shorthand for lambdas (e.g. `_.name`), or always named parameters?
- Can a sealed type's variants be spread over several files in one package, or must they sit in one declaration?

## Open questions

- Should "rigor must be cheap" rank above "if it compiles, bugs cannot happen", meaning a guarantee is dropped if it cannot be made cheap?
- Limited operator overloading (e.g. `+` for money or vector types), or none at all?
- Other principles to adopt: strong backwards-compatibility promises? No macros?

## Topics still to discuss

1. ~~Core values~~ (above)
2. ~~Type system~~ (above)
3. Contract and proof model: bringing proven's ideas into the language
4. Errors and effects: the `Result` shape and propagation syntax
5. Concurrency: goroutines, channels, and the (later) isolation model
6. Go interop: no FFI in v0.1; what the future boundary looks like
7. Tooling: the `bork` CLI, formatter, tests, and modules/packages

## Explicitly not in v0.1

- Mutable state of any kind
- FFI and importing Go packages
- `recover`
- Erlang-style routine isolation and supervision
