# Examples

The [examples directory](../examples) holds complete programs. Use the Run column for a first run, and `bork test examples/<name>` for an example's tests, if it has any.

The Run column gives a first command from the repository root. Commands in parentheses change to the example's own directory so fixture files resolve correctly. `args.txt` records the arguments used by the checked transcript. An `env.txt`, where present, supplies the environment variables shown in that example's command:

```sh
cd examples/wc
bork run . -- sample.txt missing.txt poem.txt
```

HTTP demos bind available localhost ports and stop automatically. Their `serve` modes keep running until Ctrl+C; the Services section gives curl commands. Process examples require the external commands named in their source (several use Unix shell tools); SQL uses local SQLite, with no external database server. `script_uuid` resolves its UUID Go-module dependency on the first run.

Each example's expected output is kept in [testdata/examples](../testdata/examples) and checked on every change, so the examples always work with the current compiler.

## Start here

| Example | What it shows | Run from repository root |
| --- | --- | --- |
| [hello](../examples/hello/main.bork) | Functions, `if` as an expression, blocks, and printing | `bork run examples/hello` |
| [users](../examples/users/main.bork) | Records, nested `copy`, union results, `?`, and `match` | `bork run examples/users` |
| [payments](../examples/payments/main.bork) | Facts: validating input once and carrying the proof in types | `bork run examples/payments` |
| [wc](../examples/wc/main.bork) | A small `wc`: files in scopes, one task per file | `(cd examples/wc && bork run . -- sample.txt missing.txt poem.txt)` |
| [calculator](../examples/calculator/main.bork) | A parser and evaluator where every failure is a value | `bork run examples/calculator` |
| [accounts](../examples/accounts/main.bork) | A state machine built from a sealed type | `bork run examples/accounts` |
| [orders](../examples/orders/main.bork) | Validation, discounts, and pricing in cents | `bork run examples/orders` |

## Language features

| Example | What it shows | Run from repository root |
| --- | --- | --- |
| [enums](../examples/enums/main.bork) | Sealed enum values, wire names, lookup, and JSON integration | `bork run examples/enums` |
| [derive_labels](../examples/derive_labels/README.md) | A custom class derived from field metadata, including a generic record | `bork run examples/derive_labels` |
| [comptime](../examples/comptime/README.md) | A lookup table and a validated configuration file, both computed while compiling | `bork run examples/comptime` |
| [sql_interpolation](../examples/sql_interpolation/README.md) | Typed SQL literals: bound values, quoted names, and composed fragments | `bork run examples/sql_interpolation` |
| [record_conversion](../examples/record_conversion/main.bork) | Record conversion with `into`: dropped fields, defaults, overrides, failures, and private targets with invariants | `bork run examples/record_conversion` |
| [generators](../examples/generators/main.bork) | Lazy generators, early-stop file cleanup, comprehensions, and loop destructuring | `bork run examples/generators` |
| [config](../examples/config/README.md) | Field defaults, named arguments, and a record only its own package can construct | `bork run examples/config` |
| [mocking](../examples/mocking/main.bork) | Tests that replace the network and the clock with mocks | `bork run examples/mocking` |
| [assemble](../examples/assemble/main.bork) | Wiring a database and an HTTP service together from provider functions | `bork run examples/assemble` |
| [parallel_lists](../examples/parallel_lists/main.bork) | Bounded parallel `map` over a list, with cancellation | `bork run examples/parallel_lists` |
| [task_fanin](../examples/task_fanin/main.bork) | Awaiting many tasks, racing them, and timeouts | `bork run examples/task_fanin` |
| [channels](../examples/channels/main.bork) | Sending, receiving, closing, buffers, and producers | `bork run examples/channels` |
| [select_timeout](../examples/select_timeout/main.bork) | `select` with a timeout, a ticker pacing work, polling, and a scope deadline | `bork run examples/select_timeout` |
| [pipeline](../examples/pipeline/main.bork) | Stages joined by channels, stopped when the scope ends | `bork run examples/pipeline` |
| [fan_in_out](../examples/fan_in_out/main.bork) | Workers sharing one channel of jobs, and `merge` collecting their results | `bork run examples/fan_in_out` |
| [unbounded_queue](../examples/unbounded_queue/main.bork) | A queue that never blocks its producer, against a bounded one | `bork run examples/unbounded_queue` |
| [task_pool](../examples/task_pool/main.bork) | A bounded task pool that refuses work when it is full | `bork run examples/task_pool` |
| [handoff](../examples/handoff/main.bork) | Handing a connection over to another scope with `move` | `bork run examples/handoff` |
| [package_values](../examples/package_values/main.bork) | Package-level constants and inferred value types | `bork run examples/package_values` |
| [tuples](../examples/tuples/main.bork) | Tuple results, destructuring and positional access | `bork run examples/tuples` |
| [scripts](../examples/scripts/main.bork) | A script with a shebang and arguments | `(cd examples/scripts && bork run . -- Ada)` |
| [script_cli](../examples/script_cli/main.bork) | Typed CLI flags in a top-level script | `bork script examples/script_cli/main.bork --name Ada` |
| [script_cli_main](../examples/script_cli_main/main.bork) | A script with `fn main()`, a helper, and typed CLI flags | `bork script examples/script_cli_main/main.bork --name Ada` |
| [script_uuid](../examples/script_uuid/main.bork) | A script with a Go-module dependency | `bork run examples/script_uuid` |

