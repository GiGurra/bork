# bork/cli vs boa: gap report

Ticket: bork-xls6ya. Phase 1 (report only, no code).

Sources compared:

- bork: `internal/std/cli/*.bork`, `docs/std/cli.md`, `docs/std/cli-cookbook.md`,
  `examples/cli*`, `examples/subcommands`, `internal/driver/cli*_test.go`.
- boa (read-only checkout of github.com/GiGurra/boa): `README.md`, `CLAUDE.md`,
  `docs/*.md`, `pkg/boa`, `pkg/boaviper`.

Uncertain rows were checked by running small probe programs against a bork
built from main (commit 14ffffcd). Probe results are quoted where they matter.

Legend: **Y** supported, **P** partial, **N** missing, **n/a** deliberately
different design (no gap to fill). "Example" means a runnable program under
`examples/` with `args.txt` and an expected output in `testdata/examples/`.
Doc snippets and Go driver tests don't count as examples.

## 1. The three questions in the ticket

| Question | Answer | Evidence |
| --- | --- | --- |
| Example with names derived automatically from field names? | **Partly.** Auto long flags appear in every example (`serverHost` → `--server-host`, `keepAlive` → `--keep-alive`). Auto short flags appear in `cli_mapping` and `cli_fleet`. Auto env names (`autoEnv`, `envPrefix`) are configured in `cli_env` and `cli_fleet`, but **no example run reads an environment variable**. | `examples/cli_env/args.txt` only passes `--server-host`; `TestExamples` runs without an environment. |
| Example with names set manually (long, short, env)? | **Partly.** Explicit short (`cli`, `subcommands`, `cli_tree`), `Mapping.Named` long (`cli_mapping`), and an explicit env name (`cli_dynamic`: `env: "DEPLOY_NAMESPACE"`) exist, but are spread over several examples. No example turns automation off (`autoLong: false`) and names everything by hand. No example run sets the env var. | |
| Example mixing the two in one command? | **Yes, but not labelled.** `cli_mapping` mixes auto `--service-server-host`/`-s` with a manual `--listen`/`-p`. `cli_fleet` mixes `autoShort`+`autoEnv`+`envPrefix` with explicit disables. Neither says that this is the point, and neither shows env. | |

Root cause for the env half: `cli.Parse` reads the process environment directly
and has no injection point (unlike `env.LoadWith`). `TestExamples` can't set env
vars either. Env behaviour is only covered by Go driver tests using `t.Setenv`
(`internal/driver/cli_test.go`, `cli_mapping_test.go`).

## 2. Feature table

### Naming and sources

| boa feature | bork | Example | Notes |
| --- | --- | --- | --- |
| Auto kebab-case long flag from field name | Y | Y | `Settings.autoLong`, default on. Acronyms ok: `apiURL` → `--api-url`, `dbHost` → `--db-host` (probe). |
| Auto short flag (first free letter, `h` reserved) | Y | Y | `autoShort`. **Default differs:** on in boa (`ParamEnricherDefault`), off in bork. |
| Auto env var (UPPER_SNAKE from flag name) | Y | P | `autoEnv`; never exercised in an example run (see §1). |
| Env prefix (`ParamEnricherEnvPrefix`) | Y | P | `envPrefix`; same caveat. |
| Flag prefix | Y (bork only) | Y | `flagPrefix`; boa only prefixes via named structs. |
| Explicit long / short / env name (`name`, `short`, `env` tags) | Y | P | `Mapping.Named`, `short`, `env`. No example runs an explicit env name. |
| Disable all enrichment (`ParamEnricherNone`) | Y | N | `autoLong: false, autoShort: false`; documented, no example. |
| Custom / composed enrichers | Y | Y | `Settings.enrichers`, `cli_enrichers`, `cli_fleet`. |
| `boa:"noflag"` (env/config only) | Y | Y | `long: Mapping.Disabled` (`cli_env`). |
| `boa:"noenv"` | Y | Y | `envName: Mapping.Disabled` (`cli_env`). |
| `boa:"configonly"` (no CLI, no env, still validated) | Y | N | `long: Disabled` + `envName: Disabled`. No dedicated example. |
| `boa:"ignore"` (only raw config writes it) | n/a | – | Every bork field is decoded and proven; no use for an unvalidated field. |
| Env value empty = absent | Y | N | Documented, Go-tested. |
| Precedence CLI > env > config > defaults | Y | P | `cli` and `cli_dynamic` show CLI > config. env layer never shown. |

### Values and types

