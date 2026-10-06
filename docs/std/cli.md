# bork/cli

Build command-line applications from records: flags, environment variables and
JSON configuration are decoded and validated before a typed handler runs.

Save this as `greet.bork`:

```bork
import "bork/cli"
import "bork/codec"
import "bork/process"
use codec.Defaults

type Options = {
  // Person to greet.
  name: String
  // Print an excited greeting.
  excited: Bool = false
} derive (codec.Decode)

fn main() {
  result = cli.Run[Options]("greet", "Greet someone", (options, s) => {
    ending = if (options.excited) { "!" } else { "." }
    println(s"Hello, ${options.name}${ending}")
  })
  match (result) {
    Ok => {}
    error: cli.Error => {
      eprintln(toString(error))
      process.Exit(2)
    }
  }
}
```

Build with `bork build greet.bork -o greet`, then run `./greet --name Ada`.
The handler receives an `Options` record and a fresh scope; the scope closes
when the handler returns. Help skips the handler. Errors remain values, so the
application prints them to stderr and exits with status 2.


```text
$ ./greet --name Ada
Hello, Ada.
$ ./greet --name Ada --excited
Hello, Ada!
$ ./greet --help
Greet someone

Usage:
  greet [flags]

Flags:
      --name string   Person to greet. (required)
      --excited       Print an excited greeting. (default false)
  -h, --help          help for greet
$ ./greet
Error { errors: [DecodeError { path: ".name", message: "is missing" }] }
$ echo $?
2
```

## Entry points

Import `bork/codec` and select `use codec.Defaults` for primitive and container
decoders. Options must be a record deriving `codec.Decode`. Arguments passed to
Parse or Dispatch exclude the executable name.

| Signature | Use |
| --- | --- |
| `Parse[T: codec.Decode](name: String, description: String, args: List[String], flags: List[cli.Flag] = [], configFiles: List[String] = [], settings: cli.Settings = .{}) uses io: T \| cli.Error \| cli.Help` | Parse explicit arguments without invoking a handler. |
| `ParseDetailed[T: codec.Decode](name: String, description: String, args: List[String], flags: List[cli.Flag] = [], configFiles: List[String] = [], settings: cli.Settings = .{}) uses io: cli.Parsed[T] \| cli.Error \| cli.Help` | Also return successful deprecation warnings. |
| `Run[T: codec.Decode](name: String, description: String, run: (T, Scope) => Ok, flags: List[cli.Flag] = [], configFiles: List[String] = [], settings: cli.Settings = .{}) uses io: Ok \| cli.Error` | Read process arguments, print help/warnings, and invoke the handler. Also carries the handler's effects. |
| `Subcommand[T: codec.Decode](name: String, description: String, run: (T, Scope) uses io + net + clock + random + state => Ok, flags: List[cli.Flag] = [], configFiles: List[String] = [], settings: cli.Settings = .{}): cli.Command` | Create a typed command leaf. Pure: stores the handler without invoking it. |
| `Group(name: String, description: String, children: List[cli.Command]): cli.Command` | Create a routing branch with no options or handler. |
| `Dispatch(name: String, description: String, arguments: List[String], commands: List[cli.Command], settings: cli.Settings = .{}) uses io + net + clock + random + state: Ok \| cli.Error \| cli.Help` | Execute a command tree with explicit arguments. |
| `RunCommands(name: String, description: String, commands: List[cli.Command], settings: cli.Settings = .{}) uses io + net + clock + random + state: Ok \| cli.Error` | Execute a command tree with process arguments and printed help/warnings. |
| `ParseWith[T: codec.Decode](name: String, description: String, args: List[String], completions: List[cli.Completion] = [], flags: List[cli.Flag] = [], configFiles: List[String] = [], settings: cli.Settings = .{}) uses io + net + clock + random + state: T \| cli.Error \| cli.Help` | Parse with effectful value completers. |
| `ParseDetailedWith[T: codec.Decode](name: String, description: String, args: List[String], completions: List[cli.Completion] = [], flags: List[cli.Flag] = [], configFiles: List[String] = [], settings: cli.Settings = .{}) uses io + net + clock + random + state: cli.Parsed[T] \| cli.Error \| cli.Help` | Also return successful warnings. |
| `RunWith[T: codec.Decode](name: String, description: String, run: (T, Scope) uses io + net + clock + random + state => Ok, completions: List[cli.Completion] = [], flags: List[cli.Flag] = [], configFiles: List[String] = [], settings: cli.Settings = .{}) uses io + net + clock + random + state: Ok \| cli.Error` | Run with dynamic completion. |
| `SubcommandWith[T: codec.Decode](name: String, description: String, run: (T, Scope) uses io + net + clock + random + state => Ok, completions: List[cli.Completion] = [], flags: List[cli.Flag] = [], configFiles: List[String] = [], settings: cli.Settings = .{}): cli.Command` | Create a typed leaf with dynamic completion. |
| `(partial: cli.Partial) Get[U: codec.Decode](field: String): U \| cli.Missing \| codec.DecodeError` | Decode one supplied partial field without proving the complete record. |