## Services

| Example | What it shows | Run from repository root |
| --- | --- | --- |
| [http_server](../examples/http_server/main.bork) | A REST API for a to-do list, with JSON, shared state, and logging | `(cd examples/http_server && bork run . -- demo)` |
| [http_routes](../examples/http_routes/main.bork) | Routes with path parameters | `(cd examples/http_routes && bork run . -- demo)` |
| [signup](../examples/signup/main.bork) | Validating sign-up requests read as JSON Lines | `(cd examples/signup && bork run . -- requests.jsonl)` |
| [signup_api](../examples/signup_api/main.bork) | An HTTP endpoint whose requests decode straight into validated values | `(cd examples/signup_api && bork run . -- demo)` |
| [service_context](../examples/service_context/main.bork) | Two services passing a trace id and a shrinking deadline between them | `bork run examples/service_context` |
| [slow_downstream](../examples/slow_downstream/main.bork) | Bounded admission and a shared retry budget against a slow service | `bork run examples/slow_downstream` |
| [sql](../examples/sql/main.bork) | SQLite connections and transactions owned by scopes | `bork run examples/sql` |
| [http_multi](../examples/http_multi/main.bork) | API, metrics and debug listeners with one application lifetime | `bork run examples/http_multi` |
| [service_tour](../examples/service_tour/main.bork) | The [service tour](tour-service.md): environment configuration, validated JSON and SQLite | `bork run examples/service_tour -- demo` |

### Keep a service running

These commands keep listening; run curl in another terminal, then stop the service with Ctrl+C:

| Service | Start | Try a request |
| --- | --- | --- |
| In-memory todo API | `bork run examples/http_server -- serve 127.0.0.1:8080` | `curl -H 'Content-Type: application/json' -d '{"title":"buy milk"}' http://127.0.0.1:8080/todos` |
| Validated signup API | `bork run examples/signup_api -- serve 127.0.0.1:8080` | `curl -H 'Content-Type: application/json' -d '{"name":"ann","plan":"Free"}' http://127.0.0.1:8080/signup` |
| API, metrics and debug | `bork run examples/http_multi -- serve` | `curl http://127.0.0.1:8080/hello` (metrics: port 9090; debug: port 6060) |
| SQLite notes API | `bork run examples/service_tour -- serve` | `curl http://127.0.0.1:8080/notes` (creates `notes.db` in the current directory) |

Choose one service at a time if they share port 8080. See [HTTP](std/http.md) for listeners, routing and shutdown. The [service tour](tour-service.md) covers configuration with `TOUR_ADDRESS` and `TOUR_DATABASE`, POST requests and tests.

## Standard packages

