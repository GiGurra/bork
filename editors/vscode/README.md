# bork for VS Code

Language support for [bork](https://github.com/GiGurra/bork), a backend language that compiles to Go. The compiler checks immutable values, validation facts, effects, and resource lifetimes.

## Install

Find **bork** (`gigurra.bork`) in the Extensions view, or open its [Marketplace page](https://marketplace.visualstudio.com/items?itemName=gigurra.bork). VSCodium and editors using Open VSX can use the [Open VSX page](https://open-vsx.org/extension/gigurra/bork). Registry listings become available after the first release.

Install [Go](https://go.dev/dl/) 1.26 or later, then the compiler:

```sh
go install github.com/GiGurra/bork/cmd/bork@latest
bork new hello
code hello
```

VS Code 1.82 or later is required. The extension does not bundle a compiler. Set `bork.serverPath` if bork is not on the editor's PATH; restart the extension after changing it. For SSH, containers, and other remote workspaces, install the compiler and Go on the remote host.

## Features

The extension highlights `.bork` files, including embedded Go, and launches
`bork lsp` for diagnostics, types and proven facts on hover, go-to-definition,
references, conservative completion, document symbols, formatting and suggested
compiler fixes. Install a current `bork` binary on PATH or set `bork.serverPath`
to its absolute path. Restart the extension after changing that setting.

Unsaved files participate in package checking, including imported files and new
siblings. Changes are checked after a 150 ms pause; open/save checks run
immediately. Broken edits publish current errors while hover and completion use
the last successful check, marked **Stale**. Navigation positions may move while
a buffer is broken. Formatting uses the current text regardless of type errors.

References search the compiler graphs for open packages and their imports.
Rename currently supports local variables and package-private functions, requires
successful current checks, and refuses names already present in affected packages. Proposed edits are
checked in memory; unsupported references, such as some named where predicates,
can cause rename to be rejected.
It does not rename exported names, types or fields. Completion offers package
functions/types/values and visible methods after a dot. Standard-library and
prelude definitions use virtual compiler paths and are not opened as disk files.

Scripts with a first-line shebang such as `#!/usr/bin/env -S bork script` are
checked as individual files, independently of neighboring `.bork` files. Top-level
statements support the same diagnostics, hover, definitions and rename as function
bodies. Synthetic `main` is hidden from symbols and completion. For automatic
editor script mode, include the shebang even when invoking `bork script` directly.

Language-server activation requires a trusted workspace because checking may
execute compile-time code or Go tools. Highlighting remains lexical and works
without a compiler. Files in virtual workspaces are not supported by the client.

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
