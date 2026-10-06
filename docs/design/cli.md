# CLI usability and boa parity

> **Status:** Implemented: settings, mapping, commands and dynamic completion. Current docs: [CLI](../std/cli.md) and [CLI cookbook](../std/cli-cookbook.md). The comparisons and delivery sequence below record the original design.

Design rationale for awb bork-5xggub. The implemented API is documented in
the [CLI reference](../std/cli.md); proposal signatures below preserve the
original design and may omit later fields or checks.

## Evidence and comparison

The investigation used GiGurra/boa's local checkout at v1.0.31 and the cached
v1.0.28 source. Bork main already pins v1.0.31 in both its root module and
`internal/std/cli/go-deps.mod`; no dependency upgrade is necessary for this work.
The relevant boa sources are `pkg/boa/api_base.go` (Cmd and enrichers),
`api_typed_param.go` (typed metadata views), `internal.go` (binding, completion,
source precedence), `raw_params_test.go` (partial completion regressions), and
`docs/enrichers.md`, `docs/struct-tags.md`, `docs/validation.md` and the README.
Both versions synchronize parsed raw fields before dynamic flag and positional
completion callbacks. Newer boa also supports persistent flags and richer config
handling; use the pinned version rather than copying its internals.

| Feature | Boa | Bork today | Proposed outcome |
| --- | --- | --- | --- |
| Flag names | Kebab case, explicit `name`/`long`, configurable enricher | Kebab case only | Explicit override; global derivation switch and prefix |
| Short flags | Automatic first letter, collision skipping, explicit override | Explicit only | Opt-in automatic shorts, deterministic collision handling, explicit disable |
| Environment | Opt-in automatic UPPER_SNAKE_CASE, prefix, `noenv` | Explicit `env` only | Global auto-env/prefix plus per-field override/disable |
| Enrichers | Ordered functions over current parameter and previously processed parameters | None | Pure functions over immutable field specs; ordered composition |
| Field help | `descr` or programmatic description | Leading field comments from codec.Decode schema | Official leading-comment docs; explicit description override |
| Required and optional | Tags, pointers, global optional default, conditional hooks | Required unless default or Option; decoder remains authority | Preserve bork rules; use Option/defaults and typed validation |
| Defaults | Tags/programmatic defaults, bool false enricher, help display | Record defaults and help display | Preserve record defaults; no implicit bool false |
| Validation | Alternatives, min/max/pattern/custom validators | Derived codec.Decode and field facts, collected errors | Keep codec.Decode; add explicit static choices policy for CLI values |
| Bash/zsh/fish/PowerShell | Cobra completion command and generators | Leaf inherits some Cobra behavior; dispatcher disables command and rejects hidden endpoints | Uniform scripts and completion endpoints for leaves and trees |
| Static value completion | Alternatives on flags/positionals, command names | Not exposed | Choices with descriptions and file-completion controls |
| Dynamic value completion | Callback sees synchronized partial raw params | Not exposed | Partial input view with typed decoding; callbacks have declared effects |
| Subcommands | Arbitrary Cobra trees, aliases, persistent params | One-level typed dispatch via flag-free routing commands | Real Cobra leaf commands in nested groups, aliases and command docs |
| Config files | JSON/custom formats, explicit source controls, nested files | JSON files, config-file selector, flags > env > files > defaults | Preserve precedence; per-field source disabling, documented JSON behavior |
| Positionals | Ordered fields, final collection, per-field completion | At most one field, possibly a List | Ordered positional metadata; final List collects remaining inputs |
| Hidden/deprecated | Cobra interop; `noflag` is source disabling, not hidden-but-accepted | Not exposed | Hidden accepted flags and deprecation messages; separate source disabling |
| Collections/nested records | Native Go handlers and nested struct flattening | Repeated List flags; other compound values use JSON | Keep existing bork encoding; positional Lists covered explicitly |
| Hooks/live reload/Cobra access | Extensive Go lifecycle API | Typed handlers in Scope | Outside this epic; retain typed bork boundaries |

