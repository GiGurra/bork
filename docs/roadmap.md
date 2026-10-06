# bork roadmap

> M0–M3 are completed milestones. This page records their acceptance criteria and the current compiler pipeline. The [language guide](language/basics.md) describes the implemented language; the [design notes](contributing.md#design-notes) identify active work and remaining proposals.

## Guiding decisions

- **bork's syntax is its own.** The compiler has a hand-written parser for bork's grammar. Go's parsers are never used on bork source.
- **The first compiler is written in Go.** A self-hosting compiler in bork may come later.
- **The compiler models bork; the output just runs.** All checking happens on the compiler's own model of the language. The Go output is a lowering that may drop checked knowledge, use runtime helpers, and generate support functions. It is not a one-to-one mapping of bork types to Go types. Inspired by TypeScript.
- **Go output is generated with Go's own AST tools** (`go/ast`, `go/printer`), not by gluing strings, so the output is always syntactically valid and consistently formatted. This only affects how Go is written, never how bork looks.
- **Example programs are the spec.** Each milestone is defined by `.bork` programs it must compile and run (or reject, with specific diagnostics).

## Compiler pipeline

1. **Lex.** Tokens with positions, keeping comments (needed later for `bork fmt` and editor support). Automatic statement termination at line ends.
2. **Parse.** Hand-written recursive descent into a syntax tree. Priority on error recovery and clear messages.
3. **Load packages and resolve names.** Go-style: directory = package, imports by module path, circular imports rejected. The loader builds the complete source/import graph; reusable package-interface checking remains a design goal.
4. **Type check.** Base types only, with every `where` clause ignored: records, unions, generics, local inference, lambdas, and exhaustive `match`.
5. **Check facts.** Collect obligations (constrained parameters, constrained record construction, resource use), and resolve each one backwards from its use site through guards, `match`es, bindings, declared results, inference rules, and scopes. Unresolved obligations become diagnostics.
6. **Evaluate at compile time.** Predicates on compile-time-known values and explicit `comptime` blocks use bounded native evaluators generated as Go programs. Eligible standard interpolation validators use compiler-embedded implementations. Closed pure proof batches have separate bounded reuse; explicit comptime values are baked into generated output.
7. **Lower.** Desugar into a small intermediate form: `?` into explicit branches, `match` into tests, scopes into finalizers, derived instances into generated functions. Facts are erased here.
8. **Generate Go**, plus a small bork runtime package written in Go (unions, persistent collections, scopes and finalizers, panics). `//line` directives point Go's errors, panics, and stack traces back at `.bork` source.
9. **Build.** The `bork` CLI runs `go build` on the generated code. Compiler/Go toolchain selection and executable reuse follow the [CLI contracts](cli.md).

Imported sources are checked in the complete loaded graph. Complete-program Session and disk caches reuse eligible results; serialized package-interface summaries and partitioned Go output remain proposals in the [incremental design](design/incremental.md).

## Testing

- **Golden-file end-to-end tests from day one.** Each case under `testdata/cases/` is a `.bork` program plus either its expected output or its expected diagnostics.
- **Diagnostics are tested like output.** Friendly, specific error messages are a core value, so their text is part of the expected results.
- **Ok tests** for the lexer, parser, type checker, and fact resolver.

## Milestones

### M0: a plain language

**Status: done.** Everything below compiles and runs, plus sized numbers with checked conversions, runes, string interpolation, nested patterns, `unsafe go` function bodies, and a prelude of string, rune, and parsing functions. The example programs that define M0 are [calculator](../examples/calculator/main.bork) (a recursive-descent parser and evaluator), [orders](../examples/orders/main.bork) (validation and pricing), [accounts](../examples/accounts/main.bork) (a state machine), and [users](../examples/users/main.bork); their output is checked by the tests.

Functions, records, unions and sealed types, `match` with exhaustiveness, `Option`, `?` (leftmost), immutable bindings, `Int`/`String`/`Bool`, printing. bork → Go → binary.

*Done when* small programs using all of the above compile, run, and print the expected output, and non-exhaustive matches are rejected.

### M1: facts

**Status: done.** Implemented:

- **Constraints:** `pred` (also generic); `where` on parameters, results (also per union member), record fields, constrained aliases, typed bindings, and inside type arguments (`List[Int where positive]`, `Option[...]`, generic records and sealed types); predicate arguments that are constants or parameters; predicate parameters (`List[T where keep]`); OR, with `and` and parentheses.
- **Fact sources:** guards (`if`, early `return`/`panic`, `&&`, `||`, `!`), callers' own requirements, callees' promises (also through `?` and `match`), derived results of helpers (through chains), fields and paths, `trust`, inference rules (with conditions, chains, and cycles), proofs by cases over OR facts, and parametricity (facts flow through generic functions and into lambdas).
- **Checking:** verified result promises; compile-time evaluation of any predicate on constants and literals, using the program's own code; diagnostics with fixes; test mode (`bork test`), where trusted facts (`trust`, and promises of `unsafe go` functions) are checked as the tests run, and every inference rule gets a property test that looks for counterexamples (on Int, the sized numbers, Float, String, and Bool variables).
- **Groundwork it needed:** generic functions and types, function types and lambdas, `List[T]`, `|>`, `Option` as a prelude type, and `test` declarations with `assert` and `assertEqual`.

Packages are in too: obligations across packages work (importers see what signatures declare, and compile-time checks run the imported predicates). JSON decoding validates constrained request fields, as demonstrated by [signup_api](../examples/signup_api/main.bork).

`pred`, `where` on parameters and returns, constrained type aliases, guards and `match` as fact sources, the backward resolver, "callers prove or declare", inference rules, `trust`, and compile-time checks of literals.

*Done when* the constrained-input examples in the requirements compile, and each kind of unproven call fails with a clear diagnostic. This milestone proves the core idea; if it does not hold up in practice, we find out before building the rest.

#### Baseline

The M1 baseline has two parts, and both must hold:

1. **Every example in [requirements.md](requirements.md)** that involves facts: the core-idea example, the constrained-input examples (`transfer`, `connect` with `Port`, the `CreateUser` request, `clamp`, the refining `filter`), and "callers prove or declare" (`payOut`, `payOutChecked`, `payOutDeclared`). Each becomes a golden test, along with the failing variants it implies.
2. **Everything proven can do**, described below.

The target example programs (a JSON-over-HTTP endpoint with constrained request fields, a database handler using scopes, a small CLI) must also work by the end of the milestones that cover their features. Done: the JSON-over-HTTP endpoint ([signup_api](../examples/signup_api/main.bork), with `bork/http`) and the CLI ([wc](../examples/wc/main.bork), [signup](../examples/signup/main.bork)).

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

What proven needed but bork does not: all mutation handling (invalidating facts on reassignment, field writes, `++`, address escapes). bork's immutability makes it unnecessary. proven's mutation test cases become "cannot happen" in bork. Same-block rebinding creates a new binding; nested shadowing is rejected. Facts stay attached to the value they describe.

proven's `testdata/cases/` (108 cases) are a good source of golden tests: each one, rewritten in bork syntax, is a ready-made test for M1.

### M2: scopes

**Status: done.** `scope s { ... }` blocks; resource types (`type File = resource`) made in `unsafe go`; finalizers that run last-first when a scope's block ends, returns early (also by `?`), or panics (a failing finalizer does not stop the others); `onClose`; a minimal file API (`openFile`, `createFile`, `readAll`, `write`); and lifetimes, which reject using a possibly released value, returning a value of a scope the function opened (also inside records, lists, and lambdas), and giving a scope something that may not live as long. Structured concurrency: `fork`/`await` start and join scoped tasks. Scope exit cancels tasks, joins them, then runs finalizers; a `taskTimeout` policy grants a bounded wait before cancellation. Cancellation through scopes (`cancel`, `cancelAfter`, `delay`, `checkpoint`, request scopes in `bork/http`), channels, and `attach`, which keeps a resource open for a task of an outer scope. **M2 is done.**

`scope` blocks, resources attached to scopes, finalizers in reverse order, the proof-of-open-scope rule, and "possibly released" diagnostics. A minimal file API is the first resource.

*Done when* use-after-scope and escaping resources are rejected at compile time, and finalizers run correctly on normal exit and on panic.

### M3: type classes

**Status: done.** Done: classes, named instances (also generic, with bounds), bounded type parameters, `use` of other packages' instances and of named sets of them (`instances Json { ... }`), ambiguity and missing-instance errors, explicit type arguments, format-independent codecs in `bork/codec` (`codec.Value`, `codec.Decode`, `codec.Encode`), and `derive (codec.Decode, codec.Encode)`, whose decoders check the fields' where clauses (the `signup` example decodes requests into proven values). `Eq` is built in (structural `==`, also on lists, and `[T: Eq]` bounds), so it and `Show` need no `derive`; and instances on constrained types, chosen for fields whose where clauses include them. M3 is done, including constrained instances at call sites (declared facts of the arguments, or an explicit constrained type argument, which also proves the result).

Type classes, instances (including on constrained types), explicit instance-scope imports, ambiguity errors, and open library-defined derivation. `codec.Decode`, `codec.Encode` and `GoStruct` are library derivations; structural `Eq` and default `Show` need no derive.

*Done when* a request record with constrained fields decodes from JSON into already-proven values.

### Implemented beyond M3

Structured concurrency, channel/select operations, explicit compile-time blocks,
open derivation, lazy/computed fields, loops and tail calls, `bork fmt`, language-server
editor support, Go interop, Bork library dependencies and compiler/Go toolchain
selection are implemented. Their current APIs are documented in the
[language guide](language/basics.md), [standard-library reference](std/README.md)
and [CLI](cli.md).

Package-level incremental checking, partitioned Go output and independently
cached comptime values remain design work. See the status banners in
[contributor design notes](contributing.md#design-notes) for individual boundaries.
The native compiler produces Go executables; the browser playground supports
checking and formatting rather than program execution.