| boa feature | bork | Example | Notes |
| --- | --- | --- | --- |
| Required by default; optional via pointer / `optional` | Y | Y | Plain field required; `Option[T]` or a default makes it optional. |
| Defaults (`default` tag) | Y | Y | Record field defaults, shown in help. |
| Bool flags default to false (`ParamEnricherBool`) | **N** | – | `verbose: Bool` with no default is *required*: help shows `-v, --verbose (required)` (probe). Surprising for a switch. |
| Global "optional by default" (`WithDefaultOptional`) | n/a | – | Types say it: `Option`/default. |
| Distinguish "unset" from zero (pointer fields, `HasValue`) | Y | P | `Option[T]` gives None. No source tracking (which layer set a value); see hooks below. |
| Slices: repeated flags | Y | Y | `cli`: `--tags one --tags two`. |
| Slices: CSV `--tags a,b` (boa default) | **N** | – | `--nums 1,2` fails: "invalid JSON" (probe). Only repeated flags. |
| Maps `--labels k=v,k=v` | **P** | N | JSON only: `--labels '{"a":"b"}'`. `k=v` is rejected (probe). A map field also can't have a `{}` default ("must be a closed value"), so it needs `Option[Map]`. |
| Complex types as JSON on the CLI | Y | N | Nested lists/records accept JSON. Positional `List[List[Int]]` documented. |
| Enum values (`alts` + `strict`) | Y | Y | `choices` + `strictChoices` on String fields (`cli_completion`, `cli_fleet`). |
| Enums as typed values (sealed fieldless variants) | **P** | N | `level: Level` works only as JSON: `--level '"Debug"'`. `--level Debug` fails with "invalid JSON" (probe). Variants aren't offered as choices/completion. boa has no typed enums, but this is the natural bork shape. |
| min / max / pattern validation | Y | Y | Field facts (`where validPort`) in `cli`, `subcommands`, `cli_fleet`. Regex facts exist (`regex.Matches`), not shown in a CLI example. |
| Custom validator | Y | Y | Any `pred`. |
| Cross-field validation, conditional required (`SetRequiredFn`) | Y | N | Record facts work: `hi: Int where atLeast(lo)` → `.hi: must satisfy value >= lo` (probe). No CLI example. |
| Flag constraints (mutually exclusive, required together, one required) | P | N | Expressible as record facts over `Option` fields; no helper, no example. |
| `time.Duration`, `time.Time`, `net.IP`, `*url.URL` | **N** | – | `time.Duration` has no `codec.Decode` instance (probe compile error). Same for the others as far as I can see. |
| Custom types (`RegisterType` Parse/Format) | P | N | A custom `codec.Decode` instance with `FieldSchema` metadata should work; undocumented for CLI and no example. |
| Type aliases | Y | – | Not specifically checked. |

### Structure

| boa feature | bork | Example | Notes |
| --- | --- | --- | --- |
| Positional args (single, optional, slice) | Y | Y | `positional`, `position`, `cli_positionals`, `subcommands`. |
| Nested records with auto-prefixed flags (`DB.Host` → `--db-host`, `DB_HOST`) | **N** | – | A nested record is one JSON flag: `--db string (default Db { host: "localhost", port: 5432 })` (probe). |
| Embedded structs to share options across commands | **N** | – | No flattening; each command must repeat common fields. |
| Optional parameter groups (`*DBConfig`, nil unless set) | N | – | Follows from nested records not being flattened. |
| Subcommands | Y | Y | `Subcommand`, `RunCommands`, `subcommands`. |
| Nested command trees | Y | Y | `Group`, `cli_tree`, `cli_fleet`. |
| Command aliases, long description, examples | Y | Y | `Command.copy(...)`, `cli_tree`. |
| Hidden / deprecated commands | Y | P | Deprecated covered in a test in `cli_positionals`; not in a run. |
| Help group headings (`GroupID`, `Groups`) | **N** | – | "Core Commands:" style sections in help. |
| Persistent flags inherited by subcommands (`persistent:"true"`) | **N** | – | Documented as missing: "Root and persistent flags are not exposed." |
| Root command with its own params + subcommands | N | – | Groups have no options or handler. |
| `--version` (`Version` field) | **N** | – | `--version` → "unknown flag" (probe). |

### Config files

| boa feature | bork | Example | Notes |
| --- | --- | --- | --- |
| `configfile` field selects a file | Y | Y | `configFile: true`, `cli`, `cli_dynamic`, `cli_fleet`. |
| Fixed overlay chain (`LoadConfigFiles`) | Y | Y | `configFiles`, left to right. |
| Per-field "not from config" | Y (bork only) | Y | `config: false` (`cli_fleet` enricher). |
| Config formats other than JSON (YAML, TOML via registry) | **N** | – | JSON only, though `bork/yaml` already decodes to `codec.Value`. YAML by extension is cheap to add. |
| Substruct config files | N | – | Depends on nested records. |
| Config discovery (`boaviper.FindConfig`: `./app.*`, `~/.config/app/config.*`, `/etc/app/config.*`) | N | – | |
| Load config from bytes / explicit load | n/a | – | Users can call `json.Decode`/`yaml.Decode` themselves. |
| Dump resolved config | N | – | boa writes the merged config back out. |
| Live reload (`boa.Reload[T]`) | N | – | Re-run precedence and validation; return a new value. |