Boa's enrichers derive metadata, but its global optional policy and bool-default
enricher also change value semantics. Bork already has a language-wide decoding
contract: a plain Bool without a default is required. CLI enrichment must not
silently replace that contract. Likewise, flattening nested records is not
necessary for this epic: compound fields continue accepting JSON. JSON remains
the supported config format; YAML/TOML, live reload, conditional visibility,
persistent parent options and Go lifecycle-hook exposure are follow-up work.
The command-tree API should leave room for persistent options without inventing
unmerged language features now.

## Compatibility and API shape

Keep Parse, Run, Subcommand, Dispatch and RunCommands source-compatible. New
metadata fields have defaults. Add a final `settings: Settings = .{}` argument
to these APIs for mapping and static completion. Introduce ParseWith, RunWith
and SubcommandWith for dynamic callbacks; these carry the existing closed
`io + net + clock + random + state` callback bound used by Command. Ordinary
Parse and Run retain their current io bound. A pure or narrower callback can
satisfy the larger bound; callers of With APIs must declare that bound.
Effect-polymorphic narrowing can follow when the language supports it.

The following signatures are sketches, not snippets of implemented bork:

```text
Mapping = Auto | Disabled | Named { name: String }

Flag gains:
  long: Mapping = Auto
  shortName: Mapping = Auto
  envName: Mapping = Auto
  description: Option[String] = None
  config: Bool = true
  hidden: Bool = false
  deprecated: String = ""
  choices: List[Choice] = []
  strictChoices: Bool = false
  position: Option[Int] = None

Settings = {
  autoLong: Bool = true
  autoShort: Bool = false
  autoEnv: Bool = false
  flagPrefix: String = ""
  envPrefix: String = ""
  completion: Bool = true
  enrichers: List[Enricher] = []
}

Choice = { value: String, description: String = "" }
FieldSpec = { field: String, long: Mapping, short: Mapping, env: Mapping,
              description: String, positional: Bool, config: Bool, hidden: Bool,
              deprecated: String, choices: List[Choice], strictChoices: Bool }
Enricher = (List[FieldSpec], FieldSpec) uses nothing => FieldSpec

CompletionRequest = { field: String, prefix: String,
                      arguments: List[String], partial: Partial }
Suggestions = { choices: List[Choice], files: Bool = false,
                directories: Bool = false, keepOrder: Bool = false }
Completer = (CompletionRequest, Scope)
            uses io + net + clock + random + state => Suggestions | Error
Completion = { field: String, suggest: Completer }

ParseWith[T: codec.Decode](..., completions: List[Completion], settings: Settings)
  uses io + net + clock + random + state: T | Error | Help
RunWith[T: codec.Decode](..., completions: List[Completion], settings: Settings)
  uses io + net + clock + random + state: Ok | Error
SubcommandWith[T: codec.Decode](..., completions: List[Completion], settings: Settings)
  : Command

Partial.Get[U: codec.Decode](field: String): U | Missing | codec.DecodeError
Group(name: String, description: String, children: List[Command]): Command
```

The existing `short` and `env` string fields remain compatibility aliases.
Nonempty aliases mean Named; specifying both an alias and a non-Auto mapping
for the same source is a metadata error. This deliberately distinguishes
inherit/derive, disable, and explicitly named without assigning several meanings
to an empty string. `long: Disabled` means config/env-only; disabling short
alone keeps the long flag. An env-disabled field still accepts flags/config.
Automatic shorts are suppressed when canonical long registration is disabled,
including autoLong: false and positional fields. Explicit Named shorts on such
fields are metadata errors.

`config: false` disables file input for that field. Reject a config file key for
a disabled field, including a null value, with a source/field-path diagnostic;
do not silently ignore misspelled or forbidden configuration. Required fields
still need another enabled source; defaults still apply normally. A config-file
selector obtains its path from CLI/env/default, as today, without recursively
selecting files from config values.

