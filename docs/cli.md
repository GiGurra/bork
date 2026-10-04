# The bork command

One binary does everything: `bork <command>`. Most commands take a path, which is a `.bork` file or a directory and defaults to the current directory. A directory is one package.

| Command | What it does |
| --- | --- |
| [`bork new`](#new) | Create a project from a template |
| [`bork script`](#scripts) | Run one file with an implicit main |
| [`bork run`](#run-build-and-install) | Compile and run a program |
| [`bork build`](#run-build-and-install) | Compile a program to an executable |
| [`bork install`](#run-build-and-install) | Compile a program and install the executable |
| [`bork check`](#check) | Type-check without building |
| [`bork test`](#test) | Run a package's tests |
| [`bork fmt`](#fmt) | Format source files |
| [`bork lsp`](#lsp) | Serve editor features over stdio |
| [`bork describe`](#describe) | Ask the compiler about the code at a position |
| [`bork emit`](#emit) | Print the Go code generated for a program |
| [`bork env`](#settings) | Show or save compiler settings |
| [`bork clean`](#the-compile-cache) | Remove the compiler's caches |
| [`bork deps`](#deps) | Manage Go dependencies of a module |
| `bork version` | Print the version |
| [`bork upgrade`](#upgrades) | Install the latest or requested compiler version |
| `bork completion` | Print a completion script for bash, zsh, fish, or PowerShell |

`bork <command> --help` lists a command's flags.

## New

```sh
bork new hello                              # a program and its first test
bork new --template cli greeter              # a program that reads arguments
bork new --template http service             # HTTP service and handler test
bork new --template lib library              # exported library function and test
bork new --module github.com/you/hello hello  # choose the module import path
```

`new` creates a fresh directory, including parents as needed, and refuses to replace an existing file or directory. The default module path is `example.com/<directory name>`; use `--module` for a publishable import path. No network access or Git initialization is needed. Every template includes `bork.mod`, a test, `.gitignore`, and a README with commands. Libraries have no `main` function and also include `go.mod` and `bork.sum` for dependency management.

The command prints the next steps. Run `bork check .`, `bork test .`, and `bork fmt --check .` inside the project. The HTTP service listens on `localhost:8080` and answers `/health`; its test calls the handler directly without opening a socket.

## Run, build, and install

```sh
bork run .                     # compile and run
bork run . -- input.txt -v     # arguments after -- go to the program
bork build .                   # write an executable named after the file or directory
bork build . -o server         # choose the output name
bork install .                 # build, then put the executable in BORKBIN
```

`bork install` names the executable after the source file or directory, creates `BORKBIN` if needed, and replaces an existing executable only after a successful build. Add `BORKBIN` to your `PATH` to run installed programs by name. See [settings](#settings) for where it is.

## Scripts

```sh
bork script hello.bork -- Ada   # top-level statements, with or without a shebang
```

A script with `#!/usr/bin/env -S bork script` on its first line can also run directly after `chmod +x hello.bork`. It uses the normal compile cache. See [scripts](language/scripts.md) for binding scope, inline Go dependencies and project rules.

## Check

```sh
bork check .            # report errors and warnings
bork check --watch .    # keep running, and check again when a file changes
bork check --json .     # one JSON object per diagnostic
```

`check` is the quickest way to get the compiler's answer, because it stops before generating and building Go.

It also warns about leftover development markers: `dbg(...)` calls and `todo()` placeholders. Warnings do not fail the check.

`--json` is meant for editors, scripts, and coding agents. Each diagnostic has a stable code, a position, and, where the compiler knows the repair, the text edits that apply it. `build`, `install`, and `test` accept `--json` too, and write the diagnostics to standard error. See [JSON diagnostics](diagnostics.md) and [watch mode](watch.md).

## Test

```sh
bork test .                    # run every test in the package
bork test --update .           # write new or changed snapshots instead of failing
bork test --parallel 8 .       # run up to 8 tests at a time
bork test --hermetic .         # fail tests that could reach the network without a mock
bork test --cases 500 .        # cases per property test (default 100)
bork test --seed 42 .          # repeat property tests with a given seed
bork test --auto-properties .  # also call trusted functions on generated arguments
```

The command exits with status 1 if a test fails. [Testing](language/testing.md) describes what tests can do.

## Fmt

```sh
bork fmt .            # format every .bork file under the directory, in place
bork fmt --check .    # list the files that would change, and exit 1 if there are any
```

`fmt` prints the paths it changed. It indents with two spaces and normalizes spacing and blank lines. It keeps your line breaks and comments. Hidden directories, `vendor`, and symbolic links are skipped.

## Describe

```sh
bork describe main.bork:3:9                     # the type, definition, methods, and known facts
bork describe main.bork:3:9 --where notEmpty    # is this fact proven here?
bork describe main.bork:3:9 --json
```

The position is `file:line:column`, counting from 1. `--where` takes a requirement written as in a `where` clause and answers whether the compiler can prove it for the selected value at that point. See [compiler code queries](describe.md).

## Emit

```sh
bork emit .    # print the generated Go
```

bork compiles by generating Go. `emit` shows that code, which is useful when you want to see what a feature costs at run time.

## Upgrades

```sh
bork upgrade                  # install the latest release
bork upgrade v0.4.0           # install a specific release
```

Upgrades run `go install github.com/GiGurra/bork/cmd/bork@<version>` in a temporary directory inside `BORKBIN`, then replace the installed executable after a successful build and version check. Publication is an atomic rename on Unix. Windows requires moving the previous executable aside first, and restores it if publication fails; a running compiler can leave a `.bork-old-*.exe` backup that you can remove after it exits. The command reports the running and newly installed compiler versions; the running compiler keeps its original version. Updates only happen when you request them.

Go must be on `PATH`. Downloading a release needs network access to your Go module proxy; offline installs work only when Go already has the required modules cached. Go's output explains install failures.

If `BORKBIN` is absent from `PATH`, the command warns you to add it. If you are running bork from another location, the command prints both locations: run the executable in `BORKBIN` to use the upgrade. A different bork earlier on `PATH` may still take precedence.

## Settings

Three settings control where the compiler keeps things:

| Setting | Default | Purpose |
| --- | --- | --- |
| `BORKCACHE` | the user cache directory plus `bork` (`~/.cache/bork` on Linux) | Where compiler results and staged builds are kept |
| `BORKBIN` | Go's `GOBIN`, or else `GOPATH/bin` (normally `~/go/bin`) | Where `bork install` and `bork upgrade` put executables |
| `BORK_CACHE` | `on` | Set to `off` to turn off the compile cache |

```sh
bork env                        # show every setting and where its value comes from
bork env BORKBIN                # show one
bork env --json                 # the same, as JSON
bork env -w BORKBIN=/opt/bin    # save a setting
bork env -u BORKBIN             # remove a saved setting
```

A value comes from the first of these that sets it: an environment variable, a saved setting, the default. An environment variable therefore still wins after `-w` or `-u`. `BORKCACHE` and `BORKBIN` must be absolute paths.

Saved settings live in `bork/env.json` under the user configuration directory (`~/.config/bork/env.json` on Linux).

## The compile cache

On Linux and macOS, `check`, `emit`, `build`, and `run` reuse earlier compiler results from `BORKCACHE` when nothing they depend on has changed. There is nothing to set up. A build still runs the Go compiler, which has its own cache, and `run` still runs the program each time.

Multiple `comptime` blocks share one evaluation program during compilation; their values are recomputed for each fresh compilation.

Some programs are compiled afresh every time for now: those that use `comptime`, typed literals that are checked at compile time such as `sql.SQL`, embedded files, facts proven by running predicates at compile time, or their own Go bindings and Go dependencies. Pure predicates with unchanged constant arguments can reuse their earlier answers while the rest of the program is checked again. Changed arguments or helpers are evaluated afresh. On other platforms the cache is not used.

The cache looks after itself. Entries that have not been used for five days are removed in the background. If the cache cannot be read or written, compilation carries on without it.

```sh
bork clean          # remove this compiler version's cached results, predicate answers and staged Go sources
bork clean --all    # also remove what other compiler versions left behind
```

`clean` never touches your project's files or Go's own caches. It waits for running builds to finish.

To turn the cache off, set `BORK_CACHE=off` for one command, or save it with `bork env -w BORK_CACHE=off`.

## Deps

A module pins dependencies with `require <module> <version>` lines in `bork.mod`
and checksums in `bork.sum`. The helper also writes generated `go.mod` so Go
module tools can discover its requirements. Commit all three files:

```sh
bork deps init                                   # initialize checksums and generated go.mod
bork deps get github.com/google/uuid@v1.6.0      # add or pin a dependency
bork deps get github.com/google/uuid@none        # remove it
bork deps download                               # download, fill checksums, and repair generated go.mod
bork deps migrate                                # convert legacy go-deps.mod/go-deps.sum
```

`bork.mod` is authoritative. Check/build diagnoses manual changes to generated
requirements with a hint to run `bork deps download`. Existing `go-deps.*`
projects remain supported; `migrate` converts them explicitly. See [calling Go](language/go-interop.md).

## Editor support

## lsp

`bork lsp` runs a Language Server Protocol server over stdio. Configure an LSP
client to launch that command; stdout contains protocol messages. The
[VS Code extension](../editors/vscode/README.md) starts it automatically and
provides highlighting, diagnostics, hover, definitions, references, completion,
symbols, formatting and compiler quick fixes.

Open documents are checked as packages with unsaved-buffer overlays. LSP
positions use zero-based lines and UTF-16 columns. Broken edits retain the last
successful navigation snapshot; hover and completion label it stale. References
cover open packages and their loaded imports. Rename supports local variables
and package-private functions, with current successful checks and conservative
collision rejection. Exported names, types and fields are not yet renameable.