Both `name` and `description` are required arguments, even if description is `""`.
Ordinary Run propagates the effects of its callback. Tree and With APIs use the
closed bound of all five effects even when a particular callback uses fewer effects.

| Result type | Fields |
| --- | --- |
| `cli.Error` | `errors: List[codec.DecodeError]`; each DecodeError has `path: String, message: String`. |
| `cli.Help` | `text: String, diagnostics: String = ""`; stdout and stderr respectively. |
| `cli.Parsed[T]` | `options: T, warnings: List[String]`. |

### Parse explicit arguments

Use Parse to test input or decide how to display help yourself:

```bork
import "bork/cli"
import "bork/codec"
use codec.Defaults

type Options = { name: String } derive (codec.Decode)

fn main() {
  match (cli.Parse[Options]("greet", "Greet someone", ["--name", "Ada"])) {
    options: Options => println(options.name)
    help: cli.Help => {
      println(help.text)
      if (help.diagnostics != "") { eprintln(help.diagnostics) }
    }
    error: cli.Error => eprintln(toString(error))
  }
}
```

Strings are literal; booleans accept `--excited` and `--excited=false`.
Numbers and compound values use JSON syntax. Lists take repeated flags:
`--tag one --tag two`; String elements are literal, other elements use JSON.
Nested lists use a JSON array per occurrence. A field without a default is
required, including Bool; Option fields may be omitted. Defaults and facts use
the same rules as [ordinary decoding](codec.md).

Field failures are collected before the handler runs. Flag syntax failures
(such as malformed booleans) stop parsing before field validation. The complete
decoder checks relations between sibling fields after source merging.

## Flag metadata reference

Pass overrides in `flags`. Unspecified metadata follows Settings and the record
schema. `field` always names an exact bork field, independent of its flag name.

| Flag field | Meaning |
| --- | --- |
| `field: String` | Required record field name. |
| `short: String = "", env: String = ""` | Convenient exact-name mappings; cannot be combined with non-Auto shortName/envName. |
| `long: cli.Mapping = cli.Mapping.Auto` | Long flag policy. |
| `shortName: cli.Mapping = cli.Mapping.Auto` | Short flag policy. |
| `envName: cli.Mapping = cli.Mapping.Auto` | Environment binding policy. |
| `positional: Bool = false` | Shorthand for a single positional field. |
| `position: Option[Int] = Option.None` | Indexed position in an ordered layout. |
| `configFile: Bool = false` | Select an additional JSON configuration file. |
| `description: Option[String] = Option.None` | Override the field comment; Some("") suppresses it. |
| `config: Bool = true` | Allow this field in configuration documents. |
| `hidden: Bool = false` | Accept the flag but omit it from help/name completion. |
| `deprecated: String = ""` | Hide and warn when used. |
| `choices: List[cli.Choice] = []` | Suggest values in completion. |
| `strictChoices: Bool = false` | Also enforce choices during normal parsing. |
| `files: Bool = false, directories: Bool = false` | Enable file or directory completion; mutually exclusive. |
| `keepOrder: Bool = false` | Preserve candidate order in supporting shells. |

`cli.Mapping` has `Auto`, `Disabled`, and `Named { name: String }` variants.
`cli.Choice` has `value: String, description: String = ""`.

## Field documentation

```bork
import "bork/cli"
import "bork/codec"
use codec.Defaults

type Options = {
  // Person to greet.
  name: String = "Ada"
} derive (codec.Decode)

fn main() {
  match (cli.Parse[Options]("greet", "Greet someone", ["--help"],
    flags: [.{ field: "name", description: Option.Some("Recipient.") }])) {
    help: cli.Help => {
      println(help.text)
      if (help.diagnostics != "") { eprintln(help.diagnostics) }
    }
    error: cli.Error => eprintln(toString(error))
    options: Options => println(options.name)
  }
}
```