Disabling all flags globally requires `autoLong: false` and `autoShort: false`;
explicit Named mappings still opt fields in. `autoLong: false` disables derived
long registrations; an explicit short needs a Named long because Cobra flags
have a canonical long name. Reject that inconsistent combination.

Existing `positional: true` retains the single-positional shorthand. `position`
provides zero-based ordering for multiple positional fields; mixing the shorthand
and explicit positions is rejected. Positions must be unique and contiguous,
with at most one List, last. Optional/default scalar positionals cannot precede
required scalars because omission would be ambiguous. Positional fields do not
also register flags; reject long/short overrides that imply flag registration.
Repeated positional List values use the same literal/JSON element conversion as
repeated List flags.

Command metadata gains aliases, long description, examples, hidden and deprecated
with defaults. Groups provide nested routing and help. A group has no typed
options or handler initially. Duplicate command names/aliases and reserved
`help`, `completion`, `__complete`, `__completeNoDesc` are validated recursively.
When completion is disabled, its visible command and hidden endpoints are absent;
reserved names stay reserved so toggling settings cannot reinterpret a user command.

## Mapping and documentation rules

Resolve all metadata before reading env or config or invoking callbacks:

1. Read declaration-order codec.Decode fields; take name and doc from the existing
   schema. Build a spec per field and validate references in Flag/Completion.
2. Normalize compatibility aliases and explicit per-field mappings. Reserve all
   explicit short names across the command before assigning automatic shorts.
3. Apply built-in mapping according to Settings. Auto long names are kebab-case;
   auto env names derive from the resolved long name in UPPER_SNAKE_CASE, or the
   field's kebab name when long registration is disabled. Auto shorts use the
   first ASCII letter of the resolved canonical long name, excluding h and
   occupied letters. No search through later letters. Non-ASCII initial letters
   get no automatic shorthand. Auto env skips positionals, as boa does;
   explicit env for a positional remains supported where boa permits it.
4. Apply user enrichers in list order to each spec, passing completed prior specs
   in declaration order. Enrichers may deliberately override derived or explicit
   mappings. Disabled survives built-in derivation; a user enricher must return
   a Named/Auto mapping deliberately to enable a source again. Resolve remaining
   Auto according to settings after the chain, without calling enrichers twice.
5. Validate the complete resulting set: unchanged field identities, known names,
   legal/nonempty canonical names, reserved names, duplicate long/short/env,
   positional shape, deprecation controls and completion references.

Prefixes apply only to auto-derived mappings, once. `envPrefix: "MY_APP"` gives
MY_APP_SERVER_HOST; `flagPrefix: "service"` gives service-server-host. Explicit
Named strings are exact and unprefixed. Reject whitespace/separators in invalid
names with field-path errors rather than relying on Cobra panics. Automatic
short collisions skip assignment; explicit or custom-enricher collisions error.
Existing default settings keep explicit-only env/short behavior. Settings are
per-command values: applications reuse one value for global policy; there is no
mutable process-global registry. Groups do not implicitly override leaf settings.

Leading `//` comments immediately above a record field are the official default
documentation source. They already travel parser -> checker -> derived codec.Decode
schema -> boa `descr`; document that path in the reader page without introducing
a second parser or annotation. `description: Some("")` deliberately suppresses
the comment; None inherits it. Required/default markers and mapped environment
names are appended separately, so overrides cannot suppress value semantics.
Include useful constraint text from the schema in help where readable. Hidden
flags remain accepted from all enabled sources and validated, but disappear from
help and suggestions. Deprecated flags remain accepted, print a replacement
message through the existing captured output path, and are omitted from value
suggestions. Run APIs print successful warnings; add ParseDetailed[T] returning Parsed[T] | Error | Help, where Parsed[T]
contains `options: T` and `warnings: List[String]`. Existing Parse explicitly
discards successful warnings to preserve its result union; callers needing them
choose ParseDetailed. Run uses the detailed internal path and prints warnings
before its handler. Provide the same detailed variant for ParseWith. Errors
include relevant warnings in their diagnostic list; Help never reports warnings
for flags that were not used. Command dispatch prints warnings on successful
execution, matching Run; detailed dispatch can be added separately if needed.

