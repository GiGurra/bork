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
| [`bork lint`](#lint) | Report advisory compiler warnings |
| [`bork test`](#test) | Run a package's tests |
| [`bork fmt`](#fmt) | Format source files |
| [`bork lsp`](#lsp) | Serve editor features over stdio |
| [`bork editor install vscode`](#editor-install-vscode) | Install the verified release extension |
| [`bork doc`](#doc) | Render checked public package APIs |
| [`bork describe`](#describe) | Ask the compiler about the code at a position |
| [`bork emit`](#emit) | Print the Go code generated for a program |
| [`bork env`](#settings) | Show or save compiler settings |
| [`bork clean`](#the-compile-cache) | Remove the compiler's caches |
| [`bork deps`](#deps) | Manage Go and bork dependencies of a module |
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

## `bork debug`

`bork debug build [path] -o program` builds with bork source locations, disables optimization and inlining, and retains generated Go in `program.bork-debug/`. `bork debug setup` installs the pinned optional debugger into BORKCACHE. `bork debug dap --listen 127.0.0.1:0` launches its loopback DAP server. See [debugging](debugging.md) for editor setup and runtime limitations.

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

Non-exhaustive matches include a structured fix in `check --json`. It adds
missing cases with destructuring patterns and `todo()` bodies. Effect errors
also include fixes for the function's `uses` clause. The language server exposes
these as quick fixes and formats the result; replace generated `todo()` calls
with the intended behavior.

It also warns about leftover development markers: `dbg(...)` calls and `todo()` placeholders. Warnings do not fail the check.

`--json` is meant for editors, scripts, and coding agents. Each diagnostic has a stable code, a position, and, where the compiler knows the repair, the text edits that apply it. `build`, `install`, and `test` accept `--json` too, and write the diagnostics to standard error. See [JSON diagnostics](diagnostics.md) and [watch mode](watch.md).

## Test

```sh
bork test .                    # run every test in the package
bork test --json .             # JSON Lines results on stdout; program output on stderr
bork test --filter "adds" .    # run the test with this exact name
bork test --update .           # write new or changed snapshots instead of failing
bork test --parallel 8 .       # run up to 8 tests at a time
bork test --hermetic .         # fail tests that could reach the network without a mock
bork test --cases 500 .        # cases per property test (default 100)
bork test --seed 42 .          # repeat property tests with a given seed
bork test --auto-properties .  # also call trusted functions on generated arguments
```

With `--json`, each result has `action` (`pass`, `fail`, or `skip`), `name`, `file`, and `line`, plus `message` for failures or skipped tests with a reason. Results appear after execution; compiler diagnostics remain JSON Lines on stderr. `--filter` selects an exact `test` declaration name (excluding generated rule and automatic property tests) and exits with status 1 if it matches nothing.

The command exits with status 1 if a test fails. [Testing](language/testing.md) describes what tests can do.

## Lint

```sh
bork lint .          # advisory warnings; exit 1 for compiler errors
bork lint --json .   # the same diagnostic schema as check --json
```

Lint checks unused local bindings, parameters and unreferenced private functions,
types and package values; predicate checks already proved by the compiler;
redundant boolean literals; and declared effects not performed by a function.
Warnings are also shown by the language server, with quick fixes when a change
can be made safely. Private references inside raw Go are opaque, so declaration
warnings are skipped for packages containing unsafe Go bodies. Removing parameter
names could break labeled calls, so unused parameter warnings have no rename fix.

Suppress an advisory rule on the same line or the next line with
`// lint:ignore lint.unused-binding reason`. Separate multiple rule codes with
commas, or use `all`. The comment only suppresses lint warnings. Exported functions
may reserve effects as API headroom; use a suppression before that declaration
when the extra effects are intentional.

Shadowing, unreachable match arms, unused imports and needless effects on private
functions remain compiler errors, including their existing fixes. Lint does not
weaken these guarantees. Warnings alone leave the command's exit status at 0.
The linter uses the compiler's parsed and checked trees, and proven-check warnings
require a proof without extra compile-time evaluation.

## Fmt

```sh
bork fmt .            # format every .bork file under the directory, in place
bork fmt --check .    # list the files that would change, and exit 1 if there are any
```

`fmt` prints the paths it changed. It indents with two spaces and normalizes spacing and blank lines. It keeps your line breaks and comments. Hidden directories, `vendor`, and symbolic links are skipped.

## Doc

```sh
bork doc                              # this package, as Markdown
bork doc ./lib --all > API.md          # packages below lib, in this module
bork doc bork/http                     # a standard package
bork doc example.com/library/api       # a pinned library package
bork doc example.com/library --all --html > api.html
```

A `.bork` file argument documents its whole package. `--all` walks package
subdirectories in the same module, skipping hidden directories, `vendor`,
and nested modules; it also works for library modules that have no root
package. Standard packages take one package argument without `--all`.

The compiler checks each package before writing any output. Signatures retain
facts and `where` clauses, effects (`uses`), ambient requirements (`needs`),
generic bounds, defaults, and construction privacy. Computed defaults show `<computed>` without exposing implementation code.
Private declarations,
private variants, tests, and implementation bodies are omitted. Adjacent
whole-line `//` comments document declarations; leading comments separated
from the first declaration by a blank line document the package. Comments
before imports also document the package. Hover and `bork doc` share the same
comment renderer, including fenced examples and escaped HTML.

Like `bork check`, documentation follows the project minimum compiler and
`BORKTOOLCHAIN` selection. Library documentation uses the versions already selected by the consumer's
`bork.mod` and verified against `bork.sum`. It reads cached dependencies and
never downloads missing modules or Go toolchains. Run `bork deps download`
first when needed. Standard APIs come from the running compiler. Output
contains relative source labels and no guessed repository links. HTML is a
standalone page with a table of contents and no external assets.

Library authors can commit `API.md` or `api.html` next to their source and
regenerate it when their API changes. This supplies documentation for `.bork`
APIs that Go documentation tools cannot render.

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

Compiler settings control storage and toolchain selection:

| Setting | Default | Purpose |
| --- | --- | --- |
| `BORKCACHE` | the user cache directory plus `bork` (`~/.cache/bork` on Linux) | Where compiler results and staged builds are kept |
| `BORKBIN` | Go's `GOBIN`, or else `GOPATH/bin` (normally `~/go/bin`) | Where `bork install` and `bork upgrade` put executables |
| `BORK_CACHE` | `on` | Set to `off` to turn off the compile cache |
| `BORKTOOLCHAIN` | `auto` | Automatically satisfy project compiler requirements; `local` disables switching, or an exact version such as `v0.4.2` selects that compiler |
| `BORKUPDATECHECK` | `on` | Quiet daily update notice in interactive commands; set `off` to disable |
| `GOTOOLCHAIN` | Go's effective selection policy | Read-only here; inherited from the process or `go env -w`, with the Bork minimum applied |
| `GOVERSION`, `GOROOT` | the selected Go SDK | Read-only; used for Go compilation and cache identity |
| `BORKVERSION` | the selected compiler's version | Read-only; also reports why that compiler was selected |

```sh
bork env                        # show every setting and where its value comes from
bork env BORKBIN                # show one
bork env --json                 # the same, as JSON
bork env -w BORKBIN=/opt/bin    # save a setting
bork env -u BORKBIN             # remove a saved setting
```

A value comes from the first of these that sets it: an environment variable, a saved setting, the default. An environment variable therefore still wins after `-w` or `-u`. `BORKCACHE` and `BORKBIN` must be absolute paths.

Saved settings live in `bork/env.json` under the user configuration directory (`~/.config/bork/env.json` on Linux).

## Compiler versions

A project can declare a minimum compiler version after its module line:

```text
module example.com/shop
bork 0.4
```

With the default `BORKTOOLCHAIN=auto`, a compiler older than the requirement downloads and builds a suitable compiler through your Go module proxy, then runs it with the same arguments, working directory and input/output. `bork 0.4` resolves to the latest published `v0.4.x` when first downloaded; `bork 0.4.2` requests that exact release if switching is necessary. A newer installed compiler already satisfies this minimum. Development builds from a checkout remain local in auto mode.

Compiler installations live in `BORKCACHE/toolchains/<os>-<arch>/<version>`. Later runs reuse them, including offline. Minor-version resolutions are cached until `bork clean --all`, so a project keeps its first resolved patch while the cache exists. Pin an exact version with `BORKTOOLCHAIN=v0.4.2` for reproducible builds across machines. An override below the project's minimum is an error. `BORKTOOLCHAIN=local` uses the running compiler and errors when it is too old.

The nearest `bork.mod` to the command's file or directory supplies the requirement. `bork env`, `bork version`, and `bork lsp` use the current directory; `bork describe` uses the source file in its position argument. `bork env BORKVERSION` and `bork version` show the selected compiler and its reason. Updating settings (`env -w` or `env -u`), formatting, cleaning, project creation and compiler upgrades use the local executable so you can repair settings without downloading a compiler.

Switching needs Go on `PATH` to install an uncached compiler. Download failures report how to check network and module-proxy access. The installed executable in `BORKBIN` stays unchanged, and different compiler images use separate compile-cache namespaces. `bork clean` keeps downloaded compilers; `bork clean --all` also removes them, waiting for active installations and selected compiler processes to finish.

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

For a bork library, pass its module path (`github.com/acme/lib@v1.0.0`) and
import packages by module path plus directory. The helper prints newly selected
libraries containing unsafe Go packages, including transitive ones. Their own
`unsafe` grants are trusted without a consumer approval step. See
[library dependencies](language/packages.md#library-dependencies).

`bork.mod` is authoritative. Check/build diagnoses manual changes to generated
requirements with a hint to run `bork deps download`. Existing `go-deps.*`
projects remain supported; `migrate` converts them explicitly. See [calling Go](language/go-interop.md).

## Editor support

## editor install vscode

```sh
bork editor install vscode
bork editor install vscode --editor cursor
bork editor install vscode --editor codium
```

Install the VSIX from the GitHub Release matching the running compiler, falling
back to the latest release when that tag or its VSIX is absent. Development
builds use the latest release. The installer checks the release's SHA-256
checksum before invoking the editor and reports the release and editor used.
A failed checksum stops installation. Internet access is required.

The default CLI is the first available `code`, `cursor`, or `codium` on PATH.
`--editor` accepts a command or executable path. Enable your editor's shell
command if it is missing. Installation uses `--force` to refresh the extension
even when its package version has not changed between compiler releases.
After `bork upgrade`, an installed Bork extension produces a refresh command;
run that command explicitly to update it. The extension launches `bork lsp`.

See [other editors](editors.md) for Neovim, Helix, Emacs and Zed integration.

## lsp

`bork lsp` runs a Language Server Protocol server over stdio. Configure an LSP
client to launch that command; stdout contains protocol messages. The
[VS Code extension](../editors/vscode/README.md) starts it automatically and
provides highlighting, diagnostics, hover, definitions, references, completion,
symbols, formatting and compiler quick fixes. Bork files format on save with
two-space indentation by default; `[bork]` editor settings can override these
defaults. The status bar shows the compiler version and server state. Click it
to restart the language server or show its output.

Open documents are checked as packages with unsaved-buffer overlays. LSP
positions use zero-based lines and UTF-16 columns. Broken edits retain the last
successful navigation snapshot; hover and completion label it stale. References
and rename cover local workspace packages and their closed importers. Rename
supports locals, parameters, exported functions, types, record fields, variants,
predicates and package values. It checks proposed edits in memory and preserves
checked bindings, including shorthand patterns and facts. All local packages
must check; dependency sources are read-only. Raw Go bodies in affected packages
prevent rename. Classes, instances and ambient values are not yet renameable.
Completion offers visible symbols, fields, named arguments and match patterns,
plus snippets in supporting clients. Auto-imports search standard packages and
the current module after two name characters.


## Update notices

Interactive successful `check`, `build`, `install`, `fmt`, `new` and `version`
commands can print a quiet notice when a newer compiler is available. A detached
worker checks the configured Go module proxy at most once per UTC day and saves
its result under `BORKCACHE/updates`. Network requests never delay the command;
its result may produce a notice on a later command that day. Notices print at
most once per day for the running compiler version.

```sh
bork env -w BORKUPDATECHECK=off
bork env BORKUPDATECHECK
```

JSON, machine output, program execution, LSP sessions, redirected output and
any nonempty `CI` environment variable suppress both checks and notices.
Development compilers skip checks. Offline failures are silent; only a successful
check from the current UTC day can produce a notice. `GOPROXY=off` or `direct`
does not run an HTTP check. Updating remains explicit with `bork upgrade`.

## Go toolchains

Bork requires Go 1.26.0 or newer for generated programs and evaluators. With Go
1.21+ installed and Go's default `GOTOOLCHAIN=auto`, an older installed SDK is
raised to `go1.26.0+auto`; Go finds that SDK on PATH or downloads it. Newer SDKs
remain selected. `+path` policies search PATH without downloading. Explicit
`local` or exact-version policies stay explicit and report an error if the
selected version is too old. Process settings and saved `go env -w` settings
are both respected.

```sh
bork env GOTOOLCHAIN GOVERSION GOROOT
go env -w GOTOOLCHAIN=auto
GOTOOLCHAIN=local bork build .
```

Go settings are managed by Go, rather than `bork env -w`. Toolchain downloads
need network access to the Go module proxy and checksum database; once cached,
they can be reused offline. Download errors include Go's output. The compile
cache includes the selected SDK's version, root and executable identity.
Switched or unusual toolchains conservatively reload metadata when the fast
cache inventory cannot certify their configuration. See
[Go's toolchain selection](https://go.dev/doc/toolchain).