| Example | Package | Run from repository root |
| --- | --- | --- |
| [bytes_encoding](../examples/bytes_encoding/main.bork) | Hex and base64 with [bork/encoding](std/encoding.md) | `bork run examples/bytes_encoding` |
| [cli](../examples/cli/main.bork) | Command-line options decoded into a record with [bork/cli](std/cli.md) | `(cd examples/cli && bork run . -- --name Ada -p 443 --tags one --tags two)` |
| [cli_visibility](../examples/cli_visibility/main.bork) | Hidden flags and warnings for deprecated flags with [bork/cli](std/cli.md) | `bork run examples/cli_visibility -- --trace --old-port 9000` |
| [subcommands](../examples/subcommands/main.bork) | Typed subcommands with [bork/cli](std/cli.md) | `(cd examples/subcommands && bork run . -- serve --host localhost -p 443)` |
| [compress_archive](../examples/compress_archive/main.bork) | Gzip, ZIP, and TAR with [bork/compress](std/compress.md) and [bork/archive](std/archive.md) | `bork run examples/compress_archive` |
| [crypto](../examples/crypto/main.bork) | Hashes and HMAC with [bork/crypto](std/crypto.md) | `bork run examples/crypto` |
| [csv](../examples/csv/main.bork) | CSV rows decoded into records with [bork/encoding](std/encoding.md) | `bork run examples/csv` |
| [embed](../examples/embed/main.bork) | Files built into the executable with [bork/embed](std/embed.md) | `bork run examples/embed` |
| [embed_templates](../examples/embed_templates/main.bork) | A whole embedded directory, with files found by naming convention at runtime | `bork run examples/embed_templates` |
| [fs](../examples/fs/main.bork) | Files and directories with [bork/fs](std/fs.md) | `bork run examples/fs` |
| [json_lines](../examples/json_lines/main.bork) | Streaming JSON Lines with [bork/json](std/json.md) | `bork run examples/json_lines` |
| [math](../examples/math/main.bork) | Exact decimal money with [bork/math](std/math.md) | `bork run examples/math` |
| [net](../examples/net/main.bork) | A TCP echo server with [bork/net](std/net.md) | `bork run examples/net` |
| [process](../examples/process/main.bork) | Running a subprocess with [bork/process](std/process.md) | `bork run examples/process`  requires `echo` |
| [process_capture](../examples/process_capture/main.bork) | Capturing stdout and stderr separately | `bork run examples/process_capture`  requires Unix `/bin/sh` |
| [process_combined](../examples/process_combined/main.bork) | Capturing stdout and stderr together, in order (2>&1) | `bork run examples/process_combined`  requires Unix `/bin/sh` |
| [process_forward](../examples/process_forward/main.bork) | Forwarding a child's stdin, stdout and stderr | `bork run examples/process_forward < /dev/null`  requires Unix `/bin/sh`; omit the redirect to enter a line interactively |
| [process_mixed](../examples/process_mixed/main.bork) | Forwarding, capturing, discarding or writing each stream to a file | `bork run examples/process_mixed`  requires Unix `/bin/sh`; creates its temporary file fixture |
| [process_exit_codes](../examples/process_exit_codes/main.bork) | Exit codes, signal deaths and checked runs | `bork run examples/process_exit_codes`  requires Unix `/bin/sh` and `true` |
| [process_stdin](../examples/process_stdin/main.bork) | Feeding stdin from text, bytes, a file or a pipe | `bork run examples/process_stdin`  requires Unix `/bin/sh`, `sort`, `wc`, `tr`; creates its input file |
| [process_streaming](../examples/process_streaming/main.bork) | Reading output line by line while it runs, and two pipes at once | `bork run examples/process_streaming` requires Unix `/bin/sh` and `sleep` |
| [process_concurrent](../examples/process_concurrent/main.bork) | Several processes at once, monitored and collected | `bork run examples/process_concurrent`  requires Unix `/bin/sh`, `sleep`, `cat` |
| [process_timeout](../examples/process_timeout/main.bork) | Timeouts, graceful cancellation and Stop | `bork run examples/process_timeout`  requires Unix `/bin/sh` and `sleep` |
| [process_pipeline](../examples/process_pipeline/main.bork) | A pipeline of processes connected by OS pipes | `bork run examples/process_pipeline`  requires `printf`, `sort`, `tr` |
| [rand](../examples/rand/main.bork) | Seeded random generators with [bork/rand](std/rand.md) | `bork run examples/rand` |
| [regex](../examples/regex/main.bork) | Regular expressions with [bork/regex](std/regex.md) | `bork run examples/regex` |
| [cli_enums](../examples/cli_enums/main.bork) | Bare enum names, exact aliases and strict CLI choices | `bork run examples/cli_enums -- --level verbose --optional INFO --levels DEBUG` |
| [cli_time](../examples/cli_time/main.bork) | Duration and RFC3339 flags with opt-in [bork/time](std/time.md) codecs | `bork run examples/cli_time -- --timeout 1m30s --since 1970-01-01T00:00:00Z --retries 500ms` |
| [cli_version](../examples/cli_version/main.bork) | Command-local version output before reading sources or running handlers | `bork run examples/cli_version -- --version` |
| [cli_help_groups](../examples/cli_help_groups/main.bork) | Native command help headings | `bork run examples/cli_help_groups -- --help` |
| [cli_nested](../examples/cli_nested/main.bork) | Nested records with prefixed flags and optional groups | `bork run examples/cli_nested -- --db-port 8080 --optional-host staging` |
| [cli_collections](../examples/cli_collections/main.bork) | Typed CSV list and key=value map modes | `bork run examples/cli_collections -- --tags web,worker --labels env=dev,team=ops --limits http=80,https=443` |
| [time_env](../examples/time_env/main.bork) | Configuration from the environment and an injectable clock with [bork/env](std/env.md) and [bork/time](std/time.md) | `bork run examples/time_env` |
| [url](../examples/url/main.bork) | Parsing and building URLs with [bork/url](std/url.md) | `bork run examples/url` |
| [uuid](../examples/uuid/main.bork) | UUIDs with [bork/uuid](std/uuid.md) | `bork run examples/uuid` |
| [yaml_config](../examples/yaml_config/main.bork) | A YAML configuration file decoded into records with [bork/yaml](std/yaml.md) | `(cd examples/yaml_config && bork run . -- config.yaml)` |

