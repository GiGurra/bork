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

**Status: done.** Everything below compiles and runs, plus sized numbers with checked conversions, runes, string interpolation, nested patterns, `unsafe go` function bodies, and a prelude of string, rune, and parsing functions. The example programs that define M0 are [calculator](../examples/calculator/main.bork) (a recursive-descent parser and evaluator), [orders](../examples/orders/main.bork) (validation and pricing), [accounts](../examples/accounts/main.bork) (a state machine), and [users](../examples/users/main.bork); their output is checked by the tests.

Functions, records, unions and sealed types, `match` with exhaustiveness, `Option`, `?` (leftmost), immutable bindings, `Int`/`String`/`Bool`, printing. bork → Go → binary.

*Done when* small programs using all of the above compile, run, and print the expected output, and non-exhaustive matches are rejected.

### M1: facts

**Status: nearly done.** Done:

- **Constraints:** `pred` (also generic); `where` on parameters, results (also per union member), record fields, constrained aliases, typed bindings, and inside type arguments (`List[Int where positive]`, `Option[...]`, generic records and sealed types); predicate arguments that are constants or parameters; predicate parameters (`List[T where keep]`); OR, with `and` and parentheses.
- **Fact sources:** guards (`if`, early `return`/`panic`, `&&`, `||`, `!`), callers' own requirements, callees' promises (also through `?` and `match`), derived results of helpers (through chains), fields and paths, `trust`, inference rules (with conditions, chains, and cycles), proofs by cases over OR facts, and parametricity (facts flow through generic functions and into lambdas).
- **Checking:** verified result promises; compile-time evaluation of any predicate on constants and literals, using the program's own code; diagnostics with fixes; test mode (`bork test`), where trusted facts (`trust`, and promises of `unsafe go` functions) are checked as the tests run.
- **Groundwork it needed:** generic functions and types, function types and lambdas, `List[T]`, `|>`, `Option` as a prelude type, and `test` declarations with `assert` and `assertEqual`.

Remaining: property tests for inference rules; the `CreateUser` example, which needs decoding (M3); and obligations across packages, which need packages.

`pred`, `where` on parameters and returns, constrained type aliases, guards and `match` as fact sources, the backward resolver, "callers prove or declare", inference rules, `trust`, and compile-time checks of literals.

*Done when* the constrained-input examples in the requirements compile, and each kind of unproven call fails with a clear diagnostic. This milestone proves the core idea; if it does not hold up in practice, we find out before building the rest.

#### Baseline

The M1 baseline has two parts, and both must hold:

1. **Every example in [requirements.md](requirements.md)** that involves facts: the core-idea example, the constrained-input examples (`transfer`, `connect` with `Port`, the `CreateUser` request, `clamp`, the refining `filter`), and "callers prove or declare" (`payOut`, `payOutChecked`, `payOutDeclared`). Each becomes a golden test, along with the failing variants it implies.
2. **Everything proven can do**, described below.

The target example programs (a JSON-over-HTTP endpoint with constrained request fields, a database handler using scopes, a small CLI) must also work by the end of the milestones that cover their features.

#### Everything proven can do

[proven](https://github.com/GiGurra/proven)'s examples and test cases (`example/`, `testdata/cases/`) are part of the minimum constraint functionality for M1. The concepts carry over; the syntax and implementation do not. bork must support:

- **Preconditions on parameters**, with several predicates on one parameter combined with AND.
- **Fact sources:**
  - a preceding check (`if p(x) { ... }`)
  - checks joined with `&&`
  - early-return and panic guards (`if !p(x) { return ... }`)
  - a function's own preconditions, which hold inside its body
  - a callee's results, including when the call is nested as an argument (`target(normalize(x))`)
  - boundary validation that yields a union result, where the fact holds only on the success path
  - trusted injection (`trust`), both for local values and for function results
- **Promised results:** derived from the body, and pinned in the signature, where a pinned promise is verified against the body so an edit cannot silently drop it.
- **Derived results through chains of helper functions**, independent of declaration order.
- **Inference rules:** with conditions (`Given`), several premises and conclusions, chained, and safe against cycles.
- **OR obligations and OR facts**, alongside AND.
- **Generic predicates:** one predicate (e.g. `nonEmpty`) applies to every type it fits.
- **Facts on fields and deeper paths** (`user.address.zip`).
- **Relations between several values.** proven packs them into a struct; bork can additionally use predicates whose arguments refer to other parameters.
- **Literals checked at compile time.** In proven this only works for its built-in predicates; in bork it works for every predicate.
- **Obligations across packages**, through package summaries.
- **Diagnostics** that name the predicate, the parameter, and the callee, and point at the call site.
- **Rejected cases:** unproven calls, a boundary check whose failure path is not handled, unknown predicates, and mismatched trusted predicates.
- **Test support:**
  - property tests for inference rules, which find counter-examples
  - a test mode that runs contracts at runtime, so tests can assert that a given input violates a given predicate (proven's drift defense)

What proven needed but bork does not: all mutation handling (invalidating facts on reassignment, field writes, `++`, address escapes). bork's immutability makes it unnecessary. proven's mutation test cases become "cannot happen" in bork, apart from shadowing, which must not affect facts on the outer binding.

proven's `testdata/cases/` (108 cases) are a good source of golden tests: each one, rewritten in bork syntax, is a ready-made test for M1.

### M2: scopes

`scope` blocks, resources attached to scopes, finalizers in reverse order, the proof-of-open-scope rule, and "possibly released" diagnostics. A minimal file API is the first resource.

*Done when* use-after-scope and escaping resources are rejected at compile time, and finalizers run correctly on normal exit and on panic.

### M3: type classes

Type classes, instances (including on constrained types), explicit instance-scope imports, ambiguity errors, and `derive` (`Eq`, `Show`, `Decode`).

*Done when* a request record with constrained fields decodes from JSON into already-proven values.

### Later

Concurrency and structured concurrency, crash isolation and supervision, user-defined scoped facts, compile-time blocks, `bork fmt`, editor support, Go interop, other compilation targets.

## Decisions to settle before or during M0

- The target example programs: a JSON-over-HTTP endpoint with constrained request fields, a database handler using scopes, and a small CLI.