### Lifecycle, errors, help, completion

| boa feature | bork | Example | Notes |
| --- | --- | --- | --- |
| Hooks Init / PostCreate / PreValidate / PreExecute | n/a | – | bork validates through the decoder and hands over a proven record; pre-execute is the handler body. Metadata changes go through enrichers. |
| `HasValue` / value source per field | N | – | Can't tell "default" from "set to the default". Option covers most uses. |
| Conditional visibility (`SetIsEnabledFn`) | N | – | |
| Hidden flags | Y | N | `hidden: true`; Go-tested only. |
| Deprecated flags with warning | Y (bork only) | N | boa has no flag deprecation. Not in an example run. |
| Run (print usage + error, exit 1) / RunE / ToCobra | **P** | Y | `Run` returns `cli.Error`; apps print it. Every example prints the raw debug value `Error { errors: [DecodeError { ... }] }`. No renderer like `Error: required flag "name" not set` + usage, no default exit code. |
| Testing with injected args (`RunArgsE`) | Y | Y | `Parse`/`Dispatch` take explicit args; many example tests. |
| Injected environment for tests | **N** | – | See §1. Blocks runnable env examples. |
| Help generated from field docs | Y | Y | Comments become help (`cli_docs`). |
| Help shows value types (`--port int`) | **N** | – | Every scalar shows `string`: `--http-port string (default 1)` for an Int (probe). |
| Help shows readable defaults | **P** | – | Records and variants print as bork debug text: `(default Level.Info)`, `(default Db { host: "localhost", ... })` (probe). |
| Shell completion scripts (bash/zsh/fish/pwsh) | Y | Y | `cli_completion`. |
| Static value completion | Y | Y | `choices`. |
| Dynamic completion (`AlternativesFunc`, `RegisterFlagCompletionFunc`) | Y | Y | `ParseWith` / `SubcommandWith`, `cli_dynamic`, `cli_fleet`. Richer than boa: completers see partial typed input. |
| Positional completion (`ValidArgsFunc`) | Y | Y | `cli_positionals`. |
| Cobra interop (mix raw cobra commands) | n/a | – | No Go-level cobra exposure in bork. |

## 3. Proposed work

### PR A: examples (no library changes)

1. `cli_names_auto`: `autoLong` + `autoShort` + `autoEnv` + `envPrefix`, field names only.
2. `cli_names_manual`: `autoLong: false`, every name explicit (`Named` long, `short`, `env`).
3. `cli_names_mixed`: one command with auto names, one renamed flag, one disabled env, one config-only field.
4. `cli_validation`: field facts, a regex fact, a cross-field record rule (conditional required, mutual exclusion over `Option` fields) and the collected error list.
5. Extend existing examples where cheap: hidden + deprecated flag in a run.

Env in examples is the blocker. Options, in order of preference:

- (a) add `env.txt` support to `TestExamples` (KEY=VALUE lines set on the child process). Test-harness only, about 10 lines. Recommended.
- (b) add an env reader to `cli.Settings` (like `env.LoadWith`). That's an API change; it's useful for app tests too, so it could come later.

### PR B onwards: features, for the lead to choose (my priority order)

| # | Gap | Size | Why |
| --- | --- | --- | --- |
| 1 | Error rendering: `cli.Error` → readable message + usage hint, used by Run/RunCommands examples | S | Every example currently prints a debug value. |
| 2 | Help: real type names (`int`, `bool`, `strings`) and readable defaults | S–M | Visible in every `--help`. |
| 3 | Bool fields without a default default to false (boa `ParamEnricherBool`) | S | A required switch is surprising. Changes semantics; needs your call. |
| 4 | Sealed fieldless variants as enums: bare names on CLI/env, auto choices + strict completion | M | The typed version of boa `alts`. |
| 5 | Nested records flattened to prefixed flags/env (`db.host` → `--db-host`, `DB_HOST`), optional groups via `Option[Record]` | L | Largest structural gap; also enables shared option records across subcommands. |
| 6 | YAML config files by extension (via `bork/yaml`) | S | Library already exists. |
| 7 | CSV lists (`--tags a,b`) and `k=v` maps | S–M | boa defaults; needs a per-field mode, like boa `collection`. |
| 8 | `time.Duration` (and Instant) codec instances usable as flags | S–M | Common in backend CLIs. Touches `bork/time`. |
| 9 | Persistent/root flags and a root command with options | M–L | Already a documented gap. |
| 10 | `--version` | S | |
| 11 | Help group headings | S | |
| 12 | Config discovery, dump, live reload, value-source tracking | M each | Lower priority; file separate tickets. |

Not proposed: lifecycle hooks, `boa:"ignore"`, global optional-by-default, cobra
interop. bork's proven-record design replaces them on purpose.
