# bork/cli

## Command-line options

`Parse[T]` accepts arguments without the executable name and returns a proven
options record, `cli.Error`, or `cli.Help`. The record must derive `codec.Decode`.
`Run[T]` reads process arguments and invokes `(T, Scope) => Ok` only after
successful validation. Applications render errors and choose their exit code.
Both use `io`; Run also carries its handler's effects.

Strings are literal, booleans accept `--verbose` and `--verbose=false`, and
numbers and compound values use JSON syntax. Lists take repeated flags;
string elements are literal and other elements use JSON. A plain field without
a default is required, including Bool. Option fields may be omitted. Record
defaults and facts follow the same rules as ordinary decoding.

## Field documentation

Leading `//` comments immediately above a record field provide its help text.
Multiple consecutive comment lines are joined; trailing comments and comments
separated from the field by a blank line are not field documentation. Required
markers and declared defaults appear in help too.

Override a field comment with `cli.Flag { field: "host", description:
Option.Some { value: "Server address." } }`. Some with an empty String
suppresses the comment; None inherits it. Required/default markers still appear.
See [the documentation example](../../examples/cli_docs/main.bork).

## Names, environment and source policies

Pass `settings: cli.Settings { ... }` to Parse, ParseDetailed, Run or Subcommand.
Settings are per command; reuse the same value to share an application policy.
There is no process-global registry.

| Setting | Default | Behavior |
| --- | --- | --- |
| completion | true | Expose completion shell generators and hidden protocol endpoints |
| autoLong | true | Derive kebab-case long flags: httpPort becomes --http-port |
| autoShort | false | Derive the first ASCII letter of the canonical long name |
| autoEnv | false | Derive UPPER_SNAKE_CASE environment names |
| flagPrefix | empty | Prefix derived long flags, separated by a hyphen |
| envPrefix | empty | Prefix derived environment names, separated by an underscore |
| enrichers | empty | Compose pure metadata functions in list order |

Automatic shorts skip occupied letters and `h`; explicit short names on later
fields are reserved before automatic assignment. Derived environment names use
the resolved long name (including its prefix), or the field's kebab-case name
if long flags are disabled. Automatic shorts and environment bindings skip
positional fields. Explicit environment bindings for positionals are supported.
An empty environment value is absent, following boa's rules.

Each Flag identifies an exact record field. `long`, `shortName`, and `envName`
accept `cli.Mapping.Auto`, `cli.Mapping.Disabled`, or
`cli.Mapping.Named { name: "exact-name" }`. Auto follows settings; Disabled
turns that source off; Named is exact and bypasses prefixes. Existing `short`
and `env` String fields remain convenient aliases for Named. Do not specify
an alias together with a non-Auto mapping for the same source.

`long: Disabled` makes a field config/env-only; its automatic short is also
disabled. An explicit short requires an enabled canonical long flag. Env can
be disabled independently. `config: false` rejects that field's key in JSON
config files, including null; defaults and enabled CLI/env sources still work.
To disable all derived flags, set autoLong and autoShort false. Explicit Named
long flags can still opt individual fields in.

Long names contain letters, digits, underscores and hyphens and cannot start
with a hyphen. Short names are one ASCII character; `h` and the long name `help`
are reserved. Environment names are identifiers made of ASCII letters, digits
and underscores, with no leading digit. Duplicate resulting long/short/env
names, duplicate Flag metadata, invalid names and unknown fields return errors
before environment/config access. Unknown fields include a closest-name hint.

There is currently at most one positional field; mark it with `positional: true`.
A List positional collects remaining arguments. It does not register a long or
short flag. Explicit long/short mappings on a positional are metadata errors.
See [the flag mapping example](../../examples/cli_mapping/main.bork) and
[the environment example](../../examples/cli_env/main.bork).

```bork
import "bork/codec"
import "bork/cli"
use codec.Defaults
type Options = { host: String = "localhost", port: Int = 8080 } derive (codec.Decode)
fn main() {
  println(cli.Parse[Options]("app", "Example", ["--listen", "443"],
    flags: [.{ field: "port", long: cli.Mapping.Named { name: "listen" }, short: "p" }],
    settings: .{ autoEnv: true, envPrefix: "MY_APP" }))
}
```

This exposes --host, --listen/-p, MY_APP_HOST and MY_APP_LISTEN. Flags override
environment values. Environment loading through `bork/env` remains independent.

## Custom enrichers

