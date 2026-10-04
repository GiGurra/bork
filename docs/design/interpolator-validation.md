# Compile-time interpolator validation (bork-hstt36)

Named interpolation gains an optional prelude class:

```bork
class InterpolationValidator[B] {
  fn validateInterpolation(parts: StaticParts, holes: List[InterpolationHole]): List[InterpolationIssue]
}
type InterpolationHole = { kinds: List[InterpolationKind] }
type InterpolationKind = sealed {
  Builtin { name: String }
  Named { packagePath: String, name: String }
  Unknown
}
type InterpolationIssue = { hole: Option[Int], message: String }
```

A library declares the instance in its builder type's own package. The compiler
automatically selects that owner-declared instance for the factory result type,
without requiring caller `use` or a SQL/compiler registration. The complete
dictionary graph resolves in that owner scope; caller dictionaries cannot alter
helper policy. Constrained validator instance heads are forbidden, since runtime
facts cannot decide whether compile-time validation is enabled. Caller scope cannot
replace or suppress the validator. An absent instance preserves the ordinary
factory/Interpolate/Finish protocol. Ambiguous or unsatisfied declared instances
are errors, not silently skipped. Concrete generic instances use ordinary class
resolution; unresolved runtime dictionaries/type arguments cannot be evaluated
and produce an explicit validation diagnostic rather than disabling validation.

The compiler creates the parts capability and metadata only. Builtin kinds use
language names; named kinds use canonical declaring package paths and nominal
names, independent of imports/aliases. A union lists all its possible kinds;
other shapes/open type parameters are Unknown. The validator receives no hole
values, builder, receiver, or factory callback. This preserves eager once-only
runtime evaluation and permits effectful holes and factories. Validators and
helper calls must be pure under existing effects/comptime checks.

Batch distinct validator calls per source package into one execution, preserving
source order in the result mapping. Memoize repeated (resolved validator identity,
parts, hole kinds) calls within the build and batch only distinct calls.
Cross-build reuse waits for comptime's certified execution receipts; no separate
persistent evaluator cache is introduced. Files with no validator-bearing interpolation pay zero
extra execution cost. Measure check time for a file with about 50 SQL sites
against main, including warm reuse and a changed-site miss.

Run validation with existing bounded native comptime/predicate execution and
value transport, including its timeout, output limits, dependency checks and
process cleanup. Return no issues to accept; each Some(index) names a zero-based
hole and reports the validator message at that hole expression's position, with
a short note naming the validator. None reports at the prefix
(for literal-only problems). Invalid indices/results, panic, timeout or failed
proofs reject compilation at the prefix with a validator failure diagnostic.
Validation does not add a runtime call or bake a user-visible capability.

## SQL checking

The SQL builder declares this class instance. SQL classifies its own Identifier
and Statement nominal kinds, with scalar kinds remaining parameter holes. Its
validator reuses the rendering scanner rather than introducing compiler SQL
rules or a second lexer. For the default SQL factory, reject definite boundary
errors that both SQLite and Postgres reject. This gives compile errors for
unambiguous quoted/comment holes, partial tokens, NUL text and unambiguous manual
placeholders such as `$1`. Dialect-dependent cases remain render errors: notably
`?` is a SQLite placeholder and a valid Postgres JSON operator, and Postgres array
punctuation/escape rules differ. No new dialect factories are required here.

A Statement hole represents runtime fragment text and parameters; metadata alone
cannot inspect it. Stop definite checking at such an unknown fragment boundary,
then defer that tail to the existing renderer. A union permitting Statement or
an Unknown kind also defers. Rendering still flattens composition and validates
all real boundaries for the actual driver dialect. Unterminated literal fragment
text can be completed by composition, so an end-of-input error alone is deferred;
known holes already inside a quote/comment are still rejected before that point.
Every literal component is checked from an unquoted SQL context. Known bound
and identifier holes inside quotes/comments or partial tokens, and literal manual
placeholders, must satisfy this component-local construction contract. This is
stricter than flattened rendering: wrapping a locally rejected component in
additional quotes cannot make its construction legal. Unknown fragment boundaries
still defer subsequent checking. This is an explicit SQL-library rule, not a
claim that the former renderer rejects every locally invalid component in every
possible outer context. Use the explicit Unsafe API for intentional raw text.

Runtime boundary fixtures that deliberately construct now-invalid literals move
to compile-error fixtures. Keep runtime composition, dialect ambiguity and lazy
stream error tests, plus accepted SQLite/Postgres literals. Add a non-SQL custom
validator and local/imported aliases, purity/effectful-hole evaluation-order
coverage, bad index/panic/timeout checks, and direct metadata tests.

The runnable SQL interpolation example's commented quoted-hole line becomes a
compile-error example. Document which checks occur at compile time and which
remain mandatory at render time, including runtime fragments and dialects.

User documentation includes a short validator-writing section and a table of
compile-time versus mandatory render-time checks.

## Measured checking cost

On this development server, optimized CLI binaries checked one file containing
50 distinct SQL literal sites against the branch's main baseline `e6b0e12`.
`BORK_CACHE=off` and `GOPACKAGESDRIVER=off`; Go's normal build cache was retained.
Three warm runs had median 0.059s on main and 0.402s with validation (0.343s added).
The first validation check took 0.409s; editing one site's literal took 0.404s.
These are one package batch with 50 calls, not 50 evaluator processes. There is
no cross-build validator-result cache: subsequent checks execute the batch again,
while Go can reuse its compiled dependencies. The focused regression checks that
repeated parts/kinds produce one call and no-validator programs launch no evaluator.
