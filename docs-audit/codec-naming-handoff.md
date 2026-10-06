# Handoff: codec naming implementation (bork-bbtlwz)

For the implementer of [docs/design/codec-naming.md](../docs/design/codec-naming.md)
(PR #408). Read that note first. This file covers the decisions, how to split
the work into PRs, where the code is, and the traps.

Status: the design is waiting for the human to approve the syntax. **Don't
start PR 2 or later until the lead confirms the approval.** PR 1 (JSON safety)
doesn't depend on the syntax. Ask the lead before you start it.

## Decisions already made

- **Syntax:** typed tag groups, `pkg { key: value }`, written on the same line
  after a field (type/default), after a variant payload, or after a record or
  sealed body. The body is a record literal of `pkg.FieldTags`,
  `pkg.VariantTags` or `pkg.TypeTags`, whichever fits the place. `go { ... }`
  is unchanged: String values, `List[shape.Tag]`, read only by GoStruct and
  ForeignRecord.
- **Type-level options live on the type**, not on the derive request.
- **Naming:** `TypeTags.naming: Option[Naming] = Option.None`. None means the
  default for the shape: `Verbatim` for records and sealed types with
  payloads, `ScreamingSnake` for enum-shaped sealed types (human decision; a
  wire break). Policies don't pass down to field types. `type` and `values`
  are never renamed. Map keys are never renamed.
- **Overrides:** `name` is used exactly as written. Decode accepts the wire
  name or one alias; two or more present is an error. Decode never accepts the
  bork source name unless it is listed as an alias (needed for enums'
  fallback).
- **Errors** use input (wire) names in their paths.
- **cli/env:** share `codec.words` and the overrides. Flags stay kebab-case and
  env names stay UPPER_SNAKE. Alias env names are derived only with a non-empty
  prefix. Precedence is enricher, then `Mapping.Named`, then the codec
  override, then the field name. Full rules are in the note's cli section,
  agreed with boacli and the cli successor worker.
- **Enums (#409, worker `enums`):** `VariantTags.fallback: Bool`. The
  fallback's payload is a single String (a `codec.Value` payload is later).
  Enum-shaped types encode as a bare string. Coordinate any change in
  `DecodeSealed` and `encodeShape` with them, because both of you touch it.

## PR breakdown (each under ~1,500 changed lines)

1. **JSON safety** (no syntax).
   - `json.Parse` rejects duplicate object names (with line and column, like
     YAML's message) and invalid UTF-8.
   - `encodeMap` writes unordered maps sorted by key.
   - A named payload field called `type` in a sealed type becomes a compile
     error, via `shape.fail` in `encodeShape` and `DecodeSealed`.
   - Search goldens and examples for duplicate-key inputs first.
2. **Tag groups: syntax.** AST, parser, formatter, describe, tree-sitter,
   TextMate and editor queries, and docs/grammar.md. Generalise `GoTags` into
   a list of groups. Keep `go` groups exactly as they are. Follow
   docs/syntax-changes.md item by item.
3. **Tag groups: checking and shape.**
   - Resolve the group name to an imported package, or the package itself.
   - Type the body against `FieldTags`, `VariantTags` or `TypeTags`.
   - Values must be closed, evaluated with the same evaluator as defaults and
     ForeignRecord.
   - Expose `field.tagged[M]()`, `variant.tagged[M]()` and
     `shape.tagged[T, M]()`.
   - Errors: a duplicate group in one place, a group on a resource or alias
     type, and a package without the matching tags type.
4. **Codec naming and wire names.**
   - `codec.Naming`, `codec.words` and the joiners.
   - `TypeTags`, `FieldTags` and `VariantTags` in internal/std/codec.
   - Wire names in `encodeShape`, `DecodeRecord`, `DecodeSealed` and
     `RecordInfo`.
   - Compile-time collision checks and the YAML-unsafe tag check.
   - Remap error paths.
   - The enum UPPER_SNAKE default (update goldens and say in the PR that it is
     a wire break).
5. **Aliases, `omit`, `unknown`.** Decode alias matching and the "same field"
   error. `omit` with its round-trip checks. `unknown: Reject`. JSON and YAML
   round-trip tests, including YAML merge keys.
6. **Schema users.**
   - `RecordField.wireName`, `aliases` and `words`.
   - env, CSV and HTTP key objects by `wireName`. CSV encode ignores `omit`.
   - Replace `cli.flagName` and `env.variableName` with `codec.words`.
   - cli aliases, `FieldSpec.aliases`, conflict errors and config keys.
   - Message the `cli` worker before starting; their PRs touch the same files.
7. **Docs:** docs/std/json.md, encoding.md and cli.md (config keys follow the
   policy), env docs, and the language and tour pages for the tag-group
   syntax. TestDocSnippets compiles every `bork` block.

## File pointers

| What | Where |
| --- | --- |
| Codec templates and schema | `internal/std/codec/codec.bork` (`encodeShape`, `RecordInfo`, `DecodeRecord`, `DecodeSealed`, `inputField`) |
| JSON text | `internal/std/json/text.bork` (`Parse`, `Render`) |
| YAML loader and renderer | `internal/std/yaml/yaml.bork` (`load`, the `fields` merge logic, `dump`) |
| Shape API | `internal/std/shape/*.bork` (`Field`, `Variant`, `Tag`, `ForeignRecord`) |
| Field tags today | `internal/syntax/ast.go` (`GoTags`, `GoTag`), `internal/syntax/parser.go` (~line 509), `internal/check/decls.go:215`, `internal/check/types.go:186` |
| Shape descriptors and properties | `internal/check/derive_templates.go`, `internal/check/derive_metadata_types.go` (the property switch: `"tags"`, `"hasDefault"`, ...) |
| Builder error paths | `internal/check/derive_builders.go` (`FieldPath` returns `"." + field.Name`) |
| Compile-time evaluator | `internal/check/derive_defaults.go`, `internal/check/foreign_record.go` |
| cli naming | `internal/std/cli/cli.bork` (`flagName`), `internal/std/cli/mapping.bork` (resolve, duplicate checks, autoEnv) |
| env naming | `internal/std/env/env.bork` (`variableName`, `prepareWithSchema`) |
| CSV and HTTP schema users | `internal/std/encoding/csv.bork`, `internal/std/http/decode.bork` |
| Syntax change checklist | `docs/syntax-changes.md`; editors in `editors/` (tree-sitter-bork, vscode, nvim, helix, zed, emacs, vim) |
| Derive template tests | `internal/driver/derive_template_test.go`; goldens in `testdata/cases/<name>/` |

## Gotchas

- **Two jobs for `RecordField.name`.** env, CSV, HTTP and cli use it both as
  the bork field name and as the `codec.Value` key. Separate the two (PR 6)
  before any wire rename reaches them, or keep them working on Verbatim
  types until then.
- **Error paths.** `pending.finish()`, `field.check` and
  `RecordField.validate` produce bork-name paths. Remap the first segment with
  a compile-time table. Nested types already report their own wire names.
- **Round trips with `omit`.** Missing fields take their default first
  (`DecodeRecord`). Reject `Omit.None` on `Option[T] = Option.Some(x)`.
  `Omit.Default` needs an eager default and `==`.
- **YAML merge.** Codec only sees the merged value. An anchor's alias plus a
  mapping's wire name gives "same field". This is by design; make the message
  say to spell it the same way as the anchor.
- **YAML scalars.** bork/yaml uses the YAML 1.2 core schema (I checked
  go.yaml.in/yaml/v4: `ON`, `on`, `yes` and `Off` are `!!str`; `TRUE`, `NULL`
  and `~` are not). Reject only 1.2 non-string variant names. Check that the
  renderer quotes the 1.1 forms.
- **Default expressions** must end before a group's identifier. Groups must
  start on the same line, or `codec { ... }` in a sealed body parses as a
  variant.
- **Bare-string tags.** `DecodeSealed` already accepts a bare string tag.
  Encoding as a bare string is enums' change; don't duplicate it.
- **Std and prelude edits:** run `go generate ./internal/stdvalidators` if
  `TestGeneratedArtifactCurrent` reports stale sources. Every `.bork` file must
  be fmt-clean.
- **Goldens:** `go test ./internal/driver -update` only rewrites expected files
  that already exist. Create new ones (empty is fine) first, and check every
  diff by hand.
- **Tests:** run focused tests locally (`./internal/std/...`,
  `./internal/syntax`, `./internal/check`, the matching driver tests), plus
  `golangci-lint run ./...` and `gofmt -l .`. Leave the full suite to CI.