Leading `//` comments immediately above a record field provide its help text.
Multiple consecutive comment lines are joined; trailing comments and comments
separated from the field by a blank line are not field documentation. Required
markers and declared defaults appear in help too.

Override a field comment with `cli.Flag { field: "host", description:
Option.Some("Server address.") }`. Some with an empty String
suppresses the comment; None inherits it. Required/default markers still appear.
See [the documentation example](../../examples/cli_docs/main.bork).

## Names, environment and source policies

Pass `settings: cli.Settings { ... }` to Parse, ParseDetailed, Run or Subcommand.
Settings are per command; reuse the same value to share an application policy.
There is no process-global registry.

| Setting | Behavior |
| --- | --- |
| `completion: Bool = true` | Expose completion shell generators and hidden protocol endpoints |
| `autoLong: Bool = true` | Derive kebab-case long flags: httpPort becomes --http-port |
| `autoShort: Bool = false` | Derive the first ASCII letter of the canonical long name |
| `autoEnv: Bool = false` | Derive UPPER_SNAKE_CASE environment names |
| `flagPrefix: String = ""` | Prefix derived long flags, separated by a hyphen |
| `envPrefix: String = ""` | Prefix derived environment names, separated by an underscore |
| `enrichers: List[cli.Enricher] = []` | Compose pure metadata functions in list order |

Automatic shorts skip occupied letters and `h`; explicit short names on later
fields are reserved before automatic assignment. Derived environment names use
the resolved long name (including its prefix), or the field's kebab-case name
if long flags are disabled. Automatic shorts and environment bindings skip
positional fields. Explicit environment bindings for positionals are supported.
An empty environment value is absent, following boa's rules.

Each Flag identifies an exact record field. `long`, `shortName`, and `envName`
accept `cli.Mapping.Auto`, `cli.Mapping.Disabled`, or
`cli.Mapping.Named { name: "exact-name" }`. Auto follows settings; Disabled
turns that source off; Named is exact and bypasses prefixes. `short` and `env` String fields are convenient aliases for Named. Do not specify
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

