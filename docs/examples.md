# Examples

The [examples directory](../examples) holds complete programs. Run one with `bork run examples/<name>`, and its tests, if it has any, with `bork test examples/<name>`.

Some examples take arguments or read files that sit next to them. Those have an `args.txt`, and are run from their own directory with the arguments it lists:

```sh
cd examples/wc
bork run . -- sample.txt missing.txt poem.txt
```

An example that reads environment variables also has an `env.txt` with one `KEY=VALUE` per line. The example tests set them; to run such an example by hand, pass them yourself:

```sh
cd examples/cli_names_auto
env $(cat env.txt) bork run . -- $(cat args.txt)
```

Each example's expected output is kept in [testdata/examples](../testdata/examples) and checked on every change, so the examples always work with the current compiler.

## Start here

| Example | What it shows |
| --- | --- |
| [hello](../examples/hello/main.bork) | Functions, `if` as an expression, blocks, and printing |
| [users](../examples/users/main.bork) | Records, nested `copy`, union results, `?`, and `match` |
| [payments](../examples/payments/main.bork) | Facts: validating input once and carrying the proof in types |
| [wc](../examples/wc/main.bork) | A small `wc`: files in scopes, one task per file |
| [calculator](../examples/calculator/main.bork) | A parser and evaluator where every failure is a value |
| [accounts](../examples/accounts/main.bork) | A state machine built from a sealed type |
| [orders](../examples/orders/main.bork) | Validation, discounts, and pricing in cents |

## Language features

| Example | What it shows |
| --- | --- |
| [derive_labels](../examples/derive_labels/README.md) | A custom class derived from field metadata, including a generic record |
| [comptime](../examples/comptime/README.md) | A lookup table and a validated configuration file, both computed while compiling |
| [sql_interpolation](../examples/sql_interpolation/README.md) | Typed SQL literals: bound values, quoted names, and composed fragments |
| [config](../examples/config/README.md) | Field defaults, named arguments, and a record only its own package can construct |
| [mocking](../examples/mocking/main.bork) | Tests that replace the network and the clock with mocks |
| [assemble](../examples/assemble/main.bork) | Wiring a database and an HTTP service together from provider functions |
| [parallel_lists](../examples/parallel_lists/main.bork) | Bounded parallel `map` over a list, with cancellation |
| [task_fanin](../examples/task_fanin/main.bork) | Awaiting many tasks, racing them, and timeouts |
| [channels](../examples/channels/main.bork) | Sending, receiving, closing, buffers, and producers |
| [select_timeout](../examples/select_timeout/main.bork) | `select` with a timeout, a ticker pacing work, polling, and a scope deadline |
| [pipeline](../examples/pipeline/main.bork) | Stages joined by channels, stopped when the scope ends |
| [fan_in_out](../examples/fan_in_out/main.bork) | Workers sharing one channel of jobs, and `merge` collecting their results |
| [unbounded_queue](../examples/unbounded_queue/main.bork) | A queue that never blocks its producer, against a bounded one |
| [task_pool](../examples/task_pool/main.bork) | A bounded task pool that refuses work when it is full |
| [handoff](../examples/handoff/main.bork) | Handing a connection over to another scope with `move` |

## Services

| Example | What it shows |
| --- | --- |
| [http_server](../examples/http_server/main.bork) | A REST API for a to-do list, with JSON, shared state, and logging |
| [http_routes](../examples/http_routes/main.bork) | Routes with path parameters |
| [signup](../examples/signup/main.bork) | Validating sign-up requests read as JSON Lines |
| [signup_api](../examples/signup_api/main.bork) | An HTTP endpoint whose requests decode straight into validated values |
| [service_context](../examples/service_context/main.bork) | Two services passing a trace id and a shrinking deadline between them |
| [slow_downstream](../examples/slow_downstream/main.bork) | Bounded admission and a shared retry budget against a slow service |
| [sql](../examples/sql/main.bork) | SQLite connections and transactions owned by scopes |

## Standard packages