An `Enricher` is a pure `(List[cli.FieldSpec], cli.FieldSpec) => cli.FieldSpec`
function. It receives completed prior specs in declaration order and the current
spec after built-in derivation. Return a copy with the desired metadata changes.
Each function in settings.enrichers runs once, in list order, for every field;
its output feeds the next function. Enrichers may override explicit mappings,
but must preserve the field identity. Final names are validated after enrichment.

FieldSpec contains field, long, short, env, description, positional, configFile,
config, hidden, deprecated, choices, strictChoices, files, directories and keepOrder. Built-in policies preserve Disabled; a custom
function may deliberately enable it again. Auto returned by a custom function
is resolved under settings after the chain. Prefixes apply only to automatic
names, once. Enrichers cannot change record defaults, requiredness or facts.
See [the composed enricher example](../../examples/cli_enrichers/main.bork).

## Hidden flags and warnings

`hidden: true` hides a flag from help while keeping it accepted and validated.
`deprecated: "use --replacement instead"` hides a flag and warns when it is
used. These controls do not disable env/config input.

`ParseDetailed[T]` has the same arguments as Parse and returns
`cli.Parsed[T] | cli.Error | cli.Help`. Parsed contains options and a List[String]
of warnings. Parse preserves its original result union and discards successful
warnings. Run prints warnings to stderr before the handler. A selected Subcommand
prints warnings the same way. Failed parses retain warnings in their error list.
Help contains text for stdout and diagnostics for stderr; explicit-argument APIs
return both without printing. Run and RunCommands print the corresponding streams.

## Command-line schema adapter

The stdlib builds a boa reflection shadow struct from the existing derived codec.Decode
field schema; no GoStruct bound is needed. Optional shadow pointers track omitted
inputs. The adapter converts supplied values, collects missing/type/fact errors
across fields, and then invokes the complete record decoder. Sibling-dependent
facts are checked by that final decoder. Boa flag syntax failures (including
invalid booleans) stop parsing before field validation. Only the proven record
reaches the handler. Help reads no configuration files and invokes no handler.

## Configuration files

`configFiles` is a list of JSON files using the options record's exact bork field
names (`httpPort`, not `http-port`). Files overlay from left to right; a later
field replaces an earlier field completely, including lists and nested records.
Missing fields keep earlier values. Empty file paths are skipped. JSON must be
an object; unknown top-level keys, malformed JSON, and unreadable files return
`cli.Error`. Nested records follow their derived decoder's usual rules, which
ignore unknown nested keys.

Mark one String or Option[String] field with `cli.Flag { field: "config",
configFile: true }` to expose `--config <path>` (with ordinary short/env metadata
if desired). Its path comes from flags, its mapped environment variable, or its
declared default. A nonempty selected path overlays `configFiles`; values inside
files do not select additional files. Two selector fields or a selector with
another field type are metadata errors. Optional selector fields must declare
`Option[String]` concretely in the record schema. An empty selector path loads no extra
file.

Precedence, highest first: explicit CLI flags/positionals, mapped environment
values, the selected config file, later `configFiles`, earlier `configFiles`,
then declared defaults. An empty environment value is absent, as in ordinary
parsing. False, zero, empty lists, and explicit null retain their meaning; null
is valid only when the decoder accepts it. The adapter validates the final
merged values, collecting field errors; a lower-precedence invalid value can be
overridden by a valid flag. Only the resulting immutable proven record reaches
the handler. Help does not read files or invoke the handler.

```bork
import codec "bork/codec"

import "bork/cli"
use codec.Defaults

type Options = {
  config: Option[String]
  port: Int = 8080
} derive (codec.Decode)

fn main() {
  result = cli.Run[Options]("app", "Example", (options, s) => println(options.port), flags: [cli.Flag { field: "config", configFile: true }], configFiles: ["settings.json"])
  println(result)
}
```

Run with `app --config local.json --port 9000`. `settings.json` might contain
`{"port": 8081}`; `local.json` might contain `{"port": 8082}`. The handler sees
9000.

## Examples

`bork/cli` wraps boa to parse proven options from a record deriving `codec.Decode`.
It generates help from field docs and defaults, supports short flags, explicit
environment bindings, positionals, and repeated list flags, and collects field
errors before invoking a handler. See [examples/cli](../../examples/cli/main.bork). Focused examples cover
[field documentation](../../examples/cli_docs/main.bork),
[flag mapping](../../examples/cli_mapping/main.bork),
[environment policies](../../examples/cli_env/main.bork), and
[custom enrichers](../../examples/cli_enrichers/main.bork).

The checked-in example also supports a selectable config file:

