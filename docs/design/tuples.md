# Tuples and ordinary provider bundles

Approved design for bork-ys21yg (2026-10-05). Tuples are immutable,
heterogeneous positional values. They introduce no keyword or named builtin type.
Implementation is split into core tuples, then assembly/test helpers and migration.

## Syntax and typing

- Literals: `(a, b)` and `(a, b, c)`, with an optional trailing comma.
- Singleton: `(a,)`. `(a)` remains a parenthesized expression.
- Types: `(Int, String)` and `(Int,)`; `(Int)` remains a grouped type.
- No empty tuple in this change. `()` remains valid as the parameter list in
  `() => expr`, but is not a value or a type. `Ok` retains its existing role.
- Existing lambdas and function types keep their syntax: `(a, b) => expr` and
  `(Int, String) => Bool` are functions, distinguished by the following arrow
  (and the existing effects syntax for function types). A comma inside parentheses
  alone does not classify a function. Parser recovery handles unfinished forms.
- Element access: `pair.0`, `pair.1`, with zero-based decimal integer selectors.
  Selectors are unsigned integer tokens, not arbitrary expressions, and must be
  in range. Chaining such as `nested.0.1` must lex as selectors rather than floats;
  ordinary decimal float literals keep their existing meaning.
- Binding destructuring: `(a, b) = pair`, including `_`, singleton and nested
  tuple patterns. Bindings must be irrefutable, so nested refutable patterns are
  rejected. Explicit annotations can use `pair: (Int, String) = ...`; destructured
  element constraints follow the existing restrictions on constrained patterns.
- Match patterns: `(p, q)` and `(p,)`, with existing wildcards, literals, variant,
  type and nested patterns in each position. Arity must match; exhaustiveness and
  reachability combine the existing element pattern rules. Grouped patterns keep
  their existing meaning, and tuple patterns do not use record labels.

Each literal position is inferred independently. An expected tuple type supplies
an expected type to the corresponding element (including lambdas and literals);
arity mismatches are errors. Without an expected type, normal element inference
applies, including normal monomorphic function-value rules. Tuples are structural:
arity and ordered element types determine identity. Generic substitution,
unification, aliases, unions and class constraints recurse into elements. A tuple
return is one value, so functions and calls use the ordinary return/call rules.
No variadic type parameters, implicit flattening, named positions or implicit
conversion to a list/record are introduced.

## Structural operations and codecs

Equality compares corresponding elements using bork equality, and is available
only when all elements support Eq. Tuple map keys use the corresponding structural
hash operation; rendering uses `(a, b)` / `(a,)` and element Show instances.
There is no automatic ordering for tuples in this change.

Structural Encode/Decode instances are synthesized when every element has the
required instance, using the dictionaries selected in the current instance scope.
JSON representation is an array with exactly the tuple's arity. Decode rejects
non-arrays, missing or extra elements, reports failing element paths as `[0]`,
`[1]`, etc., and checks element facts before success. Nested tuple paths compose
with the existing record/list paths. A tuple containing a function has neither
Eq nor codecs merely because it is a tuple.

Tuples nested in derived records/sealed variants work through these structural
instances. A named tuple alias may request `derive (Encode, Decode)` using the
same element requirements and representation; it does not acquire named fields.
GoStruct derivation remains restricted to its existing supported declarations;
a tuple is not a public Go record contract. Unsupported derive classes retain
ordinary diagnostics, rather than silently generating a different representation.

## Evaluation, facts, effects and scopes

Literal elements evaluate once, left to right. A projection evaluates its receiver
once. Constructing/copying a tuple is pure; evaluation effects and ambient needs
come from its elements. Function elements retain their invocation effects/needs;
storing a function does not call it. Package tuple values follow the existing
package-value memoization, initialization effects and export rules.

Element constraints are checked at construction, parameter/result boundaries and
Decode, as for record fields. Tuple projections use stable positional identities
so facts and scope provenance survive projection and destructuring. Tuple aliases,
branches and returns preserve the existing fact rules. Declared-function metadata
is retained per element when a function is directly stored in a tuple literal,
including immutable local/package aliases and imports; it is not guessed through
arbitrary runtime functions or opaque branches. This allows ordinary package
provider tuples to retain declaration facts, parameter names and ambient needs.
Other elements remain ordinary function values with their checked signatures.