Use `positional: true` as the single-field shorthand, or `position: Option.Some(0)` and subsequent indices for ordered positional fields. A final List
collects remaining arguments. Positional fields do not register long or short
flags; explicit long/short mappings on them are metadata errors. See
[ordered positionals](#ordered-positional-arguments) for layout rules.
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

```bork
import "bork/cli"
import "bork/codec"
use codec.Defaults

type Options = { name: String = "Ada" } derive (codec.Decode)

fn noConfig(previous: List[cli.FieldSpec], field: cli.FieldSpec): cli.FieldSpec {
  field.copy(config: false)
}

fn main() {
  println(cli.Parse[Options]("greet", "Greet someone", [],
    settings: .{ enrichers: [noConfig] }))
}
```

An `Enricher` is a pure `(List[cli.FieldSpec], cli.FieldSpec) uses nothing => cli.FieldSpec`
function. It receives completed prior specs in declaration order and the current
spec after built-in derivation. Return a copy with the desired metadata changes.
Each function in settings.enrichers runs once, in list order, for every field;
its output feeds the next function. Enrichers may override explicit mappings,
but must preserve the field identity. Final names are validated after enrichment.

FieldSpec is the immutable metadata passed through the chain:

| FieldSpec fields | Type/default |
| --- | --- |
| `field, description, deprecated` | `String` |
| `long, short, env` | `cli.Mapping` |
| `positional, configFile, config, hidden` | `Bool` |
| `position` | `Option[Int] = Option.None` |
| `choices` | `List[cli.Choice] = []` |
| `strictChoices, files, directories, keepOrder` | `Bool = false` |

Built-in policies preserve Disabled; a custom
function may deliberately enable it again. Auto returned by a custom function
is resolved under settings after the chain. Prefixes apply only to automatic
names, once. Enrichers cannot change record defaults, requiredness or facts.
See [the composed enricher example](../../examples/cli_enrichers/main.bork).

## Hidden flags and warnings

```bork
import "bork/cli"
import "bork/codec"
use codec.Defaults

type Options = { name: String = "Ada" } derive (codec.Decode)

fn main() {
  match (cli.ParseDetailed[Options]("greet", "Greet someone", ["--name", "Grace"],
    flags: [.{ field: "name", deprecated: "use --recipient instead" }])) {
    parsed: cli.Parsed[Options] => {
      println(parsed.options.name)
      for (warning in parsed.warnings) { eprintln(warning) }
    }
    help: cli.Help => {
      println(help.text)
      if (help.diagnostics != "") { eprintln(help.diagnostics) }
    }
    error: cli.Error => eprintln(toString(error))
  }
}
```

`hidden: true` hides a flag from help while keeping it accepted and validated.
`deprecated: "use --replacement instead"` hides a flag and warns when it is
used. These controls do not disable env/config input.

`ParseDetailed[T]` has the same arguments as Parse and returns
`cli.Parsed[T] | cli.Error | cli.Help`. Parsed contains options and a List[String]
of warnings. Parse returns options directly and discards successful warnings. Run prints warnings to stderr before the handler. A selected Subcommand
prints warnings the same way. Failed parses retain warnings in their error list.
Help contains text for stdout and diagnostics for stderr; explicit-argument APIs
return both without printing. Run and RunCommands print the corresponding streams.

## Command-line schema adapter

The options type needs a derived codec.Decode record schema, without a GoStruct
bound. The decoder validates supplied values and final sibling-dependent facts;
only a proven immutable record reaches the handler. Help never reads config
files or invokes a handler. See [entry points](#entry-points) for result handling.

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
import "bork/codec"

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

## Subcommands

Build command trees with Subcommand and Group. Command.copy updates display
metadata without changing the leaf's typed options record.

| Command metadata | Default / meaning |
| --- | --- |
| `name: String, description: String` | Required name and one-line help. |
| `aliases: List[String] = []` | Alternate sibling names. |
| `longDescription: String = "", examples: String = ""` | Detailed help and usage examples. |
| `hidden: Bool = false, deprecated: String = ""` | Hide without rejecting; deprecation also warns. |
| `children: List[cli.Command] = [], group: Bool = false` | Set by Group for routing branches. |
| `execute: (String, List[String], Scope) uses io + net + clock + random + state => Ok \| cli.Error \| cli.Help` | Explicit callback; prefer Subcommand for typed options and completion. |

```bork
import "bork/cli"
import "bork/codec"
use codec.Defaults

type Options = { name: String } derive (codec.Decode)

fn main() {
  greet = cli.Subcommand[Options]("greet", "Greet someone", (options, s) => {
    println(s"Hello, ${options.name}.")
  })
  commands = [cli.Group("people", "People commands", [greet.copy(aliases: ["g"])])]
  match (cli.Dispatch("app", "Example application", ["people", "g", "--name", "Ada"], commands)) {
    Ok => {}
    help: cli.Help => {
      println(help.text)
      if (help.diagnostics != "") { eprintln(help.diagnostics) }
    }
    error: cli.Error => eprintln(toString(error))
  }
}
```

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
is required for storing heterogeneous callbacks and follows the `http.Handler` convention. Use ordinary `Parse`/`Run` when selective callback
effect propagation is needed.

Use `cli.Group(name, description, children)` to build nested routing branches.
Groups have no option record or handler. Leaves retain their own flags, env
mappings and config files. Set aliases, longDescription, examples or hidden
with `Command.copy(...)`; aliases work in dispatch, help and completion. Hidden
commands remain callable. `deprecated: "use replacement"` also omits a command
from help and name completion, while accepting it and warning to stderr before
its typed handler. Selecting the command with `old --help` returns the warning
in diagnostics; `help old` only displays its help and does not invoke that
command or emit its warning. Failures retain warnings in the
error list. The warning belongs to the selected command, including a group
selected for help; ancestor groups do not warn during a child invocation. Manual
execute callbacks preserve warnings in Help/Error and print successful warnings
after the callback returns; their parsing and handler share one callback, so warning timing differs from typed Subcommand handlers. Root and persistent flags are not exposed.

Names and aliases contain letters, digits, hyphens or underscores, cannot start
with a hyphen, and must be unique among siblings. `help`, `completion`,
`__complete` and `__completeNoDesc` are reserved. The entire tree and all leaf
metadata are validated before execution. `help <group> <leaf>` and aliases select
nested help. Unknown help targets return errors. Manually constructed Command.execute callbacks are supported, with no derived flag/value completion.

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

```bork
import "bork/cli"
import "bork/codec"
use codec.Defaults

type Options = { mode: String = "apply" } derive (codec.Decode)

fn main() {
  flags = [cli.Flag {
    field: "mode"
    choices: [.{ value: "apply", description: "Make changes" }, .{ value: "inspect" }]
    strictChoices: true
  }]
  println(cli.Parse[Options]("app", "Choose a mode", ["--mode", "inspect"], flags))
}
```

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

| Completion type | Fields or signature |
| --- | --- |
| `cli.Completion` | `field: String, suggest: cli.Completer`. |
| `cli.Completer` | `(cli.CompletionRequest, Scope) uses io + net + clock + random + state => cli.Suggestions \| cli.Error`. |
| `cli.CompletionRequest` | `field: String, prefix: String, arguments: List[String], partial: cli.Partial`. |
| `cli.Suggestions` | `choices: List[cli.Choice], files: Bool = false, directories: Bool = false, keepOrder: Bool = false`. |
| `cli.Missing` | `field: String`. |
| `cli.Partial` | Opaque snapshot; query with `Get[U: codec.Decode](field: String): U \| cli.Missing \| codec.DecodeError`. |

Use `ParseWith`, `RunWith` or `SubcommandWith` to register effectful value
completers. Their ordinary parsing behavior matches Parse/Run/Subcommand, while
the With APIs charge the closed `io + net + clock + random + state` bound.
Ordinary Parse uses io; Run also propagates its handler's effects. Each completer has
type `(cli.CompletionRequest, Scope) uses io + net + clock + random + state =>
cli.Suggestions | cli.Error`. Pure functions with that result union can also be
used as completers. `ParseDetailedWith` has the same arguments as ParseWith
and returns `cli.Parsed[T] | cli.Error | cli.Help`, preserving successful flag
warnings in Parsed.warnings. ParseWith discards successful warnings for the same
reason as Parse; RunWith prints them to stderr before the handler.

```bork
import "bork/codec"
import "bork/cli"
use codec.Defaults
type Options = { namespace: String = "dev", resource: String } derive (codec.Decode)
fn resources(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error {
  namespace = match (request.partial.Get[String]("namespace")) {
    value: String => value
    _: cli.Missing => "dev"
    error: codec.DecodeError => { return cli.Error { errors: [error] } }
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

`partial.Get[U: codec.Decode](field)` returns `U | cli.Missing | codec.DecodeError`. It first
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

## Ordered positional arguments

```bork
import "bork/cli"
import "bork/codec"
use codec.Defaults

type Options = { action: String, files: List[String] } derive (codec.Decode)

fn main() {
  println(cli.Parse[Options]("app", "Process files", ["copy", "first.txt", "second.txt"],
    flags: [.{ field: "action", position: Option.Some(0) },
      .{ field: "files", position: Option.Some(1) }]))
}
```

Set `Flag.position` to Some with a zero-based index for each positional field.
Indices must be unique and contiguous, and do not depend on record declaration
order. Positional display names use kebab-case and must be unique; collisions
such as fooBar/fooBAR return metadata errors. Do not mix indexed positions with the single-field `positional: true`
shorthand, including on the same field. At most one List is allowed, in the final
slot. Optional/default scalar fields cannot precede required scalar fields.
These layout failures return metadata errors before reading config or env.

Scalar positionals use the same literal String/JSON conversion as flags; a List
uses one original command-line word per element. Commas, whitespace, quotes and
empty String words retain their literal meaning. Non-String List elements use
JSON, so a List[List[Int]] can consume shell-quoted `[1,2]` and `[]` as two
elements. Mapped env and JSON config can fill omitted positions under ordinary
precedence; provided words always occupy their indexed slots.

Static and dynamic completion target the current positional index. Scalar
completion advances after a supplied word and stops after the final scalar;
a final List keeps completing additional elements. Dynamic completers can read
already supplied scalar positions through Partial.Get. `--` stops flag parsing
while preserving positional completion and conversion.

See [the positional example](../../examples/cli_positionals/main.bork):

```sh
bork run examples/cli_positionals -- copy 2 first.txt second.txt
bork run examples/cli_positionals -- __completeNoDesc copy ''
```

## More examples

The [CLI application cookbook](cli-cookbook.md) starts with a small program and
builds up to the [fleet application](../../examples/cli_fleet/main.bork). Focused
examples cover [basic options](../../examples/cli/main.bork),
[field comments](../../examples/cli_docs/main.bork),
[environment policy](../../examples/cli_env/main.bork),
[mapping](../../examples/cli_mapping/main.bork),
[enrichers](../../examples/cli_enrichers/main.bork),
[completion](../../examples/cli_completion/main.bork),
[command trees](../../examples/cli_tree/main.bork),
[dynamic suggestions](../../examples/cli_dynamic/main.bork), and
[positionals](../../examples/cli_positionals/main.bork).

The [CLI design](../design/cli.md) records implementation rationale.
