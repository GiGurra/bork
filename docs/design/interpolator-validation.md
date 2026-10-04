# Compile-time interpolator validation (bork-hstt36)

Named interpolation gains an optional prelude class:

```bork
class InterpolationValidator[B] {
  fn validateInterpolation(parts: StaticParts, holes: List[InterpolationHole]): List[InterpolationIssue]
}
type InterpolationHole = { kinds: List[InterpolationKind] }
type InterpolationKind = sealed {
  Builtin { name: String }
  Named { package: String, name: String }
  Unknown
}
type InterpolationIssue = { hole: Option[Int], message: String }
```

A library declares the instance in its builder type's own package. The compiler
automatically selects that owner-declared instance for the factory result type,
without requiring caller `use` or a SQL/compiler registration. Caller scope cannot
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

Run validation with existing bounded native comptime/predicate execution and
value transport, including its timeout, output limits, dependency checks and
process cleanup. Return no issues to accept; each Some(index) names a zero-based
hole and reports at that hole expression's position. None reports at the prefix
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
Local literal manual placeholders must be quoted in their own component or use
the explicit Unsafe API; nesting does not bypass this construction rule.

Runtime boundary fixtures that deliberately construct now-invalid literals move
to compile-error fixtures. Keep runtime composition, dialect ambiguity and lazy
stream error tests, plus accepted SQLite/Postgres literals. Add a non-SQL custom
validator and local/imported aliases, purity/effectful-hole evaluation-order
coverage, bad index/panic/timeout checks, and direct metadata tests.

The runnable SQL interpolation example's commented quoted-hole line becomes a
compile-error example. Document which checks occur at compile time and which
remain mandatory at render time, including runtime fragments and dialects.
