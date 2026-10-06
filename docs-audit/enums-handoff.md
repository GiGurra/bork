# Enums implementation handoff (bork-rz5wv6)

For the worker who implements [docs/design/enums.md](../docs/design/enums.md)
after the human approves it. Read the design note first; this file only adds
the decisions, the PR order and what to watch for in the code.

## Status of decisions

The lead sent these to the human on 2026-10-06. Check the PR #409 thread or
ask the lead for the answers, and update this table before starting.

| # | Decision | Recommended | Human |
| --- | --- | --- | --- |
| 1 | Enum-shaped sealed types encode as bare strings, no opt-out | yes | pending |
| 2 | Marker: `Other(String) codec { fallback: true }` | yes | pending |
| 3 | Compiler invariant: the fallback's String is never a wire name or alias | yes | pending |
| 4 | Strictness via `T where enum.known`; accept the shared-DTO gap | yes | pending |
| 5 | `codec.Value` fallback for payload-carrying sealed types | later | pending |
| 6 | CLI rejects unknown names even with a fallback | yes | pending |

Agreed with peers (not human decisions):

- codecnames (#408) owns tag groups, `codec.VariantTags { name, aliases,
  fallback }`, naming policies, and the checks: one fallback, payload is one
  String, no name/aliases on the fallback, and no YAML-unsafe wire names
  (`true/false/null/~/on/off`).
- The cli worker reads `FieldSchema.variants: List[codec.VariantSchema { name, doc }]`,
  matches exactly, and never offers or accepts the fallback.

## Dependencies

- PR 1 below does not depend on #408. Start there.
- PRs 2 to 4 need #408's tag groups (`variant.tagged[codec.VariantTags]()`)
  and wire names. If #408 hasn't merged, coordinate with codecnames rather
  than adding a second tag mechanism.

## PR breakdown

### PR 1: bare-string wire form (decision 1)

- `internal/std/codec/codec.bork`, `encodeShape`: in the `shape.Sealed`
  branch, when every variant has no fields, encode `Value.String { value: name }`
  instead of the `{"type": ...}` object. Add a `derive fn` helper
  (`EnumShaped[T]()`) using `comptime for` over `shape.variants[T]()` and
  `variant.fields.isEmpty()`. Check that the metadata evaluator can fold it in
  a `comptime if` (derivation.md lists what it supports: Bool ops, `length()`
  and `isEmpty()` are fine).
- `DecodeSealed` already accepts a bare string. Keep the object form working.
  Improve the unknown-name error to list expected names:
  `unknown variant "purple" of Color; expected one of Red, Green, Blue`.
- Goldens that change (grep for `{"type":"X"}`): `testdata/cases/derive`,
  `testdata/cases/yaml_typed`, and maybe `derive_packages`, `positional_variants`,
  `lazy_*_codecs`, `describe_queries`. Check each diff by hand. Mixed sealed types
  must NOT change.
- Docs: `docs/std/codec.md` ("Records encode as objects..." paragraph),
  `docs/std/json.md` and `docs/std/yaml.md` if they show the shape.
- Run `go generate ./internal/stdvalidators` if its staleness test asks.

### PR 2: fallback in codec (decisions 2, 3)

- Encode: the fallback variant writes its String payload verbatim.
- Decode order: wire names, aliases, then the fallback. Inputs that fall back
  are a bare string or `{"type": "x"}` with no other members; anything else
  is an error. A known name never falls back.
- Invariant (decision 3): the checker adds a generated fact on the fallback's
  payload: "not one of the wire names/aliases". Look at how variant `where`
  clauses become obligations (`type_invariants_*` cases,
  `internal/check` invariant code) and generate an equivalent obligation
  instead of parsing source. Literal `Color.Other("Red")` must be a compile
  error; a runtime String needs a guard. If decision 3 is "no", skip it.
- Goldens: decode/encode round trip, `Other("purple")` round trip, known name
  doesn't fall back, legacy object form, object with extra members fails,
  literal known name compile error.

### PR 3: `bork/enum`

- New std package `internal/std/enum` (copy the layout of `internal/std/codec`
  and register it like the others in `internal/std/std.go` / `deps.go`).
- `class Enum[T]` with `values`, `name`, `byName`, `parse`, `index`;
  `type UnknownName`; `metadata enum.Info` (names, docs, has fallback).
- Template rejects with `shape.fail` any target that isn't enum-shaped or has
  private (lower-case) variants. Construct fieldless variants with
  `variant.builder().finish()`; the fallback with `.set(field, name)`.
- `pred known[T: Enum](value: T)`: generic preds with bounds exist
  (`pred same[T: Eq]` in testdata) but verify a user class bound works in a
  `where` alias (`type KnownColor = Color where enum.known`) and in derived
  decoding. If it doesn't, generate a per-type predicate and update the note.
- Docs: new `docs/std/enum.md`, link from `docs/std/README.md`, mention in
  `docs/language/types.md` near sealed types. Add an example under `examples/`.
- Goldens: values order, byName strict with a fallback, parse falls back,
  name of the fallback, index None for the fallback, KnownColor rejects
  "purple" with the expected-names message, shape.fail diagnostics.

### PR 4: schema for CLI

- Add `doc` to `shape.Variant` (`internal/std/shape/shape.bork`) and its
  checker support: `internal/check/derive_metadata_types.go` (property lists
  around lines 150 and 335) and `internal/check/derive_templates.go` (~364).
  Variant docs are the `//` lines before the variant; check the parser keeps them.
- `codec.FieldSchema` gets `variants: List[VariantSchema] = []`; the enum-shaped
  decoder publishes `kind: "string"` and the list (fallback excluded). Update the
  `FieldSchema { ... }` literals in codec.bork and numbers.bork if the default
  isn't enough.
- Tell the cli worker when it lands; they own `internal/std/cli`.

## Gotchas

- Every `.bork` file must be fmt-clean; std sources too.
- Golden updates: `go test ./internal/driver -run <Case> -update` rewrites only
  existing expected files; create new ones first (empty is fine).
- Design notes are excluded from TestDocSnippets, but docs/std and
  docs/language pages are compiled. Keep their examples runnable.
- Don't change mixed sealed types' wire form; one type has one shape.
- Exhaustiveness: the fallback is an ordinary variant; no checker change.
- Private variants: codec keeps encoding them as today; only `enum.Enum`
  rejects such types.
- Testing policy: focused tests plus `golangci-lint run ./...` and `gofmt -l .`
  locally; CI runs the full suite.