Static choices are suggestions by default. `strictChoices: true` is an additional
CLI acceptance policy, reported as collected codec.DecodeError values and applied to
supplied CLI/env/config values consistently; record facts remain the authority
for type guarantees. Use string fields or List[String] initially and reject
other strict-choice shapes, instead of making JSON stringification a hidden
semantic contract. Record defaults must also satisfy strict choices. Dynamic
suggestions never become validation: querying a service twice could yield
inconsistent options, and a completion callback must not become a hidden effect
in normal Parse.

## Dynamic completion and partial input

A callback cannot receive T: required fields may be missing, supplied numeric
values may be malformed, and record facts may fail. A compiler-generated
Partial[T] record would add a new derivation and risk duplicating decoder rules.
Use an opaque Partial that stores the already collected field inputs and their
proven schema decoders. Its generic Get method runs the original field decoder
and then the requested U decoder. It returns a U proven by codec.Decode, Missing for
an omitted input, or codec.DecodeError for unknown fields, invalid input, or a U mismatch.
The field-name relationship is checked at runtime, as it already is for Flag;
no unchecked casts or zero-valued stand-ins escape into bork. The schema field
decoder checks independent field constraints only; sibling-dependent constraints
remain the final record decoder's responsibility. Get never claims the partial
record satisfies those relations. U may be a user
record or constrained type with a codec.Decode instance; a different U does not inherit
facts from the declared field. Preserve paths in errors.

Partial contains explicitly supplied CLI, env and config values under the normal
precedence, excluding the word currently being completed. Missing remains Missing
for omitted fields even if the record declares a default: completion code chooses
its fallback or captures typed default/config values. This avoids an Encode bound
or a new compiler helper solely to serialize arbitrary proven defaults. Document
this distinction prominently and show a callback matching Missing explicitly.
A configFile field's existing default can still select the file, as in normal
parsing. An explicit Option null is a supplied value, not an omitted input.
Partial exposes input errors per field, so an invalid unrelated port need not
prevent completing a namespace. Syntax errors that prevent Cobra from resolving
flags produce empty native Cobra completion output and no handler. Native
routing/flag syntax errors retain Cobra v1.10.2's `:0` directive and direct
process-stderr debug messages (which bypass SetErr); the lead accepted this
upstream limit to avoid a parser copy or dependency fork. Our source/callback
errors return `:1` and captured diagnostics.

Each invocation creates fresh partial state. Snapshot env/config once per query;
JSON files use bork/json and the same overlay helper used by normal Parse. Missing
or malformed config returns a completion error directive and diagnostics on
stderr; it must not leak error text into candidate stdout. Never run the command
handler, final record decoder, normal validation hooks, or finalizers registered
by a handler. Dynamic callbacks run inside their own Scope, permitting cancellation
and cleanup; users opt into network/filesystem work by choosing a With API. Help
and script generation invoke no completers and read no config files.

Callbacks return candidate values and optional descriptions; reject embedded
newlines/tabs that would corrupt Cobra's line protocol. The library handles
prefix filtering, deduplication and directives. Dynamic results replace that
field's static choices; duplicate callback specs are rejected. Cobra handles
quoting and shell-specific insertion. Completion callbacks should return values
without shell escaping. Test flags with `--field=value`, shorthand, trailing empty
words, `--`, repeated Lists, and positional prefixes.

## Implementation boundary

Refactor the existing adapter into command construction, input collection,
field conversion, and final decoding. Keep boa/Cobra inside the stdlib unsafe-go
bridge. Reuse `codec.Schema`, independent field validation and the final T decoder.
Do not implement facts, field documentation parsing, option inference, kebab naming
or shell protocols a second time in the compiler.

