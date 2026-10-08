# bork/cli

Build command-line applications from records: flags, environment variables and
JSON or YAML configuration are decoded and validated before a typed handler runs.

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

fn main(): Ok | process.ExitCode {
  result = cli.Run[Options]("greet", "Greet someone", (options, s) => {
    ending = if (options.excited) { "!" } else { "." }
    println(s"Hello, ${options.name}${ending}")
  })
  match (result) {
    Ok => {}
    error: cli.Error => {
      return process.ExitCode { code: 2, message: error.Render("greet") }
    }
  }
}
```

Build with `bork build greet.bork -o greet`, then run `./greet --name Ada`.
The handler receives an `Options` record and a fresh scope; the scope closes
when the handler returns. Help skips the handler. Errors remain values, so the
application returns an ExitCode; after scope cleanup, main prints its message
with an `error:` prefix to stderr and exits with status 2.


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
error: Error: .name: is missing

Try 'greet --help' for usage.
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
| `(error: cli.Error) Render(command: String): String` | Format field errors and a usage hint. Pure; pass the result to eprintln. |
| `(partial: cli.Partial) Get[U: codec.Decode](field: String): U \| cli.Missing \| codec.DecodeError` | Decode one supplied partial field without proving the complete record. |

Both `name` and `description` are required arguments, even if description is `""`.
Ordinary Run propagates the effects of its callback. Tree and With APIs use the
closed bound of all five effects even when a particular callback uses fewer effects.

| Result type | Fields |
| --- | --- |
| `cli.Error` | `errors: List[codec.DecodeError]`; each DecodeError has `path: String, message: String`. |
| `cli.Help` | `text: String, diagnostics: String = ""`; stdout and stderr respectively. |
| `cli.Parsed[T]` | `options: T, warnings: List[String]`. |

### Render errors

`cli.Error { errors }` contains field paths and messages. Render formats one
`Error: path: message` line per issue, omitting the path for a whole-record error,
then a usage hint for the supplied command name. It returns a String without a
trailing newline, so eprintln supplies the newline and selects stderr.

```bork
import "bork/cli"
import "bork/codec"

fn main() {
  error = cli.Error { errors: [codec.DecodeError { path: ".name", message: "is missing" }] }
  eprintln(error.Render("greet"))
}
```

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
    error: cli.Error => eprintln(error)
  }
}
```

Fields whose selected decoder has string input take literal text; booleans accept `--excited` and `--excited=false`.
Enums whose selected decoder advertises known variants accept bare canonical
names or exact aliases. Matching is case-sensitive. Numbers and compound values
use JSON syntax. Lists take repeated flags:
`--tag one --tag two`; Elements with string input are literal; other elements use JSON.
Nested lists use a JSON array per occurrence. A non-optional Bool without a
declared default starts as false; `--excited` sets it true. Declared defaults
still apply, and Option[Bool] remains None when omitted. Other fields without
defaults are required; Option fields may be omitted. This inferred false is a
CLI source policy, including command trees, rather than a change to ordinary
codec decoding or `env.Load`. Field facts still validate the inferred value.
See [cli_bool](../../examples/cli_bool/main.bork).

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
| `configFile: Bool = false` | Select an additional JSON or YAML configuration file. |
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
    error: cli.Error => eprintln(error)
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

### Input labels and displayed defaults

Help labels follow the selected decoder's input kind. Built-in numbers display
labels such as `int` and `float`; Rune displays `rune`, and structured values
use `json`. Repeated lists display plural labels such as `strings` and `ints`.
Bool flags take no value when used to set true.

Defaults are readable display text: outer type prefixes are removed, an outer
Some is unwrapped, and None displays as `unset`. Nested values keep their
representation. These defaults are not guaranteed to be accepted input syntax.
A nonpositional field with an enabled environment binding but no flag appears under
`Environment variables:`; hidden and deprecated fields are omitted. Help skips
source reads and validation, so a required environment variable need not be set
just to view it. See [cli_help](../../examples/cli_help/main.bork).