```sh
bork run examples/cli -- --config examples/cli/config.json --port 9000
```

## Subcommands

`Subcommand[T: codec.Decode](name, description, handler, flags = [], configFiles = [], settings = .{})` creates a
`cli.Command`, capturing the derived decoder and a typed handler for `T`.
Commands with different option records can share a `List[cli.Command]`; the public command stores an erased callback, while decoded options keep type
`T` inside that callback. The handler never receives
raw JSON or another command's options.

`Dispatch(name, description, arguments, commands, settings = .{})` accepts explicit arguments
without the executable name and returns `Ok | cli.Error | cli.Help`.
`RunCommands(name, description, commands, settings = .{})` reads process arguments and prints
help, returning `Ok | cli.Error`. Both use cobra to select one subcommand and
boa to parse its own derived option record, including command-specific flags,
environment mappings, configFiles, and config-file selectors. A selected handler runs in a fresh
scope, and cleanup finishes before dispatch returns. Errors and help never run
a handler. No command, root `--help`, `<command> --help`, and `help <command>`
produce help; command help includes field docs/defaults and its own flags.
Unknown commands and invalid flags return errors.

Each handler has the closed type `(T, Scope) uses io + net + clock + random +
state => Ok`. `Subcommand` itself is pure: it resolves metadata/enrichers and stores the handler
without running it. `Command.execute`, `Dispatch`, and `RunCommands` conservatively
charge all five effects, even if the selected handler uses fewer. A fixed bound
is required for storing heterogeneous callbacks and follows the existing
`http.Handler` convention. Use ordinary `Parse`/`Run` when selective callback
effect propagation is needed.

Use `cli.Group(name, description, children)` to build nested routing branches.
Groups have no option record or handler. Leaves retain their own flags, env
mappings and config files. Set aliases, longDescription, examples or hidden
with `Command.copy(...)`; aliases work in dispatch, help and completion. Hidden
commands remain callable. Root and persistent flags are not exposed.

Names and aliases contain letters, digits, hyphens or underscores, cannot start
with a hyphen, and must be unique among siblings. `help`, `completion`,
`__complete` and `__completeNoDesc` are reserved. The entire tree and all leaf
metadata are validated before execution. `help <group> <leaf>` and aliases select
nested help. Unknown help targets return errors. Existing manually constructed
Command.execute callbacks remain supported, with no derived flag/value completion.

See [the nested command example](../../examples/cli_tree/main.bork):

```sh
bork run examples/cli_tree -- k d -n prod web worker
bork run examples/cli_tree -- help cluster deploy
```

See [examples/subcommands](../../examples/subcommands/main.bork):

```sh
bork run examples/subcommands -- serve --host localhost -p 443
bork run examples/subcommands -- echo one two
bork run examples/subcommands -- help serve
```

## Shell completion and static choices

Completion is enabled by default for both ordinary Parse/Run and command trees.
Build the [completion example](../../examples/cli_completion/main.bork), then
install a script for your shell:

```sh
bork build examples/cli_completion -o deploy
./deploy completion bash > deploy.bash
./deploy completion zsh > _deploy
./deploy completion fish > deploy.fish
./deploy completion powershell > deploy.ps1
```

Source deploy.bash in Bash, put _deploy on your zsh fpath, install deploy.fish
under your fish completions directory, or dot-source deploy.ps1 in PowerShell.
The native Cobra generators handle shell quoting and descriptions. Generating a
script or requesting help never reads config files or runs a handler or its
finalizers. Static value completion also skips configuration and handlers. Completion does not require a complete valid options
record; required fields may be omitted while choosing a value.

A Flag's `choices: List[cli.Choice]` supplies value/description pairs for long
and short flag values and the positional field. Choices are filtered by the
current prefix and deduplicated by value, preserving the first description.
They suggest values without restricting normal parsing. `strictChoices: true`
also validates String, Option[String], List[String] and Option[List[String]]
inputs, including their declared defaults. Normal CLI/env/config precedence
applies before membership checks. Other field types, an empty strict choice set,
empty choice values, or tabs/newlines in values/descriptions are metadata errors.
Enrichers can change choices and completion policy in FieldSpec too.

Default value completion suppresses filename suggestions. Set `files: true` to
allow shell file completion, `directories: true` to request directory completion,
or `keepOrder: true` to preserve the declared order in shells that support it.
Files and directories are mutually exclusive. Hidden/deprecated flags are omitted
from flag-name completion and return no value candidates. Command and group
completion uses the same native tree as dispatch and help.