| Example | Package |
| --- | --- |
| [bytes_encoding](../examples/bytes_encoding/main.bork) | Hex and base64 with [bork/encoding](std/encoding.md) |
| [cli](../examples/cli/main.bork) | Command-line options decoded into a record with [bork/cli](std/cli.md) |
| [cli_names_auto](../examples/cli_names_auto/main.bork) | Flag, short and environment names derived from field names |
| [cli_names_manual](../examples/cli_names_manual/main.bork) | Every flag, short and environment name written by hand |
| [cli_names_mixed](../examples/cli_names_mixed/main.bork) | Derived and explicit names, flag-only and config-only fields in one command |
| [cli_validation](../examples/cli_validation/main.bork) | Field, cross-field and whole-record rules on command-line options |
| [subcommands](../examples/subcommands/main.bork) | Typed subcommands with [bork/cli](std/cli.md) |
| [compress_archive](../examples/compress_archive/main.bork) | Gzip, ZIP, and TAR with [bork/compress](std/compress.md) and [bork/archive](std/archive.md) |
| [crypto](../examples/crypto/main.bork) | Hashes and HMAC with [bork/crypto](std/crypto.md) |
| [csv](../examples/csv/main.bork) | CSV rows decoded into records with [bork/encoding](std/encoding.md) |
| [embed](../examples/embed/main.bork) | Files built into the executable with [bork/embed](std/embed.md) |
| [embed_templates](../examples/embed_templates/main.bork) | A whole embedded directory, with files found by naming convention at runtime |
| [fs](../examples/fs/main.bork) | Files and directories with [bork/fs](std/fs.md) |
| [json_lines](../examples/json_lines/main.bork) | Streaming JSON Lines with [bork/json](std/json.md) |
| [math](../examples/math/main.bork) | Exact decimal money with [bork/math](std/math.md) |
| [net](../examples/net/main.bork) | A TCP echo server with [bork/net](std/net.md) |
| [process](../examples/process/main.bork) | Running a subprocess with [bork/process](std/process.md) |
| [process_capture](../examples/process_capture/main.bork) | Capturing stdout and stderr separately |
| [process_combined](../examples/process_combined/main.bork) | Capturing stdout and stderr together, in order (2>&1) |
| [process_forward](../examples/process_forward/main.bork) | Forwarding a child's stdin, stdout and stderr |
| [process_mixed](../examples/process_mixed/main.bork) | Forwarding, capturing, discarding or writing each stream to a file |
| [process_exit_codes](../examples/process_exit_codes/main.bork) | Exit codes, signal deaths and checked runs |
| [process_stdin](../examples/process_stdin/main.bork) | Feeding stdin from text, bytes, a file or a pipe |
| [process_streaming](../examples/process_streaming/main.bork) | Reading output line by line while it runs, and two pipes at once |
| [process_concurrent](../examples/process_concurrent/main.bork) | Several processes at once, monitored and collected |
| [process_timeout](../examples/process_timeout/main.bork) | Timeouts, graceful cancellation and Stop |
| [process_pipeline](../examples/process_pipeline/main.bork) | A pipeline of processes connected by OS pipes |
| [rand](../examples/rand/main.bork) | Seeded random generators with [bork/rand](std/rand.md) |
| [regex](../examples/regex/main.bork) | Regular expressions with [bork/regex](std/regex.md) |
| [cli_time](../examples/cli_time/main.bork) | Duration and RFC3339 flags with opt-in [bork/time](std/time.md) codecs | `bork run examples/cli_time -- --timeout 1m30s --since 1970-01-01T00:00:00Z --retries 500ms` |
| [time_env](../examples/time_env/main.bork) | Configuration from the environment and an injectable clock with [bork/env](std/env.md) and [bork/time](std/time.md) |
| [url](../examples/url/main.bork) | Parsing and building URLs with [bork/url](std/url.md) |
| [uuid](../examples/uuid/main.bork) | UUIDs with [bork/uuid](std/uuid.md) |
| [yaml_config](../examples/yaml_config/main.bork) | A YAML configuration file decoded into records with [bork/yaml](std/yaml.md) |