Scope analysis recurses through tuple elements, including nested tuples and
closures capturing resources. A tuple cannot make a scope-owned value safe to
use outside its scope. Projection/destructuring retains the corresponding owner;
passing, returning, package initialization and storing tuples enforce the same
lifetime restrictions as other containers. OwnedScope move/borrow restrictions
also recurse; a tuple cannot duplicate ownership. Swapping an element recomputes
provenance from the retained elements and replacement.

## Go lowering

A checked tuple lowers to a structural Go anonymous struct with exported generated
fields `E0`, `E1`, etc., whose field types are the lowered element types. Identical
bork tuple shapes share the same Go representation. A structural tag preserves
identity when Go erases union members or function effects, with union alternatives in canonical
order. Generic call and record boundaries convert between the declared Go layout
and the checked specialization, retagging tuple values and converting elements
when a generic union collapses. The tag has no runtime value. Tuple results remain
a single Go result value. Literals
and destructuring use temporaries where necessary to preserve evaluation order
and avoid duplicate execution. Projections lower to `.E0`, etc. Match lowering
reuses record/variant pattern machinery with positional fields, and debug source
maps point at the tuple expression, element or pattern being executed. Existing
structural equality, hash, Show, schema and codec generation recurse over tuples.

## Assembly splicing

`assemble[T](s, p1, p2)` remains valid. All three assembly forms also accept
ordinary tuple expressions in provider argument positions:

```text
ServiceProviders = (newService, openDb, newConfig)
assemble[Service](app, ServiceProviders)
assembleAll[Plugin](app, Plugins, extraPlugin)
assembleRecord[Application](app, (newServer, newWorker), Services)
```

Splicing depends on the statically known checked tuple type, not constant contents:
literals, package/imported values, local aliases, function results and Swap results
are accepted when the tuple shape and concrete function signatures are known.
Each tuple is spliced one level in positional order. Nested tuples, lists, union
shapes and unresolved generic functions are invalid provider elements; no recursive
flattening or runtime graph construction. Multiple tuples and individual providers
can be mixed, with all ordinary duplicate, missing, unused and cycle checks after
expansion. Existing target and scope restrictions remain unchanged.

The scope and provider expressions evaluate once in written order; tuple fields
are read from that result, then selected providers run in graph order as today.
Tuple creation stores provider functions, not their products. Package bundles
therefore still create fresh products for each assembly. Function values retain
ordinary ambient-need capture rules: constructors with uncaptured needs must stay
inline assembly providers or take explicit parameters. Tuples do not defer capture
until assembly. A tuple expression's
construction effects are charged even if a particular provider is rejected/unused.

Diagnostics name both the flattened provider number and its origin, for example
`provider #3 Services.1`, and point to the literal element when available. For a
package value or computed tuple, point at its use and attach the defining element
location when available. Graph describe/JSON output includes tuple origin and
position while retaining the existing graph information.

## Test replacement helpers

Add standard package `bork/test`, with ordinary qualified exports backed by
compiler-checked, shape-polymorphic helpers (no public magic tuple type):

```text
test.Swap(providers, replacement)
test.SwapAt(providers, 0, replacement)
```

Both return a new tuple of the original type, retain the other positions, evaluate
the tuple then replacement exactly once, and do not call any provider. SwapAt
also checks its index expression as a pure compile-time Int constant. These
helpers are available in ordinary code importing the package, not restricted to
special test syntax. There is no assemble-specific replacement syntax.

Swap chooses the unique position whose element type is identical to the checked
replacement type. Ordinary transparent aliases compare by their resolved types;
nominal types remain distinct. Function type identity includes parameters, result
(including failure union), effects and other checked signature contracts. This is
not a match on the provider's success product and performs no adaptation. An
unannotated lambda is checked normally; annotate it when its parameters or result
need a type. The fixture example works when the slot has type `() => Service`:
`assemble[Service](app, test.Swap(ServiceProviders, () => fixture))`.

Exact errors:

