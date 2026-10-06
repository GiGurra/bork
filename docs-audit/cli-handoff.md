# bork/cli vs boa: handoff

From worker `boacli` (Claude) to the next implementer. Ticket: bork-xls6ya
(epic, in progress). Gap report: [cli-vs-boa.md](cli-vs-boa.md). Read it
first; item numbers below refer to its §3 table.

## Status

- **PR A, #407** (branch `cli-name-examples`): the gap report, `env.txt` support
  in `TestExamples`, and the examples `cli_names_auto`, `cli_names_manual`,
  `cli_names_mixed` and `cli_validation`. It's pushed and was cold-reviewed (all
  findings fixed). CI was running at handoff; check it, then the lead merges.
- **bork-9ay1ag**: filed for deferred item 12 (config discovery, dump, live
  reload, value sources). Not part of this plan.
- No library changes have been made yet.

## Approved order (lead, message #14009): one focused PR each, under ~2k lines

1. **Error rendering.** A `cli.Error` → readable text (`Error: ...` lines plus a
   usage hint). Run/RunCommands examples use it instead of `println(error)`.
   Whole-record errors have an empty `path`; render them sensibly
   (`cli_validation` prints `(options)` by hand today). Also show hidden and
   deprecated flags in an example run here; that was left out of PR A.
2. **Help: real types and readable defaults.** Today every scalar shows
   `string` (`--http-port string`), and record/variant defaults print debug text
   (`(default Level.Info)`, `(default Db { host: ... })`). Help also omits
   env-only fields (`long: Disabled` + an env name), so the variable can't be
   discovered. Consider listing them in a separate section.
3. **Item 4: sealed fieldless variants as plain choices.** Bare names on the
   CLI, env and positionals (`--level Debug`, not `'"Debug"'`), variants as
   completion choices, strict by construction. **Coordinate with worker `enums`
   (bork-rz5wv6)**. I sent them questions in message #14039: a variant list on
   codec.FieldSchema, case sensitivity, and the forward-compatible fallback
   variant. My lean: reject unknown names on the CLI even when there is a
   fallback.
4. **Item 6: YAML config files** by extension, via `bork/yaml`. This applies to
   `configFiles` and the `--config` selector.
5. **Item 3: a Bool without a default is a switch defaulting to false.** It's
   approved because a required bool flag is a trap. Document it in
   docs/std/cli.md and check `bork/env` and other codec users for consistency.
6. **Item 7: CSV lists (`--tags a,b`) and `k=v` maps.** These need a per-field
   mode, like boa's `collection: slice|array`. Repeated flags must keep working.
   A map field can't have a `{}` default today ("must be a closed value"), so
   examples need `Option[Map[...]]`.
7. **Item 8: `time.Duration` (and Instant) as flags.** Add a `codec.Decode`
   instance (with `FieldSchema` kind `string`). This touches `bork/time`.
8. **Item 9: persistent/root flags** and a root command with its own options.
9. **Item 10: `--version`.**
10. **Item 11: help group headings** for commands.
11. **Item 5 (last, largest): flatten nested records** into prefixed flags and
    env names (`db: Db` → `--db-host`, `DB_HOST`), `Option[Record]` as an
    optional group, and shared option records across subcommands. **Send the
    lead a short DoD before starting.**

## Naming: don't build your own mechanism

The lead said CLI flag and env names must share the codec naming policy
designed by worker `codecnames` (bork-bbtlwz, design PR #408,
docs/design/codec-naming.md, "Sharing with bork/cli and bork/env"). Agreed so
far:

- Precedence: enricher rename > `cli.Mapping.Named` > codec override > field
  name.
- An override renames the words; flags stay kebab-case and env names
  UPPER_SNAKE.
- Aliases become hidden long flags and env names, with no shorts and no
  warning, and they join the duplicate check.
- Config keys = wire name + aliases (JSON and YAML). Having both in one file is
  an error.
- Positional overrides only rename the name shown in help. For nested
  flattening, an override renames its own segment of the path.
- `env.Load` follows the same rules. There's a proposed shared `codec.words`
  splitter.
- Settled in #14047: aliases follow Auto derivation and prefixes, even when the
  canonical name is `Named`. `Disabled` turns that source's aliases off.
  `FieldSpec.aliases` (long and env) is visible to enrichers.
- Giving both the canonical name and an alias in one source (flags or env) is
  an error; an empty env value is absent.
- A codec policy changes the accepted config keys; say so in
  docs/std/cli.md when implemented.
- `codec.words` keeps today's splitting (`apiURL` → `api-url`).

## Gotchas

- `cli.Parse` reads the real process environment; there's no injection. Only
  `TestExamples` sets env (`env.txt`); `bork test` blocks can't, so test env
  behaviour in examples through the run's golden output. Go driver tests use
  `t.Setenv` (`internal/driver/cli_test.go`, `cli_mapping_test.go`).
- `args.txt` and `env.txt` split on whitespace and lines respectively, so there
  are no spaces inside an argument. Lines in `env.txt` without `=` fail the
  test.
- `go test ./internal/driver -update` rewrites only existing expected files.
  Create `testdata/examples/<name>.txt` (empty) for a new example first.
- Field comments become help text. Don't put flag or env names in them, or help
  repeats itself. Put name notes in a comment above `flags()`.
- Rules relating fields (`where atLeast(lo)`, record-level `where`) run only
  after every field is valid on its own, so they don't appear in a run that
  also has field errors. A combined record rule reports one message
  (`must be consistent`).
- `-h` is never an automatic short (reserved), so `httpPort` gets no short.
- `bork fmt --check <dir>` prints the file name when it's not fmt-clean. Every
  .bork file must be fmt-clean.
- `if` in an interpolation needs block braces: `${if (c) { "a" } else { b }}`.
- The full local `go test ./...` once failed `TestTaskPoolRace` (a timing flake
  under load; it passes alone). It's unrelated to the CLI.
- boa itself is read-only at /home/johkjo/git/boa. The docs most useful for
  comparison are `docs/struct-tags.md`, `docs/examples-advanced.md`,
  `docs/examples-config.md` and `docs/enrichers.md`.
