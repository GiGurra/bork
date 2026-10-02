# bork syntax highlighting

This local VS Code extension recognizes `.bork` files. It highlights declarations,
keywords, comments, numbers, runes, strings, `$name` and `${...}` interpolation,
records and maps, generic arguments, and `|>` pipelines. `unsafe go { ... }`
bodies use VS Code's built-in Go grammar, including nested braces and Go strings
and comments. It provides comment toggling and bracket/quote pairing.

From the repository root, open an extension development window:

```sh
code --extensionDevelopmentPath="$PWD/editors/vscode" "$PWD/examples/hello/main.bork"
```

For a persistent local installation on Linux or macOS, copy this directory into
VS Code's extensions directory and reload VS Code:

```sh
mkdir -p "$HOME/.vscode/extensions"
cp -R editors/vscode "$HOME/.vscode/extensions/gigurra.bork-0.0.1"
```

On Windows the directory is `%USERPROFILE%\.vscode\extensions`. VS Code Insiders
uses `.vscode-insiders` instead of `.vscode`. Select **bork** in the language mode
menu if a file was already open. No Go compiler or extension activation code is
needed for highlighting. This is a local extension, not a Marketplace release.

Other TextMate-compatible editors can use
[`syntaxes/bork.tmLanguage.json`](syntaxes/bork.tmLanguage.json), register the
`source.bork` scope for `.bork`, and provide a `source.go` grammar for embedded Go.
Highlighting is lexical; contextual keywords can also be used as names in bork,
so some such names will use keyword colors. This extension does not provide
completion, diagnostics or a language server.

Run the grammar tests from the repository root (Node.js 18 or later):

```sh
npm ci --prefix editors/vscode
npm test --prefix editors/vscode
```

Tests use VS Code's TextMate engine and Oniguruma regex engine. They check scopes,
interpolation boundaries, embedded Go nesting, and tokenization of all cases and
examples. A small Go grammar stand-in keeps the tests self-contained. To also
check integration with a real Go grammar, set `BORK_GO_GRAMMAR` to its absolute
path when running the tests; JSON and plist TextMate grammars are supported.