The hidden `__complete` and `__completeNoDesc` endpoints return Cobra's candidate
lines and final numeric directive, such as `:4` for no filename completion.
Parse/Dispatch return this stdout in Help.text; successful protocol diagnostics
are returned separately in Help.diagnostics. Run/RunCommands print each stream.
Cobra's native routing/flag completion failures retain its `:0` directive and
write error debug messages directly to process stderr, even through explicit
Parse/Dispatch; those upstream error messages cannot be captured in Help.diagnostics.

Set `settings: .{ completion: false }` to reject the visible completion command
and both hidden endpoints. For a tree, pass these settings to Dispatch or
RunCommands: the entrypoint controls the whole tree. Subcommand settings govern
field mappings, and its completion setting applies when calling Command.execute
directly. Disabled completion leaves ordinary flags and positionals available.

## Dynamic completion from partial inputs

Use `ParseWith`, `RunWith` or `SubcommandWith` to register effectful value
completers. Their ordinary parsing behavior matches Parse/Run/Subcommand, while
the With APIs charge the closed `io + net + clock + random + state` bound.
Ordinary Parse and Run keep their existing effect behavior. Each completer has
type `(cli.CompletionRequest, Scope) uses io + net + clock + random + state =>
cli.Suggestions | cli.Error`. Pure functions with that result union can also be
used as completers. `ParseDetailedWith` has the same arguments as ParseWith
and returns `cli.Parsed[T] | cli.Error | cli.Help`, preserving successful flag
warnings in Parsed.warnings. ParseWith discards successful warnings for the same
reason as Parse; RunWith prints them to stderr before the handler.

```bork
import "bork/cli"
type Options = { namespace: String = "dev", resource: String } derive (Decode)
fn resources(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error {
  namespace = match (request.partial.Get[String]("namespace")) {
    value: String => value
    missing: cli.Missing => "dev"
    error: DecodeError => { return cli.Error { errors: [error] } }
  }
  cli.Suggestions { choices: [.{ value: s"${namespace}-web", description: "Web service" }] }
}
fn main() {
  println(cli.ParseWith[Options]("deploy", "", ["__complete", "--namespace", "team", "--resource", ""],
    completions: [.{ field: "resource", suggest: resources }]))
}
```

CompletionRequest contains the exact record field name, current prefix, prior
positional words in arguments, and an opaque Partial snapshot. Partial includes
supplied CLI flags/positionals, mapped env and overlaid config inputs under normal
precedence, excluding the word being completed. Config-file selectors can use
their ordinary declared path default. Each query creates fresh state and reads
its sources once. Help and shell-script generation invoke no completer and read
no config files.

`partial.Get[U: Decode](field)` returns `U | cli.Missing | DecodeError`. It first
checks the original field decoder's independent constraints, then decodes U;
unknown fields, malformed input and a requested type mismatch retain field paths.
U can be a derived user record. Get does not prove relations between sibling
fields or claim that the whole options record is valid. Normal dispatch still
checks all fields and the complete record before running its typed handler.

Omitted inputs remain Missing even when their record declares defaults. Match
Missing to choose an explicit fallback, as above. A supplied null is an input,
so Get[Option[String]] returns None for config null rather than Missing. Invalid
unrelated fields remain errors on their own Get calls and do not block completion
of another field. Syntax/binding failures that prevent collecting raw inputs
return a completion error before the callback runs.

Each callback runs in a fresh Scope; its cleanup completes before completion
returns. The command handler and its finalizers never run during completion.
Return unescaped Choice values and descriptions in Suggestions. The library
filters by prefix, deduplicates by value and applies files/directories/keepOrder
using the same policy as static choices. Dynamic suggestions replace static
choices for that field. Unknown/duplicate callback fields and fields without an
enabled flag or positional are metadata errors. Tabs/newlines in returned choices
and conflicting file/directory policy return an error directive.

Our config/binding/callback failures return Help with `:1` on stdout and errors in
diagnostics for stderr. ParseWith/Dispatch return these streams; RunWith and
RunCommands print them. Cobra's own flag/routing syntax failures retain the
native behavior described above. Completers should keep their own stdout clear
for the shell protocol.

See [the dynamic example](../../examples/cli_dynamic/main.bork), which combines
namespace flags, env and JSON config with resource suggestions:

```sh
bork run examples/cli_dynamic -- --namespace team --resource team-web
bork run examples/cli_dynamic -- __complete --config examples/cli_dynamic/settings.json --resource team-w
bork run examples/cli_dynamic -- completion bash
```