## Names, environment and source policies

Pass `settings: cli.Settings { ... }` to Parse, ParseDetailed, Run or Subcommand.
Settings are per command; reuse the same value to share an application policy.
There is no process-global registry.

| Setting | Behavior |
| --- | --- |
| `version: String = ""` | Enable command-local --version output when nonempty |
| `helpGroups: List[cli.HelpGroup] = []` | Headings for immediate child commands at a tree/root entrypoint |
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
if long flags are disabled. Automatic names use the shared [codec words](codec.md): a field override `codec { name: "listen_port" }` produces `--listen-port`, while a type naming policy changes wire keys only. Automatic shorts and environment bindings skip
positional fields. Explicit environment bindings for positionals are supported.
An empty environment value is absent, following boa's rules.

Each Flag identifies an exact record field. `long`, `shortName`, and `envName`
accept `cli.Mapping.Auto`, `cli.Mapping.Disabled`, or
`cli.Mapping.Named { name: "exact-name" }`. Auto follows settings; Disabled
turns that source off; Named is exact and bypasses prefixes. `short` and `env` String fields are convenient aliases for Named. Do not specify
an alias together with a non-Auto mapping for the same source.

`long: Disabled` makes a field config/env-only; its automatic short is also
disabled. An explicit short requires an enabled canonical long flag. Env can
be disabled independently. `config: false` rejects that field's key in
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

FieldSpec is the immutable metadata passed through the chain. `aliases` contains already derived alternative long and env names; enrichers may rename or drop these lists:

| FieldSpec fields | Type/default |
| --- | --- |
| `field, description, deprecated` | `String` |
| `long, short, env` | `cli.Mapping` |
| `aliases` | `cli.FieldAliases = cli.FieldAliases {}` (`long, env: List[String] = []`) |
| `positional, configFile, config, hidden` | `Bool` |
| `position` | `Option[Int] = Option.None` |
| `collection` | `cli.Collection = cli.Collection.Repeat` |
| `choices` | `List[cli.Choice] = []` |
| `strictChoices, files, directories, keepOrder` | `Bool = false` |

Built-in policies preserve Disabled; a custom
function may deliberately enable it again. Auto returned by a custom function
is resolved under settings after the chain. Prefixes apply only to automatic
names, once. Enrichers cannot change record defaults, requiredness or facts.
See [the composed enricher example](../../examples/cli_enrichers/main.bork).

Codec field aliases become hidden long flags and env bindings. They receive no
automatic shorthand or deprecation warning. Auto aliases use the usual prefixes;
Named keeps the canonical name exact while aliases remain derived with prefixes.
Disabled disables that source's aliases. Alias env bindings are derived only with
a nonempty envPrefix. Canonical and alias names share duplicate checks before env
or config access. Supplying both names for one field in flags, env, or one config
object is an error; empty env values remain absent. Alias CLI values retain the
usual precedence over env and config. Persistent root flags follow the same rules.

## Version output

Set `settings: .{ version: "1.2.3" }` to enable `--version` for Parse/Run,
the With APIs, or a tree/root entrypoint. It prints
`<command path> version 1.2.3` followed by a newline. Explicit-argument APIs
return this output in `cli.Help`; Run APIs print it. Version output skips
configuration reads, field validation and handlers, even with required fields.

For a leaf, pass its own Settings to Subcommand; for a group, use
`group.copy(version: "1.2.3")`. `Command.version` is also available through copy
for per-command metadata. Versions are local to each command and are not
inherited by descendants. An empty version leaves an ordinary `version` field
available; enabling output reserves the long name `version` and rejects a
colliding flag. No automatic `-v` shorthand is added.
See [cli_version](../../examples/cli_version/main.bork).

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
    error: cli.Error => eprintln(error)
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

## Persistent root flags

Use `RootCommand[R]`, `RootSubcommand[R, T]` and `RootGroup[R]` when descendants
share a root options record. The leaf handler receives `(root, leaf, scope)`;
both records are validated before it runs. `RunRoot[R]` reads process arguments
and prints help; `DispatchRoot[R]` accepts explicit arguments and returns
`Ok | cli.Error | cli.Help`.

