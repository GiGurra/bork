# Pattern tests and typed test assertions

Ticket: bork-878uk7. Implement alongside positional variants (bork-3ic73j),
coordinating the `bork/test` package with tuples. This includes
the human's addition requiring fact-qualified tests and assertions.

## `is` expressions

`x is Pattern` returns Bool, evaluates `x` once, and reuses match-pattern
typing and lowering without exhaustiveness checking. Accept every match
pattern that introduces no bindings: types, qualified/context variants,
literals, wildcards and nested record/list/tuple/payload patterns. Reject bare
binding names, bound type patterns, shorthand field bindings and named rest
bindings; use `_` and explicit field patterns when ignoring values. Match
remains the way to extract values. `is` does not narrow surrounding bindings or
introduce branch-local facts in this change.

Parse `is` as a contextual infix keyword at comparison precedence, above `&&`
and `||`. Existing declarations, calls and selectors named `is` remain valid;
do not add it to the unconditional lexer keyword map. A bare name after `is`
must resolve as a type, rather than becoming a binding. Context variants use
the tested value's type and the same candidate/visibility rules as match.

Allow direct fact-qualified type tests, including constrained aliases:
`x is Int where positive and atMost(100)`. Test the erased type first, then
evaluate the predicates in source order with ordinary `and`/`or`
short-circuiting. Predicate panics propagate. Resolve relational arguments from
the current scope and check predicate parameter requirements and purity using
the existing bound-type-pattern machinery; unavailable names get ordinary
scope diagnostics. Retain existing errors for unsupported nested constraints
instead of silently dropping them.

A well-formed statically impossible type/literal/variant test evaluates false
and warns; a statically exhaustive test, including already-proven predicates,
warns that it always succeeds. An unknown variant, inaccessible constructor,
unknown field or unsupported constraint is still an error. Static decisions
must preserve evaluation of the operand. Use stable warning codes and source
ranges suitable for JSON diagnostics and lint/editor presentation.

## `test.AssertIs[T](value)`

Expose the requested compiler-backed helper in `bork/test`, alongside tuple
swaps. Its target is the one explicit type argument; infer the input type
independently. On success, return the value narrowed to T. On mismatch, fail
with the actual value, its bork type and the expected type/failed predicates.
Evaluate the input once. Use the existing test assertion failure path and
caller location so helpers and parallel tests keep correct attribution.

The target may carry direct facts:
`test.AssertIs[Int where positive and atMost(100)](x)`. Check the erased type,
run the predicates, and attach those proven facts to the returned value for
later calls and rule implications. Relational facts retain their checked
argument identities. Facts apply only after every required predicate passes;
failure returns no value. Reuse existing type/effect matching restrictions,
including function and sequence types, so runtime type erasure cannot create
an unsupported guarantee. A statically guaranteed success warns as for `is`.

The compiler recognizes the helper by the standard package's resolved identity,
not an arbitrary function's spelling. This permits its independently typed
input and checked target facts without relaxing generic-call constraints for
other APIs. Use `test.AssertIs` under the ordinary uppercase export convention, matching
`test.Swap` (lead clarification: the lowercase spelling was a sketch).
Describe, completion, signatures, navigation and inlay hints must report the
actual instantiated target/input types. Do not add `assertMatches` now;
document `assert(value is Pattern)` for arbitrary shape assertions.

## Delivery and validation

Follow every applicable row of [the syntax-change checklist](../syntax-changes.md).
Add syntax/formatter/editor grammar coverage, nested/context/positional tests,
binding rejection, impossible/guaranteed warnings, runtime constrained success
and failure, relational facts, short-circuit order, one-time operand evaluation,
predicate requirements and panics, returned fact propagation, assertion failure
text/locations, parallel-test attribution and compiler-owned API identity tests.
Update grammar/specification, matching/testing reader pages, representative
match-only assertion examples and compiler-backed tooling. Run focused checks,
lint and formatting locally; CI supplies the full suite. Cold review before
reporting the PR ready.