## Command-line applications

These examples build on [bork/cli](std/cli.md). Configuration and catalog paths are relative to the example directory. File names in `cli_positionals` are demonstration arguments; that example does not open them. For larger applications, follow the [CLI cookbook](std/cli-cookbook.md).

| Example | What it shows | Run from repository root |
| --- | --- | --- |
| [cli_bool](../examples/cli_bool/main.bork) | Omitted Bool switches, explicit defaults, and optional Bool values | `bork run examples/cli_bool` |
| [cli_completion](../examples/cli_completion/main.bork) | Shell completion and positional arguments | `(cd examples/cli_completion && bork run . -- --environment prod web worker)` |
| [cli_docs](../examples/cli_docs/main.bork) | Field comments and flag help | `(cd examples/cli_docs && bork run . -- --host docs.example --keep-alive)` |
| [cli_dynamic](../examples/cli_dynamic/main.bork) | Dynamic defaults and choices with local configuration | `(cd examples/cli_dynamic && bork run . -- --config settings.json --namespace team --resource team-web)` |
| [cli_enrichers](../examples/cli_enrichers/main.bork) | Composed flag metadata policies | `(cd examples/cli_enrichers && bork run . -- --team-host team.example --team-port 9000)` |
| [cli_env](../examples/cli_env/main.bork) | Environment bindings and command-line overrides | `(cd examples/cli_env && bork run . -- --server-host env.example)` |
| [cli_help](../examples/cli_help/main.bork) | Selected input kinds, readable defaults and env-only help | `bork run examples/cli_help` |
| [cli_yaml](../examples/cli_yaml/main.bork) | YAML and JSON configuration overlays, selected files, and flag precedence | `(cd examples/cli_yaml && bork run . -- --config selected.yml --port 9090)` |
| [cli_fleet](../examples/cli_fleet/main.bork) | A complete nested CLI with config files and dynamic choices | `(cd examples/cli_fleet && bork run . -- k r d --config settings.json --catalog catalog.json --namespace team -r 2 apply web worker)` |
| [cli_mapping](../examples/cli_mapping/main.bork) | Explicit long and short flag mappings | `(cd examples/cli_mapping && bork run . -- -s api.example -p 443)` |
| [cli_positionals](../examples/cli_positionals/main.bork) | Ordered positional arguments and a trailing list | `(cd examples/cli_positionals && bork run . -- copy 2 first.txt second.txt)` |
| [cli_root](../examples/cli_root/main.bork) | Typed root options inherited by nested subcommands | `bork run examples/cli_root -- --region east services deploy --service api --replicas 3 --verbose` |
| [cli_tree](../examples/cli_tree/main.bork) | Nested typed subcommands | `(cd examples/cli_tree && bork run . -- k d -n prod web worker)` |
| [cli_names_auto](../examples/cli_names_auto/main.bork) | Derived flag, short and environment names | `NAMES_HTTP_PORT=9090 bork run examples/cli_names_auto -- -s api.example -v` |
| [cli_names_manual](../examples/cli_names_manual/main.bork) | Explicit flag, short and environment names | `MANUAL_PORT=5433 MANUAL_TOKEN=secret bork run examples/cli_names_manual -- --addr db.internal` |
| [cli_names_mixed](../examples/cli_names_mixed/main.bork) | Flag/env/config precedence with derived and explicit names | `(cd examples/cli_names_mixed && MIXED_SERVER_HOST=env.example MIXED_LISTEN=7000 MIXED_DEBUG=true bork run . -- --config settings.json --server-host flag.example)` |
| [cli_validation](../examples/cli_validation/main.bork) | Field, cross-field and whole-record validation | `bork run examples/cli_validation -- --name Billing_API --port 0` (intentionally exits 2); use `--name billing-api --port 8080` for success |