```bork
import "bork/cli"
import "bork/codec"
import "bork/process"
use codec.Defaults

type Global = { region: String = "west", verbose: Bool } derive (codec.Decode)
type Deploy = { service: String } derive (codec.Decode)
fn main(): Ok | process.ExitCode {
  commands = [cli.RootSubcommand[Global, Deploy]("deploy", "Deploy a service", (global, deploy, s) => {
    println(s"${global.region}/${deploy.service}/${global.verbose}")
  })]
  match (cli.RunRoot[Global]("fleet", "Shared flags", commands)) {
    error: cli.Error => { return process.ExitCode { code: 2, message: error.Render("fleet") } }
    Ok => {}
  }
}
```

`fleet --region east deploy --service api` and
`fleet deploy --service api --region east` use the same root value. Nested
`RootGroup[R]` branches inherit these flags too. Each record has its own `flags`,
`configFiles`, `settings` and `completions`; their configuration chains retain the
usual precedence independently. Root env-only fields appear in descendant help.
Root validation errors have paths beginning with `.root`.

Without a `run` callback, invoking the root displays help. Set
`run: Option.Some((global, s) => { println(global.region) })` on `RunRoot` or
`DispatchRoot` to handle a standalone root invocation; it does not run when a
child is selected. Both functions accept `flags`, `configFiles`, `settings` and
`completions` before the optional `run` argument.

Root fields cannot be positional. Root and descendant long-name collisions and
explicit shorthand collisions are metadata errors. An automatically assigned
root shorthand is dropped when a descendant uses it. Dynamic completers see
only their declaring record's partial options; omitted defaults remain Missing.
See [cli_root](../../examples/cli_root/main.bork) for a nested runnable example.

## Persistent-root value sources

`RootSubcommandResolved[R, T]` is the source-aware form of `RootSubcommand`.
Its handler receives `(cli.Resolved[R], cli.Resolved[T], Scope)` after both
records pass validation. It returns the same `RootCommand[R]`, so existing
`RootGroup[R]` branches and leaves with different option records work unchanged.
Legacy and source-aware leaves can share one command tree.

`DispatchRootResolved[R]` and `RunRootResolved[R]` accept the same flags, files,
settings, completions and commands as their ordinary root counterparts. Their
optional standalone `run` handler receives `(cli.Resolved[R], Scope)`.
`DispatchRootResolved` takes explicit arguments and returns `Ok | cli.Error |
cli.Help`; `RunRootResolved` reads process arguments, prints help/completion
output, and returns `Ok | cli.Error`. Like the existing root functions, both
charge `io + net + clock + random + state`. Without a standalone handler, root
invocation displays help. A standalone handler never runs for a selected leaf.

```bork
import "bork/cli"
import "bork/codec"
use codec.Defaults

type Global = { region: String = "west" } derive (codec.Decode)
type Deploy = { service: String } derive (codec.Decode)

fn main() {
  commands = [cli.RootSubcommandResolved[Global, Deploy]("deploy", "Deploy", (global, deploy, s) => {
    println(global.value.region)
    println(global.Source("region"))
    println(deploy.value.service)
    println(deploy.Source("service"))
  })]
  println(cli.RunRootResolved[Global]("fleet", "Shared flags", commands,
    run: Option.Some((global, s) => { println(global.Source("region")) })))
}
```

For `fleet --region east deploy --service api`, both sources are `Flag`.
Root flags work before or after the selected command, including through nested
`RootGroup` branches. Root and child config/env/flag precedence remains separate.
Queries use dotted bork field paths local to each snapshot: for example,
`global.Source("db.port")` and `deploy.Source("name")`. Root source keys have no
`.root` prefix; root validation errors still have that prefix. Codec wire names
and aliases do not change query keys. Parent defaults and config null resets
follow the same source rules as `ParseResolved`.

