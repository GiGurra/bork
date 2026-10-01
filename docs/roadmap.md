# bork roadmap

> How we get from the requirements in [requirements.md](requirements.md) to a working v0.1 compiler. The milestones are ordered so the core idea (facts on immutable values) is tested early.

## Guiding decisions

- **bork's syntax is its own.** The compiler has a hand-written parser for bork's grammar. Go's parsers are never used on bork source.
- **The first compiler is written in Go.** A self-hosting compiler in bork may come later.
- **The compiler models bork; the output just runs.** All checking happens on the compiler's own model of the language. The Go output is a lowering that may drop checked knowledge, use runtime helpers, and generate support functions. It is not a one-to-one mapping of bork types to Go types. Inspired by TypeScript.
- **Go output is generated with Go's own AST tools** (`go/ast`, `go/printer`), not by gluing strings, so the output is always syntactically valid and consistently formatted. This only affects how Go is written, never how bork looks.
- **Example programs are the spec.** Each milestone is defined by `.bork` programs it must compile and run (or reject, with specific diagnostics).

## Compiler pipeline

1. **Lex.** Tokens with positions, keeping comments (needed later for `bork fmt` and editor support). Automatic statement termination at line ends.
2. **Parse.** Hand-written recursive descent into a syntax tree. Priority on error recovery and clear messages.
3. **Load packages and resolve names.** Go-style: directory = package, imports by module path, circular imports rejected. Each package is compiled on its own against summaries of the packages it imports.
4. **Type check.** Base types only, with every `where` clause ignored: records, unions, generics, local inference, lambdas, and exhaustive `match`.
5. **Check facts.** Collect obligations (constrained parameters, constrained record construction, resource use), and resolve each one backwards from its use site through guards, `match`es, bindings, declared results, inference rules, and scopes. Unresolved obligations become diagnostics.
6. **Evaluate at compile time.** Predicates on compile-time-known values run inside the compiler. That requires a small interpreter for bork's lowered form, which can be reused for compile-time blocks.
7. **Lower.** Desugar into a small intermediate form: `?` into explicit branches, `match` into tests, scopes into finalizers, derived instances into generated functions. Facts are erased here.
8. **Generate Go**, plus a small bork runtime package written in Go (unions, persistent collections, scopes and finalizers, panics). `//line` directives point Go's errors, panics, and stack traces back at `.bork` source.
9. **Build.** The `bork` CLI runs `go build` on the generated code. Go must be installed (or bundled later).

Each package writes a summary (exported types, signatures with their constraints, instances) that importing packages read, similar to proven's sidecars.

## Testing

- **Golden-file end-to-end tests from day one.** Each case under `testdata/cases/` is a `.bork` program plus either its expected output or its expected diagnostics.
- **Diagnostics are tested like output.** Friendly, specific error messages are a core value, so their text is part of the expected results.
- **Unit tests** for the lexer, parser, type checker, and fact resolver.

## Milestones

### M0: a plain language

Functions, records, unions and sealed types, `match` with exhaustiveness, `Option`, `?` (leftmost), immutable bindings, `Int`/`String`/`Bool`, printing. bork → Go → binary.

*Done when* small programs using all of the above compile, run, and print the expected output, and non-exhaustive matches are rejected.

### M1: facts

`pred`, `where` on parameters and returns, constrained type aliases, guards and `match` as fact sources, the backward resolver, "callers prove or declare", inference rules, `trust`, and compile-time checks of literals.

*Done when* the constrained-input examples in the requirements compile, and each kind of unproven call fails with a clear diagnostic. This milestone proves the core idea; if it does not hold up in practice, we find out before building the rest.

### M2: scopes

`scope` blocks, resources attached to scopes, finalizers in reverse order, the proof-of-open-scope rule, and "possibly released" diagnostics. A minimal file API is the first resource.

*Done when* use-after-scope and escaping resources are rejected at compile time, and finalizers run correctly on normal exit and on panic.

### M3: type classes

Type classes, instances (including on constrained types), explicit instance-scope imports, ambiguity errors, and `derive` (`Eq`, `Show`, `Decode`).

*Done when* a request record with constrained fields decodes from JSON into already-proven values.

### Later

Concurrency and structured concurrency, crash isolation and supervision, user-defined scoped facts, compile-time blocks, `bork fmt`, editor support, Go interop, other compilation targets.

## Decisions to settle before or during M0

- The open syntax questions in [requirements.md](requirements.md): the colon in `name: Type`, `fn` vs `func`, shadowing, parentheses around conditions, `match` vs `switch`, comments and string literals.
- A grammar draft (EBNF).
- The first example programs that define M0.
