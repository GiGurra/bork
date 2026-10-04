# bork for VS Code

Language support for [bork](https://github.com/GiGurra/bork), a backend language that compiles to Go. The compiler checks immutable values, validation facts, effects, and resource lifetimes.

## Install

Download the `.vsix` from a [compiler GitHub Release](https://github.com/GiGurra/bork/releases), then install it with `code --install-extension <downloaded.vsix>` (or the editor's **Install from VSIX** command). The same package works in Cursor and VSCodium. A Marketplace account is not required.

Registry publishing is available separately: find **bork** (`gigurra.bork`) in the Extensions view, or open its [Marketplace page](https://marketplace.visualstudio.com/items?itemName=gigurra.bork). VSCodium and editors using Open VSX can use the [Open VSX page](https://open-vsx.org/extension/gigurra/bork). Registry listings become available after a maintainer publishes to those registries.

Install [Go](https://go.dev/dl/) 1.26 or later, then the compiler:

```sh
go install github.com/GiGurra/bork/cmd/bork@latest
bork new hello
code hello
```

VS Code 1.82 or later is required. The extension does not bundle a compiler. Set `bork.serverPath` if bork is not on the editor's PATH; run **bork: Restart Language Server** after changing it. For SSH, containers, and other remote workspaces, install the compiler and Go on the remote host.

## Features

The extension highlights `.bork` files, including embedded Go, and launches
`bork lsp` for diagnostics, types and proven facts on hover, go-to-definition,
references, completion, document symbols, formatting and suggested
compiler fixes. Install a current `bork` binary on PATH or set `bork.serverPath`
to its absolute path. Run **bork: Restart Language Server** after changing that setting.

Bork files format on save by default and use two-space indentation, matching
`bork fmt`. Override these defaults in your `[bork]` editor settings. The status
bar shows the running compiler version and language-server state when a bork file
is active. Click it to restart the server or show its output; both actions are
also available in the Command Palette. Restart reads the current `bork.serverPath`.

Unsaved files participate in package checking, including imported files and new
siblings. Changes are checked after a 150 ms pause; open/save checks run
immediately. Broken edits publish current errors while hover and completion use
the last successful check, marked **Stale**. Navigation positions may move while
a buffer is broken. Formatting uses the current text regardless of type errors.

Quick fixes add the effects a function needs and fill missing match arms with
destructuring patterns. Generated arms call `todo()` until you implement them.
Fixes use current compiler diagnostics, even before the first successful check,
and format the edited document with `bork fmt`.

References search the compiler graphs for open packages and their imports.
Rename currently supports local variables and package-private functions, requires
successful current checks, and refuses names already present in affected packages. Proposed edits are
checked in memory; unsupported references, such as some named where predicates,
can cause rename to be rejected.
It does not rename exported names, types or fields. Completion ranks visible locals before package symbols, keywords, and imports.
It offers checked fields and methods after a dot, exported names after a package
qualifier, record fields in literals and destructuring patterns, named arguments,
and match-arm patterns. Clients supporting LSP snippets also receive `fn`,
`match`, `type`, and `test` templates. Starting an exported name with two or more
characters offers auto-imports from standard packages, the current module, and resolved library dependencies;
accepting one adds the import and inserts a qualified name. Completion never downloads libraries; run `bork deps download` to resolve missing dependencies. Standard-library and
prelude definitions use virtual compiler paths and are not opened as disk files.

Scripts with a first-line shebang such as `#!/usr/bin/env -S bork script` are
checked as individual files, independently of neighboring `.bork` files. Top-level
statements support the same diagnostics, hover, definitions and rename as function
bodies. Synthetic `main` is hidden from symbols and completion. For automatic
editor script mode, include the shebang even when invoking `bork script` directly.

Language-server activation requires a trusted workspace because checking may
execute compile-time code or Go tools. Highlighting remains lexical and works
without a compiler. Files in virtual workspaces are not supported by the client.

## Lint warnings

Unused bindings, parameters and private declarations, proved predicate checks,
redundant boolean expressions and needless declared effects are reported as LSP
warnings. Use the lightbulb for safe fixes, including discarding an unused local
value while preserving its effects. Run `bork lint --json .` for the same warnings
outside the editor. Suppress an advisory rule with
`// lint:ignore lint.unused-binding reason` immediately before or on its line.
Exported APIs may intentionally reserve extra effects; suppress that warning when
keeping this headroom. Compiler errors are never suppressed.

## Run and test

Click **▶ run** above `main` to run its package with `bork run`, or at the top of
shebang scripts to use `bork script`. The program runs in a terminal with normal
stdin and output. **▶ run test** above a test block runs that test through the CLI.
Files are saved before execution, including unsaved dependencies.

The Test Explorer discovers test blocks through `bork lsp` when you open or edit
a file. Expand or refresh the explorer to discover all workspace `.bork` files.
Run individual tests, files, or all discovered tests; passing, failing and skipped
results appear beside their declarations. Compilation errors and program output
appear in the test output panel. Cancellation stops the running CLI process.

The extension uses `bork test --json --filter "test name" <package>` for execution.
Discovery uses the compiler parser and current editor buffers, including broken
edits; the extension does not parse bork or compile programs itself. Inference-rule
and opt-in automatic property tests remain available through the CLI. This wiring
uses the [VS Code Testing API](https://code.visualstudio.com/api/extension-guides/testing).

## Develop and package

Install dependencies and open an extension development window from the repo root:

```sh
npm ci --prefix editors/vscode
code --extensionDevelopmentPath="$PWD/editors/vscode" "$PWD/examples/hello/main.bork"
```

To create and install a local VSIX (Node.js 22 or later):

```sh
npm run package --prefix editors/vscode
code --install-extension editors/vscode/bork-0.1.0.vsix
```

The package contains the language client; it locates the separately installed
compiler rather than bundling platform binaries. Publishing is separate; maintainers use the [release guide](PUBLISHING.md). The client follows the official [VS Code language server guide](https://code.visualstudio.com/api/language-extensions/language-server-extension-guide),
and packaging uses [vsce](https://code.visualstudio.com/api/working-with-extensions/publishing-extension).

Other TextMate-compatible editors can use
[`syntaxes/bork.tmLanguage.json`](syntaxes/bork.tmLanguage.json), register the
`source.bork` scope for `.bork`, and provide a `source.go` grammar for embedded Go.
Any LSP client can launch `bork lsp` over stdio with full-document synchronization.

Run the grammar, client, and release-guard tests:

```sh
npm test --prefix editors/vscode
```

Grammar tests use VS Code's TextMate and Oniguruma engines. They check scopes,
interpolation boundaries, embedded Go nesting, and all cases and examples.
To check integration with a real Go grammar, set `BORK_GO_GRAMMAR` to its absolute
path; JSON and plist grammars are supported. Client tests check launch and cleanup
without requiring a graphical VS Code process.

## Debugging

Choose **Run and Debug → Debug bork** to launch the current package. Breakpoints, stepping and locals use compiler source mappings. If the optional debugger is missing, the extension offers to install its pinned version through `bork debug setup`. See [debugging](https://github.com/GiGurra/bork/blob/main/docs/debugging.md) for launch configurations and current limits, including native Go representations for unions and options.