Both snapshots' `warnings` contain the diagnostics for the **whole selected
invocation**, including root/leaf flag and command deprecations. Dispatch still
prints each accepted warning once. Root-only snapshots contain their root
invocation warnings. Source-aware leaves can also be used with ordinary
`DispatchRoot`/`RunRoot`; ordinary leaves can be used with the resolved entry
points. Help, completion and validation failures never invoke these handlers.
Dynamic completers continue to receive only their declaring record's partial
inputs, with omitted fields represented by `Missing`.

## Command-line schema adapter

The options type needs a derived codec.Decode record schema, without a GoStruct
bound. The decoder validates supplied values and final sibling-dependent facts;
only a proven immutable record reaches the handler. Help never reads config
files or invokes a handler. See [entry points](#entry-points) for result handling.

## Configuration discovery

`FindConfig(appName, searchPaths = []) uses io` opts into discovery and returns
`Option[String] | cli.Error`. Without search paths, it searches `./<appName>.*`,
`$XDG_CONFIG_HOME/<appName>/config.*` (falling back to
`~/.config/<appName>/config.*` when unset), then `/etc/<appName>/config.*`.
The home directory comes from the operating system (`HOME` on Unix) and is
looked up only when needed.
Within each directory, the extension order is `.json`, `.yaml`, `.yml`.
The first regular file wins; discovery does not merge multiple matches or read
file contents. Directories are skipped, symlinks to regular files are followed,
missing candidates give `None`, and other filesystem errors give `cli.Error`.
The app name must be a nonempty file name without slashes, other than `.` or `..`.

Explicit search paths are file stems without extensions and replace the
defaults in priority order. For example, `["./app", "/etc/app/config"]` tries
`./app.json`, `./app.yaml`, `./app.yml`, then `/etc/app/config.json`, and so on. To load the match, pass it in
`configFiles`. For example:

```bork
import "bork/cli"
import "bork/codec"
use codec.Defaults

type Options = { port: Int = 8080 } derive (codec.Decode)

fn main() {
  match (cli.FindConfig("app")) {
    Option.Some(path) => println(cli.Parse[Options]("app", "Example", [], configFiles: [path]))
    Option.None => println(cli.Parse[Options]("app", "Example", []))
    error: cli.Error => println(error)
  }
}
```

Place the discovered path before explicit overlay files in `configFiles` to
keep those overlays higher priority. Environment and command-line inputs still
win over every file. `Parse` and `Run` never discover files automatically.
Calling `FindConfig` before parsing performs filesystem access even for help;
applications that require help without filesystem access can check for help
before opting into discovery.

## Winning value sources

`ParseResolved[T: codec.Decode](name, description, args, flags = [],
configFiles = [], settings = .{}) uses io` returns `cli.Resolved[T] | cli.Error |
cli.Help`. It uses the same precedence, validation, naming settings and warnings
as `ParseDetailed`; `Parse` and `Run` keep their existing return types.
`Resolved` contains `value: T`, `sources: List[cli.FieldSource]`, and
`warnings: List[String]`. No resolved value is returned for help or a failure.

`resolved.Source("db.port")` returns `Option[cli.Source]` for a dotted **bork
field path**, independent of config wire names or flag aliases. Flattened records
have one entry per leaf; `cli { flatten: false }` fields have one entry for the
whole value. The sources list follows schema order. Unknown paths return `None`.

| Source | Meaning |
| --- | --- |
| `cli.Source.Default` | The declared default, an inherited parent record default, or the implicit false boolean default won. |
| `cli.Source.Config { path }` | This file supplied the final value, including an explicit null. |
| `cli.Source.Env { name }` | This environment variable supplied the value; aliases retain the selected variable name. |
| `cli.Source.Flag { name }` | A flag or positional supplied the value. Flags identify the canonical long name, including when an alias or short flag was used; positionals identify the bork field path. |
| `cli.Source.Absent` | An optional field was omitted, or its enclosing optional flattened group is inactive. |

A null at a flattened group clears its inherited parent default and deactivates
its leaves until the group is supplied again. Inactive leaves have source
`Absent`; reactivated leaves report their actual supplying layer or their own
default. The group itself has no leaf entry. A null at an ordinary optional
leaf is still a `Config` source. Empty environment values remain absent.
False, zero, empty strings and empty lists retain their supplying layer.

```bork
import "bork/cli"
import "bork/codec"
use codec.Defaults

type Options = { port: Int = 8080 } derive (codec.Decode)

fn main() {
  match (cli.ParseResolved[Options]("app", "Example", ["--port", "9000"])) {
    resolved: cli.Resolved[Options] => {
      println(resolved.value.port)
      println(resolved.Source("port"))
    }
    error: cli.Error => println(error)
    help: cli.Help => println(help.text)
  }
}
```

For persistent-root and child snapshots, see
[persistent-root value sources](#persistent-root-value-sources).

## Reloading configuration

`Reload[T: codec.Decode](name, description, args, flags = [], configFiles = [],
settings = .{}) uses io` returns `cli.Resolved[T] | cli.Error | cli.Help`.
It reruns `ParseResolved` against the current files and environment, using the
arguments, metadata and settings supplied to this call. File overlays, selected
config files, env/flag precedence, and both field and whole-record validation
run again. The returned snapshot contains fresh values, winning sources and
warnings. No previous snapshot is modified, including when reload fails.

Keep the original arguments and parser settings if flags should stay fixed
across reloads; pass different arguments to change them. Discovery is opt-in:
rerun `FindConfig` explicitly if the selected path should change. `Reload` does
not install a file watcher, signal handler, or mutable configuration cell; the
application chooses when to call it and when to adopt a successful snapshot.
Help returns `cli.Help` without reading config files as in ordinary parsing.

```bork
import "bork/cli"
import "bork/codec"
import "bork/process"
use codec.Defaults

type Options = { port: Int = 8080 } derive (codec.Decode)

fn main() {
  arguments = process.Args()
  initial = cli.ParseResolved[Options]("app", "Example", arguments)
  println(initial)
  // A caller can invoke this later when it wants to refresh configuration.
  refreshed = cli.Reload[Options]("app", "Example", arguments)
  match (refreshed) {
    resolved: cli.Resolved[Options] => println(resolved.value)
    error: cli.Error => println(error)
    help: cli.Help => println(help.text)
  }
}
```

## Dumping effective options

`Dump[T: codec.Encode](value, format: cli.ConfigFormat = .Json)` returns
`String | cli.Error`. It encodes the complete effective value through the selected
`codec.Encode` instance, using JSON by default or YAML with `.Yaml`. Derived
encoders preserve canonical codec wire names, including naming policies and
field overrides. Effective defaults, false, zero, empty strings, empty lists and
Option values follow ordinary codec encoding; dumps do not filter by source.
The encoder must produce an object. Invalid codec number values produce an error.
Custom encoders and codec omission policies can intentionally omit fields, so
round-trip behavior follows the chosen Encode/Decode instances.

`DumpFile[T: codec.Encode](path, value) uses io` returns `Ok | cli.Error`.
`.yaml` and `.yml` extensions select YAML case-insensitively; every other name
selects JSON, just like config loading. Encoding completes before opening the
file, so an encoding failure leaves an existing destination intact. Successful
writes replace the file contents; parent directories must already exist. New
files use owner-only permissions (0600 before the process umask); existing file
permissions are retained. Write failures include the destination path.

```bork
import "bork/cli"
import "bork/codec"
use codec.Defaults

type Options = { port: Int = 8080 } derive (codec.Decode, codec.Encode)

fn main() {
  match (cli.Parse[Options]("app", "Example", ["--port", "9000"])) {
    options: Options => {
      println(cli.Dump(options))
      println(cli.Dump(options, .Yaml))
      println(cli.DumpFile("resolved.yaml", options))
    }
    error: cli.Error => println(error)
    help: cli.Help => println(help.text)
  }
}
```

With source-aware parsing, pass `resolved.value` to these same helpers.

## Configuration files

`configFiles` accepts JSON and YAML files using canonical codec wire keys
and field aliases. Without a naming policy or override, keys remain the exact
bork field names (`httpPort`, not `http-port`). With snake naming, that key is
`http_port`; an explicit field name overrides the policy. Mapping.Named and
enrichers rename CLI sources only, leaving config keys unchanged. Each file
selects its parser by its extension: `.yaml` and `.yml`, case-insensitively,
select YAML; all other names, including extensionless paths, select JSON. The
config-file selector follows the same rule, so one overlay chain can mix formats.

Files overlay from left to right; a later field replaces an earlier field
completely, including lists and nested records. Missing fields keep earlier
values. Empty file paths are skipped. Each document must decode to an object;
unknown top-level keys, malformed documents and unreadable files return
`cli.Error`. YAML parse errors retain line/column diagnostics. Nested records
follow each nested decoder's `codec.Unknown` policy.
See [cli_yaml](../../examples/cli_yaml/main.bork). Compound flag values still use
JSON syntax, regardless of the config file's format.

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

## Duration and instant inputs

Import `bork/time` and select `use time.Codecs` alongside `use codec.Defaults`
to decode time values from strings:

```bork
import "bork/cli"
import "bork/codec"
import "bork/time"
use codec.Defaults
use time.Codecs

type Options = {
  timeout: time.Duration
  since: Option[time.Instant]
  retries: List[time.Duration] = []
} derive (codec.Decode)

fn main() {
  println(cli.Parse[Options]("timer", "Time inputs", [
    "--timeout", "1h2m3s", "--since", "1970-01-01T00:00:00Z",
    "--retries", "250ms", "--retries", "1.5s"
  ]))
}
```

Duration uses `time.ParseDuration` syntax, including signed and compound values
such as `-250ms` and `1h2m3s`. Instant accepts RFC3339 timestamps with offsets
and optional fractional seconds. Values must fit signed nanoseconds. Option and
List wrappers preserve these string inputs; help labels them `string` or
`strings`, and environment scalar inputs use the same literal strings.

The opt-in codecs also use strings in JSON and YAML; numeric values are
rejected. Encoding formats Duration with `time.FormatDuration` and normalizes
Instant to UTC RFC3339 with nanosecond precision. Quote timestamps in YAML to
retain their string type. See [cli_time](../../examples/cli_time/main.bork).

## Subcommands

Build command trees with Subcommand and Group. Command.copy updates display
metadata without changing the leaf's typed options record.

| Command metadata | Default / meaning |
| --- | --- |
| `name: String, description: String` | Required name and one-line help. |
| `version: String = ""` | Command-local version text; empty disables version output. |
| `helpGroup: String = ""` | Heading ID used by this command's parent. |
| `helpGroups: List[cli.HelpGroup] = []` | Heading definitions for this command's children. |
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
    error: cli.Error => eprintln(error)
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
after the callback returns; their parsing and handler share one callback, so warning timing differs from typed Subcommand handlers. Use the
[persistent root APIs](#persistent-root-flags) for shared root options.

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

### Command help headings

Set `command.copy(helpGroup: "operations")` to categorize a command in its
parent's help. Define titles with `cli.HelpGroup { id: "operations", title:
"Operations:" }`: pass `settings.helpGroups` for a tree/root entrypoint, or
`group.copy(helpGroups: [...])` for a nested routing branch. RootCommand exposes
this metadata through its `command` field.

Configured headings appear in list order. Referenced IDs without a definition
get a heading `<id>:` in command declaration order. With no heading metadata,
help keeps its ordinary Available Commands list; otherwise ungrouped commands,
including generated help/completion commands, appear under Additional Commands.
Command names within each heading retain Cobra's alphabetical order. Hidden
and deprecated commands remain omitted from the visible command list.

IDs follow command-name character rules and must be nonempty; definition IDs
must be unique within their parent. Titles must be nonempty and contain no
newlines. Metadata errors are returned before source reads or handlers. Headings
change help presentation only; aliases, routing and completion keep their usual
behavior. See [cli_help_groups](../../examples/cli_help_groups/main.bork).

## Enum inputs

Derived sealed enums with fieldless known alternatives accept bare wire names:
`--level DEBUG`. The default codec naming policy uses SCREAMING_SNAKE for enum
names; codec naming overrides and aliases are shared with JSON and YAML.
Matching is exact and case-sensitive. Aliases decode to their known variant.

```bork
import "bork/cli"
import "bork/codec"
use codec.Defaults

type Level = sealed {
  Info,
  Debug codec { aliases: ["verbose"] },
  Other(String) codec { fallback: true }
} derive (codec.Decode)
type Options = { level: Level = Level.Info } derive (codec.Decode)

fn main() {
  println(cli.Parse[Options]("app", "Logging", ["--level", "verbose"]))
}
```

CLI flags, their environment bindings and positionals reject names outside the
advertised known variants, including when the codec has a fallback. Error
messages list canonical names. Configuration values and declared defaults keep
the selected codec's fallback behavior. Enum restrictions cannot be disabled by
changing `FieldSpec.strictChoices` or completion choices.

`Option` and `List` preserve enum metadata. Repeated flags populate enum lists:
`--levels INFO --levels DEBUG`. Names within nested JSON lists are also checked;
their input remains a JSON array per occurrence.

Help lists canonical choices. For scalar enums and lists with literal string
input, static completion automatically offers canonical names and variant docs;
input aliases and the fallback do not appear. Explicit choices, enrichers and
dynamic completers can customize suggestions while enum input validation stays
strict. Nested JSON-list fields keep their compound format and do not receive
bare-name suggestions.

The selected decoder's metadata governs this behavior. A custom decoder without
advertised variants retains its own input kind and validation. See
[cli_enums](../../examples/cli_enums/main.bork).

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

## CSV lists and key=value maps

By default, List flags keep one literal element per occurrence and Map fields
accept a whole JSON object. Opt in per field with the typed `cli` tag group.
Select `use cli.FieldTagsEncode` alongside `use codec.Defaults` where the options
record derives its decoder, so its schema can carry the validated tag value:

```bork
import "bork/cli"
import "bork/codec"
use codec.Defaults
use cli.FieldTagsEncode

type Options = {
  tags: List[String] = [] cli { collection: cli.Collection.Csv }
  labels: Option[Map[String, String]] cli { collection: cli.Collection.KeyValue }
} derive (codec.Decode)

fn main() {
  println(cli.Parse[Options]("app", "Collections", [
    "--tags", "web,worker", "--tags", "api", "--labels", "env=dev,team=ops"
  ]))
}
```

`Collection.Csv` splits each occurrence into CSV elements and appends repeated
occurrences. Standard CSV quotes preserve commas inside a value. For example,
`--tags '"one,two",three'` supplies two tags; an empty occurrence supplies an
empty list. Whitespace inside an element remains part of its value. Input must
contain one CSV record; malformed quoting and extra records return errors.

`Collection.KeyValue` accepts comma-separated `key=value` pairs, including
repeated flags. Keys must be nonempty; values split at the first `=`, so later
`=` characters remain in the value. Duplicate keys are errors naming the key.
The selected map-value decoder must accept string, number or bool input:
strings are literal, while numbers and booleans use JSON syntax. An empty
occurrence supplies an empty map; `key=` supplies an empty string value.
Structured map values keep whole JSON-object syntax in Repeat mode.

CLI environment bindings and final collection positionals use the same CSV
rules, including quoted commas. Configuration files retain ordinary arrays and
objects, and a higher-precedence source replaces the whole field. Field and
record facts still validate the decoded result. Partial completion sees decoded
supplied collections and keeps omitted fields Missing. Enrichers can override
`FieldSpec.collection`; incompatible modes are metadata errors before source
reads. Explicit `Collection.Repeat` preserves the default input policy.
See [cli_collections](../../examples/cli_collections/main.bork).

## Nested records

A field whose selected decoder advertises named record inputs becomes prefixed
flags. Each segment follows the shared codec naming policy: `db.host` becomes
`--db-host` and, with automatic environment bindings, `DB_HOST`. Codec overrides
and aliases apply to their own segment. CLI prefixes apply to the full name.
Explicit `Flag` and `Completion` metadata use dotted source paths such as
`field: "db.host"`; enrichers see the same stable identity.

```bork
import "bork/cli"
import "bork/codec"
use codec.Defaults

type Db = { host: String = "localhost", port: Int = 5432 } derive (codec.Decode)
type Options = { db: Db, replica: Option[Db] } derive (codec.Decode)

fn main() {
  println(cli.Parse[Options]("app", "Databases", ["--db-port", "8080"]))
}
```

In this example, no flags yields the leaf defaults for the database and
`replica: Option.None`.
Supplying any replica child activates that group; its missing required leaves
must then be supplied, while its own defaults still apply. CLI Bool switches
retain their inferred false default inside an active group.

Configuration keeps nested objects: `{"db":{"host":"config","port":5432}}`.
A child flag or environment value overrides that leaf and preserves configured
siblings. Record facts run after complete reconstruction. Partial completion
can read supplied leaves with `partial.Get[String]("db.host")`; omitted leaves
remain Missing.

To retain whole JSON input for a record field, select `use cli.FieldTagsEncode`
where its options decoder is derived and annotate it with `cli { flatten: false }`.
A flattened record field can also declare a parent default. Supplied children
overlay that value and preserve its other fields. Parent values take precedence
over leaf declaration defaults and inferred Bool false; config files, environment
values and CLI inputs then override the corresponding leaves in the usual order.

```bork
import "bork/cli"
import "bork/codec"
use codec.Defaults

type Db = { host: String = "localhost", port: Int = 5432, enabled: Bool } derive (codec.Decode)
type Options = { db: Db = Db { host: "production", port: 443, enabled: true } } derive (codec.Decode)

fn main() {
  // Keeps host "production" and enabled true, without requiring Encode.
  println(cli.Parse[Options]("app", "Database", ["--db-port", "8443"]))
}
```

An optional parent's Some default provides the same baseline; a None default
stays absent until a child activates the group. Explicit config null clears the
parent baseline, even if a later config file supplies a partial object. A
higher-precedence child can reactivate the group, using the leaves' own defaults
and requiredness for remaining inputs. Optional descendant record defaults stay
absent until those groups are activated. Parent facts run
on the reconstructed value. Reusable command options keep independent overlays;
partial completion still reports omitted leaves as Missing.

Defaults reach the CLI through the selected decoder's optional
[`codec.DefaultInput[T]`](codec.md#decoder-metadata) metadata and each record field's
lazy `defaultInput` provider. Standard and derived decoders supply this metadata
without an Encode bound. A custom decoder inside a parent default must publish
its own typed input conversion. Provider types are checked at compile time;
missing providers produce a field-path metadata error. Flattened parent input must be an object, or null for
an optional record. Presentation text from DefaultSchema is never parsed as input.
Custom selected decoders without record metadata retain their advertised input
kind, such as literal strings or whole JSON input.
Record groups cannot be positional arguments or configuration-file selectors;
individual scalar leaves can be positioned as usual. Configuration-file selectors
must be top-level fields. Metadata rejects more than 32 nested record levels or
256 accepted name paths for one leaf.

The same options type can be passed to multiple `Subcommand` constructors;
flattening also applies to persistent `RunRoot` options. See
[cli_nested](../../examples/cli_nested/main.bork).

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

Scalar positionals use the same decoder-based literal/JSON conversion as flags; a List
uses one original command-line word per element. Commas, whitespace, quotes and
empty words retain their literal meaning for string-input decoders. Other List
elements use JSON, so a List[List[Int]] can consume shell-quoted `[1,2]` and `[]` as two
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

Run `bork doc bork/cli` for checked public declarations.
The [CLI design](../design/cli.md) records implementation rationale.