Replace dispatch's DisableFlagParsing routing children with actual boa-backed
leaf Cobra commands. Build the whole tree before Execute and script generation;
each leaf captures its own schema, settings, shadow state and typed handler.
Heterogeneous option types remain erased only behind Command's private bridge.
Group traversals and script generation inspect actual registered leaf flags,
allowing Cobra's completion/help routing to work uniformly. Keep public command
constructors typed and avoid exposing reflect values or Go command pointers.
Help/script/completion protocol output uses an extended Help carrier:
`Help { text: String, diagnostics: String = "" }`. Capture Cobra stdout and
stderr separately. Parse/Dispatch return protocol stdout in text and stderr
diagnostics in diagnostics, including explicit-argument queries; Run APIs print
text to stdout and diagnostics to stderr without calling the handler. Completion
failures return Help with Cobra's error directive in text and the diagnostic in
diagnostics, so Run returns Ok after emitting the protocol and application error
rendering cannot accidentally add candidates to stdout. Ordinary parse failures
remain Error values. Add tests for malformed config and callback failure through
both explicit-argument and process-argument APIs. Normal command
execution creates a fresh Scope after successful decoding. Existing externally
constructible Command.execute remains supported through a fallback routing leaf,
with documented absence of schema/value completion on such custom commands.

## PR sequence and verification

1. **Design (this PR):** comparison, API rules and planned delivery. Lead approves
   before implementation.
2. **Metadata and enrichers:** mapping variants, Settings, docs override, pure
   enrichers, source disabling, hidden/deprecated flags and warning transport.
   Add focused mapping/docs/environment/custom-enricher examples and tests.
3. **Command construction and static completion:** shared boa/Cobra builder,
   nested Group and command metadata, script generation for four shells, flag,
   command and single-positional choices. Add a completion example. Update tests
   that currently require hidden endpoints to be rejected; retain equivalent
   negative tests with `completion: false`.
4. **Dynamic completion:** opaque Partial/Get, With APIs, shared config/env
   collection and scoped callbacks. Add a deterministic namespace/resource
   example driven by another flag and JSON config, plus an optional file-backed
   example; tests need no external services.
5. **Multiple positionals and polish:** ordered position metadata, final List,
   help/deprecation/error output, integration regressions across nested commands
   and all enabled sources. Expand subcommand and positional examples.
6. **Reader guide and example suite:** consolidate docs/std/cli.md, link all
   focused examples, document setup and invocations, add a realistic composed
   CLI. Each implementation PR already includes its matching docs and examples;
   this final PR brings the narrative and composition coverage together.

Each PR branches from fresh main, rebases before reporting, gets a cold review,
and waits for CI. Locally run focused internal/driver tests for changed CLI
behavior, relevant doc snippet/link and example tests, golangci-lint and gofmt;
CI runs full suites. Avoid relying on unmerged tuple, rebinding, UTF-8 or context
changes. New examples must be fmt-clean, runnable with args.txt/config fixtures,
and included in the existing example harness.

Regression tests cover metadata errors before effects, explicit vs automatic
mapping, reserved/help names, shorthand collisions independent of later explicit
fields, prefix/disable composition, docs/default/required help, collected facts,
config zero/null values and precedence, no handler during help or completion,
all four generated scripts, nested completion endpoints, and state isolation
across queries. Test dynamic callback input visibility, invalid/missing unrelated
fields, typed Get mismatch, standalone vs sibling-dependent facts, config-selected
paths, config-disabled keys/required/defaults, disabled-long automatic shorts,
candidate protocol validation,
effects rejected when undeclared, and callback scope cleanup. Script tests assert
shell-specific generator output; protocol tests call __complete directly so
installed shells are not required. When available, add focused bash/fish/zsh
syntax checks without making PowerShell installation a local prerequisite.