- Both functions require the declared positional argument count, a nonempty tuple
  first argument, and reject named or explicit type arguments.
- Swap rejects zero identical elements and reports the replacement and tuple types.
- Swap rejects two or more identical elements and reports their zero-based indices,
  suggesting SwapAt. Matching duplicates do not silently pick the first element.
- SwapAt requires a compile-time Int index in `[0, arity)`; nonconstant, negative,
  out-of-range or non-Int indices are errors at the index argument.
- SwapAt checks replacement with the selected element's expected type and rejects
  any differing type. No union widening, product-only comparison or signature
  change is permitted. Diagnostics point at the replacement and describe the slot.
- Existing lifetime/fact requirements still apply at the replacement position.

The previous provider-specialization facility allowed changed dependencies,
effects and failures with the same success product. The fixed ticket decisions
replace that with exact element types; migration must explicitly explain this
behavior change. Tests needing a different signature build a tuple literal with
the desired providers instead.

## Removing providers declarations

Retain a narrow parser recovery path for legacy `providers Name = { ... }` to
emit a migration diagnostic, not a usable declaration. Drop its contextual keyword
status, symbols, specialized calls, checker/codegen contracts and describe/editor
metadata. Ordinary functions/bindings named providers become legal.

Offer a compiler-owned fix from `providers Name = { label: fn, ... }` to
`Name = (fn, ...)`, including singleton comma, preserving order and comments.
Only offer the fix when the declaration can be rewritten safely. Empty/ambiguous
forms receive guidance without a fix. For legacy `Name(label: replacement)` uses,
report the replacement of bundle specialization by test.Swap/SwapAt or an explicit
tuple; do not emit a misleading mechanical fix where signature changes were legal.
Migrate all repository examples/tests/docs and any bundled sources together with
removal. Old positive bundle goldens become tuple cases; retain negative migration
cases covering the fix, comments, singleton and unsafe-to-rewrite cases.

## Delivery and syntax checklist

PR 1: tuple AST/type/checking, patterns, lowering, structural operations/codecs,
scopes/facts, formatter, tooling, grammars, focused positive/negative goldens and
reader documentation. PR 2: assembly splicing/origins, bork/test helpers, provider
removal/fixes, migration, docs and focused assembly goldens. The second builds on
the first after it reaches main. Each gets cold review and full CI; local tests
remain focused, with lint/gofmt and manually inspected golden changes.

Apply every entry in docs/syntax-changes.md:

- Compiler: token/lexer (numeric selectors and legacy contextual keyword), parser/
  AST/recovery, checker type/fact/effect/scope traversals, gen and debug mappings,
  formatter/idempotence, describe, prelude/std; regenerate stdvalidators if stale.
- Compiler-backed tools: completion/query metadata and recovery, import organization,
  extraction/capture identities and spans, inlay hints, signature help, semantic
  classifications/legend, LSP rename/navigation/fixes/snippets, lint/suppression.
  The tuple worker owns these updates.
- Playground: real compiler/WASM parity and build; check bork new templates.
- TextMate grammar/tests and VS Code editing behavior; tree-sitter grammar/scanner,
  generated sources, highlights/locals/indents/folds/injections; Vim/Neovim lexical
  grammar/tests; Emacs font-lock/indentation/imenu/tree-sitter tests; shared query
  snapshots for nvim/helix/zed via sync-queries.py; Helix/Zed grammar pins and
  upstream patches/pin tests. The tuple worker owns this syntax delta.
- Formal grammar, requirements, README, docs/language/types.md, matching.md,
  collections.md, facts.md, scopes.md, testing.md, packages.md, assembly reader
  page, docs/std test-package page/index, tour and examples. Keep bork blocks
  executable for TestDocSnippets; format every changed bork source.
- Cover grouped expressions/types versus tuples/lambdas/functions, singleton,
  generics/inference, selectors, nested patterns/exhaustiveness, equality/map keys/
  Show/codecs/derived containers, effects/evaluation order, constrained elements,
  scope/ownership escapes, imports/package values, assembly graph contracts,
  Swap identity/ambiguity/positions and migration diagnostics/fixes. Editor grammar
  suites must parse/highlight every added case; check fmt and playground parity.
