# Building a CLI application

The [fleet example](../../examples/cli_fleet/main.bork) combines the CLI APIs in
one application: nested commands and aliases, field documentation, reusable
mapping policy, JSON settings, proven options, ordered positionals, and dynamic
resource completion. Its option and catalog records derive `codec.Decode`;
`use codec.Defaults` selects the primitive and container instances. Run these commands from the repository root:

```sh
bork run examples/cli_fleet -- k r d --config examples/cli_fleet/settings.json -n team -r 2 apply web worker
bork run examples/cli_fleet -- help cluster resource deploy
bork run examples/cli_fleet -- cluster status
```

`k r d` aliases `cluster resource deploy`. The deploy record has a positive
replica count and defaults for namespace, replicas, and resources. The action
and resource names occupy positional slots 0 and 1; the final List receives
each remaining word as one resource name. The handler receives a fully decoded
record, then its scope closes after it returns. A failed decode does not invoke
the handler.

## Reuse a mapping policy

The example's settings enable automatic short flags and environment bindings
with a `FLEET` prefix. Flags such as `--namespace` and `-n` bind
`FLEET_NAMESPACE`; replicas bind `--replicas`, `-r`, and `FLEET_REPLICAS`.
Explicit metadata disables the catalog's automatic short name to avoid its
collision with config. Indexed positionals keep their positional names and
receive no automatic flag or environment binding.

A pure enricher excludes config-file selectors from config document fields and
adds a positive-value hint to replica help. Leading field comments supply the
other descriptions. Settings belong to each typed command; the empty status
command has its own default settings. Root settings control tree completion.
See [mapping and enrichers](cli.md#names-environment-and-source-policies) for policy ordering.

## Layer settings without requiring a file

A config selector is optional. Without `--config` or `FLEET_CONFIG`, normal
invocations use supplied CLI/environment values and record defaults:

```sh
FLEET_NAMESPACE=team FLEET_REPLICAS=3 bork run examples/cli_fleet -- k r d apply web
bork run examples/cli_fleet -- k r d --config examples/cli_fleet/settings.json apply web
```

CLI values take precedence over environment values, which take precedence over
config values. Later explicit config files override earlier files, and a selected file overlays
them; record defaults
fill missing fields after source merging. The checked-in settings file provides
namespace `team` and two replicas. A relative config or catalog path resolves
from the process working directory, so commands above use repository-relative
paths. Config documents use JSON; catalog JSON is a separate application format.

The action offers `apply` and `inspect` as strict choices. Completion metadata
alone only suggests values; strict choices also reject other values during
normal parsing. The positive replica predicate applies to normal decoding
regardless of whether the number came from CLI, environment, or config.

## Complete resources using partial options

The resources completer reads a typed JSON catalog through a file owned by its
fresh completion scope. It filters entries by the partial namespace and returns
resource names with descriptions, preserving catalog order. The CLI filters
those candidates by the current prefix and writes only the shell protocol to
stdout. Catalog access and decode errors return directive `:1` with diagnostics
on stderr.

```sh
bork run examples/cli_fleet -- __complete k r d --config examples/cli_fleet/settings.json --catalog examples/cli_fleet/catalog.json apply w
```

This query suggests `web` and `worker`. A later resource word uses the same
completer. `Partial.Get[String]("namespace")` merges supplied CLI/environment/
config sources and checks the selected field's decoder. Omitted defaults are
`Missing`, so the completer explicitly chooses `dev` and `catalog.json` as
fallbacks. An invalid unrelated replica input does not prevent resource
suggestions. The full options decoder runs only for a normal invocation.

Help, script generation, and static action completion do not open the catalog
or config files. Dynamic resource completion does read selected config files
and the catalog. Keep callback output in returned Suggestions/Error values;
printing to stdout would corrupt the shell protocol.

Build a binary and install its generated completion script in your shell:

```sh
bork build examples/cli_fleet -o fleet
./fleet completion bash > fleet.bash
source fleet.bash
# Alternatives: ./fleet completion zsh, fish, or powershell
```

The generated scripts resolve the nested tree and call its hidden completion
endpoint. There is no separate completion application or second options schema.

## Choose an entry point and test it

Use `Run`/`Subcommand` for typed handlers with static completion, and
`RunWith`/`SubcommandWith` when callbacks need partial options or effects.
`Parse`/`ParseWith` return options, Help, or Error without invoking a handler.
Their `ParseDetailed`/`ParseDetailedWith` variants also return successful warning
lists. `RunCommands` executes a native tree; `Dispatch` accepts an explicit
argument list for tests and manual execution.

The fleet example embeds tests for nested help and static completion. Its driver
integration test runs the actual binary against temporary catalog and config
files, checks precedence during normal invocation and completion, and verifies
that help and failed decoding skip the handler. Run its embedded tests with:

```sh
bork test examples/cli_fleet
```

For a focused introduction, choose an example below. Each is runnable and has
embedded tests.

| Topic | Example |
| --- | --- |
| Typed decoding and config selection | [cli](../../examples/cli/main.bork) |
| Field docs and description overrides | [cli_docs](../../examples/cli_docs/main.bork) |
| Automatic, explicit, and disabled mappings | [cli_mapping](../../examples/cli_mapping/main.bork) |
| Environment naming and precedence | [cli_env](../../examples/cli_env/main.bork) |
| Composed pure enrichers | [cli_enrichers](../../examples/cli_enrichers/main.bork) |
| Static choices and shell scripts | [cli_completion](../../examples/cli_completion/main.bork) |
| Nested groups, aliases, and handler scopes | [cli_tree](../../examples/cli_tree/main.bork) |
| Partial options and dynamic suggestions | [cli_dynamic](../../examples/cli_dynamic/main.bork) |
| Indexed positionals and command deprecation | [cli_positionals](../../examples/cli_positionals/main.bork) |

The [API guide](cli.md) describes diagnostics, completion directives, and the
current limits: JSON config, flat option records, no persistent root flags, and
no user lifecycle hooks. Native Cobra completion syntax/routing errors retain
Cobra's protocol behavior; application callback errors use `:1`.
