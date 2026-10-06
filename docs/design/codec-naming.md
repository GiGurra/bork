# Codec naming, field overrides and json/v2 learnings (bork-bbtlwz)

Status: proposal for human approval. Nothing here is implemented. The syntax
in the examples is proposed, not valid bork today.

## Summary

- Each type has one **naming policy** (verbatim by default). Fields and
  variants can set a **wire name** and decode **aliases**. JSON, YAML, cli,
  env, CSV and HTTP all follow the same names.
- Syntax: **typed tag groups** (`codec { ... }`), which generalise
  `go { ... }`. No new keywords.
- From json/v2: reject duplicate names and invalid UTF-8 now. Add opt-in
  unknown-member rejection and round-trip-safe `omit`. The rest is in the
  [table](#learnings-from-gos-encodingjsonv2).

## Example

```text
import codec "bork/codec"

type Account = {
  // The JSON and YAML key is "user_name"; the flag is --user-name.
  userName: String
  // Wire name "login"; old documents that say "user" or "uid" still decode.
  loginId: String codec { name: "login", aliases: ["user", "uid"] }
  nickname: Option[String] codec { omit: codec.Omit.None }
  role: Role = Role.Member
} codec { naming: codec.Naming.Snake, unknown: codec.Unknown.Reject }
  derive (codec.Decode, codec.Encode)

type Role = sealed {
  Member
  // Tags are "member" and "site_admin"; "admin" still decodes.
  SiteAdmin codec { aliases: ["admin"] }
} codec { naming: codec.Naming.Snake } derive (codec.Decode, codec.Encode)
```

The encoded value as JSON:
`{"user_name":"ada","login":"a1","role":"member"}`. A payload-less sealed type
encoding as a bare string is pending the `enums` design (#409); today it
would be `{"type":"member"}`.

The same value as YAML:

```yaml
user_name: ada
login: a1
role: member
```

## Today

- `encodeShape`, `DecodeRecord` and `DecodeSealed` in
  `internal/std/codec/codec.bork` use `field.name` and `variant.name` verbatim.
- Field tags (`go { json: "x" }`) are untyped `List[shape.Tag]`. Only GoStruct
  and ForeignRecord read them. Codec ignores them.
- `codec.RecordField.name` has two jobs. It is the bork field name, and it is
  also the object key that cli, env, CSV and HTTP use when they build a
  `codec.Value` to decode. A wire rename would break all four, so the schema
  must separate the two names (see [Schema changes](#schema-changes)).
- cli (`flagName`) and env (`variableName`) each have a copy of the same
  word-splitting rule.
- A named variant payload field called `type` collides with the discriminator
  member today, and nothing reports it. (`values` is used only by positional
  payloads, so a named field called `values` is fine.)

## Naming policy

`codec.Naming = sealed { Verbatim, Camel, Pascal, Snake, Kebab, ScreamingSnake }`.

- **Words.** A name is split into words with today's cli/env rule: a break
  before an upper-case letter that follows a lower-case letter or a digit, or
  that starts a new capitalised word (`httpURLPort` gives http, url, port;
  `ipv4Addr` gives ipv4, addr; flags stay `apiURL` → `--api-url`). `_`, `-`
  and spaces also separate words, so an override such as `name: "user_name"`
  gives the flag `--user-name`. The rule moves to one function,
  `codec.words`, which cli and env then call.
- **Joining.** Each policy joins the words: `httpUrlPort`, `HttpUrlPort`,
  `http_url_port`, `http-url-port`, `HTTP_URL_PORT`. Acronym case is not kept,
  so `userID` under Camel becomes `userId`. Use a `name` override to keep it.
- **Scope.** The policy is set on the type and covers the record's fields. On
  a sealed type it covers variant tags and the fields of named payloads. A
  separate policy for tags (serde's `rename_all` versus `rename_all_fields`)
  can come later if someone needs it.
- **No inheritance.** Field types keep their own policy. A snake-case record
  that holds a `Point` writes `Point` however `Point` declares it. Each type
  owns its wire contract wherever it is used.
- **Protocol members.** The discriminator member `type` and the positional
  payload member `values` are not renamed. Choosing another discriminator
  name comes later.
- **Map keys** are data, so a policy never changes them.

## Field and variant overrides

`codec { name: "...", aliases: [...] }` can go on a field or on a variant.

- **Encode** writes `name`, or the policy's name when there is no override.
  `name` is used exactly as written; the policy does not apply to it.
- **Decode** accepts the wire name or any one alias. If more than one of them
  is present, decoding fails at the second one, for example
  `.uid: same field as .login`.
- **Errors** use the name that the input used, so a path points at text the
  user can find. A missing field is reported under its wire name. Builder and
  fact errors from `pending.finish()` and `RecordField.validate` carry
  bork-name paths today (`FieldPath` in internal/check/derive_builders.go).
  The template replaces their first segment with the wire name from a
  compile-time table. env and CSV trim paths by `wireName`.
- **Compile-time checks.** These are reported at the tag group, or at the
  type if the policy causes the problem:
  - two members get the same wire name after the policy (`userId` and `userID`
    under Snake);
  - an alias equals any wire name or alias in the same type, including its own
    wire name;
  - an empty name;
  - a named payload member whose wire name or alias is `type` (this also fixes
    today's silent collision);
  - the YAML merge key `<<`;
  - `name`, `aliases` or `omit` on a tuple slot or positional payload field,
    because these have no member names (a positional variant can still rename
    its tag);
  - `omit` that a missing field would not round-trip. `Omit.None` needs an
    `Option` field with no default, or a default of `Option.None`. An
    `Option[T] = Option.Some(x)` field would decode an omitted None as
    `Some(x)`. `Omit.Default` needs an eager default and `==` on the field type;
    a lazy default depends on its siblings, so it is rejected.
- **Unknown options and wrong types** are ordinary type errors in the tag
  group's record literal (see [Syntax](#syntax)).
- **Computed fields** are checked by wire name and aliases in the "computed
  field is read-only" check.
- **CSV encode** needs every column, so it ignores `omit` and writes an empty
  cell for None.

## Syntax

Three designs were compared.

| | A: generalised string tags | B: typed annotations | C: typed tag groups (recommended) |
| --- | --- | --- | --- |
| Field | `codec { name: "x", aliases: "a,b" }` | `codec.FieldTags { name: "x", aliases: ["a"] }` | `codec { name: "x", aliases: ["a"] }` |
| Type | `codec { naming: "snake" }` | `codec.TypeTags { naming: ... }` | `codec { naming: codec.Naming.Snake }` |
| Values | Strings only | Any closed value | Any closed value |
| Unknown keys and typos | Only found if a template checks | Type checker | Type checker |
| Editor completion and docs | None | Yes | Yes |
| New grammar | Group name becomes any identifier | A type name plus record literal in field position | Group name becomes any identifier |

**Recommendation: C.** It reads like the `go { ... }` tags people already
know, and it is type-checked like B without a type name on every field.

- **Group name.** The name before `{` must be an imported package's
  qualifier, or the built-in `go`. Inside the package that declares the tag
  types, the group uses that package's own name. A misspelt group is an
  unknown-name error. The same group twice in one place is an error.
- **Body.** The body is checked as a record literal of the package's exported
  `FieldTags`, `VariantTags` or `TypeTags`, depending on where the group is
  written. A package with no such type for that place cannot be used there.
  Fields with defaults can be left out. Values must be closed, with the same
  rules as field defaults, so the derive expansion can evaluate them.
- **`go { ... }`** is unchanged: String values and ordered `List[Tag]`. It
  stays the only group that GoStruct and ForeignRecord read.
- **Placement.** Groups go after the field type and default, after a
  variant's payload (before its `where`), and after the body of a record or
  sealed type (before `where` and `derive`). They cannot go on resource,
  alias or bare Go-name types. Several groups can be written in sequence, e.g.
  `port: Int = 8080 codec { name: "p" } cli { ... }`. A default expression
  ends before an identifier, as it already ends before `go`.
- **Same line.** A group starts on the same line as what it tags. Otherwise
  `codec { ... }` on its own line inside a sealed body would parse as a variant
  named `codec`.
- **Grammar sketch.**
  `TagGroup = ( "go" | Ident ) "{" [ TagEntry { Sep TagEntry } [ Sep ] ] "}"`,
  `TagEntry = Ident ":" Expr`. `Field`, `Variant` and `TypeDecl` each take
  `{ TagGroup }`.
- **Shape API.** `field.tagged[M]()`, `variant.tagged[M]()` and
  `shape.tagged[T, M]()` return `Option[M]` during expansion. The codec
  templates read `codec.FieldTags` and the other two through these.
- **Where the type-level option lives: on the type, not on the derive
  request.** Encode, Decode, Schema, cli, env and CSV must all agree on names.
  Two separate requests (`derive codec.Decode for T` and
  `derive codec.Encode for T`) could otherwise disagree and break round trips.
  A type that needs two different wire forms needs two types, or a
  hand-written instance.

Codec's tag types:

```text
type TypeTags = { naming: Naming = Naming.Verbatim, unknown: Unknown = Unknown.Ignore }
type FieldTags = { name: Option[String] = Option.None, aliases: List[String] = [], omit: Omit = Omit.Never }
type VariantTags = { name: Option[String] = Option.None, aliases: List[String] = [], fallback: Bool = false }
type Unknown = sealed { Ignore, Reject }
type Omit = sealed { Never, None, Default, NoneOrDefault }
```

Following docs/syntax-changes.md, the grammar change also reaches the
tree-sitter grammar, editor packages, fmt and the LSP (completion inside tag
groups comes from the tag type's fields).

## JSON and YAML

Both formats parse to `codec.Value` and render from it, so this design needs
nothing format-specific in codec. Points for YAML:

- **Key style.** Common YAML conventions differ: Kubernetes uses camelCase,
  Compose and Ansible use snake_case, and some tools use kebab-case. A
  per-type policy covers all of them. There is no per-format policy, so a
  value written as JSON and read back as YAML still decodes.
- **Duplicate keys.** `bork/yaml` already rejects them. The JSON change below
  makes both formats behave the same, which aliases depend on.
- **Keys that need quotes.** Names such as `on`, `yes`, `null` or `1` are
  allowed as wire names. `bork/yaml` reads keys by their text, so decoding
  works. The renderer must quote them; round-trip tests will check that.
- **`<<`** is rejected as a wire name (see the checks above).
- **Variant tags that YAML reads as other scalars.** A variant wire name or
  alias such as `true`, `false`, `null`, `~`, `on` or `off` is a compile
  error. As a bare-string value, hand-written YAML `role: null` would read as
  Null, not as the tag. Object keys don't have this problem, because keys are
  read by their text.
- **Merge keys and aliases.** In YAML a mapping's own key overrides the same
  key from a merged anchor. Codec only sees the merged `codec.Value`, so if
  an anchor supplies the alias `user` and the mapping writes `login`, decoding
  fails with "same field". The message says to use the same spelling as the
  anchor. Teaching the loader about aliases would tie YAML parsing to one
  type, so this design doesn't.
- **Config files.** CLI config files (JSON today, YAML planned) use the wire
  name and aliases as keys, the same in both formats.

## Sharing with bork/cli and bork/env

Agreed with worker boacli, who owns the CLI changes.

- **Words.** Without an override, automatic flag and env names use the words
  of the field name, as they do today. With a `name` override, they use the
  words of that name: `loginId codec { name: "login" }` gives `--login` and
  `LOGIN`. The policy changes the wire spelling only. Flags stay kebab-case
  and env names stay UPPER_SNAKE. This is a deliberate reading of "from the
  same policy": the words and overrides are shared, but each source keeps its
  own conventional spelling, since `--http_port` or a camelCase env name
  would surprise users.
- **Precedence**, from strongest to weakest: an enricher rename, then
  `cli.Mapping.Named` (exact, no prefixes), then the codec override, then the
  field name.
- **Aliases** become hidden long flags and env names. They get no automatic
  shorts and no deprecation warning, and they join the existing duplicate
  name check before any env or config access. How they combine with the
  canonical name's mapping:
  - **Auto:** alias names are derived like Auto names, with `flagPrefix` and
    `envPrefix`.
  - **Named:** the canonical name is exact, and the aliases are still derived
    with prefixes.
  - **Disabled:** that source's aliases are disabled too.
- **Alias env names need a prefix.** Aliases such as `user` or `uid` would
  give `USER` and `UID`, which shells set. So alias env names are derived only
  when `envPrefix` (or env.Load's prefix) is non-empty.
- **Enrichers** see aliases as `FieldSpec.aliases` (long and env), so a
  rename can also change or drop them.
- **Conflicts.** Giving the canonical flag and an alias flag (or setting both
  env names) is an error, as in Decode. An empty env value still counts as
  absent.
- **Config keys** are the wire name and aliases. So a snake-case policy
  changes the accepted keys, from `httpPort` to `http_port`. This is opt-in,
  and docs/std/cli.md will say so when it is implemented. If a config file has both the
  wire name and an alias for the same field, that is an error, as in Decode.
- **Positional fields.** An override only changes the name shown in help.
- **Nested flattening** (planned in cli): an override renames that field's own
  segment of words, not the whole path. Prefixes join the parents' words.
- **`bork/env`** (`env.Load`) follows the same words and alias rules. cli
  `autoEnv` builds env names from the long flag, including `flagPrefix`, so
  the two agree when there is no `flagPrefix`. That is today's behaviour and
  this design keeps it.

## Sealed enums (worker `enums`, bork-rz5wv6)

`enums` is designing enum values, lookup by name, and a fallback variant for
forward compatibility. To keep the two designs consistent:

- Lookup by name for wire purposes uses the variant's wire name and aliases.
  Bork source names stay available for code.
- Each variant has one external name, its wire name. Codec, CLI and enum
  tooling (`enum.name`, `byName`) all use it.
- The fallback variant is marked with `Unknown(String) codec { fallback: true }`.
  It receives any tag that matches no wire name or alias, and re-encodes that
  tag unchanged. These are compile-time errors: more than one fallback, a
  name or aliases on the fallback, and a fallback whose payload is not a
  single String. A `codec.Value` payload for payload-carrying types is later;
  see [enums.md](https://github.com/GiGurra/bork/pull/409). The type's invariant
  says that a fallback's String is never a wire name or alias.
- A sealed type whose variants all have no payload encodes as a bare string
  (`"red"`), not `{"type":"red"}`. That is the `enums` design. Decode already
  accepts a bare string tag, and the naming policy applies to the string.
- CLI choices for an enum field show the wire names.

## Schema changes

`codec.RecordField` gains `wireName: String`, `aliases: List[String]` and
`words: List[String]`. `name` stays the bork field name. cli, env, CSV and
HTTP build `codec.Value` objects keyed by `wireName`. CSV headers and HTTP
form keys use the wire name and accept aliases. `codec.words` and the joiners
are public, so other libraries can follow the same rules.

## Learnings from Go's encoding/json/v2

Read from `go doc encoding/json/v2` and `encoding/json/jsontext` (Go 1.27).

| Feature | json/v2 behaviour | bork today | Recommendation |
| --- | --- | --- | --- |
| Name matching | Case-sensitive by default. `case:ignore` and `MatchCaseInsensitiveNames` also ignore `-` and `_`. | Exact match | **Never** case-insensitive matching. v2 itself calls it a source of duplicates. Aliases cover renames. |
| Duplicate names | Rejected by default (`AllowDuplicateNames` to allow) | YAML rejects. JSON accepts; a record decode keeps the first, a Map decode keeps the last. | **Now:** `json.Parse` rejects duplicates with line and column, as YAML does. |
| Invalid UTF-8 | Rejected (v1 replaced it with U+FFFD) | `json.Parse` replaces it through v1 `Unmarshal` | **Now:** reject when parsing. **Later:** Render is infallible today, so it needs its own decision. |
| Unknown members | Ignored by default; `RejectUnknownMembers` rejects them; an inline fallback map can capture them | Ignored | **Now:** `codec { unknown: codec.Unknown.Reject }` on the type, which rejects at the member's path. Ignore stays the default because APIs need to evolve. **Later:** capture into a `Map[String, codec.Value]` field. |
| omitzero / omitempty | omitzero: Go zero or `IsZero()`. omitempty: values that encode as null, `""`, `{}` or `[]`. | Every field is written; None is written as null | **Now:** `omit: None / Default / NoneOrDefault` on a field. Only values that Decode rebuilds when they are missing can be omitted, and the compile-time checks keep round trips safe. **Later:** a type-level default. **Never** omitempty: an omitted `[]` with no default would fail to decode. |
| Inline / embed | `embed` brings a struct's members up into the parent; fallback maps | No embedding | **Later:** `codec { inline: true }` on a record-typed field, with the same compile-time collision checks. Shares the flattening work with cli. |
| `string` option | Quote numbers inside JSON strings, only at the field's top level | Numbers keep exact text, but JavaScript readers lose precision | **Later:** `codec { quoted: true }`. The template turns Number into String on encode and back on decode. A compile-time error if the field is not a number. |
| Format options | The released v2 has no `format:` tag. `time.Time` uses RFC 3339 and Duration has no default form. | No codecs for `time.Instant`, `Duration` or `Bytes` | **Later** (separate ticket): the package that owns each type gives it a default instance (RFC 3339, Go duration text, base64). Other forms are separate instance sets chosen with `use`, not format strings in tags. |
| nil vs empty | nil slices and maps encode as `[]` and `{}` (`FormatNil*AsNull` to change) | No nil. None is null and empty lists are `[]`. | **Never** add nil formatting options. Bork already behaves like v2's default. |
| Map ordering | Random order; `Deterministic` sorts | Ordered maps keep insertion order. `encodeMap` walks unordered maps in random order. | **Now:** encode unordered maps with sorted keys, the order printing already uses. |
| Per-call options | `Options` passed to each Marshal/Unmarshal; tags override them | None. Instances are chosen statically. | **Never** for anything that changes the representation: the wire contract is the type plus its selected instance. **Later:** decode limits such as depth and size as explicit parameters. |
| Streaming | `jsontext` Encoder/Decoder, `MarshalerTo` / `UnmarshalerFrom` | Every value goes through the `codec.Value` tree | **Later**, when the parked performance work resumes: add `encodeTo` / `decodeFrom` class methods whose default goes through the tree. Derived instances can write tokens directly. This design keeps that easy: names are compile-time constants, and aliases become a fixed match. |
| Error detail | `SemanticError` with a JSON Pointer, kind and Go type | `DecodeError { path, message }` | **Now:** keep paths in wire names (see overrides). **Later:** add a source line and column to DecodeError when parsing and decoding are combined. |

## Delivery plan (after approval)

1. **JSON safety:** reject duplicate names and invalid UTF-8 in `json.Parse`,
   sort unordered map keys on encode, and report the `type` payload
   collision.
2. **Tag groups:** grammar, parser, fmt, checker typing, `shape.tagged`, and
   the editor grammars (docs/syntax-changes.md).
3. **Naming and overrides** in the codec templates: `codec.words`, the
   policies, wire names, aliases, checks, `omit` and `unknown`. Golden and
   round-trip tests in JSON and YAML.
4. **Schema users:** `RecordField.wireName`, `aliases` and `words` in cli, env,
   CSV and HTTP, coordinated with boacli's CLI changes.
5. **Later tickets:** inline and capture, `quoted`, time/bytes codecs, decode
   limits, streaming.

## Decisions to review

- Typed tag groups (C) instead of string tags or full annotations.
- Wire options on the type, not on the derive request. One wire contract per
  type, shared by JSON and YAML.
- Policies don't pass down to field types.
- Flags and env names keep their own spelling. Only the words and overrides
  are shared with the wire policy.
- Alias env names exist only with a non-empty env prefix.
- A YAML merge that mixes an alias and the wire name is an error.
- `Camel` loses acronym case (`userID` becomes `userId`).
- Unknown members are still ignored by default.
- Duplicate JSON names become an error. This breaks any caller that relies on
  today's first-wins behaviour. The implementation PR will search the repo and
  examples for such callers.
